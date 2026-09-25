package feed

import (
	"context"
	"testing"

	"digesterr/internal/store"
)

// Score refresh was fixed for rows the enrichment query happens to return, but
// that query is an ELIGIBILITY filter: status = 'unread' AND fetched_at >
// cutoff AND not recently enriched. A row that fails any of those — already
// read, older than the window, or enriched within the last hour — is left
// represented by the ID-less candidate placeholder, and enrichment updates the
// placeholder. Every DB write keys on the row id, so nothing is persisted: the
// stored score stays at its old value and the enriched score is discarded.
//
// A read story that gains popularity upstream is the common case: the user read
// it, and its score never moves again.
func TestRepolledReadItemStillRefreshesScore(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)

	url := "https://example.com/story/read"
	rssSrv := rssBodyServer(t, scoreRSSBody([]string{url}))

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "hn", Name: "Score Feed", Url: rssSrv.URL,
		Config: "{}", Status: "active", MinScore: 0,
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	// Stored, unenriched, and ALREADY READ — so ListUnreadItemsForEnrichment
	// cannot return it.
	stored, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: feedObj.ID,
		Hash:   itemHash("guid-A", url, "Item A", ""),
		Title:  "Item A",
		Url:    url,
		Points: 0,
		Status: "read",
	})
	if err != nil {
		t.Fatalf("create pre-existing read item: %v", err)
	}

	poller := NewPoller(queries)
	poller.enrichers["hn"] = &mapEnricher{points: map[string]int{url: 137}}

	if err := poller.PollFeed(context.Background(), feedObj); err != nil {
		t.Fatalf("PollFeed failed: %v", err)
	}

	after, err := queries.GetItem(t.Context(), stored.ID)
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if after.Points != 137 {
		t.Errorf("stored points = %d, want 137: a re-poll must refresh a READ row's score, not enrich an id-less placeholder", after.Points)
	}
	// The status must not be disturbed by the score refresh.
	if after.Status != "read" {
		t.Errorf("status = %q, want \"read\": refreshing a score must not change read state", after.Status)
	}
}

// The same for a row that is unread but too OLD for the enrichment window: the
// window governs opportunistic refresh, not whether a score can be persisted.
func TestRepolledOldUnreadItemStillRefreshesScore(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)

	url := "https://example.com/story/old"
	rssSrv := rssBodyServer(t, scoreRSSBody([]string{url}))

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "hn", Name: "Score Feed", Url: rssSrv.URL,
		Config: "{}", Status: "active", MinScore: 0,
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	stored, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: feedObj.ID,
		Hash:   itemHash("guid-A", url, "Item A", ""),
		Title:  "Item A",
		Url:    url,
		Points: 0,
		Status: "unread",
	})
	if err != nil {
		t.Fatalf("create pre-existing old item: %v", err)
	}
	// Push fetched_at well outside the enrichment window.
	if _, err := queries.DB().ExecContext(t.Context(),
		"UPDATE items SET fetched_at = DATETIME('now', '-10 days') WHERE id = ?", stored.ID); err != nil {
		t.Fatalf("age the item: %v", err)
	}

	poller := NewPoller(queries)
	poller.enrichers["hn"] = &mapEnricher{points: map[string]int{url: 137}}

	if err := poller.PollFeed(context.Background(), feedObj); err != nil {
		t.Fatalf("PollFeed failed: %v", err)
	}

	after, err := queries.GetItem(t.Context(), stored.ID)
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if after.Points != 137 {
		t.Errorf("stored points = %d, want 137: a re-poll must refresh an old row's score", after.Points)
	}
}

// Refreshing read/old rows' scores must NOT make them eligible for the
// min_score cleanup, which exists to discard disposable rows the poll just
// inserted. A read row is one the user has already seen.
func TestScoreRefreshDoesNotDeleteBelowMinScoreReadRows(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)

	url := "https://example.com/story/low"
	rssSrv := rssBodyServer(t, scoreRSSBody([]string{url}))

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "hn", Name: "Score Feed", Url: rssSrv.URL,
		Config: "{}", Status: "active", MinScore: 100,
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	stored, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: feedObj.ID,
		Hash:   itemHash("guid-A", url, "Item A", ""),
		Title:  "Item A",
		Url:    url,
		Points: 0,
		Status: "read",
	})
	if err != nil {
		t.Fatalf("create pre-existing read item: %v", err)
	}

	poller := NewPoller(queries)
	// The enricher reports a score BELOW the feed's min_score.
	poller.enrichers["hn"] = &mapEnricher{points: map[string]int{url: 5}}

	if err := poller.PollFeed(context.Background(), feedObj); err != nil {
		t.Fatalf("PollFeed failed: %v", err)
	}

	if _, err := queries.GetItem(t.Context(), stored.ID); err != nil {
		t.Fatalf("a READ row was deleted by the min_score cleanup: %v", err)
	}
}
