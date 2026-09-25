package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestSignoutRevokesRefreshToken drives signin → signout → refresh through a
// real cookie jar, so the browser's path-scoping rules decide what is sent
// where. That is the whole point of the test: the refresh cookie used to be
// scoped to /api/auth/refresh, so the browser never sent it to
// /api/auth/signout and the handler's r.Cookie lookup failed — leaving the
// token in the database. The refresh endpoint also accepts the token in a JSON
// body, so whoever held a copy could keep minting access tokens until it
// expired (refresh tokens live for seven days).
//
// Driving it with a raw http.CookieJar rather than by hand-copying the cookie
// value is what makes the regression reproducible: a test that adds the cookie
// explicitly would pass against the broken code.
func TestSignoutRevokesRefreshToken(t *testing.T) {
	server, queries := setupTestServer(t)
	createTestUser(t, queries, "revoke@example.com", "password123")

	jar, err := newCookieJar("http://digesterr.test")
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}

	// Sign in. The jar stores the refresh cookie and path-scopes it itself.
	resp := doJarRequest(t, server, jar, http.MethodPost, "/api/auth/signin", map[string]string{
		"email":    "revoke@example.com",
		"password": "password123",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("signin: %d: %s", resp.Code, resp.Body.String())
	}
	var signin map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&signin); err != nil {
		t.Fatalf("decode signin: %v", err)
	}
	accessToken, _ := signin["access_token"].(string)
	if accessToken == "" {
		t.Fatal("no access_token from signin")
	}

	// Capture the raw refresh token the client legitimately holds, so we can
	// prove it is dead afterwards. This is the JSON-body fallback path.
	refreshToken := jarRefreshToken(t, jar, "http://digesterr.test/api/auth/refresh")
	if refreshToken == "" {
		t.Fatal("no refresh_token cookie stored by the jar after signin: " +
			"the cookie must be scoped so the client can use it")
	}

	// Sanity: the token works before signout, otherwise the assertion after
	// signout would pass for the wrong reason.
	resp = doJarRequest(t, server, jar, http.MethodPost, "/api/auth/refresh", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("refresh before signout: %d: %s (the fixture is wrong, not the fix)",
			resp.Code, resp.Body.String())
	}
	// Rotate gave us a new token; re-read it so we assert on the live one.
	refreshToken = jarRefreshToken(t, jar, "http://digesterr.test/api/auth/refresh")
	if refreshToken == "" {
		t.Fatal("refresh did not return a new refresh_token cookie")
	}

	// Sign out with the access token (the refresh cookie is not needed for the
	// request to authorize — but it IS needed for the handler to find and
	// delete the token, which is precisely the bug).
	resp = doJarRequest(t, server, jar, http.MethodPost, "/api/auth/signout", nil, bearer(accessToken))
	if resp.Code != http.StatusOK {
		t.Fatalf("signout: %d: %s", resp.Code, resp.Body.String())
	}

	// The token must be gone from the database...
	if _, err := server.auth.ValidateRefreshToken(t.Context(), refreshToken); err == nil {
		t.Error("signout left the refresh token valid in the database: " +
			"the browser never sent it to /api/auth/signout because the cookie path was too narrow")
	}

	// ...and the JSON-body fallback must not resurrect the session. This is
	// the attack the finding describes: hand the captured token to refresh.
	resp = doJarRequest(t, server, jar, http.MethodPost, "/api/auth/refresh",
		map[string]string{"refresh_token": refreshToken})
	if resp.Code == http.StatusOK {
		t.Errorf("refresh with a signed-out token succeeded (%d): the token was not revoked", resp.Code)
	}

	// A second signout must be tolerated too: the cookie is delivered every
	// time now, but the token is already deleted.
	resp = doJarRequest(t, server, jar, http.MethodPost, "/api/auth/signout", nil, bearer(accessToken))
	if resp.Code != http.StatusOK {
		t.Errorf("second signout: %d, want 200 (signout must stay idempotent)", resp.Code)
	}
}

