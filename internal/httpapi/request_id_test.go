package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Every response carries an X-Request-ID: generated when the client sends
// none, echoed (truncated) when it does.
func TestRequestIDStampedOnResponse(t *testing.T) {
	server, _ := setupTestServer(t)

	w := makeRequest(server, http.MethodGet, "/healthz", nil, "")
	if got := w.Header().Get("X-Request-ID"); got == "" {
		t.Fatal("generated request ID missing from response")
	}

	w2 := makeRequestWithHeaders(server, http.MethodGet, "/healthz", nil, map[string]string{
		"X-Request-ID": "client-123",
	})
	if got := w2.Header().Get("X-Request-ID"); got != "client-123" {
		t.Fatalf("client request ID = %q, want echoed client-123", got)
	}
}

// Client IDs are truncated so a hostile header cannot grow log lines
// unboundedly.
func TestRequestIDTruncatesClientValue(t *testing.T) {
	server, _ := setupTestServer(t)
	long := strings.Repeat("x", 200)
	w := makeRequestWithHeaders(server, http.MethodGet, "/healthz", nil, map[string]string{
		"X-Request-ID": long,
	})
	if got := w.Header().Get("X-Request-ID"); len(got) > 64 {
		t.Fatalf("request ID length = %d, want <= 64", len(got))
	}
}

func TestRequestIDFromContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), requestIDKey, "abc")
	if got := RequestIDFromContext(ctx); got != "abc" {
		t.Fatalf("RequestIDFromContext = %q, want abc", got)
	}
	if got := RequestIDFromContext(context.Background()); got != "" {
		t.Fatalf("RequestIDFromContext on bare ctx = %q, want empty", got)
	}
}

func makeRequestWithHeaders(server *Server, method, path string, body interface{}, headers map[string]string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if buf.Len() > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	return w
}
