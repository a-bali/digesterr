package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The refresh cookie was moved from /api/auth/refresh to /api/auth (so that
// /api/auth/signout could see it and revoke the token). Cookies are keyed on
// (name, domain, path), so that move left every existing client holding TWO
// cookies for the request path. Browsers send them longest-path-first — the
// stale one first — and net/http's Request.Cookie returns the first match, so
// the server validated a token whose database row had already been rotated
// away and answered 401. Every pre-upgrade session ended up in a refresh
// failure loop, and signing in again did not clear it.

// refreshCookieJar models a browser jar for the behaviours these tests turn on:
// (name, path) keying, deletion by negative Max-Age, and longest-path-first
// ordering of the Cookie header (RFC 6265bis S5.8.3, which Chromium implements
// in CookieMonster::CookieSorter and Firefox in its own cookie service).
type refreshCookieJar struct {
	cookies map[string]*http.Cookie // key: path
}

func newRefreshCookieJar() *refreshCookieJar {
	return &refreshCookieJar{cookies: map[string]*http.Cookie{}}
}

func (j *refreshCookieJar) SetCookies(_ *url.URL, cookies []*http.Cookie) {
	for _, c := range cookies {
		if c.Name != "refresh_token" {
			continue
		}
		if c.MaxAge < 0 || (c.MaxAge == 0 && c.Value == "") {
			delete(j.cookies, c.Path)
			continue
		}
		clone := *c
		j.cookies[c.Path] = &clone
	}
}

func (j *refreshCookieJar) Cookies(*url.URL) []*http.Cookie {
	out := make([]*http.Cookie, 0, len(j.cookies))
	for _, c := range j.cookies {
		out = append(out, c)
	}
	// Longest path first; ties by earlier creation are impossible here.
	for i := 1; i < len(out); i++ {
		for k := i; k > 0 && len(out[k].Path) > len(out[k-1].Path); k-- {
			out[k], out[k-1] = out[k-1], out[k]
		}
	}
	return out
}

// doRefresh sends POST /api/auth/refresh through the jar and returns the
// status plus the parsed error message.
func doRefresh(t *testing.T, server *Server, jar *refreshCookieJar) (int, string) {
	t.Helper()
	u, err := url.Parse("http://digesterr.test/api/auth/refresh")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, u.String(), nil)
	req.Host = u.Host
	req.Header.Set("Content-Type", "application/json")
	for _, c := range jar.Cookies(u) {
		req.AddCookie(c) // preserves order, unlike AddCookie per call site
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	jar.SetCookies(u, w.Result().Cookies())
	var body map[string]string
	_ = json.NewDecoder(w.Body).Decode(&body)
	return w.Code, body["error"]
}

// jarPaths reports which paths currently hold a cookie, for assertions.
func jarPaths(jar *refreshCookieJar) []string {
	out := make([]string, 0, len(jar.cookies))
	for p := range jar.cookies {
		out = append(out, p)
	}
	return out
}

// TestSigninRecoversFromLegacyRefreshCookiePath is the regression test for the
// duplicate-cookie logout loop. A client that signed in under the old build
// holds a cookie at /api/auth/refresh; after signing in under the current build
// that stale cookie is still in the jar, and the session must work anyway.
// Before the fix the stale cookie was presented first, selected by r.Cookie,
// found missing from refresh_tokens and answered 401 — so the user was logged
// out again within minutes, permanently.
func TestSigninRecoversFromLegacyRefreshCookiePath(t *testing.T) {
	server, queries := setupTestServer(t)
	createTestUser(t, queries, "upgrade@example.com", "password123")

	jar := newRefreshCookieJar()
	// The upgrade state: a refresh cookie at the pre-fix path, as a browser
	// that used the previous release still holds it.
	jar.cookies[legacyRefreshCookiePath] = &http.Cookie{
		Name: "refresh_token", Value: "token-from-the-old-build", Path: legacyRefreshCookiePath,
	}

	u, _ := url.Parse("http://digesterr.test/api/auth/signin")
	body, _ := json.Marshal(map[string]string{"email": "upgrade@example.com", "password": "password123"})
	req := httptest.NewRequest(http.MethodPost, u.String(), strings.NewReader(string(body)))
	req.Host = u.Host
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	jar.SetCookies(u, w.Result().Cookies())

	if w.Code != http.StatusOK {
		t.Fatalf("signin: %d: %s", w.Code, w.Body.String())
	}
	// Signin does not disturb the stale cookie (a deletion Set-Cookie next to a
	// credential makes some clients discard the credential). What matters is
	// that the session WORKS: the stale cookie is tried after the live one and
	// must not be able to shadow it. Before the fix this returned 401 and the
	// user was logged out again within minutes.
	live := jar.cookies[refreshCookiePath]
	if live == nil || live.Value == "token-from-the-old-build" {
		t.Fatalf("signin did not store a fresh cookie at %q: jar = %v", refreshCookiePath, jar.cookies)
	}
	if code, msg := doRefresh(t, server, jar); code != http.StatusOK {
		t.Fatalf("refresh right after an upgrade signin: %d (%s), want 200 — the stale "+
			"cookie at %q must not shadow the live one", code, msg, legacyRefreshCookiePath)
	}
}

// TestRefreshIgnoresStaleLegacyCookie covers the path that actually runs in
// production: the SPA refreshes silently on every page load (the router guard),
// long before the user signs in again. This is the request that returned 401 in
// the browser console, and it must succeed while the stale cookie is still
// present.
func TestRefreshIgnoresStaleLegacyCookie(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "refresh@example.com", "password123")

	live, err := server.auth.CreateRefreshToken(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("create refresh token: %v", err)
	}

	jar := newRefreshCookieJar()
	// Both cookies, exactly as the failed upgrade left them: the live one at
	// the current path, a dead one at the legacy path. The jar orders longest
	// path first, so the dead one is presented ahead of the live one — this is
	// what makes r.Cookie pick the wrong one on the unfixed code.
	jar.cookies[refreshCookiePath] = &http.Cookie{
		Name: "refresh_token", Value: live, Path: refreshCookiePath,
	}
	jar.cookies[legacyRefreshCookiePath] = &http.Cookie{
		Name: "refresh_token", Value: "rotated-away-token", Path: legacyRefreshCookiePath,
	}

	code, msg := doRefresh(t, server, jar)
	if code != http.StatusOK {
		t.Fatalf("refresh with a live cookie present but a stale one at the legacy path: "+
			"%d (%s), want 200 — the stale cookie must not shadow the live one", code, msg)
	}
	// The rotated replacement must be usable, repeatedly: this is the "logged
	// out after a couple of minutes" symptom, where every silent refresh failed
	// for the rest of the session.
	for i := 0; i < 3; i++ {
		if code, msg := doRefresh(t, server, jar); code != http.StatusOK {
			t.Fatalf("refresh #%d: %d (%s), want 200", i+2, code, msg)
		}
	}
}

