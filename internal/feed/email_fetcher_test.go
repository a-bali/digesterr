package feed

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"digesterr/internal/store"

	_ "modernc.org/sqlite"
)

// emailFetchSchema mirrors the email parts of the real schema plus the feeds
// and items tables the fetcher path touches.
const emailFetchSchema = `
CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    api_key       TEXT NOT NULL DEFAULT '',
    llm_model     TEXT NOT NULL DEFAULT '',
    email_inbox   TEXT NOT NULL DEFAULT '',
    email_api_key TEXT NOT NULL DEFAULT '',
    created_at    DATETIME NOT NULL DEFAULT (DATETIME('now')),
    updated_at    DATETIME NOT NULL DEFAULT (DATETIME('now'))
);

CREATE TABLE feeds (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    category TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL,
    name TEXT NOT NULL,
    url TEXT NOT NULL DEFAULT '',
    config TEXT NOT NULL DEFAULT '{}',
    min_score INTEGER NOT NULL DEFAULT 0,
    last_fetched_at DATETIME,
    status TEXT NOT NULL DEFAULT 'active',
    extract_content INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    last_error_at DATETIME,
    last_error_severity TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT (DATETIME('now')),
    updated_at DATETIME NOT NULL DEFAULT (DATETIME('now'))
);
CREATE TABLE items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    feed_id INTEGER NOT NULL,
    hash TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL,
    url TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    author TEXT NOT NULL DEFAULT '',
    comment_url TEXT NOT NULL DEFAULT '',
    points INTEGER NOT NULL DEFAULT 0,
    comments_count INTEGER NOT NULL DEFAULT 0,
    published_at DATETIME,
    status TEXT NOT NULL DEFAULT 'unread',
    fetched_at DATETIME NOT NULL DEFAULT (DATETIME('now')),
    created_at DATETIME NOT NULL DEFAULT (DATETIME('now')),
    updated_at DATETIME NOT NULL DEFAULT (DATETIME('now')),
    extract_content TEXT NOT NULL DEFAULT '',
    extract_status TEXT NOT NULL DEFAULT '',
    extract_retries INTEGER NOT NULL DEFAULT 0,
    extract_date DATETIME
);
CREATE UNIQUE INDEX idx_items_feed_hash ON items(feed_id, hash) WHERE hash != '';
CREATE TABLE email_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    inbox_id TEXT NOT NULL,
    message_id TEXT NOT NULL,
    thread_id TEXT NOT NULL DEFAULT '',
    from_addr TEXT NOT NULL DEFAULT '',
    to_addrs TEXT NOT NULL DEFAULT '',
    subject TEXT NOT NULL DEFAULT '',
    text TEXT NOT NULL DEFAULT '',
    html TEXT NOT NULL DEFAULT '',
    raw_headers TEXT NOT NULL DEFAULT '{}',
    received_at TEXT,
    complete_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT (DATETIME('now')),
    UNIQUE (inbox_id, message_id)
);
CREATE TABLE email_sync_state (
    inbox_id TEXT PRIMARY KEY,
    last_synced_at TEXT,
    last_error TEXT NOT NULL DEFAULT '',
    last_error_at TEXT,
    updated_at DATETIME NOT NULL DEFAULT (DATETIME('now'))
);
CREATE TABLE email_message_recipients (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    inbox_id TEXT NOT NULL,
    message_id TEXT NOT NULL,
    address TEXT NOT NULL,
    UNIQUE (inbox_id, message_id, address),
    FOREIGN KEY (inbox_id, message_id) REFERENCES email_messages(inbox_id, message_id) ON DELETE CASCADE
);
CREATE INDEX idx_email_recipients_inbox_address ON email_message_recipients(inbox_id, address);
`

