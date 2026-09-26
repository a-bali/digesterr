package httpapi

import (
	"net/url"
	"strconv"
	"testing"

	"digesterr/internal/store"
)

// Verification tests for four reported issues. Each test asserts the CORRECT
// behaviour, so a failure is the evidence that the report is real.

// digestReaderID renders the Google Reader item id for a digest result id, the
// form clients actually submit on edit-tag. digestRefOffset is added so the
// id round-trips through splitReaderItemRef back to readerEditDigest.
func digestReaderID(id int64) string {
	return readerItemIDPrefix + strconv.FormatInt(id+digestRefOffset, 16)
}

// editTagRead posts an add-read edit-tag for the given digest results.
func editTagRead(t *testing.T, server *Server, ids ...int64) {
	t.Helper()
	form := url.Values{}
	for _, id := range ids {
		form.Add("i", digestReaderID(id))
	}
	form.Add("a", readerTagRead)
	postReaderForm(t, server, "/reader/api/0/edit-tag", form)
}

// CLAIM 1: edit-tag cascades the SUBMITTED digest ids even when the status
// update matched nothing (already-read result).
//
// The cascade query is driven by the submitted id set and filters only
// `status = 'digested'`. The repro: a result that is already read, whose linked
// item is still 'digested' because a LATER digest generation re-linked it and
// set it back to 'digested'. Re-adding the read tag to the OLD result must not
// consume that item out of the newer unread digest.
func TestEditTagDoesNotCascadeAlreadyReadResult(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")

	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "F", Url: "https://f.example.com/feed", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	item := createSeedItem(t, queries, feed.ID, "shared-item", "Shared item", "digested", streamTimes.aUnreadNewest)

	cfg, err := queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID: user.ID, Name: "Daily", Schedule: "daily", Config: "{}",
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}
	// The OLD result is already read; the item is linked to it.
	oldResult, err := queries.CreateDigestResult(t.Context(), store.CreateDigestResultParams{
		UserID: user.ID, ConfigID: cfg.ID, ConfigSnapshot: "{}", Content: "{}", Status: "read",
	})
	if err != nil {
		t.Fatalf("create old digest result: %v", err)
	}
	if _, err := queries.CreateDigestResultItem(t.Context(), store.CreateDigestResultItemParams{
		ResultID: oldResult.ID, ItemID: item.ID, IsHighlight: 1,
	}); err != nil {
		t.Fatalf("link item to old result: %v", err)
	}
	// A NEWER unread result also links the same item (this is what a later
	// generation does: it re-links the shared item and sets it back to
	// 'digested').
	newResult, err := queries.CreateDigestResult(t.Context(), store.CreateDigestResultParams{
		UserID: user.ID, ConfigID: cfg.ID, ConfigSnapshot: "{}", Content: "{}", Status: "new",
	})
	if err != nil {
		t.Fatalf("create new digest result: %v", err)
	}
	if _, err := queries.CreateDigestResultItem(t.Context(), store.CreateDigestResultItemParams{
		ResultID: newResult.ID, ItemID: item.ID, IsHighlight: 0,
	}); err != nil {
		t.Fatalf("link item to new result: %v", err)
	}

	// Re-add the read tag to the OLD (already read) result.
	editTagRead(t, server, oldResult.ID)

	if got := itemStatus(t, queries, item.ID); got != "digested" {
		t.Errorf("item status = %q, want %q: edit-tag on an already-read result "+
			"cascaded into the newer unread digest and consumed the shared item",
			got, "digested")
	}
	if got := digestStatus(t, queries, newResult.ID, user.ID); got != "new" {
		t.Errorf("new result status = %q, want %q", got, "new")
	}
}

// CONTROL for claim 1: the cascade must still fire on a genuine transition
// (unread result -> read). If this fails, claim 1's test may be passing for the
// wrong reason.
func TestEditTagStillCascadesOnRealTransition(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")

	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "F", Url: "https://f.example.com/feed", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	item := createSeedItem(t, queries, feed.ID, "item", "Item", "digested", streamTimes.aUnreadNewest)
	cfg, err := queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID: user.ID, Name: "Daily", Schedule: "daily", Config: "{}",
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}
	result, err := queries.CreateDigestResult(t.Context(), store.CreateDigestResultParams{
		UserID: user.ID, ConfigID: cfg.ID, ConfigSnapshot: "{}", Content: "{}", Status: "new",
	})
	if err != nil {
		t.Fatalf("create digest result: %v", err)
	}
	if _, err := queries.CreateDigestResultItem(t.Context(), store.CreateDigestResultItemParams{
		ResultID: result.ID, ItemID: item.ID, IsHighlight: 1,
	}); err != nil {
		t.Fatalf("link item: %v", err)
	}

	editTagRead(t, server, result.ID)

	if got := digestStatus(t, queries, result.ID, user.ID); got != "read" {
		t.Errorf("result status = %q, want read", got)
	}
	if got := itemStatus(t, queries, item.ID); got != "read" {
		t.Errorf("item status = %q, want read (cascade must fire on a transition)", got)
	}
}

