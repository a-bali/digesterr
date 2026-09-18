package digest

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

// A deleted ("cleared") extraction must be treated like "nothing to extract":
// enrichAndExtractItems runs before every digest generation and picks up any
// item whose status is neither 'done' nor 'skipped'. Without this distinction
// the next scheduled digest would fetch the article body straight back and undo
// the user's delete — the exact reason the delete writes 'cleared' instead of
// leaving the status empty.
func TestClearedExtractionIsNotReExtracted(t *testing.T) {
	cleared := store.Item{ExtractStatus: "cleared", ExtractContent: ""}
	if itemNeedsExtraction(cleared) {
		t.Error("an item whose extraction the user deleted must not be re-extracted")
	}
}

func TestItemsWithAnExtractedBodyAreNotReExtracted(t *testing.T) {
	cases := []struct {
		name   string
		status string
	}{
		{"done", "done"},
		{"skipped", "skipped"},
		{"cleared", "cleared"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if itemNeedsExtraction(store.Item{ExtractStatus: tc.status}) {
				t.Errorf("status %q must not be queued for extraction", tc.status)
			}
		})
	}
}

func TestItemsAwaitingExtractionAreReExtracted(t *testing.T) {
	// The states that legitimately still need a body: never extracted, in
	// flight, or failed. Regression guard for over-correcting the skip above.
	for _, status := range []string{"", "pending", "failed"} {
		if !itemNeedsExtraction(store.Item{ExtractStatus: status}) {
			t.Errorf("status %q should still be handed to the extractor", status)
		}
	}
}

func TestContentForRenderingFallsBackAfterDelete(t *testing.T) {
	item := store.Item{
		Content:        "<p>Original body</p>",
		ExtractContent: "",
		ExtractStatus:  "cleared",
	}
	if got := contentForRendering(item); got != "<p>Original body</p>" {
		t.Errorf("expected the original body after a delete, got %q", got)
	}
}

// newTestGenerator builds a generator over a fresh in-memory DB with a poller
// whose enrichers are stubs — no network is reachable from here.
func newTestGenerator(t *testing.T) (*Generator, *store.Queries) {
	t.Helper()
	db, queries, cleanup := setupDigestTest(t)
	t.Cleanup(cleanup)
	return NewGenerator(db, queries, nil, feed.NewPoller(queries)), queries
}

// discardLogger returns a logger whose output goes nowhere, for helpers that
// take one.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestClearedItemKeepsOriginalContentThroughExtractionPass drives the real
// enrichAndExtractItems path over a stored item and asserts the extracted body
// is not restored. With the pre-fix condition (`ExtractStatus == "done" ||
// ExtractContent != ""`) a cleared item has neither, so it was handed to the
// extractor and came back 'done'.
//
// The item URL points at a local server: the extractor fetches item.url, and a
// test that reaches the real network is both slow and non-deterministic. The
// body it serves is deliberately non-empty so that a wrongly-queued item really
// does come back 'done' — a stub returning nothing would let the bug through
// (extractSingleItem leaves an item alone when the extractor returns empty).
func TestClearedItemKeepsOriginalContentThroughExtractionPass(t *testing.T) {
	gen, queries := newTestGenerator(t)
	ctx := t.Context()

	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body><article><p>Extracted body from the page</p></article></body></html>")
	}))
	t.Cleanup(page.Close)

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "cleared-extraction@example.com",
		PasswordHash: "hash",
		ApiKey:       "test-api-key",
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	feedRow, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID:         user.ID,
		Type:           "rss",
		Name:           "Extract Feed",
		Url:            "https://example.com/feed",
		ExtractContent: 1,
	})
	if err != nil {
		t.Fatalf("failed to create feed: %v", err)
	}
	item, err := queries.CreateItem(ctx, store.CreateItemParams{
		FeedID:  feedRow.ID,
		Hash:    "hash-cleared-extraction",
		Title:   "Article",
		Url:     page.URL,
		Content: "<p>Original body</p>",
	})
	if err != nil {
		t.Fatalf("failed to create item: %v", err)
	}
	// The state a completed extract leaves behind, then the delete.
	if _, err := queries.UpdateItemExtraction(ctx, store.UpdateItemExtractionParams{
		ID:             item.ID,
		ExtractContent: "<p>Extracted body</p>",
		ExtractStatus:  "done",
	}); err != nil {
		t.Fatalf("failed to set extraction: %v", err)
	}
	if _, err := queries.ClearItemExtraction(ctx, item.ID); err != nil {
		t.Fatalf("failed to clear extraction: %v", err)
	}
	// The digest path works from rows it loaded from the DB, so the in-memory
	// item must be re-read here: passing the pre-clear value would test a state
	// the extractor never sees.
	cleared, err := queries.GetItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("failed to re-read item: %v", err)
	}
	if cleared.ExtractStatus != "cleared" {
		t.Fatalf("precondition: expected the cleared row, got status %q", cleared.ExtractStatus)
	}

	items := []digestItem{{Item: cleared}}
	gen.enrichAndExtractItems(ctx, items, discardLogger())

	after, err := queries.GetItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("failed to re-read item: %v", err)
	}
	if after.ExtractStatus != "cleared" {
		t.Errorf("a deleted extraction must survive the extraction pass, got status %q content %q",
			after.ExtractStatus, after.ExtractContent)
	}
	if after.Content != "<p>Original body</p>" {
		t.Errorf("original content must be untouched, got %q", after.Content)
	}
}
