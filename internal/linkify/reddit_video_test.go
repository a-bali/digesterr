package linkify

import (
	"strings"
	"testing"
)

func TestUpgradeStoredRedditVideo(t *testing.T) {
	prefix := `<!doctype html><p CLASS='odd'>A &amp; B</p><!-- keep -->`
	legacy := `<video src="https://v.redd.it/2mu8k4a613nh1/DASH_1080.mp4?source=fallback" controls><a href="https://v.redd.it/2mu8k4a613nh1">Watch</a></video>`
	got := UpgradeRedditVideos(prefix + legacy + `<p>end</p>`)
	if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, `<p>end</p>`) {
		t.Fatal("changed unrelated bytes", got)
	}
	if strings.Index(got, "HLSPlaylist.m3u8") > strings.Index(got, "DASH_1080.mp4") {
		t.Fatal("HLS not first", got)
	}
	if !strings.Contains(got, `</video><a href="https://v.redd.it/2mu8k4a613nh1/HLSPlaylist.m3u8"`) {
		t.Fatal("watch link hidden", got)
	}
	if UpgradeRedditVideos(got) != got {
		t.Fatal("upgrade is not idempotent")
	}
}

func TestUpgradeRedditVideoControls(t *testing.T) {
	for _, input := range []string{
		`<video src="https://video.twimg.com/a.mp4"></video>`,
		`<video src="https://v.redd.it.evil.test/id/CMAF_720.mp4"></video>`,
		`<video src="https://v.redd.it@evil.test/id/CMAF_720.mp4"></video>`,
		`<video src="https://v.redd.it/id%2fother/CMAF_720.mp4"></video>`,
		`<video loop src="https://v.redd.it/id/CMAF_720.mp4"></video>`,
		`<video data-reddit-gif src="https://v.redd.it/id/CMAF_720.mp4"></video>`,
		`<video><source src="https://v.redd.it/id/CMAF_720.mp4"><source src="https://example.com/a.mp4"></video>`,
		`<video><source src="https://v.redd.it/id/CMAF_720.mp4"><source src="https://v.redd.it/other/CMAF_480.mp4"></video>`,
		`<video src="https://v.redd.it/id/CMAF_720.mp4">unfinished`,
		`<script>const s = '<video src="https://v.redd.it/id/CMAF_720.mp4"></video>'</script>`,
	} {
		if got := UpgradeRedditVideos(input); got != input {
			t.Errorf("control changed: %s -> %s", input, got)
		}
	}
}

func TestRedditVideoMediaIDValidation(t *testing.T) {
	for _, raw := range []string{"https://v.redd.it/id/CMAF_720.mp4?source=fallback", "https://v.redd.it/id/HLSPlaylist.m3u8", "https://v.redd.it/id"} {
		if id, ok := RedditVideoMediaID(raw); !ok || id != "id" {
			t.Errorf("rejected valid asset %s", raw)
		}
	}
	for _, raw := range []string{"https://v.redd.it:443/id/CMAF_720.mp4", "http://v.redd.it/id/CMAF_720.mp4", "https://evil.test/v.redd.it/id/CMAF_720.mp4", "https://v.redd.it/id/CMAF_AUDIO_128.mp4", "https://v.redd.it/id/CMAF_720.m3u8", "https://v.redd.it/id/CMAF_720.mp4#x"} {
		if _, ok := RedditVideoMediaID(raw); ok {
			t.Errorf("accepted unsafe/unrelated asset %s", raw)
		}
	}
	if CommentVideoHTML(`id" onerror="alert(1)`) != "" {
		t.Fatal("unvalidated ID rendered")
	}
}
