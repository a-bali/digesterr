package digest

import (
	"context"
	"strconv"
	"testing"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

// newEmailTestSetup creates a user + feed with 3 unread items and a digest
// config with the given config JSON, mirroring min_score_test.go setup.
func newEmailTestSetup(t *testing.T, configJSON string) (*store.Queries, store.DigestConfig, func()) {
	t.Helper()
	queries, cleanup := setupDigestTest(t)
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "email-gen@test.com", PasswordHash: "hash", ApiKey: "email-test-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Category: "Tech", Type: "hn",
		Name: "HN", Url: "https://news.ycombinator.com/rss", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := queries.CreateItem(ctx, store.CreateItemParams{
			FeedID: feedObj.ID, Hash: "email-" + strconv.Itoa(i),
			Title: "Item " + strconv.Itoa(i), Url: "https://example.com/" + strconv.Itoa(i),
			Points: int64(10 + i), Status: "unread",
		}); err != nil {
			t.Fatalf("create item %d: %v", i, err)
		}
	}
	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID: user.ID, Schedule: "0 8 * * *", Config: configJSON,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}
	return queries, cfg, cleanup
}

func newEmailTestGenerator(queries *store.Queries) *Generator {
	return &Generator{
		queries:         queries,
		llm:             fakeLLM{resp: `{"title":"Emailed","summary":"s","highlights":[]}`},
		enrichers:       map[string]feed.Enricher{},
		commentFetchers: map[string]feed.CommentFetcher{},
	}
}

func TestGenerateSendsEmailWhenEnabled(t *testing.T) {
	queries, cfg, cleanup := newEmailTestSetup(t, `{"feeds":[],"send_email":true,"email_to":"me@example.com"}`)
	defer cleanup()

	sender := &fakeSender{}
	g := newEmailTestGenerator(queries)
	g.sender = sender

	if _, err := g.Generate(context.Background(), cfg); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if sender.calls != 1 {
		t.Fatalf("expected 1 email send, got %d", sender.calls)
	}
	if sender.last.to != "me@example.com" {
		t.Errorf("to = %q", sender.last.to)
	}
	if sender.last.subject == "" || sender.last.text == "" || sender.last.html == "" {
		t.Errorf("empty email parts: subject=%q text=%q html=%q", sender.last.subject, sender.last.text, sender.last.html)
	}
}

func TestGenerateNoEmailWhenDisabled(t *testing.T) {
	queries, cfg, cleanup := newEmailTestSetup(t, `{"feeds":[]}`)
	defer cleanup()

	sender := &fakeSender{}
	g := newEmailTestGenerator(queries)
	g.sender = sender

	if _, err := g.Generate(context.Background(), cfg); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if sender.calls != 0 {
		t.Errorf("expected 0 sends with send_email disabled, got %d", sender.calls)
	}
}

func TestGenerateEmailFailureDoesNotFailGeneration(t *testing.T) {
	queries, cfg, cleanup := newEmailTestSetup(t, `{"feeds":[],"send_email":true,"email_to":"me@example.com"}`)
	defer cleanup()

	g := newEmailTestGenerator(queries)
	g.sender = &fakeSender{err: context.DeadlineExceeded}

	result, err := g.Generate(context.Background(), cfg)
	if err != nil {
		t.Fatalf("generation should succeed despite email failure: %v", err)
	}
	if result.ID == 0 {
		t.Error("expected digest result to be created")
	}
}
