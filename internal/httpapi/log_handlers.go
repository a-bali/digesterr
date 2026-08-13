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
// Pagination is offset-based over the filtered stream (newest first);
// has_more reports whether older entries exist beyond this page.
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
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset = n
		}
	}

	minLevel := strings.ToUpper(r.URL.Query().Get("level"))
	minRank, hasLevel := levelRank[minLevel]
	typ := r.URL.Query().Get("type")

	all := s.logs.List(0)
	filtered := make([]logbuf.Entry, 0, len(all))
	for _, e := range all {
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

	if offset > len(filtered) {
		offset = len(filtered)
	}
	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	page := filtered[offset:end]

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"entries":  page,
		"total":    len(filtered),
		"has_more": end < len(filtered),
	})
}
