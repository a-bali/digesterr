package httpapi

import (
	"fmt"
	"net/http"
	"testing"

	"digesterr/internal/store"
)

// dropDigestResultItems removes the table the digest read cascade writes to, so
// the cascade statement fails deterministically. The bug under test is that a
// failed cascade was swallowed while the preceding result update had already
// committed — a partial success that is only observable when the cascade can
// actually fail.
func dropDigestResultItems(t *testing.T, queries *store.Queries) {
	t.Helper()
	if _, err := queries.DB().Exec("DROP TABLE digest_result_items"); err != nil {
		t.Fatalf("dropping digest_result_items: %v", err)
	}
}

// TestMarkDigestResultsReadConfigRollsBackWhenCascadeFails covers the
// config-scoped mark-read. Before the fix this answered 200 and left the result
// read while its linked items stayed digested.
func TestMarkDigestResultsReadConfigRollsBackWhenCascadeFails(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "cascade-cfg@example.com", "password123")
	token := getTestToken(t, server.auth, &user)
	_, result := seedDigestResultWithItem(t, queries, user.ID, "cascade-cfg")

	dropDigestResultItems(t, queries)

	rec := makeRequest(server, http.MethodPost, "/api/digest-results/mark-read",
		map[string]any{"config_id": result.ConfigID}, token)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("cascade failure returned %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if got := digestStatus(t, queries, result.ID, user.ID); got != "new" {
		t.Errorf("result status = %q after a failed cascade, want \"new\" — the result update was not rolled back", got)
	}
}

// TestMarkDigestResultsReadAllRollsBackWhenCascadeFails covers the unscoped
// (all-configs) branch, which discarded the cascade error the same way.
func TestMarkDigestResultsReadAllRollsBackWhenCascadeFails(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "cascade-all@example.com", "password123")
	token := getTestToken(t, server.auth, &user)
	_, result := seedDigestResultWithItem(t, queries, user.ID, "cascade-all")

	dropDigestResultItems(t, queries)

	rec := makeRequest(server, http.MethodPost, "/api/digest-results/mark-read",
		map[string]any{}, token)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("cascade failure returned %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if got := digestStatus(t, queries, result.ID, user.ID); got != "new" {
		t.Errorf("result status = %q after a failed cascade, want \"new\"", got)
	}
}

// TestMarkDigestRelativeRollsBackWhenCascadeFails covers mark-read-above, which
// also ran its cascade as a separate, error-discarding statement.
func TestMarkDigestRelativeRollsBackWhenCascadeFails(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "cascade-rel@example.com", "password123")
	token := getTestToken(t, server.auth, &user)
	// Two results: the boundary is the older one, so "above" has a target.
	_, boundary := seedDigestResultWithItem(t, queries, user.ID, "cascade-rel-old")
	_, newer := seedDigestResultWithItem(t, queries, user.ID, "cascade-rel-new")

	dropDigestResultItems(t, queries)

	rec := makeRequest(server, http.MethodPost,
		fmt.Sprintf("/api/digest-results/%d/mark-read-above", boundary.ID),
		map[string]any{}, token)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("cascade failure returned %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if got := digestStatus(t, queries, newer.ID, user.ID); got != "new" {
		t.Errorf("newer result status = %q after a failed cascade, want \"new\"", got)
	}
	if got := digestStatus(t, queries, boundary.ID, user.ID); got != "new" {
		t.Errorf("boundary result status = %q, want \"new\" (boundary must stay excluded)", got)
	}
}

// TestUpdateDigestResultStatusRollsBackWhenCascadeFails covers the REST PATCH.
// It did propagate the cascade error, but only after committing the status
// update, so the result was left read with its items still digested.
func TestUpdateDigestResultStatusRollsBackWhenCascadeFails(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "cascade-patch@example.com", "password123")
	token := getTestToken(t, server.auth, &user)
	_, result := seedDigestResultWithItem(t, queries, user.ID, "cascade-patch")

	dropDigestResultItems(t, queries)

	rec := makeRequest(server, http.MethodPatch,
		fmt.Sprintf("/api/digest-results/%d", result.ID),
		map[string]any{"status": "read"}, token)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("cascade failure returned %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if got := digestStatus(t, queries, result.ID, user.ID); got != "new" {
		t.Errorf("result status = %q after a failed cascade, want \"new\" — the status update was committed before the cascade", got)
	}
}
