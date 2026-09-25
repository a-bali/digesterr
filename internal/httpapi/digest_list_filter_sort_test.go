package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"digesterr/internal/store"
)

// These tests cover item 3: the digest list's status filter, its oldest-first
// sort, and reaching a result that is NOT on page one.
//
// All three were previously applied in the browser over the rows the client had
// already loaded, so "Oldest" was the reverse of the first page, the filter
// could only hide already-fetched rows, and an older deep link resolved to
// nothing. The fix moved the filter and the sort into the query, which is what
// these tests pin.

// seedManyResults inserts n results for one config with an ascending created_at
// (index 0 = oldest), so id order and date order agree and the assertions read
// naturally. Returns the ids oldest-first.
func seedManyResults(t *testing.T, queries *store.Queries, userID, configID int64, n int, statusOf func(i int) string) []int64 {
	t.Helper()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		created := base.Add(time.Duration(i) * time.Hour)
		row, err := queries.CreateDigestResult(t.Context(), store.CreateDigestResultParams{
			UserID:         userID,
			ConfigID:       configID,
			ConfigSnapshot: "{}",
			Content:        fmt.Sprintf(`{"title":"Result %d"}`, i),
			Status:         statusOf(i),
		})
		if err != nil {
			t.Fatalf("seeding result %d: %v", i, err)
		}
		if _, err := queries.DB().ExecContext(t.Context(),
			`UPDATE digest_results SET created_at = ? WHERE id = ?`,
			created.Format("2006-01-02 15:04:05"), row.ID); err != nil {
			t.Fatalf("pinning created_at for %d: %v", i, err)
		}
		ids = append(ids, row.ID)
	}
	return ids
}

// seedConfig creates a digest config owned by userID.
func seedConfig(t *testing.T, queries *store.Queries, userID int64, tag string) int64 {
	t.Helper()
	cfg, err := queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID: userID, Name: "Cfg " + tag, Schedule: "daily", Config: "{}",
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}
	return cfg.ID
}

type listPage struct {
	ids      []int64
	statuses []string
	hasMore  bool
}

