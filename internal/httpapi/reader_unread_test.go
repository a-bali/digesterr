package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"digesterr/internal/store"
)

func seedReaderUnread(t *testing.T, queries *store.Queries, userID int64) (feedA, feedB store.Feed, cfg store.DigestConfig) {
	t.Helper()

	var err error
	feedA, err = queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: userID,
		Type:   "rss",
		Name:   "Feed A",
		Url:    "https://a.example.com/feed",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("failed to create feed A: %v", err)
	}

	feedB, err = queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: userID,
		Type:   "rss",
		Name:   "Feed B",
		Url:    "https://b.example.com/feed",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("failed to create feed B: %v", err)
	}

	// Feed A: 2 unread + 1 read.
	for i, status := range []string{"unread", "unread", "read"} {
		if _, err := queries.CreateItem(t.Context(), store.CreateItemParams{
			FeedID: feedA.ID,
			Hash:   "a-item-" + strconv.Itoa(i),
			Title:  "A item " + strconv.Itoa(i),
			Url:    "https://a.example.com/" + strconv.Itoa(i),
			Status: status,
		}); err != nil {
			t.Fatalf("failed to create feed A item: %v", err)
		}
	}

	// Feed B: 0 unread (a single read item).
	if _, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: feedB.ID,
		Hash:   "b-read",
		Title:  "B item",
		Url:    "https://b.example.com/read",
		Status: "read",
	}); err != nil {
		t.Fatalf("failed to create feed B item: %v", err)
	}

	// One saved item for the starred count.
	if _, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: feedA.ID,
		Hash:   "a-saved",
		Title:  "A saved item",
		Url:    "https://a.example.com/saved",
		Status: "saved",
	}); err != nil {
		t.Fatalf("failed to create saved item: %v", err)
	}

	cfg, err = queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID:   userID,
		Name:     "Daily",
		Schedule: "daily",
		Config:   "{}",
	})
	if err != nil {
		t.Fatalf("failed to create digest config: %v", err)
	}

	// One unread digest result.
	if _, err := queries.CreateDigestResult(t.Context(), store.CreateDigestResultParams{
		UserID:         userID,
		ConfigID:       cfg.ID,
		ConfigSnapshot: "{}",
		Content:        "{}",
		Status:         "new",
	}); err != nil {
		t.Fatalf("failed to create digest result: %v", err)
	}

	return feedA, feedB, cfg
}

func TestReaderUnreadCount(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	feedA, _, cfg := seedReaderUnread(t, queries, user.ID)

	w := readerRequest(server, http.MethodGet, "/reader/api/0/unread-count?output=json", "", map[string]string{
		"Authorization": "GoogleLogin auth=testkey123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Max          string `json:"max"`
		UnreadCounts []struct {
			ID                      string `json:"id"`
			Count                   int64  `json:"count"`
			NewestItemTimestampUsec string `json:"newestItemTimestampUsec"`
		} `json:"unreadcounts"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Max == "" {
		t.Fatal("expected non-empty max")
	}
	if _, err := strconv.ParseInt(resp.Max, 10, 64); err != nil {
		t.Fatalf("expected max to be a numeric string, got %q", resp.Max)
	}

	counts := make(map[string]int64, len(resp.UnreadCounts))
	usecs := make(map[string]string, len(resp.UnreadCounts))
	for _, entry := range resp.UnreadCounts {
		counts[entry.ID] = entry.Count
		usecs[entry.ID] = entry.NewestItemTimestampUsec
	}

	if got := counts[feedStreamID(feedA.ID)]; got != 2 {
		t.Fatalf("expected feed/%d count 2, got %d", feedA.ID, got)
	}
	if got := counts[digestStreamID(cfg.ID)]; got != 1 {
		t.Fatalf("expected digest/%d count 1, got %d", cfg.ID, got)
	}
	if got := counts["user/-/state/com.google/reading-list"]; got != 3 {
		t.Fatalf("expected reading-list count 3, got %d", got)
	}
	if got := counts["user/-/state/com.google/starred"]; got != 1 {
		t.Fatalf("expected starred count 1, got %d", got)
	}

	// Feed A has unread items with a fetched_at, so its newest-item usec should
	// be non-zero; digest and global entries are always "0".
	if usecs[feedStreamID(feedA.ID)] == "" || usecs[feedStreamID(feedA.ID)] == "0" {
		t.Fatalf("expected non-zero newestItemTimestampUsec for feed/%d, got %q", feedA.ID, usecs[feedStreamID(feedA.ID)])
	}
	for _, id := range []string{"user/-/state/com.google/reading-list", "user/-/state/com.google/starred", digestStreamID(cfg.ID)} {
		if usecs[id] != "0" {
			t.Fatalf("expected newestItemTimestampUsec %q for %s, got %q", "0", id, usecs[id])
		}
	}
}

func TestReaderUnreadCountOmitsZeroFeed(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	_, feedB, _ := seedReaderUnread(t, queries, user.ID)

	w := readerRequest(server, http.MethodGet, "/reader/api/0/unread-count?output=json", "", map[string]string{
		"Authorization": "GoogleLogin auth=testkey123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		UnreadCounts []struct {
			ID string `json:"id"`
		} `json:"unreadcounts"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	for _, entry := range resp.UnreadCounts {
		if entry.ID == feedStreamID(feedB.ID) {
			t.Fatalf("expected zero-count feed/%d to be omitted", feedB.ID)
		}
	}
}

func TestReaderUnreadCountUnauthorized(t *testing.T) {
	server, queries := setupTestServer(t)
	createReaderUser(t, queries, "testkey123")

	// No key supplied.
	w := readerRequest(server, http.MethodGet, "/reader/api/0/unread-count?output=json", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without key, got %d", w.Code)
	}

	// Unknown key.
	w = readerRequest(server, http.MethodGet, "/reader/api/0/unread-count?output=json", "", map[string]string{
		"Authorization": "GoogleLogin auth=wrongkey",
	})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with unknown key, got %d", w.Code)
	}
}