// TestSignoutRetiresEveryRefreshCookiePath is the signout-side counterpart: the
// whole point of moving the path was that signout can revoke. If signout
// retires only the current path, a legacy-path cookie outlives it and keeps
// counting as a live session in the jar.
func TestSignoutRetiresEveryRefreshCookiePath(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "signout-paths@example.com", "password123")

	live, err := server.auth.CreateRefreshToken(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("create refresh token: %v", err)
	}
	access, err := server.auth.GenerateAccessToken(&user)
	if err != nil {
		t.Fatalf("access token: %v", err)
	}

	jar := newRefreshCookieJar()
	jar.cookies[refreshCookiePath] = &http.Cookie{Name: "refresh_token", Value: live, Path: refreshCookiePath}
	jar.cookies[legacyRefreshCookiePath] = &http.Cookie{
		Name: "refresh_token", Value: "dead-token", Path: legacyRefreshCookiePath,
	}

	u, _ := url.Parse("http://digesterr.test/api/auth/signout")
	req := httptest.NewRequest(http.MethodPost, u.String(), nil)
	req.Host = u.Host
	req.Header.Set("Authorization", "Bearer "+access)
	for _, c := range jar.Cookies(u) {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	jar.SetCookies(u, w.Result().Cookies())

	if w.Code != http.StatusOK {
		t.Fatalf("signout: %d: %s", w.Code, w.Body.String())
	}
	if paths := jarPaths(jar); len(paths) != 0 {
		t.Fatalf("jar after signout = %v, want none: a refresh cookie surviving signout is a live credential", paths)
	}
	// The retired path must have been named explicitly, or a browser keeps the
	// cookie there and the jar would not be empty.
	named := map[string]bool{}
	for _, c := range w.Result().Cookies() {
		if c.Name == "refresh_token" && c.MaxAge < 0 {
			named[c.Path] = true
		}
	}
	if !named[refreshCookiePath] || !named[legacyRefreshCookiePath] {
		t.Fatalf("signout named deletions for %v, want both %q and %q", named, refreshCookiePath, legacyRefreshCookiePath)
	}
	if _, err := server.auth.ValidateRefreshToken(t.Context(), live); err == nil {
		t.Error("refresh token still valid after signout")
	}
}

