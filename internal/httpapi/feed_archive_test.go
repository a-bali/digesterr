package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"digesterr/internal/store"
)

// Deleting a live feed re-homes its saved items to an archive feed and drops
// everything else, so the saved keepsake survives with its provenance.
func TestDeleteFeedArchivesSavedItems(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "archive@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Category: "Tech", Type: "rss", Name: "Source Feed",
		Url: "https://example.com/feed", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	mkItem := func(hash, title, status string, saved bool) store.Item {
		t.Helper()
		it, err := queries.CreateItem(t.Context(), store.CreateItemParams{
			FeedID: feed.ID, Hash: hash, Title: title, Url: "https://example.com/" + hash,
			Content: "c", Status: status, Points: 1,
		})
		if err != nil {
			t.Fatalf("create item %s: %v", hash, err)
		}
		if saved {
			it, err = queries.SetItemSaved(t.Context(), store.SetItemSavedParams{ID: it.ID, Saved: 1, MarkRead: 1})
			if err != nil {
				t.Fatalf("save item: %v", err)
			}
		}
		return it
	}
	savedItem := mkItem("h-saved", "Keep me", "read", true)
	plainItem := mkItem("h-plain", "Drop me", "unread", false)

	w := makeRequest(server, http.MethodDelete, "/api/feeds/"+itoa(feed.ID), nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: got %d: %s", w.Code, w.Body.String())
	}

	// Source feed is gone.
	if _, err := queries.GetFeed(t.Context(), store.GetFeedParams{ID: feed.ID, UserID: user.ID}); err == nil {
		t.Error("source feed still present after delete")
	}
	// Non-saved item is gone.
	if _, err := queries.GetItem(t.Context(), plainItem.ID); err == nil {
		t.Error("plain item survived feed deletion")
	}
	// Saved item survives, now under an archive feed named for the source.
	kept, err := queries.GetItem(t.Context(), savedItem.ID)
	if err != nil {
		t.Fatalf("saved item was deleted: %v", err)
	}
	archive, err := queries.GetFeedByID(t.Context(), kept.FeedID)
	if err != nil {
		t.Fatalf("archive feed missing: %v", err)
	}
	if archive.IsArchive != 1 {
		t.Errorf("archive feed is_archive = %d, want 1", archive.IsArchive)
	}
	if archive.Name != "Saved -- Source Feed" {
		t.Errorf("archive name = %q, want %q", archive.Name, "Saved -- Source Feed")
	}
	if archive.Category != "Tech" {
		t.Errorf("archive category = %q, want Tech", archive.Category)
	}
	if archive.Status != "paused" {
		t.Errorf("archive status = %q, want paused", archive.Status)
	}

	// The archive is absent from the sidebar listing.
	feeds, err := queries.ListFeeds(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("list feeds: %v", err)
	}
	for _, f := range feeds {
		if f.ID == archive.ID {
			t.Error("archive feed leaked into ListFeeds (sidebar)")
		}
	}

	// The saved view returns it with provenance.
	ws := makeRequest(server, http.MethodGet, "/api/saved", nil, token)
	if ws.Code != http.StatusOK {
		t.Fatalf("GET /api/saved: %d: %s", ws.Code, ws.Body.String())
	}
	var savedResp struct {
		Items []struct {
			ID       int64  `json:"id"`
			Title    string `json:"title"`
			FeedName string `json:"feed_name"`
		} `json:"items"`
	}
	if err := json.NewDecoder(ws.Body).Decode(&savedResp); err != nil {
		t.Fatalf("decode saved: %v", err)
	}
	var found bool
	for _, it := range savedResp.Items {
		if it.ID == savedItem.ID {
			found = true
			if it.FeedName != "Saved -- Source Feed" {
				t.Errorf("saved row feed_name = %q, want archive name", it.FeedName)
			}
		}
	}
	if !found {
		t.Error("saved item missing from GET /api/saved")
	}
}

// Deleting an archive discards it and its items permanently.
func TestDeleteArchiveFeedDiscardsItems(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "archive2@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Src", Url: "https://example.com/f",
		Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	it, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: feed.ID, Hash: "h", Title: "T", Url: "https://example.com/t",
		Content: "c", Status: "read", Points: 1,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	if _, err := queries.SetItemSaved(t.Context(), store.SetItemSavedParams{ID: it.ID, Saved: 1, MarkRead: 1}); err != nil {
		t.Fatalf("save item: %v", err)
	}
	if w := makeRequest(server, http.MethodDelete, "/api/feeds/"+itoa(feed.ID), nil, token); w.Code != http.StatusOK {
		t.Fatalf("delete source: %d", w.Code)
	}
	kept, err := queries.GetItem(t.Context(), it.ID)
	if err != nil {
		t.Fatalf("saved item missing after source delete: %v", err)
	}
	archiveID := kept.FeedID

	if w := makeRequest(server, http.MethodDelete, "/api/feeds/"+itoa(archiveID), nil, token); w.Code != http.StatusOK {
		t.Fatalf("delete archive: %d: %s", w.Code, w.Body.String())
	}
	if _, err := queries.GetItem(t.Context(), it.ID); err == nil {
		t.Error("item survived archive deletion")
	}
}
