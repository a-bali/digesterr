package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"digesterr/internal/store"
)

// readerRequest builds an HTTP request against the server, optionally carrying
// reader auth headers (Authorization: GoogleLogin auth=... or X-Auth-Token).
func readerRequest(server *Server, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	return w
}

func createReaderUser(t *testing.T, queries *store.Queries, apiKey string) store.User {
	t.Helper()
	user, err := queries.CreateUser(t.Context(), store.CreateUserParams{
		Email:        "reader@example.com",
		PasswordHash: "unused",
		ApiKey:       apiKey,
	})
	if err != nil {
		t.Fatalf("failed to create reader test user: %v", err)
	}
	return user
}

func TestReaderTokenGoogleLogin(t *testing.T) {
	server, queries := setupTestServer(t)
	createReaderUser(t, queries, "testkey123")

	w := readerRequest(server, http.MethodGet, "/reader/api/0/token", "", map[string]string{
		"Authorization": "GoogleLogin auth=testkey123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "testkey123" {
		t.Fatalf("expected body %q, got %q", "testkey123", got)
	}
}

func TestReaderTokenXAuthToken(t *testing.T) {
	server, queries := setupTestServer(t)
	createReaderUser(t, queries, "testkey123")

	w := readerRequest(server, http.MethodGet, "/reader/api/0/token", "", map[string]string{
		"X-Auth-Token": "testkey123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "testkey123" {
		t.Fatalf("expected body %q, got %q", "testkey123", got)
	}
}

func TestReaderUserInfo(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")

	w := readerRequest(server, http.MethodGet, "/reader/api/0/user-info", "", map[string]string{
		"Authorization": "GoogleLogin auth=testkey123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var info map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&info); err != nil {
		t.Fatalf("failed to decode user-info response: %v", err)
	}
	if got := info["userId"]; got != strconv.FormatInt(user.ID, 10) {
		t.Fatalf("expected userId %q, got %v", strconv.FormatInt(user.ID, 10), got)
	}
	if got := info["userProfileId"]; got != strconv.FormatInt(user.ID, 10) {
		t.Fatalf("expected userProfileId %q, got %v", strconv.FormatInt(user.ID, 10), got)
	}
	if got := info["userName"]; got != "digesterr" {
		t.Fatalf("expected userName %q, got %v", "digesterr", got)
	}
}

func TestReaderAuthUnauthorized(t *testing.T) {
	server, queries := setupTestServer(t)
	createReaderUser(t, queries, "testkey123")

	for _, path := range []string{"/reader/api/0/token", "/reader/api/0/user-info"} {
		// No key supplied at all.
		w := readerRequest(server, http.MethodGet, path, "", nil)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s with no key: expected 401, got %d", path, w.Code)
		}

		// Unknown key.
		w = readerRequest(server, http.MethodGet, path, "", map[string]string{
			"Authorization": "GoogleLogin auth=wrongkey",
		})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s with unknown key: expected 401, got %d", path, w.Code)
		}
	}
}

func TestReaderClientLogin(t *testing.T) {
	server, queries := setupTestServer(t)
	createReaderUser(t, queries, "testkey123")

	form := url.Values{"Email": {"testkey123"}}
	w := readerRequest(server, http.MethodPost, "/reader/api/0/accounts/ClientLogin", form.Encode(), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "Auth=testkey123") {
		t.Fatalf("expected body to contain %q, got %q", "Auth=testkey123", body)
	}
}

func TestReaderClientLoginPasswdFallback(t *testing.T) {
	server, queries := setupTestServer(t)
	createReaderUser(t, queries, "testkey123")

	form := url.Values{"Passwd": {"testkey123"}}
	w := readerRequest(server, http.MethodPost, "/reader/api/0/accounts/ClientLogin", form.Encode(), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "Auth=testkey123") {
		t.Fatalf("expected body to contain %q, got %q", "Auth=testkey123", body)
	}
}

func TestReaderClientLoginBadAuth(t *testing.T) {
	server, queries := setupTestServer(t)
	createReaderUser(t, queries, "testkey123")

	form := url.Values{"Email": {"wrongkey"}}
	w := readerRequest(server, http.MethodPost, "/reader/api/0/accounts/ClientLogin", form.Encode(), nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, "Error=BadAuthentication") {
		t.Fatalf("expected body to contain %q, got %q", "Error=BadAuthentication", body)
	}
}