// listDigestPage GETs a list endpoint through the real router and decodes the
// preview payload.
func listDigestPage(t *testing.T, server *Server, tok, path string) listPage {
	t.Helper()
	w := makeRequest(server, http.MethodGet, path, nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, body %s", path, w.Code, w.Body.String())
	}
	var body struct {
		Results []struct {
			ID     int64  `json:"id"`
			Status string `json:"status"`
		} `json:"results"`
		HasMore bool `json:"has_more"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	p := listPage{hasMore: body.HasMore}
	for _, r := range body.Results {
		p.ids = append(p.ids, r.ID)
		p.statuses = append(p.statuses, r.Status)
	}
	return p
}

func reversed(ids []int64) []int64 {
	out := make([]int64, len(ids))
	for i, id := range ids {
		out[len(ids)-1-i] = id
	}
	return out
}

// A status filter must select across the WHOLE table, not just the loaded page.
// With unread results spread across three pages, a page-one-only filter would
// miss the ones further down.
func TestDigestListStatusFilterAppliesServerSide(t *testing.T) {
	server, queries := setupTestServer(t)
	user, token := authedTestUser(t, server, queries, "filter-server@example.com")
	configID := seedConfig(t, queries, user.ID, "filterserver")

	statusOf := func(i int) string {
		if i%3 == 0 {
			return "read"
		}
		return "new"
	}
	allIDs := seedManyResults(t, queries, user.ID, configID, 30, statusOf)

	var wantUnread []int64
	for i, id := range allIDs {
		if statusOf(i) == "new" {
			wantUnread = append(wantUnread, id)
		}
	}
	wantUnread = reversed(wantUnread) // default sort is newest first

	// limit=100 spans the whole fixture: if the filter ran over only the loaded
	// page, the unread rows further down would be missing.
	got := listDigestPage(t, server, token,
		fmt.Sprintf("/api/digest-configs/%d/results?limit=100&status=unread", configID))

	if len(got.ids) != len(wantUnread) {
		t.Fatalf("unread filter returned %d rows, want %d (ids %v)", len(got.ids), len(wantUnread), got.ids)
	}
	for i := range got.ids {
		if got.ids[i] != wantUnread[i] {
			t.Fatalf("unread ids = %v, want %v", got.ids, wantUnread)
		}
		// The list endpoints pass the STORED status through unchanged; the
		// browser normalises 'new' to 'unread' (see normalize() in
		// web/src/stores/digest.ts). The contract at this layer is 'new'.
		if got.statuses[i] != "new" {
			t.Fatalf("row %d has status %q, want new", i, got.statuses[i])
		}
	}

	gotRead := listDigestPage(t, server, token,
		fmt.Sprintf("/api/digest-configs/%d/results?limit=100&status=read", configID))
	// statusOf marks every third row read, so 10 of 30.
	if len(gotRead.ids) != 10 {
		t.Fatalf("read filter returned %d rows, want 10", len(gotRead.ids))
	}
	for i, s := range gotRead.statuses {
		if s != "read" {
			t.Fatalf("read filter row %d has status %q", i, s)
		}
	}
}

// "Oldest" must reorder the whole table. Sorting only the loaded page returned
// the first page reversed, which is not the oldest results at all.
func TestDigestListOldestSortAppliesServerSide(t *testing.T) {
	server, queries := setupTestServer(t)
	user, token := authedTestUser(t, server, queries, "sort-server@example.com")
	configID := seedConfig(t, queries, user.ID, "sortserver")
	allIDs := seedManyResults(t, queries, user.ID, configID, 30, func(int) string { return "new" })

	oldest := listDigestPage(t, server, token,
		fmt.Sprintf("/api/digest-configs/%d/results?limit=10&sort=oldest", configID))
	if len(oldest.ids) != 10 {
		t.Fatalf("oldest page returned %d rows, want 10", len(oldest.ids))
	}
	for i := range oldest.ids {
		if oldest.ids[i] != allIDs[i] {
			t.Fatalf("oldest page ids = %v, want the first ten seeded %v", oldest.ids, allIDs[:10])
		}
	}

	newest := listDigestPage(t, server, token,
		fmt.Sprintf("/api/digest-configs/%d/results?limit=10", configID))
	wantNewest := reversed(allIDs)
	if len(newest.ids) != 10 || newest.ids[0] != wantNewest[0] {
		t.Fatalf("newest page ids = %v, want first ten of %v", newest.ids, wantNewest)
	}
}

// Oldest-first must PAGE, not just reorder page one: page two must continue
// where page one stopped.
func TestDigestListOldestSortPaginates(t *testing.T) {
	server, queries := setupTestServer(t)
	user, token := authedTestUser(t, server, queries, "sort-page@example.com")
	configID := seedConfig(t, queries, user.ID, "sortpage")
	allIDs := seedManyResults(t, queries, user.ID, configID, 12, func(int) string { return "new" })

	p1 := listDigestPage(t, server, token,
		fmt.Sprintf("/api/digest-configs/%d/results?limit=10&sort=oldest", configID))
	if !p1.hasMore {
		t.Fatal("page one of 12 oldest-first rows reported has_more=false")
	}
	p2 := listDigestPage(t, server, token,
		fmt.Sprintf("/api/digest-configs/%d/results?limit=10&sort=oldest&offset=10", configID))

	if len(p1.ids) != 10 || len(p2.ids) != 2 {
		t.Fatalf("oldest paging returned %d + %d rows, want 10 + 2", len(p1.ids), len(p2.ids))
	}
	if p2.ids[0] != allIDs[10] || p2.ids[1] != allIDs[11] {
		t.Fatalf("page two ids = %v, want %v", p2.ids, allIDs[10:])
	}
}

// A deep link to a result beyond page one must resolve. The route handler
// previously searched only the loaded first page, so an older result id
// resolved to nothing.
func TestDigestDeepLinkBeyondFirstPageResolves(t *testing.T) {
	server, queries := setupTestServer(t)
	user, token := authedTestUser(t, server, queries, "deeplink-server@example.com")
	configID := seedConfig(t, queries, user.ID, "deeplink")
	allIDs := seedManyResults(t, queries, user.ID, configID, 30, func(int) string { return "new" })

	oldest := allIDs[0] // page three; absent from the default page-one list

	// Guard the fixture: the old behaviour would only have worked if the row
	// were on page one.
	page1 := listDigestPage(t, server, token,
		fmt.Sprintf("/api/digest-configs/%d/results?limit=10", configID))
	for _, id := range page1.ids {
		if id == oldest {
			t.Fatal("fixture is wrong: the deep-linked result is on page one")
		}
	}

	// The by-id endpoint is what the client falls back to, so it must serve it.
	w := makeRequest(server, http.MethodGet, fmt.Sprintf("/api/digest-results/%d", oldest), nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/digest-results/%d = %d, want 200: %s", oldest, w.Code, w.Body.String())
	}
}

// The All-Digests list has the same controls and must behave identically.
func TestAllDigestsListFilterAndSortServerSide(t *testing.T) {
	server, queries := setupTestServer(t)
	user, token := authedTestUser(t, server, queries, "all-server@example.com")
	cfgA := seedConfig(t, queries, user.ID, "allA")
	cfgB := seedConfig(t, queries, user.ID, "allB")

	alt := func(i int) string {
		if i%2 == 0 {
			return "new"
		}
		return "read"
	}
	idsA := seedManyResults(t, queries, user.ID, cfgA, 8, alt)
	idsB := seedManyResults(t, queries, user.ID, cfgB, 8, alt)

	got := listDigestPage(t, server, token, "/api/digest-results?limit=100&status=unread")
	if len(got.ids) != 8 {
		t.Fatalf("all-digests unread filter returned %d rows, want 8", len(got.ids))
	}
	for i, s := range got.statuses {
		if s != "new" {
			t.Fatalf("all-digests unread row %d has status %q, want new", i, s)
		}
	}

	// Oldest overall leads; config A was seeded entirely before config B.
	oldest := listDigestPage(t, server, token, "/api/digest-results?limit=100&sort=oldest")
	if len(oldest.ids) == 0 || oldest.ids[0] != idsA[0] {
		t.Fatalf("all-digests oldest-first leads with %v, want %d", oldest.ids, idsA[0])
	}
	newest := listDigestPage(t, server, token, "/api/digest-results?limit=100")
	if len(newest.ids) == 0 || newest.ids[0] != idsB[len(idsB)-1] {
		t.Fatalf("all-digests newest-first leads with %v, want %d", newest.ids, idsB[len(idsB)-1])
	}
}
