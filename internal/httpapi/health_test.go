package httpapi

import (
	"net/http"
	"testing"
)

func TestHealthEndpoints(t *testing.T) {
	server, _ := setupTestServer(t)

	// /healthz is a pure liveness check, no auth, no DB dependency.
	w := makeRequest(server, http.MethodGet, "/healthz", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /healthz: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "ok" {
		t.Fatalf("GET /healthz: expected body %q, got %q", "ok", w.Body.String())
	}

	// /api/health pings the DB; with a valid test DB it returns 200.
	w = makeRequest(server, http.MethodGet, "/api/health", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/health: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}