func emailSyncTestDB(t *testing.T) (*store.Queries, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(emailFetchSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return store.New(db), db
}

// syncTestServer serves the two endpoints plus a request counter, so a test can
// assert how many times the mailbox was actually contacted.
func syncTestServer(t *testing.T, listBody string, getBody func(messageID string) string) (*httptest.Server, *int64) {
	t.Helper()
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/messages/") {
			// Single-message get: the tail of the path is the message id.
			idx := strings.LastIndex(r.URL.Path, "/messages/")
			id := r.URL.Path[idx+len("/messages/"):]
			fmt.Fprint(w, getBody(id))
			return
		}
		fmt.Fprint(w, listBody)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func newSyncFor(t *testing.T, q *store.Queries, srv *httptest.Server) *EmailSync {
	t.Helper()
	c := NewAgentMailClient("k", "name@agentmail.to")
	if c == nil {
		t.Fatal("client construction failed")
	}
	c.baseURL = srv.URL
	return NewEmailSync(c, q)
}

// THE fan-out guard: two email feeds share one inbox, so one sync pass must
// contact the mailbox once — not once per feed. The Twitter 429 incident was
// exactly this shape (a fetcher that re-fetched the timeline per item), so this
// is the test that matters most in this file.
func TestEmailSyncContactsMailboxOncePerPass(t *testing.T) {
	q, _ := emailSyncTestDB(t)
	const addr = "name+sport@agentmail.to"

	body := `{"count":1,"next_page_token":"","messages":[{"message_id":"<m1@x>","timestamp":"2026-09-15T10:00:00.000Z","to":["` + addr + `"],"from":"a@b.c","subject":"one","labels":["received"]}]}`
	srv, calls := syncTestServer(t, body, func(string) string {
		return `{"message_id":"<m1@x>","timestamp":"2026-09-15T10:00:00.000Z","from":"a@b.c","subject":"one","text":"body","html":"<p>body</p>"}`
	})

	s := newSyncFor(t, q, srv)
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	// 1 list + 1 get for the single new message.
	if got := atomic.LoadInt64(calls); got != 2 {
		t.Fatalf("first sync made %d requests, want 2 (1 list + 1 get)", got)
	}

	// The two feeds sharing this inbox both read the staged row — with no
	// further network traffic at all.
	f := NewEmailFetcher(q, "name@agentmail.to")
	items1, err := f.Fetch(context.Background(), addr)
	if err != nil {
		t.Fatalf("fetch feed 1: %v", err)
	}
	items2, err := f.Fetch(context.Background(), addr)
	if err != nil {
		t.Fatalf("fetch feed 2: %v", err)
	}
	if len(items1) != 1 || len(items2) != 1 {
		t.Fatalf("expected 1 item per feed, got %d and %d", len(items1), len(items2))
	}
	if got := atomic.LoadInt64(calls); got != 2 {
		t.Fatalf("Fetch performed network I/O: request count rose to %d, want 2 (the mailbox is drained only by the sync stage)", got)
	}

	// A second sync over the same window must not re-fetch the body: the row
	// is duplicated in the listing but already staged, so only the list call
	// happens (the overlap means the message is listed again).
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if got := atomic.LoadInt64(calls); got != 3 {
		t.Fatalf("second sync made the count %d, want 3 (list only: the message is already staged, so no body re-fetch)", got)
	}
}

// The overlap must be applied to the stored watermark: the window starts before
// it, because `after` exclusivity is unverified and a shared second can drop mail.
func TestEmailSyncAppliesOverlapToWatermark(t *testing.T) {
	q, _ := emailSyncTestDB(t)
	if err := q.UpsertEmailSyncState(context.Background(), store.UpsertEmailSyncStateParams{
		InboxID:      "name@agentmail.to",
		LastSyncedAt: nsString("2026-09-15T12:00:00Z"),
	}); err != nil {
		t.Fatalf("seed watermark: %v", err)
	}

	var gotAfter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAfter = r.URL.Query().Get("after")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"count":0,"next_page_token":"","messages":[]}`)
	}))
	t.Cleanup(srv.Close)

	s := newSyncFor(t, q, srv)
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	// 12:00 minus the 15m overlap.
	want := "2026-09-15T11:45:00Z"
	if gotAfter != want {
		t.Fatalf("after = %q, want %q (watermark rewound by the overlap)", gotAfter, want)
	}
}

// The first sync has no watermark and must not invent a lower bound that would
// hide the mailbox's existing mail.
func TestEmailSyncFirstRunHasNoLowerBound(t *testing.T) {
	q, _ := emailSyncTestDB(t)
	var gotAfter string
	seen := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = true
		gotAfter = r.URL.Query().Get("after")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"count":0,"next_page_token":"","messages":[]}`)
	}))
	t.Cleanup(srv.Close)

	s := newSyncFor(t, q, srv)
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !seen {
		t.Fatal("sync did not call the API")
	}
	if gotAfter != "" {
		t.Fatalf("after = %q on a first sync, want empty (no lower bound)", gotAfter)
	}
}

