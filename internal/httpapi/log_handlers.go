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

// categoryOf returns the effective category for an entry. An explicit
// "category" attribute (set by the logging package, e.g. "item" for
// item-level records logged from the http layer) wins over the
// source-derived type. Entries without a category attribute fall back to
// their type, preserving pre-category behavior.
func categoryOf(e logbuf.Entry) string {
	if c, ok := e.Attrs["category"]; ok && c != "" {
		return c
	}
	return e.Type
}

// handleListLogs returns the most recent captured log entries, optionally
// filtered by minimum severity (level=info) and exact category (type=digest).
// The category filter matches the entry's effective category: an explicit
// "category" attribute when present, otherwise its source-derived type.
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
		if typ != "" && categoryOf(e) != typ {
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
