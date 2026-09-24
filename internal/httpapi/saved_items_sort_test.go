package httpapi

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"digesterr/internal/store"
)

// seedSavedItemsAt creates saved items on one feed with explicit publish times,
// so an ordering assertion never depends on a tie between two rows. seedSavedItems
// stamps every row with the same time, which is fine for pagination (the id
// tie-break carries it) but useless for proving a sort direction.
func seedSavedItemsAt(t *testing.T, queries *store.Queries, userID int64, titles []string) store.Feed {
	t.Helper()
	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: userID,
		Type:   "rss",
		Name:   "Sorted saved feed",
		Url:    "https://saved-sorted.example.com/feed",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	for i, title := range titles {
		// Later titles get LATER publish times, so index order == publish order.
		createSeedItem(t, queries, feed.ID, fmt.Sprintf("sorted-%03d", i), title, "saved",
			time.Date(2024, 3, 1+i, 12, 0, 0, 0, time.UTC))
	}
	return feed
}

func savedTitles(page savedItemsPage) []string {
	titles := make([]string, len(page.Items))
	for i, it := range page.Items {
		titles[i], _ = it["title"].(string)
	}
	return titles
}

// TestListSavedItemsSortsOldestFirst pins the reported bug: choosing "Oldest" in
// the Saved view changed the label but left the list newest-first, because the
// API only ever ordered by published_at DESC while the client's sort function
// returned the list unchanged. The server now honors the direction.
func TestListSavedItemsSortsOldestFirst(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "saved-sort-asc@example.com", "password123")
	token := getTestToken(t, server.auth, &user)
	seedSavedItemsAt(t, queries, user.ID, []string{"First", "Second", "Third", "Fourth"})

	page := fetchSavedPage(t, server, token, "?sort=date_asc")
	want := []string{"First", "Second", "Third", "Fourth"}
	if got := savedTitles(page); !equalStrings(got, want) {
		t.Errorf("date_asc order = %v, want %v", got, want)
	}
}

// TestListSavedItemsDefaultsToNewestFirst is the control: the default (and an
// explicit date_desc) must stay exactly as it is today, so existing callers that
// send no sort are unaffected.
func TestListSavedItemsDefaultsToNewestFirst(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "saved-sort-desc@example.com", "password123")
	token := getTestToken(t, server.auth, &user)
	seedSavedItemsAt(t, queries, user.ID, []string{"First", "Second", "Third", "Fourth"})

	want := []string{"Fourth", "Third", "Second", "First"}
	for _, query := range []string{"", "?sort=date_desc"} {
		page := fetchSavedPage(t, server, token, query)
		if got := savedTitles(page); !equalStrings(got, want) {
			t.Errorf("query %q order = %v, want %v", query, got, want)
		}
	}
}

// TestListSavedItemsSortPaginatesConsistently proves the direction survives
// paging: walking the pages in ascending order must cover every item exactly
// once, in ascending order. A tie-break that disagrees with the primary key
// would duplicate or skip rows across a page boundary.
func TestListSavedItemsSortPaginatesConsistently(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "saved-sort-page@example.com", "password123")
	token := getTestToken(t, server.auth, &user)
	seedSavedItemsAt(t, queries, user.ID, []string{"A", "B", "C", "D", "E"})

	var got []string
	for offset := 0; ; offset += 2 {
		page := fetchSavedPage(t, server, token, fmt.Sprintf("?sort=date_asc&limit=2&offset=%d", offset))
		got = append(got, savedTitles(page)...)
		if !page.HasMore {
			break
		}
		if offset > 20 {
			t.Fatal("paging did not terminate")
		}
	}
	want := []string{"A", "B", "C", "D", "E"}
	if !equalStrings(got, want) {
		t.Errorf("paged date_asc order = %v, want %v", got, want)
	}
}

// TestListSavedItemsSortAppliesToFilteredList keeps the sort and the read-state
// filter independent: sorting must not drop the filter, and the filter must not
// pin the order.
func TestListSavedItemsSortAppliesToFilteredList(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "saved-sort-filter@example.com", "password123")
	token := getTestToken(t, server.auth, &user)
	seedSavedItemsAt(t, queries, user.ID, []string{"One", "Two", "Three"})

	// seedSavedItemsAt saves with mark_read, so every row is read. A limit is
	// passed so the response carries the filter-aware total alongside the order.
	page := fetchSavedPage(t, server, token, "?sort=date_asc&status=read&limit=50")
	want := []string{"One", "Two", "Three"}
	if got := savedTitles(page); !equalStrings(got, want) {
		t.Errorf("filtered date_asc order = %v, want %v", got, want)
	}
	if page.Total != 3 {
		t.Errorf("total = %d, want 3", page.Total)
	}
}

// TestListSavedItemsRejectsUnsupportedSort guards the contract. The Saved view
// mixes feed types, so a cross-feed score ordering is not meaningful there and
// the UI does not offer it. Accepting the value while ignoring it is precisely
// the bug being fixed (the label would lie), so it must be rejected.
func TestListSavedItemsRejectsUnsupportedSort(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "saved-sort-bad@example.com", "password123")
	token := getTestToken(t, server.auth, &user)
	seedSavedItemsAt(t, queries, user.ID, []string{"Only"})

	for _, sort := range []string{"score_desc", "score_asc", "sideways"} {
		rec := makeRequest(server, http.MethodGet, "/api/saved?sort="+sort, nil, token)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("sort=%s returned %d, want 400", sort, rec.Code)
		}
	}
}
