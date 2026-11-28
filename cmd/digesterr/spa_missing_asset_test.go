package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// A request for a missing file must be a 404, not the SPA document.
//
// The fallback exists so client-side routes (/feeds/1, /digests/1/2) load the
// app. Answered for a missing ASSET, it returns HTML with a 200, and clients
// treat that as a broken file rather than a missing one: NetNewsWire caches an
// icon URL that returned a non-image as "no favicon here" for days, which is how
// every feed showed a placeholder instead of digesterr's own icon.
func TestSPAHandlerMissingAssetIs404(t *testing.T) {
	h := spaHandler(http.NotFoundHandler(), http.FS(fstest.MapFS{
		"index.html":       {Data: []byte("<html></html>")},
		"favicon.ico":      {Data: []byte("ico")},
		"assets/index.js":  {Data: []byte("js")},
		"favicon-32.png":   {Data: []byte("png")},
		"apple-touch.png":  {Data: []byte("png")},
		"assets/style.css": {Data: []byte("css")},
	}))

	for _, path := range []string{
		"/nonexistent.ico",
		"/assets/missing-abc123.js",
		"/favicon-999.png",
		"/robots.txt",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404 (body %.60q)", path, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); strings.Contains(ct, "text/html") && rec.Body.Len() > 0 {
			t.Errorf("%s: answered with HTML for a missing asset: %q", path, ct)
		}
	}
}

// The same handler must still serve the app for client-side routes and for
// files that do exist — the 404 above must not break either.
func TestSPAHandlerStillServesRoutesAndRealFiles(t *testing.T) {
	h := spaHandler(http.NotFoundHandler(), http.FS(fstest.MapFS{
		"index.html":     {Data: []byte("<html>app</html>")},
		"favicon.ico":    {Data: []byte("ico")},
		"assets/main.js": {Data: []byte("js")},
	}))

	for _, path := range []string{"/", "/feeds/1", "/feeds/15/779648", "/digests/2", "/digests/2/7"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "app") {
			t.Errorf("%s: did not serve the SPA document: %q", path, rec.Body.String())
		}
	}

	for _, path := range []string{"/favicon.ico", "/assets/main.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "app") {
			t.Errorf("%s: served the SPA document instead of the file", path)
		}
	}
}
