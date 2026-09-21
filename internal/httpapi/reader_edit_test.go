package httpapi

import (
	"net/http"
	"net/url"
	"testing"

	"digesterr/internal/store"
)

// editSeed is the fixture for edit-tag / mark-all-as-read tests. Feed A holds
// items in various states, feed B holds a single unread item (used to assert
// per-feed isolation), and cfg holds two digest results (one new, one read).
type editSeed struct {
	feedA   store.Feed
	feedB   store.Feed
	cfg     store.DigestConfig
	aUnread store.Item
	aRead   store.Item
	aSaved  store.Item
	bUnread store.Item
	drNew   store.DigestResult
	drRead  store.DigestResult
}

func seedEditState(t *testing.T, queries *store.Queries, userID int64) editSeed {
	t.Helper()
	var seed editSeed

	var err error
	seed.feedA, err = queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: userID,
		Type:   "rss",
		Name:   "Feed A",
		Url:    "https://a.example.com/feed",
		Status: "active",
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

	seed.aUnread = createSeedItem(t, queries, seed.feedA.ID, "a-unread", "A unread", "unread", streamTimes.aUnreadNewest)
	seed.aRead = createSeedItem(t, queries, seed.feedA.ID, "a-read", "A read", "read", streamTimes.aRead)
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

	return seed
}

// itemStatus returns an item's READ state ("unread"/"read"/"digested").
// Saved is a flag now, not a status, so use itemIsSaved for that.
func itemStatus(t *testing.T, queries *store.Queries, id int64) string {
	t.Helper()
	item, err := queries.GetItem(t.Context(), id)
	if err != nil {
		t.Fatalf("failed to get item %d: %v", id, err)
	}
	return item.Status
}

// itemIsSaved reports the saved FLAG, which is independent of read state.
func itemIsSaved(t *testing.T, queries *store.Queries, id int64) bool {
	t.Helper()
	item, err := queries.GetItem(t.Context(), id)
	if err != nil {
		t.Fatalf("failed to get item %d: %v", id, err)
	}
	return item.IsSaved != 0
}

func digestStatus(t *testing.T, queries *store.Queries, id, userID int64) string {
	t.Helper()
	dr, err := queries.GetDigestResult(t.Context(), store.GetDigestResultParams{ID: id, UserID: userID})
	if err != nil {
		t.Fatalf("failed to get digest result %d: %v", id, err)
	}
	return dr.Status
}

