package httpapi

import (
	"net/http"
	"strconv"
	"testing"

	"digesterr/internal/store"
)

// resultPath builds the single-result endpoint path for a result id.
func resultPath(id int64) string {
	return "/api/digest-results/" + strconv.FormatInt(id, 10)
}

// The single-result mark-read cascaded UNCONDITIONALLY: the handler flipped the
// linked items whenever the request asked for status "read", whether or not the
// result was already read.
//
// That matters because an item is linked to more than one result over time — the
// live dev DB has 219 items in two or three results, max 3. Generating a newer
// digest re-links a shared item into the new result and sets it back to
// 'digested' (the generator consumes unread items). So repeating "mark read" on
// an OLD result, already read weeks ago, sweeps the shared item out of the
// NEWER unread digest's lifecycle: the item shows as read while the digest it
// belongs to is still unread.
//
// The bulk and relative paths were fixed in an earlier round by cascading only
// through rows they actually transitioned. This is the same rule for the
// single-result endpoint: the cascade belongs to the TRANSITION.

// TestSingleResultMarkReadDoesNotRecascadeAlreadyReadResult is the regression
// guard.
func TestSingleResultMarkReadDoesNotRecascadeAlreadyReadResult(t *testing.T) {
	server, queries := setupTestServer(t)
	user, token := authedTestUser(t, server, queries, "single-recascade@example.com")

	// A result already read... whose item is nevertheless still 'digested',
	// because a later generation re-linked and re-consumed it.
	old := seedDigestResultsWithItems(t, queries, user.ID, "singleold", "read", "digested")

	// Repeat the mark-read on a result that is ALREADY read.
	w := makeRequest(server, http.MethodPatch, resultPath(old.result.ID),
		map[string]any{"status": "read"}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("mark read: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if got := itemStatus(t, queries, old.items[0].ID); got != "digested" {
		t.Errorf("repeating mark-read on an already-read result cascaded into its item: "+
			"status = %q, want \"digested\". The cascade belongs to the transition, so a "+
			"no-op request must not consume an item a newer digest re-linked", got)
	}
}

// TestSingleResultMarkReadStillCascadesOnTransition is the inverse guard: the
// first mark-read of an unread result must still flip its items, or the fix
// removes the feature instead of narrowing it.
func TestSingleResultMarkReadStillCascadesOnTransition(t *testing.T) {
	server, queries := setupTestServer(t)
	user, token := authedTestUser(t, server, queries, "single-transition@example.com")

	fresh := seedDigestResultsWithItems(t, queries, user.ID, "singlenew", "new", "digested")

	w := makeRequest(server, http.MethodPatch, resultPath(fresh.result.ID),
		map[string]any{"status": "read"}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("mark read: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if got := itemStatus(t, queries, fresh.items[0].ID); got != "read" {
		t.Errorf("first mark-read of an unread result did not cascade: item status = %q, want \"read\"", got)
	}
}

// TestSingleResultUnreadThenReadCascadesAgain pins that the guard keys on the
// TRANSITION, not on the result having ever been read: marking read -> unread ->
// read must cascade on the second read.
func TestSingleResultUnreadThenReadCascadesAgain(t *testing.T) {
	server, queries := setupTestServer(t)
	user, token := authedTestUser(t, server, queries, "single-cycle@example.com")

	fx := seedDigestResultsWithItems(t, queries, user.ID, "cycle", "new", "digested")

	// First transition: unread -> read cascades.
	if w := makeRequest(server, http.MethodPatch, resultPath(fx.result.ID),
		map[string]any{"status": "read"}, token); w.Code != http.StatusOK {
		t.Fatalf("mark read: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := itemStatus(t, queries, fx.items[0].ID); got != "read" {
		t.Fatalf("first cascade did not happen: item status = %q", got)
	}

	// Back to unread, and re-digest the item as a generation would.
	if w := makeRequest(server, http.MethodPatch, resultPath(fx.result.ID),
		map[string]any{"status": "unread"}, token); w.Code != http.StatusOK {
		t.Fatalf("mark unread: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if _, err := queries.UpdateItemStatus(t.Context(), store.UpdateItemStatusParams{
		ID: fx.items[0].ID, Status: "digested",
	}); err != nil {
		t.Fatalf("re-digest item: %v", err)
	}

	// Second transition on the SAME result must cascade again.
	if w := makeRequest(server, http.MethodPatch, resultPath(fx.result.ID),
		map[string]any{"status": "read"}, token); w.Code != http.StatusOK {
		t.Fatalf("second mark read: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := itemStatus(t, queries, fx.items[0].ID); got != "read" {
		t.Errorf("marking read again after unread did not cascade: item status = %q, want \"read\"", got)
	}
}
