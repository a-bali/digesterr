package httpapi

import (
	"net/http"
)

func (s *Server) handleResetMetrics(w http.ResponseWriter, r *http.Request) {
	s.metrics.Reset()
	writeJSON(w, http.StatusOK, map[string]string{"message": "metrics reset"})
}
