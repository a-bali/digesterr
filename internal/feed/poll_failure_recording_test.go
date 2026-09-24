package feed

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"digesterr/internal/store"
	"digesterr/internal/testutil"
)

// setupFailureDB returns a raw handle (to install triggers), queries and a feed.
func setupFailureDB(t *testing.T) (*sql.DB, *store.Queries, store.Feed) {
	t.Helper()
	db, err := testutil.OpenTestDB()
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	queries := store.New(db)

	user, err := queries.CreateUser(t.Context(), store.CreateUserParams{
		Email:        "poll-failure@example.com",
		PasswordHash: "hash",
		ApiKey:       "poll-failure-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Poll Failure Feed",
		Url:    "https://example.com/failure.xml",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	return db, queries, feedObj
}

// TestPollWithFailedInsertsIsNotRecordedAsSuccess: a poll whose item writes all
// fail must not be recorded as a success. Today it returns nil, clears the
// backoff and stamps last_fetched_at, so a feed that can never store anything
// looks perfectly healthy.
func TestPollWithFailedInsertsIsNotRecordedAsSuccess(t *testing.T) {
	db, queries, feedObj := setupFailureDB(t)

	rssSrv := rssBodyServer(t, scoreRSSBody([]string{"https://example.com/fail/a"}))
	feedObj = setFeedURLAndScore(t, queries, feedObj, rssSrv.URL, 0)

	// Every item insert fails, exactly like a full disk or a broken table.
	if _, err := db.Exec(`CREATE TRIGGER fail_item_insert BEFORE INSERT ON items
BEGIN
  SELECT RAISE(ABORT, 'simulated write failure');
END`); err != nil {
		t.Fatalf("install trigger: %v", err)
	}

	poller := NewPoller(queries)
	err := poller.PollFeed(context.Background(), feedObj)
	if err == nil {
		t.Error("PollFeed returned nil although every item write failed: the poll was recorded as a success")
	}

	got, err := queries.GetFeedByID(context.Background(), feedObj.ID)
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if got.Status != "error" {
		t.Errorf("feed status = %q, want \"error\" after failed writes", got.Status)
	}
	if got.LastError == "" {
		t.Error("last_error is empty after failed writes: the failure is invisible to the user")
	}
	if got.FailCount == 0 {
		t.Error("fail_count = 0 after failed writes: no backoff was applied")
	}
	if got.LastFetchedAt.Valid {
		t.Error("last_fetched_at was stamped despite failed writes: the poll claims success")
	}
}

// TestPollTimeoutRecordsBackoff: a feed that hangs until the per-feed poll
// timeout must be backed off. Today the backoff write is issued through the
// already-expired poll context, so the driver rejects it and a permanently
// hanging feed is retried every single cycle forever.
func TestPollTimeoutRecordsBackoff(t *testing.T) {
	_, queries, feedObj := setupFailureDB(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // never answer: force the poll timeout
	}))
	t.Cleanup(srv.Close)

	feedObj = setFeedURLAndScore(t, queries, feedObj, srv.URL, 0)

	poller := NewPoller(queries)
	poller.SetPollTimeout(500 * time.Millisecond)
	poller.PollAll(context.Background())

	got, err := queries.GetFeedByID(context.Background(), feedObj.ID)
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if got.FailCount == 0 {
		t.Error("a timed-out feed was not backed off: fail_count = 0 (expired poll context rejected the write)")
	}
	if !got.NextPollAt.Valid {
		t.Error("a timed-out feed has no next_poll_at: it will be retried on every cycle")
	}
}

// TestShutdownDoesNotMarkHealthyFeedFailed pins the CORRECT behaviour that the
// bug report claims is broken. A caller-cancelled context (SIGTERM, client
// disconnect) means the feed is fine, so nothing may be recorded: no failure,
// no status change, no backoff.
func TestShutdownDoesNotMarkHealthyFeedFailed(t *testing.T) {
	_, queries, feedObj := setupFailureDB(t)

	entered := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	feedObj = setFeedURLAndScore(t, queries, feedObj, srv.URL, 0)

	ctx, cancel := context.WithCancel(context.Background())
	poller := NewPoller(queries)
	poller.SetPollTimeout(30 * time.Second)

	done := make(chan struct{})
	go func() { poller.PollAll(ctx); close(done) }()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("fetch never started")
	}
	cancel() // SIGTERM while the fetch is in flight

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("PollAll did not return after cancel")
	}

	got, err := queries.GetFeedByID(context.Background(), feedObj.ID)
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if got.FailCount != 0 {
		t.Errorf("fail_count = %d after shutdown, want 0 (a cancelled poll is not a feed failure)", got.FailCount)
	}
	if got.Status != "active" {
		t.Errorf("feed status = %q after shutdown, want \"active\"", got.Status)
	}
	if got.LastError != "" {
		t.Errorf("last_error = %q after shutdown, want empty", got.LastError)
	}
}
