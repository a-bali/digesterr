package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"digesterr/internal/auth"
	"digesterr/internal/logging"
	"digesterr/internal/store"
)

// generateAPIKey returns a random 32-byte key hex-encoded (64 lowercase hex chars).
func generateAPIKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// userResponse is the JSON-safe view of a user. store.User carries the
// bcrypt hash and Reader API key, which must never be serialized.
type userResponse struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func newUserResponse(u store.User) userResponse {
	return userResponse{ID: u.ID, Email: u.Email, CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt}
}

// refreshCookie builds the refresh-token cookie scoped to /api/auth.
//
// The path must cover every endpoint that has to READ the token, not just the
// one that refreshes it. It used to be /api/auth/refresh, which meant a browser
// never sent the cookie to /api/auth/signout: the handler's r.Cookie lookup
// failed, the database row was never deleted, and the token stayed usable for
// its full seven-day lifetime via the refresh endpoint's JSON-body fallback.
// Scoping to /api/auth keeps the credential off the rest of the API (it is the
// one cookie the SPA cannot read, so it should travel as little as possible)
// while making revocation work. Widening to "/" would also work but would
// attach the token to every request for no reason.
//
// Secure defaults to the request's TLS state, which is correct when the app
// terminates TLS itself but silently wrong behind a TLS-terminating proxy: the
// request the app sees is plain HTTP, so the cookie goes out without Secure over
// an HTTPS-only deployment. server.cookie_secure overrides that. Leaving it
// unset preserves the old behaviour, so a plain-HTTP install (LAN, no proxy)
// keeps a working refresh cookie instead of one the browser refuses to store.
//
// CHANGING Path REQUIRES RETIRING THE OLD PATH (see staleRefreshCookiePaths
// and retireSupersededRefreshCookies).
// Cookies are keyed on (name, domain, path), so a Set-Cookie for a new path
// does not replace one at the old path — the browser keeps both and sends
// both. Since browsers order cookies longest-path-first, a leftover cookie at
// the old, longer path arrives FIRST, and net/http's Request.Cookie returns
// the first match. That is how the /api/auth/refresh -> /api/auth move logged
// every existing user out: their old cookie was presented ahead of the live
// one, the handler validated it, the server's row for it was already gone, and
// it answered 401. Signing in again did not help, because signing in only ever
// wrote the new path.
func (s *Server) refreshCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	secure := r.TLS != nil
	if s.cookieSecure != nil {
		secure = *s.cookieSecure
	}
	return &http.Cookie{
		Name:     "refresh_token",
		Value:    value,
		Path:     refreshCookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

const (
	// refreshCookiePath is where the refresh cookie lives today.
	refreshCookiePath = "/api/auth"
	// legacyRefreshCookiePath is where it lived before the signout-revocation
	// fix (pre-v0.7.4).
	legacyRefreshCookiePath = "/api/auth/refresh"
)

// staleRefreshCookiePaths are the paths a client may still be holding a refresh
// cookie at: every path this application has ever issued one on, other than the
// current path. Append here whenever refreshCookiePath changes — a client
// carrying a cookie at a path this list does not know about keeps sending it,
// and it sorts ahead of the live cookie.
//
// Deriving the set from the request instead is not possible: a Cookie request
// header carries name=value only, so the server is never told which path a
// presented cookie was scoped to.
var staleRefreshCookiePaths = []string{
	legacyRefreshCookiePath,
}

// retireSupersededRefreshCookies expires the refresh cookie at every retired
// path.
//
// MaxAge -1 (with an empty value) is the deletion form, and expiry is scoped to
// the cookie's own path: there is no "delete this name everywhere" in the
// cookie protocol, so removing a stale cookie requires naming its path. A
// client that has no cookie at that path is unaffected — the browser simply has
// nothing to delete.
//
// Call this from responses that INVALIDATE the session (signout, password
// change), never from the responses that mint one: a deletion Set-Cookie
// travelling next to a credential makes clients that key deletion on the cookie
// name alone discard the new token. Signin and refresh leave the stale cookie
// in place on purpose — it is tried after the live one and its row is already
// gone, so it cannot shadow a session.
func (s *Server) retireSupersededRefreshCookies(w http.ResponseWriter, r *http.Request) {
	for _, path := range staleRefreshCookiePaths {
		deletion := s.refreshCookie(r, "", -1)
		deletion.Path = path
		http.SetCookie(w, deletion)
	}
}

// presentedRefreshTokens returns every refresh token the request carries, in
// the order the browser sent them.
//
// r.Cookie("refresh_token") returns the FIRST match, and browsers serialise
// cookies longest-path-first (RFC 6265bis S5.8.3; Chromium's
// CookieMonster::CookieSorter sorts on path length descending, Firefox does the
// same in its cookie service). A stale cookie left behind at an older, longer
// path is therefore the one r.Cookie hands back, even when a live cookie is
// present at the current path. Any client that tried the current release once
// while holding a pre-upgrade cookie is in exactly that state.
//
// Ordering does not tell us which cookie is live, so callers must try all of
// them. Duplicates are dropped so a client that repeats a value cannot cause
// repeated work.
func presentedRefreshTokens(r *http.Request) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range r.Cookies() {
		if c.Name != "refresh_token" || c.Value == "" || seen[c.Value] {
			continue
		}
		seen[c.Value] = true
		out = append(out, c.Value)
	}
	return out
}

type signupRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	var req signupRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password are required")
		return
	}
	if err := auth.CheckPasswordLength(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeServerError(w, "failed to hash password", err)
		return
	}

	apiKey, err := generateAPIKey()
	if err != nil {
		writeServerError(w, "failed to generate api key", err)
		return
	}

	user, err := s.queries.CreateUser(r.Context(), store.CreateUserParams{
		Email:        req.Email,
		PasswordHash: hash,
		ApiKey:       apiKey,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "email already exists")
		return
	}

	accessToken, err := s.auth.GenerateAccessToken(&user)
	if err != nil {
		writeServerError(w, "failed to generate token", err)
		return
	}
	refreshToken, err := s.auth.CreateRefreshToken(r.Context(), user.ID)
	if err != nil {
		writeServerError(w, "failed to create refresh token", err)
		return
	}

	http.SetCookie(w, s.refreshCookie(r, refreshToken, int(auth.RefreshTokenDuration.Seconds())))

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"user":         newUserResponse(user),
		"access_token": accessToken,
		"api_key":      apiKey,
	})
}

type signinRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) handleSignin(w http.ResponseWriter, r *http.Request) {
	var req signinRequest
	if !decodeBody(w, r, &req) {
		return
	}

	user, err := s.queries.GetUserByEmail(r.Context(), req.Email)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if !auth.CheckPassword(req.Password, user.PasswordHash) {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	accessToken, err := s.auth.GenerateAccessToken(&user)
	if err != nil {
		writeServerError(w, "failed to generate token", err)
		return
	}
	refreshToken, err := s.auth.CreateRefreshToken(r.Context(), user.ID)
	if err != nil {
		writeServerError(w, "failed to create refresh token", err)
		return
	}

	http.SetCookie(w, s.refreshCookie(r, refreshToken, int(auth.RefreshTokenDuration.Seconds())))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user":         newUserResponse(user),
		"access_token": accessToken,
	})
}

