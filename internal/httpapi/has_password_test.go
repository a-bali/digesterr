package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"digesterr/internal/store"
)

// The account dialog has to know whether "change password" is a change or a
// first-time set: an OIDC-provisioned account has no password to type into the
// "current password" field, and asking for one makes the documented path to a
// local password unreachable from the UI.
//
// The flag is derived from the stored hash, never from how the account was
// created: a linked account that still holds its original password must keep
// reporting true.
func TestCurrentUserReportsWhetherAPasswordExists(t *testing.T) {
	server, queries := setupTestServer(t)

	local := createTestUser(t, queries, "has-password@example.com", "password123")
	w := makeRequest(server, http.MethodGet, "/api/auth/me", nil, getTestToken(t, server.auth, &local))
	if w.Code != http.StatusOK {
		t.Fatalf("/api/auth/me: %d %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v, ok := got["has_password"].(bool); !ok || !v {
		t.Fatalf("has_password = %v (present=%v), want true for a password account", got["has_password"], ok)
	}

	sso, err := queries.CreateOIDCUserNoPassword(t.Context(), store.CreateOIDCUserNoPasswordParams{
		Email:  "no-password@example.com",
		ApiKey: "b" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abc",
	})
	if err != nil {
		t.Fatalf("create passwordless user: %v", err)
	}
	w = makeRequest(server, http.MethodGet, "/api/auth/me", nil, getTestToken(t, server.auth, &sso))
	if w.Code != http.StatusOK {
		t.Fatalf("/api/auth/me: %d %s", w.Code, w.Body.String())
	}
	got = map[string]any{}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v, ok := got["has_password"].(bool); !ok || v {
		t.Fatalf("has_password = %v (present=%v), want false for an OIDC-provisioned account", got["has_password"], ok)
	}
	// The hash itself must never leave the process.
	if _, leaked := got["password_hash"]; leaked {
		t.Error("/api/auth/me serialized password_hash")
	}
}
