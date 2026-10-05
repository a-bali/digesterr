package linkify

import (
	"strings"
	"testing"
)

// The reported defect (CLAIM): a comment whose body is an app-only Reddit
// comment-video deep link rendered as a link to
//
//	https://reddit.com/link/<cid>/video/<mid>/player
//
// which Reddit redirects to "r/subreddit is banned" for an anonymous visitor.
// The media id in that URL reaches the video directly, so the renderer should
// emit an inline player instead.
//
// Real input: r/opencode post 1wvyg3k, comment pdge9kv, media id
// kprqyu3cd3th1 (author 2frames_app).

const commentVideoURL = "https://reddit.com/link/pdge9kv/video/kprqyu3cd3th1/player"

func TestTextRendersCommentVideoInline(t *testing.T) {
	got := Text(commentVideoURL)

	for _, want := range []string{
		"<video",
		"https://v.redd.it/kprqyu3cd3th1/CMAF_720.mp4",
		"https://v.redd.it/kprqyu3cd3th1/CMAF_480.mp4",
		"https://v.redd.it/kprqyu3cd3th1/HLSPlaylist.m3u8",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	// The defect itself: no link back to the banned page.
	if strings.Contains(got, `href="https://reddit.com/link/`) {
		t.Errorf("still links the app-only URL, which is the reported bug:\n%s", got)
	}
	// CMAF first, HLS last: a browser takes the first source it understands, so
	// putting the manifest first leaves Chrome/Firefox with an empty player.
	if strings.Index(got, "CMAF_720.mp4") > strings.Index(got, "HLSPlaylist.m3u8") {
		t.Errorf("HLS source must come after the progressive sources:\n%s", got)
	}
	// DASH_*.mp4 is 403 for every id tested; a DASH source silently drops video.
	if strings.Contains(got, "DASH") {
		t.Errorf("DASH source is a 403 on this CDN:\n%s", got)
	}
	// A reader whose browser plays none of the sources still needs a link.
	if !strings.Contains(got, "Watch video") {
		t.Errorf("no fallback anchor for a client that plays none of the sources:\n%s", got)
	}
}

// Control: ordinary URLs must keep rendering as links — the new rule must not
// claim them.
func TestTextKeepsOrdinaryURLAsLink(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"plain https", "https://example.com/a"},
		{"www without scheme", "www.example.com/a"},
		{"reddit post permalink", "https://www.reddit.com/r/opencode/comments/1wvyg3k/x/"},
		{"reddit comment without media", "https://reddit.com/link/pdge9kv/comments"},
		{"reddit link missing the player suffix", "https://reddit.com/link/pdge9kv/video/kprqyu3cd3th1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Text(tt.input)
			if strings.Contains(got, "<video") {
				t.Errorf("rendered a player for a non-video URL:\n%s", got)
			}
			if !strings.Contains(got, "<a href=") {
				t.Errorf("stopped linkifying an ordinary URL:\n%s", got)
			}
		})
	}
}

// The rule matches a shape, not an author: prose around the link, a bare
// host, and a trailing slash are all accepted forms.
func TestTextCommentVideoShapeVariants(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"bare host as authors paste it", "reddit.com/link/pdge9kv/video/kprqyu3cd3th1/player"},
		{"prose around the link", "made this horse: https://reddit.com/link/pdge9kv/video/kprqyu3cd3th1/player enjoy"},
		{"trailing slash", "https://reddit.com/link/pdge9kv/video/kprqyu3cd3th1/player/"},
		{"www host", "https://www.reddit.com/link/pdge9kv/video/kprqyu3cd3th1/player"},
		{"old host", "https://old.reddit.com/link/pdge9kv/video/kprqyu3cd3th1/player"},
		{"m host", "https://m.reddit.com/link/pdge9kv/video/kprqyu3cd3th1/player"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Text(tt.input)
			if !strings.Contains(got, "<video") {
				t.Errorf("expected an inline player:\n%s", got)
			}
		})
	}
}

// A match starting mid-word is one word, not a URL — same rule replaceBareURLs
// applies.
func TestTextDoesNotRenderCommentVideoMidWord(t *testing.T) {
	got := Text("xhttps://reddit.com/link/pdge9kv/video/kprqyu3cd3th1/player")
	if strings.Contains(got, "<video") {
		t.Errorf("rendered a player for a mid-word match:\n%s", got)
	}
}

