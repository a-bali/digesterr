package feed

// Per-user AgentMail accounts: the poller drains each DISTINCT inbox once per
// cycle, resolves credentials per feed owner, and isolates feeds by inbox.
//
// These are the tests for the consequences of moving AgentMail config from the
// instance to the account. The failure modes they guard are all silent:
//
//   - a feed owned by an account with no credentials must not stop the other
//     accounts from polling;
//   - the drain must happen once per MAILBOX, not once per feed (the N-fold
//     fan-out that caused the production Twitter 429s was exactly this shape);
//   - and a feed must read only its own owner's staged mail. Before per-user
//     credentials there was one inbox, so ListEmailMessagesByRecipient's
//     inbox_id filter was a formality; now it is the isolation boundary.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"digesterr/internal/store"
)

// fakeMailbox describes one inbox served by a single stub AgentMail server. The
// same server answers for every inbox (the real API routes by the inbox_id in
// the path), so the map is keyed by that id.
type fakeMailbox struct {
	inbox   string
	address string // the plus-address a message is delivered to
	subject string
}

// fakeAgentMailMulti serves several inboxes from one server and records how many
// requests each inbox received, so "once per mailbox" is measurable per inbox.
func fakeAgentMailMulti(t *testing.T, boxes []fakeMailbox) (*httptest.Server, func(inbox string) int) {
	t.Helper()
	var mu sync.Mutex
	calls := map[string]int{}

	byInbox := map[string]fakeMailbox{}
	for _, b := range boxes {
		byInbox[b.inbox] = b
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /inboxes/<id>/messages and /inboxes/<id>/messages/<msgid>
		rest := strings.TrimPrefix(r.URL.Path, "/inboxes/")
		inbox, tail, _ := strings.Cut(rest, "/")
		mu.Lock()
		calls[inbox]++
		mu.Unlock()

		box, known := byInbox[inbox]
		w.Header().Set("Content-Type", "application/json")
		if !known {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":"inbox not found"}`)
			return
		}
		if strings.HasPrefix(tail, "messages/") {
			fmt.Fprintf(w, `{"message_id":"<%s@test>","timestamp":"2026-09-17T08:00:00.000Z",
			  "from":"news@example.com","to":[%q],"subject":%q,"text":"body","html":"<p>body</p>"}`,
				inbox, box.address, box.subject)
			return
		}
		fmt.Fprintf(w, `{"count":1,"next_page_token":"","messages":[{
		  "inbox_id":%q,"message_id":"<%s@test>","thread_id":"t","labels":["received"],
		  "timestamp":"2026-09-17T08:00:00.000Z","from":"news@example.com","to":[%q],
		  "subject":%q,"preview":"p","size":10,"headers":{}}]}`,
			inbox, inbox, box.address, box.subject)
	}))
	t.Cleanup(srv.Close)

	count := func(inbox string) int {
		mu.Lock()
		defer mu.Unlock()
		return calls[inbox]
	}
	return srv, count
}

// seedUserWithEmailConfig creates an account and stores its AgentMail
// credentials the way the settings dialog does.
func seedUserWithEmailConfig(t *testing.T, q *store.Queries, email, inbox, token string) store.User {
	t.Helper()
	ctx := context.Background()
	u, err := q.CreateUser(ctx, store.CreateUserParams{
		Email: email, PasswordHash: "x", ApiKey: "k-" + email,
	})
	if err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}
	if inbox == "" && token == "" {
		return u
	}
	u, err = q.UpdateUserEmailConfig(ctx, store.UpdateUserEmailConfigParams{
		EmailInbox: inbox, EmailApiKey: token, ID: u.ID,
	})
	if err != nil {
		t.Fatalf("set email config for %s: %v", email, err)
	}
	return u
}

