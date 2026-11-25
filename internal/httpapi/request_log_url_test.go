package httpapi

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"digesterr/internal/testutil"
)

// The request logger must record the PATH only. The OIDC callback carries the
// authorization code and state in its query string, and the start redirect
// carries the nonce and PKCE challenge in the Location header; an access log
// built from r.URL.RequestURI() (or from the raw request target) would put all
// of them into a store that every account can read through /api/logs.
//
// This is a guard rather than a bug fix: it passed when written. It was
// verified to fail by switching the middleware's "path" attribute to
// r.URL.RequestURI(), which is the single-line change most likely to be made by
// someone "improving" the log line later.
func TestRequestLogRecordsPathWithoutQuery(t *testing.T) {
	server, _ := setupTestServer(t)
	capture := testutil.CaptureSlog(t, slog.LevelDebug)

	const secretQuery = "code=SUPERSECRETCODE&state=SUPERSECRETSTATE"
	req := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?"+secretQuery, nil)
	req.Host = "digesterr.test"
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	var checked int
	for _, r := range capture.Records() {
		if r.Message != "request" {
			continue
		}
		checked++
		attrs := testutil.AttrMap(r)
		if attrs["path"] != "/api/auth/oidc/callback" {
			t.Errorf("request log path = %q, want the bare path with no query string", attrs["path"])
		}
		for key, val := range attrs {
			if strings.Contains(val, "SUPERSECRET") {
				t.Errorf("request log attribute %q carries the query string: %q", key, val)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no 'request' record was captured: this test proves nothing about the access log")
	}

	// And nothing anywhere in the log stream may carry the query, including
	// records from a handler that decides to log the failure.
	for _, r := range capture.Records() {
		if strings.Contains(r.Message, "SUPERSECRET") {
			t.Errorf("record %q carries the query string", r.Message)
		}
		testutil.AttrMap(r)
		for key, val := range testutil.AttrMap(r) {
			if strings.Contains(val, "SUPERSECRET") {
				t.Errorf("record %q attribute %q carries the query string: %q", r.Message, key, val)
			}
		}
	}
}
