package feed

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"digesterr/internal/store"
)

// newCountingBodyServer serves a fixed list plus bodies, counting only the body
// (single-message) requests, so a test can tell a body fetch from a list call.
func newCountingBodyServer(t *testing.T, listBody string, bodies *int, getBody func(string) string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/messages/") {
			*bodies++
			idx := strings.LastIndex(r.URL.Path, "/messages/")
			fmt.Fprint(w, getBody(r.URL.Path[idx+len("/messages/"):]))
			return
		}
		fmt.Fprint(w, listBody)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newFailingBodyServer serves a list and answers every body request with 404,
// forever: the shape of a provider that will never yield the body.
func newFailingBodyServer(t *testing.T, listBody string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/messages/") {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"gone"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, listBody)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// halfStaged seeds a row exactly as a pass that failed partway leaves it:
// metadata present (so UNIQUE (inbox_id, message_id) makes the next re-read a
// no-op) but with no recipient rows, an empty body, and no completion marker.
func halfStaged(t *testing.T, q *store.Queries, inbox, msgID, receivedAt, addr string) {
	t.Helper()
	if _, err := q.CreateEmailMessageIfNotExists(context.Background(), store.CreateEmailMessageIfNotExistsParams{
		InboxID:    inbox,
		MessageID:  msgID,
		ToAddrs:    `["` + addr + `"]`,
		Subject:    "half staged",
		ReceivedAt: nsString(receivedAt),
	}); err != nil {
		t.Fatalf("seed half-staged row: %v", err)
	}
}

func countRecipients(t *testing.T, q *store.Queries, inbox, msgID string) int {
	t.Helper()
	rows, err := q.ListEmailMessagesByRecipientExact(context.Background(), store.ListEmailMessagesByRecipientExactParams{
		InboxID: inbox, Address: "name+sport@agentmail.to", Since: "", Lim: 100,
	})
	if err != nil {
		t.Fatalf("list recipients: %v", err)
	}
	n := 0
	for _, r := range rows {
		if r.MessageID == msgID {
			n++
		}
	}
	return n
}

func readWatermark(t *testing.T, q *store.Queries, inbox string) string {
	t.Helper()
	st, err := q.GetEmailSyncState(context.Background(), inbox)
	if err != nil {
		t.Fatalf("read sync state: %v", err)
	}
	if !st.LastSyncedAt.Valid {
		return ""
	}
	return st.LastSyncedAt.String
}

// The overlap re-read is the documented repair window ("Re-reading a short
// overlap is free because email_messages has UNIQUE (inbox_id, message_id)"),
// but idempotency is not completeness. Treating an existing row as fully staged
// skipped the recipient and body work, so a pass that lost routing never
// repaired it: the recipient was gone permanently and the watermark had already
// moved past the message.
func TestEmailSyncRepairsHalfStagedMessageOnReRead(t *testing.T) {
	q, db := emailSyncTestDB(t)
	ctx := context.Background()
	const inbox, addr, msgID = "name@agentmail.to", "name+sport@agentmail.to", "<half@x>"

	halfStaged(t, q, inbox, msgID, "2026-09-15T10:00:00Z", addr)

	// The next pass re-reads the overlap and sees the same message again.
	list := `{"count":1,"messages":[{"message_id":"` + msgID + `","timestamp":"2026-09-15T10:00:00.000Z","to":["` + addr + `"],"subject":"half staged"}]}`
	srv, _ := syncTestServer(t, list, func(string) string { return syncGetBody })
	s := newSyncFor(t, q, srv)

	if err := s.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	if n := countRecipients(t, q, inbox, msgID); n != 1 {
		t.Errorf("routing rows for %s = %d, want 1: the re-read must repair missing recipients", msgID, n)
	}
	var text string
	if err := db.QueryRow(`SELECT text FROM email_messages WHERE inbox_id = ? AND message_id = ?`, inbox, msgID).Scan(&text); err != nil {
		t.Fatalf("read body: %v", err)
	}
	if text == "" {
		t.Error("body is still empty after the re-read: the failed body was never retried")
	}
	var complete *string
	if err := db.QueryRow(`SELECT complete_at FROM email_messages WHERE message_id = ?`, msgID).Scan(&complete); err != nil {
		t.Fatalf("read complete_at: %v", err)
	}
	if complete == nil {
		t.Error("complete_at is still NULL after a successful repair")
	}
}

// This is the core of the fix. A pass that fails on a message must NOT advance
// the watermark past it: a message behind the watermark is never listed again,
// so its missing half can never be filled and the loss is permanent.
//
// Pinning the watermark at the failure is what replaces the repair pass. It is
// the smallest thing that closes the hole -- there is no second query, no
// attempt budget, and nothing to run when the window is empty.
func TestEmailSyncDoesNotAdvanceWatermarkPastAFailedMessage(t *testing.T) {
	q, _ := emailSyncTestDB(t)
	ctx := context.Background()
	const inbox, addr = "name@agentmail.to", "name+sport@agentmail.to"
	const failedMsg, okMsg = "<failed@x>", "<ok@x>"

	// A newer message stages cleanly; the older one cannot be staged at all.
	list := `{"count":2,"messages":[` +
		`{"message_id":"` + failedMsg + `","timestamp":"2026-09-15T10:00:00.000Z","to":["` + addr + `"],"subject":"fails"},` +
		`{"message_id":"` + okMsg + `","timestamp":"2026-09-15T11:00:00.000Z","to":["` + addr + `"],"subject":"ok"}]}`

	// The list succeeds; only the failed message's body 404s.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, failedMsg) {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"gone"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/messages/") {
			fmt.Fprint(w, syncGetBody)
			return
		}
		fmt.Fprint(w, list)
	}))
	t.Cleanup(srv.Close)

	s := newSyncFor(t, q, srv)
	if err := s.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	// The watermark must be pinned at the FAILED message's timestamp, not the
	// newer successful sibling's: stepping to 11:00 would leave 10:00 unlisted
	// forever.
	got := readWatermark(t, q, inbox)
	if !strings.HasPrefix(got, "2026-09-15T10:00:00") {
		t.Errorf("watermark = %q, want it pinned at the failed message (2026-09-15T10:00:00): "+
			"advancing past it puts the message out of reach of every later pass", got)
	}

	// The healthy sibling must still have been staged: one bad message must not
	// stop the rest of the mailbox from flowing.
	if n := countRecipients(t, q, inbox, okMsg); n != 1 {
		t.Errorf("routing rows for the healthy message = %d, want 1: a failure must not block the rest of the window", n)
	}
}

