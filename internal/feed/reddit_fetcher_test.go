package feed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestRedditFetcherFetch(t *testing.T) {
	// Mock OAuth token endpoint
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/access_token" {
			t.Errorf("unexpected token path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "testid" || pass != "testsecret" {
			t.Errorf("unexpected basic auth: user=%q pass=%q", user, pass)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "mock-token-123",
			"token_type":   "bearer",
			"expires_in":   3600,
			"scope":        "read",
		})
	}))
	defer tokenServer.Close()

	// Mock Reddit JSON API
	listingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/r/") {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer mock-token-123" {
			t.Errorf("missing or wrong Authorization header: %q", r.Header.Get("Authorization"))
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"children": []map[string]any{
					{
						"kind": "t3",
						"data": map[string]any{
							"id":            "abc123",
							"title":         "Test post title",
							"author":        "testuser",
							"url":           "https://example.com/article",
							"permalink":     "/r/testsub/comments/abc123/test_post_title/",
							"selftext":      "",
							"score":         42,
							"num_comments":  7,
							"created_utc":   1693000000.0,
							"is_self":       false,
							"over_18":       false,
							"stickied":      false,
						},
					},
					{
						"kind": "t3",
						"data": map[string]any{
							"id":            "def456",
							"title":         "Self post with text",
							"author":        "anotheruser",
							"url":           "https://www.reddit.com/r/testsub/comments/def456/self_post_with_text/",
							"permalink":     "/r/testsub/comments/def456/self_post_with_text/",
							"selftext":      "This is the self post body.",
							"score":         5,
							"num_comments":  2,
							"created_utc":   1693000100.0,
							"is_self":       true,
							"over_18":       false,
							"stickied":      false,
						},
					},
					{
						"kind": "t3",
						"data": map[string]any{
							"id":           "nsfw789",
							"title":        "NSFW post",
							"author":       "nsfwuser",
							"url":          "https://example.com/nsfw",
							"permalink":    "/r/testsub/comments/nsfw789/nsfw_post/",
							"score":        100,
							"num_comments": 50,
							"created_utc":  1693000200.0,
							"is_self":      false,
							"over_18":      true,
							"stickied":     false,
						},
					},
					{
						"kind": "t3",
						"data": map[string]any{
							"id":           "stickied000",
							"title":        "Stickied post",
							"author":       "moderator",
							"url":          "https://example.com/sticky",
							"permalink":    "/r/testsub/comments/stickied000/stickied_post/",
							"score":        10,
							"num_comments": 0,
							"created_utc":  1693000300.0,
							"is_self":      false,
							"over_18":      false,
							"stickied":     true,
						},
					},
				},
			},
		})
	}))
	defer listingServer.Close()

	fetcher := &RedditFetcher{
		client:       &http.Client{},
		baseURL:      tokenServer.URL,
		apiBaseURL:   listingServer.URL,
		clientID:     "testid",
		clientSecret: "testsecret",
	}

	items, err := fetcher.Fetch(context.Background(), "https://www.reddit.com/r/testsub/new")
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	// Should have 2 items: 1 external link + 1 self post (NSFW and stickied filtered out)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	// Check first item (external link)
	if items[0].GUID != "reddit:abc123" {
		t.Errorf("item[0].GUID = %q, want reddit:abc123", items[0].GUID)
	}
	if items[0].Title != "Test post title" {
		t.Errorf("item[0].Title = %q, want 'Test post title'", items[0].Title)
	}
	if items[0].URL != "https://example.com/article" {
		t.Errorf("item[0].URL = %q, want 'https://example.com/article'", items[0].URL)
	}
	if items[0].Author != "u/testuser" {
		t.Errorf("item[0].Author = %q, want 'u/testuser'", items[0].Author)
	}
	if items[0].Points != 42 {
		t.Errorf("item[0].Points = %d, want 42", items[0].Points)
	}
	if items[0].CommentsCnt != 7 {
		t.Errorf("item[0].CommentsCnt = %d, want 7", items[0].CommentsCnt)
	}

	// Check second item (self post)
	if items[1].GUID != "reddit:def456" {
		t.Errorf("item[1].GUID = %q, want reddit:def456", items[1].GUID)
	}
	if !strings.Contains(items[1].Content, "This is the self post body") {
		t.Errorf("item[1].Content missing self text: %q", items[1].Content)
	}
	// Self post URL should be the permalink, not the self-referencing URL
	if items[1].URL != "https://www.reddit.com/r/testsub/comments/def456/self_post_with_text/" {
		t.Errorf("item[1].URL = %q, want permalink for self post", items[1].URL)
	}
}

func TestRedditFetcherExtractSubreddit(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://www.reddit.com/r/opencode", "opencode"},
		{"https://www.reddit.com/r/opencode.rss", "opencode"},
		{"https://www.reddit.com/r/opencode/new", "opencode"},
		{"https://www.reddit.com/r/programming/", "programming"},
		{"https://old.reddit.com/r/golang", "golang"},
		{"not-a-url", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractSubreddit(tt.url)
		if got != tt.want {
			t.Errorf("extractSubreddit(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestRedditFetcherNoCredentials(t *testing.T) {
	fetcher := &RedditFetcher{
		client:  &http.Client{},
		baseURL: "https://www.reddit.com",
	}
	_, err := fetcher.Fetch(context.Background(), "https://www.reddit.com/r/test")
	if err == nil {
		t.Fatal("expected error when no credentials set")
	}
	if !strings.Contains(err.Error(), "credentials not configured") {
		t.Errorf("error = %q, want 'credentials not configured'", err.Error())
	}
}

func TestRedditFetcherRateLimit(t *testing.T) {
	// Mock servers
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "mock-token",
			"expires_in":   3600,
		})
	}))
	defer tokenServer.Close()

	requestCount := 0
	listingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"children": []any{}},
		})
	}))
	defer listingServer.Close()

	fetcher := &RedditFetcher{
		client:       &http.Client{},
		baseURL:      tokenServer.URL,
		apiBaseURL:   listingServer.URL,
		clientID:     "id",
		clientSecret: "secret",
		limiter:      rate.NewLimiter(1, 1), // 1 rps, burst 1
	}

	ctx := context.Background()

	// First request should succeed immediately (burst=1)
	_, err := fetcher.Fetch(ctx, "https://www.reddit.com/r/test")
	if err != nil {
		t.Fatalf("first fetch failed: %v", err)
	}
	if requestCount != 1 {
		t.Errorf("requestCount = %d, want 1", requestCount)
	}

	// Second request immediately should be throttled (burst exhausted)
	// The limiter will wait up to ~1s; verify it blocks briefly
	done := make(chan struct{})
	go func() {
		fetcher.Fetch(ctx, "https://www.reddit.com/r/test")
		close(done)
	}()

	select {
	case <-done:
		// Should have completed (after ~1s wait from limiter)
	case <-time.After(2 * time.Second):
		t.Fatal("second fetch took too long — rate limiter may not be working")
	}
	if requestCount != 2 {
		t.Errorf("requestCount = %d, want 2", requestCount)
	}
}
