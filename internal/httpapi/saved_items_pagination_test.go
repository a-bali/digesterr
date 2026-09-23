package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"digesterr/internal/store"
)

// seedSavedItems creates n saved items on one feed for userID, newest first.
func seedSavedItems(t *testing.T, queries *store.Queries, userID int64, n int) store.Feed {
	t.Helper()
	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: userID,
		Type:   "rss",
		Name:   "Saved feed",
		Url:    "https://saved.example.com/feed",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	for i := 0; i < n; i++ {
		createSeedItem(t, queries, feed.ID, fmt.Sprintf("saved-%03d", i),
			fmt.Sprintf("Saved %d", i), "saved", streamTimes.aSaved)
	}
	return feed
}

type savedItemsPage struct {
	Items   []map[string]any `json:"items"`
	HasMore bool             `json:"has_more"`
	Total   int64            `json:"total"`
}

func fetchSavedPage(t *testing.T, server *Server, token, query string) savedItemsPage {
	t.Helper()
	rec := makeRequest(server, http.MethodGet, "/api/saved"+query, nil, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/saved%s returned %d: %s", query, rec.Code, rec.Body.String())
	}
	var page savedItemsPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode saved page: %v", err)
	}
	return page
}

// TestListSavedItemsPaginates pins the lazy-loading contract: a limit returns one
// page plus has_more and the full total, and successive pages walk the list
// without gaps or repeats.
func TestListSavedItemsPaginates(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "saved-page@example.com", "password123")
	token := getTestToken(t, server.auth, &user)
	seedSavedItems(t, queries, user.ID, 5)

	first := fetchSavedPage(t, server, token, "?limit=2&offset=0")
	if len(first.Items) != 2 {
		t.Errorf("first page has %d items, want 2", len(first.Items))
	}
	if !first.HasMore {
		t.Error("first page has_more = false, want true with 5 saved and a limit of 2")
	}
	if first.Total != 5 {
		t.Errorf("total = %d, want 5 (the count must cover the whole list, not the page)", first.Total)
	}

	// Walk every page and collect ids: each item must appear exactly once.
	seen := map[int64]int{}
	for offset := 0; ; offset += 2 {
		page := fetchSavedPage(t, server, token, fmt.Sprintf("?limit=2&offset=%d", offset))
		for _, it := range page.Items {
			id := int64(it["id"].(float64))
			seen[id]++
		}
		if !page.HasMore {
			break
		}
		if offset > 20 {
			t.Fatal("paging did not terminate")
		}
	}
	if len(seen) != 5 {
		t.Errorf("paging returned %d distinct items, want 5", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("item %d returned %d times, want exactly 1", id, n)
		}
	}
}

// TestListSavedItemsWithoutLimitReturnsAll is the back-compat control: callers
// that pass no limit must still get the entire list, in the old response shape.
func TestListSavedItemsWithoutLimitReturnsAll(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "saved-all@example.com", "password123")
	token := getTestToken(t, server.auth, &user)
	seedSavedItems(t, queries, user.ID, 4)

	page := fetchSavedPage(t, server, token, "")
	if len(page.Items) != 4 {
		t.Errorf("unpaginated list has %d items, want all 4", len(page.Items))
	}
	if page.HasMore {
		t.Error("unpaginated response must not report has_more")
	}
}

// TestListSavedItemsIsScopedToUser guards the ownership predicate while the query
// changes shape: another account's saved items must never appear.
func TestListSavedItemsIsScopedToUser(t *testing.T) {
	server, queries := setupTestServer(t)
	owner := createTestUser(t, queries, "saved-owner@example.com", "password123")
	other := createTestUser(t, queries, "saved-other@example.com", "password123")
	seedSavedItems(t, queries, other.ID, 3)

	token := getTestToken(t, server.auth, &owner)
	page := fetchSavedPage(t, server, token, "?limit=50&offset=0")
	if len(page.Items) != 0 {
		t.Errorf("owner sees %d items belonging to another account, want 0", len(page.Items))
	}
	if page.Total != 0 {
		t.Errorf("total = %d for a user with no saved items, want 0", page.Total)
	}
}
