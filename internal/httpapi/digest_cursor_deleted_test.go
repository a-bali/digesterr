package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"digesterr/internal/store"
)

// A cursor page must keep working after the row it names has been DELETED.
//
// The first implementation resolved the boundary by looking that row up
// (SELECT created_at FROM digest_results WHERE id = :cursor_id). Once the row was
// gone the subquery yielded NULL, nothing matched, and the response was an EMPTY
// page with has_more=false -- indistinguishable from the end of the list. Because
// the client keeps sending the same id, every deeper result stayed unreachable
// until the list was reset. Deleting the deepest loaded row makes it the boundary,
// so this was not an edge case.
//
// The boundary is now supplied BY the client as (cursor_at, cursor_id) and
// compared as a value, so it does not need the row to exist.

// cursorPageAt GETs a cursored page and decodes ids/has_more/total.
func cursorPageAt(t *testing.T, server *Server, token, path string) (ids []int64, hasMore bool, total int64) {
	t.Helper()
	w := makeRequest(server, http.MethodGet, path, nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, body %s", path, w.Code, w.Body.String())
	}
	var body struct {
		Results []struct {
			ID int64 `json:"id"`
		} `json:"results"`
		HasMore bool  `json:"has_more"`
		Total   int64 `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	for _, r := range body.Results {
		ids = append(ids, r.ID)
	}
	return ids, body.HasMore, body.Total
}

func TestDigestListCursorStillPagesAfterBoundaryRowIsDeleted(t *testing.T) {
	server, queries := setupTestServer(t)
	user, token := authedTestUser(t, server, queries, "cursor-deleted@example.com")
	configID := seedConfig(t, queries, user.ID, "cursordeleted")
	allIDs := seedManyResults(t, queries, user.ID, configID, 120, alwaysNew)

	// Page one: the 50 newest.
	first, hasMore, total := cursorPageAt(t, server, token,
		fmt.Sprintf("/api/digest-configs/%d/results?limit=50", configID))
	if len(first) != 50 || !hasMore {
		t.Fatalf("page one: %d ids, has_more=%v; want 50/true", len(first), hasMore)
	}
	// seedManyResults returns ids oldest-first, so the deepest loaded row (newest
	// first ordering) is the highest id here: page one covers ids[70..119].
	boundary := first[len(first)-1]

	// Capture the boundary's created_at the way a client would have it, BEFORE
	// deleting the row.
	boundaryAt := resultCreatedAt(t, queries, boundary)

	// Delete the boundary row itself. This is the reported case: deleting the
	// deepest loaded row makes it the paging boundary.
	if _, err := queries.DeleteDigestResult(t.Context(), store.DeleteDigestResultParams{
		ID: boundary, UserID: user.ID,
	}); err != nil {
		t.Fatalf("delete boundary row: %v", err)
	}

	// The next page uses the same (created_at, id) pair. It must still resolve,
	// and must reach the rows beyond the deleted boundary.
	path := fmt.Sprintf("/api/digest-configs/%d/results?limit=50&cursor=%d&cursor_at=%s",
		configID, boundary, url.QueryEscape(boundaryAt))
	second, _, _ := cursorPageAt(t, server, token, path)

	if len(second) == 0 {
		t.Fatalf("page two is EMPTY after the boundary row was deleted: every deeper "+
			"result is unreachable (page one held %d rows, total was %d)", len(first), total)
	}
	// The deepest loaded row was ids[70]; the next row down is ids[69].
	if second[0] != allIDs[69] {
		t.Errorf("page two begins at %d, want %d (the row directly below the deleted boundary)",
			second[0], allIDs[69])
	}
	// And the deleted row must not reappear.
	for _, id := range second {
		if id == boundary {
			t.Errorf("deleted boundary row %d was served again", id)
		}
	}
}

// An unparseable cursor_at falls back to resolving the boundary by id, so a
// client that sends only `cursor` keeps working. That is strictly better than
// restarting: the fallback still pages after the row the client named.
//
// With NO usable boundary at all (garbage cursor too) the request is the first
// page, never an error.
func TestDigestListGarbageCursorAtFallsBackToTheIdLookup(t *testing.T) {
	server, queries := setupTestServer(t)
	user, token := authedTestUser(t, server, queries, "cursor-garbageat@example.com")
	configID := seedConfig(t, queries, user.ID, "cursorgarbageat")
	allIDs := seedManyResults(t, queries, user.ID, configID, 4, alwaysNew)

	// id order is oldest-first, so newest-first display is allIDs[3],[2],[1],[0].
	// Cursoring on allIDs[2] must serve exactly the one row below it.
	for _, at := range []string{"not-a-time", "2026-13-45T99:99:99Z", ""} {
		ids, _, _ := cursorPageAt(t, server, token,
			fmt.Sprintf("/api/digest-configs/%d/results?limit=10&cursor=%d&cursor_at=%s",
				configID, allIDs[2], url.QueryEscape(at)))
		// Two rows sit below the boundary in a 4-row fixture.
		want := []int64{allIDs[1], allIDs[0]}
		if len(ids) != len(want) || ids[0] != want[0] || ids[1] != want[1] {
			t.Errorf("cursor_at=%q: served %v, want %v via the id-lookup fallback",
				at, ids, want)
		}
	}

	// Nothing usable: first page.
	ids, _, _ := cursorPageAt(t, server, token,
		fmt.Sprintf("/api/digest-configs/%d/results?limit=10&cursor=abc&cursor_at=nonsense", configID))
	if len(ids) != 4 {
		t.Errorf("no usable boundary served %d rows, want the full first page of 4", len(ids))
	}
}

// The All-Digests twin must behave identically after a boundary deletion.
func TestAllDigestResultsCursorStillPagesAfterBoundaryRowIsDeleted(t *testing.T) {
	server, queries := setupTestServer(t)
	user, token := authedTestUser(t, server, queries, "cursordeletedall@example.com")
	configID := seedConfig(t, queries, user.ID, "cursordeletedall")
	allIDs := seedManyResults(t, queries, user.ID, configID, 60, alwaysNew)

	first, hasMore, _ := cursorPageAt(t, server, token, "/api/digest-results?limit=50")
	if len(first) != 50 || !hasMore {
		t.Fatalf("page one: %d ids, has_more=%v; want 50/true", len(first), hasMore)
	}
	boundary := first[len(first)-1]
	boundaryAt := resultCreatedAt(t, queries, boundary)

	if _, err := queries.DeleteDigestResult(t.Context(), store.DeleteDigestResultParams{
		ID: boundary, UserID: user.ID,
	}); err != nil {
		t.Fatalf("delete boundary row: %v", err)
	}

	second, _, _ := cursorPageAt(t, server, token,
		fmt.Sprintf("/api/digest-results?limit=50&cursor=%d&cursor_at=%s", boundary, url.QueryEscape(boundaryAt)))
	if len(second) == 0 {
		t.Fatal("All-Digests page two is empty after the boundary row was deleted")
	}
	if second[0] != allIDs[9] {
		t.Errorf("All-Digests page two begins at %d, want %d", second[0], allIDs[9])
	}
}

// resultCreatedAt reads the stored created_at for a row, rendered the way the API
// hands timestamps to the browser (RFC3339). That is the form the client sends
// back, and therefore the form the query must normalise.
// resultCreatedAt is the timestamp a CLIENT would be holding for this row: the
// RFC3339 form the API hands out, not the stored 'YYYY-MM-DD HH:MM:SS' text. The
// driver does the conversion on scan, so reading the column as a string yields
// exactly what the browser received -- which is what makes this a fair test of
// the query's strftime normalisation.
func resultCreatedAt(t *testing.T, queries *store.Queries, id int64) string {
	t.Helper()
	var out string
	if err := queries.DB().QueryRowContext(t.Context(),
		`SELECT created_at FROM digest_results WHERE id = ?`, id).Scan(&out); err != nil {
		t.Fatalf("created_at for %d: %v", id, err)
	}
	return out
}
