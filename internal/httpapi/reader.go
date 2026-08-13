package httpapi

import (
	"net/http"
	"strings"
)

const (
	// readerAuthPrefix is the Authorization header scheme used by Google
	// Reader API clients to supply their API key.
	readerAuthPrefix = "GoogleLogin auth="
	// readerTokenHeader is the fallback header carrying the API key.
	readerTokenHeader = "X-Auth-Token"
)

// readerKeyFromRequest extracts the API key used to authenticate a Google
// Reader API request. It checks the Authorization header
// ("GoogleLogin auth=<key>") first, then falls back to the X-Auth-Token
// header. The result is whitespace-trimmed.
func readerKeyFromRequest(r *http.Request) string {
	if authz := r.Header.Get("Authorization"); strings.HasPrefix(authz, readerAuthPrefix) {
		return strings.TrimSpace(strings.TrimPrefix(authz, readerAuthPrefix))
	}
	return strings.TrimSpace(r.Header.Get(readerTokenHeader))
}

// readerAuth is middleware for the Google Reader API. Unlike requireAuth
// (JWT bearer tokens), it authenticates the request with the user's API key,
// resolving the key via the GoogleLogin/X-Auth-Token headers.
func (s *Server) readerAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := readerKeyFromRequest(r)
		if key == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		user, err := s.queries.GetUserByAPIKey(r.Context(), key)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r.WithContext(contextWithUserID(r, user.ID)))
	}
}