func (s *Server) handleSignout(w http.ResponseWriter, r *http.Request) {
	// The token may arrive as a cookie OR in the JSON body, because handleRefresh
	// accepts both. A non-browser client that signs in and refreshes with a body
	// token has no cookie to read, so looking only at the cookie left it unable
	// to revoke: the row survived and the token stayed valid until expiry, which
	// is exactly the state signout exists to end.
	//
	// ALL cookies are revoked, not just the one r.Cookie returns: a client may
	// still hold a cookie at a retired path (see presentedRefreshTokens), and a
	// credential that outlives signout is the failure this endpoint exists to
	// prevent.
	tokens := presentedRefreshTokens(r)
	if len(tokens) == 0 && r.Body != nil {
		var req refreshRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil && req.RefreshToken != "" {
			tokens = []string{req.RefreshToken}
		}
	}
	for _, token := range tokens {
		if err := s.auth.DeleteRefreshToken(r.Context(), token); err != nil {
			// Revocation failed: report it rather than answering "signed out".
			// A client that believes it revoked a live credential is worse off
			// than one that knows it did not.
			slog.Error("signout: failed to delete refresh token",
				logging.CategoryAttr(logging.CategoryAuth), "error", err)
			writeError(w, http.StatusInternalServerError, "failed to revoke refresh token")
			return
		}
	}
	// Retire stale siblings, then expire the live cookie: signout must leave
	// nothing behind, or the session it just ended is still a credential.
	s.retireSupersededRefreshCookies(w, r)
	http.SetCookie(w, s.refreshCookie(r, "", -1))
	writeJSON(w, http.StatusOK, map[string]string{"message": "signed out"})
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// findLiveRefreshToken picks the token to rotate out of everything the request
// presents. It exists because the cookie the client sends FIRST is not
// necessarily the live one: a stale cookie at a retired path sorts ahead of the
// current one (see presentedRefreshTokens), so trusting the first cookie turned
// a working session into a permanent 401.
//
// Validation order is presentation order, so the ordinary single-cookie client
// costs exactly one query. Returns the row and its raw token.
func (s *Server) findLiveRefreshToken(ctx context.Context, tokens []string) (*store.RefreshToken, string, error) {
	var firstErr error
	for _, token := range tokens {
		rt, err := s.auth.ValidateRefreshToken(ctx, token)
		if err == nil {
			return rt, token, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr == nil {
		firstErr = auth.ErrTokenInvalid
	}
	return nil, "", firstErr
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	tokens := presentedRefreshTokens(r)
	if len(tokens) == 0 {
		var req refreshRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RefreshToken == "" {
			writeError(w, http.StatusUnauthorized, "no refresh token provided")
			return
		}
		tokens = []string{req.RefreshToken}
	}

	rt, presented, err := s.findLiveRefreshToken(r.Context(), tokens)
	if err != nil {
		if errors.Is(err, auth.ErrTokenExpired) {
			writeError(w, http.StatusUnauthorized, "refresh token expired")
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}

	user, err := s.queries.GetUserByID(r.Context(), rt.UserID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}

	accessToken, err := s.auth.GenerateAccessToken(&user)
	if err != nil {
		slog.Error("refresh token: failed to generate access token", logging.CategoryAttr(logging.CategoryAuth), "error", err, "user_id", user.ID)
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	newRefresh, err := s.auth.RotateRefreshToken(r.Context(), presented, user.ID)
	if err != nil {
		slog.Error("refresh token: rotation failed (old token kept)", logging.CategoryAttr(logging.CategoryAuth), "error", err, "user_id", user.ID)
		writeError(w, http.StatusInternalServerError, "failed to create refresh token")
		return
	}

	// The user_id is what operators correlate on; the token prefix is never
	// logged — even 8 chars aids correlation, and this record is served to
	// every account through /api/logs.
	slog.Debug("token refreshed", logging.CategoryAttr(logging.CategoryAuth), "user_id", user.ID)

	// Deliberately does NOT retire cookies at older paths here. Doing so means
	// emitting a deletion Set-Cookie alongside the credential, and clients that
	// treat a deletion as "this cookie name is gone" would drop the token they
	// were just handed. The stale cookie is inert regardless: it is tried after
	// the live one and has no row in refresh_tokens, so it can never shadow a
	// session again. Signout retires it.
	http.SetCookie(w, s.refreshCookie(r, newRefresh, int(auth.RefreshTokenDuration.Seconds())))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"access_token": accessToken,
	})
}

type changePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)

	var req changePasswordRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.OldPassword == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "old_password and new_password are required")
		return
	}
	if err := auth.CheckPasswordLength(req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	user, err := s.queries.GetUserByID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}
	if !auth.CheckPassword(req.OldPassword, user.PasswordHash) {
		writeError(w, http.StatusUnauthorized, "invalid old password")
		return
	}

	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeServerError(w, "failed to hash password", err)
		return
	}

	_, err = s.queries.UpdateUserPassword(r.Context(), store.UpdateUserPasswordParams{
		ID:           userID,
		PasswordHash: newHash,
	})
	if err != nil {
		writeServerError(w, "failed to update password", err)
		return
	}

	s.auth.DeleteAllUserRefreshTokens(r.Context(), userID)
	s.retireSupersededRefreshCookies(w, r)
	http.SetCookie(w, s.refreshCookie(r, "", -1))
	writeJSON(w, http.StatusOK, map[string]string{"message": "password changed"})
}

// handleGetCurrentUser returns the authenticated user's safe profile. The
// access token is memory-only on the client, so after a reload the UI holds a
// session (via the refresh cookie) but no user object; this endpoint lets it
// rehydrate the profile (e.g. the account email) without a new signin.
func (s *Server) handleGetCurrentUser(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	user, err := s.queries.GetUserByID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}
	writeJSON(w, http.StatusOK, newUserResponse(user))
}

// handleGetAPIKey returns the current Google Reader API key for the
// authenticated user. The key authenticates Google Reader clients against
// /reader/ endpoints (Authorization: GoogleLogin auth=<key>).
func (s *Server) handleGetAPIKey(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)

	user, err := s.queries.GetUserByID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"api_key": user.ApiKey})
}

// handleRotateAPIKey generates a new random API key, persists it, and returns
// it. The previous key stops authenticating immediately.
func (s *Server) handleRotateAPIKey(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)

	apiKey, err := generateAPIKey()
	if err != nil {
		slog.Error("rotate api key: failed to generate key", logging.CategoryAttr(logging.CategoryAuth), "error", err, "user_id", userID)
		writeError(w, http.StatusInternalServerError, "failed to generate api key")
		return
	}

	if _, err := s.queries.UpdateUserAPIKey(r.Context(), store.UpdateUserAPIKeyParams{
		ID:     userID,
		ApiKey: apiKey,
	}); err != nil {
		slog.Error("rotate api key: failed to persist key", logging.CategoryAttr(logging.CategoryAuth), "error", err, "user_id", userID)
		writeError(w, http.StatusInternalServerError, "failed to update api key")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"api_key": apiKey})
}