// Routing loss was the FIRST of the two permanent-loss paths: a recipient-insert
// error was logged and skipped, and the watermark stepped over the message, so a
// message with no recipient row is invisible to every feed and can never be
// listed again.
//
// The failure is injected by dropping the recipient table, which is the only way
// to make the insert genuinely error (it is INSERT OR IGNORE, so a duplicate is
// not an error).
func TestEmailSyncDoesNotAdvanceWatermarkPastARoutingFailure(t *testing.T) {
	q, db := emailSyncTestDB(t)
	ctx := context.Background()
	const inbox, addr, msgID = "name@agentmail.to", "name+sport@agentmail.to", "<noroute@x>"

	list := `{"count":1,"messages":[{"message_id":"` + msgID + `","timestamp":"2026-09-15T10:00:00.000Z","to":["` + addr + `"],"subject":"noroute"}]}`
	srv, _ := syncTestServer(t, list, func(string) string { return syncGetBody })
	s := newSyncFor(t, q, srv)

	if _, err := db.Exec(`DROP TABLE email_message_recipients`); err != nil {
		t.Fatalf("drop recipients table: %v", err)
	}

	if err := s.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	got := readWatermark(t, q, inbox)
	if !strings.HasPrefix(got, "2026-09-15T10:00:00") {
		t.Errorf("watermark = %q, want it pinned at the unroutable message (2026-09-15T10:00:00): "+
			"advancing past it makes the message invisible to every feed, permanently", got)
	}

	// And it must not be marked complete: a message with no recipient row is
	// not fully staged, whatever its body says.
	var complete *string
	if err := db.QueryRow(`SELECT complete_at FROM email_messages WHERE message_id = ?`, msgID).Scan(&complete); err != nil {
		t.Fatalf("read complete_at: %v", err)
	}
	if complete != nil {
		t.Error("complete_at is set for a message with no recipient row: it would never be retried")
	}
}

// A pinned window stops the inbox making progress, so it must not look like a
// healthy sync: the stall is recorded where the UI renders it. Without this the
// only evidence is a log line, and a silently stalled feed is the failure this
// whole change exists to prevent.
func TestEmailSyncSurfacesAPinnedWindow(t *testing.T) {
	q, _ := emailSyncTestDB(t)
	ctx := context.Background()
	const inbox, addr, msgID = "name@agentmail.to", "name+sport@agentmail.to", "<gone@x>"

	list := `{"count":1,"messages":[{"message_id":"` + msgID + `","timestamp":"2026-09-15T10:00:00.000Z","to":["` + addr + `"],"subject":"gone"}]}`
	srv := newFailingBodyServer(t, list)
	s := newSyncFor(t, q, srv)

	if err := s.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	st, err := q.GetEmailSyncState(ctx, inbox)
	if err != nil {
		t.Fatalf("read sync state: %v", err)
	}
	if st.LastError == "" {
		t.Error("last_error is empty after a message could not be staged: " +
			"the pinned window is invisible, so a stalled feed looks healthy")
	}
}

