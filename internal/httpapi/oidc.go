package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"digesterr/internal/auth"
	"digesterr/internal/logging"
	oidcsvc "digesterr/internal/oidc"
	"digesterr/internal/store"
)

// Native OIDC sign-in.
//
// Shape of the flow, and the decisions behind it:
//
//   - Authorization code with PKCE (S256), exchanged server-side. The client
//     secret never leaves the process, and PKCE still binds the code to the
//     request that started it.
//   - state, nonce and the PKCE verifier are generated per request. state is
//     single-use: it is stored hashed, deleted by the callback that consumes
//     it, and a second use finds nothing.
//   - state is additionally bound to the initiating browser by a random value
//     in an HttpOnly SameSite=Lax cookie scoped to /api/auth/oidc. A callback
//     arriving in a different browser fails, so a leaked state is not enough.
//   - The redirect URI is the configured fixed value. It is never derived from
//     the request Host header.
//   - The resulting session is the SAME session local sign-in creates: an
//     access token plus the HttpOnly refresh cookie, so the router guard,
//     silent refresh and signout all behave identically.
//
// Nothing here logs a URL, a query string, a code, a state or a token. The
// request logger writes r.URL.Path only (see ServeHTTP), and these handlers add
// no URL-shaped attributes of their own.

const (
	// oidcBindingCookieName carries the browser binding for one in-flight
	// authorization request.
	oidcBindingCookieName = "oidc_flow"
	// oidcBindingCookiePath scopes the binding cookie to the OIDC routes, so
	// it does not travel with ordinary API calls.
	oidcBindingCookiePath = "/api/auth/oidc"
	// oidcFlowTTL bounds how long a started login may take to come back. It is
	// also the binding cookie's lifetime, so the browser forgets the flow at
	// the same moment the server does.
	oidcFlowTTL = 10 * time.Minute
	// oidcStateBytes is the entropy behind a state value.
	oidcStateBytes = 32
)

// oidcHandler holds everything the three OIDC endpoints need. It exists only
// when OIDC is configured and discovery succeeded; when the feature is off, no
// routes are registered at all, so the endpoints 404 rather than answering
// "disabled" (a 404 does not advertise an authentication mechanism that is not
// configured).
type oidcHandler struct {
	server   *Server
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config
	svc      *oidcsvc.Service
	scopes   []string
	// providerName labels the sign-in button.
	providerName string
}

// EnableOIDC performs provider discovery and installs the OIDC routes. It
// returns an error when the configured issuer cannot be reached or when the
// discovery document's issuer does not match, so a misconfigured instance fails
// at startup instead of serving a broken login button.
//
// Calling it on a server whose config has OIDC disabled is a no-op, not an
// error: a caller can wire it unconditionally.
func (s *Server) EnableOIDC(ctx context.Context) error {
	cfg := s.oidcConfig
	if !cfg.Enabled {
		return nil
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	// A bounded context for discovery: an unreachable provider must not stall
	// startup indefinitely.
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	provider, err := oidc.NewProvider(fetchCtx, cfg.Issuer)
	if err != nil {
		return fmt.Errorf("oidc: discovery for issuer %q failed: %w", cfg.Issuer, err)
	}

	h := &oidcHandler{
		server:   s,
		provider: provider,
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		oauth: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  cfg.RedirectURL,
			Scopes:       cfg.EffectiveScopes(),
		},
		svc:          oidcsvc.NewService(s.db, s.queries, cfg.Issuer, auth.GenerateAPIKey),
		scopes:       cfg.EffectiveScopes(),
		providerName: cfg.ProviderName,
	}
	if h.providerName == "" {
		h.providerName = "single sign-on"
	}

	mux := s.mux
	mux.HandleFunc("GET /api/auth/oidc/status", h.handleStatus)
	mux.HandleFunc("GET /api/auth/oidc/start", h.handleStart)
	mux.HandleFunc("GET /api/auth/oidc/callback", h.handleCallback)

	slog.Info("OIDC sign-in enabled", logging.CategoryAttr(logging.CategoryAuth),
		"issuer", cfg.Issuer, "provider", h.providerName)
	return nil
}

func (h *oidcHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	// Unauthenticated by design: the login page has to know whether to draw
	// the button before anyone is signed in. It exposes only the label.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":       true,
		"provider_name": h.providerName,
	})
}

