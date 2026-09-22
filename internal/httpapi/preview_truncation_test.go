package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

// List previews carry a BOUNDED body plus a ready-to-render excerpt: the middle
// pane needs one line of text, not the whole article, and a page of full bodies
// was megabytes per render. The full body is fetched separately when an item is
// opened.
//
// The bound is 8192 characters rather than 500 because the excerpt is flattened
// from this prefix, and the cut counts RAW HTML characters: a body opening with
// inline SVG or a long srcset list can spend several KB of markup before its
// first word of prose. Under a 500-char cap those rows showed an empty excerpt.
func TestListFeedItemsTruncatesPreviewBody(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "preview@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feedRow, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Long", Url: "https://example.com/long",
		Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	long := strings.Repeat("x", 5000)
	if _, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: feedRow.ID, Hash: "h-long", Title: "Long body", Url: "https://example.com/long/1",
		Content: long, Status: "unread",
	}); err != nil {
		t.Fatalf("create item: %v", err)
	}

	w := makeRequest(server, http.MethodGet, "/api/feeds/"+itoa(feedRow.ID)+"/items?limit=10", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("list items: %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []struct {
			Content string `json:"content"`
			Excerpt string `json:"excerpt"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(resp.Items))
	}
	if len(resp.Items[0].Content) > feed.ExcerptMaxBody {
		t.Errorf("preview content = %d bytes, want <= %d", len(resp.Items[0].Content), feed.ExcerptMaxBody)
	}
	// A 5000-character body of plain text is a SHORT body: the cap never applies
	// to it, and the excerpt it produces is still bounded by the text budget.
	if got := resp.Items[0].Excerpt; len(got) > feed.ExcerptTextBudget+16 {
		t.Errorf("excerpt = %d bytes, want it bounded near %d", len(got), feed.ExcerptTextBudget)
	}
}
