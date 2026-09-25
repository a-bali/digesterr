package feed

import (
	"testing"

	"digesterr/internal/store"
)

// The sidebar staleness report: a feed's error mark did not disappear after the
// condition cleared. Two independent causes, both fixed here.

// TestPollFeedClearsWarningOnSuccess pins the backend half: a WARNING-severity
// last_error used to be permanent.
//
// pollFeed cleared the error only inside `if feed.Status == "error"`, but
// UpdateFeedWarning writes last_error_severity='warning' WITHOUT ever touching
// status. So an enrichment warning survived every subsequent successful poll and
// had no other clear path in the codebase — the amber mark stayed on a feed that
// had fully recovered.
func TestPollFeedClearsWarningOnSuccess(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	rssSrv := rssBodyServer(t, testRSS)

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Warned Feed",
		Url:    rssSrv.URL,
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	// The real producer of severity='warning': an enrichment failure.
	if err := queries.UpdateFeedWarning(t.Context(), store.UpdateFeedWarningParams{
		ID:    feedObj.ID,
		Error: "enrichment failed for 3 items",
	}); err != nil {
		t.Fatalf("set warning: %v", err)
	}

	// Precondition: the warning is stored and status was NOT flipped, which is
	// exactly why the old status-gated clear never ran for it.
	warned, err := queries.GetFeed(t.Context(), store.GetFeedParams{ID: feedObj.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if warned.LastError == "" || warned.LastErrorSeverity != "warning" {
		t.Fatalf("precondition: last_error=%q severity=%q, want a warning",
			warned.LastError, warned.LastErrorSeverity)
	}
	if warned.Status != "active" {
		t.Fatalf("precondition: status = %q, want 'active'", warned.Status)
	}

	// A fully successful poll.
	poller := NewPoller(queries)
	if err := poller.PollFeed(t.Context(), warned); err != nil {
		t.Fatalf("poll: %v", err)
	}

	feed, err := queries.GetFeed(t.Context(), store.GetFeedParams{ID: feedObj.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if feed.LastError != "" {
		t.Errorf("last_error = %q after a successful poll, want empty (a warning must not be permanent)", feed.LastError)
	}
	if feed.LastErrorSeverity != "" {
		t.Errorf("last_error_severity = %q after a successful poll, want empty", feed.LastErrorSeverity)
	}
	// The feed was never in error, so it must stay active — the clear is about
	// the mark, not the lifecycle state.
	if feed.Status != "active" {
		t.Errorf("status = %q, want 'active'", feed.Status)
	}
}

// TestPollFeedClearsErrorWhoseRowWasReadEarlier pins the second, subtler half.
//
// The poller's success path must clear the error even when the `feed` struct it
// was handed predates the error. Production callers (PollAll, the UI refresh
// handler) reload the row first, but that is a property of the CALLERS, not of
// this code — and relying on it made the clear silently do nothing whenever a
// caller held a stale snapshot. The clear is now unconditional and idempotent in
// SQL, so it cannot depend on what the caller happened to have read.
func TestPollFeedClearsErrorWhoseRowWasReadEarlier(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	rssSrv := rssBodyServer(t, testRSS)

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Stale Snapshot Feed",
		Url:    rssSrv.URL,
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	// Read the feed BEFORE the error is recorded: this is the stale snapshot a
	// caller may legitimately be holding.
	stale, err := queries.GetFeed(t.Context(), store.GetFeedParams{ID: feedObj.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if stale.LastError != "" {
		t.Fatalf("precondition: snapshot should have no error, got %q", stale.LastError)
	}

	// The error lands after that read (a concurrent poll failing).
	if err := queries.UpdateFeedError(t.Context(), store.UpdateFeedErrorParams{
		ID:    feedObj.ID,
		Error: "rate limited",
	}); err != nil {
		t.Fatalf("set error: %v", err)
	}

	// Poll with the STALE struct. The row's error must still be cleared.
	poller := NewPoller(queries)
	if err := poller.PollFeed(t.Context(), stale); err != nil {
		t.Fatalf("poll: %v", err)
	}

	feed, err := queries.GetFeed(t.Context(), store.GetFeedParams{ID: feedObj.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if feed.LastError != "" {
		t.Errorf("last_error = %q, want empty: the clear must not depend on the caller's snapshot", feed.LastError)
	}
	if feed.LastErrorSeverity != "" {
		t.Errorf("last_error_severity = %q, want empty", feed.LastErrorSeverity)
	}
}

// TestSuccessfulPollLeavesAPausedFeedPaused guards the split between the error
// clear and the status reset: clearing the mark must not resurrect a feed the
// user paused.
func TestSuccessfulPollLeavesAPausedFeedPaused(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	rssSrv := rssBodyServer(t, testRSS)

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Paused Feed",
		Url:    rssSrv.URL,
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	if err := queries.UpdateFeedWarning(t.Context(), store.UpdateFeedWarningParams{
		ID: feedObj.ID, Error: "enrichment failed for 1 items",
	}); err != nil {
		t.Fatalf("set warning: %v", err)
	}
	// The user pauses the feed while it carries a warning.
	if _, err := queries.UpdateFeedStatus(t.Context(), store.UpdateFeedStatusParams{
		ID: feedObj.ID, Status: "paused",
	}); err != nil {
		t.Fatalf("pause feed: %v", err)
	}

	f, err := queries.GetFeed(t.Context(), store.GetFeedParams{ID: feedObj.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	poller := NewPoller(queries)
	if err := poller.PollFeed(t.Context(), f); err != nil {
		t.Fatalf("poll: %v", err)
	}

	after, err := queries.GetFeed(t.Context(), store.GetFeedParams{ID: feedObj.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if after.Status != "paused" {
		t.Errorf("status = %q, want 'paused' — a poll must not unpause a feed", after.Status)
	}
	if after.LastError != "" {
		t.Errorf("last_error = %q, want empty — the mark is cleared independently of status", after.LastError)
	}
}