// Sanitization must happen on INGEST, so raw publisher markup never reaches the
// database that both the reader and the Reader API serve from.
func TestEmailSyncSanitizesOnIngest(t *testing.T) {
	q, db := emailSyncTestDB(t)
	const addr = "name+sport@agentmail.to"
	body := `{"count":1,"next_page_token":"","messages":[{"message_id":"<evil@x>","timestamp":"2026-09-15T10:00:00.000Z","to":["` + addr + `"],"subject":"evil"}]}`
	srv, _ := syncTestServer(t, body, func(string) string {
		return `{"message_id":"<evil@x>","timestamp":"2026-09-15T10:00:00.000Z","subject":"evil","text":"hi","html":"<p>hi</p><script>alert(1)</script><img src=x onerror=alert(2)><a href=\"javascript:alert(3)\">x</a>"}`
	})

	s := newSyncFor(t, q, srv)
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}

	var stored string
	if err := db.QueryRow(`SELECT html FROM email_messages WHERE message_id = '<evil@x>'`).Scan(&stored); err != nil {
		t.Fatalf("read stored html: %v", err)
	}
	for _, bad := range []string{"<script", "onerror", "javascript:"} {
		if strings.Contains(strings.ToLower(stored), bad) {
			t.Errorf("stored HTML still contains %q: %s", bad, stored)
		}
	}
	if !strings.Contains(stored, "<p>hi</p>") {
		t.Errorf("legitimate content lost: %s", stored)
	}
}

// A text-only newsletter must still produce a readable item.
// stageWithRecipients stages a complete message plus its normalized recipient
// rows, the state the sync leaves a fully ingested message in.
func stageWithRecipients(t *testing.T, q *store.Queries, inbox, msgID, receivedAt string, addrs []string, subject string) {
	t.Helper()
	toJSON, err := json.Marshal(addrs)
	if err != nil {
		t.Fatalf("marshal addrs: %v", err)
	}
	if _, err := q.CreateEmailMessageIfNotExists(context.Background(), store.CreateEmailMessageIfNotExistsParams{
		InboxID: inbox, MessageID: msgID, ToAddrs: string(toJSON),
		Subject: subject, Text: subject, Html: "<p>x</p>",
		ReceivedAt: nsString(receivedAt),
	}); err != nil {
		t.Fatalf("stage: %v", err)
	}
	for _, addr := range addrs {
		if err := q.AddEmailMessageRecipient(context.Background(), store.AddEmailMessageRecipientParams{
			InboxID: inbox, MessageID: msgID, Address: addr,
		}); err != nil {
			t.Fatalf("stage recipient: %v", err)
		}
	}
	// Completeness is what the production path records; the reader filters on
	// it, so a fixture that skips this is modelling a half-staged message.
	if _, err := q.MarkEmailMessageComplete(context.Background(), store.MarkEmailMessageCompleteParams{
		InboxID: inbox, MessageID: msgID,
	}); err != nil {
		t.Fatalf("mark complete: %v", err)
	}
}

