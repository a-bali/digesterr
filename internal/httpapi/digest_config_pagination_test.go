package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"digesterr/internal/store"
)

// The single-config digest list could not reach page two.
//
// The query was asked for exactly `limit` rows and the handler then tested
// len(results) > limit for has_more — a condition that can never hold, because
// the query never returns more than limit rows. has_more was therefore always
// false and paging stopped at the first page for every config. The sibling
// all-results endpoint asks for limit+1 and is the reference shape.

// seedConfigDigestResults creates n results (oldest first) for one config and
// returns the config id.
func seedConfigDigestResults(t *testing.T, queries *store.Queries, userID int64, tag string, n int) int64 {
	t.Helper()
	cfg, err := queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID:   userID,
		Name:     "Cfg " + tag,
		Schedule: "daily",
		Config:   "{}",
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}
	for i := 0; i < n; i++ {
		if _, err := queries.CreateDigestResult(t.Context(), store.CreateDigestResultParams{
			UserID:         userID,
			ConfigID:       cfg.ID,
			ConfigSnapshot: "{}",
			Content:        fmt.Sprintf(`{"title":"Result %d"}`, i),
			Status:         "new",
		}); err != nil {
			t.Fatalf("create digest result %d: %v", i, err)
		}
	}
	return cfg.ID
}

type digestConfigPage struct {
	Results []struct {
		ID       int64  `json:"id"`
		ConfigID int64  `json:"config_id"`
		Title    string `json:"title"`
	} `json:"results"`
	HasMore bool  `json:"has_more"`
	Total   int64 `json:"total"`
}

func fetchConfigDigestPage(t *testing.T, server *Server, token string, configID int64, query string) digestConfigPage {
	t.Helper()
	rec := makeRequest(server, http.MethodGet,
		fmt.Sprintf("/api/digest-configs/%d/results%s", configID, query), nil, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("list digest results returned %d: %s", rec.Code, rec.Body.String())
	}
	var page digestConfigPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return page
}

// TestConfigDigestResultsReportHasMore is the core regression: with more results
// than the page size, the first page must say has_more.
func TestConfigDigestResultsReportHasMore(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "cfgpage@example.com", "password123")
	token := getTestToken(t, server.auth, &user)
	cfgID := seedConfigDigestResults(t, queries, user.ID, "cfgpage", 5)

	first := fetchConfigDigestPage(t, server, token, cfgID, "?limit=2&offset=0")
	if len(first.Results) != 2 {
		t.Errorf("first page has %d results, want 2", len(first.Results))
	}
	if !first.HasMore {
		t.Error("first page has_more = false, want true with 5 results and a limit of 2: the handler asks the query for `limit` rows and then tests len > limit, which can never be true")
	}
}

// TestConfigDigestResultsPageThroughEveryResult walks the pages: each result
// must appear exactly once, and the walk must terminate.
func TestConfigDigestResultsPageThroughEveryResult(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "cfgwalk@example.com", "password123")
	token := getTestToken(t, server.auth, &user)
	cfgID := seedConfigDigestResults(t, queries, user.ID, "cfgwalk", 5)

	seen := map[int64]int{}
	pages := 0
	for offset := 0; ; offset += 2 {
		page := fetchConfigDigestPage(t, server, token, cfgID,
			fmt.Sprintf("?limit=2&offset=%d", offset))
		pages++
		for _, r := range page.Results {
			seen[r.ID]++
		}
		if !page.HasMore {
			break
		}
		if pages > 10 {
			t.Fatal("paging did not terminate")
		}
	}
	if len(seen) != 5 {
		t.Errorf("paging returned %d distinct results, want 5 (has_more false stopped the walk early)", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("result %d returned %d times, want exactly 1", id, n)
		}
	}
}

// TestConfigDigestResultsLastPageHasNoMore guards the other direction: the
// fix must not report has_more on a genuinely final page, or the client would
// page forever.
func TestConfigDigestResultsLastPageHasNoMore(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "cfglast@example.com", "password123")
	token := getTestToken(t, server.auth, &user)
	cfgID := seedConfigDigestResults(t, queries, user.ID, "cfglast", 3)

	// Exactly the page size: nothing beyond it.
	page := fetchConfigDigestPage(t, server, token, cfgID, "?limit=3&offset=0")
	if len(page.Results) != 3 {
		t.Errorf("page has %d results, want 3", len(page.Results))
	}
	if page.HasMore {
		t.Error("has_more = true on a page that exactly exhausts the list")
	}

	// One beyond the end.
	last := fetchConfigDigestPage(t, server, token, cfgID, "?limit=2&offset=2")
	if len(last.Results) != 1 {
		t.Errorf("final page has %d results, want 1", len(last.Results))
	}
	if last.HasMore {
		t.Error("has_more = true on the final page")
	}
}

// TestConfigDigestResultsPagingMatchesLimitBoundary pins the off-by-one at the
// page boundary with a limit+1 shaped fixture: 3 results with a limit of 2 must
// report has_more on page one and stop on page two.
func TestConfigDigestResultsPagingMatchesLimitBoundary(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "cfgbound@example.com", "password123")
	token := getTestToken(t, server.auth, &user)
	cfgID := seedConfigDigestResults(t, queries, user.ID, "cfgbound", 3)

	p1 := fetchConfigDigestPage(t, server, token, cfgID, "?limit=2&offset=0")
	if len(p1.Results) != 2 || !p1.HasMore {
		t.Errorf("page 1 = %d results, has_more=%v; want 2, true", len(p1.Results), p1.HasMore)
	}
	p2 := fetchConfigDigestPage(t, server, token, cfgID, "?limit=2&offset=2")
	if len(p2.Results) != 1 || p2.HasMore {
		t.Errorf("page 2 = %d results, has_more=%v; want 1, false", len(p2.Results), p2.HasMore)
	}
}
