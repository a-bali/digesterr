package httpapi

import (
	"net/http"
	"runtime"
	"time"
)

// Version is the backend version, injected at build time via
// -ldflags "-X digesterr/internal/httpapi.Version=vX.Y.Z". Falls back to "dev".
var Version = "dev"

// startedAt records process start for uptime reporting.
var startedAt = time.Now()

// handleVersion answers GET /api/version with build version and uptime.
// Unauthenticated (like /api/health) so the UI can show it without extra
// round-trips and probes can use it.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":       Version,
		"go":            runtime.Version(),
		"os":            runtime.GOOS,
		"arch":          runtime.GOARCH,
		"uptime_seconds": int64(time.Since(startedAt).Seconds()),
	})
}
