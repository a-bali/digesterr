package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"digesterr/internal/auth"
	"digesterr/internal/config"
	"digesterr/internal/logbuf"
	"digesterr/internal/store"
)

// setupStreamTestServer mirrors setupTestServer but also returns the raw
// *sql.DB so tests can set deterministic timestamps (digest result created_at
// has no query-level setter).
func setupStreamTestServer(t *testing.T) (*Server, *store.Queries, *sql.DB) {
	t.Helper()
	db, err := openTestDB()
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	queries := store.New(db)
	authSvc := auth.NewService("test-jwt-secret-for-testing", queries)
	return NewServer(db, queries, authSvc, logbuf.New(100), config.Default()), queries, db
}

// streamSeed holds the rows created for a stream test, so tests can assert
// against stable IDs.
type streamSeed struct {
	feedA         store.Feed
	feedB         store.Feed
	cfg           store.DigestConfig
	aUnreadNewest store.Item
	aUnreadOldest store.Item
	aRead         store.Item
	aDigested     store.Item
	aSaved        store.Item
	bUnread       store.Item
	drNew         store.DigestResult
	drRead        store.DigestResult
}

// streamTimes are the controlled timestamps used across the stream tests.
var streamTimes = struct {
	aUnreadNewest time.Time // 2024-01-05
	aRead         time.Time // 2024-01-04
	drNew         time.Time // 2024-01-04 12:00
	aUnreadOldest time.Time // 2024-01-03
	aDigested     time.Time // 2024-01-02
	bUnread       time.Time // 2024-01-01
	aSaved        time.Time // 2024-01-06
	drRead        time.Time // 2024-01-07
}{
	aUnreadNewest: time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
	aRead:         time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC),
	drNew:         time.Date(2024, 1, 4, 12, 0, 0, 0, time.UTC),
	aUnreadOldest: time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
	aDigested:     time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
	bUnread:       time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	aSaved:        time.Date(2024, 1, 6, 0, 0, 0, 0, time.UTC),
	drRead:        time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC),
}

func createSeedItem(t *testing.T, queries *store.Queries, feedID int64, hash, title, status string, published time.Time) store.Item {
	t.Helper()
	item, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID:      feedID,
		Hash:        hash,
		Title:       title,
		Url:         "https://example.com/" + hash,
		Status:      status,
		PublishedAt: sql.NullTime{Time: published, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create item %q: %v", hash, err)
	}
	return item
}

func setDigestCreatedAt(t *testing.T, db *sql.DB, id int64, ts time.Time) {
	t.Helper()
	if _, err := db.Exec("UPDATE digest_results SET created_at = ? WHERE id = ?", ts, id); err != nil {
		t.Fatalf("failed to set digest created_at: %v", err)
	}
}

func seedReaderStream(t *testing.T, queries *store.Queries, db *sql.DB, userID int64) streamSeed {
	t.Helper()
	var seed streamSeed

	var err error

	seed.feedA, err = queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID:   userID,
		Category: "Tech",
		Type:     "rss",
		Name:     "Feed A",
		Url:      "https://a.example.com/feed",
		Status:   "active",
	})
	if err != nil {
		t.Fatalf("failed to create feed A: %v", err)
	}

	seed.feedB, err = queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: userID,
		Type:   "rss",
		Name:   "Feed B",
		Url:    "https://b.example.com/feed",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("failed to create feed B: %v", err)
	}

	seed.aUnreadNewest = createSeedItem(t, queries, seed.feedA.ID, "a-unread-newest", "A unread newest", "unread", streamTimes.aUnreadNewest)
	seed.aUnreadOldest = createSeedItem(t, queries, seed.feedA.ID, "a-unread-oldest", "A unread oldest", "unread", streamTimes.aUnreadOldest)
	seed.aRead = createSeedItem(t, queries, seed.feedA.ID, "a-read", "A read", "read", streamTimes.aRead)
	seed.aDigested = createSeedItem(t, queries, seed.feedA.ID, "a-digested", "A digested", "digested", streamTimes.aDigested)
	seed.aSaved = createSeedItem(t, queries, seed.feedA.ID, "a-saved", "A saved", "saved", streamTimes.aSaved)
	seed.bUnread = createSeedItem(t, queries, seed.feedB.ID, "b-unread", "B unread", "unread", streamTimes.bUnread)

	seed.cfg, err = queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID:   userID,
		Name:     "Daily",
		Schedule: "daily",
		Config:   "{}",
	})
	if err != nil {
		t.Fatalf("failed to create digest config: %v", err)
	}

	seed.drNew, err = queries.CreateDigestResult(t.Context(), store.CreateDigestResultParams{
		UserID:         userID,
		ConfigID:       seed.cfg.ID,
		ConfigSnapshot: "{}",
		Content:        "{}",
		Status:         "new",
	})
	if err != nil {
		t.Fatalf("failed to create new digest result: %v", err)
	}
	setDigestCreatedAt(t, db, seed.drNew.ID, streamTimes.drNew)

	seed.drRead, err = queries.CreateDigestResult(t.Context(), store.CreateDigestResultParams{
		UserID:         userID,
		ConfigID:       seed.cfg.ID,
		ConfigSnapshot: "{}",
		Content:        "{}",
		Status:         "read",
	})
	if err != nil {
		t.Fatalf("failed to create read digest result: %v", err)
	}
	setDigestCreatedAt(t, db, seed.drRead.ID, streamTimes.drRead)

	return seed
}