func TestEmailRecipientRoutingMatchesLegacyLikeQuery(t *testing.T) {
	// Parity: the indexed exact-match query must return exactly what the old
	// leading-wildcard LIKE query returned for the same rows.
	q, _ := emailSyncTestDB(t)
	ctx := context.Background()
	stageWithRecipients(t, q, "name@agentmail.to", "<a@x>", "2026-09-15T10:00:00Z", []string{"name+sport@agentmail.to", "name+other@agentmail.to"}, "both")
	stageWithRecipients(t, q, "name@agentmail.to", "<b@x>", "2026-09-15T10:00:00Z", []string{"name+sport@agentmail.to"}, "sport")
	stageWithRecipients(t, q, "name@agentmail.to", "<c@x>", "2026-09-15T10:00:00Z", []string{"name+other@agentmail.to"}, "other")
	stageWithRecipients(t, q, "other@agentmail.to", "<d@x>", "2026-09-15T10:00:00Z", []string{"name+sport@agentmail.to"}, "wrong-inbox")

	legacy, err := q.ListEmailMessagesByRecipient(ctx, store.ListEmailMessagesByRecipientParams{
		InboxID: "name@agentmail.to", Address: "name+sport@agentmail.to", Since: "", Lim: 200,
	})
	if err != nil {
		t.Fatalf("legacy list: %v", err)
	}
	exact, err := q.ListEmailMessagesByRecipientExact(ctx, store.ListEmailMessagesByRecipientExactParams{
		InboxID: "name@agentmail.to", Address: "name+sport@agentmail.to", Since: "", Lim: 200,
	})
	if err != nil {
		t.Fatalf("exact list: %v", err)
	}
	if len(legacy) != len(exact) {
		t.Fatalf("row count differs: legacy=%d exact=%d", len(legacy), len(exact))
	}
	for i := range legacy {
		if legacy[i].MessageID != exact[i].MessageID {
			t.Fatalf("row %d differs: legacy=%s exact=%s", i, legacy[i].MessageID, exact[i].MessageID)
		}
	}
}

func TestEmailFetcherTextOnlyMessage(t *testing.T) {
	q, _ := emailSyncTestDB(t)
	const addr = "name+sport@agentmail.to"
	stageWithRecipients(t, q, "name@agentmail.to", "<text@x>", "2026-09-15T10:00:00Z", []string{addr}, "text only")
	// The helper stages a placeholder body; replace it with the text-only body
	// this test asserts on.
	if err := q.UpdateEmailMessageBody(context.Background(), store.UpdateEmailMessageBodyParams{
		InboxID: "name@agentmail.to", MessageID: "<text@x>", Text: "line one\nline two", Html: "",
	}); err != nil {
		t.Fatalf("body: %v", err)
	}

	items, err := NewEmailFetcher(q, "name@agentmail.to").Fetch(context.Background(), addr)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	it := items[0]
	if !strings.Contains(it.Content, "line one") || !strings.Contains(it.Content, "line two") {
		t.Errorf("text content not rendered: %q", it.Content)
	}
	if it.GUID != "<text@x>" {
		t.Errorf("GUID = %q, want the RFC Message-ID", it.GUID)
	}
	if it.Title != "text only" {
		t.Errorf("title = %q", it.Title)
	}
}

// Items must be returned oldest-first, matching every other fetcher, and must
// carry the Message-ID as GUID so itemHash dedupes across polls.
func TestEmailFetcherOrdersChronologically(t *testing.T) {
	q, _ := emailSyncTestDB(t)
	const addr = "name+sport@agentmail.to"
	ctx := context.Background()
	for i, ts := range []string{"2026-09-15T12:00:00Z", "2026-09-15T10:00:00Z", "2026-09-15T11:00:00Z"} {
		stageWithRecipients(t, q, "name@agentmail.to", fmt.Sprintf("<m%d@x>", i), ts, []string{addr}, fmt.Sprintf("s%d", i))
	}

	items, err := NewEmailFetcher(q, "name@agentmail.to").Fetch(ctx, addr)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	for i := 1; i < len(items); i++ {
		if items[i].PublishedAt.Before(items[i-1].PublishedAt) {
			t.Fatalf("items not chronological: %v then %v", items[i-1].PublishedAt, items[i].PublishedAt)
		}
	}
}

