package feed

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"digesterr/internal/store"
)

// mapEnricher assigns points/comments per item URL, simulating a source API
// (HN/Lobste.rs/Reddit). Items absent from the map keep Points == 0, which
// represents "no score data".
type mapEnricher struct {
	points map[string]int
}

func (m *mapEnricher) Enrich(_ context.Context, item *store.Item) error {
	if p, ok := m.points[item.Url]; ok {
		item.Points = int64(p)
		item.CommentsCount = int64(p / 10)
	}
	return nil
}

func (m *mapEnricher) BulkEnrich(_ context.Context, items []*store.Item) error {
	for _, it := range items {
		_ = m.Enrich(context.Background(), it)
	}
	return nil
}

func (m *mapEnricher) FetchComments(_ context.Context, _ store.Item, _ int) ([]Comment, error) {
	return nil, nil
}
func (m *mapEnricher) CommentTargetURL() string { return "" }

// scoreRSSBody renders an RSS document with one item per URL.
func scoreRSSBody(urls []string) string {
	body := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Score Feed</title>
    <link>https://example.com</link>
    <description>d</description>`
	for i, u := range urls {
		body += `
    <item>
      <title>Item ` + string(rune('A'+i)) + `</title>
      <link>` + u + `</link>
      <guid>guid-` + string(rune('A'+i)) + `</guid>
    </item>`
	}
	body += `
  </channel>
</rss>`
	return body
}

var scoreFeedURLs = []string{
	"https://example.com/story/a",
	"https://example.com/story/b",
	"https://example.com/story/c",
	"https://example.com/story/d",
}

// setupMinScoreFeed creates an hn-type feed served from an httptest RSS
// server with a mapEnricher registered, so the poller's pre-insert scoring
// path is exercised exactly like a real score feed.
func setupMinScoreFeed(t *testing.T, minScore int64, points map[string]int) (*store.Queries, store.Feed, *Poller, *mapEnricher) {
	t.Helper()
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	rssSrv := rssBodyServer(t, scoreRSSBody(scoreFeedURLs))
	t.Cleanup(func() { rssSrv.Close() })

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID:   user.ID,
		Type:     "hn",
		Name:     "Score Feed",
		Url:      rssSrv.URL,
		Config:   "{}",
		Status:   "active",
		MinScore: minScore,
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	poller := NewPoller(queries)
	enr := &mapEnricher{points: points}
	poller.enrichers["hn"] = enr
	return queries, feedObj, poller, enr
}

func titlesByFeed(t *testing.T, queries *store.Queries, feedID int64) map[string]store.Item {
	t.Helper()
	items, err := queries.ListItemsByFeed(t.Context(), feedID)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	byTitle := make(map[string]store.Item, len(items))
	for _, it := range items {
		byTitle[it.Title] = it
	}
	return byTitle
}

// TestPollFeedMinScoreRejectsBelowThreshold verifies the core filtering:
// items whose refreshed score is below the feed's min_score are not
// inserted, the boundary (score == min_score) is inserted, and items with no
// score data always pass.
func TestPollFeedMinScoreRejectsBelowThreshold(t *testing.T) {
	queries, feedObj, poller, _ := setupMinScoreFeed(t, 100, map[string]int{
		"https://example.com/story/a": 10,  // below threshold
		"https://example.com/story/b": 100, // exactly at the boundary
		"https://example.com/story/c": 150, // above threshold
		// story/d: absent from the map -> no score data, must pass
	})

	if err := poller.PollFeed(context.Background(), feedObj); err != nil {
		t.Fatalf("PollFeed failed: %v", err)
	}

	byTitle := titlesByFeed(t, queries, feedObj.ID)

	if _, ok := byTitle["Item A"]; ok {
		t.Error("item below min_score (10 < 100) was inserted")
	}
	if got := byTitle["Item B"].Points; got != 100 {
		t.Errorf("boundary item points = %d, want 100 (stored with refreshed score)", got)
	}
	if got := byTitle["Item C"].Points; got != 150 {
		t.Errorf("above-threshold item points = %d, want 150", got)
	}
	if _, ok := byTitle["Item D"]; ok {
		t.Error("item without score data should have been cleaned up by min_score filter")
	}
	if len(byTitle) != 2 {
		t.Errorf("expected 2 inserted items (A below threshold, D unenriched zero-score), got %d", len(byTitle))
	}
}

// TestPollFeedMinScoreNoScoreDataPassesWithoutEnricher verifies that a feed
// whose enricher fails or returns no scores (simulated by an empty points
// map) lets every item through — the threshold only applies when score data
// exists.
func TestPollFeedMinScoreNoScoreDataPassesWithoutEnricher(t *testing.T) {
	queries, feedObj, poller, _ := setupMinScoreFeed(t, 100, nil)

	if err := poller.PollFeed(context.Background(), feedObj); err != nil {
		t.Fatalf("PollFeed failed: %v", err)
	}

	items, err := queries.ListItemsByFeed(t.Context(), feedObj.ID)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items (enricher returned no scores, min_score cleans them up), got %d", len(items))
	}
}

// TestPollFeedMinScoreScoreRefreshOnSubsequentFetch verifies the "scores rise
// over time" behavior: an item skipped on the first poll because it was below
// the threshold is inserted on a later poll once its score has risen.
func TestPollFeedMinScoreScoreRefreshOnSubsequentFetch(t *testing.T) {
	queries, feedObj, poller, enr := setupMinScoreFeed(t, 100, map[string]int{
		"https://example.com/story/a": 10,
	})

	if err := poller.PollFeed(context.Background(), feedObj); err != nil {
		t.Fatalf("first PollFeed failed: %v", err)
	}
	if _, ok := titlesByFeed(t, queries, feedObj.ID)["Item A"]; ok {
		t.Fatal("item with score 10 was inserted on the first poll")
	}

	// The story gains popularity: score rises above the threshold.
	enr.points["https://example.com/story/a"] = 250

	if err := poller.PollFeed(context.Background(), feedObj); err != nil {
		t.Fatalf("second PollFeed failed: %v", err)
	}
	byTitle := titlesByFeed(t, queries, feedObj.ID)
	if got, ok := byTitle["Item A"]; !ok {
		t.Fatal("item whose score rose above min_score was not inserted on a subsequent fetch")
	} else if got.Points != 250 {
		t.Errorf("item points = %d, want 250 (score refreshed on fetch)", got.Points)
	}
}

// TestPollFeedMinScoreZeroKeepsAll verifies the default: min_score 0 disables
// the threshold entirely and items are inserted without a pre-insert scoring
// pass.
func TestPollFeedMinScoreZeroKeepsAll(t *testing.T) {
	queries, feedObj, poller, _ := setupMinScoreFeed(t, 0, map[string]int{
		"https://example.com/story/a": 10,
	})

	if err := poller.PollFeed(context.Background(), feedObj); err != nil {
		t.Fatalf("PollFeed failed: %v", err)
	}

	byTitle := titlesByFeed(t, queries, feedObj.ID)
	if _, ok := byTitle["Item A"]; !ok {
		t.Error("item was rejected even though min_score is 0 (threshold disabled)")
	}
	if len(byTitle) != len(scoreFeedURLs) {
		t.Errorf("expected %d items, got %d", len(scoreFeedURLs), len(byTitle))
	}
}

// TestPollFeedMinScoreRSSFeedAllowsAll verifies the documented behavior for
// non-score feed types: an rss feed configured with min_score still inserts
// every item because no score data exists (points are always 0 for RSS).
func TestPollFeedMinScoreRSSFeedAllowsAll(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	rssSrv := rssBodyServer(t, scoreRSSBody(scoreFeedURLs))
	t.Cleanup(func() { rssSrv.Close() })

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID:   user.ID,
		Type:     "rss",
		Name:     "RSS Score Feed",
		Url:      rssSrv.URL,
		Config:   "{}",
		Status:   "active",
		MinScore: 100,
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	poller := NewPoller(queries)
	if err := poller.PollFeed(context.Background(), feedObj); err != nil {
		t.Fatalf("PollFeed failed: %v", err)
	}

	items, err := queries.ListItemsByFeed(t.Context(), feedObj.ID)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if len(items) != len(scoreFeedURLs) {
		t.Errorf("expected %d items for an RSS feed with min_score set, got %d", len(scoreFeedURLs), len(items))
	}
}

// TestPollFeedMinScoreCleanupNeverDeletesPreExistingRows is the regression
// test for the prod resurrection loop (2026-09-18): the min_score cleanup
// deleted a READ row via (feed_id, hash) when the enricher missed it on a
// re-poll, and the next poll re-inserted it as unread — the user saw the
// same article come back as unread. The cleanup may only remove rows this
// poll inserted.
func TestPollFeedMinScoreCleanupNeverDeletesPreExistingRows(t *testing.T) {
	queries, feedObj, poller, enr := setupMinScoreFeed(t, 100, map[string]int{
		"https://example.com/story/a": 150, // scored above threshold
	})

	if err := poller.PollFeed(context.Background(), feedObj); err != nil {
		t.Fatalf("first PollFeed failed: %v", err)
	}
	byTitle := titlesByFeed(t, queries, feedObj.ID)
	itemA, ok := byTitle["Item A"]
	if !ok {
		t.Fatal("Item A was not inserted on the first poll")
	}

	// The user reads the item.
	if _, err := queries.UpdateItemStatus(t.Context(), store.UpdateItemStatusParams{
		ID:     itemA.ID,
		Status: "read",
	}); err != nil {
		t.Fatalf("mark read: %v", err)
	}

	// Next cycle the enricher misses every post (e.g. Reddit info.json
	// returns nothing for it). The re-poll must neither delete nor resurrect
	// the read row.
	enr.points = map[string]int{}
	if err := poller.PollFeed(context.Background(), feedObj); err != nil {
		t.Fatalf("second PollFeed failed: %v", err)
	}

	got, err := queries.GetItem(t.Context(), itemA.ID)
	if err != nil {
		t.Fatalf("pre-existing read row was deleted by the re-poll: %v", err)
	}
	if got.Status != "read" {
		t.Errorf("item status = %q, want %q (row must survive a scoreless re-poll)", got.Status, "read")
	}
	byTitle = titlesByFeed(t, queries, feedObj.ID)
	if len(byTitle) != 1 {
		// Only Item A may exist: B/C were never scored on the first poll
		// (inserted, then cleaned up) and stay that way. The count must not
		// grow — no duplicate/resurrected row.
		t.Errorf("expected 1 item after the scoreless re-poll, got %d items", len(byTitle))
	}
}

// TestListItemsByFeedSortedScoreDesc guards the sqlc query used by
// GET /feeds/:id/items?sort=score_desc with min_score filtering.
func TestListItemsByFeedSortedScoreDesc(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "hn",
		Name:   "Sorted Feed",
		Url:    "https://example.com/feed",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	pub := sql.NullTime{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}
	for i, p := range []int64{5, 50, 100, 0} {
		_, err := queries.CreateItem(t.Context(), store.CreateItemParams{
			FeedID:      feedObj.ID,
			Hash:        "h-" + string(rune('a'+i)),
			Title:       "Item " + string(rune('a'+i)),
			Url:         "https://example.com/" + string(rune('a'+i)),
			PublishedAt: pub,
			Points:      p,
			Status:      "unread",
		})
		if err != nil {
			t.Fatalf("create item: %v", err)
		}
	}

	items, err := queries.ListItemsByFeedSortedScoreDesc(t.Context(), store.ListItemsByFeedSortedScoreDescParams{
		FeedID:   feedObj.ID,
		MinScore: 50,
		Limit:    -1,
	})
	if err != nil {
		t.Fatalf("ListItemsByFeedSortedScoreDesc: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items at min_score 50, got %d", len(items))
	}
	if items[0].Points != 100 || items[1].Points != 50 {
		t.Errorf("score_desc order = [%d, %d], want [100, 50]", items[0].Points, items[1].Points)
	}
}
