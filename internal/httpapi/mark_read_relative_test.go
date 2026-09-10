package httpapi

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"
	"time"

	"digesterr/internal/store"
)

// markReadFixture is a user with feeds and items used to exercise the relative
// mark-read endpoints.
type markReadFixture struct {
	server  *Server
	queries *store.Queries
	token   string
	userID  int64
}

// newMarkReadFixture builds a user with n feeds, all in category "tech".
func newMarkReadFixture(t *testing.T, n int) (markReadFixture, []store.Feed) {
	t.Helper()
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "mark-read-relative@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	fx := markReadFixture{server: server, queries: queries, token: token, userID: user.ID}
	feeds := make([]store.Feed, 0, n)
	for i := 0; i < n; i++ {
		feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
			UserID:   user.ID,
			Category: "tech",
			Type:     "rss",
			Name:     fmt.Sprintf("feed-%d", i),
			Url:      fmt.Sprintf("https://example.com/%d.xml", i),
			Config:   "{}",
			Status:   "active",
		})
		if err != nil {
			t.Fatalf("create feed %d: %v", i, err)
		}
		feeds = append(feeds, feed)
	}
	return fx, feeds
}

// addItem inserts an unread item. A zero published time is stored as NULL,
// which is what undated feeds produce.
func addItem(t *testing.T, queries *store.Queries, feedID int64, title string, published time.Time) store.Item {
	t.Helper()
	var pub sql.NullTime
	if !published.IsZero() {
		pub = sql.NullTime{Time: published, Valid: true}
	}
	item, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID:      feedID,
		Hash:        title,
		Title:       title,
		Url:         "https://example.com/" + title,
		Content:     "body",
		PublishedAt: pub,
		Status:      "unread",
	})
	if err != nil {
		t.Fatalf("create item %q: %v", title, err)
	}
	return item
}

// markedTitles returns the titles of items whose status is "read", preserving
// the order they were passed in.
func markedTitles(t *testing.T, queries *store.Queries, inOrder []store.Item) []string {
	t.Helper()
	var got []string
	for _, it := range inOrder {
		item, err := queries.GetItem(context.Background(), it.ID)
		if err != nil {
			t.Fatalf("get item %d: %v", it.ID, err)
		}
		if item.Status == "read" {
			got = append(got, item.Title)
		}
	}
	return got
}

// markRelative posts to a relative mark-read endpoint and fails on a non-2xx.
func markRelative(t *testing.T, fx markReadFixture, itemID int64, direction string, body any) {
	t.Helper()
	rec := makeRequest(fx.server, http.MethodPost,
		fmt.Sprintf("/api/items/%d/mark-read-%s", itemID, direction), body, fx.token)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark-read-%s returned %d, body: %s", direction, rec.Code, rec.Body.String())
	}
}

// TestMarkReadRelativeSingleFeed is the regression test for the inverted
// direction and the excluded boundary item.
//
// Items are ordered newest-first by the list queries (published_at DESC,
// created_at DESC). With a 5-item feed and the boundary on the middle item,
// "above" must mark the boundary and the two newer items, and "below" must mark
// the two older items.
func TestMarkReadRelativeSingleFeed(t *testing.T) {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		direction string
		want      []string
	}{
		{name: "above marks boundary and newer", direction: "above", want: []string{"t4", "t3", "t2"}},
		{name: "below marks older only", direction: "below", want: []string{"t1", "t0"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx, feeds := newMarkReadFixture(t, 1)

			// t0 oldest .. t4 newest; the list shows t4,t3,t2,t1,t0.
			var items []store.Item
			for i := 0; i < 5; i++ {
				items = append(items, addItem(t, fx.queries, feeds[0].ID,
					fmt.Sprintf("t%d", i), base.Add(time.Duration(i)*time.Hour)))
			}
			newestFirst := []store.Item{items[4], items[3], items[2], items[1], items[0]}

			markRelative(t, fx, items[2].ID, tt.direction, map[string]any{})

			got := markedTitles(t, fx.queries, newestFirst)
			if !equalStrings(got, tt.want) {
				t.Errorf("marked %v, want %v (all items: t4,t3,t2,t1,t0)", got, tt.want)
			}
		})
	}
}

