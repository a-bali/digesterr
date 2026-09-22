package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

// The list response must carry a ready-to-render excerpt: the row is plain text
// and the stored bodies are HTML, so flattening in the client is what let a
// character-capped body prefix become no excerpt at all.
func TestListItemsCarriesExcerpt(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "excerpt@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feedRow, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Excerpt", Url: "https://example.com/excerpt",
		Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	// A body whose first word sits behind more markup than the old 500-char cut
	// allowed. This is the production shape that lost its excerpt.
	padding := strings.Repeat("https://publisher.example/img/very-long-name,w_1024,f_webp ", 45)
	body := `<div id="readability-page-1"><picture><source srcset="` + padding +
		`"></picture></div><div><p>Believe me, the words are here.</p></div>`
	if _, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: feedRow.ID, Hash: "h-excerpt", Title: "Has text behind markup",
		Url: "https://example.com/excerpt/1", Content: body, Status: "unread",
	}); err != nil {
		t.Fatalf("create item: %v", err)
	}

	// An extracted body must win over the feed's own body.
	if _, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: feedRow.ID, Hash: "h-excerpt-2", Title: "Extracted",
		Url: "https://example.com/excerpt/2", Content: "<p>Feed body</p>", Status: "unread",
	}); err != nil {
		t.Fatalf("create item: %v", err)
	}
	items, err := queries.ListItemsByFeedPaginated(t.Context(), store.ListItemsByFeedPaginatedParams{
		FeedID: feedRow.ID, Limit: 10, Offset: 0,
	})
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	var extractedID int64
	for _, it := range items {
		if it.Hash == "h-excerpt-2" {
			extractedID = it.ID
		}
	}
	if extractedID == 0 {
		t.Fatal("extracted item not found")
	}
	if _, err := queries.UpdateItemExtraction(t.Context(), store.UpdateItemExtractionParams{
		ExtractContent: "<p>Extracted opening text</p>", ExtractStatus: "done", ID: extractedID,
	}); err != nil {
		t.Fatalf("update extraction: %v", err)
	}

	w := makeRequest(server, http.MethodGet, "/api/feeds/"+itoa(feedRow.ID)+"/items?limit=10", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("list items: %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []struct {
			Title   string `json:"title"`
			Excerpt string `json:"excerpt"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byTitle := map[string]string{}
	for _, it := range resp.Items {
		byTitle[it.Title] = it.Excerpt
	}
	if got := byTitle["Has text behind markup"]; got != "Believe me, the words are here." {
		t.Errorf("excerpt behind markup = %q, want the article's opening sentence", got)
	}
	if got := byTitle["Extracted"]; got != "Extracted opening text" {
		t.Errorf("excerpt = %q, want the extracted body's text", got)
	}
}

// The excerpt must not be able to smuggle markup into the row, and it must not
// grow with the article: the list is the hot path for every pane render.
func TestListItemsExcerptIsBoundedPlainText(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "bounded@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feedRow, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Long", Url: "https://example.com/long",
		Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	body := `<p><iframe src="https://www.youtube.com/embed/abc"><span data-mce-type="bookmark">x</span></iframe></p>` +
		"<p>" + strings.Repeat("word ", 20000) + "</p>"
	if _, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: feedRow.ID, Hash: "h-long", Title: "Long body", Url: "https://example.com/long/1",
		Content: body, Status: "unread",
	}); err != nil {
		t.Fatalf("create item: %v", err)
	}

	w := makeRequest(server, http.MethodGet, "/api/feeds/"+itoa(feedRow.ID)+"/items?limit=10", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("list items: %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []struct {
			Excerpt string `json:"excerpt"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(resp.Items))
	}
	excerpt := resp.Items[0].Excerpt
	if strings.Contains(excerpt, "<") || strings.Contains(excerpt, "data-mce-type") {
		t.Errorf("excerpt carries markup: %q", excerpt)
	}
	if !strings.HasPrefix(excerpt, "word word") {
		t.Errorf("excerpt = %q, want it to start at the body's text", excerpt)
	}
	// The budget counts non-whitespace characters, so the block separators the
	// walk inserts are the only thing allowed past it.
	visible := 0
	for _, r := range excerpt {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			visible++
		}
	}
	if visible > feed.ExcerptTextBudget+1 {
		t.Errorf("visible characters = %d, want <= %d", visible, feed.ExcerptTextBudget)
	}
}

// The saved view and the multi-feed/category view are separate queries and both
// feed the same row component, so both must carry the excerpt.
func TestSavedAndMultiFeedItemsCarryExcerpt(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "views@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feedRow, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Views", Url: "https://example.com/views",
		Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	item, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: feedRow.ID, Hash: "h-views", Title: "Saved item",
		Url: "https://example.com/views/1", Content: "<p>Saved item body</p>", Status: "unread",
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	if _, err := queries.SetItemSaved(t.Context(), store.SetItemSavedParams{ID: item.ID, Saved: 1, MarkRead: false}); err != nil {
		t.Fatalf("save item: %v", err)
	}

	decodeExcerpts := func(w *http.Response, path string) map[string]string {
		t.Helper()
		if w == nil || w.Body == nil {
			t.Fatalf("%s: no response", path)
		}
		var resp struct {
			Items []struct {
				Title   string `json:"title"`
				Excerpt string `json:"excerpt"`
			} `json:"items"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("%s decode: %v", path, err)
		}
		out := map[string]string{}
		for _, it := range resp.Items {
			out[it.Title] = it.Excerpt
		}
		return out
	}

	saved := makeRequest(server, http.MethodGet, "/api/saved", nil, token)
	if saved.Code != http.StatusOK {
		t.Fatalf("saved items: %d: %s", saved.Code, saved.Body.String())
	}
	if got := decodeExcerpts(saved.Result(), "/api/saved")["Saved item"]; got != "Saved item body" {
		t.Errorf("saved excerpt = %q, want %q", got, "Saved item body")
	}

	multi := makeRequest(server, http.MethodGet, "/api/items/multi?feed_ids="+itoa(feedRow.ID)+"&limit=10", nil, token)
	if multi.Code != http.StatusOK {
		t.Fatalf("multi items: %d: %s", multi.Code, multi.Body.String())
	}
	if got := decodeExcerpts(multi.Result(), "/api/items/multi")["Saved item"]; got != "Saved item body" {
		t.Errorf("multi-feed excerpt = %q, want %q", got, "Saved item body")
	}

	// The multi-feed handler maps columns by NAME, so an unaliased substr()
	// leaves the row's body empty. Guard both columns, not just the excerpt.
	raw := makeRequest(server, http.MethodGet, "/api/items/multi?feed_ids="+itoa(feedRow.ID)+"&limit=10", nil, token)
	var bodyResp struct {
		Items []struct {
			Content        string `json:"content"`
			ExtractContent string `json:"extract_content"`
		} `json:"items"`
	}
	if err := json.NewDecoder(raw.Body).Decode(&bodyResp); err != nil {
		t.Fatalf("decode multi-feed bodies: %v", err)
	}
	if len(bodyResp.Items) != 1 {
		t.Fatalf("multi-feed items = %d, want 1", len(bodyResp.Items))
	}
	if !strings.Contains(bodyResp.Items[0].Content, "Saved item body") {
		t.Errorf("multi-feed content = %q, want the item body", bodyResp.Items[0].Content)
	}
}
