package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"digesterr/internal/store"
)

// A request body over the cap must be rejected with 413, not the generic 400
// that malformed JSON gets.
func TestRequestBodyTooLarge(t *testing.T) {
	server, _ := setupTestServer(t)

	huge := strings.Repeat("a", maxRequestBody+1024)
	w := makeRequest(server, http.MethodPost, "/api/auth/signup", map[string]string{
		"email":    "big@example.com",
		"password": huge,
	}, "")
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized body, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "too large") {
		t.Errorf("expected 'too large' message, got %s", w.Body.String())
	}
}

// A normal-sized body still decodes fine.
func TestRequestBodyWithinLimit(t *testing.T) {
	server, _ := setupTestServer(t)
	w := makeRequest(server, http.MethodPost, "/api/auth/signup", map[string]string{
		"email":    "ok@example.com",
		"password": "password123",
	}, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestTruncateError(t *testing.T) {
	if got := truncateError(nil, 10); got != "" {
		t.Errorf("truncateError(nil) = %q, want empty", got)
	}
	if got := truncateError(errors.New("short"), 10); got != "short" {
		t.Errorf("short error not preserved: %q", got)
	}
	long := strings.Repeat("x", 100)
	got := truncateError(errors.New(long), 10)
	// 10 kept bytes plus the multi-byte ellipsis rune.
	if !strings.HasPrefix(got, strings.Repeat("x", 10)) {
		t.Errorf("truncated error lost its prefix: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix, got %q", got)
	}
}

// The generation failure response must not echo the upstream error chain,
// while the stored copy is bounded.
func TestGenerateDigestErrorIsSanitized(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "genfail@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	// A config that matches no feeds makes Generate fail deterministically
	// before any LLM call, with an internal error message.
	cfg, err := queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "empty",
		Schedule: "0 * * * *",
		Config:   `{"feeds":[],"categories":[]}`,
	})
	if err != nil {
		t.Fatalf("create config: %v", err)
	}

	w := makeRequest(server, http.MethodPost, "/api/digest-configs/"+itoa(cfg.ID)+"/generate", nil, token)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "matches no feeds") {
		t.Errorf("response leaked internal error: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "failed to generate digest") {
		t.Errorf("expected stable message, got %s", w.Body.String())
	}
	stored, err := queries.GetDigestConfig(t.Context(), store.GetDigestConfigParams{ID: cfg.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if len(stored.LastError) > 501 {
		t.Errorf("stored error not bounded: %d bytes", len(stored.LastError))
	}
}
