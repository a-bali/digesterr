package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"digesterr/internal/store"
)

// The digest lists page by CURSOR (the id of the last row the client holds), not
// by offset. An offset assumes nothing before the boundary changed, which is
// false: deleting a result -- or changing one so it stops matching the active
// filter -- shifts every later row up, so the offset points past a row that was
// never served. That row then appears on neither page, permanently, because the
// client only ever re-reads page one.
//
// These tests pin the server half: a cursor page continues strictly after the
// given row, so a shifted set cannot lose a row, and the reported total is the
// size of the whole filtered set rather than of the page.

// cursorPage GETs a digest list endpoint and returns the ids, has_more and total.
// Needed alongside listDigestPage because that helper predates the `total` field
// and does not decode it.
func cursorPage(t *testing.T, server *Server, token, path string) (ids []int64, hasMore bool, total int64) {
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

func alwaysNew(i int) string { return "new" }

// Two pages of a cursor walk must cover every row exactly once. This is the
// baseline the shift case below departs from.
func TestDigestListCursorWalksEveryRowOnce(t *testing.T) {
	server, queries := setupTestServer(t)
	user, token := authedTestUser(t, server, queries, "cursor-walk@example.com")
	configID := seedConfig(t, queries, user.ID, "cursorwalk")
	seedManyResults(t, queries, user.ID, configID, 6, alwaysNew)

	first, hasMore, total := cursorPage(t, server, token,
		fmt.Sprintf("/api/digest-configs/%d/results?limit=3", configID))
	if len(first) != 3 {
		t.Fatalf("page one returned %d ids (%v), want 3", len(first), first)
	}
	if !hasMore {
		t.Errorf("has_more = false with 6 results and limit 3")
	}
	if total != 6 {
		t.Errorf("total = %d, want 6: it must describe the whole filtered set, not the page", total)
	}

	second, _, _ := cursorPage(t, server, token,
		fmt.Sprintf("/api/digest-configs/%d/results?limit=3&cursor=%d", configID, first[len(first)-1]))
	if len(second) != 3 {
		t.Fatalf("page two returned %d ids (%v), want 3", len(second), second)
	}

	seen := map[int64]bool{}
	for _, id := range append(append([]int64{}, first...), second...) {
		if seen[id] {
			t.Errorf("id %d served on both pages", id)
		}
		seen[id] = true
	}
	if len(seen) != 6 {
		t.Errorf("the two pages covered %d distinct rows, want 6: a row was skipped or repeated", len(seen))
	}
}

// The reported failure, at the server: with an offset, deleting a row ahead of
// the boundary makes the next page start one row too late, and the row that
// shifted into the boundary is never served. A cursor is anchored to a row, so
// that row is still reached.
func TestDigestListCursorSurvivesADeletionAheadOfTheBoundary(t *testing.T) {
	server, queries := setupTestServer(t)
	user, token := authedTestUser(t, server, queries, "cursor-shift@example.com")
	configID := seedConfig(t, queries, user.ID, "cursorshift")
	allIDs := seedManyResults(t, queries, user.ID, configID, 6, alwaysNew)

	// Newest first, so page one is the three highest-index rows.
	first, _, _ := cursorPage(t, server, token,
		fmt.Sprintf("/api/digest-configs/%d/results?limit=3", configID))
	if len(first) != 3 {
		t.Fatalf("page one returned %d ids (%v), want 3", len(first), first)
	}
	boundary := first[len(first)-1]

	// Delete the NEWEST row: it precedes the boundary in the ordering, so every
	// later row shifts up by one.
	if _, err := queries.DeleteDigestResult(t.Context(), store.DeleteDigestResultParams{
		ID: allIDs[5], UserID: user.ID,
	}); err != nil {
		t.Fatalf("delete newest result: %v", err)
	}

	second, _, _ := cursorPage(t, server, token,
		fmt.Sprintf("/api/digest-configs/%d/results?limit=3&cursor=%d", configID, boundary))

	for _, id := range second {
		if id == boundary {
			t.Errorf("boundary row %d repeated on page two", id)
		}
	}
	// The row directly after the boundary in the ORIGINAL order is the one that
	// shifted up into the boundary's old position, so it must be served next.
	// An offset of "3 rows served" would skip straight past it.
	wantNext := allIDs[2]
	if len(second) == 0 || second[0] != wantNext {
		t.Errorf("page two = %v, want it to begin with %d (the row that shifted into the boundary)",
			second, wantNext)
	}
}

// A deleted boundary row must STOP pagination, not resume from an arbitrary
// point. The cursor no longer resolves, so no row matches the predicate: the list
// ends and a refresh restarts it. Skipping is unrecoverable; stopping is not.
func TestDigestListCursorOnADeletedRowStopsRatherThanSkips(t *testing.T) {
	server, queries := setupTestServer(t)
	user, token := authedTestUser(t, server, queries, "cursor-gone@example.com")
	configID := seedConfig(t, queries, user.ID, "cursorgone")
	seedManyResults(t, queries, user.ID, configID, 6, alwaysNew)

	// Use the oldest row as the cursor, then delete it.
	oldestIDs, _, _ := cursorPage(t, server, token,
		fmt.Sprintf("/api/digest-configs/%d/results?limit=100&sort=oldest", configID))
	if len(oldestIDs) != 6 {
		t.Fatalf("oldest-first page returned %d ids, want 6", len(oldestIDs))
	}
	oldest := oldestIDs[len(oldestIDs)-1]

	if _, err := queries.DeleteDigestResult(t.Context(), store.DeleteDigestResultParams{
		ID: oldest, UserID: user.ID,
	}); err != nil {
		t.Fatalf("delete boundary result: %v", err)
	}

	ids, hasMore, total := cursorPage(t, server, token,
		fmt.Sprintf("/api/digest-configs/%d/results?limit=3&cursor=%d", configID, oldest))
	if len(ids) != 0 {
		t.Errorf("page after a deleted cursor served %v, want none: an unresolvable boundary must "+
			"stop pagination rather than resume from an arbitrary point", ids)
	}
	if hasMore {
		t.Errorf("has_more = true after an unresolvable cursor")
	}
	// The total still describes the surviving set, so the header stays truthful.
	if total != 5 {
		t.Errorf("total = %d, want 5 after one deletion", total)
	}
}

// An unparseable or absent cursor means "first page", never an error: a bad
// cursor must restart the list rather than 400 or silently skip rows.
func TestDigestListCursorIgnoresGarbage(t *testing.T) {
	server, queries := setupTestServer(t)
	user, token := authedTestUser(t, server, queries, "cursor-garbage@example.com")
	configID := seedConfig(t, queries, user.ID, "cursorgarbage")
	seedManyResults(t, queries, user.ID, configID, 4, alwaysNew)

	for _, cur := range []string{"", "abc", "-5", "0"} {
		ids, _, total := cursorPage(t, server, token,
			fmt.Sprintf("/api/digest-configs/%d/results?limit=10&cursor=%s", configID, cur))
		if len(ids) != 4 {
			t.Errorf("cursor=%q served %d rows (%v), want the full first page of 4: a non-cursor "+
				"value must be treated as 'no cursor'", cur, len(ids), ids)
		}
		if total != 4 {
			t.Errorf("cursor=%q total = %d, want 4", cur, total)
		}
	}
}

// The status filter must apply to the total as well as the page, or the header
// would count rows the list is not showing.
func TestDigestListTotalHonoursTheStatusFilter(t *testing.T) {
	server, queries := setupTestServer(t)
	user, token := authedTestUser(t, server, queries, "cursor-filtertotal@example.com")
	configID := seedConfig(t, queries, user.ID, "cursorfiltertotal")

	// index 0 = oldest. Make two of them read.
	statusOf := func(i int) string {
		if i < 2 {
			return "read"
		}
		return "new"
	}
	seedManyResults(t, queries, user.ID, configID, 6, statusOf)

	_, _, allTotal := cursorPage(t, server, token,
		fmt.Sprintf("/api/digest-configs/%d/results?limit=100", configID))
	if allTotal != 6 {
		t.Errorf("unfiltered total = %d, want 6", allTotal)
	}

	_, _, unreadTotal := cursorPage(t, server, token,
		fmt.Sprintf("/api/digest-configs/%d/results?limit=100&status=unread", configID))
	if unreadTotal != 4 {
		t.Errorf("unread total = %d, want 4: the total must describe the filtered set", unreadTotal)
	}

	_, _, readTotal := cursorPage(t, server, token,
		fmt.Sprintf("/api/digest-configs/%d/results?limit=100&status=read", configID))
	if readTotal != 2 {
		t.Errorf("read total = %d, want 2", readTotal)
	}
}

// The All-Digests endpoint takes the same cursor and must honour it identically.
func TestAllDigestResultsCursorContinuesAfterARow(t *testing.T) {
	server, queries := setupTestServer(t)
	user, token := authedTestUser(t, server, queries, "cursor-all@example.com")
	configID := seedConfig(t, queries, user.ID, "cursorall")
	seedManyResults(t, queries, user.ID, configID, 6, alwaysNew)

	first, hasMore, total := cursorPage(t, server, token, "/api/digest-results?limit=3")
	if len(first) != 3 || !hasMore || total != 6 {
		t.Fatalf("page one: ids=%v has_more=%v total=%d, want 3/true/6", first, hasMore, total)
	}

	second, _, _ := cursorPage(t, server, token,
		fmt.Sprintf("/api/digest-results?limit=3&cursor=%d", first[len(first)-1]))
	seen := map[int64]bool{}
	for _, id := range append(append([]int64{}, first...), second...) {
		if seen[id] {
			t.Errorf("id %d served on both All-Digests pages", id)
		}
		seen[id] = true
	}
	if len(seen) != 6 {
		t.Errorf("All-Digests pages covered %d distinct rows, want 6", len(seen))
	}
}

// Both sort directions must walk the same set, so a cursor cannot be honoured in
// one direction only.
func TestDigestListCursorHonoursSortDirection(t *testing.T) {
	server, queries := setupTestServer(t)
	user, token := authedTestUser(t, server, queries, "cursor-sort@example.com")
	configID := seedConfig(t, queries, user.ID, "cursorsort")
	seedManyResults(t, queries, user.ID, configID, 6, alwaysNew)

	newest, _, _ := cursorPage(t, server, token,
		fmt.Sprintf("/api/digest-configs/%d/results?limit=6", configID))
	oldest, _, _ := cursorPage(t, server, token,
		fmt.Sprintf("/api/digest-configs/%d/results?limit=6&sort=oldest", configID))
	if len(newest) != 6 || len(oldest) != 6 {
		t.Fatalf("newest=%v oldest=%v, want 6 each", newest, oldest)
	}
	if newest[0] != oldest[len(oldest)-1] {
		t.Errorf("newest-first begins at %d but oldest-first ends at %d: the two directions "+
			"describe different sets", newest[0], oldest[len(oldest)-1])
	}

	// Walk oldest-first past the first row and confirm the next page continues.
	second, _, _ := cursorPage(t, server, token,
		fmt.Sprintf("/api/digest-configs/%d/results?limit=2&sort=oldest&cursor=%d", configID, oldest[1]))
	if len(second) == 0 {
		t.Fatal("oldest-first page two is empty")
	}
	if second[0] != oldest[2] {
		t.Errorf("oldest-first page two begins at %d, want %d", second[0], oldest[2])
	}
}
