package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"digesterr/internal/auth"
	"digesterr/internal/config"
	"digesterr/internal/digest"
	"digesterr/internal/logbuf"
	"digesterr/internal/store"
)

// lengthLabel renders a window ("2-3") when it spans more than one sentence and
// a plain count otherwise, so both read naturally in the prompt.
func TestLengthLabel(t *testing.T) {
	tests := []struct {
		min, max int
		want     string
	}{
		{2, 3, "2-3"},
		{4, 5, "4-5"},
		{10, 15, "10-15"},
		{5, 5, "5"},
		{0, 5, "5"},
		{3, 0, "3"},
	}
	for _, tt := range tests {
		if got := lengthLabel(tt.min, tt.max); got != tt.want {
			t.Errorf("lengthLabel(%d, %d) = %q, want %q", tt.min, tt.max, got, tt.want)
		}
	}
}

// The UI offers length presets described as a range, so the window has to reach
// the prompt; a bare count made the model undershoot the longer presets.
func TestSummarizeItemPassesLengthWindowToPrompt(t *testing.T) {
	var gotBody string

	db, queries := setupTestDB(t)
	authSvc := auth.NewService("test-jwt-secret-for-testing", queries, db)
	cfg := config.Default()
	cfg.LLM.SummaryPrompt = "Content in {content_sentences} sentences, comments in {comment_sentences}."
	server := NewServer(db, queries, authSvc, logbuf.New(100), cfg)

	mockLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": `{"content_summary":"a","comments_summary":"b"}`}},
			},
		})
	}))
	t.Cleanup(mockLLM.Close)
	server.llmClient = digest.NewLLMClient(mockLLM.URL, "test-key", "test-model")

	user := createTestUser(t, queries, "summarize-window@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Window Feed",
		Url:    "https://example.com/feed",
	})
	if err != nil {
		t.Fatalf("failed to create feed: %v", err)
	}
	item, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID:  feed.ID,
		Hash:    "test-hash-window",
		Title:   "Length window",
		Url:     "https://example.com/window",
		Content: "<p>Body text used for the summary request.</p>",
	})
	if err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	w := makeRequest(server, http.MethodPost, "/api/items/"+itoa(item.ID)+"/summarize",
		map[string]any{"sentence_count": 15, "min_sentences": 10, "max_sentences": 15}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	// 10-15 content sentences -> a 3-5 comment window.
	if !strings.Contains(gotBody, "Content in 10-15 sentences, comments in 3-5.") {
		t.Fatalf("length window was not passed to the prompt, request body: %s", gotBody)
	}
}

// A caller that only knows a single count keeps working: the window collapses
// to that count instead of rendering an empty or inverted range.
func TestSummarizeItemSingleSentenceCountStillWorks(t *testing.T) {
	var gotBody string

	db, queries := setupTestDB(t)
	authSvc := auth.NewService("test-jwt-secret-for-testing", queries, db)
	cfg := config.Default()
	cfg.LLM.SummaryPrompt = "Content in {content_sentences} sentences, comments in {comment_sentences}."
	server := NewServer(db, queries, authSvc, logbuf.New(100), cfg)

	mockLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": `{"content_summary":"a","comments_summary":"b"}`}},
			},
		})
	}))
	t.Cleanup(mockLLM.Close)
	server.llmClient = digest.NewLLMClient(mockLLM.URL, "test-key", "test-model")

	user := createTestUser(t, queries, "summarize-single@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feed, _ := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Single Feed", Url: "https://example.com/feed2",
	})
	item, _ := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: feed.ID, Hash: "test-hash-single", Title: "Single count",
		Url: "https://example.com/single", Content: "<p>Body text.</p>",
	})

	w := makeRequest(server, http.MethodPost, "/api/items/"+itoa(item.ID)+"/summarize",
		map[string]any{"sentence_count": 9}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(gotBody, "Content in 9 sentences, comments in 3.") {
		t.Fatalf("single sentence count was not rendered, request body: %s", gotBody)
	}
}
