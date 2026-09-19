package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"digesterr/internal/store"
)

// seedThresholdFeed creates a feed with the given min_score and four items:
// zero (0), low (5, below), exact (50, at), high (100, above).
func seedThresholdFeed(t *testing.T, queries *store.Queries, user store.User, name string, minScore int64) store.Feed {
	t.Helper()
	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID:   user.ID,
		Type:     "rss",
		Name:     name,
		Url:      "https://example.com/" + name + "/feed",
		Config:   "{}",
		MinScore: minScore,
		Status:   "active",
	})
	if err != nil {
		t.Fatalf("create feed %s: %v", name, err)
	}
	for _, it := range []struct {
		title  string
		points int64
	}{
		{"zero", 0}, {"low", 5}, {"exact", 50}, {"high", 100},
	} {
		if _, err := queries.CreateItem(t.Context(), store.CreateItemParams{
			FeedID:      feedObj.ID,
			Hash:        name + "-" + it.title,
			Title:       it.title,
			Url:         "https://example.com/" + name + "/" + it.title,
			PublishedAt: sql.NullTime{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true},
			Points:      it.points,
			Status:      "unread",
		}); err != nil {
			t.Fatalf("create item %s: %v", it.title, err)
		}
	}
	return feedObj
}

func getFeedItemTitles(t *testing.T, server *Server, path, token string) []string {
	t.Helper()
	w := makeRequest(server, http.MethodGet, path, nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: expected 200, got %d: %s", path, w.Code, w.Body.String())
	}
	var resp struct {
		Items []store.Item `json:"items"`
		Total int64        `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	titles := make([]string, len(resp.Items))
	for i, it := range resp.Items {
		titles[i] = it.Title
	}
	return titles
}

func pagedFeedTitles(t *testing.T, server *Server, path, token string) ([]string, int64) {
	t.Helper()
	w := makeRequest(server, http.MethodGet, path, nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: expected 200, got %d: %s", path, w.Code, w.Body.String())
	}
	var resp struct {
		Items []store.Item `json:"items"`
		Total int64        `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	titles := make([]string, len(resp.Items))
	for i, it := range resp.Items {
		titles[i] = it.Title
	}
	return titles, resp.Total
}

// The feed's own min_score applies when the caller passes no explicit value.
func TestListFeedItemsDefaultsToFeedMinScore(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "default-minscore@example.com", "password123")
	token := getTestToken(t, server.auth, &user)
	feedObj := seedThresholdFeed(t, queries, user, "thresh", 50)
	path := "/api/feeds/" + itoa(feedObj.ID) + "/items"

	if got := getFeedItemTitles(t, server, path, token); len(got) != 2 {
		t.Fatalf("default list = %v, want [exact high]", got)
	}

	// Explicit min_score=0 disables the feed default entirely.
	if got := getFeedItemTitles(t, server, path+"?min_score=0&limit=10", token); len(got) != 4 {
		t.Fatalf("min_score=0 list = %v, want all 4", got)
	}

	// An explicit value overrides the feed default in either direction.
	if got := getFeedItemTitles(t, server, path+"?min_score=6&limit=10", token); len(got) != 2 {
		t.Fatalf("min_score=6 list = %v, want [exact high]", got)
	}
	if got := getFeedItemTitles(t, server, path+"?min_score=51&limit=10", token); len(got) != 1 || got[0] != "high" {
		t.Fatalf("min_score=51 list = %v, want [high]", got)
	}

	// The paginated total agrees with the default-filtered list.
	if _, total := pagedFeedTitles(t, server, path+"?limit=10", token); total != 2 {
		t.Fatalf("default paginated total = %d, want 2", total)
	}

	// The starred set is exempt: a saved low-score item stays visible.
	saved, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID:      feedObj.ID,
		Hash:        "thresh-saved-low",
		Title:       "saved-low",
		Url:         "https://example.com/thresh/saved-low",
		PublishedAt: sql.NullTime{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true},
		Points:      5,
		Status:      "read",
	})
	if err != nil {
		t.Fatalf("create saved item: %v", err)
	}
	if _, err := queries.SetItemSaved(t.Context(), store.SetItemSavedParams{ID: saved.ID, Saved: 1, MarkRead: 0}); err != nil {
		t.Fatalf("save item: %v", err)
	}
	if got := getFeedItemTitles(t, server, path+"?status=saved", token); len(got) != 1 || got[0] != "saved-low" {
		t.Fatalf("saved list = %v, want [saved-low]", got)
	}
}

// The unread badge must agree with the filtered pane, not count hidden rows.
func TestUnreadCountsRespectFeedMinScore(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "counts-minscore@example.com", "password123")
	token := getTestToken(t, server.auth, &user)
	feedObj := seedThresholdFeed(t, queries, user, "cnt", 50)

	w := makeRequest(server, http.MethodGet, "/api/unread-counts", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var counts map[int64]int64
	if err := json.NewDecoder(w.Body).Decode(&counts); err != nil {
		t.Fatalf("decode counts: %v", err)
	}
	// Go marshals int64 map keys as strings; decode via a string-keyed map.
	var strCounts map[string]int64
	w2 := makeRequest(server, http.MethodGet, "/api/unread-counts", nil, token)
	if err := json.NewDecoder(w2.Body).Decode(&strCounts); err != nil {
		t.Fatalf("decode counts: %v", err)
	}
	_ = counts
	key := itoa(feedObj.ID)
	if strCounts[key] != 2 {
		t.Fatalf("unread count for threshold feed = %d, want 2 (exact + high)", strCounts[key])
	}
}

// With no explicit min_score the multi-item view applies each feed's own
// threshold per row; an explicit value is one threshold across the view.
func TestMultiItemsDefaultPerFeedMinScore(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "multi-minscore@example.com", "password123")
	token := getTestToken(t, server.auth, &user)
	strict := seedThresholdFeed(t, queries, user, "strict", 50)
	open := seedThresholdFeed(t, queries, user, "open", 0)

	getMulti := func(query string) []string {
		t.Helper()
		ids := itoa(strict.ID) + "," + itoa(open.ID)
		w := makeRequest(server, http.MethodGet, "/api/items/multi?feed_ids="+ids+query, nil, token)
		if w.Code != http.StatusOK {
			t.Fatalf("GET multi%s: expected 200, got %d: %s", query, w.Code, w.Body.String())
		}
		var resp struct {
			Items []struct {
				Title string `json:"title"`
			} `json:"items"`
			Total int64 `json:"total"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode multi: %v", err)
		}
		titles := make([]string, len(resp.Items))
		for i, it := range resp.Items {
			titles[i] = it.Title
		}
		return titles
	}

	// strict contributes exact+high, open contributes all 4.
	if got := getMulti(""); len(got) != 6 {
		t.Fatalf("default multi = %d items %v, want 6", len(got), got)
	}
	// Explicit min_score=0 returns all 8.
	if got := getMulti("&min_score=0"); len(got) != 8 {
		t.Fatalf("min_score=0 multi = %d items, want 8", len(got))
	}
	// Explicit min_score=51 returns only the two "high" rows.
	if got := getMulti("&min_score=51"); len(got) != 2 {
		t.Fatalf("min_score=51 multi = %d items %v, want 2", len(got), got)
	}
}
