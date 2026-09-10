package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"digesterr/internal/auth"
	"digesterr/internal/config"
	"digesterr/internal/digest"
	"digesterr/internal/logbuf"
	"digesterr/internal/store"
)

// setupTestServerWithLLM creates a test server with a mock LLM endpoint.
func setupTestServerWithLLM(t *testing.T, llmResponse string) (*Server, *store.Queries) {
	t.Helper()
	db, queries := setupTestDB(t)
	authSvc := auth.NewService("test-jwt-secret-for-testing", queries, db)
	server := NewServer(db, queries, authSvc, logbuf.New(100), config.Default())

	// Set up a mock LLM server that returns a canned response.
	mockLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": llmResponse}},
			},
		})
	}))
	t.Cleanup(mockLLM.Close)

	server.llmClient = digest.NewLLMClient(mockLLM.URL, "test-key", "test-model")
	return server, queries
}

func TestSummarizeItem(t *testing.T) {
	llmResponse := `{"content_summary":"This article discusses important topics.","comments_summary":"Commenters had mixed opinions."}`
	server, queries := setupTestServerWithLLM(t, llmResponse)

	user := createTestUser(t, queries, "summarize-test@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Test Feed",
		Url:    "https://example.com/feed",
	})
	if err != nil {
		t.Fatalf("failed to create feed: %v", err)
	}

	item, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID:        feed.ID,
		Hash:          "test-hash",
		Title:         "Test Article",
		Url:           "https://example.com/article",
		Content:       "<p>This is test content about an important topic with many details.</p>",
		CommentUrl:    "https://example.com/article/comments",
		CommentsCount: 5,
	})
	if err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	w := makeRequest(server, http.MethodPost, "/api/items/"+itoa(item.ID)+"/summarize",
		map[string]any{"sentence_count": 3}, token)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp summarizeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.ContentSummary == "" {
		t.Fatal("expected non-empty content_summary")
	}
	if resp.CommentsSummary == "" {
		t.Fatal("expected non-empty comments_summary")
	}
}

func TestSummarizeItemWithExtractedContent(t *testing.T) {
	llmResponse := `{"content_summary":"Summary of extracted content.","comments_summary":"No notable comments."}`
	server, queries := setupTestServerWithLLM(t, llmResponse)

	user := createTestUser(t, queries, "summarize-extracted@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Test Feed",
		Url:    "https://example.com/feed",
	})
	if err != nil {
		t.Fatalf("failed to create feed: %v", err)
	}

	item, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID:        feed.ID,
		Hash:          "test-hash-extracted",
		Title:         "Article with Extraction",
		Url:           "https://example.com/article2",
		Content:       "<p>Original RSS content.</p>",
		CommentUrl:    "https://example.com/article2/comments",
		CommentsCount: 3,
	})
	if err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	// Set extracted content via update (same as the extract handler does).
	_, err = queries.UpdateItemExtraction(t.Context(), store.UpdateItemExtractionParams{
		ID:             item.ID,
		ExtractContent: "<p>This is the full extracted content from the web page with much more detail.</p>",
		ExtractStatus:  "done",
		ExtractRetries: 0,
	})
	if err != nil {
		t.Fatalf("failed to update extraction: %v", err)
	}

	w := makeRequest(server, http.MethodPost, "/api/items/"+itoa(item.ID)+"/summarize",
		map[string]any{"sentence_count": 2}, token)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp summarizeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.ContentSummary != "Summary of extracted content." {
		t.Fatalf("expected extracted content summary, got %q", resp.ContentSummary)
	}
}

func TestSummarizeItemNoLLMKey(t *testing.T) {
	db, queries := setupTestDB(t)
	authSvc := auth.NewService("test-jwt-secret-for-testing", queries, db)
	server := NewServer(db, queries, authSvc, logbuf.New(100), config.Default())
	// llmClient not set — HasAPIKey() will return false.

	user := createTestUser(t, queries, "summarize-nollm@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Test Feed",
		Url:    "https://example.com/feed",
	})
	if err != nil {
		t.Fatalf("failed to create feed: %v", err)
	}

	item, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID:        feed.ID,
		Hash:          "test-hash-nollm",
		Title:         "Test",
		Url:           "https://example.com/test",
		Content:       "<p>Some content.</p>",
		CommentUrl:    "",
		CommentsCount: 0,
	})
	if err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	w := makeRequest(server, http.MethodPost, "/api/items/"+itoa(item.ID)+"/summarize",
		map[string]any{"sentence_count": 3}, token)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSummarizeItemNotFound(t *testing.T) {
	server, queries := setupTestServerWithLLM(t, `{"content_summary":"x"}`)
	user := createTestUser(t, queries, "summarize-404@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	w := makeRequest(server, http.MethodPost, "/api/items/99999/summarize",
		map[string]any{"sentence_count": 3}, token)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSummarizeItemSentenceCountClamped(t *testing.T) {
	llmResponse := `{"content_summary":"Short summary.","comments_summary":"Brief."}`
	server, queries := setupTestServerWithLLM(t, llmResponse)

	user := createTestUser(t, queries, "summarize-clamp@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Test Feed",
		Url:    "https://example.com/feed",
	})
	if err != nil {
		t.Fatalf("failed to create feed: %v", err)
	}

	item, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID:        feed.ID,
		Hash:          "test-hash-clamp",
		Title:         "Test",
		Url:           "https://example.com/test",
		Content:       "<p>Content.</p>",
		CommentUrl:    "",
		CommentsCount: 0,
	})
	if err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	// Excessive sentence count should be clamped to 50.
	w := makeRequest(server, http.MethodPost, "/api/items/"+itoa(item.ID)+"/summarize",
		map[string]any{"sentence_count": 100}, token)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSummarizeItemCustomPrompt(t *testing.T) {
	llmResponse := `{"content_summary":"Custom prompt summary.","comments_summary":"Custom comments."}`
	server, queries := setupTestServerWithLLM(t, llmResponse)

	user := createTestUser(t, queries, "summarize-custom@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Test Feed",
		Url:    "https://example.com/feed",
	})
	if err != nil {
		t.Fatalf("failed to create feed: %v", err)
	}

	item, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID:        feed.ID,
		Hash:          "test-hash-custom",
		Title:         "Test",
		Url:           "https://example.com/test",
		Content:       "<p>Content.</p>",
		CommentUrl:    "",
		CommentsCount: 0,
	})
	if err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	w := makeRequest(server, http.MethodPost, "/api/items/"+itoa(item.ID)+"/summarize",
		map[string]any{"sentence_count": 2, "prompt": "Summarize in 2 sentences as JSON."}, token)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp summarizeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.ContentSummary != "Custom prompt summary." {
		t.Fatalf("expected custom prompt summary, got %q", resp.ContentSummary)
	}
}
