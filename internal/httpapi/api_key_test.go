package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestSignupGeneratesAPIKey(t *testing.T) {
	server, queries := setupTestServer(t)

	body := map[string]string{
		"email":    "apikey@example.com",
		"password": "password123",
	}
	w := makeRequest(server, http.MethodPost, "/api/auth/signup", body, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode signup response: %v", err)
	}
	if resp.APIKey == "" {
		t.Fatal("expected non-empty api_key in signup response")
	}
	if len(resp.APIKey) != 64 {
		t.Fatalf("expected 64-char hex api_key, got %d chars", len(resp.APIKey))
	}

	// Round-trip: the generated key should resolve back to the user.
	ctx := context.Background()
	user, err := queries.GetUserByAPIKey(ctx, resp.APIKey)
	if err != nil {
		t.Fatalf("GetUserByAPIKey returned error for generated key: %v", err)
	}
	if user.Email != "apikey@example.com" {
		t.Fatalf("expected email apikey@example.com, got %s", user.Email)
	}

	// A missing/empty key must not resolve to any user.
	if _, err := queries.GetUserByAPIKey(ctx, ""); err == nil {
		t.Fatal("expected error for empty api_key lookup")
	}
	if _, err := queries.GetUserByAPIKey(ctx, "does-not-exist"); err == nil {
		t.Fatal("expected error for unknown api_key lookup")
	}
}

func TestGetAPIKey(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "getkey@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	w := makeRequest(server, http.MethodGet, "/api/auth/api-key", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.APIKey != user.ApiKey {
		t.Fatalf("expected api_key %q, got %q", user.ApiKey, resp.APIKey)
	}
}

func TestRotateAPIKey(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "rotatekey@example.com", "password123")
	oldKey := user.ApiKey
	token := getTestToken(t, server.auth, &user)

	w := makeRequest(server, http.MethodPost, "/api/auth/api-key/rotate", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.APIKey) != 64 {
		t.Fatalf("expected 64-char hex api_key, got %d chars", len(resp.APIKey))
	}
	if resp.APIKey == oldKey {
		t.Fatal("expected rotated api_key to differ from the old one")
	}

	ctx := context.Background()

	// The new key resolves back to the same user…
	rotated, err := queries.GetUserByAPIKey(ctx, resp.APIKey)
	if err != nil {
		t.Fatalf("GetUserByAPIKey returned error for rotated key: %v", err)
	}
	if rotated.Email != user.Email {
		t.Fatalf("expected rotated key to resolve to %s, got %s", user.Email, rotated.Email)
	}

	// …and the old key no longer authenticates.
	if _, err := queries.GetUserByAPIKey(ctx, oldKey); err == nil {
		t.Fatal("expected error for revoked old api_key lookup")
	}
}

func TestAPIKeyEndpointsRequireAuth(t *testing.T) {
	server, _ := setupTestServer(t)

	if w := makeRequest(server, http.MethodGet, "/api/auth/api-key", nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated GET api-key, got %d", w.Code)
	}
	if w := makeRequest(server, http.MethodPost, "/api/auth/api-key/rotate", nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated POST api-key/rotate, got %d", w.Code)
	}
}
