package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"digesterr/internal/logbuf"
)

// levelRank maps a severity string to a comparable rank.
var levelRank = map[string]int{
	"DEBUG": 0,
	"INFO":  1,
	"WARN":  2,
	"ERROR": 3,
}

// handleListLogs returns the most recent captured log entries, optionally
// filtered by minimum severity (level=info) and exact type (type=digest).
func (s *Server) handleListLogs(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 1000 {
		limit = 1000
	}

	minLevel := strings.ToUpper(r.URL.Query().Get("level"))
	minRank, hasLevel := levelRank[minLevel]
	typ := r.URL.Query().Get("type")

	entries := s.logs.List(limit)
	filtered := make([]logbuf.Entry, 0, len(entries))
	for _, e := range entries {
		if hasLevel {
			rank, ok := levelRank[strings.ToUpper(e.Level)]
			if !ok || rank < minRank {
				continue
			}
		}
		if typ != "" && e.Type != typ {
			continue
		}
		filtered = append(filtered, e)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"entries": filtered,
		"total":   len(filtered),
	})
}
