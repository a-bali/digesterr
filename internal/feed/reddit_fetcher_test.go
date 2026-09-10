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
					{
						"kind": "t3",
						"data": map[string]any{
							"id":        "gallery001",
							"title":     "Gallery post with images",
							"author":    "galleryuser",
							"url":       "https://www.reddit.com/gallery/gallery001",
							"permalink": "/r/testsub/comments/gallery001/gallery_post_with_images/",
							"selftext":  "",
							"score":     100,
							"num_comments": 15,
							"created_utc":  1693000400.0,
							"is_self":      false,
							"is_gallery":   true,
							"over_18":      false,
							"stickied":     false,
							"media_metadata": map[string]any{
								"img_a": map[string]any{
									"m": "image/jpeg",
									"s": map[string]any{"u": "https://preview.redd.it/gallery-photo1.jpg?width=640"},
								},
								"img_b": map[string]any{
									"m": "image/png",
									"s": map[string]any{"u": "https://i.redd.it/gallery-photo2.png"},
								},
							},
							"gallery_data": map[string]any{
								"items": []map[string]any{
									{"media_id": "img_a"},
									{"media_id": "img_b"},
								},
							},
						},
					},
					{
						"kind": "t3",
						"data": map[string]any{
							"id":        "video001",
							"title":     "Video post with fallback URL",
							"author":    "videouser",
							"url":       "https://v.redd.it/abc123video",
							"permalink": "/r/testsub/comments/video001/video_post_with_fallback_url/",
							"selftext":  "",
							"score":     50,
							"num_comments": 3,
							"created_utc":  1693000500.0,
							"is_self":      false,
							"is_video":     true,
							"over_18":      false,
							"stickied":     false,
							"media": map[string]any{
								"reddit_video": map[string]any{
									"fallback_url": "https://v.redd.it/abc123video/DASH_720.mp4",
									"height":       720,
									"width":        1280,
									"is_gif":       false,
								},
							},
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

	// Should have 4 items: 1 external link + 1 self post + 1 gallery + 1 video (NSFW and stickied filtered out)
	if len(items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(items))
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

	// Check third item (gallery post)
	if items[2].GUID != "reddit:gallery001" {
		t.Errorf("item[2].GUID = %q, want reddit:gallery001", items[2].GUID)
	}
	if items[2].Title != "Gallery post with images" {
		t.Errorf("item[2].Title = %q, want 'Gallery post with images'", items[2].Title)
	}
	if items[2].Points != 100 {
		t.Errorf("item[2].Points = %d, want 100", items[2].Points)
	}
	// Gallery content should contain inline images, not a bare link
	if !strings.Contains(items[2].Content, "<img src=") {
		t.Errorf("gallery content should contain <img> tags, got: %q", items[2].Content)
	}
	if strings.Count(items[2].Content, "<img src=") != 2 {
		t.Errorf("gallery content should have 2 images, got: %q", items[2].Content)
	}
	// Gallery URL should be the permalink
	if items[2].URL != "https://www.reddit.com/gallery/gallery001" {
		t.Errorf("item[2].URL = %q, want gallery URL", items[2].URL)
	}

	// Check fourth item (video post)
	if items[3].GUID != "reddit:video001" {
		t.Errorf("item[3].GUID = %q, want reddit:video001", items[3].GUID)
	}
	if items[3].Points != 50 {
		t.Errorf("item[3].Points = %d, want 50", items[3].Points)
	}
	// Video content should use fallback_url, not the bare v.redd.it URL
	if !strings.Contains(items[3].Content, "<video") {
		t.Errorf("video content should contain <video> tag, got: %q", items[3].Content)
	}
	if !strings.Contains(items[3].Content, "DASH_720.mp4") {
		t.Errorf("video content should use fallback_url with DASH_720.mp4, got: %q", items[3].Content)
	}
	if strings.Contains(items[3].Content, `src="https://v.redd.it/abc123video"`) {
		t.Errorf("video content should NOT use bare v.redd.it URL, got: %q", items[3].Content)
	}
}

func TestParseListingGalleryWithSelftext(t *testing.T) {
	// Gallery posts that also have selftext (e.g. user explains the images)
	// must include both the gallery images AND the selftext in content.
	listingJSON := `{
		"data": {
			"children": [{
				"kind": "t3",
				"data": {
					"id": "zDGYkDKbuf",
					"title": "Built a 3-node Kubernetes homelab",
					"author": "root0ps",
					"url": "https://www.reddit.com/gallery/zDGYkDKbuf",
					"permalink": "/r/selfhosted/comments/zDGYkDKbuf/built_a_3_node/",
					"selftext": "I recently set up a 3-node Kubernetes homelab.\n\nThe setup uses three control-plane nodes.",
					"score": 107,
					"num_comments": 8,
					"created_utc": 1693000000.0,
					"is_self": false,
					"is_gallery": true,
					"over_18": false,
					"stickied": false,
					"gallery_data": {
						"items": [
							{"media_id": "img1"},
							{"media_id": "img2"}
						]
					},
					"media_metadata": {
						"img1": {"status": "valid", "e": "Image", "m": "image/jpeg", "s": {"u": "https://i.redd.it/photo1.jpg"}},
						"img2": {"status": "valid", "e": "Image", "m": "image/png", "s": {"u": "https://i.redd.it/photo2.png"}}
					}
				}
			}]
		}
	}`

	f := &RedditFetcher{}
	items, err := f.parseListing([]byte(listingJSON), "selfhosted")
	if err != nil {
		t.Fatalf("parseListing: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}

	item := items[0]

	// Must contain gallery images
	if !strings.Contains(item.Content, "<img src=\"https://i.redd.it/photo1.jpg\"") {
		t.Errorf("content missing first gallery image: %s", item.Content)
	}
	if !strings.Contains(item.Content, "<img src=\"https://i.redd.it/photo2.png\"") {
		t.Errorf("content missing second gallery image: %s", item.Content)
	}

	// Must also contain the selftext — this was the bug
	if !strings.Contains(item.Content, "3-node Kubernetes homelab") {
		t.Errorf("content missing selftext from gallery post: %s", item.Content)
	}
}

func TestParseListingSelftextOnly(t *testing.T) {
	// Self-posts with no media should still render selftext.
	listingJSON := `{
		"data": {
			"children": [{
				"kind": "t3",
				"data": {
					"id": "self123",
					"title": "Question about storage",
					"author": "user1",
					"url": "https://www.reddit.com/r/selfhosted/comments/self123/question_about_storage/",
					"permalink": "/r/selfhosted/comments/self123/question_about_storage/",
					"selftext": "What NAS do you recommend?",
					"score": 10,
					"num_comments": 5,
					"created_utc": 1693000000.0,
					"is_self": true,
					"over_18": false,
					"stickied": false
				}
			}]
		}
	}`

	f := &RedditFetcher{}
	items, err := f.parseListing([]byte(listingJSON), "selfhosted")
	if err != nil {
		t.Fatalf("parseListing: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if !strings.Contains(items[0].Content, "What NAS do you recommend?") {
		t.Errorf("content missing selftext: %s", items[0].Content)
	}
}

func TestParseListingCrosspost(t *testing.T) {
	// A crosspost's own payload is a stub: selftext is empty and the
	// gallery/media keys are absent entirely — the shared content lives in
	// crosspost_parent_list[0]. Before the fix these items were stored as a
	// single bare link to the parent's gallery and rendered as an empty post.
	// JSON below mirrors the real r/hungary post 1wbr8ey -> r/magyar 1wbr3af.
	listingJSON := `{
		"data": {
			"children": [{
				"kind": "t3",
				"data": {
					"id": "1wbr8ey",
					"title": "KERESSÜK! Eltünt a nagymamám!",
					"author": "someone",
					"url": "https://www.reddit.com/gallery/1wbr3af",
					"permalink": "/r/hungary/comments/1wbr8ey/keressuk_eltunt_a_nagymamam/",
					"selftext": "",
					"score": 591,
					"num_comments": 105,
					"created_utc": 1788972970.0,
					"is_self": false,
					"over_18": false,
					"stickied": false,
					"crosspost_parent": "t3_1wbr3af",
					"crosspost_parent_list": [{
						"id": "1wbr3af",
						"title": "KERESSÜK! Eltünt a nagymamám!",
						"author": "someone",
						"url": "https://www.reddit.com/gallery/1wbr3af",
						"permalink": "/r/magyar/comments/1wbr3af/keressuk_eltunt_a_nagymamam/",
						"selftext": "Tegnap délután 5 óra körül nagymamám elindult az Interspárba.",
						"score": 12,
						"num_comments": 3,
						"created_utc": 1788971000.0,
						"is_self": false,
						"is_gallery": true,
						"over_18": false,
						"stickied": false,
						"gallery_data": {"items": [{"media_id": "img1"}, {"media_id": "img2"}]},
						"media_metadata": {
							"img1": {"status": "valid", "e": "Image", "m": "image/jpg", "s": {"u": "https://preview.redd.it/bvil45owxioh1.jpg?width=1080"}},
							"img2": {"status": "valid", "e": "Image", "m": "image/jpg", "s": {"u": "https://preview.redd.it/second.jpg?width=1080"}}
						}
					}]
				}
			}]
		}
	}`

	f := &RedditFetcher{}
	items, err := f.parseListing([]byte(listingJSON), "hungary")
	if err != nil {
		t.Fatalf("parseListing: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	item := items[0]

	// The parent's gallery images must be rendered inline.
	if !strings.Contains(item.Content, "<img src=\"https://preview.redd.it/bvil45owxioh1.jpg") {
		t.Errorf("content missing parent gallery image 1: %s", item.Content)
	}
	if !strings.Contains(item.Content, "<img src=\"https://preview.redd.it/second.jpg") {
		t.Errorf("content missing parent gallery image 2: %s", item.Content)
	}
	// ...and the parent's selftext must be present.
	if !strings.Contains(item.Content, "nagymamám elindult az Interspárba") {
		t.Errorf("content missing parent selftext: %s", item.Content)
	}
	// The bare-link degradation is the bug — content must be more than a link.
	if strings.Count(item.Content, "<a href=") > 0 && !strings.Contains(item.Content, "<img") {
		t.Errorf("content degraded to a bare link: %s", item.Content)
	}
	if strings.Contains(item.Content, `rel="noopener">https://www.reddit.com/gallery/1wbr3af</a>`) {
		t.Errorf("content is the bare parent gallery link: %s", item.Content)
	}
	// The item still points at the crosspost's own target and comment thread.
	if item.URL != "https://www.reddit.com/gallery/1wbr3af" {
		t.Errorf("URL = %q", item.URL)
	}
	if item.CommentURL != "https://www.reddit.com/r/hungary/comments/1wbr8ey/keressuk_eltunt_a_nagymamam/" {
		t.Errorf("CommentURL = %q", item.CommentURL)
	}
}

func TestParseListingCrosspostKeepsOwnText(t *testing.T) {
	// A crosspost may carry the crossposter's own commentary in addition to the
	// shared content — both must survive.
	listingJSON := `{
		"data": {
			"children": [{
				"kind": "t3",
				"data": {
					"id": "cp1",
					"title": "Crosspost with commentary",
					"author": "reposter",
					"url": "https://example.com/article",
					"permalink": "/r/testsub/comments/cp1/crosspost/",
					"selftext": "Worth reading, this explains it well.",
					"score": 5,
					"num_comments": 1,
					"created_utc": 1693000000.0,
					"is_self": false,
					"over_18": false,
					"stickied": false,
					"crosspost_parent_list": [{
						"id": "op1",
						"title": "Original article",
						"author": "op",
						"url": "https://example.com/article",
						"permalink": "/r/other/comments/op1/original/",
						"selftext": "The original post body.",
						"score": 90,
						"num_comments": 40,
						"created_utc": 1692000000.0,
						"is_self": true,
						"over_18": false,
						"stickied": false
					}]
				}
			}]
		}
	}`

	f := &RedditFetcher{}
	items, err := f.parseListing([]byte(listingJSON), "testsub")
	if err != nil {
		t.Fatalf("parseListing: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	content := items[0].Content
	if !strings.Contains(content, "Worth reading, this explains it well.") {
		t.Errorf("content missing crossposter commentary: %s", content)
	}
	if !strings.Contains(content, "The original post body.") {
		t.Errorf("content missing parent selftext: %s", content)
	}
}

func TestParseListingCrosspostDoesNotAffectNormalPosts(t *testing.T) {
	// crosspost_parent_list is absent on ordinary posts; their content must be
	// unchanged by the crosspost handling.
	listingJSON := `{
		"data": {
			"children": [{
				"kind": "t3",
				"data": {
					"id": "plain1",
					"title": "Plain link post",
					"author": "user1",
					"url": "https://example.com/story",
					"permalink": "/r/testsub/comments/plain1/plain_link_post/",
					"selftext": "",
					"score": 3,
					"num_comments": 0,
					"created_utc": 1693000000.0,
					"is_self": false,
					"over_18": false,
					"stickied": false
				}
			}]
		}
	}`

	f := &RedditFetcher{}
	items, err := f.parseListing([]byte(listingJSON), "testsub")
	if err != nil {
		t.Fatalf("parseListing: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	want := `<p><a href="https://example.com/story" target="_blank" rel="noopener">https://example.com/story</a></p>`
	if items[0].Content != want {
		t.Errorf("content = %q, want %q", items[0].Content, want)
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

func TestRenderMarkdownTable(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string
		excludes []string
	}{
		{
			name: "basic 3-column table",
			input: `| Name | Value | Notes |
| --- | --- | --- |
| foo | bar | baz |
| one | two | three |`,
			contains: []string{"<table>", "<thead>", "<th>Name</th>", "<th>Value</th>", "<td>foo</td>", "<td>bar</td>", "<td>one</td>", "</table>"},
		},
		{
			name: "table without leading pipes",
			input: `Col A | Col B
--- | ---
x | y`,
			contains: []string{"<table>", "<th>Col A</th>", "<td>x</td>", "</table>"},
		},
		{
			name: "separator with colons",
			input: `| Left | Center | Right |
| :--- | :---: | ---: |
| a | b | c |`,
			contains: []string{"<table>", "<td>a</td>", "<td>c</td>"},
		},
		{
			name: "not a table (no separator row)",
			input: `This line has | pipes
but no separator row`,
			excludes: []string{"<table>"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := renderRedditMarkdown(tt.input)
			for _, s := range tt.contains {
				if !strings.Contains(result, s) {
					t.Errorf("result missing %q\ngot: %s", s, result)
				}
			}
			for _, s := range tt.excludes {
				if strings.Contains(result, s) {
					t.Errorf("result unexpectedly contains %q\ngot: %s", s, result)
				}
			}
		})
	}
}

func TestIsTableSeparator(t *testing.T) {
	valid := []string{"---", ":---", ":---:", "---:", "| --- | --- |", "| --- | --- | --- |"}
	invalid := []string{"", "hello", "abc", "--- not a separator", "| foo | bar |"}
	for _, s := range valid {
		if !isTableSeparator(s) {
			t.Errorf("isTableSeparator(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if isTableSeparator(s) {
			t.Errorf("isTableSeparator(%q) = true, want false", s)
		}
	}
}

// TestRenderRedditMarkdownEscapesUserContent is the regression test for markup
// injection through selftext: rendered HTML is inserted with v-html, so any
// user-derived fragment that reaches a placeholder unescaped becomes live
// markup. Placeholders are restored *after* the escaping pass, so each fragment
// must be escaped as it is stashed.
func TestRenderRedditMarkdownEscapesUserContent(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		excludes []string
	}{
		{
			name:     "link text cannot inject markup",
			input:    `[<script>alert(1)</script>](http://example.com)`,
			excludes: []string{"<script>", "</script>"},
		},
		{
			name:     "link URL cannot break out of the attribute",
			input:    `[click](http://example.com" onmouseover="alert(1))`,
			excludes: []string{`onmouseover="`, "onmouseover=\"", "<script>"},
		},
		{
			name:     "javascript URL is not linked",
			input:    `[click](javascript:alert(1))`,
			excludes: []string{"javascript:", "<a "},
		},
		{
			name:     "mixed-case javascript URL is not linked",
			input:    `[click](JaVaScRiPt:alert(1))`,
			excludes: []string{"avascript:", "<a "},
		},
		{
			name:     "data URL is not linked",
			input:    `[click](data:text/html,<script>alert(1)</script>)`,
			excludes: []string{"<a ", "href=\"data:", "<script>"},
		},
		{
			name:     "image alt cannot inject markup",
			input:    `![<script>alert(1)</script>](http://example.com/a.png)`,
			excludes: []string{"<script>", "</script>"},
		},
		{
			name:     "image src cannot break out of the attribute",
			input:    `![alt](http://example.com/a.png" onerror="alert(1)`,
			excludes: []string{`onerror="`, "onerror=\""},
		},
		{
			// Angle brackets must be escaped, so the payload renders as literal
			// text rather than becoming an <img> element. Asserting on the raw
			// tag is the meaningful check: an escaped "&lt;img ... onerror=...&gt;"
			// is inert.
			name:     "bold inner text cannot inject an element",
			input:    `**<img src=x onerror=alert(1)>**`,
			excludes: []string{"<img", "<strong><img"},
		},
		{
			name:     "italic inner text cannot inject an element",
			input:    `*<iframe src=javascript:alert(1)>*`,
			excludes: []string{"<iframe"},
		},
		{
			name: "table cells cannot inject markup",
			input: `| H1 | H2 |
| --- | --- |
| <script>alert(1)</script> | b |`,
			excludes: []string{"<script>", "</script>"},
		},
		{
			name:     "bare URL cannot break out of the attribute",
			input:    `see http://example.com/?a=1&b=2 for details`,
			excludes: []string{`href="http://example.com/?a=1&b=2"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderRedditMarkdown(tt.input)
			for _, s := range tt.excludes {
				if strings.Contains(got, s) {
					t.Errorf("rendered output contains %q — user content was not escaped\ninput: %s\ngot:   %s",
						s, tt.input, got)
				}
			}
		})
	}
}

// TestRenderRedditMarkdownKeepsLegitimateMarkup asserts the escaping does not
// over-correct: ordinary markdown still renders as HTML.
func TestRenderRedditMarkdownKeepsLegitimateMarkup(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string
	}{
		{
			name:     "http link",
			input:    `[click](http://example.com/a)`,
			contains: []string{`<a href="http://example.com/a"`, ">click</a>"},
		},
		{
			name:     "https link with query",
			input:    `[click](https://example.com/a?x=1&y=2)`,
			contains: []string{`<a href="https://example.com/a?x=1&amp;y=2"`},
		},
		{
			name:     "relative link",
			input:    `[click](/r/foo)`,
			contains: []string{`<a href="/r/foo"`},
		},
		{
			name:     "mailto link",
			input:    `[mail](mailto:a@b.com)`,
			contains: []string{`<a href="mailto:a@b.com"`},
		},
		{
			name:     "image",
			input:    `![alt text](https://i.redd.it/a.png)`,
			contains: []string{`<img src="https://i.redd.it/a.png"`, `alt="alt text"`},
		},
		{
			name:     "bold",
			input:    `**bold text**`,
			contains: []string{"<strong>bold text</strong>"},
		},
		{
			name:     "italic",
			input:    `*italic text*`,
			contains: []string{"<em>italic text</em>"},
		},
		{
			name: "table",
			input: `| A | B |
| --- | --- |
| 1 | 2 |`,
			contains: []string{"<table>", "<th>A</th>", "<td>1</td>"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderRedditMarkdown(tt.input)
			for _, s := range tt.contains {
				if !strings.Contains(got, s) {
					t.Errorf("rendered output missing %q\ninput: %s\ngot:   %s", s, tt.input, got)
				}
			}
		})
	}
}

// TestRedditQuotaSnapshotExposed asserts the metrics API surfaces Reddit's
// rate-limit headroom, so a client running out of budget is diagnosable from
// the API rather than only from the absence of new posts.
func TestRedditQuotaSnapshotExposed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/api/v1/access_token") {
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
			return
		}
		// Reddit formats these as floats, e.g. "999.0" (verified against
		// the live API) — an integer here would not exercise the parser.
		w.Header().Set("X-Ratelimit-Used", "120.0")
		w.Header().Set("X-Ratelimit-Remaining", "480.0")
		w.Header().Set("X-Ratelimit-Reset", "300")
		_, _ = w.Write([]byte(`{"data":{"children":[]}}`))
	}))
	t.Cleanup(srv.Close)

	rf := NewRedditFetcher()
	rf.baseURL = srv.URL
	rf.apiBaseURL = srv.URL
	rf.SetCredentials("client-id", "client-secret")
	rf.SetRateLimiter(nil)

	poller := NewPoller(nil)
	poller.fetchers["reddit"] = rf

	// A freshly constructed client has observed nothing yet, so it must not
	// look exhausted.
	if snap := quotaFor(t, poller, "reddit"); snap.Measured {
		t.Error("Measured = true before any response was seen")
	}

	if _, err := rf.Fetch(t.Context(), "https://www.reddit.com/r/golang"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	snap := quotaFor(t, poller, "reddit")
	if !snap.Measured {
		t.Fatal("Measured = false after a response carrying X-Ratelimit-* headers")
	}
	// Reddit never advertises an absolute limit, so it is derived from
	// used + remaining (120 + 480 for a 600-request OAuth window).
	if snap.Limit != 600 || snap.Remaining != 480 {
		t.Errorf("Limit/Remaining = %d/%d, want 600/480", snap.Limit, snap.Remaining)
	}
	if snap.Reserve != redditQuotaReserve {
		t.Errorf("Reserve = %d, want %d", snap.Reserve, redditQuotaReserve)
	}
	if snap.BudgetLow {
		t.Error("BudgetLow = true with 480 requests remaining")
	}
	if snap.Reset == nil {
		t.Fatal("Reset is nil; the API consumer needs the window reset time")
	}
	if snap.CheckedAt == nil {
		t.Error("CheckedAt is nil after a measurement")
	}
}

// TestQuotaHeaderInt covers the header formats Reddit actually emits. The
// float form ("999.0") is the one seen in production; an Atoi-only parser
// rejected it and left the quota unmeasured forever.
func TestQuotaHeaderInt(t *testing.T) {
	cases := []struct {
		raw   string
		want  int
		valid bool
	}{
		{"999.0", 999, true},
		{"480.0", 480, true},
		{"120", 120, true},
		{" 999.0 ", 999, true},
		{"0.0", 0, true},
		{"", 0, false},
		{"n/a", 0, false},
	}
	for _, c := range cases {
		got, ok := quotaHeaderInt(c.raw)
		if ok != c.valid || got != c.want {
			t.Errorf("quotaHeaderInt(%q) = (%d, %v), want (%d, %v)", c.raw, got, ok, c.want, c.valid)
		}
	}
}

// TestRedditQuotaMeasuredWithFloatHeaders pins the production symptom: with
// the header values Reddit really sends, the snapshot must be measured.
func TestRedditQuotaMeasuredWithFloatHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/api/v1/access_token") {
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
			return
		}
		w.Header().Set("X-Ratelimit-Used", "1")
		w.Header().Set("X-Ratelimit-Remaining", "999.0")
		w.Header().Set("X-Ratelimit-Reset", "352")
		_, _ = w.Write([]byte(`{"data":{"children":[]}}`))
	}))
	t.Cleanup(srv.Close)

	rf := NewRedditFetcher()
	rf.baseURL = srv.URL
	rf.apiBaseURL = srv.URL
	rf.SetCredentials("client-id", "client-secret")
	rf.SetRateLimiter(nil)

	if _, err := rf.Fetch(t.Context(), "https://www.reddit.com/r/golang"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	snap := rf.QuotaSnapshot(redditQuotaReserve)
	if !snap.Measured {
		t.Fatal("Measured = false with Reddit's real float header format")
	}
	if snap.Remaining != 999 || snap.Limit != 1000 {
		t.Errorf("Limit/Remaining = %d/%d, want 1000/999", snap.Limit, snap.Remaining)
	}
}
