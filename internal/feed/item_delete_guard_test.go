package feed

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"testing"

	"digesterr/internal/store"
	"digesterr/internal/testutil"
)

// setupDeleteGuard returns a DB handle (for triggers), queries, and one feed.
func setupDeleteGuard(t *testing.T) (*sql.DB, *store.Queries, store.Feed) {
	t.Helper()
	db, err := testutil.OpenTestDB()
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	queries := store.New(db)

	user, err := queries.CreateUser(t.Context(), store.CreateUserParams{
		Email:        "delete-guard@example.com",
		PasswordHash: "hash",
		ApiKey:       "delete-guard-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "hn",
		Name:   "Delete Guard Feed",
		Url:    "https://example.com/guard.xml",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	// digest_results.config_id is a real FK, so the digest helper needs a config.
	if _, err := queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Delete Guard Digest",
		Schedule: "0 8 * * *",
		Config:   `{"feeds":[{"id":` + itoa64(feedObj.ID) + `,"highlights":1,"comments":0}]}`,
	}); err != nil {
		t.Fatalf("create digest config: %v", err)
	}
	return db, queries, feedObj
}

func itoa64(v int64) string { return strconv.FormatInt(v, 10) }

// setFeedURLAndScore rewrites a feed's url and min_score through UpdateFeed,
// which is the only query that exposes both.
func setFeedURLAndScore(t *testing.T, queries *store.Queries, f store.Feed, url string, minScore int64) store.Feed {
	t.Helper()
	updated, err := queries.UpdateFeed(t.Context(), store.UpdateFeedParams{
		Name:           f.Name,
		Url:            url,
		Config:         f.Config,
		Category:       f.Category,
		Status:         f.Status,
		ExtractContent: f.ExtractContent,
		MinScore:       minScore,
		Readability:    f.Readability,
		ID:             f.ID,
		UserID:         f.UserID,
	})
	if err != nil {
		t.Fatalf("update feed: %v", err)
	}
	return updated
}

// linkToDigest creates a digest result and links itemID to it, exactly as
// Generate does.
func linkToDigest(t *testing.T, queries *store.Queries, userID, itemID int64) int64 {
	t.Helper()
	result, err := queries.CreateDigestResult(t.Context(), store.CreateDigestResultParams{
		UserID:         userID,
		ConfigID:       1,
		ConfigSnapshot: "{}",
		Content:        "{}",
		Status:         "new",
	})
	if err != nil {
		t.Fatalf("create digest result: %v", err)
	}
	if _, err := queries.CreateDigestResultItem(t.Context(), store.CreateDigestResultItemParams{
		ResultID:    result.ID,
		ItemID:      itemID,
		IsHighlight: 1,
	}); err != nil {
		t.Fatalf("link item to digest: %v", err)
	}
	return result.ID
}

// TestDeleteItemRefusesSavedItem: a saved item must never be removed by the
// poller's score cleanup. A saved row whose status still reads unread is the
// dangerous case, because the cleanup selects on status.
func TestDeleteItemRefusesSavedItem(t *testing.T) {
	db, queries, feedObj := setupDeleteGuard(t)

	item, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: feedObj.ID, Hash: "saved-1", Title: "Saved", Url: "https://example.com/s",
		Status: "unread",
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	// Force the dangerous shape directly: saved, yet status = 'unread'.
	if _, err := db.Exec(`UPDATE items SET is_saved = 1 WHERE id = ?`, item.ID); err != nil {
		t.Fatalf("mark saved: %v", err)
	}

	// Refusal is signalled as "no row deleted", which callers treat as kept.
	if _, err := queries.DeleteItem(t.Context(), item.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("DeleteItem failed unexpectedly for a saved row: %v", err)
	}
	if _, err := queries.GetItem(t.Context(), item.ID); err != nil {
		t.Errorf("saved item was deleted: %v", err)
	}
}

