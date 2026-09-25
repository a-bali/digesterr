package digest

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

// An empty item pool is a normal outcome, not a failure. Before this, a config
// whose items were all already read/digested — a quiet stretch, or a catch-up
// run hours after the occurrence it is recovering — was recorded as an error and
// the sidebar showed a red mark on a config that was working perfectly.

// TestEmptyPoolIsNotAnError pins the sentinel contract at the generator.
func TestEmptyPoolIsNotAnError(t *testing.T) {
	_, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "empty-pool@example.com",
		PasswordHash: "hash",
		ApiKey:       "empty-...-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Empty Feed",
		Url: "https://example.com/empty.xml", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	// No items at all: the pool is empty.
	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Quiet Config",
		Schedule: "0 5 * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":1,"comments":0}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	gen := NewGenerator(nil, queries, fakeLLM{resp: `{"title":"X","summary":"s","highlights":[]}`}, feed.NewPoller(queries))
	_, err = gen.Generate(ctx, cfg)
	if !errors.Is(err, ErrNoUnreadItems) {
		t.Fatalf("Generate on an empty pool = %v, want ErrNoUnreadItems", err)
	}
}

// TestSchedulerEmptyPoolDoesNotRecordError is the regression guard for the
// production symptom: the catch-up run reported "no unread items found" and the
// config carried a last_error that nothing would clear — a red mark on a healthy
// config, reading as a broken app on a quiet morning.
func TestSchedulerEmptyPoolDoesNotRecordError(t *testing.T) {
	_, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "empty-sched@example.com",
		PasswordHash: "hash",
		ApiKey:       "empty-...-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Empty Feed",
		Url: "https://example.com/empty.xml", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Quiet Config",
		Schedule: "* * * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":1,"comments":0}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	gen := NewGenerator(nil, queries, fakeLLM{resp: `{"title":"X","summary":"s","highlights":[]}`}, feed.NewPoller(queries))
	s := NewScheduler(queries, gen, time.UTC)
	s.tick(ctx)

	after, err := queries.GetDigestConfig(ctx, store.GetDigestConfigParams{ID: cfg.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get digest config: %v", err)
	}
	if after.LastError != "" {
		t.Errorf("last_error = %q, want empty: an empty item pool is not a failure", after.LastError)
	}
	if after.LastErrorAt.Valid {
		t.Errorf("last_error_at = %v, want unset", after.LastErrorAt.Time)
	}
}

// TestSchedulerEmptyPoolClearsAStaleError verifies the empty-pool run also
// REMOVES a pre-existing error. This is the part that fixes the production
// state: the config already carried the bogus "no unread items" error, so a
// run that merely declined to record a new one would leave the red mark in
// place indefinitely.
func TestSchedulerEmptyPoolClearsAStaleError(t *testing.T) {
	_, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "empty-stale@example.com",
		PasswordHash: "hash",
		ApiKey:       "empty-...-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Empty Feed",
		Url: "https://example.com/empty.xml", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Quiet Config",
		Schedule: "* * * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":1,"comments":0}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	// The bogus error the old code stored.
	if err := queries.UpdateDigestConfigError(ctx, store.UpdateDigestConfigErrorParams{
		ID:    cfg.ID,
		Error: "no unread items found for digest config 1",
	}); err != nil {
		t.Fatalf("seed error: %v", err)
	}

	gen := NewGenerator(nil, queries, fakeLLM{resp: `{"title":"X","summary":"s","highlights":[]}`}, feed.NewPoller(queries))
	s := NewScheduler(queries, gen, time.UTC)
	s.tick(ctx)

	after, err := queries.GetDigestConfig(ctx, store.GetDigestConfigParams{ID: cfg.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get digest config: %v", err)
	}
	if after.LastError != "" {
		t.Errorf("last_error = %q, want empty: a quiet run must clear the stale mark", after.LastError)
	}
}

// TestSchedulerRealFailureStillRecordsError guards the other direction: with the
// empty-pool case reclassified, a GENUINE failure must still be recorded, or the
// fix would have silenced the error mark entirely.
func TestSchedulerRealFailureStillRecordsError(t *testing.T) {
	_, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "real-fail@example.com",
		PasswordHash: "hash",
		ApiKey:       "real-...-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Feed",
		Url: "https://example.com/f.xml", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	// An item, so the run reaches the LLM and fails THERE (not at the empty pool).
	addItems(t, queries, feedObj.ID, "realfail", 1)

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Failing Config",
		Schedule: "* * * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":1,"comments":0}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	gen := NewGenerator(nil, queries, failingChatter{}, feed.NewPoller(queries))
	s := NewScheduler(queries, gen, time.UTC)
	s.tick(ctx)

	after, err := queries.GetDigestConfig(ctx, store.GetDigestConfigParams{ID: cfg.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get digest config: %v", err)
	}
	if after.LastError == "" {
		t.Error("last_error is empty after a real generation failure; the reclassification must not swallow genuine errors")
	}
}
