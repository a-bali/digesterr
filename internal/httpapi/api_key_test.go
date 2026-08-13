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
