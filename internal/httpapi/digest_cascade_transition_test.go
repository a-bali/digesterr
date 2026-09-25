package httpapi

import (
	"fmt"
	"net/http"
	"testing"

	"digesterr/internal/auth"
	"digesterr/internal/store"
)

type digestFixture struct {
	cfg    store.DigestConfig
	result store.DigestResult
	items  []store.Item
}

// seedDigestResultsWithItems creates a feed + config + result for userID in the
// given result status, linking one item per entry in itemStatuses.
func seedDigestResultsWithItems(t *testing.T, queries *store.Queries, userID int64, tag, resultStatus string, itemStatuses ...string) digestFixture {
	t.Helper()
	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: userID, Type: "rss", Name: "Feed " + tag,
		Url: "https://" + tag + ".example.com/feed", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	cfg, err := queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID: userID, Name: "Cfg " + tag, Schedule: "daily", Config: "{}",
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}
	result, err := queries.CreateDigestResult(t.Context(), store.CreateDigestResultParams{
		UserID: userID, ConfigID: cfg.ID, ConfigSnapshot: "{}", Content: "{}",
		Status: resultStatus,
	})
	if err != nil {
		t.Fatalf("create digest result: %v", err)
	}
	fx := digestFixture{cfg: cfg, result: result}
	for i, st := range itemStatuses {
		item := createSeedItem(t, queries, feed.ID,
			fmt.Sprintf("%s-%d", tag, i), tag+" item", st, streamTimes.aUnreadNewest)
		if _, err := queries.CreateDigestResultItem(t.Context(), store.CreateDigestResultItemParams{
			ResultID: result.ID, ItemID: item.ID, IsHighlight: 0,
		}); err != nil {
			t.Fatalf("link item: %v", err)
		}
		fx.items = append(fx.items, item)
	}
	return fx
}

// authedTestUser creates a user with a password and returns a bearer token for
// the JSON API.
func authedTestUser(t *testing.T, server *Server, queries *store.Queries, email string) (store.User, string) {
	t.Helper()
	user := createTestUser(t, queries, email, "pw-"+email)
	token := getTestToken(t, server.auth, &user)
	return user, token
}