// handleStart begins a login: it mints state/nonce/PKCE, records them, sets the
// browser binding cookie, and redirects to the provider.
func (h *oidcHandler) handleStart(w http.ResponseWriter, r *http.Request) {
	state, err := randToken(oidcStateBytes)
	if err != nil {
		writeServerError(w, "failed to start sign-in", err)
		return
	}
	nonce, err := randToken(oidcStateBytes)
	if err != nil {
		writeServerError(w, "failed to start sign-in", err)
		return
	}
	binding, err := randToken(oidcStateBytes)
	if err != nil {
		writeServerError(w, "failed to start sign-in", err)
		return
	}
	verifier := oauth2.GenerateVerifier()

	returnTo := safeReturnTo(r.URL.Query().Get("return_to"))

	// Sweep abandoned requests before inserting this one. Every /start writes a
	// row, so without this the table grows with failed and abandoned attempts
	// forever. Only rows already past expires_at are removed, so concurrent
	// logins do not cancel each other. A sweep failure is not fatal: the login
	// itself is still valid, and a full disk is a separate problem.
	if err := h.server.queries.DeleteExpiredOIDCAuthRequests(r.Context()); err != nil {
		slog.Debug("OIDC: expired authorization request sweep failed", logging.CategoryAttr(logging.CategoryAuth), "error", err)
	}

	if err := h.server.queries.CreateOIDCAuthRequest(r.Context(), store.CreateOIDCAuthRequestParams{
		StateHash:    hashSecret(state),
		Nonce:        nonce,
		CodeVerifier: verifier,
		BindingHash:  hashSecret(binding),
		ReturnTo:     returnTo,
		// Unix seconds: the column is INTEGER, so this cannot be compared as
		// text (see internal/schema/schema.sql).
		ExpiresAt: time.Now().Add(oidcFlowTTL).Unix(),
	}); err != nil {
		writeServerError(w, "failed to start sign-in", err)
		return
	}

	// The cookie is HttpOnly (script cannot read or forge the binding) and
	// SameSite=Lax (the provider's redirect is a top-level GET, which Lax
	// permits while still withholding the cookie from cross-site subresource
	// requests). Secure follows the same rule as the refresh cookie so a
	// plain-HTTP LAN install keeps working.
	secure := r.TLS != nil
	if h.server.cookieSecure != nil {
		secure = *h.server.cookieSecure
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oidcBindingCookieName,
		Value:    binding,
		Path:     oidcBindingCookiePath,
		MaxAge:   int(oidcFlowTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})

	// No-store on the redirect: the Location header carries state and nonce,
	// and an intermediary caching this response would hand the same values to
	// the next browser.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, h.oauth.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(verifier),
	), http.StatusFound)
}

