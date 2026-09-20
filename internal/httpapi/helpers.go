package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeServerError(w http.ResponseWriter, msg string, err error) {
	slog.Error("server error", "error", err, "message", msg)
	writeError(w, http.StatusInternalServerError, msg)
}

// truncateError renders err as a bounded string for values that are persisted
// in the database or surfaced in the UI. Unbounded upstream text (LLM response
// bodies, provider error pages) must not end up verbatim in error columns.
func truncateError(err error, maxLen int) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

// decodeBody reads a JSON request body into dst, writing the appropriate error
// response and returning false on failure. It distinguishes an oversized body
// (413, from the MaxBytesReader installed in ServeHTTP) from malformed JSON
// (400), so a too-large upload is not reported as a syntax error.
func decodeBody(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}
