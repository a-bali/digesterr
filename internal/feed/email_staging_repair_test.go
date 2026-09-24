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
// (single-message) requests, so a test can tell a repair fetch from a list call.
func newCountingBodyServer(t *testing.T, bodies *int, getBody func(string) string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/messages/") {
			*bodies++
			idx := strings.LastIndex(r.URL.Path, "/messages/")
			fmt.Fprint(w, getBody(r.URL.Path[idx+len("/messages/"):]))
			return
		}
		fmt.Fprint(w, `{"count":0,"messages":[]}`)
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

// newCountingBodyServerStatus counts body requests and fails them with status.
func newCountingBodyServerStatus(t *testing.T, bodies *int, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/messages/") {
			*bodies++
			w.WriteHeader(status)
			fmt.Fprint(w, `{"message":"unavailable"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"count":0,"messages":[]}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// halfStaged seeds a row exactly as a pass that failed partway leaves it:
// metadata present (so UNIQUE (inbox_id, message_id) makes the next re-read a
// no-op) but with no recipient rows and an empty body.
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
	var n int
	rows, err := q.ListEmailMessagesByRecipientExact(context.Background(), store.ListEmailMessagesByRecipientExactParams{
		InboxID: inbox, Address: "name+sport@agentmail.to", Since: "", Lim: 100,
	})
	if err != nil {
		t.Fatalf("list recipients: %v", err)
	}
	for _, r := range rows {
		if r.MessageID == msgID {
			n++
		}
	}
	return n
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
}

// Repair must not depend on the API window. A message whose body failed more
// than one overlap (15m) before the newest sibling is never returned by
// ListMessages again, so the repair has to be driven from the staged rows
// themselves. Without this the loss is permanent, which is the whole bug.
func TestEmailSyncRepairsIncompleteRowsOutsideTheWindow(t *testing.T) {
	q, db := emailSyncTestDB(t)
	ctx := context.Background()
	const inbox, addr, msgID = "name@agentmail.to", "name+sport@agentmail.to", "<stale@x>"

	// Staged a month ago, incomplete, and the mailbox returns nothing: it is far
	// outside any overlap.
	halfStaged(t, q, inbox, msgID, "2026-08-15T10:00:00Z", addr)
	if err := q.UpsertEmailSyncState(ctx, store.UpsertEmailSyncStateParams{
		InboxID: inbox, LastSyncedAt: nsString("2026-09-15T10:00:00Z"),
	}); err != nil {
		t.Fatalf("seed watermark: %v", err)
	}

	srv, _ := syncTestServer(t, `{"count":0,"messages":[]}`, func(string) string { return syncGetBody })
	s := newSyncFor(t, q, srv)

	if err := s.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	var text string
	if err := db.QueryRow(`SELECT text FROM email_messages WHERE message_id = ?`, msgID).Scan(&text); err != nil {
		t.Fatalf("read body: %v", err)
	}
	if text == "" {
		t.Error("a message outside the overlap window was never repaired: the loss is permanent")
	}
	if n := countRecipients(t, q, inbox, msgID); n != 1 {
		t.Errorf("routing rows = %d, want 1: repair must restore routing too", n)
	}
}

// A repaired message must stop being retried, or every cycle re-fetches bodies
// for mail that is already whole.
func TestEmailSyncRepairsEachIncompleteRowOnce(t *testing.T) {
	q, db := emailSyncTestDB(t)
	ctx := context.Background()
	const inbox, addr, msgID = "name@agentmail.to", "name+sport@agentmail.to", "<stale@x>"

	halfStaged(t, q, inbox, msgID, "2026-08-15T10:00:00Z", addr)

	var bodies int
	srv := newCountingBodyServer(t, &bodies, func(string) string { return syncGetBody })
	s := newSyncFor(t, q, srv)

	if err := s.Run(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if bodies != 1 {
		t.Fatalf("body fetches after first pass = %d, want 1", bodies)
	}

	if err := s.Run(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if bodies != 1 {
		t.Errorf("body fetches after second pass = %d, want 1: a repaired row must not be re-fetched", bodies)
	}

	var complete *string
	if err := db.QueryRow(`SELECT complete_at FROM email_messages WHERE message_id = ?`, msgID).Scan(&complete); err != nil {
		t.Fatalf("read complete_at: %v", err)
	}
	if complete == nil {
		t.Error("complete_at is still NULL after a successful repair")
	}
}

// A pass with nothing to repair must not spend requests: the repair path runs
// every cycle, so an unconditional body fetch would re-download the mailbox
// forever.
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

// A body failure must leave the row repairable rather than looking finished.
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
	if inbox == "" {
		t.Fatal("unreachable")
	}
}

// A message the provider will never return a body for must not be retried
// forever: this codebase has already been burned once by hammering a
// rate-limited provider every cycle. After the retry budget is spent the row is
// surrendered (so the item still surfaces, as it did before this fix) and
// logged.
func TestEmailSyncStopsRetryingAfterBudgetExhausted(t *testing.T) {
	q, db := emailSyncTestDB(t)
	ctx := context.Background()
	const inbox, addr, msgID = "name@agentmail.to", "name+sport@agentmail.to", "<gone@x>"

	halfStaged(t, q, inbox, msgID, "2026-08-15T10:00:00Z", addr)

	var bodies int
	srv := newCountingBodyServerStatus(t, &bodies, http.StatusNotFound)
	s := newSyncFor(t, q, srv)

	for i := 0; i < emailRepairMaxAttempts+3; i++ {
		if err := s.Run(ctx); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}

	if bodies > emailRepairMaxAttempts {
		t.Errorf("body fetches = %d, want at most %d: retries are unbounded", bodies, emailRepairMaxAttempts)
	}

	var attempts int
	var complete *string
	if err := db.QueryRow(`SELECT staged_attempts, complete_at FROM email_messages WHERE message_id = ?`, msgID).Scan(&attempts, &complete); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if complete == nil {
		t.Error("complete_at is NULL after the retry budget was spent: the row is retried forever")
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

func TestEmailRepairBudgetIsBounded(t *testing.T) {
	if emailRepairMaxAttempts <= 0 {
		t.Fatal("the repair retry budget must be positive, or every incomplete row is surrendered immediately")
	}
	if emailRepairBatch <= 0 {
		t.Fatal("the repair batch must be positive")
	}
	if emailRepairBatch > 500 {
		t.Fatalf("repair batch = %d: an unbounded batch would hammer the provider in one cycle", emailRepairBatch)
	}
	if fmt.Sprint(emailRepairMaxAttempts) == "" {
		t.Fatal("unreachable")
	}
}