// TestRefreshCookieScopeIsCurrentPath pins the actual scope, so a future move
// cannot silently repeat the bug without updating staleRefreshCookiePaths.
func TestRefreshCookieScopeIsCurrentPath(t *testing.T) {
	server, _ := setupTestServer(t)
	c := server.refreshCookie(httptest.NewRequest(http.MethodPost, "/api/auth/signin", nil), "tok", 3600)
	if c.Path != refreshCookiePath {
		t.Errorf("Path = %q, want %q", c.Path, refreshCookiePath)
	}
	// /api/auth must still cover signin, signout and refresh; widening past
	// /api/auth is what the scoping comment argues against.
	for _, p := range []string{"/api/auth/signin", "/api/auth/signout", "/api/auth/refresh"} {
		if !strings.HasPrefix(p, refreshCookiePath) {
			t.Errorf("%s is not covered by cookie path %s", p, refreshCookiePath)
		}
	}
}

// TestRetireSupersededRefreshCookiesNamesEveryRetiredPath guards the list and
// the header order: every path the app has ever issued on must be retired, and
// the live cookie must be the FIRST Set-Cookie of a signin response, because a
// JSON-body client reads its credential out of that header.
func TestRetireSupersededRefreshCookiesNamesEveryRetiredPath(t *testing.T) {
	server, _ := setupTestServer(t)

	var retired []string
	for _, path := range staleRefreshCookiePaths {
		retired = append(retired, path)
	}
	seenLegacy := false
	for _, p := range retired {
		if p == legacyRefreshCookiePath {
			seenLegacy = true
		}
		if p == refreshCookiePath {
			t.Errorf("staleRefreshCookiePaths contains the CURRENT path %q: this would expire the live cookie", p)
		}
	}
	if !seenLegacy {
		t.Fatalf("staleRefreshCookiePaths = %v, must include the pre-v0.7.4 path %q",
			retired, legacyRefreshCookiePath)
	}

	// The deletion cookies really are emitted, one per retired path, at that path.
	req := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)
	w := httptest.NewRecorder()
	server.retireSupersededRefreshCookies(w, req)
	var deleted []string
	for _, c := range w.Result().Cookies() {
		if c.Name == "refresh_token" {
			if c.MaxAge >= 0 {
				t.Errorf("retirement emitted a non-deleting cookie at %q (MaxAge=%d)", c.Path, c.MaxAge)
			}
			deleted = append(deleted, c.Path)
		}
	}
	if len(deleted) != len(staleRefreshCookiePaths) {
		t.Fatalf("deletions = %v, want one per retired path %v", deleted, staleRefreshCookiePaths)
	}

	// Minting a cookie must not put a deletion next to it, or a client that
	// treats "Set-Cookie: refresh_token=" as the name being cleared would drop
	// the token it was just handed. signin/refresh use http.SetCookie directly
	// for the credential; only the invalidation paths call the retire helper.
	w = httptest.NewRecorder()
	server.clearRefreshCookieForTest(w, req, "the-new-token")
	set := w.Result().Cookies()
	if len(set) != 1 {
		t.Fatalf("minting emitted %d Set-Cookie(s), want exactly 1 (the credential)", len(set))
	}
	if set[0].Value != "the-new-token" || set[0].Path != refreshCookiePath || set[0].MaxAge < 0 {
		t.Fatalf("Set-Cookie = value %q path %q MaxAge %d, want the live token at %q",
			set[0].Value, set[0].Path, set[0].MaxAge, refreshCookiePath)
	}
}

// clearRefreshCookieForTest is the credential-minting call signin and refresh
// make: a single Set-Cookie, no deletions alongside it.
func (s *Server) clearRefreshCookieForTest(w http.ResponseWriter, r *http.Request, value string) {
	http.SetCookie(w, s.refreshCookie(r, value, 3600))
}