type streamItemRef struct {
	ID              string   `json:"id"`
	TimestampUsec   string   `json:"timestampUsec"`
	DirectStreamIDs []string `json:"directStreamIds"`
}

type streamResponse struct {
	ItemRefs     []streamItemRef `json:"itemRefs"`
	Continuation *string         `json:"continuation"`
}

func getStream(t *testing.T, server *Server, path string) streamResponse {
	t.Helper()
	w := readerRequest(server, http.MethodGet, path, "", map[string]string{
		"Authorization": "GoogleLogin auth=testkey123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp streamResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return resp
}

func refIDs(refs []streamItemRef) []string {
	ids := make([]string, len(refs))
	for i, r := range refs {
		ids[i] = r.ID
	}
	return ids
}

func TestReaderStreamReadingList(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedReaderStream(t, queries, db, user.ID)

	resp := getStream(t, server, "/reader/api/0/stream/items/ids?s=user/-/state/com.google/reading-list&output=json")

	want := []string{
		itemStreamID(seed.aUnreadNewest.ID), // 2024-01-05
		digestItemStreamID(seed.drNew.ID),   // 2024-01-04 12:00
		itemStreamID(seed.aUnreadOldest.ID), // 2024-01-03
		itemStreamID(seed.bUnread.ID),       // 2024-01-01
	}
	if got := refIDs(resp.ItemRefs); !equalStrings(got, want) {
		t.Fatalf("reading-list ids mismatch:\n got %v\nwant %v", got, want)
	}

	// Verify timestamps for each entry.
	useByID := map[string]string{}
	for _, r := range resp.ItemRefs {
		useByID[r.ID] = r.TimestampUsec
	}
	if useByID[itemStreamID(seed.aUnreadNewest.ID)] != itoa(streamTimes.aUnreadNewest.UnixMicro()) {
		t.Fatalf("unexpected timestampUsec for newest unread item")
	}
	if useByID[digestItemStreamID(seed.drNew.ID)] != itoa(streamTimes.drNew.UnixMicro()) {
		t.Fatalf("unexpected timestampUsec for digest result")
	}

	// directStreamIds must be an empty array (not null).
	for _, r := range resp.ItemRefs {
		if r.DirectStreamIDs == nil || len(r.DirectStreamIDs) != 0 {
			t.Fatalf("expected empty directStreamIds array, got %v", r.DirectStreamIDs)
		}
	}

	// fresh is an alias of reading-list.
	fresh := getStream(t, server, "/reader/api/0/stream/items/ids?s=user/-/state/com.google/fresh&output=json")
	if got := refIDs(fresh.ItemRefs); !equalStrings(got, want) {
		t.Fatalf("fresh stream should match reading-list:\n got %v\nwant %v", got, want)
	}
}

func TestReaderStreamRead(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedReaderStream(t, queries, db, user.ID)

	resp := getStream(t, server, "/reader/api/0/stream/items/ids?s=user/-/state/com.google/read&output=json")

	want := []string{
		digestItemStreamID(seed.drRead.ID),  // 2024-01-07
		itemStreamID(seed.aRead.ID),         // 2024-01-04
		itemStreamID(seed.aDigested.ID),     // 2024-01-02 (digested maps to read)
	}
	if got := refIDs(resp.ItemRefs); !equalStrings(got, want) {
		t.Fatalf("read stream ids mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestReaderStreamStarred(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedReaderStream(t, queries, db, user.ID)

	resp := getStream(t, server, "/reader/api/0/stream/items/ids?s=user/-/state/com.google/starred&output=json")

	want := []string{itemStreamID(seed.aSaved.ID)}
	if got := refIDs(resp.ItemRefs); !equalStrings(got, want) {
		t.Fatalf("starred stream ids mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestReaderStreamFeed(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedReaderStream(t, queries, db, user.ID)

	resp := getStream(t, server, "/reader/api/0/stream/items/ids?s="+feedStreamID(seed.feedA.ID)+"&output=json")

	want := []string{
		itemStreamID(seed.aSaved.ID),       // 2024-01-06
		itemStreamID(seed.aUnreadNewest.ID), // 2024-01-05
		itemStreamID(seed.aRead.ID),         // 2024-01-04
		itemStreamID(seed.aUnreadOldest.ID), // 2024-01-03
		itemStreamID(seed.aDigested.ID),     // 2024-01-02
	}
	if got := refIDs(resp.ItemRefs); !equalStrings(got, want) {
		t.Fatalf("feed stream ids mismatch:\n got %v\nwant %v", got, want)
	}

	// Feed B has a single item.
	respB := getStream(t, server, "/reader/api/0/stream/items/ids?s="+feedStreamID(seed.feedB.ID)+"&output=json")
	if want := []string{itemStreamID(seed.bUnread.ID)}; !equalStrings(refIDs(respB.ItemRefs), want) {
		t.Fatalf("feed B stream ids mismatch:\n got %v\nwant %v", refIDs(respB.ItemRefs), want)
	}
}

func TestReaderStreamDigest(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedReaderStream(t, queries, db, user.ID)

	resp := getStream(t, server, "/reader/api/0/stream/items/ids?s="+digestStreamID(seed.cfg.ID)+"&output=json")

	want := []string{
		digestItemStreamID(seed.drRead.ID), // 2024-01-07
		digestItemStreamID(seed.drNew.ID),  // 2024-01-04 12:00
	}
	if got := refIDs(resp.ItemRefs); !equalStrings(got, want) {
		t.Fatalf("digest stream ids mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestReaderStreamLabel(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedReaderStream(t, queries, db, user.ID)

	resp := getStream(t, server, "/reader/api/0/stream/items/ids?s=user/-/label/Tech&output=json")

	// Tech category holds only feed A, so this matches feed A's items.
	want := []string{
		itemStreamID(seed.aSaved.ID),        // 2024-01-06
		itemStreamID(seed.aUnreadNewest.ID), // 2024-01-05
		itemStreamID(seed.aRead.ID),         // 2024-01-04
		itemStreamID(seed.aUnreadOldest.ID), // 2024-01-03
		itemStreamID(seed.aDigested.ID),     // 2024-01-02
	}
	if got := refIDs(resp.ItemRefs); !equalStrings(got, want) {
		t.Fatalf("label stream ids mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestReaderStreamPagination(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedReaderStream(t, queries, db, user.ID)

	// Reading-list has 4 entries. n=2 returns the first two plus a continuation.
	page1 := getStream(t, server, "/reader/api/0/stream/items/ids?s=user/-/state/com.google/reading-list&n=2&output=json")
	if len(page1.ItemRefs) != 2 {
		t.Fatalf("expected 2 itemRefs, got %d", len(page1.ItemRefs))
	}
	if page1.Continuation == nil || *page1.Continuation != "2" {
		t.Fatalf("expected continuation %q, got %v", "2", page1.Continuation)
	}

	wantPage1 := []string{
		itemStreamID(seed.aUnreadNewest.ID),
		digestItemStreamID(seed.drNew.ID),
	}
	if got := refIDs(page1.ItemRefs); !equalStrings(got, wantPage1) {
		t.Fatalf("page 1 ids mismatch:\n got %v\nwant %v", got, wantPage1)
	}

	// Passing c=<continuation> returns the next page with no overlap.
	page2 := getStream(t, server, "/reader/api/0/stream/items/ids?s=user/-/state/com.google/reading-list&n=2&c=2&output=json")
	wantPage2 := []string{
		itemStreamID(seed.aUnreadOldest.ID),
		itemStreamID(seed.bUnread.ID),
	}
	if got := refIDs(page2.ItemRefs); !equalStrings(got, wantPage2) {
		t.Fatalf("page 2 ids mismatch:\n got %v\nwant %v", got, wantPage2)
	}
	if page2.Continuation != nil {
		t.Fatalf("expected continuation to be omitted on final page, got %q", *page2.Continuation)
	}
}

func TestReaderStreamOldestFirst(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedReaderStream(t, queries, db, user.ID)

	resp := getStream(t, server, "/reader/api/0/stream/items/ids?s=user/-/state/com.google/reading-list&r=o&output=json")

	want := []string{
		itemStreamID(seed.bUnread.ID),       // 2024-01-01
		itemStreamID(seed.aUnreadOldest.ID), // 2024-01-03
		digestItemStreamID(seed.drNew.ID),   // 2024-01-04 12:00
		itemStreamID(seed.aUnreadNewest.ID), // 2024-01-05
	}
	if got := refIDs(resp.ItemRefs); !equalStrings(got, want) {
		t.Fatalf("oldest-first ids mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestReaderStreamUnknownStream(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seedReaderStream(t, queries, db, user.ID)

	resp := getStream(t, server, "/reader/api/0/stream/items/ids?s=user/-/state/com.google/unknown&output=json")
	if len(resp.ItemRefs) != 0 {
		t.Fatalf("expected empty itemRefs for unknown stream, got %v", refIDs(resp.ItemRefs))
	}
	if resp.Continuation != nil {
		t.Fatalf("expected omitted continuation, got %q", *resp.Continuation)
	}
}

func TestReaderStreamUnauthorized(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seedReaderStream(t, queries, db, user.ID)

	// No key supplied.
	w := readerRequest(server, http.MethodGet, "/reader/api/0/stream/items/ids?s=user/-/state/com.google/reading-list", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without key, got %d", w.Code)
	}

	// Unknown key.
	w = readerRequest(server, http.MethodGet, "/reader/api/0/stream/items/ids?s=user/-/state/com.google/reading-list", "", map[string]string{
		"Authorization": "GoogleLogin auth=wrongkey",
	})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with unknown key, got %d", w.Code)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