// CLAIM 2: `fresh` is a listing alias of the reading list, but mark-all-as-read
// on it falls through to the no-op branch.
func TestMarkAllAsReadFreshScopeIsNotANoOp(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")

	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "F", Url: "https://f.example.com/feed", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	item := createSeedItem(t, queries, feed.ID, "u1", "Unread", "unread", streamTimes.aUnreadNewest)

	form := url.Values{}
	form.Set("s", readerStreamFresh)
	postReaderForm(t, server, "/reader/api/0/mark-all-as-read", form)

	if got := itemStatus(t, queries, item.ID); got != "read" {
		t.Errorf("item status = %q, want read: mark-all-as-read on the `fresh` "+
			"stream is a listing alias of the reading list and is treated as scope, "+
			"but the request fell through to the no-op branch", got)
	}
}

// CLAIM 4 (item side): the cascade must not be driven by the submitted id set.
// Directly asserts the batch statement's behaviour: an already-read result's
// linked 'digested' item must be left alone when the result update matches no
// row, which is what the discarded row count in reader_edit.go cannot detect.
func TestCascadeStatementIgnoresUnchangedResult(t *testing.T) {
	_, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")

	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "F", Url: "https://f.example.com/feed", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	item := createSeedItem(t, queries, feed.ID, "x", "X", "digested", streamTimes.aUnreadNewest)
	cfg, err := queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID: user.ID, Name: "C", Schedule: "daily", Config: "{}",
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}
	result, err := queries.CreateDigestResult(t.Context(), store.CreateDigestResultParams{
		UserID: user.ID, ConfigID: cfg.ID, ConfigSnapshot: "{}", Content: "{}", Status: "read",
	})
	if err != nil {
		t.Fatalf("create digest result: %v", err)
	}
	if _, err := queries.CreateDigestResultItem(t.Context(), store.CreateDigestResultItemParams{
		ResultID: result.ID, ItemID: item.ID, IsHighlight: 1,
	}); err != nil {
		t.Fatalf("link item: %v", err)
	}

	// The transition query is what the handler now derives the cascade from. An
	// already-read result does not transition, so it must NOT be returned --
	// returning it is exactly the bug, because the caller cascades what it gets
	// back rather than what it submitted.
	transitioned, err := queries.TransitionDigestResultStatusByIDs(t.Context(), store.TransitionDigestResultStatusByIDsParams{
		Status: "read", Ids: idsJSON([]int64{result.ID}), UserID: user.ID,
	})
	if err != nil {
		t.Fatalf("transition: %v", err)
	}
	if len(transitioned) != 0 {
		t.Errorf("transition returned %v, want none: an already-read result does not "+
			"transition, so the cascade must not see it", transitioned)
	}

	// And a genuine transition IS returned, so the cascade still fires on the
	// real new->read case. Without this the previous assertion would also pass
	// for an implementation that never returns anything.
	newResult, err := queries.CreateDigestResult(t.Context(), store.CreateDigestResultParams{
		UserID: user.ID, ConfigID: cfg.ID, ConfigSnapshot: "{}", Content: "{}", Status: "new",
	})
	if err != nil {
		t.Fatalf("create result 2: %v", err)
	}
	got, err := queries.TransitionDigestResultStatusByIDs(t.Context(), store.TransitionDigestResultStatusByIDsParams{
		Status: "read", Ids: idsJSON([]int64{newResult.ID}), UserID: user.ID,
	})
	if err != nil {
		t.Fatalf("transition: %v", err)
	}
	if len(got) != 1 || got[0] != newResult.ID {
		t.Errorf("transition returned %v, want [%d]: a real new->read transition must be "+
			"reported so its items cascade", got, newResult.ID)
	}
}
