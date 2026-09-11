package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func createTestUserAndToken(t *testing.T, server *Server, email, password string) string {
	t.Helper()
	user := createTestUser(t, server.queries, email, password)
	return getTestToken(t, server.auth, &user)
}

const testRSSDoc = `<?xml version="1.0"?><rss version="2.0"><channel><title>Test RSS Feed</title><link>https://example.com/</link><item><title>Item 1</title><link>https://example.com/1</link></item></channel></rss>`

const testAtomDoc = `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"><title>Test Atom Feed</title><entry><title>Entry 1</title><id>https://example.com/1</id></entry></feed>`

func startFeedServer(t *testing.T, body, contentType string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func discoverBody(t *testing.T, w *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func TestHandleDiscoverFeedRequiresAuth(t *testing.T) {
	server, _ := setupTestServer(t)
	w := makeRequest(server, http.MethodPost, "/api/feeds/discover",
		map[string]string{"type": "rss", "url": "https://example.com/feed"}, "")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHandleDiscoverFeedMissingTypeOrURL(t *testing.T) {
	server, _ := setupTestServer(t)
	token := createTestUserAndToken(t, server, "test@example.com", "password")

	tests := []struct {
		name string
		body map[string]string
	}{
		{"missing type", map[string]string{"url": "https://example.com/feed"}},
		{"missing url", map[string]string{"type": "rss"}},
		{"empty type", map[string]string{"type": "", "url": "https://example.com/feed"}},
		{"empty url", map[string]string{"type": "rss", "url": ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := makeRequest(server, http.MethodPost, "/api/feeds/discover", tt.body, token)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestHandleDiscoverFeedUnknownTypeReturnsEmptyName(t *testing.T) {
	server, _ := setupTestServer(t)
	token := createTestUserAndToken(t, server, "test@example.com", "password")

	w := makeRequest(server, http.MethodPost, "/api/feeds/discover", map[string]string{
		"type": "bogus",
		"url":  "https://example.com/feed",
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (unknown type is a soft failure)", w.Code, http.StatusOK)
	}
	resp := discoverBody(t, w)
	if resp["name"] != "" {
		t.Errorf("name = %q, want empty for unknown type", resp["name"])
	}
}

func TestHandleDiscoverFeedRSS(t *testing.T) {
	srv := startFeedServer(t, testRSSDoc, "application/rss+xml")
	server, _ := setupTestServer(t)
	token := createTestUserAndToken(t, server, "test@example.com", "password")

	w := makeRequest(server, http.MethodPost, "/api/feeds/discover", map[string]string{
		"type": "rss",
		"url":  srv.URL,
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	resp := discoverBody(t, w)
	if resp["name"] != "Test RSS Feed" {
		t.Errorf("name = %q, want %q", resp["name"], "Test RSS Feed")
	}
}

func TestHandleDiscoverFeedAtom(t *testing.T) {
	srv := startFeedServer(t, testAtomDoc, "application/atom+xml")
	server, _ := setupTestServer(t)
	token := createTestUserAndToken(t, server, "test@example.com", "password")

	w := makeRequest(server, http.MethodPost, "/api/feeds/discover", map[string]string{
		"type": "atom",
		"url":  srv.URL,
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	resp := discoverBody(t, w)
	if resp["name"] != "Test Atom Feed" {
		t.Errorf("name = %q, want %q", resp["name"], "Test Atom Feed")
	}
}

func TestHandleDiscoverFeedHN(t *testing.T) {
	srv := startFeedServer(t, testRSSDoc, "application/rss+xml")
	server, _ := setupTestServer(t)
	token := createTestUserAndToken(t, server, "test@example.com", "password")

	w := makeRequest(server, http.MethodPost, "/api/feeds/discover", map[string]string{
		"type": "hn",
		"url":  srv.URL,
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	resp := discoverBody(t, w)
	if resp["name"] != "Test RSS Feed" {
		t.Errorf("name = %q, want %q", resp["name"], "Test RSS Feed")
	}
}

func TestHandleDiscoverFeedLobsters(t *testing.T) {
	srv := startFeedServer(t, testRSSDoc, "application/rss+xml")
	server, _ := setupTestServer(t)
	token := createTestUserAndToken(t, server, "test@example.com", "password")

	w := makeRequest(server, http.MethodPost, "/api/feeds/discover", map[string]string{
		"type": "lobsters",
		"url":  srv.URL,
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	resp := discoverBody(t, w)
	if resp["name"] != "Test RSS Feed" {
		t.Errorf("name = %q, want %q", resp["name"], "Test RSS Feed")
	}
}

func TestHandleDiscoverFeedReddit(t *testing.T) {
	// Mock Reddit OAuth + about API.
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"mock-tok","token_type":"bearer","expires_in":3600}`))
	}))
	defer tokenSrv.Close()

	aboutSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "about") {
			_, _ = w.Write([]byte(`{"data":{"display_name":"golang","display_name_prefixed":"r/golang"}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer aboutSrv.Close()

	server, _ := setupTestServer(t)
	token := createTestUserAndToken(t, server, "test@example.com", "password")

	// Point the reddit fetcher at mock servers.
	rf := server.poller.Enrichers() // just checking it's there
	_ = rf
	// The handler delegates to s.poller.DiscoverFeedName, which uses the
	// RedditFetcher's baseURL/apiBaseURL. These are not directly settable
	// from the handler test. Instead, we test at the feed layer
	// (discover_test.go TestDiscoverFeedNameReddit covers this).
	// Here we verify the handler returns 200 + empty name when
	// upstream fails (reddit credentials not configured in test).
	w := makeRequest(server, http.MethodPost, "/api/feeds/discover", map[string]string{
		"type": "reddit",
		"url":  "https://www.reddit.com/r/golang",
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (soft failure when upstream unavailable)", w.Code, http.StatusOK)
	}
	resp := discoverBody(t, w)
	// Without credentials configured, the name is empty — the user types it.
	if resp["name"] != "" {
		t.Errorf("name = %q, want empty when reddit credentials missing", resp["name"])
	}
}

func TestHandleDiscoverFeedTwitter(t *testing.T) {
	// Same approach as Reddit: credentials not configured in test, so soft failure.
	server, _ := setupTestServer(t)
	token := createTestUserAndToken(t, server, "test@example.com", "password")

	w := makeRequest(server, http.MethodPost, "/api/feeds/discover", map[string]string{
		"type": "twitter",
		"url":  "https://x.com/elonmusk",
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (soft failure when upstream unavailable)", w.Code, http.StatusOK)
	}
	resp := discoverBody(t, w)
	if resp["name"] != "" {
		t.Errorf("name = %q, want empty when twitter credentials missing", resp["name"])
	}
}

// The handler never returns a 500; even a bad URL yields empty name.
func TestHandleDiscoverFeedBadURLReturnsEmptyName(t *testing.T) {
	server, _ := setupTestServer(t)
	token := createTestUserAndToken(t, server, "test@example.com", "password")

	w := makeRequest(server, http.MethodPost, "/api/feeds/discover", map[string]string{
		"type": "rss",
		"url":  "https://definitely-not-a-real-host-12345.example/feed.xml",
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (bad URL is soft failure)", w.Code, http.StatusOK)
	}
	resp := discoverBody(t, w)
	if resp["name"] != "" {
		t.Errorf("name = %q, want empty for unreachable URL", resp["name"])
	}
}