// postReaderForm POSTs a form-encoded body to a reader endpoint with valid
// auth and asserts a 200 response with body "OK".
func postReaderForm(t *testing.T, server *Server, path string, form url.Values) {
	t.Helper()
	w := readerRequest(server, http.MethodPost, path, form.Encode(), map[string]string{
		"Authorization": "GoogleLogin auth=testkey123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("%s: expected 200, got %d: %s", path, w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "OK" {
		t.Fatalf("%s: expected body %q, got %q", path, "OK", got)
	}
}

func editTagForm(add, remove string, ids ...string) url.Values {
	form := url.Values{}
	for _, id := range ids {
		form.Add("i", id)
	}
	if add != "" {
		form.Add("a", add)
	}
	if remove != "" {
		form.Add("r", remove)
	}
	return form
}

func TestReaderEditTagAddRead(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedEditState(t, queries, user.ID)

	postReaderForm(t, server, "/reader/api/0/edit-tag", editTagForm(readerTagRead, "", itemStreamID(seed.aUnread.ID)))

	if got := itemStatus(t, queries, seed.aUnread.ID); got != "read" {
		t.Fatalf("expected unread item to become read, got %q", got)
	}
}

func TestReaderEditTagRemoveRead(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedEditState(t, queries, user.ID)

	postReaderForm(t, server, "/reader/api/0/edit-tag", editTagForm("", readerTagRead, itemStreamID(seed.aRead.ID)))

	if got := itemStatus(t, queries, seed.aRead.ID); got != "unread" {
		t.Fatalf("expected read item to become unread, got %q", got)
	}
}

func TestReaderEditTagAddStarred(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedEditState(t, queries, user.ID)

	postReaderForm(t, server, "/reader/api/0/edit-tag", editTagForm(readerTagStarred, "", itemStreamID(seed.aUnread.ID)))

	// Starred is a FLAG now, so it is asserted on its own field — not by the
	// status string it used to occupy.
	if !itemIsSaved(t, queries, seed.aUnread.ID) {
		t.Fatal("expected unread item to become saved")
	}
	// Saving reads an unread item, preserving the pre-flag behaviour where a
	// saved item was absent from the unread counts.
	if got := itemStatus(t, queries, seed.aUnread.ID); got != "read" {
		t.Fatalf("expected saving an unread item to mark it read, got %q", got)
	}
}

// The whole point of the flag: a read-state change must not destroy saved-ness.
// This is the bug the user reported ("mark unread removes it from saved").
func TestReaderEditTagRemoveReadKeepsStarred(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedEditState(t, queries, user.ID)

	postReaderForm(t, server, "/reader/api/0/edit-tag", editTagForm("", readerTagRead, itemStreamID(seed.aSaved.ID)))

	if itemIsSaved(t, queries, seed.aSaved.ID) != true {
		t.Fatal("marking a starred item unread unstarred it")
	}
	if got := itemStatus(t, queries, seed.aSaved.ID); got != "unread" {
		t.Fatalf("expected starred item to become unread, got %q", got)
	}
}

func TestReaderEditTagRemoveStarred(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedEditState(t, queries, user.ID)

	postReaderForm(t, server, "/reader/api/0/edit-tag", editTagForm("", readerTagStarred, itemStreamID(seed.aSaved.ID)))

	if itemIsSaved(t, queries, seed.aSaved.ID) {
		t.Fatal("expected the saved flag to be cleared")
	}
	// Unstarring no longer writes read state at all — it used to force 'read',
	// which is what made it destructive. The seeded item was 'read' already, so
	// the assertion is that it is unchanged, not that it was set.
	if got := itemStatus(t, queries, seed.aSaved.ID); got != "read" {
		t.Fatalf("expected unstarred item to keep its read state, got %q", got)
	}
}

func TestReaderEditTagDigestAddRead(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedEditState(t, queries, user.ID)

	postReaderForm(t, server, "/reader/api/0/edit-tag", editTagForm(readerTagRead, "", digestItemStreamID(seed.drNew.ID)))

	if got := digestStatus(t, queries, seed.drNew.ID, user.ID); got != "read" {
		t.Fatalf("expected new digest result to become read, got %q", got)
	}
}

func TestReaderEditTagDigestAddStarredNoOp(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedEditState(t, queries, user.ID)

	postReaderForm(t, server, "/reader/api/0/edit-tag", editTagForm(readerTagStarred, "", digestItemStreamID(seed.drNew.ID)))

	if got := digestStatus(t, queries, seed.drNew.ID, user.ID); got != "new" {
		t.Fatalf("expected digest result status unchanged, got %q", got)
	}
}

func TestReaderEditTagMultipleIDs(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedEditState(t, queries, user.ID)

	postReaderForm(t, server, "/reader/api/0/edit-tag",
		editTagForm(readerTagRead, "", itemStreamID(seed.aUnread.ID), itemStreamID(seed.bUnread.ID), digestItemStreamID(seed.drNew.ID)))

	if got := itemStatus(t, queries, seed.aUnread.ID); got != "read" {
		t.Fatalf("expected item A to become read, got %q", got)
	}
	if got := itemStatus(t, queries, seed.bUnread.ID); got != "read" {
		t.Fatalf("expected item B to become read, got %q", got)
	}
	if got := digestStatus(t, queries, seed.drNew.ID, user.ID); got != "read" {
		t.Fatalf("expected digest result to become read, got %q", got)
	}
}

func TestReaderEditTagUnknownTagNoOp(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedEditState(t, queries, user.ID)

	postReaderForm(t, server, "/reader/api/0/edit-tag", editTagForm("user/-/label/Tech", "", itemStreamID(seed.aUnread.ID)))

	if got := itemStatus(t, queries, seed.aUnread.ID); got != "unread" {
		t.Fatalf("expected unknown tag to leave item unread, got %q", got)
	}
}

func TestReaderEditTagMissingI(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seedEditState(t, queries, user.ID)

	// Missing/empty `i` must still return 200 OK.
	postReaderForm(t, server, "/reader/api/0/edit-tag", editTagForm(readerTagRead, ""))
}

func TestReaderMarkAllAsReadFeed(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedEditState(t, queries, user.ID)

	postReaderForm(t, server, "/reader/api/0/mark-all-as-read", url.Values{
		"s": {feedStreamID(seed.feedA.ID)},
	})

	if got := itemStatus(t, queries, seed.aUnread.ID); got != "read" {
		t.Fatalf("expected feed A unread item read, got %q", got)
	}
	if got := itemStatus(t, queries, seed.bUnread.ID); got != "unread" {
		t.Fatalf("expected feed B unread item untouched, got %q", got)
	}
}

func TestReaderMarkAllAsReadDigest(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedEditState(t, queries, user.ID)

	postReaderForm(t, server, "/reader/api/0/mark-all-as-read", url.Values{
		"s": {digestStreamID(seed.cfg.ID)},
	})

	if got := digestStatus(t, queries, seed.drNew.ID, user.ID); got != "read" {
		t.Fatalf("expected new digest result read, got %q", got)
	}
	if got := digestStatus(t, queries, seed.drRead.ID, user.ID); got != "read" {
		t.Fatalf("expected already-read digest result to stay read, got %q", got)
	}
	// Items are untouched by a digest-scope mark-all-as-read.
	if got := itemStatus(t, queries, seed.aUnread.ID); got != "unread" {
		t.Fatalf("expected feed A unread item untouched, got %q", got)
	}
}

func TestReaderMarkAllAsReadGlobal(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedEditState(t, queries, user.ID)

	postReaderForm(t, server, "/reader/api/0/mark-all-as-read", url.Values{
		"s": {readerStreamReadingList},
	})

	if got := itemStatus(t, queries, seed.aUnread.ID); got != "read" {
		t.Fatalf("expected feed A unread item read, got %q", got)
	}
	if got := itemStatus(t, queries, seed.bUnread.ID); got != "read" {
		t.Fatalf("expected feed B unread item read, got %q", got)
	}
	// Already-read and saved items are left as-is.
	if got := itemStatus(t, queries, seed.aRead.ID); got != "read" {
		t.Fatalf("expected read item to stay read, got %q", got)
	}
	// A saved item keeps its FLAG across mark-all-read. Its read state may
	// legitimately flip (read state and savedness are independent), so the flag
	// is what is asserted here.
	if !itemIsSaved(t, queries, seed.aSaved.ID) {
		t.Fatal("expected saved item to stay saved")
	}
	if got := digestStatus(t, queries, seed.drNew.ID, user.ID); got != "read" {
		t.Fatalf("expected new digest result read, got %q", got)
	}
}

func TestReaderMarkAllAsReadGlobalEmptyStream(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedEditState(t, queries, user.ID)

	// Missing `s` falls back to global mark-all-read.
	postReaderForm(t, server, "/reader/api/0/mark-all-as-read", url.Values{})

	if got := itemStatus(t, queries, seed.aUnread.ID); got != "read" {
		t.Fatalf("expected feed A unread item read, got %q", got)
	}
	if got := digestStatus(t, queries, seed.drNew.ID, user.ID); got != "read" {
		t.Fatalf("expected new digest result read, got %q", got)
	}
}

func TestReaderMarkAllAsReadForeignFeed(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedEditState(t, queries, user.ID)

	other, err := queries.CreateUser(t.Context(), store.CreateUserParams{
		Email:        "other@example.com",
		PasswordHash: "unused",
		ApiKey:       "otherkey999",
	})
	if err != nil {
		t.Fatalf("failed to create other user: %v", err)
	}
	otherFeed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: other.ID,
		Type:   "rss",
		Name:   "Other Feed",
		Url:    "https://other.example.com/feed",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("failed to create other feed: %v", err)
	}

	// Marking a feed not owned by this user is a safe no-op returning 200.
	postReaderForm(t, server, "/reader/api/0/mark-all-as-read", url.Values{"s": {feedStreamID(otherFeed.ID)}})

	// Nothing owned by the user changed.
	if got := itemStatus(t, queries, seed.aUnread.ID); got != "unread" {
		t.Fatalf("expected unread item untouched, got %q", got)
	}
}