// firstLinkIn must pick an outbound link for the reader's "open original", and
// must not be fooled into returning a mailto or a relative path.
func TestEmailItemURLExtraction(t *testing.T) {
	cases := []struct{ html, want string }{
		{`<p><a href="https://example.com/a">read</a></p>`, "https://example.com/a"},
		{`<p><a href="mailto:x@y.z">mail</a></p>`, ""},
		{`<p><a href="/local">rel</a></p>`, ""},
		{`<img src="https://cdn.example.com/i.png">`, ""},
	}
	for _, tc := range cases {
		got := firstLinkIn(tc.html)
		if got != tc.want {
			t.Errorf("firstLinkIn(%s) = %q, want %q", tc.html, got, tc.want)
		}
	}
	// When a relative link precedes an absolute one, the absolute one wins.
	got := firstLinkIn(`<a href="/local">rel</a> then <a href="https://example.com/b">abs</a>`)
	if got != "https://example.com/b" {
		t.Errorf("firstLinkIn picked %q, want the first absolute URL", got)
	}
}

// A failing sync must record the error and NOT advance the watermark, so the
// next cycle retries the same window instead of skipping the mail.
func TestEmailSyncFailureKeepsWatermark(t *testing.T) {
	q, db := emailSyncTestDB(t)
	ctx := context.Background()
	if err := q.UpsertEmailSyncState(ctx, store.UpsertEmailSyncStateParams{
		InboxID: "name@agentmail.to", LastSyncedAt: nsString("2026-09-15T12:00:00Z"),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"message":"boom"}`)
	}))
	t.Cleanup(srv.Close)

	s := newSyncFor(t, q, srv)
	if err := s.Run(ctx); err == nil {
		t.Fatal("expected an error from a failing sync")
	}

	var watermark string
	if err := db.QueryRow(`SELECT last_synced_at FROM email_sync_state WHERE inbox_id = 'name@agentmail.to'`).Scan(&watermark); err != nil {
		t.Fatalf("read watermark: %v", err)
	}
	if watermark != "2026-09-15T12:00:00Z" {
		t.Fatalf("watermark = %q, want it unchanged by a failed sync", watermark)
	}
	var lastErr string
	if err := db.QueryRow(`SELECT last_error FROM email_sync_state WHERE inbox_id = 'name@agentmail.to'`).Scan(&lastErr); err != nil {
		t.Fatalf("read error: %v", err)
	}
	if lastErr == "" {
		t.Fatal("sync error was not recorded")
	}
}

// A nil sync must be inert, so callers need no branching when AgentMail is
// unconfigured.
func TestEmailSyncNilIsInert(t *testing.T) {
	var s *EmailSync
	if s.Configured() {
		t.Fatal("nil sync reports configured")
	}
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("nil sync Run returned %v, want nil", err)
	}
	if NewEmailSync(nil, nil) != nil {
		t.Fatal("NewEmailSync(nil, nil) should return nil")
	}
}

// Body fetch failure must keep the staged metadata rather than losing the mail.
func TestEmailSyncKeepsMessageWhenBodyFetchFails(t *testing.T) {
	q, db := emailSyncTestDB(t)
	const addr = "name+sport@agentmail.to"
	body := `{"count":1,"next_page_token":"","messages":[{"message_id":"<nobody@x>","timestamp":"2026-09-15T10:00:00.000Z","to":["` + addr + `"],"subject":"meta only"}]}`
	srv, _ := syncTestServer(t, body, func(string) string {
		return "" // unreachable body; the decoder will fail
	})
	// Make the body endpoint fail explicitly.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/messages/") {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"gone"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv2.Close)
	_ = srv

	s := newSyncFor(t, q, srv2)
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("sync should tolerate a body failure, got %v", err)
	}

	var subject string
	if err := db.QueryRow(`SELECT subject FROM email_messages WHERE message_id = '<nobody@x>'`).Scan(&subject); err != nil {
		t.Fatalf("the message's metadata was lost when its body failed: %v", err)
	}
	if subject != "meta only" {
		t.Errorf("subject = %q", subject)
	}
}
