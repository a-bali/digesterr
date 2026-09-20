package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"
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
// It advertises NO feature state, authenticated or not. Email feed availability
// used to be reported here, which made a probe endpoint the frontend's source of
// truth for a per-user setting; that now lives in GET /api/settings/email. The
// payload is byte-identical with and without a token, so a probe response cannot
// vary with credentials.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// Bound the ping so a wedged database cannot hang the readiness probe
	// (and therefore the orchestrator's shutdown decision) indefinitely.
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.queries.DB().PingContext(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// healthWithFeatures was removed with the instance-wide email configuration: the
// email feed type is per user now, so feature availability is reported by
// GET /api/settings/email for the authenticated caller and by nothing at all here.
