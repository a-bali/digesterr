package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"digesterr/internal/store"
)

// List previews carry truncated bodies: the middle pane needs a one-line
// excerpt, not the full article, and a page of full bodies was megabytes per
// render. The full body is fetched separately when an item is opened.
func TestListFeedItemsTruncatesPreviewBody(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "preview@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Long", Url: "https://example.com/long",
		Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	long := strings.Repeat("x", 5000)
	if _, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: feed.ID, Hash: "h-long", Title: "Long body", Url: "https://example.com/long/1",
		Content: long, Status: "unread",
	}); err != nil {
		t.Fatalf("create item: %v", err)
	}

	w := makeRequest(server, http.MethodGet, "/api/feeds/"+itoa(feed.ID)+"/items?limit=10", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("list items: %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []struct {
			Content string `json:"content"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(resp.Items))
	}
	if len(resp.Items[0].Content) > 500 {
		t.Errorf("preview content = %d bytes, want <= 500", len(resp.Items[0].Content))
	}
}
