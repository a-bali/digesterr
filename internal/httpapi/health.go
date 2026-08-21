package httpapi

import (
	"net/http"
	"strings"
)

// handleHealthz answers 200 "ok" for liveness probes. It intentionally does
// no I/O: a deadlocked but still-serving process should be restarted, and
// liveness probes must not depend on database state.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeText(w, http.StatusOK, "ok")
}

// handleHealth answers 200 with a small JSON status for readiness probes. It
// performs a database ping so a pod with a broken DB connection is taken out
// of service. It is intentionally unauthenticated (probes carry no token).
//
// When the request carries a valid access token, the response additionally
// includes an "agentmail" boolean reporting whether AgentMail email delivery
// is configured, which the digest config UI uses to enable the email option.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.queries.DB().PingContext(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if s.agentMailConfigured && bearerToken(r) != "" {
		if _, err := s.auth.ValidateAccessToken(bearerToken(r)); err == nil {
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "agentmail": true})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// bearerToken extracts the Bearer credential from the Authorization header,
// returning "" when absent or malformed.
func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return parts[1]
}
