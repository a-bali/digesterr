package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"digesterr/internal/store"
)

// TestFeedMinScoreValidation verifies negative min_score values are rejected
// on both create and update.
func TestFeedMinScoreValidation(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "minscore@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	w := makeRequest(server, http.MethodPost, "/api/feeds", map[string]interface{}{
		"type":      "hn",
		"name":      "Bad Min Score",
		"url":       "https://news.ycombinator.com/rss",
		"min_score": -1,
	}, token)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for negative min_score on create, got %d: %s", w.Code, w.Body.String())
	}

	created := makeRequest(server, http.MethodPost, "/api/feeds", map[string]interface{}{
		"type": "hn",
		"name": "Good Feed",
		"url":  "https://news.ycombinator.com/rss",
	}, token)
	if created.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", created.Code, created.Body.String())
	}
	var feed store.Feed
	json.NewDecoder(created.Body).Decode(&feed)

	w = makeRequest(server, http.MethodPatch, "/api/feeds/"+itoa(feed.ID), map[string]interface{}{
		"min_score": -5,
	}, token)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for negative min_score on update, got %d: %s", w.Code, w.Body.String())
	}
}

// TestFeedMinScorePersistenceRoundTrip verifies min_score survives
// create -> list -> update -> partial update.
func TestFeedMinScorePersistenceRoundTrip(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "minscore-rt@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	w := makeRequest(server, http.MethodPost, "/api/feeds", map[string]interface{}{
		"type":      "hn",
		"name":      "Threshold Feed",
		"url":       "https://news.ycombinator.com/rss",
		"min_score": 50,
	}, token)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created store.Feed
	json.NewDecoder(w.Body).Decode(&created)
	if created.MinScore != 50 {
		t.Fatalf("created min_score = %d, want 50", created.MinScore)
	}

	w = makeRequest(server, http.MethodGet, "/api/feeds", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var feeds []store.Feed
	json.NewDecoder(w.Body).Decode(&feeds)
	if len(feeds) != 1 || feeds[0].MinScore != 50 {
		t.Fatalf("listed feed min_score = %d, want 50", feeds[0].MinScore)
	}

	w = makeRequest(server, http.MethodPatch, "/api/feeds/"+itoa(created.ID), map[string]interface{}{
		"min_score": 75,
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated store.Feed
	json.NewDecoder(w.Body).Decode(&updated)
	if updated.MinScore != 75 {
		t.Fatalf("updated min_score = %d, want 75", updated.MinScore)
	}

	// A partial update that omits min_score must preserve the stored value.
	w = makeRequest(server, http.MethodPatch, "/api/feeds/"+itoa(created.ID), map[string]interface{}{
		"category": "Tech",
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var partial store.Feed
	json.NewDecoder(w.Body).Decode(&partial)
	if partial.MinScore != 75 {
		t.Fatalf("min_score after unrelated patch = %d, want 75 (preserved)", partial.MinScore)
	}
}

// TestListFeedItemsMinScoreAndSort exercises the server-side min_score and
// sort query params on GET /feeds/:id/items.
func TestListFeedItemsMinScoreAndSort(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "listitems@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "hn",
		Name:   "Item List Feed",
		Url:    "https://news.ycombinator.com/rss",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	mkItem := func(title string, points int64, pub time.Time) store.Item {
		t.Helper()
		item, err := queries.CreateItem(t.Context(), store.CreateItemParams{
			FeedID:      feedObj.ID,
			Hash:        "h-" + title,
			Title:       title,
			Url:         "https://example.com/" + title,
			PublishedAt: sql.NullTime{Time: pub, Valid: true},
			Points:      points,
			Status:      "unread",
		})
		if err != nil {
			t.Fatalf("create item %s: %v", title, err)
		}
		return item
	}

	day1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	day3 := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	day4 := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
	mkItem("low", 5, day1)
	mkItem("mid", 50, day2)
	mkItem("high", 100, day3)
	mkItem("zero", 0, day4)

	path := "/api/feeds/" + itoa(feedObj.ID) + "/items"

	getItems := func(query string) []store.Item {
		t.Helper()
		w := makeRequest(server, http.MethodGet, path+query, nil, token)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s%s: expected 200, got %d: %s", path, query, w.Code, w.Body.String())
		}
		var resp struct {
			Items []store.Item `json:"items"`
		}
		json.NewDecoder(w.Body).Decode(&resp)
		return resp.Items
	}

	t.Run("default sort is date_desc", func(t *testing.T) {
		items := getItems("")
		titles := []string{items[0].Title, items[1].Title, items[2].Title, items[3].Title}
		want := []string{"zero", "high", "mid", "low"}
		for i := range want {
			if titles[i] != want[i] {
				t.Fatalf("date_desc order = %v, want %v", titles, want)
			}
		}
	})

	t.Run("min_score filters below threshold", func(t *testing.T) {
		items := getItems("?min_score=50")
		if len(items) != 2 {
			t.Fatalf("min_score=50 returned %d items, want 2", len(items))
		}
		for _, it := range items {
			if it.Points < 50 {
				t.Errorf("item %s points %d below min_score=50 leaked through", it.Title, it.Points)
			}
		}
	})

	t.Run("score_desc sorts by points", func(t *testing.T) {
		items := getItems("?sort=score_desc&min_score=5")
		titles := []string{items[0].Title, items[1].Title, items[2].Title}
		want := []string{"high", "mid", "low"}
		for i := range want {
			if titles[i] != want[i] {
				t.Fatalf("score_desc order = %v, want %v", titles, want)
			}
		}
	})

	t.Run("score_asc sorts by points ascending", func(t *testing.T) {
		items := getItems("?sort=score_asc&min_score=5")
		titles := []string{items[0].Title, items[1].Title, items[2].Title}
		want := []string{"low", "mid", "high"}
		for i := range want {
			if titles[i] != want[i] {
				t.Fatalf("score_asc order = %v, want %v", titles, want)
			}
		}
	})

	t.Run("date_asc sorts oldest first", func(t *testing.T) {
		items := getItems("?sort=date_asc")
		if items[0].Title != "low" || items[len(items)-1].Title != "zero" {
			t.Fatalf("date_asc order = %v, want low..zero", items)
		}
	})

	t.Run("status combines with min_score and sort", func(t *testing.T) {
		items := getItems("?status=unread&min_score=100&sort=score_asc")
		if len(items) != 1 || items[0].Title != "high" {
			t.Fatalf("status+min_score+sort returned %v, want [high]", items)
		}
	})

	t.Run("paginated response includes has_more and total", func(t *testing.T) {
		w := makeRequest(server, http.MethodGet, path+"?limit=2", nil, token)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var resp struct {
			Items   []store.Item `json:"items"`
			HasMore bool         `json:"has_more"`
			Total   int64        `json:"total"`
		}
		json.NewDecoder(w.Body).Decode(&resp)
		if len(resp.Items) != 2 || !resp.HasMore {
			t.Fatalf("paginated response = %d items, has_more=%v; want 2, true", len(resp.Items), resp.HasMore)
		}
		if resp.Total != 4 {
			t.Fatalf("paginated total = %d; want 4", resp.Total)
		}
	})

	t.Run("invalid sort rejected", func(t *testing.T) {
		w := makeRequest(server, http.MethodGet, path+"?sort=bogus", nil, token)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid sort, got %d", w.Code)
		}
	})

	t.Run("invalid min_score rejected", func(t *testing.T) {
		w := makeRequest(server, http.MethodGet, path+"?min_score=-3", nil, token)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for negative min_score, got %d", w.Code)
		}
		w = makeRequest(server, http.MethodGet, path+"?min_score=abc", nil, token)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for non-numeric min_score, got %d", w.Code)
		}
	})
}
