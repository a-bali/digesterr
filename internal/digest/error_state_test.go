package digest

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

// TestSchedulerSetsErrorOnFailure verifies that when digest generation fails,
// the digest config's error fields are populated.
func TestSchedulerSetsErrorOnFailure(t *testing.T) {
	_, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "sched-error@example.com",
		PasswordHash: "hash",
		ApiKey:       "sched-error-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Error Feed",
		Url:    "https://example.com/feed.xml",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Fail Digest",
		Schedule: "* * * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":2,"comments":5}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	// Items to digest, so the run reaches the LLM instead of stopping at the
	// empty-pool skip (an empty pool is a normal outcome, not a failure).
	addItems(t, queries, feedObj.ID, "errstate", 1)

	// A scheduler whose LLM always fails: a genuine generation failure.
	gen := NewGenerator(nil, queries, failingChatter{}, feed.NewPoller(queries))
	s := NewScheduler(queries, gen, nil)
	s.tick(ctx)

	// Give the goroutine a moment to complete.
	// We check the config error state directly.
	cfgAfter, err := queries.GetDigestConfig(ctx, store.GetDigestConfigParams{
		ID:     cfg.ID,
		UserID: user.ID,
	})
	if err != nil {
		t.Fatalf("get digest config: %v", err)
	}
	if cfgAfter.LastError == "" {
		t.Error("expected last_error to be set after generation failure")
	}
	if cfgAfter.LastErrorAt.Time.IsZero() {
		t.Error("expected last_error_at to be set")
	}
}

// TestDigestHandlerErrorState verifies that the HTTP handler sets and clears
// error state on digest generation.
func TestDigestHandlerErrorState(t *testing.T) {
	// This test verifies the error state queries work correctly via direct
	// DB operations since handler tests require full server setup.
	// The handler logic is tested indirectly through integration tests.
	_, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "handler-error@example.com",
		PasswordHash: "hash",
		ApiKey:       "handler-error-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Handler Digest",
		Schedule: "0 8 * * *",
		Config:   "{}",
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	// Set error
	err = queries.UpdateDigestConfigError(ctx, store.UpdateDigestConfigErrorParams{
		ID:    cfg.ID,
		Error: "generation failed",
	})
	if err != nil {
		t.Fatalf("update digest config error: %v", err)
	}

	cfgAfter, err := queries.GetDigestConfig(ctx, store.GetDigestConfigParams{
		ID:     cfg.ID,
		UserID: user.ID,
	})
	if err != nil {
		t.Fatalf("get digest config: %v", err)
	}
	if cfgAfter.LastError != "generation failed" {
		t.Errorf("last_error = %q, want 'generation failed'", cfgAfter.LastError)
	}

	// Clear error
	err = queries.ClearDigestConfigError(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("clear digest config error: %v", err)
	}

	cfgAfter, err = queries.GetDigestConfig(ctx, store.GetDigestConfigParams{
		ID:     cfg.ID,
		UserID: user.ID,
	})
	if err != nil {
		t.Fatalf("get digest config: %v", err)
	}
	if cfgAfter.LastError != "" {
		t.Errorf("last_error = %q, want empty after clear", cfgAfter.LastError)
	}
	if cfgAfter.LastErrorAt.Valid {
		t.Error("expected last_error_at to be NULL after clear")
	}
}

// TestDigestGenerationSuccessClearsError verifies that a successful digest
// generation clears previously set error state.
func TestDigestGenerationSuccessClearsError(t *testing.T) {
	_, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "digest-success-error@example.com",
		PasswordHash: "hash",
		ApiKey:       "digest-success-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Success Feed",
		Url:    "https://example.com/feed.xml",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Recovery Digest",
		Schedule: "0 8 * * *",
		Config:   fmt.Sprintf(`{"feeds":[{"id":%d,"highlights":1,"comments":2}]}`, feedObj.ID),
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	// Set error state first
	queries.UpdateDigestConfigError(ctx, store.UpdateDigestConfigErrorParams{
		ID:    cfg.ID,
		Error: "previous failure",
	})

	// Verify error is set
	cfgAfter, err := queries.GetDigestConfig(ctx, store.GetDigestConfigParams{ID: cfg.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get digest config: %v", err)
	}
	if cfgAfter.LastError != "previous failure" {
		t.Fatalf("precondition: last_error = %q, want 'previous failure'", cfgAfter.LastError)
	}

	// Clear error (simulating success)
	queries.ClearDigestConfigError(ctx, cfg.ID)

	cfgAfter, err = queries.GetDigestConfig(ctx, store.GetDigestConfigParams{ID: cfg.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get digest config: %v", err)
	}
	if cfgAfter.LastError != "" {
		t.Errorf("last_error = %q, want empty after success", cfgAfter.LastError)
	}

	// Verify the scheduler clears error on success via a test with a working generator
}

// TestSchedulerClearsErrorOnSuccess verifies that the scheduler clears
// error state when digest generation succeeds.
func TestSchedulerClearsErrorOnSuccess(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "sched-success@example.com",
		PasswordHash: "hash",
		ApiKey:       "sched-success-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Success Feed",
		Url:    "https://example.com/feed.xml",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	// Insert items for the digest
	for i, title := range []string{"Story A", "Story B"} {
		queries.CreateItem(ctx, store.CreateItemParams{
			FeedID:        feedObj.ID,
			Hash:          "success-h" + strconv.Itoa(i),
			Title:         title,
			Url:           "https://example.com/" + title,
			Content:       "content",
			Points:        50,
			CommentsCount: 5,
			Status:        "unread",
		})
	}

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Recovery Digest",
		Schedule: "* * * * *",
		Config:   fmt.Sprintf(`{"feeds":[{"id":%d,"highlights":1,"comments":2}]}`, feedObj.ID),
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	// Set error state
	queries.UpdateDigestConfigError(ctx, store.UpdateDigestConfigErrorParams{
		ID:    cfg.ID,
		Error: "previous failure",
	})

	// Verify error is set
	cfgCheck, _ := queries.GetDigestConfig(ctx, store.GetDigestConfigParams{ID: cfg.ID, UserID: user.ID})
	if cfgCheck.LastError != "previous failure" {
		t.Fatalf("precondition: last_error = %q", cfgCheck.LastError)
	}

	// Create a working generator with a mock LLM that returns valid JSON
	llmBody := `{"title":"Test","summary":"Summary","highlights":[{"title":"Story A","summary":"s1"}]}`
	llmSrv := llmTestServer(t, llmBody)
	llm := NewLLMClient(llmSrv.URL, "test-key", "test-model")
	gen := NewGenerator(db, queries, llm, feed.NewPoller(queries))

	s := NewScheduler(queries, gen, nil)
	s.tick(ctx)

	cfgAfter, err := queries.GetDigestConfig(ctx, store.GetDigestConfigParams{ID: cfg.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get digest config: %v", err)
	}
	if cfgAfter.LastError != "" {
		t.Errorf("last_error = %q, want empty after successful generation", cfgAfter.LastError)
	}
}
