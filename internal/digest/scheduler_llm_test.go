package digest

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

// recordingChatter is a Chatter that records the user it was resolved for
// and answers with a distinctive digest title, so tests can tell which
// client served the generation.
type recordingChatter struct {
	userID int64
	title  string
	seen   *int64
}

func (f recordingChatter) Chat(_ context.Context, _, _ string) (string, error) {
	if f.seen != nil {
		*f.seen = f.userID
	}
	return `{"title":` + strconv.Quote(f.title) + `,"summary":"s","highlights":[]}`, nil
}

// TestSchedulerUsesPerUserLLMResolver is the regression guard for the prod
// incident where the UI showed virtual-regular but every scheduled digest
// ran on the instance default (virtual-free): tick must resolve the config
// owner's LLM client and pass it to Generate.
func TestSchedulerUsesPerUserLLMResolver(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "sched-llm@example.com",
		PasswordHash: "hash",
		ApiKey:       "sched-llm-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Sched LLM Feed",
		Url:    "https://example.com/sched-llm.xml",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	for i, title := range []string{"Sched LLM A", "Sched LLM B"} {
		if _, err := queries.CreateItem(ctx, store.CreateItemParams{
			FeedID: feedObj.ID,
			Hash:   "sched-llm-" + strconv.Itoa(i),
			Title:  title,
			Url:    "https://example.com/sched-llm-" + strconv.Itoa(i),
			Status: "unread",
		}); err != nil {
			t.Fatalf("create item: %v", err)
		}
	}

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Sched LLM Digest",
		Schedule: "* * * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":2,"comments":0}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	poller := feed.NewPoller(queries)
	gen := NewGenerator(db, queries,
		fakeLLM{resp: `{"title":"DEFAULT-MODEL","summary":"s","highlights":[]}`},
		poller)

	var resolvedFor int64
	s := NewScheduler(queries, gen, nil)
	s.SetLLMResolver(func(ctx context.Context, userID int64) Chatter {
		return recordingChatter{userID: userID, title: "OWNER-MODEL", seen: &resolvedFor}
	})
	s.tick(ctx)

	if resolvedFor != user.ID {
		t.Errorf("resolver got userID = %d, want config owner %d", resolvedFor, user.ID)
	}

	recently, err := queries.GetLatestDigestResult(ctx, store.GetLatestDigestResultParams{
		ConfigID: cfg.ID,
		UserID:   user.ID,
	})
	if err != nil || recently.ID == 0 {
		t.Fatalf("no digest result after tick: %v", err)
	}
	var content digestResultContent
	if err := json.Unmarshal([]byte(recently.Content), &content); err != nil {
		t.Fatalf("unmarshal content: %v", err)
	}
	if content.Title != "OWNER-MODEL" {
		t.Errorf("digest title = %q, want %q (owner's resolved client, not the generator default)",
			content.Title, "OWNER-MODEL")
	}
}

// TestSchedulerWithoutResolverUsesDefault pins the fallback: a scheduler
// with no resolver behaves as before and generates on the default client.
func TestSchedulerWithoutResolverUsesDefault(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "sched-default@example.com",
		PasswordHash: "hash",
		ApiKey:       "sched-default-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Sched Default Feed",
		Url:    "https://example.com/sched-default.xml",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	if _, err := queries.CreateItem(ctx, store.CreateItemParams{
		FeedID: feedObj.ID,
		Hash:   "sched-default-0",
		Title:  "Sched Default A",
		Url:    "https://example.com/sched-default-0",
		Status: "unread",
	}); err != nil {
		t.Fatalf("create item: %v", err)
	}

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Sched Default Digest",
		Schedule: "* * * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":1,"comments":0}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	poller := feed.NewPoller(queries)
	gen := NewGenerator(db, queries,
		fakeLLM{resp: `{"title":"DEFAULT-MODEL","summary":"s","highlights":[]}`},
		poller)

	s := NewScheduler(queries, gen, nil)
	s.tick(ctx)

	recently, err := queries.GetLatestDigestResult(ctx, store.GetLatestDigestResultParams{
		ConfigID: cfg.ID,
		UserID:   user.ID,
	})
	if err != nil || recently.ID == 0 {
		t.Fatalf("no digest result after tick: %v", err)
	}
	var content digestResultContent
	if err := json.Unmarshal([]byte(recently.Content), &content); err != nil {
		t.Fatalf("unmarshal content: %v", err)
	}
	if content.Title != "DEFAULT-MODEL" {
		t.Errorf("digest title = %q, want %q (generator default, no resolver installed)",
			content.Title, "DEFAULT-MODEL")
	}
}
