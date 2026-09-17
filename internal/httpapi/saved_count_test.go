package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"digesterr/internal/store"
)

// TestSavedCount covers the endpoint behind the sidebar's saved badge.
//
// The badge needs a total that does NOT depend on which feeds the client has
// loaded: the saved view is assembled client-side from per-feed requests, so
// its array length is only the loaded page. Equally, "saved" is a status, not
// an unread tally, so it must not be derived from the unread counts.
func TestSavedCount(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "saved-count@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Name:   "f",
		Type:   "rss",
		Url:    "https://example.com/rss",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	// Nothing saved yet.
	if got := savedCount(t, server, token); got != 0 {
		t.Fatalf("empty account: got %d, want 0", got)
	}

	// Three items in three different states. Only 'saved' counts, and the
	// unread one must not leak into the total.
	for i, status := range []string{"unread", "read", "saved"} {
		if _, err := queries.CreateItem(t.Context(), store.CreateItemParams{
			FeedID:  feed.ID,
			Hash:    fmt.Sprintf("h%d", i),
			Title:   fmt.Sprintf("t%d", i),
			Url:     fmt.Sprintf("https://example.com/%d", i),
			Content: "c",
			Status:  status,
		}); err != nil {
			t.Fatalf("create item %d: %v", i, err)
		}
	}

	if got := savedCount(t, server, token); got != 1 {
		t.Fatalf("one saved item: got %d, want 1 (read/unread must not count)", got)
	}

	// A second saved item still increments, so this is a count and not a flag.
	if _, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID:  feed.ID,
		Hash:    "h3",
		Title:   "t3",
		Url:     "https://example.com/3",
		Content: "c",
		Status:  "saved",
	}); err != nil {
		t.Fatalf("create item 3: %v", err)
	}
	if got := savedCount(t, server, token); got != 2 {
		t.Fatalf("two saved items: got %d, want 2", got)
	}
}

// TestSavedCountIsScopedToUser pins that the badge cannot leak another
// account's saved items.
func TestSavedCountIsScopedToUser(t *testing.T) {
	server, queries := setupTestServer(t)
	mine := createTestUser(t, queries, "mine@example.com", "password123")
	theirs := createTestUser(t, queries, "theirs@example.com", "password123")
	myToken := getTestToken(t, server.auth, &mine)

	theirFeed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: theirs.ID, Name: "other", Type: "rss", Url: "https://other.example/rss",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	if _, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: theirFeed.ID, Hash: "x", Title: "x", Url: "https://other.example/1",
		Content: "c", Status: "saved",
	}); err != nil {
		t.Fatalf("create item: %v", err)
	}

	if got := savedCount(t, server, myToken); got != 0 {
		t.Fatalf("saw another user's saved items: got %d, want 0", got)
	}
}

// TestSavedCountRequiresAuth pins that the endpoint is behind requireAuth.
func TestSavedCountRequiresAuth(t *testing.T) {
	server, _ := setupTestServer(t)
	if w := makeRequest(server, http.MethodGet, "/api/saved-count", nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request: got %d, want 401", w.Code)
	}
}

func savedCount(t *testing.T, server *Server, token string) int64 {
	t.Helper()
	w := makeRequest(server, http.MethodGet, "/api/saved-count", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("saved-count returned %d, body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Count int64 `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode saved-count: %v", err)
	}
	return resp.Count
}
