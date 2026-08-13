package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"digesterr/internal/logbuf"
)

func TestListLogsRequiresAuth(t *testing.T) {
	server, _ := setupTestServer(t)
	w := makeRequest(server, http.MethodGet, "/api/logs", nil, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", w.Code)
	}
}

func TestListLogsFilters(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "logs@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	// Seed the buffer with known entries.
	now := time.Now()
	server.logs.Add(logbuf.Entry{Time: now.Add(-3 * time.Second), Level: "INFO", Type: "http", Message: "request ok"})
	server.logs.Add(logbuf.Entry{Time: now.Add(-2 * time.Second), Level: "WARN", Type: "feed", Message: "fetch slow"})
	server.logs.Add(logbuf.Entry{Time: now.Add(-1 * time.Second), Level: "ERROR", Type: "digest", Message: "gen failed"})

	// No filters: all 3, newest first.
	w := makeRequest(server, http.MethodGet, "/api/logs", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp struct {
		Entries []logbuf.Entry `json:"entries"`
		Total   int            `json:"total"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Total != 3 || len(resp.Entries) != 3 {
		t.Fatalf("expected 3 entries, got total=%d len=%d", resp.Total, len(resp.Entries))
	}
	if resp.Entries[0].Message != "gen failed" {
		t.Fatalf("expected newest first, got %s", resp.Entries[0].Message)
	}

	// Minimum level=warn: only warn+error.
	w = makeRequest(server, http.MethodGet, "/api/logs?level=warn", nil, token)
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Total != 2 {
		t.Fatalf("expected 2 entries for level=warn, got %d", resp.Total)
	}

	// Exact type=feed.
	w = makeRequest(server, http.MethodGet, "/api/logs?type=feed", nil, token)
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Total != 1 || resp.Entries[0].Message != "fetch slow" {
		t.Fatalf("expected 1 feed entry, got %d", resp.Total)
	}

	// Combined level=info&type=digest.
	w = makeRequest(server, http.MethodGet, "/api/logs?level=info&type=digest", nil, token)
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Total != 1 || resp.Entries[0].Message != "gen failed" {
		t.Fatalf("expected 1 digest entry, got %d", resp.Total)
	}
}
