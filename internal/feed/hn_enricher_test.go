package feed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"digesterr/internal/store"
)

func TestExtractHNID(t *testing.T) {
	tests := []struct {
		name       string
		commentURL string
		itemURL    string
		want       int
	}{
		{
			name:       "standard HN comment URL",
			commentURL: "https://news.ycombinator.com/item?id=48533848",
			want:       48533848,
		},
		{
			name:       "fallback to item URL when comment URL empty",
			commentURL: "",
			itemURL:    "https://news.ycombinator.com/item?id=48533848",
			want:       48533848,
		},
		{
			name:       "prefers comment URL over item URL",
			commentURL: "https://news.ycombinator.com/item?id=111",
			itemURL:    "https://news.ycombinator.com/item?id=222",
			want:       111,
		},
		{
			name:       "non-HN URL returns 0",
			commentURL: "https://example.com/item?id=123",
			want:       0,
		},
		{
			name:       "missing id param",
			commentURL: "https://news.ycombinator.com/some-page",
			want:       0,
		},
		{
			name:       "zero id",
			commentURL: "https://news.ycombinator.com/item?id=0",
			want:       0,
		},
		{
			name:       "algolia HN URL",
			commentURL: "https://hn.algolia.com/api/v1/items/48533848?id=42",
			want:       42,
		},
		{
			name:       "both URLs empty",
			commentURL: "",
			itemURL:    "",
			want:       0,
		},
		{
			name:       "malformed URL",
			commentURL: ":bad://",
			want:       0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractHNID(tt.commentURL, tt.itemURL)
			if got != tt.want {
				t.Fatalf("extractHNID(%q, %q) = %d, want %d", tt.commentURL, tt.itemURL, got, tt.want)
			}
		})
	}
}

func TestNewHNEnricher(t *testing.T) {
	e := NewHNEnricher()
	if e == nil {
		t.Fatal("NewHNEnricher returned nil")
	}
	if e.client.Timeout != 10*time.Second {
		t.Fatalf("expected timeout 10s, got %v", e.client.Timeout)
	}
}

func TestHNEnricherEnrich(t *testing.T) {
	firebaseResp := map[string]interface{}{
		"id":          123,
		"score":       42,
		"descendants": 15,
		"title":       "Test Story",
		"url":         "https://example.com",
		"by":          "testuser",
	}
	respBody, _ := json.Marshal(firebaseResp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/v0/item/123.json" {
			t.Fatalf("expected path /v0/item/123.json, got %s", got)
		}
		w.Write(respBody)
	}))
	defer server.Close()

	e := &HNEnricher{client: server.Client(), firebaseURL: server.URL}
	item := &store.Item{
		CommentUrl: "https://news.ycombinator.com/item?id=123",
	}
	if err := e.Enrich(context.Background(), item); err != nil {
		t.Fatalf("Enrich failed: %v", err)
	}
	if item.Points != 42 {
		t.Fatalf("expected points 42, got %d", item.Points)
	}
	if item.CommentsCount != 15 {
		t.Fatalf("expected comments_count 15, got %d", item.CommentsCount)
	}
}

func TestHNEnricherEnrichNoID(t *testing.T) {
	e := &HNEnricher{client: &http.Client{}}
	item := &store.Item{CommentUrl: "https://example.com/wiki"}
	if err := e.Enrich(context.Background(), item); err == nil {
		t.Fatal("expected error for non-HN URL")
	}
}

func TestHNEnricherEnrichNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("null"))
	}))
	defer server.Close()

	e := &HNEnricher{client: server.Client(), firebaseURL: server.URL}
	item := &store.Item{
		CommentUrl: "https://news.ycombinator.com/item?id=404",
	}
	if err := e.Enrich(context.Background(), item); err == nil {
		t.Fatal("expected error for 404 response")
	}
}

func TestHNEnricherEnrichInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer server.Close()

	e := &HNEnricher{client: server.Client(), firebaseURL: server.URL}
	item := &store.Item{
		CommentUrl: "https://news.ycombinator.com/item?id=1",
	}
	if err := e.Enrich(context.Background(), item); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestHNEnricherEnrichContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1 * time.Second)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	e := &HNEnricher{client: server.Client(), firebaseURL: server.URL}
	item := &store.Item{
		CommentUrl: "https://news.ycombinator.com/item?id=1",
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Enrich(ctx, item); err == nil {
		t.Fatal("expected error for canceled context")
	}
}
