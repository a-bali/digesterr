package feed

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderGalleryContent(t *testing.T) {
	tests := []struct {
		name     string
		post     redditPostData
		want     string
		wantImgs int
		wantVid  int
	}{
		{
			name: "gallery with ordered images",
			post: redditPostData{
				IsGallery: true,
				GalleryData: struct {
					Items []redditGalleryItem `json:"items"`
				}{
					Items: []redditGalleryItem{
						{MediaID: "img1"},
						{MediaID: "img2"},
					},
				},
				MediaMetadata: map[string]redditMediaItem{
					"img1": {
						M: "image/jpeg",
						S: redditMediaSource{URL: "https://preview.redd.it/photo1.jpg?width=640"},
					},
					"img2": {
						M: "image/png",
						S: redditMediaSource{URL: "https://i.redd.it/photo2.png"},
					},
				},
			},
			wantImgs: 2,
			want:     "https://preview.redd.it/photo1.jpg",
		},
		{
			name: "gallery with HTML-escaped URLs",
			post: redditPostData{
				IsGallery: true,
				GalleryData: struct {
					Items []redditGalleryItem `json:"items"`
				}{
					Items: []redditGalleryItem{
						{MediaID: "vid1"},
					},
				},
				MediaMetadata: map[string]redditMediaItem{
					"vid1": {
						M: "image/jpeg",
						S: redditMediaSource{URL: "https://preview.redd.it/photo&amp;width=640"},
					},
				},
			},
			wantImgs: 1,
			// The & is attribute-escaped to &amp; in the src (correct: raw &
			// would parse as an entity start in HTML). The URL itself is
			// decoded from &amp; to & before escaping.
			want: "https://preview.redd.it/photo&amp;width=640",
		},
		{
			name: "empty gallery data returns empty",
			post: redditPostData{
				IsGallery:     true,
				GalleryData:   struct{ Items []redditGalleryItem `json:"items"` }{},
				MediaMetadata: map[string]redditMediaItem{},
			},
			want: "",
		},
		{
			name: "gallery with missing media_id returns partial",
			post: redditPostData{
				IsGallery: true,
				GalleryData: struct {
					Items []redditGalleryItem `json:"items"`
				}{
					Items: []redditGalleryItem{
						{MediaID: "exists"},
						{MediaID: "missing"},
					},
				},
				MediaMetadata: map[string]redditMediaItem{
					"exists": {
						M: "image/jpeg",
						S: redditMediaSource{URL: "https://i.redd.it/exists.jpg"},
					},
				},
			},
			wantImgs: 1,
			want:     "https://i.redd.it/exists.jpg",
		},
		{
			name: "gallery with animated GIF uses MP4 video",
			post: redditPostData{
				IsGallery: true,
				GalleryData: struct {
					Items []redditGalleryItem `json:"items"`
				}{
					Items: []redditGalleryItem{
						{MediaID: "gif1"},
					},
				},
				MediaMetadata: map[string]redditMediaItem{
					"gif1": {
						E: "AnimatedImage",
						M: "image/gif",
						S: redditMediaSource{
							GIF: "https://i.redd.it/v9bt92qrbknh1.gif",
							MP4: "https://preview.redd.it/v9bt92qrbknh1.gif?format=mp4&amp;s=abc123",
						},
					},
				},
			},
			wantVid: 1,
			want:    "https://preview.redd.it/v9bt92qrbknh1.gif?format=mp4&amp;s=abc123",
		},
		{
			name: "gallery with animated GIF without MP4 uses GIF URL",
			post: redditPostData{
				IsGallery: true,
				GalleryData: struct {
					Items []redditGalleryItem `json:"items"`
				}{
					Items: []redditGalleryItem{
						{MediaID: "gif2"},
					},
				},
				MediaMetadata: map[string]redditMediaItem{
					"gif2": {
						E: "AnimatedImage",
						M: "image/gif",
						S: redditMediaSource{
							GIF: "https://i.redd.it/animation.gif",
						},
					},
				},
			},
			wantImgs: 1,
			want:     "https://i.redd.it/animation.gif",
		},
		{
			name: "mixed gallery with static and animated items",
			post: redditPostData{
				IsGallery: true,
				GalleryData: struct {
					Items []redditGalleryItem `json:"items"`
				}{
					Items: []redditGalleryItem{
						{MediaID: "static1"},
						{MediaID: "anim1"},
						{MediaID: "static2"},
					},
				},
				MediaMetadata: map[string]redditMediaItem{
					"static1": {
						M: "image/jpeg",
						S: redditMediaSource{URL: "https://i.redd.it/photo1.jpg"},
					},
					"anim1": {
						E: "AnimatedImage",
						M: "image/gif",
						S: redditMediaSource{
							GIF: "https://i.redd.it/anim.gif",
							MP4: "https://preview.redd.it/anim.gif?format=mp4&amp;s=xyz",
						},
					},
					"static2": {
						M: "image/png",
						S: redditMediaSource{URL: "https://i.redd.it/photo2.png"},
					},
				},
			},
			wantImgs: 2,
			wantVid:  1,
			want:     "https://i.redd.it/photo1.jpg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderGalleryContent(tt.post)
			if tt.want == "" {
				if got != "" {
					t.Errorf("renderGalleryContent() = %q, want empty", got)
				}
				return
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("renderGalleryContent() = %q, want to contain %q", got, tt.want)
			}
			imgCount := strings.Count(got, "<img src=")
			if imgCount != tt.wantImgs {
				t.Errorf("got %d images, want %d (content: %s)", imgCount, tt.wantImgs, got)
			}
			vidCount := strings.Count(got, "<video src=")
			if vidCount != tt.wantVid {
				t.Errorf("got %d videos, want %d (content: %s)", vidCount, tt.wantVid, got)
			}
		})
	}
}

