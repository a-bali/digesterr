package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mustJSON encodes v for a test request body.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return b
}

// A JSON-body refresh client must be able to REVOKE its token.
//
// /api/auth/refresh accepts the refresh token either as a cookie or in a JSON
// body, so a non-browser client (a script, a mobile app, curl) can authenticate
// with a body token and never touch a cookie. /api/auth/signout looked ONLY at
// the cookie: that client's signout returned 200 "signed out" while the row
// stayed in refresh_tokens and the token kept minting access tokens for its
// full seven-day lifetime. The 200 made the failure silent.
//
// Revocation is asserted through ValidateRefreshToken rather than by attempting
// a refresh, deliberately: /api/auth/refresh ROTATES the token (it deletes the
// old row and issues a new one), so a probe-refresh would revoke the very token
// under test and the assertion would then pass whether or not signout worked.
// That is exactly how the first version of this test came out green against
// code with the body branch removed.
func TestSignoutRevokesBodyRefreshToken(t *testing.T) {
	server, queries := setupTestServer(t)
	createTestUser(t, queries, "bodyrevoke@example.com", "password123")

	// Sign in WITHOUT a jar, so we hold the token as a literal value. The
	// refresh token is issued as a Set-Cookie, which is how a non-browser client
	// legitimately obtains it: read the header, keep the value, send it in the
	// JSON body from then on.
	w := makeRequest(server, http.MethodPost, "/api/auth/signin", map[string]string{
		"email":    "bodyrevoke@example.com",
		"password": "password123",
	}, "")
	if w.Code != http.StatusOK {
		t.Fatalf("signin: %d: %s", w.Code, w.Body.String())
	}
	var signin struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(w.Body).Decode(&signin); err != nil {
		t.Fatalf("decode signin: %v", err)
	}
	refreshToken := refreshTokenFromSetCookie(w)
	if refreshToken == "" {
		t.Fatalf("no refresh_token cookie in the signin response, so there is no token "+
			"to revoke: %s", w.Header().Get("Set-Cookie"))
	}

	// Sanity: the token is live before signout. Without this the assertion after
	// signout could pass for the wrong reason (a token that was never valid).
	ctx := context.Background()
	if _, err := server.auth.ValidateRefreshToken(ctx, refreshToken); err != nil {
		t.Fatalf("refresh token is not valid before signout: %v (fixture is wrong, not the fix)", err)
	}

	// Sign out presenting the token in the BODY and no cookie at all.
	w = makeRequest(server, http.MethodPost, "/api/auth/signout", map[string]string{
		"refresh_token": refreshToken,
	}, signin.AccessToken)
	if w.Code != http.StatusOK {
		t.Fatalf("signout: %d: %s", w.Code, w.Body.String())
	}

	// The token must now be dead. Before the fix the handler read only the
	// cookie, found none, and still answered 200 — so this is the assertion that
	// fails against the bug.
	if _, err := server.auth.ValidateRefreshToken(ctx, refreshToken); err == nil {
		t.Errorf("signout with a JSON-body token did not revoke it: the token still validates "+
			"after a 200 'signed out', so it keeps minting access tokens for its full "+
			"lifetime. Got token %s…", refreshToken[:8])
	}
}

// TestSignoutWithoutAnyTokenStillSucceeds guards the idempotency the original
// handler had: signing out with no session is not an error, and the cookie is
// cleared regardless. Widening the handler to read the body must not turn a
// no-op signout into a failure.
func TestSignoutWithoutAnyTokenStillSucceeds(t *testing.T) {
	server, queries := setupTestServer(t)
	_, token := authedTestUser(t, server, queries, "notoken@example.com")

	w := makeRequest(server, http.MethodPost, "/api/auth/signout", nil, token)
	if w.Code != http.StatusOK {
		t.Errorf("signout with no refresh token at all: %d, want 200 (it is idempotent): %s",
			w.Code, w.Body.String())
	}
}

// TestSignoutPrefersCookieOverBody pins the precedence: when both are present
// the cookie wins, which is what a browser sends. A body value must not be able
// to redirect the revocation onto a different token than the one the session
// actually holds.
func TestSignoutPrefersCookieOverBody(t *testing.T) {
	server, queries := setupTestServer(t)
	createTestUser(t, queries, "precedence@example.com", "password123")
	ctx := context.Background()

	signin := func() (string, string) {
		w := makeRequest(server, http.MethodPost, "/api/auth/signin", map[string]string{
			"email":    "precedence@example.com",
			"password": "password123",
		}, "")
		if w.Code != http.StatusOK {
			t.Fatalf("signin: %d: %s", w.Code, w.Body.String())
		}
		var body struct {
			AccessToken string `json:"access_token"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("decode signin: %v", err)
		}
		return refreshTokenFromSetCookie(w), body.AccessToken
	}
	cookieTok, accessTok := signin() // the session the browser holds
	bodyTok, _ := signin()           // an unrelated token supplied in the body

	req := httptest.NewRequest(http.MethodPost, "/api/auth/signout", bytes.NewReader(mustJSON(t, map[string]string{"refresh_token": bodyTok})))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessTok)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: cookieTok})
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("signout: %d: %s", w.Code, w.Body.String())
	}

	if _, err := server.auth.ValidateRefreshToken(ctx, cookieTok); err == nil {
		t.Error("the cookie's token was not revoked: the body value took precedence")
	}
	if _, err := server.auth.ValidateRefreshToken(ctx, bodyTok); err != nil {
		t.Error("the body's token was revoked even though a cookie was present: " +
			"a request must not be able to revoke a token it does not hold as its session")
	}
}

// refreshTokenFromSetCookie extracts the refresh-token value a JSON-body client
// would keep after signin. Such a client has no cookie jar, so the Set-Cookie
// header is the only place the credential is handed over.
func refreshTokenFromSetCookie(w *httptest.ResponseRecorder) string {
	for _, c := range w.Result().Cookies() {
		if c.Name == "refresh_token" {
			return c.Value
		}
	}
	return ""
}
