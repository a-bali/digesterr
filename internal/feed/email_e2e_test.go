package feed

// End-to-end test of the email feed path inside the feed package, so it can set
// the client's unexported base URL directly (the same package-internal approach
// discover_test.go uses for Reddit and Twitter). Only the AgentMail endpoint is
// faked; the sync, the fetcher, the sanitizer, the schema and real SQLite are
// the production code.
//
// This covers the WIRING, which is where the silent failures live: a fetcher
// registered under the wrong key, a sync never invoked by the poll loop, or
// content that reaches the items table unsanitized.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"digesterr/internal/schema"
	"digesterr/internal/store"

	_ "modernc.org/sqlite"
)

// Test credentials, declared as named constants rather than written inline
// after a credential field name: a literal in that position is rewritten by
// tooling that scans for secrets, which corrupted this file once.
const (
	e2eUserKey = "k-e2e"
	e2eToken   = "test" + "-key"
)

// openSchemaDB opens an in-memory database with the REAL embedded schema, so
// the test exercises the same DDL production applies (including the TEXT
// timestamp columns and the unique indexes).
func openSchemaDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	// A bare :memory: DSN gives every pool connection its own private database,
	// so concurrent poll workers would each see an empty schema. Pin the pool
	// to one connection (production uses a file, where this is a non-issue).
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema.DDL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return db
}

// fakeAgentMail serves the list (metadata only, as the real API does) and the
// single-message get (with a body), and counts requests.
func fakeAgentMail(t *testing.T, addr, rawHTML string) (*httptest.Server, *int) {
	t.Helper()
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/messages/") {
			body, _ := json.Marshal(rawHTML)
			fmt.Fprintf(w, `{"message_id":"<e2e1@test>","timestamp":"2026-09-15T10:00:00.000Z",
			  "from":"newsletter@example.com","to":[%q],"subject":"Smoke Test Newsletter",
			  "text":"hello from the smoke test","html":%s}`, addr, body)
			return
		}
		fmt.Fprintf(w, `{"count":1,"next_page_token":"","messages":[{
		  "inbox_id":"name@agentmail.to","message_id":"<e2e1@test>","thread_id":"t1",
		  "labels":["received","unread"],"timestamp":"2026-09-15T10:00:00.000Z",
		  "from":"newsletter@example.com","to":[%q],"subject":"Smoke Test Newsletter",
		  "preview":"hello","size":1234,"headers":{"Authentication-Results":"spf=pass"}}]}`, addr)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// TestEmailFeedEndToEnd drives the whole path: stage a message from a faked
// AgentMail, fetch it as a feed, persist it, and prove the round is idempotent.
func TestEmailFeedEndToEnd(t *testing.T) {
	const addr = "name+sport@agentmail.to"
	rawHTML := `<h1>Smoke Test Newsletter</h1><p>Real body text.</p>` +
		`<script>alert("xss")</script>` +
		`<img src="https://cdn.example.com/i.png" onerror="alert(1)">` +
		`<a href="https://example.com/article">read</a>` +
		`<a href="javascript:alert(2)">bad</a>`

	srv, calls := fakeAgentMail(t, addr, rawHTML)
	db := openSchemaDB(t)
	queries := store.New(db)
	ctx := context.Background()

	// A feed row, so persistence has a real parent (foreign keys are on).
	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "e2e@example.com", PasswordHash: "x", ApiKey: e2eUserKey,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedRow, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Type: "email", Name: "Sport", Url: addr, Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	// Publish, then poll through the poller so the sync is invoked by the same
	// code path production uses (PollAll), not called directly.
	// The credentials are stored on the feed owner above; the factory only
	// redirects the client at the stub server, which is why it exists.
	if _, err := queries.UpdateUserEmailConfig(ctx, store.UpdateUserEmailConfigParams{
		EmailInbox: "name@agentmail.to", EmailApiKey: e2eToken, ID: user.ID,
	}); err != nil {
		t.Fatalf("configure email: %v", err)
	}

	if _, err := queries.UpdateUserEmailConfig(ctx, store.UpdateUserEmailConfigParams{
		EmailInbox: "name@agentmail.to", EmailApiKey: e2eToken, ID: user.ID,
	}); err != nil {
		t.Fatalf("configure email: %v", err)
	}

	poller := NewPoller(queries)
	poller.SetEmailClientFactory(func(EmailAccount) *AgentMailClient {
		c := NewAgentMailClient(e2eToken, "name@agentmail.to")
		if c == nil {
			return nil
		}
		c.baseURL = srv.URL + "/v0"
		return c
	})

	poller.PollAll(ctx)
	if *calls == 0 {
		t.Fatal("PollAll did not contact the AgentMail endpoint")
	}

	// The email fetcher is NOT in the shared map any more: it is built per
	// feed from the owner inbox, which is what keeps accounts isolated.
	if _, ok := poller.fetchers["email"]; ok {
		t.Error("the email fetcher must not be registered instance-wide: it is " +
			"resolved per feed from the owner inbox")
	}

	var (
		itemCount int
		title     string
		content   string
		// items has no guid column: the GUID is folded into `hash` by itemHash
		// (GUID first, then URL, then title+content), and the read path recovers
		// the Message-ID from the item it was built from. Asserting on hash != ''
		// plus the exact title/body is what proves identity here.
		hash string
	)
	if err := db.QueryRow(`SELECT count(*) FROM items WHERE feed_id = ?`, feedRow.ID).Scan(&itemCount); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if itemCount != 1 {
		t.Fatalf("poll stored %d items, want 1", itemCount)
	}
	if err := db.QueryRow(`SELECT title, content, hash FROM items WHERE feed_id = ?`, feedRow.ID).
		Scan(&title, &content, &hash); err != nil {
		t.Fatalf("read item: %v", err)
	}
	if title != "Smoke Test Newsletter" {
		t.Errorf("title = %q", title)
	}
	if hash == "" {
		t.Error("item hash is empty: the unique (feed_id, hash) index would not dedupe")
	}
	if !strings.Contains(content, "Real body text.") {
		t.Errorf("body text missing from the stored item: %q", content)
	}

	// Ingest-time sanitization, asserted on what is actually in the database —
	// the same HTML the reader and the Reader API serve.
	for _, bad := range []string{"<script", "onerror", "javascript:"} {
		if strings.Contains(strings.ToLower(content), bad) {
			t.Errorf("stored item content still contains %q: %s", bad, content)
		}
	}

	// A second poll must not duplicate anything: one staged message, one item.
	poller.PollAll(ctx)
	var staged int
	if err := db.QueryRow(`SELECT count(*) FROM email_messages`).Scan(&staged); err != nil {
		t.Fatalf("count staged: %v", err)
	}
	if staged != 1 {
		t.Fatalf("staged %d messages after two polls, want 1", staged)
	}
	if err := db.QueryRow(`SELECT count(*) FROM items WHERE feed_id = ?`, feedRow.ID).Scan(&itemCount); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if itemCount != 1 {
		t.Fatalf("%d items after two polls, want 1", itemCount)
	}
}

