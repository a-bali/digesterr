package feed

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"digesterr/internal/store"
	"digesterr/internal/testutil"
)

func TestItemHashStableAcrossTitleEdits(t *testing.T) {
	// Regression for the duplicate pair 4653/5059: the same HN story
	// (guid "hn:49291268") was fetched once as "NP-Overrated" and again as
	// "NP-overrated" after an HN title edit. The GUID is the item's identity;
	// a title edit must not change the hash, or the unique (feed_id, hash)
	// index lets the story in twice.
	h1 := itemHash("hn:49291268", "NP-Overrated", "https://gruhn.me/blog/2026-08-13/", "")
	h2 := itemHash("hn:49291268", "NP-overrated", "https://gruhn.me/blog/2026-08-13/", "")
	if h1 != h2 {
		t.Fatalf("hash changed after title edit: %s != %s", h1, h2)
	}
}

func TestItemHashDistinctGuidsDistinctHashes(t *testing.T) {
	// Distinct items must still hash differently — duplicate detection must
	// not collapse separate stories that share a title.
	h1 := itemHash("hn:49291268", "Same title", "https://example.com/a", "")
	h2 := itemHash("hn:49291269", "Same title", "https://example.com/b", "")
	if h1 == h2 {
		t.Fatal("different GUIDs must produce different hashes")
	}
}

func TestItemHashURLFallbackIgnoresTitle(t *testing.T) {
	// Without a GUID the URL is the identity; title edits must not duplicate.
	h1 := itemHash("", "Title A", "https://example.com/story", "")
	h2 := itemHash("", "Title B", "https://example.com/story", "")
	if h1 != h2 {
		t.Fatalf("URL fallback hash changed with title: %s != %s", h1, h2)
	}
}

func TestItemHashContentFallback(t *testing.T) {
	// With neither GUID nor URL, identity is title + stripped content:
	// different titles must differ, and HTML tags must be stripped before
	// hashing so equal plain content still matches.
	h1 := itemHash("", "", "", "<p>body</p>")
	h2 := itemHash("", "Title", "", "<p>body</p>")
	if h1 == h2 {
		t.Fatal("content fallback should distinguish titles")
	}
	h3 := itemHash("", "Title", "", "<p>body</p>")
	h4 := itemHash("", "Title", "", "body")
	if h3 != h4 {
		t.Fatal("content fallback should strip HTML before hashing")
	}
}

// mutableRSSBodyServer serves an RSS document whose body can be swapped
// between polls, simulating a publisher editing an item's title.
func mutableRSSBodyServer(t *testing.T) (string, func(string)) {
	t.Helper()
	var mu sync.Mutex
	body := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func(b string) {
		mu.Lock()
		defer mu.Unlock()
		body = b
	}
}

// TestPollFeedNoDuplicateAfterTitleEdit reproduces the 4653/5059 scenario: a
// feed item with a stable GUID is polled twice, and between polls the title
// changes (HN-style title edit). The second poll must NOT insert a second
// row — the GUID-based hash has to be unaffected by the title.
func TestPollFeedNoDuplicateAfterTitleEdit(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)

	rssURL, setBody := mutableRSSBodyServer(t)
	setBody(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>HN</title>
    <link>https://news.ycombinator.com/</link>
    <description>Hacker News</description>
    <item>
      <title>NP-Overrated</title>
      <link>https://gruhn.me/blog/2026-08-13/</link>
      <guid isPermaLink="false">https://news.ycombinator.com/item?id=49291268</guid>
      <pubDate>Thu, 13 Aug 2026 20:14:17 +0000</pubDate>
    </item>
  </channel>
</rss>`)

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "HN-like Feed",
		Url:    rssURL,
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	poller := NewPoller(queries)

	if err := poller.PollFeed(context.Background(), feedObj); err != nil {
		t.Fatalf("first PollFeed failed: %v", err)
	}

	// Publisher edits the title (case change, exactly like HN item 49291268).
	setBody(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>HN</title>
    <link>https://news.ycombinator.com/</link>
    <description>Hacker News</description>
    <item>
      <title>NP-overrated</title>
      <link>https://gruhn.me/blog/2026-08-13/</link>
      <guid isPermaLink="false">https://news.ycombinator.com/item?id=49291268</guid>
      <pubDate>Thu, 13 Aug 2026 20:14:17 +0000</pubDate>
    </item>
  </channel>
</rss>`)

	if err := poller.PollFeed(context.Background(), feedObj); err != nil {
		t.Fatalf("second PollFeed failed: %v", err)
	}

	items, err := queries.ListItemsByFeed(t.Context(), feedObj.ID)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item after title edit, got %d (duplicate inserted)", len(items))
	}
	// The poller never updates already-stored items; the point of the test is
	// that the title edit does not create a second row.
	if items[0].Title != "NP-Overrated" {
		t.Errorf("expected the original title to be preserved, got %q", items[0].Title)
	}
}

