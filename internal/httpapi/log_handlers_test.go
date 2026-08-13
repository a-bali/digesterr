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

func TestListLogsPagination(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "logspage@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	now := time.Now()
	for i := 0; i < 5; i++ {
		server.logs.Add(logbuf.Entry{Time: now.Add(-time.Duration(5-i) * time.Second), Level: "INFO", Type: "app", Message: "m" + itoa(int64(i))})
	}

	var resp struct {
		Entries []logbuf.Entry `json:"entries"`
		Total   int            `json:"total"`
		HasMore bool           `json:"has_more"`
	}

	// First page: 2 newest (m4, m3).
	w := makeRequest(server, http.MethodGet, "/api/logs?limit=2", nil, token)
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Entries) != 2 || resp.Total != 5 || !resp.HasMore {
		t.Fatalf("page 1: got len=%d total=%d has_more=%v", len(resp.Entries), resp.Total, resp.HasMore)
	}
	if resp.Entries[0].Message != "m4" || resp.Entries[1].Message != "m3" {
		t.Fatalf("page 1 order wrong: %v", resp.Entries)
	}

	// Second page: next 2 (m2, m1).
	w = makeRequest(server, http.MethodGet, "/api/logs?limit=2&offset=2", nil, token)
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Entries) != 2 || !resp.HasMore {
		t.Fatalf("page 2: got len=%d has_more=%v", len(resp.Entries), resp.HasMore)
	}
	if resp.Entries[0].Message != "m2" || resp.Entries[1].Message != "m1" {
		t.Fatalf("page 2 order wrong: %v", resp.Entries)
	}

	// Third page: last one, has_more false.
	w = makeRequest(server, http.MethodGet, "/api/logs?limit=2&offset=4", nil, token)
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Entries) != 1 || resp.HasMore {
		t.Fatalf("page 3: got len=%d has_more=%v", len(resp.Entries), resp.HasMore)
	}
	if resp.Entries[0].Message != "m0" {
		t.Fatalf("page 3 message wrong: %v", resp.Entries[0].Message)
	}

	// Offset past the end: empty page, has_more false.
	w = makeRequest(server, http.MethodGet, "/api/logs?limit=2&offset=10", nil, token)
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Entries) != 0 || resp.HasMore {
		t.Fatalf("offset past end: got len=%d has_more=%v", len(resp.Entries), resp.HasMore)
	}
}
