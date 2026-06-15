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

func TestExtractLobstersShortID(t *testing.T) {
	tests := []struct {
		name        string
		commentURL  string
		itemURL     string
		guid        string
		want        string
	}{
		{
			name:       "extract from comment URL with slug",
			commentURL: "https://lobste.rs/s/lvodx4/deconstructing_datalog",
			want:       "lvodx4",
		},
		{
			name:       "extract from comment URL without slug",
			commentURL: "https://lobste.rs/s/lvodx4",
			want:       "lvodx4",
		},
		{
			name:    "fallback to guid",
			commentURL: "",
			itemURL: "https://example.com/story",
			guid:    "https://lobste.rs/s/1xyby4",
			want:    "1xyby4",
		},
		{
			name:       "prefers comment URL over guid",
			commentURL: "https://lobste.rs/s/abc/foo",
			guid:       "https://lobste.rs/s/xyz",
			want:       "abc",
		},
		{
			name:       "non-Lobsters URL returns empty",
			commentURL: "https://example.com/s/abc",
			want:       "",
		},
		{
			name:       "invalid path without /s/",
			commentURL: "https://lobste.rs/t/abc",
			want:       "",
		},
		{
			name:       "malformed URL",
			commentURL: ":bad://",
			want:       "",
		},
		{
			name:        "all empty",
			commentURL:  "",
			itemURL:     "",
			guid:        "",
			want:        "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractLobstersShortID(tt.commentURL, tt.itemURL, tt.guid)
			if got != tt.want {
				t.Fatalf("extractLobstersShortID(%q, %q, %q) = %q, want %q",
					tt.commentURL, tt.itemURL, tt.guid, got, tt.want)
			}
		})
	}
}

func TestNewLobstersEnricher(t *testing.T) {
	e := NewLobstersEnricher()
	if e == nil {
		t.Fatal("NewLobstersEnricher returned nil")
	}
	if e.client.Timeout != 10*time.Second {
		t.Fatalf("expected timeout 10s, got %v", e.client.Timeout)
	}
	if e.baseURL != "https://lobste.rs" {
		t.Fatalf("expected baseURL https://lobste.rs, got %s", e.baseURL)
	}
}

func TestLobstersEnricherEnrich(t *testing.T) {
	apiResp := map[string]interface{}{
		"short_id":      "lvodx4",
		"score":         88,
		"comment_count": 12,
	}
	respBody, _ := json.Marshal(apiResp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/s/lvodx4.json" {
			t.Fatalf("expected path /s/lvodx4.json, got %s", got)
		}
		w.Write(respBody)
	}))
	defer server.Close()

	e := &LobstersEnricher{client: server.Client(), baseURL: server.URL}
	item := &store.Item{
		CommentUrl: "https://lobste.rs/s/lvodx4/deconstructing_datalog",
	}
	if err := e.Enrich(context.Background(), item); err != nil {
		t.Fatalf("Enrich failed: %v", err)
	}
	if item.Points != 88 {
		t.Fatalf("expected points 88, got %d", item.Points)
	}
	if item.CommentsCount != 12 {
		t.Fatalf("expected comments_count 12, got %d", item.CommentsCount)
	}
}

func TestLobstersEnricherEnrichNoID(t *testing.T) {
	e := &LobstersEnricher{client: &http.Client{}}
	item := &store.Item{CommentUrl: "https://example.com/s/abc"}
	if err := e.Enrich(context.Background(), item); err == nil {
		t.Fatal("expected error for non-Lobsters URL")
	}
}

func TestLobstersEnricherEnrichNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("null"))
	}))
	defer server.Close()

	e := &LobstersEnricher{client: server.Client(), baseURL: server.URL}
	item := &store.Item{
		CommentUrl: "https://lobste.rs/s/missing",
	}
	if err := e.Enrich(context.Background(), item); err == nil {
		t.Fatal("expected error for 404 response")
	}
}

func TestLobstersEnricherEnrichInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer server.Close()

	e := &LobstersEnricher{client: server.Client(), baseURL: server.URL}
	item := &store.Item{
		CommentUrl: "https://lobste.rs/s/abc",
	}
	if err := e.Enrich(context.Background(), item); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLobstersEnricherEnrichContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1 * time.Second)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	e := &LobstersEnricher{client: server.Client(), baseURL: server.URL}
	item := &store.Item{
		CommentUrl: "https://lobste.rs/s/abc",
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Enrich(ctx, item); err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestLobstersEnricherEnrichEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"short_id": "", "score": 0, "comment_count": 0}`))
	}))
	defer server.Close()

	e := &LobstersEnricher{client: server.Client(), baseURL: server.URL}
	item := &store.Item{
		CommentUrl: "https://lobste.rs/s/missing",
	}
	if err := e.Enrich(context.Background(), item); err == nil {
		t.Fatal("expected error for empty short_id in response")
	}
}