// TestMarkReadRelativeIncludesUndatedItems asserts items with a NULL
// published_at are reachable. They sort last in the list, so "below" a dated
// boundary must mark them, and "above" an undated boundary must mark every
// dated item.
func TestMarkReadRelativeIncludesUndatedItems(t *testing.T) {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	t.Run("below a dated boundary reaches undated items", func(t *testing.T) {
		fx, feeds := newMarkReadFixture(t, 1)
		dated1 := addItem(t, fx.queries, feeds[0].ID, "dated1", base.Add(time.Hour))
		dated2 := addItem(t, fx.queries, feeds[0].ID, "dated2", base.Add(2*time.Hour))
		undated := addItem(t, fx.queries, feeds[0].ID, "undated", time.Time{})

		// List order: dated2, dated1, undated.
		markRelative(t, fx, dated2.ID, "below", map[string]any{})

		got := markedTitles(t, fx.queries, []store.Item{dated2, dated1, undated})
		if want := []string{"dated1", "undated"}; !equalStrings(got, want) {
			t.Errorf("marked %v, want %v — items with NULL published_at must be reachable", got, want)
		}
	})

	t.Run("above an undated boundary marks every dated item", func(t *testing.T) {
		fx, feeds := newMarkReadFixture(t, 1)
		dated1 := addItem(t, fx.queries, feeds[0].ID, "dated1", base.Add(time.Hour))
		dated2 := addItem(t, fx.queries, feeds[0].ID, "dated2", base.Add(2*time.Hour))
		undated := addItem(t, fx.queries, feeds[0].ID, "undated", time.Time{})

		markRelative(t, fx, undated.ID, "above", map[string]any{})

		got := markedTitles(t, fx.queries, []store.Item{dated2, dated1, undated})
		if want := []string{"dated2", "dated1", "undated"}; !equalStrings(got, want) {
			t.Errorf("marked %v, want %v — undated items sort last, so every dated item is above", got, want)
		}
	})
}

// TestMarkReadRelativeCategorySpansFeeds asserts the label scope marks items
// across every feed in the category, and does not touch other categories.
func TestMarkReadRelativeCategorySpansFeeds(t *testing.T) {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	fx, feeds := newMarkReadFixture(t, 2)
	other, err := fx.queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID:   fx.userID,
		Category: "news",
		Type:     "rss",
		Name:     "other",
		Url:      "https://example.com/other.xml",
		Config:   "{}",
		Status:   "active",
	})
	if err != nil {
		t.Fatalf("create other feed: %v", err)
	}

	f0old := addItem(t, fx.queries, feeds[0].ID, "f0old", base.Add(time.Hour))
	f0new := addItem(t, fx.queries, feeds[0].ID, "f0new", base.Add(3*time.Hour))
	f1old := addItem(t, fx.queries, feeds[1].ID, "f1old", base.Add(time.Hour))
	f1new := addItem(t, fx.queries, feeds[1].ID, "f1new", base.Add(4*time.Hour))
	untouched := addItem(t, fx.queries, other.ID, "untouched", base.Add(5*time.Hour))

	// Boundary: f0old. Everything newer in the category must be marked, in both
	// feeds, and nothing in the "news" category.
	markRelative(t, fx, f0old.ID, "above", map[string]any{"category": "tech"})

	got := markedTitles(t, fx.queries, []store.Item{f0new, f1new, f1old, f0old})
	if want := []string{"f0new", "f1new", "f1old", "f0old"}; !equalStrings(got, want) {
		t.Errorf("marked %v, want %v", got, want)
	}

	item, err := fx.queries.GetItem(context.Background(), untouched.ID)
	if err != nil {
		t.Fatalf("get untouched item: %v", err)
	}
	if item.Status != "unread" {
		t.Errorf("item in another category was marked %q, want unread", item.Status)
	}
}