func TestRenderGalleryContentIntegration(t *testing.T) {
	// Simulate a real gallery post like the one from r/hungary
	post := redditPostData{
		ID:        "1w52m1n",
		Title:     "Tiborcz és Ráhel Londonban",
		IsSelf:    false,
		IsGallery: true,
		URL:       "https://www.reddit.com/gallery/1w52m1n",
		GalleryData: struct {
			Items []redditGalleryItem `json:"items"`
		}{
			Items: []redditGalleryItem{
				{MediaID: "a1b2c3"},
				{MediaID: "d4e5f6"},
			},
		},
		MediaMetadata: map[string]redditMediaItem{
			"a1b2c3": {
				M: "image/jpeg",
				S: redditMediaSource{URL: "https://preview.redd.it/tiborcz-photo1.jpeg?width=720&amp;format=pjpg&amp;auto=webp"},
			},
			"d4e5f6": {
				M: "image/jpeg",
				S: redditMediaSource{URL: "https://preview.redd.it/tiborcz-photo2.jpeg?width=720&amp;format=pjpg&amp;auto=webp"},
			},
		},
	}

	content := renderGalleryContent(post)
	if !strings.Contains(content, "<img src=") {
		t.Error("expected img tags in gallery content")
	}
	if strings.Count(content, "<img src=") != 2 {
		t.Errorf("expected 2 images, got content: %s", content)
	}
	// The payload's &amp; entities are decoded to & and then re-escaped for
	// the attribute context: the src must carry &amp; (a raw & would parse as
	// an entity start in HTML).
	if !strings.Contains(content, "&amp;") {
		t.Error("expected attribute-escaped &amp; in image URLs")
	}
	if strings.Contains(content, "format=pjpg&auto") {
		t.Error("raw & in attribute value: must be escaped as &amp;")
	}
	if !strings.Contains(content, "format=pjpg") {
		t.Error("expected URL to contain format=pjpg")
	}
}

// TestGalleryAnimatedImageDeserialization validates that the real Reddit API
// JSON structure for animated images deserializes correctly. The key insight
// is that animated images use s.gif and s.mp4 instead of s.u.
func TestGalleryAnimatedImageDeserialization(t *testing.T) {
	// Real JSON from Reddit API for a gallery with animated images
	apiJSON := `{
		"is_gallery": true,
		"gallery_data": {
			"items": [
				{"media_id": "v9bt92qrbknh1", "id": 1036504855},
				{"media_id": "4s1c12qrbknh1", "id": 1036504856}
			]
		},
		"media_metadata": {
			"v9bt92qrbknh1": {
				"status": "valid",
				"e": "AnimatedImage",
				"m": "image/gif",
				"s": {
					"y": 1204,
					"gif": "https://i.redd.it/v9bt92qrbknh1.gif",
					"mp4": "https://preview.redd.it/v9bt92qrbknh1.gif?format=mp4&amp;s=d36be46d7c9b37a66d34b200f08c5078b513d4d7",
					"x": 540
				}
			},
			"4s1c12qrbknh1": {
				"status": "valid",
				"e": "AnimatedImage",
				"m": "image/gif",
				"s": {
					"y": 1028,
					"gif": "https://i.redd.it/4s1c12qrbknh1.gif",
					"mp4": "https://preview.redd.it/4s1c12qrbknh1.gif?format=mp4&amp;s=5d33ebfa90aa8174f100cde7faa205242a1cccaa",
					"x": 921
				}
			}
		}
	}`

	type rawPost struct {
		IsGallery    bool                              `json:"is_gallery"`
		GalleryData  struct {
			Items []redditGalleryItem `json:"items"`
		} `json:"gallery_data"`
		MediaMetadata map[string]redditMediaItem `json:"media_metadata"`
	}

	var raw rawPost
	if err := json.Unmarshal([]byte(apiJSON), &raw); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	post := redditPostData{
		IsGallery:    raw.IsGallery,
		GalleryData:  raw.GalleryData,
		MediaMetadata: raw.MediaMetadata,
	}

	content := renderGalleryContent(post)
	if content == "" {
		t.Fatal("renderGalleryContent returned empty for animated gallery")
	}

	// Should produce 2 <video> tags (MP4 preferred for AnimatedImage)
	vidCount := strings.Count(content, "<video src=")
	if vidCount != 2 {
		t.Errorf("expected 2 video tags, got %d; content: %s", vidCount, content)
	}

	// The payload's &amp; entities are decoded to & and then re-escaped for
	// the attribute context: a raw & in the src would parse as an entity
	// start in HTML, so the escaped form is the correct output.
	if !strings.Contains(content, "&amp;") {
		t.Errorf("expected attribute-escaped &amp; in video URLs: %s", content)
	}

	// Verify MP4 URLs are used (not GIF)
	if !strings.Contains(content, "format=mp4") {
		t.Errorf("expected MP4 URLs in video tags: %s", content)
	}
}