func TestReaderNoOpEndpoints(t *testing.T) {
	server, queries := setupTestServer(t)
	createReaderUser(t, queries, "testkey123")

	paths := []string{
		"/reader/api/0/subscription/quickadd",
		"/reader/api/0/subscription/edit",
		"/reader/api/0/rename-tag",
		"/reader/api/0/disable-tag",
	}
	for _, path := range paths {
		postReaderForm(t, server, path, url.Values{"ignored": {"value"}})
	}
}

func TestReaderEditTagUnauthorized(t *testing.T) {
	server, queries := setupTestServer(t)
	createReaderUser(t, queries, "testkey123")

	w := readerRequest(server, http.MethodPost, "/reader/api/0/edit-tag", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("edit-tag without key: expected 401, got %d", w.Code)
	}
}

func TestReaderMarkAllAsReadUnauthorized(t *testing.T) {
	server, queries := setupTestServer(t)
	createReaderUser(t, queries, "testkey123")

	w := readerRequest(server, http.MethodPost, "/reader/api/0/mark-all-as-read", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("mark-all-as-read without key: expected 401, got %d", w.Code)
	}
}

// A bulk sync (many IDs, two tags) applies atomically: items flip, digests
// flip, and the digest read cascade reaches linked items in the same request.
func TestReaderEditTagBatchAtomicity(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedEditState(t, queries, user.ID)

	// Link a digested item to the new digest result so the cascade has a
	// target: result read must flip it to read in the same request.
	digested := createSeedItem(t, queries, seed.feedA.ID, "a-digested", "A digested", "digested", streamTimes.aRead)
	if _, err := queries.CreateDigestResultItem(t.Context(), store.CreateDigestResultItemParams{
		ResultID: seed.drNew.ID, ItemID: digested.ID, IsHighlight: 1,
	}); err != nil {
		t.Fatalf("link digest item: %v", err)
	}

	postReaderForm(t, server, "/reader/api/0/edit-tag",
		editTagForm(readerTagRead, "",
			itemStreamID(seed.aUnread.ID), itemStreamID(seed.bUnread.ID),
			digestItemStreamID(seed.drNew.ID)))

	if got := itemStatus(t, queries, seed.aUnread.ID); got != "read" {
		t.Errorf("item A = %q, want read", got)
	}
	if got := itemStatus(t, queries, seed.bUnread.ID); got != "read" {
		t.Errorf("item B = %q, want read", got)
	}
	if got := digestStatus(t, queries, seed.drNew.ID, user.ID); got != "read" {
		t.Errorf("digest = %q, want read", got)
	}
	if got := itemStatus(t, queries, digested.ID); got != "read" {
		t.Errorf("cascaded item = %q, want read (digest read must flip linked items)", got)
	}
}

