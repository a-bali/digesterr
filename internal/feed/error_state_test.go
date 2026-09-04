package feed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"digesterr/internal/store"
)

// TestPollFeedSetsErrorOnFetchFailure verifies that when fetcher.Fetch()
// fails, the feed's last_error and last_error_severity are populated.
func TestPollFeedSetsErrorOnFetchFailure(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)

	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server error"))
	}))
	defer failSrv.Close()

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Failing Feed",
		Url:    failSrv.URL,
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	poller := NewPoller(queries)
	if err := poller.PollFeed(context.Background(), feedObj); err == nil {
		t.Fatal("expected PollFeed to fail for broken server")
	}

	feed, err := queries.GetFeed(t.Context(), store.GetFeedParams{ID: feedObj.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if feed.LastError == "" {
		t.Error("expected last_error to be set after fetch failure")
	}
	if feed.LastErrorSeverity != "error" {
		t.Errorf("last_error_severity = %q, want 'error'", feed.LastErrorSeverity)
	}
	if feed.LastErrorAt.Time.IsZero() {
		t.Error("expected last_error_at to be set")
	}
	if feed.Status != "error" {
		t.Errorf("status = %q, want 'error'", feed.Status)
	}
}

// TestPollFeedClearsErrorOnSuccess verifies that when a feed recovers from
// error state (previously had status='error'), the error fields are cleared.
func TestPollFeedClearsErrorOnSuccess(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	rssSrv := rssBodyServer(t, testRSS)

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Recovering Feed",
		Url:    rssSrv.URL,
		Config: "{}",
		Status: "error",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	// Manually set an error first
	queries.UpdateFeedError(t.Context(), store.UpdateFeedErrorParams{
		ID:    feedObj.ID,
		Error: "previous error",
	})

	// Verify error is set
	feed, err := queries.GetFeed(t.Context(), store.GetFeedParams{ID: feedObj.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if feed.LastError != "previous error" {
		t.Fatalf("precondition: last_error = %q, want 'previous error'", feed.LastError)
	}

	poller := NewPoller(queries)
	if err := poller.PollFeed(t.Context(), feedObj); err != nil {
		t.Fatalf("PollFeed failed: %v", err)
	}

	feed, err = queries.GetFeed(t.Context(), store.GetFeedParams{ID: feedObj.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if feed.LastError != "" {
		t.Errorf("last_error = %q, want empty after recovery", feed.LastError)
	}
	if feed.LastErrorSeverity != "" {
		t.Errorf("last_error_severity = %q, want empty after recovery", feed.LastErrorSeverity)
	}
	if feed.Status != "active" {
		t.Errorf("status = %q, want 'active' after recovery", feed.Status)
	}
}

// TestEnrichmentWarningDoesNotOverwriteFetchError verifies that an
// enrichment warning only writes when no fetch error exists.
func TestEnrichmentWarningDoesNotOverwriteFetchError(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	rssSrv := rssBodyServer(t, testRSS)

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Warning Feed",
		Url:    rssSrv.URL,
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	// Simulate an existing fetch error
	queries.UpdateFeedError(t.Context(), store.UpdateFeedErrorParams{
		ID:    feedObj.ID,
		Error: "fetch failed",
	})

	// Verify the error is set
	feed, err := queries.GetFeed(t.Context(), store.GetFeedParams{ID: feedObj.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if feed.LastError != "fetch failed" {
		t.Fatalf("precondition: last_error = %q, want 'fetch failed'", feed.LastError)
	}

	// Attempt to write a warning — should be no-op because error exists
	queries.UpdateFeedWarning(t.Context(), store.UpdateFeedWarningParams{
		ID:    feedObj.ID,
		Error: "enrichment warning",
	})

	// Verify the error is NOT overwritten
	feed, err = queries.GetFeed(t.Context(), store.GetFeedParams{ID: feedObj.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if feed.LastError != "fetch failed" {
		t.Errorf("last_error = %q, want 'fetch failed' (warning should not overwrite)", feed.LastError)
	}
	if feed.LastErrorSeverity != "error" {
		t.Errorf("last_error_severity = %q, want 'error' (should not change)", feed.LastErrorSeverity)
	}
}

// TestEnrichmentWarningWritesWhenClean verifies that UpdateFeedWarning
// writes when no fetch error or warning exists.
func TestEnrichmentWarningWritesWhenClean(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	rssSrv := rssBodyServer(t, testRSS)

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Clean Feed",
		Url:    rssSrv.URL,
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	// Write a warning on a clean feed
	queries.UpdateFeedWarning(t.Context(), store.UpdateFeedWarningParams{
		ID:    feedObj.ID,
		Error: "enrichment issue",
	})

	feed, err := queries.GetFeed(t.Context(), store.GetFeedParams{ID: feedObj.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if feed.LastError != "enrichment issue" {
		t.Errorf("last_error = %q, want 'enrichment issue'", feed.LastError)
	}
	if feed.LastErrorSeverity != "warning" {
		t.Errorf("last_error_severity = %q, want 'warning'", feed.LastErrorSeverity)
	}
}
