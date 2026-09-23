package httpapi

import (
	"net/http"
	"net/url"
	"testing"

	"digesterr/internal/store"
)

// postReaderFormAs is postReaderForm with an explicit API key, so a test can act
// as two different accounts. postReaderForm hardcodes the shared "testkey123",
// and createReaderUser hardcodes its email, so neither can express two accounts.
func postReaderFormAs(t *testing.T, server *Server, apiKey, path string, form url.Values) {
	t.Helper()
	w := readerRequest(server, http.MethodPost, path, form.Encode(), map[string]string{
		"Authorization": "GoogleLogin auth=" + apiKey,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("%s: expected 200, got %d: %s", path, w.Code, w.Body.String())
	}
}

// createUserWithKey makes a user with a KNOWN Reader API key. createReaderUser
// hardcodes both the email and the key, so a second distinct account needs this.
func createUserWithKey(t *testing.T, queries *store.Queries, email, apiKey string) store.User {
	t.Helper()
	user, err := queries.CreateUser(t.Context(), store.CreateUserParams{
		Email:        email,
		PasswordHash: "unused",
		ApiKey:       apiKey,
	})
	if err != nil {
		t.Fatalf("failed to create user %q: %v", email, err)
	}
	return user
}

// seedDigestResultWithItem creates a digest config + result for userID and links
// one 'digested' item from that user's own feed to it — the minimum shape the
// edit-tag cascade needs to have something to (wrongly) touch.
func seedDigestResultWithItem(t *testing.T, queries *store.Queries, userID int64, tag string) (store.Item, store.DigestResult) {
	t.Helper()
	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: userID,
		Type:   "rss",
		Name:   "Feed " + tag,
		Url:    "https://" + tag + ".example.com/feed",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("failed to create feed for %s: %v", tag, err)
	}
	item := createSeedItem(t, queries, feed.ID, tag+"-item", tag+" item", "digested", streamTimes.aUnreadNewest)

	cfg, err := queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID:   userID,
		Name:     "Cfg " + tag,
		Schedule: "daily",
		Config:   "{}",
	})
	if err != nil {
		t.Fatalf("failed to create digest config for %s: %v", tag, err)
	}
	result, err := queries.CreateDigestResult(t.Context(), store.CreateDigestResultParams{
		UserID:         userID,
		ConfigID:       cfg.ID,
		ConfigSnapshot: "{}",
		Content:        "{}",
		Status:         "new",
	})
	if err != nil {
		t.Fatalf("failed to create digest result for %s: %v", tag, err)
	}
	if _, err := queries.CreateDigestResultItem(t.Context(), store.CreateDigestResultItemParams{
		ResultID: result.ID, ItemID: item.ID, IsHighlight: 1,
	}); err != nil {
		t.Fatalf("failed to link item to result for %s: %v", tag, err)
	}
	return item, result
}

// TestReaderEditTagCannotCascadeIntoAnotherAccount is the regression test for a
// cross-account write.
//
// edit-tag scopes the digest RESULT update by user_id, but the cascade that
// follows it (MarkDigestResultItemsReadByResultIDs) takes the submitted result
// IDs straight into `digest_result_items` with no ownership predicate, and runs
// unconditionally rather than only for rows the result update actually touched.
// So an attacker who knows another account's result id can flip that account's
// linked items from 'digested' to 'read' — a silent, unauditable write into
// someone else's reading state.
//
// Digest reference ids are derived from the result id (digestItemStreamID), so a
// client sees its own ids in its stream and can enumerate a foreign one.
func TestReaderEditTagCannotCascadeIntoAnotherAccount(t *testing.T) {
	server, queries := setupTestServer(t)

	victim := createUserWithKey(t, queries, "victim@example.com", "victimkey123")
	victimItem, victimResult := seedDigestResultWithItem(t, queries, victim.ID, "victim")

	createUserWithKey(t, queries, "attacker@example.com", "attackkey123")

	// The attacker submits the VICTIM's digest result id as a read.
	postReaderFormAs(t, server, "attackkey123", "/reader/api/0/edit-tag",
		editTagForm(readerTagRead, "", digestItemStreamID(victimResult.ID)))

	// The victim's state must be untouched.
	if got := itemStatus(t, queries, victimItem.ID); got != "digested" {
		t.Errorf("cross-account cascade flipped a foreign item: status = %q, want \"digested\"", got)
	}
	if got := digestStatus(t, queries, victimResult.ID, victim.ID); got != "new" {
		t.Errorf("foreign digest result was modified: status = %q, want \"new\"", got)
	}
}

// TestReaderEditTagCascadesWithinOwnAccount is the positive control for the test
// above: the same request against the account that OWNS the result must still
// cascade, so the fix cannot be "never cascade".
func TestReaderEditTagCascadesWithinOwnAccount(t *testing.T) {
	server, queries := setupTestServer(t)

	owner := createUserWithKey(t, queries, "owner@example.com", "ownerkey123")
	item, result := seedDigestResultWithItem(t, queries, owner.ID, "owner")

	postReaderFormAs(t, server, "ownerkey123", "/reader/api/0/edit-tag",
		editTagForm(readerTagRead, "", digestItemStreamID(result.ID)))

	if got := itemStatus(t, queries, item.ID); got != "read" {
		t.Errorf("own-account cascade did not run: item status = %q, want \"read\"", got)
	}
	if got := digestStatus(t, queries, result.ID, owner.ID); got != "read" {
		t.Errorf("own digest result not marked read: status = %q, want \"read\"", got)
	}
}