// Foreign IDs match nothing and do not abort the batch: the request still
// applies the caller's own IDs and answers 200 OK.
func TestReaderEditTagBatchSkipsForeignIDs(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedEditState(t, queries, user.ID)
	other := createTestUser(t, queries, "other-batch@example.com", "password123")
	foreignFeed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: other.ID, Type: "rss", Name: "Foreign", Url: "https://f.example.com/x",
		Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create foreign feed: %v", err)
	}
	foreign := createSeedItem(t, queries, foreignFeed.ID, "f-unread", "Foreign", "unread", streamTimes.aUnreadNewest)

	postReaderForm(t, server, "/reader/api/0/edit-tag",
		editTagForm(readerTagRead, "", itemStreamID(seed.aUnread.ID), itemStreamID(foreign.ID)))

	if got := itemStatus(t, queries, seed.aUnread.ID); got != "read" {
		t.Errorf("own item = %q, want read", got)
	}
	if got := itemStatus(t, queries, foreign.ID); got != "unread" {
		t.Errorf("foreign item = %q, want still unread", got)
	}
}

// The digest-scope mark-all-read flips results and their linked items
// together: a result must never sit read while its items stay digested.
func TestReaderMarkAllAsReadDigestCascadesAtomically(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedEditState(t, queries, user.ID)

	digested := createSeedItem(t, queries, seed.feedA.ID, "a-cascade", "Cascade", "digested", streamTimes.aRead)
	if _, err := queries.CreateDigestResultItem(t.Context(), store.CreateDigestResultItemParams{
		ResultID: seed.drNew.ID, ItemID: digested.ID,
	}); err != nil {
		t.Fatalf("link digest item: %v", err)
	}

	form := url.Values{}
	form.Set("s", digestStreamID(seed.cfg.ID))
	postReaderForm(t, server, "/reader/api/0/mark-all-as-read", form)

	if got := digestStatus(t, queries, seed.drNew.ID, user.ID); got != "read" {
		t.Errorf("digest = %q, want read", got)
	}
	if got := itemStatus(t, queries, digested.ID); got != "read" {
		t.Errorf("cascaded item = %q, want read", got)
	}
}