// TestExpireOldData verifies feed/digest data retention: items older than
// the configured window are deleted, saved items are exempt, items still
// referenced by a digest result are exempt, and digest results older than
// the window are deleted (their join rows cascade).
func TestExpireOldData(t *testing.T) {
	db, err := testutil.OpenTestDB()
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	queries := store.New(db)

	user, err := queries.CreateUser(t.Context(), store.CreateUserParams{
		Email:        "expiry@example.com",
		PasswordHash: "hash",
		ApiKey:       "expiry-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Expiry Feed",
		Url:    "https://example.com/feed.xml",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	mkItem := func(title, status string) store.Item {
		t.Helper()
		item, err := queries.CreateItem(t.Context(), store.CreateItemParams{
			FeedID:        feedObj.ID,
			Hash:          "h-" + title,
			Title:         title,
			Url:           "https://example.com/" + title,
			Content:       "content",
			CommentUrl:    "",
			Author:        "",
			PublishedAt:   sql.NullTime{},
			Points:        10,
			CommentsCount: 1,
			Status:        status,
		})
		if err != nil {
			t.Fatalf("create item %s: %v", title, err)
		}
		return item
	}

	// 48h old, in SQLite's DATETIME text format (matches _time_format=sqlite).
	oldTS := time.Now().Add(-48 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	setOld := func(id int64) {
		t.Helper()
		if _, err := db.Exec("UPDATE items SET created_at = ? WHERE id = ?", oldTS, id); err != nil {
			t.Fatalf("age item %d: %v", id, err)
		}
	}

	mkItem("fresh-unread", "unread")  // recent, kept
	old := mkItem("old-read", "read") // old, deleted
	setOld(old.ID)
	oldSaved := mkItem("old-saved", "saved") // old but saved, kept
	setOld(oldSaved.ID)
	linked := mkItem("old-linked", "digested") // old but in a digest, kept
	setOld(linked.ID)

	dcfg, err := queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Expiry Config",
		Schedule: "0 8 * * *",
		Config:   "{}",
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	freshResult, err := queries.CreateDigestResult(t.Context(), store.CreateDigestResultParams{
		UserID:         user.ID,
		ConfigID:       dcfg.ID,
		ConfigSnapshot: "{}",
		Content:        "{}",
		Status:         "new",
	})
	if err != nil {
		t.Fatalf("create fresh digest result: %v", err)
	}
	if _, err := queries.CreateDigestResultItem(t.Context(), store.CreateDigestResultItemParams{
		ResultID:    freshResult.ID,
		ItemID:      linked.ID,
		IsHighlight: 1,
	}); err != nil {
		t.Fatalf("link item to digest: %v", err)
	}

	oldResult, err := queries.CreateDigestResult(t.Context(), store.CreateDigestResultParams{
		UserID:         user.ID,
		ConfigID:       dcfg.ID,
		ConfigSnapshot: "{}",
		Content:        "{}",
		Status:         "new",
	})
	if err != nil {
		t.Fatalf("create old digest result: %v", err)
	}
	if _, err := db.Exec("UPDATE digest_results SET created_at = ? WHERE id = ?", oldTS, oldResult.ID); err != nil {
		t.Fatalf("age digest result: %v", err)
	}

	poller := NewPoller(queries)
	poller.SetExpiry(24*time.Hour, 24*time.Hour)

	itemsDeleted, digestsDeleted, err := poller.ExpireOldData(t.Context())
	if err != nil {
		t.Fatalf("ExpireOldData: %v", err)
	}
	if itemsDeleted != 1 {
		t.Errorf("items deleted = %d, want 1 (only old-read)", itemsDeleted)
	}
	if digestsDeleted != 1 {
		t.Errorf("digest results deleted = %d, want 1", digestsDeleted)
	}

	remaining, err := queries.ListItemsByFeed(t.Context(), feedObj.ID)
	if err != nil {
		t.Fatalf("list remaining items: %v", err)
	}
	got := make(map[string]bool, len(remaining))
	for _, it := range remaining {
		got[it.Title] = true
	}
	if !got["fresh-unread"] || !got["old-saved"] || !got["old-linked"] {
		t.Errorf("expected fresh-unread, old-saved, old-linked to remain, got %v", got)
	}
	if got["old-read"] {
		t.Error("old-read item survived expiry")
	}

	// The fresh digest result keeps its item link.
	links, err := queries.ListDigestResultItems(t.Context(), freshResult.ID)
	if err != nil {
		t.Fatalf("list fresh result items: %v", err)
	}
	if len(links) != 1 {
		t.Errorf("fresh digest result has %d linked items, want 1", len(links))
	}

	// The old digest result is gone.
	if _, err := queries.GetDigestResult(t.Context(), store.GetDigestResultParams{ID: oldResult.ID, UserID: user.ID}); err == nil {
		t.Error("old digest result survived expiry")
	}

	// Zero windows disable expiry entirely.
	poller.SetExpiry(0, 0)
	itemsDeleted, digestsDeleted, err = poller.ExpireOldData(t.Context())
	if err != nil {
		t.Fatalf("ExpireOldData (disabled): %v", err)
	}
	if itemsDeleted != 0 || digestsDeleted != 0 {
		t.Errorf("disabled expiry deleted items=%d digests=%d, want 0/0", itemsDeleted, digestsDeleted)
	}
}
