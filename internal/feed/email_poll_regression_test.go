package feed

// Regression tests for the two defects that made email feeds look broken to a
// user who had actually received mail.
//
// 1. A per-feed poll never drained the mailbox. An email feed's Fetch only reads
//    the local staging table, and only PollAll ran the sync. The UI's refresh
//    button and the auto-fetch after creating a feed both call PollFeed, so they
//    always reported zero new items — the user's mail sat in AgentMail and the
//    feed stayed empty until the next 15-minute cycle.
//
// 2. The sync's HTTP traffic was left in the shared metrics transport, so the
//    FIRST feed polled in a cycle reported it as its own. Measured on a live
//    instance: a ~30 KB RSS fetch showed bytes=1252698 / avg_ms=514 while both
//    email feeds showed bytes=0.

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"digesterr/internal/metrics"
	"digesterr/internal/store"
)

const syncListBody = `{"messages":[{"message_id":"<m1@test>","timestamp":"2026-09-16T19:02:52.000Z","subject":"Money Stuff","preview":"p","from":"Bloomberg <subscriptions@mail.bloomberg.com>","to":["name+mattlevine@agentmail.to"]}],"count":1}`

const syncGetBody = `{"message_id":"<m1@test>","timestamp":"2026-09-16T19:02:52.000Z","subject":"Money Stuff","text":"body","html":"<p>body</p><a href=\"https://example.com/story\">Read</a>"}`

// newEmailPoller builds a poller with the email feed type registered and the
// sync pointed at the stub server, mirroring the server wiring.
func newEmailPoller(t *testing.T, q *store.Queries, srv *httptest.Server) (*Poller, *EmailSync) {
	t.Helper()
	sync := newSyncFor(t, q, srv)
	if sync == nil {
		t.Fatal("sync not configured")
	}
	p := NewPoller(q)
	p.SetEmailSync(sync)
	return p, sync
}

// TestPollFeedDrainsMailboxForEmailFeed is the regression for defect 1: polling
// a single email feed must read the mailbox, not just the staging table.
func TestPollFeedDrainsMailboxForEmailFeed(t *testing.T) {
	db := openSchemaDB(t)
	q := store.New(db)
	ctx := context.Background()

	srv, calls := syncTestServer(t, syncListBody, func(string) string { return syncGetBody })
	p, _ := newEmailPoller(t, q, srv)

	seedEmailFeed(t, q, "name+mattlevine@agentmail.to")

	feedRow := loadEmailFeed(t, db, "name+mattlevine@agentmail.to")

	if err := p.PollFeed(ctx, feedRow); err != nil {
		t.Fatalf("PollFeed: %v", err)
	}

	if *calls == 0 {
		t.Fatal("PollFeed made no mailbox requests: an email feed's Fetch reads only " +
			"the staging table, so the mailbox must be drained first")
	}

	var items int
	if err := db.QueryRow(`SELECT count(*) FROM items WHERE feed_id = ?`, feedRow.ID).Scan(&items); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if items != 1 {
		t.Errorf("items for the email feed = %d, want 1: the message was in the mailbox "+
			"but a per-feed poll did not surface it", items)
	}
}

// TestPollAllRecordsSyncTrafficSeparately is the regression for defect 2: the
// sync must consume its own counters and report them as `email_sync`, leaving
// nothing for the first-polled feed to claim.
func TestPollAllRecordsSyncTrafficSeparately(t *testing.T) {
	db := openSchemaDB(t)
	q := store.New(db)
	ctx := context.Background()

	srv, _ := syncTestServer(t, syncListBody, func(string) string { return syncGetBody })

	transport := metrics.NewTransport(http.DefaultTransport)
	sync := newSyncFor(t, q, srv)
	sync.setTransport(transport)

	p := NewPoller(q)
	p.SetTransport(transport)
	p.SetEmailSync(sync)

	type rec struct {
		reason string
		name   string
		bytes  int64
		reqs   int
	}
	var records []rec
	p.SetRecordFunc(func(r Record) {
		records = append(records, rec{r.Reason, r.FeedName, r.Bytes, r.Requests})
	})

	seedEmailFeed(t, q, "name+mattlevine@agentmail.to")

	p.PollAll(ctx)

	var syncRec *rec
	for i := range records {
		if records[i].reason == "email_sync" {
			syncRec = &records[i]
		}
	}
	if syncRec == nil {
		t.Fatalf("no email_sync record emitted; records=%+v. The sync's traffic is "+
			"attributed to whichever feed is polled first", records)
	}
	if syncRec.reqs == 0 {
		t.Error("email_sync recorded 0 requests, but the sync issued HTTP calls")
	}

	// Nothing may be left in the transport for the next feed to claim.
	if b, r := transport.Consume(); b != 0 || r != 0 {
		t.Errorf("sync left bytes=%d reqs=%d in the shared transport; the first feed "+
			"polled will report them as its own", b, r)
	}
}

// TestSyncMetricsAreRecordedOncePerPollAll keeps a single drain per cycle: the
// sync must not run once per email feed.
func TestSyncMetricsAreRecordedOncePerPollAll(t *testing.T) {
	db := openSchemaDB(t)
	q := store.New(db)
	ctx := context.Background()

	srv, calls := syncTestServer(t, syncListBody, func(string) string { return syncGetBody })
	p, _ := newEmailPoller(t, q, srv)

	seedEmailFeed(t, q, "name+one@agentmail.to")
	seedEmailFeed(t, q, "name+two@agentmail.to")

	p.PollAll(ctx)

	// One list + one body fetch = 2 requests, regardless of feed count.
	if *calls > 2 {
		t.Errorf("mailbox contacted %d times for 2 email feeds in one cycle, want <= 2 "+
			"(one drain per cycle)", *calls)
	}
}

// ---- helpers -------------------------------------------------------------

func seedEmailFeed(t *testing.T, q *store.Queries, address string) int64 {
	t.Helper()
	ctx := context.Background()
	user, err := q.CreateUser(ctx, store.CreateUserParams{Email: address + ".owner@test", PasswordHash: "x", ApiKey: "k-" + address})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	f, err := q.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID,
		Type:   "email",
		Name:   "Matt Levine - Money Stuff",
		Url:    address,
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	return f.ID
}

func loadEmailFeed(t *testing.T, db *sql.DB, address string) store.Feed {
	t.Helper()
	feeds, err := store.New(db).ListActiveFeeds(context.Background())
	if err != nil {
		t.Fatalf("list feeds: %v", err)
	}
	for _, f := range feeds {
		if f.Url == address {
			return f
		}
	}
	t.Fatalf("feed %q not found", address)
	return store.Feed{}
}
