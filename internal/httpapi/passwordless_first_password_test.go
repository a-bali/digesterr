package httpapi

import (
	"net/http"
	"testing"

	"digesterr/internal/auth"
	"digesterr/internal/store"
)

// An account provisioned through OIDC starts with an empty password hash: no
// password can match it, so there is nothing to "change from". Without this
// path such an account can never gain a password at all, and an instance whose
// provider is down has no way back in for that user.
//
// The old-password check is therefore skipped only when the stored hash is
// empty. A `!oidc`-style sentinel would be indistinguishable from a real hash
// here, so emptiness is the test.
func TestChangePasswordSetsFirstPasswordForPasswordlessAccount(t *testing.T) {
	server, queries := setupTestServer(t)
	user, err := queries.CreateOIDCUserNoPassword(t.Context(), store.CreateOIDCUserNoPasswordParams{
		Email:  "sso-only@example.com",
		ApiKey: "a" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abc",
	})
	if err != nil {
		t.Fatalf("create passwordless user: %v", err)
	}
	token := getTestToken(t, server.auth, &user)

	// No old_password is sent, because the caller has none to give.
	w := makeRequest(server, http.MethodPost, "/api/auth/change-password", map[string]string{
		"new_password": "brandnewpass1",
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("setting a first password: %d %s", w.Code, w.Body.String())
	}

	signin := makeRequest(server, http.MethodPost, "/api/auth/signin", map[string]string{
		"email":    "sso-only@example.com",
		"password": "brandnewpass1",
	}, "")
	if signin.Code != http.StatusOK {
		t.Fatalf("sign in with the password just set: %d %s", signin.Code, signin.Body.String())
	}

	// Now that a password exists, the old-password check is back in force:
	// a session alone must not let an attacker silently replace a known
	// password.
	again := makeRequest(server, http.MethodPost, "/api/auth/change-password", map[string]string{
		"new_password": "attackerchosen1",
	}, token)
	if again.Code != http.StatusUnauthorized {
		t.Fatalf("second change without old_password: %d, want 401", again.Code)
	}

	updated, err := queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if !auth.CheckPassword("brandnewpass1", updated.PasswordHash) {
		t.Error("the password set by the first call no longer authenticates")
	}
}
