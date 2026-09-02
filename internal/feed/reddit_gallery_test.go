package feed

import (
	"strings"
	"testing"
)

func TestRenderGalleryContent(t *testing.T) {
	tests := []struct {
		name     string
		post     redditPostData
		want     string
		wantImgs int
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
			want:     "https://preview.redd.it/photo&width=640",
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
				t.Errorf("got %d images, want %d", imgCount, tt.wantImgs)
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
	// Verify HTML entities are unescaped
	if strings.Contains(content, "&amp;") {
		t.Error("HTML entities should be unescaped in image URLs")
	}
	if !strings.Contains(content, "format=pjpg") {
		t.Error("expected unescaped URL to contain format=pjpg")
	}
}