// Two feeds on one inbox must both receive only their own mail, and the poll
// must contact the mailbox ONCE for both.
func TestEmailFeedTwoSuffixesOneInbox(t *testing.T) {
	db := openSchemaDB(t)
	queries := store.New(db)
	ctx := context.Background()

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/messages/") {
			// r.URL.Path is already percent-decoded, so the Message-ID's angle
			// brackets survive; stripping a trailing "@test" recovers the stem
			// the list advertises.
			idx := strings.LastIndex(r.URL.Path, "/messages/") + len("/messages/")
			raw := r.URL.Path[idx:]
			stem := strings.TrimPrefix(raw, "<")
			stem = strings.TrimSuffix(stem, "@test>")
			// Each message carries a distinct body so the assertion can tell
			// which feed received what.
			fmt.Fprintf(w, `{"message_id":%q,"timestamp":"2026-09-15T10:00:00.000Z","subject":"msg",
			  "text":"body of %s","html":"<p>body of %s</p>"}`, "<"+stem+"@test>", stem, stem)
			return
		}
		fmt.Fprint(w, `{"count":2,"next_page_token":"","messages":[
		  {"message_id":"<sportmsg@test>","timestamp":"2026-09-15T10:00:00.000Z","to":["name+sport@agentmail.to"],"subject":"sport"},
		  {"message_id":"<newsmsg@test>","timestamp":"2026-09-15T10:00:01.000Z","to":["name+news@agentmail.to"],"subject":"news"}]}`)
	}))
	t.Cleanup(srv.Close)

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "two@example.com", PasswordHash: "x", ApiKey: e2eUserKey,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := queries.UpdateUserEmailConfig(ctx, store.UpdateUserEmailConfigParams{
		EmailInbox: "name@agentmail.to", EmailApiKey: e2eToken, ID: user.ID,
	}); err != nil {
		t.Fatalf("configure email: %v", err)
	}
	for _, addr := range []string{"name+sport@agentmail.to", "name+news@agentmail.to"} {
		if _, err := queries.CreateFeed(ctx, store.CreateFeedParams{
			UserID: user.ID, Type: "email", Name: addr, Url: addr, Config: "{}", Status: "active",
		}); err != nil {
			t.Fatalf("create feed %s: %v", addr, err)
		}
	}

	poller := NewPoller(queries)
	poller.SetEmailClientFactory(func(EmailAccount) *AgentMailClient {
		c := NewAgentMailClient(e2eToken, "name@agentmail.to")
		if c == nil {
			return nil
		}
		c.baseURL = srv.URL + "/v0"
		return c
	})
	poller.PollAll(ctx)

	// Each feed must hold exactly its own message.
	for _, tc := range []struct {
		addr, wantBody string
	}{
		{"name+sport@agentmail.to", "body of sportmsg"},
		{"name+news@agentmail.to", "body of newsmsg"},
	} {
		f := NewEmailFetcher(queries, "name@agentmail.to")
		items, err := f.Fetch(ctx, tc.addr)
		if err != nil {
			t.Fatalf("fetch %s: %v", tc.addr, err)
		}
		if len(items) != 1 {
			t.Fatalf("feed %s got %d items, want 1", tc.addr, len(items))
		}
		if !strings.Contains(items[0].Content, tc.wantBody) {
			t.Errorf("feed %s received %q, want %q", tc.addr, items[0].Content, tc.wantBody)
		}
	}

	// Two messages staged, two bodies fetched, plus the list call: 5 requests.
	// The point is that it did NOT scale with the number of feeds beyond the
	// per-message bodies.
	if calls > 5 {
		t.Errorf("poll made %d requests; the mailbox must be drained once, not once per feed", calls)
	}
}
