package feed

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"digesterr/internal/store"
)

// setupEnrichTestDB creates an in-memory DB with an hn feed and returns the
// queries plus a poller wired to a stubbed HN API.
func setupEnrichmentWindowDB(t *testing.T) (*store.Queries, *Poller) {
	t.Helper()
	queries := setupFeedLoggingDB(t)
	return queries, NewPoller(queries)
}

// TestPostInsertRemovalBelowMinScore verifies that a NULL-scored item which
// enriches below min_score gets deleted rather than kept at a failing score.
func TestPostInsertRemovalBelowMinScore(t *testing.T) {
	var mu sync.Mutex
	firebaseCalls := 0
	hnServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		firebaseCalls++
		id := r.URL.Path
		mu.Unlock()
		if id == "/v0/item/55.json" {
			// below threshold
			w.Write([]byte(`{"id":55,"score":3,"descendants":1}`))
			return
		}
		w.Write([]byte(`null`))
	}))
	defer hnServer.Close()

	queries, poller := setupEnrichmentWindowDB(t)
	user := createFeedLoggingUser(t, queries)
	feedObj, err := queries.CreateFeed(context.Background(), store.CreateFeedParams{
		UserID:   user.ID,
		Type:     "hn",
		Name:     "HN",
		Url:      "https://example.com/hn",
		Config:   "{}",
		Status:   "active",
		MinScore: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Insert an unenriched (NULL) item directly — simulates outage-era insert.
	item, err := queries.CreateItem(context.Background(), store.CreateItemParams{
		FeedID:     feedObj.ID,
		Hash:       "h-null",
		Title:      "Below threshold",
		Url:        "https://news.ycombinator.com/item?id=55",
		Points:     nullInt64(),
		Status:     "unread",
	})
	if err != nil {
		t.Fatal(err)
	}
	inserted, err := queries.GetItem(context.Background(), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if inserted.Points.Valid {
		t.Fatal("precondition: item must have NULL points")
	}

	enricher := NewHNEnricher()
	enricher.firebaseURL = hnServer.URL
	enricher.algoliaURL = "http://no-such-host.test"

	poller.SetEnrichmentWindow(24 * time.Hour)
	if err := poller.enrichFeedItemsForTest(context.Background(), feedObj, enricher); err != nil {
		t.Fatal(err)
	}

	if _, err := queries.GetItem(context.Background(), item.ID); err == nil {
		t.Error("item enriched below min_score should have been deleted")
	}
}

// TestInsertStoresNULLWhenUnenriched verifies that candidates with no score
// data are stored with NULL points, making them eligible for re-enrichment.
func TestInsertStoresNULLWhenUnenriched(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	feedObj, err := queries.CreateFeed(context.Background(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "hn",
		Name:   "HN",
		Url:    "https://example.com/rss",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate the insert path for an item whose enricher returned nothing:
	// CreateItemIfNotExists is called by PollFeed; here we verify the SQL
	// round-trip of Valid:false → NULL → ListUnreadItemsForEnrichment.
	item, err := queries.CreateItemIfNotExists(context.Background(), store.CreateItemIfNotExistsParams{
		FeedID:        feedObj.ID,
		Hash:          "h-unenriched",
		Title:         "Unenriched",
		Url:           "https://example.com/x",
		Points:        nullInt64(),
		CommentsCount: nullInt64(),
	})
	if err != nil {
		t.Fatal(err)
	}
	// CreateItemIfNotExists returns the insert ID; re-read to check NULLs.
	inserted, err := queries.GetItem(context.Background(), item)
	if err != nil {
		t.Fatal(err)
	}
	if inserted.Points.Valid {
		t.Errorf("points should be NULL, got %+v", inserted.Points)
	}

	// The item must appear as an enrichment candidate immediately.
	cutoff := time.Now().Add(-24 * time.Hour)
	cands, err := queries.ListUnreadItemsForEnrichment(context.Background(), store.ListUnreadItemsForEnrichmentParams{
		FeedID:         feedObj.ID,
		FetchedAt:      cutoff,
		LastEnrichedAt: sql.NullTime{},
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range cands {
		if c.ID == inserted.ID {
			found = true
		}
	}
	if !found {
		t.Error("unenriched item should be selected for enrichment")
	}
}

// enrichFeedItemsForTest exposes the unexported enrichment pass to tests.
func (p *Poller) enrichFeedItemsForTest(ctx context.Context, feed store.Feed, e Enricher) error {
	p.enrichFeedItems(ctx, feed, e)
	return nil
}

func nullInt64() sql.NullInt64 { return sql.NullInt64{} }
