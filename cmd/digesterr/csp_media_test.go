package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// TestSPAHandlerCSPAllowsRemoteMedia pins the prod failure of 2026-10-01: the
// reader renders Reddit/Twitter videos inline as <video src="https://v.redd.it/…">
// and <video><source src="https://video.twimg.com/…">, but the CSP had no
// media-src, so it fell back to default-src 'self' and the browser refused to
// load the file:
//
//	Loading media from 'https://v.redd.it/…/CMAF_1080.mp4?source=fallback'
//	violates the following Content Security Policy directive: "default-src 'self'".
//	Note that 'media-src' was not explicitly set, so 'default-src' is used as a
//	fallback. The action has been blocked.
//
// The video element then stalled at readyState 0 with no visible error. img-src
// already allows any https host for the same class of feed media, so media-src
// must match it.
func TestSPAHandlerCSPAllowsRemoteMedia(t *testing.T) {
	h := spaHandler(http.NotFoundHandler(), http.FS(fstest.MapFS{
		"index.html": {Data: []byte("<html></html>")},
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/feeds/15/779648", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("no Content-Security-Policy header on the SPA response")
	}
	if !hasDirective(csp, "media-src", "https:") {
		t.Errorf("CSP does not allow remote media: %q", csp)
	}
	if !hasDirective(csp, "media-src", "blob:") || !hasDirective(csp, "connect-src", "https://v.redd.it") {
		t.Errorf("HLS MSE resources blocked: %q", csp)
	}
	if hasDirective(csp, "connect-src", "https:") || hasDirective(csp, "script-src", "blob:") {
		t.Errorf("unnecessarily broad HLS policy: %q", csp)
	}
	// Control: the directives that make the policy worth having must survive.
	if !hasDirective(csp, "script-src", "'self'") {
		t.Errorf("script-src 'self' missing from CSP: %q", csp)
	}
	if !hasDirective(csp, "connect-src", "'self'") {
		t.Errorf("connect-src 'self' missing from CSP: %q", csp)
	}
	if strings.Contains(csp, "default-src 'self' https:") {
		t.Errorf("default-src was widened instead of adding media-src: %q", csp)
	}
}

// hasDirective reports whether the policy contains directive with want among
// its space-separated source values.
func hasDirective(csp, directive, want string) bool {
	for _, part := range strings.Split(csp, ";") {
		fields := strings.Fields(part)
		if len(fields) == 0 || fields[0] != directive {
			continue
		}
		for _, v := range fields[1:] {
			if v == want {
				return true
			}
		}
	}
	return false
}
