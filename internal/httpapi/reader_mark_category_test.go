package httpapi

import (
	"net/url"
	"testing"

	"digesterr/internal/store"
)

// TestReaderMarkAllAsReadCategoryIsScopedToCategory is the regression for a
// destructive scope bug.
//
// Reader streams advertise a label id per feed category (`user/-/label/<name>`,
// see readerLabelID), so a client can mark a whole category read. The handler
// only special-cased `feed/` and `digest/<id>` and sent everything else —
// including label ids — to the GLOBAL branch, which marks every item in the
// account read AND every digest result read. So "mark this category read"
// silently marked the entire account read.
//
// The three assertions below pin the scope: the category's own items go read,
// items outside it do not, and no digest result is touched.
func TestReaderMarkAllAsReadCategoryIsScopedToCategory(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")

	// Two categories, one feed each, one unread item each.
	categorized := createCategoryFeed(t, queries, user.ID, "News", "https://news.example.com/feed")
	other := createCategoryFeed(t, queries, user.ID, "Tech", "https://tech.example.com/feed")

	inCategory := createSeedItem(t, queries, categorized.ID, "news-1", "News item", "unread", streamTimes.aUnreadNewest)
	outsideCategory := createSeedItem(t, queries, other.ID, "tech-1", "Tech item", "unread", streamTimes.bUnread)
	outsideCategoryRead := createSeedItem(t, queries, other.ID, "tech-read", "Tech read", "read", streamTimes.aRead)

	// A digest result that must NOT be cascaded by a category mark-read.
	cfg := createDigestConfigFor(t, queries, user.ID)
	drNew := createDigestResultFor(t, queries, user.ID, cfg.ID, "new")

	postReaderForm(t, server, "/reader/api/0/mark-all-as-read", url.Values{
		"s": {readerLabelID("News")},
	})

	if got := itemStatus(t, queries, inCategory.ID); got != "read" {
		t.Errorf("item in the named category = %q, want read: the category stream was not scoped", got)
	}
	if got := itemStatus(t, queries, outsideCategory.ID); got != "unread" {
		t.Errorf("item outside the named category = %q, want unread: marking one category read hit the whole account", got)
	}
	if got := itemStatus(t, queries, outsideCategoryRead.ID); got != "read" {
		t.Errorf("already-read outside item = %q, want read", got)
	}
	if got := digestStatus(t, queries, drNew.ID, user.ID); got != "new" {
		t.Errorf("digest result status = %q, want new: a category mark-read must not touch digests", got)
	}
}

// TestReaderMarkAllAsReadCategoryDoesNotCrossUsers is the isolation half: a
// category name is per-user, so marking "News" read must not reach another
// user's identically-named category.
func TestReaderMarkAllAsReadCategoryDoesNotCrossUsers(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")

	mine := createCategoryFeed(t, queries, user.ID, "News", "https://mine.example.com/feed")
	myItem := createSeedItem(t, queries, mine.ID, "mine-1", "Mine", "unread", streamTimes.aUnreadNewest)

	other, err := queries.CreateUser(t.Context(), store.CreateUserParams{
		Email:        "other@example.com",
		PasswordHash: "unused",
		ApiKey:       "otherkey",
	})
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}
	theirs := createCategoryFeed(t, queries, other.ID, "News", "https://theirs.example.com/feed")
	theirItem := createSeedItem(t, queries, theirs.ID, "theirs-1", "Theirs", "unread", streamTimes.aUnreadNewest)

	postReaderForm(t, server, "/reader/api/0/mark-all-as-read", url.Values{
		"s": {readerLabelID("News")},
	})

	if got := itemStatus(t, queries, myItem.ID); got != "read" {
		t.Errorf("my item = %q, want read", got)
	}
	if got := itemStatus(t, queries, theirItem.ID); got != "unread" {
		t.Errorf("other user's item in the same-named category = %q, want unread: category scoping is not user-bound", got)
	}
}

// TestReaderMarkAllAsReadUnknownStreamIsNotGlobal pins the other half of the
// finding: an unrecognized stream must not fall through to "mark everything
// read". Silently treating an unknown scope as the global one is how a client
// typo or an unsupported stream id becomes data loss.
//
// The request still answers 200/OK (Google Reader clients expect that and would
// otherwise retry), but nothing is modified.
func TestReaderMarkAllAsReadUnknownStreamIsNotGlobal(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedEditState(t, queries, user.ID)

	postReaderForm(t, server, "/reader/api/0/mark-all-as-read", url.Values{
		"s": {"user/-/state/com.google/somethingsupported"},
	})

	if got := itemStatus(t, queries, seed.aUnread.ID); got != "unread" {
		t.Errorf("item = %q, want unread: an unknown stream id marked the account read", got)
	}
	if got := digestStatus(t, queries, seed.drNew.ID, user.ID); got != "new" {
		t.Errorf("digest result = %q, want new: an unknown stream id marked all digests read", got)
	}
}

// TestReaderMarkAllAsReadReadingListStaysGlobal keeps the intentional global
// scope working. The reading-list tag IS the whole account, so this must keep
// marking every feed read — the fix narrows the default branch, not the
// explicit global one.
func TestReaderMarkAllAsReadReadingListStaysGlobal(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedEditState(t, queries, user.ID)

	postReaderForm(t, server, "/reader/api/0/mark-all-as-read", url.Values{
		"s": {readerStreamReadingList},
	})

	if got := itemStatus(t, queries, seed.aUnread.ID); got != "read" {
		t.Errorf("feed A item = %q, want read", got)
	}
	if got := itemStatus(t, queries, seed.bUnread.ID); got != "read" {
		t.Errorf("feed B item = %q, want read", got)
	}
	if got := digestStatus(t, queries, seed.drNew.ID, user.ID); got != "read" {
		t.Errorf("digest result = %q, want read: reading-list is the account-wide scope", got)
	}
}

// --- helpers ---------------------------------------------------------------

func createCategoryFeed(t *testing.T, queries *store.Queries, userID int64, category, feedURL string) store.Feed {
	t.Helper()
	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID:   userID,
		Category: category,
		Type:     "rss",
		Name:     category + " Feed",
		Url:      feedURL,
		Status:   "active",
	})
	if err != nil {
		t.Fatalf("create %s feed: %v", category, err)
	}
	return feed
}

func createDigestConfigFor(t *testing.T, queries *store.Queries, userID int64) store.DigestConfig {
	t.Helper()
	cfg, err := queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID:   userID,
		Name:     "Daily",
		Schedule: "daily",
		Config:   "{}",
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}
	return cfg
}

func createDigestResultFor(t *testing.T, queries *store.Queries, userID, configID int64, status string) store.DigestResult {
	t.Helper()
	dr, err := queries.CreateDigestResult(t.Context(), store.CreateDigestResultParams{
		UserID:         userID,
		ConfigID:       configID,
		ConfigSnapshot: "{}",
		Content:        "{}",
		Status:         status,
	})
	if err != nil {
		t.Fatalf("create digest result: %v", err)
	}
	return dr
}
