package linkify

import (
	"strings"
	"testing"
)

// twitterVideoBody is the exact shape twitter_fetcher.mediaHTML emits.
const twitterVideoBody = `<p>hello</p><p><video controls preload="metadata" poster="https://pbs.twimg.com/amplify_video_thumb/1/img/x.jpg"><source src="https://video.twimg.com/amplify_video/1/vid/avc1/1280x720/a.mp4?tag=16" type="video/mp4" /></video></p>`

func TestTwitterVideoReferrerFixAddsMeta(t *testing.T) {
	got := TwitterVideoReferrerFix(twitterVideoBody)
	if !strings.HasPrefix(got, `<meta name="referrer" content="no-referrer">`) {
		t.Fatalf("no-referrer meta not prepended: %q", got)
	}
	if !strings.HasSuffix(got, twitterVideoBody) {
		t.Fatalf("body was rewritten, not prefixed: %q", got)
	}
}

// The meta must be honoured when the body is rendered as a fragment: Reader
// clients insert it into their own template, so there is no head to rely on.
// Browser verification of that is in the web index.html document tag; here we
// pin only that the fragment carries the tag itself, first, so it is processed
// before the parser reaches the player.
func TestTwitterVideoReferrerFixMetaPrecedesPlayer(t *testing.T) {
	got := TwitterVideoReferrerFix(twitterVideoBody)
	if strings.Index(got, `name="referrer"`) > strings.Index(got, "<video") {
		t.Fatalf("meta must precede the player: %q", got)
	}
}

func TestTwitterVideoReferrerFixScope(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"source child", twitterVideoBody, true},
		{"video src", `<video src="https://video.twimg.com/a/b.mp4"></video>`, true},
		{"no player", `<p>nothing here</p>`, false},
		{"photo only", `<p><img src="https://pbs.twimg.com/media/a.jpg" /></p>`, false},
		// A body that merely mentions the host is not a player; the policy
		// belongs on bodies that actually load one.
		{"mention only", `<p>see https://video.twimg.com/amplify_video/1/vid/a.mp4</p>`, false},
		{"nested in link", `<p><a href="https://video.twimg.com/x.mp4">x</a></p>`, false},
		{"other host", `<video src="https://v.redd.it/abc/CMAF_720.mp4"></video>`, false},
		{"http scheme", `<video src="http://video.twimg.com/a.mp4"></video>`, false},
		{"lookalike host", `<video src="https://video.twimg.com.evil.test/a.mp4"></video>`, false},
		{"subdomain", `<video src="https://cdn.video.twimg.com/a.mp4"></video>`, false},
		{"upper case host", `<video src="https://VIDEO.TWIMG.COM/a.mp4"></video>`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TwitterVideoReferrerFix(tc.body)
			has := strings.Contains(got, `name="referrer"`)
			if has != tc.want {
				t.Fatalf("meta present=%v, want %v: %q", has, tc.want, got)
			}
			if !tc.want && got != tc.body {
				t.Fatalf("ineligible body must be returned unchanged: %q", got)
			}
		})
	}
}

// A source pointing at the host without a video wrapper must not be adopted: the
// scope is a real player, and a stray <source> is not one.
func TestTwitterVideoReferrerFixSourceOutsideVideo(t *testing.T) {
	body := `<picture><source src="https://video.twimg.com/a/b.mp4" /></picture>`
	if got := TwitterVideoReferrerFix(body); got != body {
		t.Fatalf("source outside video adopted: %q", got)
	}
}

// A video holding both an eligible and an unrelated source still gets the fix:
// the CDN fetches whatever the engine selects, so the element is what matters.
func TestTwitterVideoReferrerFixMultipleSources(t *testing.T) {
	body := `<video controls><source src="https://v.redd.it/abc/CMAF_720.mp4" type="video/mp4" /><source src="https://video.twimg.com/a/b.mp4" type="video/mp4" /></video>`
	if got := TwitterVideoReferrerFix(body); !strings.Contains(got, `name="referrer"`) {
		t.Fatalf("mixed-source video missed: %q", got)
	}
	// The control case the Reddit upgrade test relies on must stay untouched:
	// a reddit-only player is handled by the HLS upgrade, not by this rule.
	redditOnly := `<video src="https://v.redd.it/2mu8k4a613nh1/CMAF_720.mp4"></video>`
	if got := TwitterVideoReferrerFix(redditOnly); got != redditOnly {
		t.Fatalf("reddit-only player must be untouched: %q", got)
	}
}

// Calling the rule twice must not stack metas: the fix runs at render time and
// more than one render path can reach the same body.
func TestTwitterVideoReferrerFixIdempotent(t *testing.T) {
	once := TwitterVideoReferrerFix(twitterVideoBody)
	twice := TwitterVideoReferrerFix(once)
	if once != twice {
		t.Fatalf("second pass changed the body:\n%q\n%q", once, twice)
	}
	if strings.Count(twice, `name="referrer"`) != 1 {
		t.Fatalf("meta stacked: %q", twice)
	}
}

// The rule must survive the Reddit upgrade pass that already runs on this body,
// in either order, since both rewrite the same call site.
func TestTwitterVideoReferrerFixComposesWithRedditUpgrade(t *testing.T) {
	mixed := `<p>x</p>` + twitterVideoBody + `<video src="https://v.redd.it/2mu8k4a613nh1/CMAF_720.mp4?source=fallback" controls></video>`
	got := TwitterVideoReferrerFix(UpgradeRedditVideos(mixed))
	if !strings.Contains(got, `name="referrer"`) {
		t.Fatalf("twitter fix lost after reddit upgrade: %q", got)
	}
	if !strings.Contains(got, "HLSPlaylist.m3u8") {
		t.Fatalf("reddit upgrade lost the HLS master: %q", got)
	}
}