// TestMarkReadRelativeFeedsSectionSpansAllFeeds asserts the "Feeds" section
// scope (signalled by feed_ids) marks across every feed the user owns. The
// client sends its complete feed list, so the scope is user-wide rather than a
// subset filter.
func TestMarkReadRelativeFeedsSectionSpansAllFeeds(t *testing.T) {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	fx, feeds := newMarkReadFixture(t, 2)
	f0old := addItem(t, fx.queries, feeds[0].ID, "f0old", base.Add(time.Hour))
	f0new := addItem(t, fx.queries, feeds[0].ID, "f0new", base.Add(3*time.Hour))
	f1new := addItem(t, fx.queries, feeds[1].ID, "f1new", base.Add(4*time.Hour))

	feedIDs := fmt.Sprintf("%d,%d", feeds[0].ID, feeds[1].ID)
	markRelative(t, fx, f0old.ID, "above", map[string]any{"feed_ids": feedIDs})

	got := markedTitles(t, fx.queries, []store.Item{f1new, f0new, f0old})
	if want := []string{"f1new", "f0new", "f0old"}; !equalStrings(got, want) {
		t.Errorf("marked %v, want %v", got, want)
	}
}

// TestMarkReadRelativeSingleFeedIsScopedToOneFeed asserts that without a
// category or feed_ids the update stays inside the boundary item's own feed,
// even when the user has other feeds.
func TestMarkReadRelativeSingleFeedIsScopedToOneFeed(t *testing.T) {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	fx, feeds := newMarkReadFixture(t, 2)
	boundary := addItem(t, fx.queries, feeds[0].ID, "boundary", base.Add(time.Hour))
	sameFeed := addItem(t, fx.queries, feeds[0].ID, "same-newer", base.Add(2*time.Hour))
	otherFeed := addItem(t, fx.queries, feeds[1].ID, "other-newer", base.Add(3*time.Hour))

	markRelative(t, fx, boundary.ID, "above", map[string]any{})

	got := markedTitles(t, fx.queries, []store.Item{sameFeed, boundary})
	if want := []string{"same-newer", "boundary"}; !equalStrings(got, want) {
		t.Errorf("marked %v, want %v within the boundary item's feed", got, want)
	}

	item, err := fx.queries.GetItem(context.Background(), otherFeed.ID)
	if err != nil {
		t.Fatalf("get other-feed item: %v", err)
	}
	if item.Status != "unread" {
		t.Errorf("item in another feed was marked %q, want unread", item.Status)
	}
}

// TestMarkReadRelativeRejectsOtherUsersItem asserts the endpoint will not act
// on an item the caller does not own. Both users live in the same database, so
// the item really is reachable — the endpoint must still refuse it.
func TestMarkReadRelativeRejectsOtherUsersItem(t *testing.T) {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	server, queries := setupTestServer(t)

	owner := createTestUser(t, queries, "owner@example.com", "password123")
	ownerFeed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID:   owner.ID,
		Category: "tech",
		Type:     "rss",
		Name:     "owner-feed",
		Url:      "https://example.com/owner.xml",
		Config:   "{}",
		Status:   "active",
	})
	if err != nil {
		t.Fatalf("create owner feed: %v", err)
	}
	foreign := addItem(t, queries, ownerFeed.ID, "foreign", base)

	intruder := createTestUser(t, queries, "intruder@example.com", "password123")
	token := getTestToken(t, server.auth, &intruder)

	rec := makeRequest(server, http.MethodPost,
		fmt.Sprintf("/api/items/%d/mark-read-above", foreign.ID), map[string]any{}, token)
	if rec.Code != http.StatusNotFound {
		t.Errorf("marking another user's item returned %d, want 404", rec.Code)
	}

	item, err := queries.GetItem(context.Background(), foreign.ID)
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if item.Status != "unread" {
		t.Errorf("foreign item status = %q, want unread", item.Status)
	}
}