// Bulk mark-read must cascade ONLY through the results it transitioned in this
// operation.
//
// MarkDigestConfigResultItemsRead selects items by joining digest_result_items
// to results whose status is 'read' — a STATE test, not a transition. Every
// result that was ALREADY read when the request arrived is included, so marking
// one new digest read also sweeps the linked items of every historically read
// digest. The transaction makes the write atomic; it does not narrow which rows
// it touches.
//
// Observable damage: an item still 'digested' under an older read result (linked
// after that result was read, or regenerated) is silently consumed by an
// unrelated mark-read, and every linked row's updated_at is rewritten.
func TestBulkDigestMarkReadCascadesOnlyNewlyReadResults(t *testing.T) {
	server, queries := setupTestServer(t)
	user, token := authedTestUser(t, server, queries, "bulk-cascade@example.com")

	// An OLD digest, already read before this request, holding an item that is
	// still 'digested'.
	old := seedDigestResultsWithItems(t, queries, user.ID, "old", "read", "digested")
	// The digest being marked read now.
	recent := seedDigestResultsWithItems(t, queries, user.ID, "recent", "new", "digested")

	w := makeRequest(server, http.MethodPost, "/api/digest-results/mark-read", map[string]any{}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("mark-read: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if got := itemStatus(t, queries, recent.items[0].ID); got != "read" {
		t.Errorf("newly read digest's item status = %q, want \"read\"", got)
	}
	if got := itemStatus(t, queries, old.items[0].ID); got != "digested" {
		t.Errorf("bulk mark-read cascaded into a historically read digest: item status = %q, want \"digested\"", got)
	}
}

// The per-config path selects by config + status='read', so the historically
// read digest that gets swept has to live in the SAME config.
func TestConfigDigestMarkReadCascadesOnlyNewlyReadResults(t *testing.T) {
	server, queries := setupTestServer(t)
	user, token := authedTestUser(t, server, queries, "cfg-cascade@example.com")

	cfg, old, recent := sharedConfigFixtures(t, queries, user.ID, "cfgshare")

	w := makeRequest(server, http.MethodPost, "/api/digest-results/mark-read",
		map[string]any{"config_id": cfg.ID}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("mark-read: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if got := itemStatus(t, queries, recent.items[0].ID); got != "read" {
		t.Errorf("newly read digest's item status = %q, want \"read\"", got)
	}
	if got := itemStatus(t, queries, old.items[0].ID); got != "digested" {
		t.Errorf("config mark-read cascaded into a historically read digest: item status = %q, want \"digested\"", got)
	}
}

// The relative (above/below) path selects by config + status='read' as well, and
// its boundary update only touches results on ONE side. A result on the other
// side that was already read is still swept by the cascade.
func TestRelativeDigestMarkReadCascadesOnlyNewlyReadResults(t *testing.T) {
	server, queries := setupTestServer(t)
	user, token := authedTestUser(t, server, queries, "rel-cascade@example.com")

	cfg, old, boundary := sharedConfigFixtures(t, queries, user.ID, "relshare")

	// Pin times: `old` sits ABOVE the boundary (newer), so marking below the
	// boundary does not newly read it — but the cascade still reaches it.
	pinResultCreatedAt(t, queries, old.result.ID, "2026-01-04 00:00:00")
	pinResultCreatedAt(t, queries, boundary.result.ID, "2026-01-02 00:00:00")

	w := makeRequest(server, http.MethodPost,
		fmt.Sprintf("/api/digest-results/%d/mark-read-below", boundary.result.ID),
		map[string]any{"config_id": cfg.ID}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("mark-read-below: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if got := itemStatus(t, queries, old.items[0].ID); got != "digested" {
		t.Errorf("relative mark-read cascaded into a historically read digest: item status = %q, want \"digested\"", got)
	}
}

// sharedConfigFixtures builds ONE config holding an already-read result (item
// still 'digested') and a 'new' result (item 'digested').
func sharedConfigFixtures(t *testing.T, queries *store.Queries, userID int64, tag string) (store.DigestConfig, digestFixture, digestFixture) {
	t.Helper()
	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: userID, Type: "rss", Name: "Feed " + tag,
		Url: "https://" + tag + ".example.com/feed", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	cfg, err := queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID: userID, Name: "Cfg " + tag, Schedule: "daily", Config: "{}",
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}
	add := func(suffix, status string) digestFixture {
		result, err := queries.CreateDigestResult(t.Context(), store.CreateDigestResultParams{
			UserID: userID, ConfigID: cfg.ID, ConfigSnapshot: "{}", Content: "{}",
			Status: status,
		})
		if err != nil {
			t.Fatalf("create digest result: %v", err)
		}
		item := createSeedItem(t, queries, feed.ID, tag+"-"+suffix, tag+" "+suffix,
			"digested", streamTimes.aUnreadNewest)
		if _, err := queries.CreateDigestResultItem(t.Context(), store.CreateDigestResultItemParams{
			ResultID: result.ID, ItemID: item.ID, IsHighlight: 0,
		}); err != nil {
			t.Fatalf("link item: %v", err)
		}
		return digestFixture{cfg: cfg, result: result, items: []store.Item{item}}
	}
	return cfg, add("old", "read"), add("new", "new")
}

func pinResultCreatedAt(t *testing.T, queries *store.Queries, id int64, at string) {
	t.Helper()
	if _, err := queries.DB().ExecContext(t.Context(),
		"UPDATE digest_results SET created_at = ? WHERE id = ?", at, id); err != nil {
		t.Fatalf("pin result created_at: %v", err)
	}
}


var _ = auth.HashPassword
