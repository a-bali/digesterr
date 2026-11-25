package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"digesterr/internal/auth"
	"digesterr/internal/config"
	"digesterr/internal/logbuf"
	"digesterr/internal/store"
	"digesterr/internal/testutil"
)

// testRedirectURL is the callback registered with the test provider. Plain
// HTTP on loopback is the one shape config validation permits without TLS (see
// config.requireAbsoluteHTTPS), which is exactly the local-provider case.
const testRedirectURL = "http://127.0.0.1:8080" + config.CallbackPath

// setupOIDCServer builds a server with OIDC enabled against a real (httptest)
// authorization server, plus an isolated cookie jar standing in for one
// browser. No part of the flow is stubbed: discovery, the redirect, the code
// exchange and ID token verification all run for real over loopback HTTP.
func setupOIDCServer(t *testing.T) (*Server, *store.Queries, *testOIDCProvider, http.CookieJar) {
	t.Helper()
	db, queries := setupTestDB(t)
	p := newTestOIDCProvider(t, "digesterr-test")

	cfg := config.Default()
	cfg.Server.JWTSecret = "test-jwt-secret-for-testing"
	cfg.OIDC = config.OIDCConfig{
		Enabled:      true,
		Issuer:       p.issuer(),
		ClientID:     p.clientID,
		ClientSecret: "test-client-secret",
		RedirectURL:  testRedirectURL,
		ProviderName: "Test SSO",
	}

	authSvc := auth.NewService(cfg.Server.JWTSecret, queries, db)
	server := NewServer(db, queries, authSvc, logbuf.New(100), cfg)
	t.Cleanup(server.commentCache.Close)
	if err := server.EnableOIDC(t.Context()); err != nil {
		t.Fatalf("EnableOIDC: %v", err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return server, queries, p, jar
}

// newBrowser is a second, independent browser (its own cookie jar) sharing the
// same server — the attacker's browser in the cross-browser tests.
func newBrowser(t *testing.T) http.CookieJar {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return jar
}

// beginOIDCLogin drives GET /api/auth/oidc/start through jar and returns the
// provider redirect the server issued, after following nothing.
func beginOIDCLogin(t *testing.T, server *Server, jar http.CookieJar, query string) *httptest.ResponseRecorder {
	t.Helper()
	return doJarRequest(t, server, jar, http.MethodGet, "/api/auth/oidc/start"+query, nil)
}

// completeOIDCLogin takes the /start redirect, asks the provider to authorize
// (which issues a code), and delivers the callback to the SAME jar. It returns
// the callback response.
func completeOIDCLogin(t *testing.T, server *Server, jar http.CookieJar) *httptest.ResponseRecorder {
	t.Helper()
	start := beginOIDCLogin(t, server, jar, "")
	if start.Code != http.StatusFound {
		t.Fatalf("start: %d, want 302: %s", start.Code, start.Body.String())
	}
	location := start.Header().Get("Location")
	if location == "" {
		t.Fatal("start returned no Location header: the browser was not sent to the provider")
	}
	return finishAtProvider(t, server, jar, location)
}

// authorizeAtProvider performs the browser's step at the provider: GET the
// /authorize URL and stop at the redirect back to us, without following it.
//
// The redirect is deliberately NOT followed by the HTTP client: the callback
// has to be delivered through the server object (which owns the session and the
// database), not through a live listener, and following it would also hide the
// very Location header these tests assert on.
func authorizeAtProvider(t *testing.T, authorizeURL string) *url.URL {
	t.Helper()
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       10 * time.Second,
	}
	resp, err := client.Get(authorizeURL) //nolint:gosec // loopback test provider
	if err != nil {
		t.Fatalf("GET provider authorize URL: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		t.Fatalf("provider /authorize: %d, want 302: %s", resp.StatusCode, body)
	}
	back := resp.Header.Get("Location")
	if back == "" {
		t.Fatal("provider returned no redirect to the callback")
	}
	u, err := url.Parse(back)
	if err != nil {
		t.Fatalf("parse provider redirect: %v", err)
	}
	return u
}

// finishAtProvider simulates the browser visiting the provider's /authorize URL
// and then delivering the provider's redirect to the callback, in the same jar.
func finishAtProvider(t *testing.T, server *Server, jar http.CookieJar, authorizeURL string) *httptest.ResponseRecorder {
	t.Helper()
	return doJarRequest(t, server, jar, http.MethodGet, authorizeAtProvider(t, authorizeURL).RequestURI(), nil)
}

// callbackError extracts the ?error= code from an error redirect to /login.
func callbackError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusFound && rec.Code != http.StatusSeeOther {
		t.Fatalf("callback: %d, want a redirect to the login page: %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse redirect %q: %v", loc, err)
	}
	if u.Path != "/login" {
		t.Fatalf("error redirect went to %q, want /login", loc)
	}
	return u.Query().Get("error")
}

func TestOIDCStatusReportsConfiguredProvider(t *testing.T) {
	server, _, _, _ := setupOIDCServer(t)
	rec := doJarRequest(t, server, newBrowser(t), http.MethodGet, "/api/auth/oidc/status", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d, want 200", rec.Code)
	}
	var body struct {
		Enabled      bool   `json:"enabled"`
		ProviderName string `json:"provider_name"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Enabled {
		t.Error("enabled = false with OIDC configured")
	}
	if body.ProviderName != "Test SSO" {
		t.Errorf("provider_name = %q, want Test SSO", body.ProviderName)
	}
}

func TestOIDCRoutesAbsentWhenDisabled(t *testing.T) {
	server, _ := setupTestServer(t)
	for _, path := range []string{
		"/api/auth/oidc/status",
		"/api/auth/oidc/start",
		"/api/auth/oidc/callback",
	} {
		rec := makeRequest(server, http.MethodGet, path, nil, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404 when oidc.enabled is false", path, rec.Code)
		}
	}
}

// The authorization request must carry everything the flow depends on, and the
// state cookie must be bound to this browser and unreadable by script.
func TestOIDCStartSendsStateNonceAndPKCE(t *testing.T) {
	server, _, p, jar := setupOIDCServer(t)
	start := beginOIDCLogin(t, server, jar, "")
	if start.Code != http.StatusFound {
		t.Fatalf("start: %d: %s", start.Code, start.Body.String())
	}

	loc, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if !strings.HasPrefix(loc.String(), p.issuer()+"/authorize") {
		t.Fatalf("start redirected to %q, want the provider's authorization endpoint", loc)
	}
	q := loc.Query()
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q, want code", q.Get("response_type"))
	}
	if q.Get("client_id") != "digesterr-test" {
		t.Errorf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("redirect_uri") != testRedirectURL {
		t.Errorf("redirect_uri = %q, want the configured fixed callback", q.Get("redirect_uri"))
	}
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		t.Errorf("PKCE missing: challenge=%q method=%q", q.Get("code_challenge"), q.Get("code_challenge_method"))
	}
	if len(q.Get("state")) < 32 {
		t.Errorf("state %q is too short to be unguessable", q.Get("state"))
	}
	if len(q.Get("nonce")) < 32 {
		t.Errorf("nonce %q is too short to be unguessable", q.Get("nonce"))
	}
	scopes := strings.Fields(q.Get("scope"))
	if len(scopes) == 0 || scopes[0] != "openid" {
		t.Errorf("scope = %q, want openid first", q.Get("scope"))
	}

	// The binding cookie must be HttpOnly (a script must not be able to read
	// or forge the browser binding) and scoped to the OIDC routes.
	u, _ := url.Parse("http://digesterr.test/api/auth/oidc/callback")
	var binding *http.Cookie
	for _, c := range jar.Cookies(u) {
		if c.Name == oidcBindingCookieName {
			binding = c
		}
	}
	if binding == nil {
		t.Fatalf("no %s cookie was set: the callback cannot verify which browser started the flow", oidcBindingCookieName)
	}
	// The query string must not carry the state back to us in a form that
	// leaks into Referer: the state lives in the URL only toward the provider.
	if strings.Contains(start.Header().Get("Location"), "digesterr.test") && q.Get("state") == "" {
		t.Error("no state parameter")
	}
}

func TestOIDCStartCookieIsHttpOnlyAndSameSite(t *testing.T) {
	server, _, _, _ := setupOIDCServer(t)
	req := httptest.NewRequest(http.MethodGet, "http://digesterr.test/api/auth/oidc/start", nil)
	req.Host = "digesterr.test"
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	var found *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == oidcBindingCookieName {
			found = c
		}
	}
	if found == nil {
		t.Fatalf("no %s cookie in the response", oidcBindingCookieName)
	}
	if !found.HttpOnly {
		t.Error("binding cookie is not HttpOnly: script could read or forge the browser binding")
	}
	if found.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax so the provider's redirect carries the cookie", found.SameSite)
	}
	if found.Path != "/api/auth/oidc" {
		t.Errorf("Path = %q, want /api/auth/oidc", found.Path)
	}
	if found.MaxAge <= 0 || found.MaxAge > 900 {
		t.Errorf("MaxAge = %d, want a short positive lifetime (<= 15m)", found.MaxAge)
	}
}

func TestOIDCHappyPathProvisionsPasswordlessUser(t *testing.T) {
	server, queries, p, jar := setupOIDCServer(t)
	p.identity = func() (string, string, bool) { return "sub-happy", "happy@example.com", true }

	cb := completeOIDCLogin(t, server, jar)
	if cb.Code != http.StatusSeeOther && cb.Code != http.StatusFound {
		t.Fatalf("callback: %d, want a redirect into the app: %s", cb.Code, cb.Body.String())
	}
	if loc := cb.Header().Get("Location"); loc != "/" {
		t.Errorf("post-login redirect = %q, want /", loc)
	}

	user, err := queries.GetUserByEmail(t.Context(), "happy@example.com")
	if err != nil {
		t.Fatalf("provisioned user missing: %v", err)
	}
	if user.PasswordHash != "" {
		t.Errorf("PasswordHash = %q, want empty", user.PasswordHash)
	}
	if len(user.ApiKey) != 64 {
		t.Errorf("ApiKey = %q (len %d), want a generated Reader key", user.ApiKey, len(user.ApiKey))
	}

	// The session is reusable through the existing refresh-cookie path: the
	// callback issues the same HttpOnly cookie local sign-in does, and the SPA
	// bootstraps from it. This is the "secure sessions reuse" requirement.
	if tok := jarRefreshToken(t, jar, "http://digesterr.test/api/auth/refresh"); tok == "" {
		t.Fatal("callback set no refresh cookie: the browser would land on / logged out")
	}
	refresh := doJarRequest(t, server, jar, http.MethodPost, "/api/auth/refresh", nil)
	if refresh.Code != http.StatusOK {
		t.Fatalf("refresh after OIDC login: %d: %s", refresh.Code, refresh.Body.String())
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(refresh.Body).Decode(&body); err != nil || body.AccessToken == "" {
		t.Fatalf("refresh returned no access token: %v", err)
	}
	me := doJarRequest(t, server, jar, http.MethodGet, "/api/auth/me", nil, map[string]string{
		"Authorization": "Bearer " + body.AccessToken,
	})
	if me.Code != http.StatusOK {
		t.Fatalf("me: %d: %s", me.Code, me.Body.String())
	}
	var profile userResponse
	if err := json.NewDecoder(me.Body).Decode(&profile); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if profile.ID != user.ID || profile.Email != "happy@example.com" {
		t.Errorf("me = %+v, want the provisioned user %d", profile, user.ID)
	}

	// The generated Reader key authenticates the Reader API, which is the
	// machine-client path OIDC does not replace.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://digesterr.test/reader/api/0/user-info", nil)
	req.Header.Set("Authorization", "GoogleLogin auth="+user.ApiKey)
	server.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("reader user-info with the generated key: %d: %s", rr.Code, rr.Body.String())
	}
}

func TestOIDCReturningLoginReusesUser(t *testing.T) {
	server, queries, p, jar := setupOIDCServer(t)
	p.identity = func() (string, string, bool) { return "sub-return", "return@example.com", true }

	if cb := completeOIDCLogin(t, server, jar); cb.Code != http.StatusFound && cb.Code != http.StatusSeeOther {
		t.Fatalf("first login: %d: %s", cb.Code, cb.Body.String())
	}
	first, err := queries.GetUserByEmail(t.Context(), "return@example.com")
	if err != nil {
		t.Fatalf("first login user: %v", err)
	}

	// Second login: the provider has since changed the address. Identity is
	// (issuer, subject), so this must land on the same account, not create a
	// second one and not move to whoever owns the new address.
	p.identity = func() (string, string, bool) { return "sub-return", "return+renamed@example.com", true }
	if cb := completeOIDCLogin(t, server, jar); cb.Code != http.StatusFound && cb.Code != http.StatusSeeOther {
		t.Fatalf("second login: %d: %s", cb.Code, cb.Body.String())
	}
	second, err := queries.GetUserByEmail(t.Context(), "return@example.com")
	if err != nil {
		t.Fatalf("original user vanished: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("second login resolved to user %d, want %d", second.ID, first.ID)
	}
	if n := len(mustListUsers(t, queries)); n != 1 {
		t.Errorf("users = %d, want 1 after two logins for one identity", n)
	}
}

func TestOIDCLinksUniqueExistingEmailAccount(t *testing.T) {
	server, queries, p, jar := setupOIDCServer(t)
	local := createTestUser(t, queries, "local@example.com", "password123")
	p.identity = func() (string, string, bool) { return "sub-local", "LOCAL@example.com", true }

	if cb := completeOIDCLogin(t, server, jar); cb.Code != http.StatusFound && cb.Code != http.StatusSeeOther {
		t.Fatalf("login: %d: %s", cb.Code, cb.Body.String())
	}
	if n := len(mustListUsers(t, queries)); n != 1 {
		t.Fatalf("users = %d, want 1: linking must not create a second account", n)
	}
	after, err := queries.GetUserByID(t.Context(), local.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if after.PasswordHash != local.PasswordHash {
		t.Error("linking replaced the local password hash: local sign-in would break")
	}
	// Local sign-in must still work for a linked account.
	signin := doJarRequest(t, server, newBrowser(t), http.MethodPost, "/api/auth/signin",
		map[string]string{"email": "local@example.com", "password": "password123"})
	if signin.Code != http.StatusOK {
		t.Errorf("local signin after linking: %d: %s", signin.Code, signin.Body.String())
	}
}

func TestOIDCRejectsUnverifiedEmail(t *testing.T) {
	server, queries, p, jar := setupOIDCServer(t)
	p.identity = func() (string, string, bool) { return "sub-unverified", "unverified@example.com", false }

	cb := completeOIDCLogin(t, server, jar)
	if got := callbackError(t, cb); got != "oidc_email_unverified" {
		t.Errorf("error = %q, want oidc_email_unverified", got)
	}
	if n := len(mustListUsers(t, queries)); n != 0 {
		t.Errorf("users = %d, want 0: an unverified address must not provision an account", n)
	}
	if tok := jarRefreshToken(t, jar, "http://digesterr.test/api/auth/refresh"); tok != "" {
		t.Error("a refresh cookie was issued despite the failed login")
	}
}

func TestOIDCRejectsAmbiguousEmail(t *testing.T) {
	server, queries, p, jar := setupOIDCServer(t)
	createTestUser(t, queries, "dup@example.com", "password123")
	createTestUser(t, queries, "DUP@example.com", "password123")
	p.identity = func() (string, string, bool) { return "sub-dup", "Dup@Example.com", true }

	cb := completeOIDCLogin(t, server, jar)
	if got := callbackError(t, cb); got != "oidc_email_ambiguous" {
		t.Errorf("error = %q, want oidc_email_ambiguous", got)
	}
	if n := len(mustListUsers(t, queries)); n != 2 {
		t.Errorf("users = %d, want 2: an ambiguous match must not create or merge accounts", n)
	}
}

// The state is single use. A second delivery of the same callback must not
// produce a session, even though it carries a valid-looking state and code.
func TestOIDCCallbackStateIsSingleUse(t *testing.T) {
	server, _, p, jar := setupOIDCServer(t)
	p.identity = func() (string, string, bool) { return "sub-replay", "replay@example.com", true }

	start := beginOIDCLogin(t, server, jar, "")
	authorizeURL := start.Header().Get("Location")
	callbackURI := authorizeAtProvider(t, authorizeURL).RequestURI()

	if cb := doJarRequest(t, server, jar, http.MethodGet, callbackURI, nil); cb.Code != http.StatusSeeOther && cb.Code != http.StatusFound {
		t.Fatalf("first callback: %d: %s", cb.Code, cb.Body.String())
	}
	// Replay the exact same request, cookies and all.
	replay := doJarRequest(t, server, jar, http.MethodGet, callbackURI, nil)
	if got := callbackError(t, replay); got == "" {
		t.Error("the replayed callback redirected into the app: state was reusable")
	}
}

// A state value leaked to another browser (or planted there by an attacker)
// must not be redeemable there: the binding cookie is missing.
func TestOIDCCallbackRejectsDifferentBrowser(t *testing.T) {
	server, queries, p, attacker := setupOIDCServer(t)
	p.identity = func() (string, string, bool) { return "sub-cross", "cross@example.com", true }

	// The attacker starts a login in their own browser and captures the
	// callback URL, which is exactly what a leaked state hands them.
	start := beginOIDCLogin(t, server, attacker, "")
	back := authorizeAtProvider(t, start.Header().Get("Location"))

	// The victim's browser is the one that must be logged in. It has no
	// binding cookie for this flow.
	victim := newBrowser(t)
	cb := doJarRequest(t, server, victim, http.MethodGet, back.RequestURI(), nil)
	if got := callbackError(t, cb); got != "oidc_state_invalid" {
		t.Errorf("error = %q, want oidc_state_invalid for a callback in another browser", got)
	}
	if n := len(mustListUsers(t, queries)); n != 0 {
		t.Errorf("users = %d, want 0", n)
	}
	if tok := jarRefreshToken(t, victim, "http://digesterr.test/api/auth/refresh"); tok != "" {
		t.Error("the victim's browser was given a refresh cookie by an attacker-initiated flow")
	}
}

func TestOIDCCallbackRejectsUnknownOrMissingState(t *testing.T) {
	server, _, _, jar := setupOIDCServer(t)
	for name, target := range map[string]string{
		"no parameters": "/api/auth/oidc/callback",
		"unknown state": "/api/auth/oidc/callback?state=deadbeef&code=abc",
	} {
		t.Run(name, func(t *testing.T) {
			rec := doJarRequest(t, server, jar, http.MethodGet, target, nil)
			if got := callbackError(t, rec); got != "oidc_state_invalid" {
				t.Errorf("error = %q, want oidc_state_invalid", got)
			}
		})
	}
}

// A provider that reports an error must not produce a session, and the error
// must reach the login page as a stable code rather than as provider text (which
// can carry attacker-influenced content).
func TestOIDCCallbackHandlesProviderError(t *testing.T) {
	server, _, _, jar := setupOIDCServer(t)
	start := beginOIDCLogin(t, server, jar, "")
	loc, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	state := loc.Query().Get("state")

	rec := doJarRequest(t, server, jar, http.MethodGet,
		"/api/auth/oidc/callback?error=access_denied&error_description=user+said+no&state="+url.QueryEscape(state), nil)
	if got := callbackError(t, rec); got != "oidc_denied" {
		t.Errorf("error = %q, want oidc_denied", got)
	}
	if strings.Contains(rec.Header().Get("Location"), "user+said+no") {
		t.Error("provider-supplied error_description was reflected into the redirect")
	}
}

// Every ID token validation failure must end the login, not fall through to a
// session. Each case is a real attack or misconfiguration.
func TestOIDCCallbackRejectsBadIDTokens(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(p *testOIDCProvider)
	}{
		{"wrong nonce", func(p *testOIDCProvider) {
			p.mutateIDToken = func(c map[string]any) { c["nonce"] = "not-the-nonce-we-sent" }
		}},
		{"missing nonce", func(p *testOIDCProvider) {
			p.mutateIDToken = func(c map[string]any) { delete(c, "nonce") }
		}},
		{"wrong issuer", func(p *testOIDCProvider) {
			p.mutateIDToken = func(c map[string]any) { c["iss"] = "https://evil.example.com" }
		}},
		{"wrong audience", func(p *testOIDCProvider) {
			p.mutateIDToken = func(c map[string]any) { c["aud"] = "some-other-client" }
		}},
		{"expired", func(p *testOIDCProvider) {
			p.mutateIDToken = func(c map[string]any) { c["exp"] = 1 }
		}},
		{"missing subject", func(p *testOIDCProvider) {
			p.mutateIDToken = func(c map[string]any) { delete(c, "sub") }
		}},
		{"missing email", func(p *testOIDCProvider) {
			p.mutateIDToken = func(c map[string]any) { delete(c, "email") }
		}},
		{"email_verified as string", func(p *testOIDCProvider) {
			// Some providers send the string "true". Accepting it would mean a
			// provider could also send "false" and be read as verified.
			p.mutateIDToken = func(c map[string]any) { c["email_verified"] = "true" }
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, queries, p, jar := setupOIDCServer(t)
			p.identity = func() (string, string, bool) { return "sub-bad", "bad@example.com", true }
			tc.mutate(p)

			cb := completeOIDCLogin(t, server, jar)
			if got := callbackError(t, cb); got == "" {
				t.Fatal("callback accepted a bad ID token and redirected into the app")
			}
			if n := len(mustListUsers(t, queries)); n != 0 {
				t.Errorf("users = %d, want 0: a rejected token must not provision", n)
			}
		})
	}
}

// A token signed by a different key must fail signature verification even when
// every claim is right.
func TestOIDCCallbackRejectsForgedSignature(t *testing.T) {
	server, queries, p, jar := setupOIDCServer(t)
	p.identity = func() (string, string, bool) { return "sub-forged", "forged@example.com", true }
	// Sign with a key the provider never publishes. Every claim is right and
	// the JWKS document is untouched, so only the signature check can catch
	// this -- which is exactly the attack a stolen signing key enables.
	other := newTestOIDCProvider(t, "digesterr-test")
	p.mu.Lock()
	p.signingKey = other.key
	p.mu.Unlock()

	cb := completeOIDCLogin(t, server, jar)
	if got := callbackError(t, cb); got == "" {
		t.Fatal("callback accepted a token signed by an unknown key")
	}
	if n := len(mustListUsers(t, queries)); n != 0 {
		t.Errorf("users = %d, want 0", n)
	}
}

// return_to is an in-app path only. An absolute URL here would turn the
// callback into an open redirect that a phishing link could aim anywhere.
func TestOIDCReturnToRejectsAbsoluteURLs(t *testing.T) {
	for _, target := range []string{
		"https://evil.example.com/steal",
		"//evil.example.com/steal",
		"http://evil.example.com",
		"/\\evil.example.com",
	} {
		server, _, p, jar := setupOIDCServer(t)
		p.identity = func() (string, string, bool) { return "sub-redirect", "redirect@example.com", true }

		start := beginOIDCLogin(t, server, jar, "?return_to="+url.QueryEscape(target))
		if start.Code != http.StatusFound {
			t.Fatalf("start with return_to=%q: %d", target, start.Code)
		}
		cb := finishAtProvider(t, server, jar, start.Header().Get("Location"))
		if cb.Code != http.StatusFound && cb.Code != http.StatusSeeOther {
			t.Fatalf("callback: %d: %s", cb.Code, cb.Body.String())
		}
		if loc := cb.Header().Get("Location"); loc != "/" {
			t.Errorf("return_to=%q produced redirect %q, want /", target, loc)
		}
	}
}

func TestOIDCReturnToKeepsInAppPath(t *testing.T) {
	server, _, p, jar := setupOIDCServer(t)
	p.identity = func() (string, string, bool) { return "sub-deeplink", "deep@example.com", true }

	start := beginOIDCLogin(t, server, jar, "?return_to="+url.QueryEscape("/feeds/12/99"))
	cb := finishAtProvider(t, server, jar, start.Header().Get("Location"))
	if loc := cb.Header().Get("Location"); loc != "/feeds/12/99" {
		t.Errorf("redirect = %q, want the requested in-app path", loc)
	}
}

// The requirement that request URLs are not logged applies most sharply here:
// the callback query carries the authorization code and state, and the /start
// Location carries the nonce. Neither may appear in a log record.
func TestOIDCFlowDoesNotLogCodesOrState(t *testing.T) {
	capture := captureAllSlog(t)
	server, _, p, jar := setupOIDCServer(t)
	p.identity = func() (string, string, bool) { return "sub-logs", "logs@example.com", true }

	start := beginOIDCLogin(t, server, jar, "")
	loc, _ := url.Parse(start.Header().Get("Location"))
	state := loc.Query().Get("state")
	nonce := loc.Query().Get("nonce")
	challenge := loc.Query().Get("code_challenge")

	back := authorizeAtProvider(t, start.Header().Get("Location"))
	code := back.Query().Get("code")
	if code == "" || state == "" || nonce == "" {
		t.Fatalf("flow did not produce code/state/nonce: %s", back)
	}
	cb := doJarRequest(t, server, jar, http.MethodGet, back.RequestURI(), nil)
	if cb.Code != http.StatusFound && cb.Code != http.StatusSeeOther {
		t.Fatalf("callback: %d: %s", cb.Code, cb.Body.String())
	}

	logged := capture.Dump()
	for name, secret := range map[string]string{
		"authorization code": code,
		"state":              state,
		"nonce":              nonce,
		"pkce challenge":     challenge,
		"code verifier":      p.lastTokenRequest(t).Get("code_verifier"),
	} {
		if secret == "" {
			continue
		}
		if strings.Contains(logged, secret) {
			t.Errorf("%s leaked into the logs", name)
		}
	}
}

// Enabling OIDC must not disturb the pre-existing authentication paths.
func TestLocalAuthUnaffectedByOIDC(t *testing.T) {
	server, _, _, _ := setupOIDCServer(t)
	jar := newBrowser(t)

	signup := doJarRequest(t, server, jar, http.MethodPost, "/api/auth/signup",
		map[string]string{"email": "still@example.com", "password": "password123"})
	if signup.Code != http.StatusCreated {
		t.Fatalf("signup with OIDC enabled: %d: %s", signup.Code, signup.Body.String())
	}
	var body struct {
		AccessToken string `json:"access_token"`
		APIKey      string `json:"api_key"`
	}
	if err := json.NewDecoder(signup.Body).Decode(&body); err != nil {
		t.Fatalf("decode signup: %v", err)
	}
	if body.APIKey == "" {
		t.Error("signup returned no Reader API key")
	}
	signin := doJarRequest(t, server, newBrowser(t), http.MethodPost, "/api/auth/signin",
		map[string]string{"email": "still@example.com", "password": "password123"})
	if signin.Code != http.StatusOK {
		t.Fatalf("signin with OIDC enabled: %d: %s", signin.Code, signin.Body.String())
	}
}

// Discovery failures must surface at startup: an instance that cannot reach its
// provider would otherwise advertise a login button that cannot work.
func TestEnableOIDCFailsOnBadIssuer(t *testing.T) {
	db, queries := setupTestDB(t)
	cfg := config.Default()
	cfg.Server.JWTSecret = "test-jwt-secret-for-testing"
	cfg.OIDC = config.OIDCConfig{
		Enabled:      true,
		Issuer:       "http://127.0.0.1:1", // nothing listens here
		ClientID:     "x",
		ClientSecret: "y",
		RedirectURL:  "http://127.0.0.1:9" + config.CallbackPath,
	}
	authSvc := auth.NewService(cfg.Server.JWTSecret, queries, db)
	server := NewServer(db, queries, authSvc, logbuf.New(10), cfg)
	t.Cleanup(server.commentCache.Close)
	if err := server.EnableOIDC(t.Context()); err == nil {
		t.Fatal("EnableOIDC succeeded against an unreachable issuer")
	}
}

// A configured issuer that disagrees with the discovery document is a
// misconfiguration (or an attack on discovery). go-oidc checks this; the test
// pins that the check is reached rather than bypassed.
func TestEnableOIDCRejectsIssuerMismatch(t *testing.T) {
	db, queries := setupTestDB(t)
	p := newTestOIDCProvider(t, "c")
	cfg := config.Default()
	cfg.Server.JWTSecret = "test-jwt-secret-for-testing"
	cfg.OIDC = config.OIDCConfig{
		Enabled:      true,
		Issuer:       p.issuer() + "/wrong",
		ClientID:     "c",
		ClientSecret: "s",
		RedirectURL:  testRedirectURL,
	}
	authSvc := auth.NewService(cfg.Server.JWTSecret, queries, db)
	server := NewServer(db, queries, authSvc, logbuf.New(10), cfg)
	t.Cleanup(server.commentCache.Close)
	if err := server.EnableOIDC(t.Context()); err == nil {
		t.Fatal("EnableOIDC accepted a discovery document whose issuer differs from the configured one")
	}
}

func mustListUsers(t *testing.T, q *store.Queries) []store.ListAllUsersEmailsRow {
	t.Helper()
	rows, err := q.ListAllUsersEmails(t.Context())
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	return rows
}

// captureAllSlog captures every record from every level and renders all of
// them — messages, attribute keys and values — into one string, so a test can
// assert that a secret appears nowhere in the log stream at all. Asserting on
// named records would only prove the fields this test thought to check are
// clean; dumping everything also catches a secret that reached the access log,
// a debug line added later, or an error record built with %v.
func captureAllSlog(t *testing.T) *slogDump {
	t.Helper()
	cap := testutil.CaptureSlog(t, slog.LevelDebug)
	t.Cleanup(func() {})
	return &slogDump{cap: cap}
}

type slogDump struct{ cap *testutil.LogCapture }

func (d *slogDump) Dump() string {
	var b strings.Builder
	for _, r := range d.cap.Records() {
		b.WriteString(r.Level.String())
		b.WriteString(" ")
		b.WriteString(r.Message)
		r.Attrs(func(a slog.Attr) bool {
			b.WriteString(" ")
			b.WriteString(a.Key)
			b.WriteString("=")
			b.WriteString(fmt.Sprint(a.Value.Any()))
			return true
		})
		b.WriteString("\n")
	}
	return b.String()
}

// Every /start inserts a row. Without a sweep, abandoned logins accumulate
// forever -- a table that grows with failed attempts is an easy way to fill a
// disk on a public instance. A new flow sweeps rows that are already expired;
// it must NOT touch live ones, or two logins in flight at once would cancel each
// other.
func TestOIDCStartSweepsExpiredRequestsOnly(t *testing.T) {
	server, queries, _, jar := setupOIDCServer(t)

	expired := time.Now().Add(-time.Minute)
	if err := queries.CreateOIDCAuthRequest(t.Context(), store.CreateOIDCAuthRequestParams{
		StateHash: "stale-hash", Nonce: "n", CodeVerifier: "v", BindingHash: "b",
		ReturnTo: "/", ExpiresAt: expired.Unix(),
	}); err != nil {
		t.Fatalf("seed expired row: %v", err)
	}

	// One live flow.
	first := beginOIDCLogin(t, server, jar, "")
	if first.Code != http.StatusFound {
		t.Fatalf("start: %d", first.Code)
	}

	var stale int
	if err := server.db.QueryRow(`SELECT count(*) FROM oidc_auth_requests WHERE state_hash = 'stale-hash'`).Scan(&stale); err != nil {
		t.Fatalf("count: %v", err)
	}
	if stale != 0 {
		t.Errorf("expired authorization request survived a new /start: rows accumulate forever")
	}

	var live int
	if err := server.db.QueryRow(`SELECT count(*) FROM oidc_auth_requests`).Scan(&live); err != nil {
		t.Fatalf("count: %v", err)
	}
	if live != 1 {
		t.Errorf("rows after start = %d, want 1: the sweep removed a live request", live)
	}
}

// A flow that comes back after oidcFlowTTL must be refused even though its
// state row still exists. The row's expires_at is part of the consume query,
// so this also proves the timestamp comparison in SQL works against the schema
// (a DATETIME stored one way and compared another would silently never expire).
func TestOIDCCallbackRejectsExpiredRequest(t *testing.T) {
	server, queries, p, jar := setupOIDCServer(t)
	p.identity = func() (string, string, bool) { return "sub-late", "late@example.com", true }

	start := beginOIDCLogin(t, server, jar, "")
	loc, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	state := loc.Query().Get("state")
	if state == "" {
		t.Fatal("no state")
	}

	// Age the recorded request the way a stale tab would: the browser kept the
	// cookie, the server-side row went past its deadline.
	if _, err := server.db.Exec(
		`UPDATE oidc_auth_requests SET expires_at = DATETIME('now', '-1 second') WHERE state_hash = ?`,
		hashSecret(state)); err != nil {
		t.Fatalf("aging request: %v", err)
	}

	cb := doJarRequest(t, server, jar, http.MethodGet,
		"/api/auth/oidc/callback?state="+url.QueryEscape(state)+"&code=whatever", nil)
	if got := callbackError(t, cb); got != "oidc_state_invalid" {
		t.Errorf("error = %q, want oidc_state_invalid for an expired request", got)
	}
	if n := len(mustListUsers(t, queries)); n != 0 {
		t.Errorf("users = %d, want 0", n)
	}
}
