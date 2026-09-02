package feed

import (
	"testing"
)

func TestIsRedditHostedURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.reddit.com/gallery/1w52m1n", true},
		{"https://www.reddit.com/r/hungary/comments/abc123/title/", true},
		{"https://reddit.com/r/golang", true},
		{"https://old.reddit.com/r/programming", true},
		{"https://i.redd.it/photo.jpg", true},
		{"https://preview.redd.it/photo.jpg", true},
		{"https://example.com/article", false},
		{"https://news.ycombinator.com/item?id=123", false},
		{"https://github.com/user/repo", false},
		{"not-a-url", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := isRedditHostedURL(tt.url)
			if got != tt.want {
				t.Errorf("isRedditHostedURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}