// Every URL in the generated markup is rebuilt from the media id through a
// fixed template, so a crafted id cannot steer a src at another host. The id
// class is the only thing taken from user text.
func TestCommentVideoMediaIDIsTheOnlyUntrustedInput(t *testing.T) {
	// The anchored predicate must reject anything that is not the whole shape.
	rejects := []string{
		"",
		"https://reddit.com/link/pdge9kv/video/kprqyu3cd3th1/player?x=1",
		"https://evil.com/link/a/video/b/player",
		"https://reddit.com/link/pdge9kv/video/b/player?x=https://evil.com",
		"see https://reddit.com/link/pdge9kv/video/kprqyu3cd3th1/player",
	}
	for _, raw := range rejects {
		if id, ok := CommentVideoMediaID(raw); ok {
			t.Errorf("CommentVideoMediaID(%q) accepted, id=%q", raw, id)
		}
	}

	if id, ok := CommentVideoMediaID(commentVideoURL); !ok || id != "kprqyu3cd3th1" {
		t.Errorf("CommentVideoMediaID(%q) = %q, %v; want kprqyu3cd3th1, true", commentVideoURL, id, ok)
	}
}

// The media id is echoed into markup, so it must never be able to carry a
// quote or a scheme break. `[A-Za-z0-9]+` is the whole surface.
func TestCommentVideoHTMLBuildsFromFixedTemplate(t *testing.T) {
	html := CommentVideoHTML("abc123XYZ")
	for _, want := range []string{
		`<video controls`,
		`https://v.redd.it/abc123XYZ/CMAF_720.mp4`,
		`https://v.redd.it/abc123XYZ/HLSPlaylist.m3u8`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in %s", want, html)
		}
	}
	if strings.Contains(html, "<script") || strings.Contains(html, `src="http://evil`) {
		t.Errorf("generated markup carries an injected attribute: %s", html)
	}
}

// The master manifest is the only asset carrying both picture and sound, so
// that is what a "watch the whole thing" URL must point at.
func TestCommentVideoMediaURLIsTheHLSMaster(t *testing.T) {
	got := CommentVideoMediaURL("kprqyu3cd3th1")
	want := "https://v.redd.it/kprqyu3cd3th1/HLSPlaylist.m3u8"
	if got != want {
		t.Errorf("CommentVideoMediaURL = %q, want %q", got, want)
	}
}

// The player must survive the block split: a link alone in a line is a
// paragraph, and a link under a quote marker is inside a blockquote.
func TestTextRendersCommentVideoInsideBlocks(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string
	}{
		{
			name:     "own paragraph",
			input:    commentVideoURL,
			contains: []string{"<video"},
		},
		{
			name:     "under a blockquote",
			input:    "&gt; " + commentVideoURL,
			contains: []string{"<blockquote>", "<video"},
		},
		{
			name:     "bullet list item",
			input:    "- " + commentVideoURL,
			contains: []string{"<li>", "<video"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Text(tt.input)
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in:\n%s", want, got)
				}
			}
		})
	}
}

// A comment-video URL inside an already-generated href must not be claimed a
// second time — that would nest a player inside an anchor.
func TestTextDoesNotClaimCommentVideoInsideAttribute(t *testing.T) {
	got := Text("[x](https://reddit.com/link/pdge9kv/video/kprqyu3cd3th1/player)")
	links := strings.Count(got, "<a href=")
	if links != 1 {
		t.Errorf("want exactly one anchor, got %d:\n%s", links, got)
	}
	if strings.Contains(got, "<video") {
		t.Errorf("nested a player inside an anchor:\n%s", got)
	}
}

// Raw HTML in a comment body reaches the renderer before the escape pass, so
// the URL sitting in an href is still literal text when the comment-video rule
// runs. It must be left alone rather than claimed into a nested player. The
// raw markup itself is escaped later (step 8), which is separate behaviour —
// what matters here is that the rule did not fire on an attribute value.
func TestTextDoesNotClaimCommentVideoInsideRawHTMLAttribute(t *testing.T) {
	got := Text(`<a href="https://reddit.com/link/pdge9kv/video/kprqyu3cd3th1/player">clip</a>`)
	if strings.Contains(got, "<video") {
		t.Errorf("claimed a URL that is already an href value:\n%s", got)
	}
	if !strings.Contains(got, "clip") {
		t.Errorf("lost the link text:\n%s", got)
	}
}
