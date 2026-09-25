package feed

import (
	"context"
	"testing"

	"digesterr/internal/store"
)

// TestRepolledScoredItemStillRefreshesScore is the regression for a candidate
// displacing the stored row during enrichment.
//
// A re-poll of the same story builds a candidate for it, so the hash is already
// in haveHash. The loop that assembles the list to enrich seeds itself from the
// candidates first, so the candidate's placeholder — which carries the RSS
// score and, crucially, id 0 — took the slot and the real row (with its id) was
// dropped. Enrichment then updated the placeholder, and every DB write that
// keys on the row id matched nothing: the stored score was never refreshed.
//
// The setup mirrors a story that entered the feed with no score data (stored
// with points 0), then gained popularity upstream.
func TestRepolledScoredItemStillRefreshesScore(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)

	url := "https://example.com/story/a"
	rssSrv := rssBodyServer(t, scoreRSSBody([]string{url}))
	t.Cleanup(rssSrv.Close)

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID:   user.ID,
		Type:     "hn",
		Name:     "Score Feed",
		Url:      rssSrv.URL,
		Config:   "{}",
		Status:   "active",
		MinScore: 0,
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	// The story is already stored, unenriched, exactly as an earlier poll
	// would have left it when the score API had no data.
	stored, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: feedObj.ID,
		Hash:   itemHash("guid-A", url, "Item A", ""),
		Title:  "Item A",
		Url:    url,
		Points: 0,
		Status: "unread",
	})
	if err != nil {
		t.Fatalf("create pre-existing item: %v", err)
	}

	// Upstream now reports a score for it.
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
		t.Errorf("stored points = %d, want 137: a re-poll must refresh the existing row's score, not enrich a placeholder", after.Points)
	}
	// The re-poll must not have created a second row for the same story.
	items, err := queries.ListItemsByFeed(t.Context(), feedObj.ID)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("items in feed = %d, want 1: a re-poll must not duplicate the story", len(items))
	}
}
