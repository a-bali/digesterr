package feed

import (
	"strings"
	"testing"
)

func TestRenderPostVideoAudioAndGIFControl(t *testing.T) {
	video := redditPostData{URL: "https://v.redd.it/id", Media: redditMedia{RedditVideo: &redditVideo{FallbackURL: "https://v.redd.it/id/CMAF_1080.mp4?source=fallback"}}}
	for _, post := range []redditPostData{video, {URL: video.URL, CrosspostParentList: []redditPostData{video}}} {
		body := renderMediaOrParent(post, post.URL)
		if !strings.Contains(body, "HLSPlaylist.m3u8") || strings.Index(body, "HLSPlaylist.m3u8") > strings.Index(body, "CMAF_720.mp4") {
			t.Fatal("new post/crosspost not HLS first", body)
		}
		if !strings.Contains(body, `src="https://v.redd.it/id/CMAF_1080.mp4?source=fallback"`) {
			t.Fatal("API fallback lost", body)
		}
		if !strings.Contains(body, `</video><a href=`) {
			t.Fatal("watch link hidden", body)
		}
	}
	video.Media.RedditVideo.IsGif = true
	for _, post := range []redditPostData{video, {URL: video.URL, CrosspostParentList: []redditPostData{video}}} {
		body := renderMediaOrParent(post, post.URL)
		if strings.Contains(body, "HLSPlaylist") || !strings.Contains(body, "data-reddit-gif") {
			t.Fatal("GIF incorrectly upgraded", body)
		}
	}
	for _, raw := range []string{"https://evil.test/v.redd.it/id", "https://v.redd.it.evil.test/id", "https://v.redd.it@evil.test/id"} {
		if isRedditVideoURL(raw) {
			t.Error("misclassified external host", raw)
		}
	}
}

func TestRenderRedditVideoRejectsUnsafeFallbacks(t *testing.T) {
	for _, fallback := range []string{"https://evil.test/id/CMAF_720.mp4", "https://v.redd.it/id", "https://v.redd.it/id?x=.mp4", "https://v.redd.it/id/HLSPlaylist.m3u8", `https://v.redd.it/id/CMAF_720.mp4" onerror="alert(1)`} {
		post := redditPostData{URL: "https://v.redd.it/id", Media: redditMedia{RedditVideo: &redditVideo{FallbackURL: fallback}}}
		if body := renderPostMedia(post, post.URL); strings.Contains(body, "<video") {
			t.Errorf("unsafe or non-MP4 API fallback emitted: %s", body)
		}
	}
}
