package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"digesterr/internal/store"
)

// timeZero predates any test row's fetched_at, so the enrichment listing
// returns every unread item on the feed.
func timeZero() time.Time { return time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC) }

// A low-score unread item on a min_score feed must be hidden from the
// reading-list, label and per-feed streams, but still visible starred and by
// direct id.
func TestReaderStreamRespectsFeedMinScore(t *testing.T) {
	server, queries, _ := setupStreamTestServer(t)
	user := createReaderUserWithEmail(t, queries, "testkey123", "readermin@example.com")
	strict := seedThresholdFeed(t, queries, user, "rstrict", 50)
	open := seedThresholdFeed(t, queries, user, "ropen", 0)

	// Per-feed streams: strict shows exact+high only, open shows all 4.
	if got := getStream(t, server, "/reader/api/0/stream/items/ids?s=feed/"+itoa(strict.ID)); len(got.ItemRefs) != 2 {
		t.Fatalf("strict feed stream = %d refs, want 2", len(got.ItemRefs))
	}
	if got := getStream(t, server, "/reader/api/0/stream/items/ids?s=feed/"+itoa(open.ID)); len(got.ItemRefs) != 4 {
		t.Fatalf("open feed stream = %d refs, want 4", len(got.ItemRefs))
	}

	// Reading-list stream: 2 (strict) + 4 (open) = 6.
	rl := "/reader/api/0/stream/items/ids?s=" + url.QueryEscape("user/-/state/com.google/reading-list")
	if got := getStream(t, server, rl); len(got.ItemRefs) != 6 {
		t.Fatalf("reading-list stream = %d refs, want 6 (threshold feed contributes exact+high)", len(got.ItemRefs))
	}

	// Label stream for a category shared by both feeds filters per row:
	// open contributes all 4, strict contributes exact+high only.
	strictCat, err := queries.UpdateFeed(t.Context(), store.UpdateFeedParams{
		Name:           strict.Name,
		Url:            strict.Url,
		Config:         strict.Config,
		Category:       "Shared",
		Status:         strict.Status,
		ExtractContent: strict.ExtractContent,
		MinScore:       strict.MinScore,
		Readability:    strict.Readability,
		ID:             strict.ID,
		UserID:         user.ID,
	})
	if err != nil {
		t.Fatalf("set strict category: %v", err)
	}
	openCat, err := queries.UpdateFeed(t.Context(), store.UpdateFeedParams{
		Name:           open.Name,
		Url:            open.Url,
		Config:         open.Config,
		Category:       "Shared",
		Status:         open.Status,
		ExtractContent: open.ExtractContent,
		MinScore:       open.MinScore,
		Readability:    open.Readability,
		ID:             open.ID,
		UserID:         user.ID,
	})
	if err != nil {
		t.Fatalf("set open category: %v", err)
	}
	_, _ = strictCat, openCat
	label := "/reader/api/0/stream/items/ids?s=" + url.QueryEscape("user/-/label/Shared")
	if got := getStream(t, server, label); len(got.ItemRefs) != 6 {
		t.Fatalf("label stream = %d refs, want 6 (2 strict + 4 open)", len(got.ItemRefs))
	}

	// Starred stream is exempt from the threshold (same rule as the web Saved
	// view): save the low-score item and it must appear. Resolve its id
	// through the unread-enrichment listing.
	existing, err := queries.ListUnreadItemsForEnrichment(t.Context(), store.ListUnreadItemsForEnrichmentParams{
		FeedID:         strict.ID,
		FetchedAt:      timeZero(),
		LastEnrichedAt: sql.NullTime{},
	})
	if err != nil || len(existing) == 0 {
		t.Fatalf("list strict items: %v", err)
	}
	lowID := int64(0)
	for _, it := range existing {
		if it.Hash == "rstrict-low" {
			lowID = it.ID
		}
	}
	if lowID == 0 {
		t.Fatalf("low item not found among %d strict items", len(existing))
	}
	if _, err := queries.SetItemSaved(t.Context(), store.SetItemSavedParams{ID: lowID, Saved: 1, MarkRead: 0}); err != nil {
		t.Fatalf("save low item: %v", err)
	}
	starred := "/reader/api/0/stream/items/ids?s=" + url.QueryEscape("user/-/state/com.google/starred")
	if got := getStream(t, server, starred); len(got.ItemRefs) != 1 {
		t.Fatalf("starred stream = %d refs, want 1 (saved low-score item)", len(got.ItemRefs))
	}
}

// Reader unread counts stay unfiltered: the protocol has no min_score knob,
// so third-party clients see every unread item while the web UI filters.
func TestReaderUnreadCountIgnoresFeedMinScore(t *testing.T) {
	server, queries, _ := setupStreamTestServer(t)
	user := createReaderUserWithEmail(t, queries, "rcountkey", "readercount@example.com")
	rcount := seedThresholdFeed(t, queries, user, "rcount", 50)

	w := readerRequest(server, http.MethodGet, "/reader/api/0/unread-count?output=json", "", map[string]string{
		"Authorization": "GoogleLogin auth=rcountkey",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		UnreadCounts []struct {
			ID    string `json:"id"`
			Count int64  `json:"count"`
		} `json:"unreadcounts"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode reader counts: %v", err)
	}
	found := false
	for _, e := range resp.UnreadCounts {
		if e.ID == "feed/"+itoa(rcount.ID) {
			found = true
			if e.Count != 4 {
				t.Fatalf("reader count = %d, want 4 (unfiltered)", e.Count)
			}
		}
	}
	if !found {
		t.Fatalf("no feed entry in reader unread-count")
	}

	// The web-UI counts endpoint on the same data reports the filtered 2.
	token := getTestToken(t, server.auth, &user)
	w2 := makeRequest(server, http.MethodGet, "/api/unread-counts", nil, token)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
	var strCounts map[string]int64
	if err := json.NewDecoder(w2.Body).Decode(&strCounts); err != nil {
		t.Fatalf("decode web counts: %v", err)
	}
	if strCounts[itoa(rcount.ID)] != 2 {
		t.Fatalf("web unread-count = %d, want 2 (filtered)", strCounts[itoa(rcount.ID)])
	}
}

// createReaderUserWithEmail mirrors createReaderUser with a distinct email so
// two reader tests can coexist without colliding on users.email.
func createReaderUserWithEmail(t *testing.T, queries *store.Queries, apiKey, email string) store.User {
	t.Helper()
	user, err := queries.CreateUser(t.Context(), store.CreateUserParams{
		Email:        email,
		PasswordHash: "unused",
		ApiKey:       apiKey,
	})
	if err != nil {
		t.Fatalf("failed to create reader test user: %v", err)
	}
	return user
}
