package feed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"digesterr/internal/store"
	"digesterr/internal/testutil"
)

func TestExtractRedditPostID(t *testing.T) {
	tests := []struct {
		name string
		ids  []string
		want string
	}{
		{
			name: "t3_ prefix",
			ids:  []string{"t3_1tlh5aj"},
			want: "1tlh5aj",
		},
		{
			name: "comments URL with slug",
			ids:  []string{"", "https://www.reddit.com/r/programming/comments/1tlh5aj/announcement/"},
			want: "1tlh5aj",
		},
		{
			name: "comments URL without slug",
			ids:  []string{"", "https://www.reddit.com/r/programming/comments/1tlh5aj/"},
			want: "1tlh5aj",
		},
		{
			name: "prefers t3_ over URL",
			ids:  []string{"t3_abc", "https://www.reddit.com/r/test/comments/xyz/"},
			want: "abc",
		},
		{
			name: "prefers comment URL over item URL",
			ids:  []string{"", "https://www.reddit.com/r/test/comments/111/", "https://www.reddit.com/r/test/comments/222/"},
			want: "111",
		},
		{
			name: "non-Reddit URL",
			ids:  []string{"", "https://example.com/comments/123/"},
			want: "",
		},
		{
			name: "no /comments/ in path",
			ids:  []string{"", "https://www.reddit.com/r/test/111/"},
			want: "",
		},
		{
			name: "all empty",
			ids:  []string{"", ""},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractRedditPostID(tt.ids...)
			if got != tt.want {
				t.Fatalf("extractRedditPostID(%v) = %q, want %q", tt.ids, got, tt.want)
			}
		})
	}
}

func TestNewRedditEnricher(t *testing.T) {
	e := NewRedditEnricher(nil)
	if e == nil {
		t.Fatal("NewRedditEnricher returned nil")
	}
	if e.client.Timeout != 10*time.Second {
		t.Fatalf("expected timeout 10s, got %v", e.client.Timeout)
	}
	if e.baseURL != "https://www.reddit.com" {
		t.Fatalf("expected baseURL https://www.reddit.com, got %s", e.baseURL)
	}
}

func TestRedditEnricherEnrich(t *testing.T) {
	db, err := testutil.OpenTestDB()
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()
	queries := store.New(db)

	apiResp := []map[string]interface{}{
		{
			"data": map[string]interface{}{
				"children": []map[string]interface{}{
					{
						"data": map[string]interface{}{
							"id":           "1tlh5aj",
							"score":        42,
							"num_comments": 15,
							"url":          "https://actual-article.com/story",
						},
					},
				},
			},
		},
		{"data": map[string]interface{}{"children": []interface{}{}}},
	}
	respBody, _ := json.Marshal(apiResp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/comments/1tlh5aj.json" {
			t.Fatalf("expected path /comments/1tlh5aj.json, got %s", got)
		}
		w.Write(respBody)
	}))
	defer server.Close()

	e := &RedditEnricher{client: server.Client(), baseURL: server.URL, queries: queries}
	item := &store.Item{ID: 1, Hash: "t3_1tlh5aj", Url: "https://www.reddit.com/r/test/comments/1tlh5aj/"}
	if err := e.Enrich(context.Background(), item); err != nil {
		t.Fatalf("Enrich failed: %v", err)
	}
	if item.Points != 42 {
		t.Fatalf("expected points 42, got %d", item.Points)
	}
	if item.CommentsCount != 15 {
		t.Fatalf("expected comments_count 15, got %d", item.CommentsCount)
	}
	if item.Url != "https://actual-article.com/story" {
		t.Fatalf("expected URL https://actual-article.com/story, got %s", item.Url)
	}
}

func TestRedditEnricherEnrichNoID(t *testing.T) {
	e := &RedditEnricher{client: &http.Client{}, queries: store.New(nil)}
	item := &store.Item{ID: 1, Hash: "", Url: "https://example.com/story"}
	if err := e.Enrich(context.Background(), item); err == nil {
		t.Fatal("expected error for non-Reddit URL")
	}
}

func TestRedditEnricherEnrichNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("{}"))
	}))
	defer server.Close()

	db, err := testutil.OpenTestDB()
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()
	e := &RedditEnricher{client: server.Client(), baseURL: server.URL, queries: store.New(db)}
	item := &store.Item{ID: 1, Hash: "t3_missing"}
	if err := e.Enrich(context.Background(), item); err == nil {
		t.Fatal("expected error for 404 response")
	}
}

func TestRedditEnricherEnrichInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer server.Close()

	e := &RedditEnricher{client: server.Client(), baseURL: server.URL, queries: store.New(nil)}
	item := &store.Item{ID: 1, Hash: "t3_abc"}
	if err := e.Enrich(context.Background(), item); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestRedditEnricherEnrichContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1 * time.Second)
		w.Write([]byte(`[]`))
	}))
	defer server.Close()

	e := &RedditEnricher{client: server.Client(), baseURL: server.URL, queries: store.New(nil)}
	item := &store.Item{ID: 1, Hash: "t3_abc"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Enrich(ctx, item); err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestRedditEnricherEnrichEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"data": {"children": []}}]`))
	}))
	defer server.Close()

	db, err := testutil.OpenTestDB()
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()
	e := &RedditEnricher{client: server.Client(), baseURL: server.URL, queries: store.New(db)}
	item := &store.Item{ID: 1, Hash: "t3_missing"}
	if err := e.Enrich(context.Background(), item); err == nil {
		t.Fatal("expected error for empty children array")
	}
}

func TestRedditEnricherEnrichKeepsRedditURL(t *testing.T) {
	db, err := testutil.OpenTestDB()
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()
	queries := store.New(db)

	apiResp := []map[string]interface{}{
		{
			"data": map[string]interface{}{
				"children": []map[string]interface{}{
					{
						"data": map[string]interface{}{
							"id":           "selfpost",
							"score":        10,
							"num_comments": 3,
							"url":          "https://www.reddit.com/r/test/comments/selfpost/",
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

	e := &RedditEnricher{client: server.Client(), baseURL: server.URL, queries: queries}
	item := &store.Item{ID: 1, Hash: "t3_selfpost", Url: "https://www.reddit.com/r/test/comments/selfpost/"}
	if err := e.Enrich(context.Background(), item); err != nil {
		t.Fatalf("Enrich failed: %v", err)
	}
	if item.Url != "https://www.reddit.com/r/test/comments/selfpost/" {
		t.Fatalf("expected URL to remain unchanged, got %s", item.Url)
	}
}
