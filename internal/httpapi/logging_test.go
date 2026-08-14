package httpapi

import (
	"log/slog"
	"net/http"
	"strconv"
	"testing"
	"time"

	"digesterr/internal/store"
	"digesterr/internal/testutil"
)

// TestHTTPRequestDebugLog verifies every HTTP request produces a debug-level
// "request" record carrying host, path, method, status, and response time.
func TestHTTPRequestDebugLog(t *testing.T) {
	server, _ := setupTestServer(t)
	capture := testutil.CaptureSlog(t, slog.LevelDebug)

	w := makeRequest(server, http.MethodGet, "/api/categories", nil, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	rec, ok := capture.Find("request")
	if !ok {
		t.Fatal("missing debug 'request' log record")
	}
	if rec.Level != slog.LevelDebug {
		t.Errorf("request record level = %v, want debug", rec.Level)
	}
	attrs := testutil.AttrMap(rec)
	for _, key := range []string{"method", "host", "path", "status", "duration"} {
		if attrs[key] == "" {
			t.Errorf("request record missing %q attribute, got %v", key, attrs)
		}
	}
	if attrs["method"] != "GET" {
		t.Errorf("method = %q, want GET", attrs["method"])
	}
	if attrs["path"] != "/api/categories" {
		t.Errorf("path = %q, want /api/categories", attrs["path"])
	}
	if attrs["status"] != "401" {
		t.Errorf("status = %q, want 401", attrs["status"])
	}
	if _, err := time.ParseDuration(attrs["duration"]); err != nil {
		t.Errorf("duration %q is not a parseable duration: %v", attrs["duration"], err)
	}
	if attrs["category"] != "http" {
		t.Errorf("category = %q, want http", attrs["category"])
	}
	testutil.AssertAllCategorized(t, capture)
}

// TestHTTPRequestLogHiddenAtInfo verifies request logging stays debug: at the
// default info level no "request" record is emitted.
func TestHTTPRequestLogHiddenAtInfo(t *testing.T) {
	server, _ := setupTestServer(t)
	capture := testutil.CaptureSlog(t, slog.LevelInfo)

	makeRequest(server, http.MethodGet, "/api/categories", nil, "")

	if _, ok := capture.Find("request"); ok {
		t.Error("debug 'request' record emitted at info level")
	}
}

// TestHTTP5xxWarn verifies a 500 response promotes the request to a warn
// record and that the failed action itself is logged at error with context.
func TestHTTP5xxWarn(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "genfail@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Empty Feed",
		Url:    "https://example.com/feed.xml",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	cfg, err := queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Empty Config",
		Schedule: "* * * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":2,"comments":5}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	capture := testutil.CaptureSlog(t, slog.LevelInfo)
	w := makeRequest(server, http.MethodPost, "/api/digest-configs/"+itoa(cfg.ID)+"/generate", nil, token)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}

	rec, ok := capture.Find("request failed with server error")
	if !ok {
		t.Fatal("missing warn 'request failed with server error' record")
	}
	if rec.Level != slog.LevelWarn {
		t.Errorf("5xx record level = %v, want warn", rec.Level)
	}
	attrs := testutil.AttrMap(rec)
	if attrs["status"] != "500" {
		t.Errorf("status = %q, want 500", attrs["status"])
	}
	if _, err := time.ParseDuration(attrs["duration"]); err != nil {
		t.Errorf("duration %q not parseable: %v", attrs["duration"], err)
	}
	if attrs["category"] != "http" {
		t.Errorf("category = %q, want http", attrs["category"])
	}

	// The failed action itself is logged at error with the config context.
	// It is emitted from the httpapi handler, so its source-derived category
	// is http (not digest) — only records from internal/digest get category
	// digest by default.
	gen, ok := capture.Find("digest generation failed")
	if !ok {
		t.Fatal("missing error 'digest generation failed' record")
	}
	if gen.Level != slog.LevelError {
		t.Errorf("digest generation failed level = %v, want error", gen.Level)
	}
	gattrs := testutil.AttrMap(gen)
	for _, key := range []string{"config_id", "config_name", "user_id", "error"} {
		if gattrs[key] == "" {
			t.Errorf("digest generation failed missing %q, got %v", key, gattrs)
		}
	}
	if gattrs["category"] != "http" {
		t.Errorf("digest generation failed category = %q, want http (source-derived from httpapi handler)", gattrs["category"])
	}
	testutil.AssertAllCategorized(t, capture)
}
