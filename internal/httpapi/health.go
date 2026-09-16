package httpapi

import (
	"net/http"
	"strings"

	"digesterr/internal/feed"
)

// handleHealthz answers 200 "ok" for liveness probes. It intentionally does
// no I/O: a deadlocked but still-serving process should be restarted, and
// liveness probes must not depend on database state.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeText(w, http.StatusOK, "ok")
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header,
// or "" when the header is absent or not a bearer credential. It does not
// validate: callers decide what an invalid token means.
func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return parts[1]
}

// handleHealth answers 200 with a small JSON status for readiness probes. It
// performs a database ping so a pod with a broken DB connection is taken out
// of service. It is intentionally unauthenticated (probes carry no token).
//
// When the request carries a valid access token the response additionally
// reports whether the email feed type is available, plus the inbox prefix and
// domain the UI renders as fixed context around the editable suffix. Those keys
// are OMITTED rather than sent as empty/false when unauthenticated, so a probe
// response stays byte-identical to what it was before this feature and the
// frontend can distinguish "not configured" from "not logged in" by absence.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.queries.DB().PingContext(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if token := bearerToken(r); token != "" {
		if _, err := s.auth.ValidateAccessToken(token); err == nil {
			writeJSON(w, http.StatusOK, s.healthWithFeatures())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// healthWithFeatures builds the authenticated health payload.
//
// The email fields describe the SHAPE of a feed address so the UI can show
// "name+" and "@agentmail.to" as non-editable context and let the user type
// only the suffix. The address is instance-wide, so nothing here is per-user.
func (s *Server) healthWithFeatures() map[string]any {
	resp := map[string]any{
		"status":        "ok",
		"email_enabled": s.emailConfigured,
	}
	if s.emailConfigured {
		resp["email_inbox"] = s.emailInbox
		resp["email_prefix"] = feed.EmailInboxPrefix(s.emailInbox)
		resp["email_domain"] = s.emailDomain
	}
	return resp
}