// handleCallback consumes the authorization request and completes the login.
//
// Every failure redirects to /login with a stable error code. Nothing from the
// provider (its error_description, its token endpoint body) is reflected into
// the redirect, because that text is attacker-influenced in the cases that
// matter.
func (h *oidcHandler) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// The flow cookie is single-use too: clear it on every outcome, so a
	// failed attempt cannot be retried against the same binding.
	h.clearBindingCookie(w, r)

	state := q.Get("state")
	if state == "" {
		h.fail(w, r, "oidc_state_invalid", errors.New("callback without state"))
		return
	}

	// Consume the request row BEFORE looking at the provider's error, so a
	// denied or failed authorization cannot be retried with the same state.
	req, err := h.server.queries.ConsumeOIDCAuthRequest(r.Context(), hashSecret(state))
	if err != nil {
		h.fail(w, r, "oidc_state_invalid", fmt.Errorf("consuming authorization request: %w", err))
		return
	}

	// Browser binding: the cookie must be present and must hash to the value
	// recorded when the flow started.
	cookie, err := r.Cookie(oidcBindingCookieName)
	if err != nil || cookie.Value == "" {
		h.fail(w, r, "oidc_state_invalid", errors.New("callback without a flow binding cookie"))
		return
	}
	if subtle.ConstantTimeCompare([]byte(hashSecret(cookie.Value)), []byte(req.BindingHash)) != 1 {
		h.fail(w, r, "oidc_state_invalid", errors.New("flow binding cookie does not match"))
		return
	}

	if e := q.Get("error"); e != "" {
		// The provider's own error code is logged (it is a small fixed
		// vocabulary in practice) but never forwarded to the browser verbatim.
		h.fail(w, r, "oidc_denied", fmt.Errorf("provider returned error %q", e))
		return
	}
	code := q.Get("code")
	if code == "" {
		h.fail(w, r, "oidc_state_invalid", errors.New("callback without an authorization code"))
		return
	}

	// Exchange the code server-side. The verifier comes from the row that was
	// just consumed, so the exchange is bound to this request's challenge.
	token, err := h.oauth.Exchange(r.Context(), code, oauth2.VerifierOption(req.CodeVerifier))
	if err != nil {
		h.fail(w, r, "oidc_exchange_failed", fmt.Errorf("code exchange: %w", err))
		return
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		h.fail(w, r, "oidc_invalid_token", errors.New("token response carried no id_token"))
		return
	}

	// Signature, issuer, audience and expiry are all verified by go-oidc
	// against the provider's published keys.
	idToken, err := h.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		h.fail(w, r, "oidc_invalid_token", fmt.Errorf("verifying id_token: %w", err))
		return
	}
	// Nonce: binds this ID token to the authorization request we started, so a
	// token minted for another request cannot be replayed here.
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(req.Nonce)) != 1 {
		h.fail(w, r, "oidc_invalid_token", errors.New("id_token nonce does not match the request"))
		return
	}

	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := idToken.Claims(&claims); err != nil {
		h.fail(w, r, "oidc_invalid_token", fmt.Errorf("decoding claims: %w", err))
		return
	}

	identity := oidcsvc.Identity{
		Issuer:        idToken.Issuer,
		Subject:       idToken.Subject,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
	}

	user, provisioned, err := h.svc.Resolve(r.Context(), identity)
	if err != nil {
		switch {
		case errors.Is(err, oidcsvc.ErrEmailNotVerified):
			h.fail(w, r, "oidc_email_unverified", err)
		case errors.Is(err, oidcsvc.ErrEmailMissing):
			h.fail(w, r, "oidc_email_missing", err)
		case errors.Is(err, oidcsvc.ErrEmailAmbiguous):
			h.fail(w, r, "oidc_email_ambiguous", err)
		case errors.Is(err, oidcsvc.ErrSubjectMissing):
			h.fail(w, r, "oidc_invalid_token", err)
		default:
			h.fail(w, r, "oidc_error", err)
		}
		return
	}
	if provisioned {
		slog.Info("OIDC first login provisioned an account", logging.CategoryAttr(logging.CategoryAuth),
			"user_id", user.ID)
	}

	// Same session as local sign-in: access token in the body-less response is
	// not possible on a redirect, so the SPA picks the session up through the
	// refresh cookie exactly as it does after a reload.
	refreshToken, err := h.server.auth.CreateRefreshToken(r.Context(), user.ID)
	if err != nil {
		writeServerError(w, "failed to create session", err)
		return
	}
	http.SetCookie(w, h.server.refreshCookie(r, refreshToken, int(auth.RefreshTokenDuration.Seconds())))

	slog.Info("OIDC sign-in succeeded", logging.CategoryAttr(logging.CategoryAuth), "user_id", user.ID)

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, req.ReturnTo, http.StatusSeeOther)
}

// fail redirects to the login page with a stable error code and logs the
// underlying cause once, server-side.
func (h *oidcHandler) fail(w http.ResponseWriter, r *http.Request, code string, cause error) {
	// user_id and the cause, never the request URL: the query string here
	// carries the authorization code and state.
	slog.Warn("OIDC sign-in failed", logging.CategoryAttr(logging.CategoryAuth),
		"reason", code, "error", cause)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, "/login?error="+url.QueryEscape(code), http.StatusFound)
}

func (h *oidcHandler) clearBindingCookie(w http.ResponseWriter, r *http.Request) {
	secure := r.TLS != nil
	if h.server.cookieSecure != nil {
		secure = *h.server.cookieSecure
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oidcBindingCookieName,
		Value:    "",
		Path:     oidcBindingCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// safeReturnTo restricts a post-login destination to a same-origin absolute
// path. Anything else — a scheme, a protocol-relative //host, a backslash trick
// browsers normalise into //, an encoded newline — falls back to "/".
//
// An absolute URL here would make the callback an open redirect, which is a
// phishing primitive: the link starts on the real domain and ends anywhere.
func safeReturnTo(raw string) string {
	if raw == "" {
		return "/"
	}
	// Reject anything with a scheme or authority outright rather than trying to
	// sanitise it.
	if strings.Contains(raw, "://") {
		return "/"
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "/"
	}
	// A backslash is treated as "/" by several browsers, so "/\evil.com" would
	// become protocol-relative.
	if strings.ContainsAny(raw, "\\\r\n\t") {
		return "/"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	// The login page itself is not a useful destination after a login.
	if u.Path == "/login" {
		return "/"
	}
	return u.RequestURI()
}

// randToken returns 32 random bytes, base64url-encoded (unpadded).
func randToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashSecret hashes a state or binding value for storage. The values are
// high-entropy random tokens, not passwords, so a plain SHA-256 is the right
// tool: no stretching is needed and the database never holds a usable value.
func hashSecret(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}