func seedEmailFeedFor(t *testing.T, q *store.Queries, userID int64, address, name string) store.Feed {
	t.Helper()
	f, err := q.CreateFeed(context.Background(), store.CreateFeedParams{
		UserID: userID, Type: "email", Name: name, Url: address, Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create email feed %s: %v", address, err)
	}
	return f
}

// pollerWithInboxes points every account's client at the stub server.
func pollerWithInboxes(q *store.Queries, srv *httptest.Server) *Poller {
	p := NewPoller(q)
	p.SetEmailClientFactory(func(acct EmailAccount) *AgentMailClient {
		c := NewAgentMailClient(acct.APIKey, acct.InboxID)
		if c == nil {
			return nil
		}
		c.baseURL = srv.URL
		return c
	})
	return p
}

// TestPollAllDrainsEachInboxOnceAcrossAccounts: two accounts, two mailboxes, two
// feeds. Each mailbox must be contacted exactly once for the cycle (one list +
// one body fetch), and each feed must receive only its own message.
func TestPollAllDrainsEachInboxOnceAcrossAccounts(t *testing.T) {
	db := openSchemaDB(t)
	q := store.New(db)
	ctx := context.Background()

	// BOTH accounts use the SAME suffix, so the recipient filter cannot separate
	// their mail: only the fetcher's inbox_id can. That is the isolation boundary
	// per-user credentials introduced, and this is what must fail if it breaks.
	boxes := []fakeMailbox{
		{inbox: "alice-inbox@agentmail.to", address: "alice-inbox+news@agentmail.to", subject: "Alice News"},
		{inbox: "bob-inbox@agentmail.to", address: "bob-inbox+news@agentmail.to", subject: "Bob Sport"},
	}
	srv, calls := fakeAgentMailMulti(t, boxes)

	alice := seedUserWithEmailConfig(t, q, "alice@test.local", boxes[0].inbox, "alice-token")
	bob := seedUserWithEmailConfig(t, q, "bob@test.local", boxes[1].inbox, "bob-token")
	aliceFeed := seedEmailFeedFor(t, q, alice.ID, boxes[0].address, "Alice News")
	bobFeed := seedEmailFeedFor(t, q, bob.ID, boxes[1].address, "Bob Sport")

	p := pollerWithInboxes(q, srv)
	p.PollAll(ctx)

	if got := calls(boxes[0].inbox); got != 2 {
		t.Errorf("alice's mailbox saw %d requests, want exactly 2 (1 list + 1 get)", got)
	}
	if got := calls(boxes[1].inbox); got != 2 {
		t.Errorf("bob's mailbox saw %d requests, want exactly 2 (1 list + 1 get)", got)
	}

	// Each feed must hold exactly its own owner's message — the per-owner fetcher
	// is what makes an inbox a real boundary now that accounts have their own.
	for _, tc := range []struct {
		feed  store.Feed
		title string
		other string
	}{
		{aliceFeed, "Alice News", "Bob Sport"},
		{bobFeed, "Bob Sport", "Alice News"},
	} {
		var titles []string
		rows, err := db.Query(`SELECT title FROM items WHERE feed_id = ?`, tc.feed.ID)
		if err != nil {
			t.Fatalf("query items: %v", err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatalf("scan: %v", err)
			}
			titles = append(titles, s)
		}
		rows.Close()
		if len(titles) != 1 || titles[0] != tc.title {
			t.Errorf("feed %q items = %v, want exactly [%q]", tc.feed.Name, titles, tc.title)
		}
		for _, s := range titles {
			if s == tc.other {
				t.Errorf("feed %q ingested the OTHER account's mail (%q): the fetcher is not "+
					"scoped to its owner's inbox (both accounts share the suffix, so only "+
					"inbox_id separates them)", tc.feed.Name, s)
			}
		}
	}
}

// TestPollAllSkipsFeedsOfUnconfiguredAccounts: an account with email feeds but no
// credentials must not stop the rest of the instance from polling, and must not
// issue any request of its own.
func TestPollAllSkipsFeedsOfUnconfiguredAccounts(t *testing.T) {
	db := openSchemaDB(t)
	q := store.New(db)
	ctx := context.Background()

	boxes := []fakeMailbox{
		{inbox: "configured@agentmail.to", address: "configured+news@agentmail.to", subject: "Configured"},
	}
	srv, _ := fakeAgentMailMulti(t, boxes)

	good := seedUserWithEmailConfig(t, q, "good@test.local", boxes[0].inbox, "token")
	goodFeed := seedEmailFeedFor(t, q, good.ID, boxes[0].address, "Good")

	// No credentials at all: this is the state after "delete the email
	// configuration" while the feed rows still exist (the plan's decision 2).
	bare := seedUserWithEmailConfig(t, q, "bare@test.local", "", "")
	bareFeed := seedEmailFeedFor(t, q, bare.ID, "somewhere+orphan@agentmail.to", "Orphan")

	p := pollerWithInboxes(q, srv)
	p.PollAll(ctx)

	// The configured account still polled...
	var goodItems int
	if err := db.QueryRow(`SELECT count(*) FROM items WHERE feed_id = ?`, goodFeed.ID).Scan(&goodItems); err != nil {
		t.Fatalf("count: %v", err)
	}
	if goodItems != 1 {
		t.Errorf("configured account's feed has %d items, want 1 — one unconfigured "+
			"account must not stop the others", goodItems)
	}

	// ...and the orphan feed recorded a visible failure rather than vanishing.
	var status, lastErr string
	if err := db.QueryRow(`SELECT status, last_error FROM feeds WHERE id = ?`, bareFeed.ID).Scan(&status, &lastErr); err != nil {
		t.Fatalf("read orphan feed: %v", err)
	}
	if status != "error" || lastErr == "" {
		t.Errorf("orphan email feed status=%q last_error=%q, want status=error with a message: "+
			"a feed whose account has no credentials must show the failure in the sidebar", status, lastErr)
	}
}

// TestPollFeedResolvesOwnerCredentialsAndDrains: the refresh button path. A
// per-feed poll must drain that feed owner's mailbox, not a stale global one.
func TestPollFeedResolvesOwnerCredentialsAndDrains(t *testing.T) {
	db := openSchemaDB(t)
	q := store.New(db)
	ctx := context.Background()

	boxes := []fakeMailbox{
		{inbox: "mine@agentmail.to", address: "mine+news@agentmail.to", subject: "Mine"},
	}
	srv, calls := fakeAgentMailMulti(t, boxes)

	u := seedUserWithEmailConfig(t, q, "per-feed@test.local", boxes[0].inbox, "token")
	feedRow := seedEmailFeedFor(t, q, u.ID, boxes[0].address, "Mine")

	p := pollerWithInboxes(q, srv)
	if err := p.PollFeed(ctx, feedRow); err != nil {
		t.Fatalf("PollFeed: %v", err)
	}

	if got := calls(boxes[0].inbox); got == 0 {
		t.Fatal("PollFeed made no mailbox requests: an email feed's Fetch reads only the " +
			"staging table, so the per-feed path must still drain its owner's inbox")
	}
	var items int
	if err := db.QueryRow(`SELECT count(*) FROM items WHERE feed_id = ?`, feedRow.ID).Scan(&items); err != nil {
		t.Fatalf("count: %v", err)
	}
	if items != 1 {
		t.Errorf("items = %d, want 1", items)
	}
}

// An account that configured credentials but owns no email feed must issue no
// requests: the drain list is derived from feeds, not from users.
func TestPollAllSkipsAccountsWithoutEmailFeeds(t *testing.T) {
	db := openSchemaDB(t)
	q := store.New(db)
	ctx := context.Background()

	boxes := []fakeMailbox{
		{inbox: "unused@agentmail.to", address: "unused+x@agentmail.to", subject: "Unused"},
	}
	srv, calls := fakeAgentMailMulti(t, boxes)

	seedUserWithEmailConfig(t, q, "configured-only@test.local", boxes[0].inbox, "token")

	p := pollerWithInboxes(q, srv)
	p.PollAll(ctx)

	if got := calls(boxes[0].inbox); got != 0 {
		t.Errorf("mailbox saw %d requests for an account with no email feeds, want 0", got)
	}
}

// Two feeds in ONE account sharing one inbox: the mailbox is drained once, and
// each feed still reads only its own plus-address.
func TestPollAllDrainsOnceForTwoFeedsInOneAccount(t *testing.T) {
	db := openSchemaDB(t)
	q := store.New(db)
	ctx := context.Background()

	boxes := []fakeMailbox{
		{inbox: "shared@agentmail.to", address: "shared+one@agentmail.to", subject: "One"},
	}
	srv, calls := fakeAgentMailMulti(t, boxes)

	u := seedUserWithEmailConfig(t, q, "two-feeds@test.local", boxes[0].inbox, "token")
	one := seedEmailFeedFor(t, q, u.ID, "shared+one@agentmail.to", "One")
	two := seedEmailFeedFor(t, q, u.ID, "shared+two@agentmail.to", "Two")

	p := pollerWithInboxes(q, srv)
	p.PollAll(ctx)

	if got := calls(boxes[0].inbox); got != 2 {
		t.Errorf("mailbox saw %d requests for one inbox with two feeds, want 2 "+
			"(one drain: 1 list + 1 get)", got)
	}
	var oneItems int
	if err := db.QueryRow(`SELECT count(*) FROM items WHERE feed_id = ?`, one.ID).Scan(&oneItems); err != nil {
		t.Fatalf("count: %v", err)
	}
	if oneItems != 1 {
		t.Errorf("feed +one has %d items, want 1", oneItems)
	}
	var twoItems int
	if err := db.QueryRow(`SELECT count(*) FROM items WHERE feed_id = ?`, two.ID).Scan(&twoItems); err != nil {
		t.Fatalf("count: %v", err)
	}
	if twoItems != 0 {
		t.Errorf("feed +two has %d items, want 0: mail addressed to +one must not reach +two", twoItems)
	}
}