// TestRefreshCookiePathCoversSignout pins the cookie path. It must reach every
// /api/auth endpoint that needs to read the token — refresh AND signout — while
// staying off unrelated API routes so the credential is not attached to every
// request. Widening it to "/" would work but leaks the cookie unnecessarily.
func TestRefreshCookiePathCoversSignout(t *testing.T) {
	server, _ := setupTestServer(t)
	c := server.refreshCookie(httptest.NewRequest(http.MethodPost, "/api/auth/signin", nil), "tok", 3600)

	if c.Path != "/api/auth" {
		t.Errorf("cookie Path = %q, want /api/auth so signout receives the token", c.Path)
	}
	// The path must match both endpoints the handler reads the cookie on.
	for _, p := range []string{"/api/auth/refresh", "/api/auth/signout"} {
		if !pathMatches(c.Path, p) {
			t.Errorf("cookie Path %q does not match %s", c.Path, p)
		}
	}
	// ...and must not be sent to unrelated routes.
	for _, p := range []string{"/api/items", "/api/feeds", "/api/digests"} {
		if pathMatches(c.Path, p) {
			t.Errorf("cookie Path %q leaks the refresh token to %s", c.Path, p)
		}
	}
}

// TestSignoutWithoutRefreshCookieStillSucceeds covers the client that signs out
// without a refresh cookie (an expired one, or a non-browser caller holding
// only an access token). Signout must not 500 just because there is nothing to
// revoke.
func TestSignoutWithoutRefreshCookieStillSucceeds(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "nocookie@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	resp := makeRequest(server, http.MethodPost, "/api/auth/signout", nil, token)
	if resp.Code != http.StatusOK {
		t.Errorf("signout without a refresh cookie: %d, want 200: %s", resp.Code, resp.Body.String())
	}
}

// --- helpers ---------------------------------------------------------------

// newCookieJar builds a jar with a plain HTTP policy: these tests run against
// httptest without TLS, and Go's jar refuses to store Secure cookies over
// plain HTTP.
func newCookieJar(base string) (http.CookieJar, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	return &plainHTTPJar{jar: make(map[string][]*http.Cookie), host: u.Host}, nil
}

// plainHTTPJar is a minimal cookie jar that honours path scoping — the
// behaviour under test — without the Secure-over-HTTP restriction.
type plainHTTPJar struct {
	jar  map[string][]*http.Cookie
	host string
}

func (j *plainHTTPJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	if u.Host != j.host {
		return
	}
	for _, c := range cookies {
		// A negative MaxAge is the delete signal.
		if c.MaxAge < 0 {
			j.jar[c.Name] = nil
			continue
		}
		kept := j.jar[c.Name][:0]
		for _, existing := range j.jar[c.Name] {
			if existing.Path != c.Path {
				kept = append(kept, existing)
			}
		}
		path := c.Path
		if path == "" {
			path = "/"
		}
		clone := *c
		clone.Path = path
		j.jar[c.Name] = append(kept, &clone)
	}
}

func (j *plainHTTPJar) Cookies(u *url.URL) []*http.Cookie {
	if u.Host != j.host {
		return nil
	}
	var out []*http.Cookie
	for _, cookies := range j.jar {
		for _, c := range cookies {
			if pathMatches(c.Path, u.Path) {
				out = append(out, c)
			}
		}
	}
	return out
}

// pathMatches applies RFC 6265 path-match semantics: the cookie path matches
// the request path when it is a prefix ending on a "/" boundary (or is "/", or
// is exactly equal).
func pathMatches(cookiePath, requestPath string) bool {
	if cookiePath == "" || cookiePath == "/" {
		return true
	}
	if requestPath == cookiePath {
		return true
	}
	if len(requestPath) > len(cookiePath) && requestPath[:len(cookiePath)] == cookiePath {
		return cookiePath[len(cookiePath)-1] == '/' || requestPath[len(cookiePath)] == '/'
	}
	return false
}

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// doJarRequest issues a request through the jar, recording Set-Cookie
// responses back into it, and returns the recorder.
func doJarRequest(t *testing.T, server *Server, jar http.CookieJar, method, path string, body interface{}, headers ...map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	u, err := url.Parse("http://digesterr.test" + path)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	var req *http.Request
	if body != nil {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
		req = httptest.NewRequest(method, u.String(), &buf)
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, u.String(), nil)
	}
	req.Host = u.Host
	for _, c := range jar.Cookies(u) {
		req.AddCookie(c)
	}
	for _, h := range headers {
		for k, v := range h {
			req.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	// Store whatever the handler set, so the jar paths evolve as a browser's would.
	jar.SetCookies(u, w.Result().Cookies())
	return w
}

func jarRefreshToken(t *testing.T, jar http.CookieJar, target string) string {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, c := range jar.Cookies(u) {
		if c.Name == "refresh_token" {
			return c.Value
		}
	}
	return ""
}