// A message the provider will never serve a body for must not stall the mailbox
// forever: the window stays pinned, but that must not cost an unbounded body
// fetch on every pass for mail that is already whole.
func TestEmailSyncReReadsPinnedWindowWithoutRefetchingCompleteMessages(t *testing.T) {
	q, _ := emailSyncTestDB(t)
	ctx := context.Background()
	const inbox, addr, msgID = "name@agentmail.to", "name+sport@agentmail.to", "<whole@x>"

	list := `{"count":1,"messages":[{"message_id":"` + msgID + `","timestamp":"2026-09-15T10:00:00.000Z","to":["` + addr + `"],"subject":"whole"}]}`
	var bodies int
	srv := newCountingBodyServer(t, list, &bodies, func(string) string { return syncGetBody })
	s := newSyncFor(t, q, srv)

	for i := 0; i < 3; i++ {
		if err := s.Run(ctx); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if bodies != 1 {
		t.Errorf("body fetches across 3 passes = %d, want 1: a complete message must be skipped on re-read", bodies)
	}
}

// A pass with nothing to repair must not spend extra requests.
func TestEmailSyncMakesNoRepairRequestsWhenNothingIsIncomplete(t *testing.T) {
	q, _ := emailSyncTestDB(t)
	ctx := context.Background()
	const inbox, addr, msgID = "name@agentmail.to", "name+sport@agentmail.to", "<whole@x>"

	list := `{"count":1,"messages":[{"message_id":"` + msgID + `","timestamp":"2026-09-15T10:00:00.000Z","to":["` + addr + `"],"subject":"whole"}]}`
	srv, calls := syncTestServer(t, list, func(string) string { return syncGetBody })
	s := newSyncFor(t, q, srv)

	if err := s.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	// One list + one body for the new message. Anything more is a repair pass
	// fetching bodies it does not need.
	if *calls != 2 {
		t.Errorf("requests = %d, want 2 (list + one body); the repair path issued extra fetches", *calls)
	}
	if inbox != s.client.InboxID() {
		t.Fatalf("inbox mismatch: %s", s.client.InboxID())
	}
}

// A body failure must leave the row incomplete rather than looking finished, so
// the next pass revisits it.
func TestEmailSyncMarksIncompleteWhenBodyFetchFails(t *testing.T) {
	q, db := emailSyncTestDB(t)
	ctx := context.Background()
	const inbox, addr, msgID = "name@agentmail.to", "name+sport@agentmail.to", "<nobody@x>"

	list := `{"count":1,"messages":[{"message_id":"` + msgID + `","timestamp":"2026-09-15T10:00:00.000Z","to":["` + addr + `"],"subject":"meta only"}]}`
	srv := newFailingBodyServer(t, list)
	s := newSyncFor(t, q, srv)

	if err := s.Run(ctx); err != nil {
		t.Fatalf("sync must tolerate a body failure, got %v", err)
	}

	var complete *string
	if err := db.QueryRow(`SELECT complete_at FROM email_messages WHERE message_id = ?`, msgID).Scan(&complete); err != nil {
		t.Fatalf("read complete_at: %v", err)
	}
	if complete != nil {
		t.Errorf("complete_at = %q, want NULL: a message whose body failed is not fully staged", *complete)
	}
}

// A message that is not fully staged must not reach the reader as an empty
// item. Today it becomes a blank <p></p> newsletter that never fills in.
func TestEmailFetcherHidesIncompleteMessages(t *testing.T) {
	q, _ := emailSyncTestDB(t)
	ctx := context.Background()
	const inbox, addr = "name@agentmail.to", "name+sport@agentmail.to"

	halfStaged(t, q, inbox, "<half@x>", "2026-09-15T10:00:00Z", addr)
	stageWithRecipients(t, q, inbox, "<whole@x>", "2026-09-15T11:00:00Z", []string{addr}, "whole")

	f := NewEmailFetcher(q, inbox)
	items, err := f.Fetch(ctx, addr)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	for _, it := range items {
		if it.GUID == "<half@x>" {
			t.Error("an incompletely staged message was served as an item; it renders as an empty newsletter")
		}
	}
	if len(items) != 1 {
		t.Errorf("items = %d, want 1 (the complete message only)", len(items))
	}
}

// The marker must not hide a complete message: a text-only newsletter has an
// empty html column and is still whole.
func TestEmailFetcherServesCompleteTextOnlyMessage(t *testing.T) {
	q, db := emailSyncTestDB(t)
	ctx := context.Background()
	const inbox, addr = "name@agentmail.to", "name+sport@agentmail.to"

	stageWithRecipients(t, q, inbox, "<textonly@x>", "2026-09-15T10:00:00Z", []string{addr}, "text only")
	if _, err := db.Exec(`UPDATE email_messages SET html = '', text = 'plain body' WHERE message_id = '<textonly@x>'`); err != nil {
		t.Fatalf("blank html: %v", err)
	}

	f := NewEmailFetcher(q, inbox)
	items, err := f.Fetch(ctx, addr)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1: an empty html column is not incompleteness", len(items))
	}
	if items[0].GUID != "<textonly@x>" {
		t.Errorf("GUID = %q", items[0].GUID)
	}
}
