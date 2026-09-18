package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"digesterr/internal/store"
)

// createItemWithExtraction builds the state the UI's "Delete extracted" button
// acts on: an item whose original content is present and whose extraction
// succeeded.
func createItemWithExtraction(t *testing.T, queries *store.Queries, userID int64, hash string) store.Item {
	t.Helper()
	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID:         userID,
		Type:           "rss",
		Name:           "Extract Feed",
		Url:            "https://example.com/feed",
		ExtractContent: 1,
	})
	if err != nil {
		t.Fatalf("failed to create feed: %v", err)
	}
	item, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID:  feed.ID,
		Hash:    hash,
		Title:   "Article with extraction",
		Url:     "https://example.com/article",
		Content: "<p>Original content from the feed.</p>",
	})
	if err != nil {
		t.Fatalf("failed to create item: %v", err)
	}
	item, err = queries.UpdateItemExtraction(t.Context(), store.UpdateItemExtractionParams{
		ID:             item.ID,
		ExtractContent: "<p>Full extracted body from the page.</p>",
		ExtractStatus:  "done",
		ExtractRetries: 0,
	})
	if err != nil {
		t.Fatalf("failed to set extraction: %v", err)
	}
	return item
}

// TestDeleteItemExtractionFallsBackToOriginal is the feature under test: the
// extracted body is dropped, the original feed content is left alone, and the
// item is not left in a state the extractor would consider work to do.
func TestDeleteItemExtractionFallsBackToOriginal(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "delete-extract@example.com", "password123")
	token := getTestToken(t, server.auth, &user)
	item := createItemWithExtraction(t, queries, user.ID, "hash-delete-extract")

	w := makeRequest(server, http.MethodDelete, "/api/items/"+itoa(item.ID)+"/extract", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp store.Item
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.ExtractContent != "" {
		t.Errorf("expected extracted content to be cleared, got %q", resp.ExtractContent)
	}
	// 'cleared' rather than '': the extractor must be able to tell a deliberate
	// deletion from an item it has simply never looked at.
	if resp.ExtractStatus != "cleared" {
		t.Errorf("expected extract_status 'cleared', got %q", resp.ExtractStatus)
	}
	// A stale retry count or date would make the item look half-extracted, and
	// the delete would not be repeatable.
	if resp.ExtractRetries != 0 {
		t.Errorf("expected extract_retries 0, got %d", resp.ExtractRetries)
	}
	if resp.ExtractDate.Valid {
		t.Errorf("expected extract_date to be NULL, got %v", resp.ExtractDate)
	}
	if resp.Content != "<p>Original content from the feed.</p>" {
		t.Errorf("original content must be untouched, got %q", resp.Content)
	}
	if resp.Status != item.Status {
		t.Errorf("item status must be untouched: expected %q, got %q", item.Status, resp.Status)
	}

	// The row the reader actually renders from (GET /api/items/{id}) must agree
	// — this is what makes the content pane fall back to the original body.
	w = makeRequest(server, http.MethodGet, "/api/items/"+itoa(item.ID), nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 from GET, got %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal GET response: %v", err)
	}
	if resp.ExtractStatus != "cleared" || resp.ExtractContent != "" {
		t.Errorf("GET must report the cleared extraction, got status %q content %q", resp.ExtractStatus, resp.ExtractContent)
	}
}

// TestDeletedExtractionIsNotHandedToTheExtractor pins the reason 'cleared'
// exists: ListItemsNeedingExtraction selects by "not done", so an empty status
// would put the deleted item straight back in the extraction queue and the next
// poll would undo the user's delete.
func TestDeletedExtractionIsNotHandedToTheExtractor(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "delete-extract-queue@example.com", "password123")
	token := getTestToken(t, server.auth, &user)
	item := createItemWithExtraction(t, queries, user.ID, "hash-delete-extract-queue")

	queuedBefore, err := queries.ListItemsNeedingExtraction(t.Context(), item.FeedID)
	if err != nil {
		t.Fatalf("failed to list items needing extraction: %v", err)
	}
	for _, queued := range queuedBefore {
		if queued.ID == item.ID {
			t.Fatalf("precondition failed: a done item must not be queued for extraction")
		}
	}

	w := makeRequest(server, http.MethodDelete, "/api/items/"+itoa(item.ID)+"/extract", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	queuedAfter, err := queries.ListItemsNeedingExtraction(t.Context(), item.FeedID)
	if err != nil {
		t.Fatalf("failed to list items needing extraction: %v", err)
	}
	for _, queued := range queuedAfter {
		if queued.ID == item.ID {
			t.Fatalf("a deleted extraction must not be re-queued for extraction (status %q)", queued.ExtractStatus)
		}
	}
}

// TestDeleteItemExtractionIsIdempotent: the button can be pressed again (or the
// request retried) without error or a partial state.
func TestDeleteItemExtractionIsIdempotent(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "delete-extract-twice@example.com", "password123")
	token := getTestToken(t, server.auth, &user)
	item := createItemWithExtraction(t, queries, user.ID, "hash-delete-extract-twice")

	for attempt := 1; attempt <= 2; attempt++ {
		w := makeRequest(server, http.MethodDelete, "/api/items/"+itoa(item.ID)+"/extract", nil, token)
		if w.Code != http.StatusOK {
			t.Fatalf("attempt %d: expected status 200, got %d: %s", attempt, w.Code, w.Body.String())
		}
	}
}

// TestDeleteItemExtractionRejectsForeignItem: another account's item is not
// merely denied — it must not be touched.
func TestDeleteItemExtractionRejectsForeignItem(t *testing.T) {
	server, queries := setupTestServer(t)
	owner := createTestUser(t, queries, "delete-extract-owner@example.com", "password123")
	intruder := createTestUser(t, queries, "delete-extract-intruder@example.com", "password123")
	intruderToken := getTestToken(t, server.auth, &intruder)
	item := createItemWithExtraction(t, queries, owner.ID, "hash-delete-extract-isolation")

	w := makeRequest(server, http.MethodDelete, "/api/items/"+itoa(item.ID)+"/extract", nil, intruderToken)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 for another account's item, got %d: %s", w.Code, w.Body.String())
	}

	after, err := queries.GetItem(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("failed to re-read item: %v", err)
	}
	if after.ExtractStatus != "done" || after.ExtractContent == "" {
		t.Errorf("a rejected delete must leave the item untouched, got status %q content %q", after.ExtractStatus, after.ExtractContent)
	}
}
