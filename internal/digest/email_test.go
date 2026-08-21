package digest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

// noopEnricher satisfies feed.Enricher without changing items.
type noopEnricher struct{}

func (noopEnricher) Enrich(_ context.Context, _ *store.Item) error { return nil }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestAgentMailSenderSuccess(t *testing.T) {
	var gotPath, gotAuth, gotCT string
	var gotBody agentMailSendRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message_id":"m1","thread_id":"t1"}`))
	}))
	defer srv.Close()

	sender := &AgentMailSender{BaseURL: srv.URL, Token: "tok", InboxID: "in@agentmail.to"}
	err := sender.Send(context.Background(), "dest@example.com", "Subj", "plain", "<p>html</p>")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotPath != "/v0/inboxes/in@agentmail.to/messages/send" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("auth = %q", gotAuth)
	}
	if !strings.HasPrefix(gotCT, "application/json") {
		t.Errorf("content-type = %q", gotCT)
	}
	if len(gotBody.To) != 1 || gotBody.To[0] != "dest@example.com" {
		t.Errorf("to = %v", gotBody.To)
	}
	if gotBody.Subject != "Subj" || gotBody.Text != "plain" || gotBody.HTML != "<p>html</p>" {
		t.Errorf("body = %+v", gotBody)
	}
}

func TestAgentMailSenderFailureIncludesSnippet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	sender := &AgentMailSender{BaseURL: srv.URL, Token: "bad", InboxID: "in@agentmail.to"}
	err := sender.Send(context.Background(), "dest@example.com", "S", "t", "h")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "invalid api key") {
		t.Errorf("error missing status/snippet: %v", err)
	}
}

func TestAgentMailSenderValidation(t *testing.T) {
	ctx := context.Background()
	if err := (&AgentMailSender{Token: "", InboxID: "x"}).Send(ctx, "a@b.c", "s", "t", "h"); err == nil {
		t.Error("expected missing-token error")
	}
	if err := (&AgentMailSender{Token: "t", InboxID: ""}).Send(ctx, "a@b.c", "s", "t", "h"); err == nil {
		t.Error("expected missing-inbox error")
	}
	if err := (&AgentMailSender{Token: "t", InboxID: "i"}).Send(ctx, "", "s", "t", "h"); err == nil {
		t.Error("expected missing-recipient error")
	}
}

type failingSender struct{ err error }

func (f failingSender) Send(ctx context.Context, to, subject, textBody, htmlBody string) error {
	return f.err
}

// TestGenerateSendFailureDoesNotFailGeneration: when email delivery fails
// after the persistence transaction commits, Generate still returns the
// digest result and a nil error.
func TestGenerateSendFailureDoesNotFailGeneration(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "email-fail@test.com", PasswordHash: "hash", ApiKey: "email-fail-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Category: "Tech", Type: "rss",
		Name: "RSS", Url: "https://example.com/rss", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	if _, err := queries.CreateItem(ctx, store.CreateItemParams{
		FeedID: feedObj.ID, Hash: "email-fail-1", Title: "Item", Url: "https://example.com/1", Status: "unread",
	}); err != nil {
		t.Fatalf("create item: %v", err)
	}

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Schedule: "0 8 * * *",
		Config:   `{"feeds":[{"id":` + itoa(feedObj.ID) + `,"highlights":5,"comments":5}],"send_email":true,"email_to":"dest@example.com"}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	g := &Generator{
		db:      db,
		queries: queries,
		llm:     fakeLLM{resp: `{"title":"T","summary":"s","highlights":[]}`},
		enrichers: map[string]feed.Enricher{
			"rss": noopEnricher{},
		},
		commentFetchers: map[string]feed.CommentFetcher{},
	}
	g.SetEmailSender(failingSender{err: errors.New("smtp down")})

	result, err := g.Generate(ctx, cfg)
	if err != nil {
		t.Fatalf("Generate failed despite email send error: %v", err)
	}
	if result == nil || result.ID == 0 {
		t.Fatal("expected persisted digest result")
	}
}

// TestGenerateSendsEmail verifies the happy path: the sender is called with
// the configured recipient and a non-empty subject.
func TestGenerateSendsEmail(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "email-ok@test.com", PasswordHash: "hash", ApiKey: "email-ok-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Category: "Tech", Type: "rss",
		Name: "RSS", Url: "https://example.com/rss", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	if _, err := queries.CreateItem(ctx, store.CreateItemParams{
		FeedID: feedObj.ID, Hash: "email-ok-1", Title: "Item", Url: "https://example.com/1", Status: "unread",
	}); err != nil {
		t.Fatalf("create item: %v", err)
	}

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Schedule: "0 8 * * *",
		Config:   `{"feeds":[{"id":` + itoa(feedObj.ID) + `,"highlights":5,"comments":5}],"send_email":true,"email_to":"dest@example.com"}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	var sentTo, sentSubject string
	g := &Generator{
		db:      db,
		queries: queries,
		llm:     fakeLLM{resp: `{"title":"Digest Title","summary":"s","highlights":[]}`},
		enrichers: map[string]feed.Enricher{
			"rss": noopEnricher{},
		},
		commentFetchers: map[string]feed.CommentFetcher{},
	}
	g.SetEmailSender(senderFunc(func(ctx context.Context, to, subject, text, html string) error {
		sentTo, sentSubject = to, subject
		return nil
	}))

	if _, err := g.Generate(ctx, cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if sentTo != "dest@example.com" {
		t.Errorf("to = %q", sentTo)
	}
	if sentSubject == "" {
		t.Error("expected non-empty subject")
	}
}

// senderFunc adapts a function to emailSender.
type senderFunc func(ctx context.Context, to, subject, textBody, htmlBody string) error

func (f senderFunc) Send(ctx context.Context, to, subject, textBody, htmlBody string) error {
	return f(ctx, to, subject, textBody, htmlBody)
}
