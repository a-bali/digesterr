package httpapi

import (
	"net/http"
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
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.queries.DB().PingContext(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "agentmail": s.agentMailConfigured})
}