// TestDeleteItemRefusesDigestLinkedItem: an item referenced by a digest result
// must never be removed. The FK is ON DELETE CASCADE, so deleting it would
// silently strip the item out of an already-generated digest.
func TestDeleteItemRefusesDigestLinkedItem(t *testing.T) {
	_, queries, feedObj := setupDeleteGuard(t)

	item, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: feedObj.ID, Hash: "linked-1", Title: "Linked", Url: "https://example.com/l",
		Status: "unread",
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	resultID := linkToDigest(t, queries, feedObj.UserID, item.ID)

	if _, err := queries.DeleteItem(t.Context(), item.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("DeleteItem failed unexpectedly for a digest-linked row: %v", err)
	}
	if _, err := queries.GetItem(t.Context(), item.ID); err != nil {
		t.Errorf("digest-linked item was deleted: %v", err)
	}
	items, err := queries.ListDigestResultItems(t.Context(), resultID)
	if err != nil {
		t.Fatalf("list digest result items: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("digest result has %d items, want 1 (link was cascade-deleted)", len(items))
	}
}

// TestDeleteItemRemovesDisposableItem is the positive control: the guard must
// not turn DeleteItem into a no-op for an item with no reason to be kept.
func TestDeleteItemRemovesDisposableItem(t *testing.T) {
	_, queries, feedObj := setupDeleteGuard(t)

	item, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: feedObj.ID, Hash: "disposable-1", Title: "Disposable", Url: "https://example.com/d",
		Status: "unread",
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	if _, err := queries.DeleteItem(t.Context(), item.ID); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}
	if _, err := queries.GetItem(t.Context(), item.ID); err == nil {
		t.Error("disposable item survived DeleteItem")
	}
}

// linkingEnricher links an item to a digest result mid-poll, reproducing the
// concurrent-generation race deterministically: the item is listed as an
// enrichment candidate (so the cleanup will consider deleting it), and the
// digest link appears AFTER that listing but BEFORE the delete.
type linkingEnricher struct {
	queries   *store.Queries
	userID    int64
	targetURL string
	points    int
	linked    bool
}

func (l *linkingEnricher) Enrich(_ context.Context, item *store.Item) error {
	if item.Url == l.targetURL {
		item.Points = int64(l.points)
		item.CommentsCount = 1
	}
	return nil
}

func (l *linkingEnricher) BulkEnrich(_ context.Context, items []*store.Item) error {
	for _, it := range items {
		if err := l.Enrich(context.Background(), it); err != nil {
			return err
		}
		if it.Url == l.targetURL && !l.linked {
			l.linked = true
			// A digest generation running concurrently claims this item.
			result, err := l.queries.CreateDigestResult(context.Background(), store.CreateDigestResultParams{
				UserID:         l.userID,
				ConfigID:       1,
				ConfigSnapshot: "{}",
				Content:        "{}",
				Status:         "new",
			})
			if err != nil {
				return err
			}
			if _, err := l.queries.CreateDigestResultItem(context.Background(), store.CreateDigestResultItemParams{
				ResultID:    result.ID,
				ItemID:      it.ID,
				IsHighlight: 1,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *linkingEnricher) FetchComments(_ context.Context, _ store.Item, _ int) ([]Comment, error) {
	return nil, nil
}
func (l *linkingEnricher) CommentTargetURL() string { return "" }

// staticFetcher returns no items, so only pre-existing rows are acted on.
type staticFetcher struct{}

func (s *staticFetcher) Fetch(_ context.Context, _ string) ([]Item, error) { return nil, nil }

// TestPollCleanupKeepsItemLinkedMidPoll is the race from the bug report: the
// poller decides to drop a below-threshold item while a digest generation
// links that same item. The link must win — the item stays.
func TestPollCleanupKeepsItemLinkedMidPoll(t *testing.T) {
	_, queries, feedObj := setupDeleteGuard(t)

	// A threshold, so the below-threshold cleanup branch runs.
	feedObj = setFeedURLAndScore(t, queries, feedObj, "https://example.com/guard.xml", 100)

	target := "https://example.com/raced"
	item, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: feedObj.ID, Hash: "raced-1", Title: "Raced", Url: target,
		Status: "unread",
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	enr := &linkingEnricher{queries: queries, userID: feedObj.UserID, targetURL: target, points: 5}
	poller := NewPoller(queries)
	poller.enrichers["hn"] = enr

	// A fetcher returning nothing new leaves the pre-existing unread item as
	// the only candidate, so the below-threshold cleanup branch acts on it.
	if err := poller.pollFeed(context.Background(), feedObj, &staticFetcher{}); err != nil {
		t.Fatalf("pollFeed: %v", err)
	}
	if !enr.linked {
		t.Fatal("test setup failed: enricher never linked the item")
	}

	if _, err := queries.GetItem(t.Context(), item.ID); err != nil {
		t.Errorf("item linked by a concurrent digest generation was deleted by the poller: %v", err)
	}
}
