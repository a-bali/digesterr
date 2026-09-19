package feed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"digesterr/internal/store"
)

// A zero score from the upstream API must not destroy a known score. Each
// enricher speaks a different API, so each gets its own end-to-end case with
// a stubbed server. Reverting the applyScore call at any of these sites
// fails exactly its own test.
func TestRedditEnricherKeepsScoreOnZero(t *testing.T) {
	apiResp := []map[string]interface{}{
		{
			"data": map[string]interface{}{
				"children": []map[string]interface{}{
					{
						"data": map[string]interface{}{
							"id":           "1tlh5aj",
							"score":        0,
							"num_comments": 15,
						},
					},
				},
			},
		},
		{"data": map[string]interface{}{"children": []interface{}{}}},
	}
	respBody, _ := json.Marshal(apiResp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(respBody)
	}))
	defer server.Close()

	e := &RedditEnricher{client: server.Client(), apiBaseURL: server.URL, queries: store.New(nil)}
	item := &store.Item{Hash: "t3_1tlh5aj", Points: 42, Url: "https://www.reddit.com/r/test/comments/1tlh5aj/"}
	if err := e.Enrich(context.Background(), item); err != nil {
		t.Fatalf("Enrich failed: %v", err)
	}
	if item.Points != 42 {
		t.Fatalf("Enrich with zero score overwrote 42, got %d", item.Points)
	}
	// Comments still update: only the score is guarded.
	if item.CommentsCount != 15 {
		t.Fatalf("expected comments_count 15, got %d", item.CommentsCount)
	}
}

func TestLobstersEnricherKeepsScoreOnZero(t *testing.T) {
	apiResp := map[string]interface{}{
		"short_id":      "lvodx4",
		"score":         0,
		"comment_count": 12,
	}
	respBody, _ := json.Marshal(apiResp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(respBody)
	}))
	defer server.Close()

	e := &LobstersEnricher{client: server.Client(), baseURL: server.URL}
	item := &store.Item{
		CommentUrl: "https://lobste.rs/s/lvodx4/deconstructing_datalog",
		Points:     88,
	}
	if err := e.Enrich(context.Background(), item); err != nil {
		t.Fatalf("Enrich failed: %v", err)
	}
	if item.Points != 88 {
		t.Fatalf("Enrich with zero score overwrote 88, got %d", item.Points)
	}
	if item.CommentsCount != 12 {
		t.Fatalf("expected comments_count 12, got %d", item.CommentsCount)
	}
}

func TestHNEnricherKeepsScoreOnZero(t *testing.T) {
	firebaseResp := map[string]interface{}{
		"id":          123,
		"score":       0,
		"descendants": 15,
		"title":       "Test Story",
		"url":         "https://example.com",
		"by":          "testuser",
	}
	respBody, _ := json.Marshal(firebaseResp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(respBody)
	}))
	defer server.Close()

	e := &HNEnricher{client: server.Client(), firebaseURL: server.URL}
	item := &store.Item{
		CommentUrl: "https://news.ycombinator.com/item?id=123",
		Points:     42,
	}
	if err := e.Enrich(context.Background(), item); err != nil {
		t.Fatalf("Enrich failed: %v", err)
	}
	if item.Points != 42 {
		t.Fatalf("Enrich with zero score overwrote 42, got %d", item.Points)
	}
	if item.CommentsCount != 15 {
		t.Fatalf("expected comments_count 15, got %d", item.CommentsCount)
	}
}
