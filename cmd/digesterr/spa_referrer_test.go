package main

import (
	"os"
	"strings"
	"testing"
)

// The web UI loads twitter video players in the SPA document itself, and its own
// origin (the digesterr host) is exactly the Referer that video.twimg.com answers
// 403 to. The document-level policy in web/index.html is what drops it, so its
// presence is what keeps playback working there.
//
// This reads the source index.html rather than the built bundle: the tag has to
// live in the Vite entry document to be in the served page at all, and asserting
// it at the source catches its removal before a build is attempted.
func TestSPAIndexDropsReferrer(t *testing.T) {
	raw, err := os.ReadFile("../../web/index.html")
	if err != nil {
		t.Fatalf("reading web/index.html: %v", err)
	}
	html := string(raw)

	meta := `<meta name="referrer" content="no-referrer"`
	if !strings.Contains(html, meta) {
		t.Fatalf("web/index.html has no no-referrer policy, so cross-origin media "+
			"loads carry the digesterr origin and video.twimg.com answers 403:\n%s", html)
	}
	// A policy is only useful if it is parsed before the app's document body; the
	// tag belongs in <head>.
	headEnd := strings.Index(html, "</head>")
	if headEnd == -1 || strings.Index(html, meta) > headEnd {
		t.Fatalf("no-referrer policy must sit in <head>:\n%s", html)
	}
}
