package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"digesterr/internal/store"
)

// seedReaderSubscriptions creates two feeds (one categorized, one not) and a
// digest config for the given user. It returns the created rows so tests can
// assert against their IDs.
func seedReaderSubscriptions(t *testing.T, queries *store.Queries, userID int64) (store.Feed, store.Feed, store.DigestConfig) {
	t.Helper()

	hn, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID:   userID,
		Category: "Tech",
		Type:     "rss",
		Name:     "HN",
		Url:      "https://hn.example.com",
		Status:   "active",
	})
	if err != nil {
		t.Fatalf("failed to create HN feed: %v", err)
	}

	lobsters, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: userID,
		Type:   "rss",
		Name:   "Lobsters",
		Url:    "https://lobste.rs/rss",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("failed to create Lobsters feed: %v", err)
	}

	cfg, err := queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID:   userID,
		Name:     "Daily",
		Schedule: "daily",
		Config:   "{}",
	})
	if err != nil {
		t.Fatalf("failed to create digest config: %v", err)
	}

	return hn, lobsters, cfg
}

func TestReaderSubscriptionList(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	hnFeed, _, dailyConfig := seedReaderSubscriptions(t, queries, user.ID)

	w := readerRequest(server, http.MethodGet, "/reader/api/0/subscription/list?output=json", "", map[string]string{
		"Authorization": "GoogleLogin auth=testkey123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Subscriptions []struct {
			ID         string `json:"id"`
			Title      string `json:"title"`
			Categories []struct {
				ID    string `json:"id"`
				Label string `json:"label"`
			} `json:"categories"`
		} `json:"subscriptions"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.Subscriptions) != 3 {
		t.Fatalf("expected 3 subscriptions, got %d", len(resp.Subscriptions))
	}

	var hn *struct {
		ID         string `json:"id"`
		Title      string `json:"title"`
		Categories []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"categories"`
	}
	var daily *struct {
		ID         string `json:"id"`
		Title      string `json:"title"`
		Categories []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"categories"`
	}
	for i := range resp.Subscriptions {
		switch resp.Subscriptions[i].ID {
		case feedStreamID(hnFeed.ID):
			hn = &resp.Subscriptions[i]
		case digestStreamID(dailyConfig.ID):
			daily = &resp.Subscriptions[i]
		}
	}

	if hn == nil {
		t.Fatalf("subscriptions missing feed/%d", hnFeed.ID)
	}
	if hn.Title != "HN" {
		t.Fatalf("expected HN feed title %q, got %q", "HN", hn.Title)
	}
	if len(hn.Categories) != 1 || hn.Categories[0].Label != "Tech" || hn.Categories[0].ID != "user/-/label/Tech" {
		t.Fatalf("expected HN feed in Tech category, got %v", hn.Categories)
	}

	if daily == nil {
		t.Fatalf("subscriptions missing digest/%d", dailyConfig.ID)
	}
	if daily.Title != "Daily" {
		t.Fatalf("expected digest title %q, got %q", "Daily", daily.Title)
	}
	if len(daily.Categories) != 0 {
		t.Fatalf("expected digest config to have no categories, got %v", daily.Categories)
	}
}

func TestReaderSubscriptionListEmpty(t *testing.T) {
	server, queries := setupTestServer(t)
	createReaderUser(t, queries, "testkey123")

	w := readerRequest(server, http.MethodGet, "/reader/api/0/subscription/list?output=json", "", map[string]string{
		"Authorization": "GoogleLogin auth=testkey123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Subscriptions []json.RawMessage `json:"subscriptions"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Subscriptions == nil {
		t.Fatalf("expected empty array, got null")
	}
	if len(resp.Subscriptions) != 0 {
		t.Fatalf("expected 0 subscriptions, got %d", len(resp.Subscriptions))
	}
}

func TestReaderTagList(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seedReaderSubscriptions(t, queries, user.ID)

	w := readerRequest(server, http.MethodGet, "/reader/api/0/tag/list?output=json", "", map[string]string{
		"Authorization": "GoogleLogin auth=testkey123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Tags []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"tags"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	ids := make([]string, 0, len(resp.Tags))
	for _, tag := range resp.Tags {
		if tag.Type != "tag" {
			t.Fatalf("expected tag type %q, got %q", "tag", tag.Type)
		}
		ids = append(ids, tag.ID)
	}

	wantPrefix := []string{
		"user/-/state/com.google/reading-list",
		"user/-/state/com.google/read",
		"user/-/state/com.google/starred",
	}
	if len(ids) < len(wantPrefix) {
		t.Fatalf("expected at least %d tags, got %d", len(wantPrefix), len(ids))
	}
	for i, id := range wantPrefix {
		if ids[i] != id {
			t.Fatalf("expected tag at index %d to be %q, got %q", i, id, ids[i])
		}
	}

	foundLabel := false
	for _, id := range ids {
		if id == "user/-/label/Tech" {
			foundLabel = true
			break
		}
	}
	if !foundLabel {
		t.Fatalf("expected label tag %q, got %v", "user/-/label/Tech", ids)
	}
}

func TestReaderSubscriptionExport(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seedReaderSubscriptions(t, queries, user.ID)

	w := readerRequest(server, http.MethodGet, "/reader/api/0/subscription/export", "", map[string]string{
		"Authorization": "GoogleLogin auth=testkey123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/x-opml") {
		t.Fatalf("expected Content-Type text/x-opml, got %q", ct)
	}

	body := w.Body.String()
	for _, want := range []string{
		"<opml",
		`xmlUrl="https://hn.example.com"`,
		`xmlUrl="https://lobste.rs/rss"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected body to contain %q, got:\n%s", want, body)
		}
	}

	// Digest config should be present with an empty xmlUrl (no web feed).
	if !strings.Contains(body, `text="Daily"`) {
		t.Fatalf("expected body to contain digest config title, got:\n%s", body)
	}
	if !strings.Contains(body, `type="rss" text="Daily" title="Daily" xmlUrl=""`) {
		t.Fatalf("expected digest config outline with empty xmlUrl, got:\n%s", body)
	}
}

func TestReaderSubscriptionsUnauthorized(t *testing.T) {
	server, queries := setupTestServer(t)
	createReaderUser(t, queries, "testkey123")

	for _, path := range []string{
		"/reader/api/0/subscription/list?output=json",
		"/reader/api/0/tag/list?output=json",
		"/reader/api/0/subscription/export",
	} {
		// No key supplied.
		w := readerRequest(server, http.MethodGet, path, "", nil)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s with no key: expected 401, got %d", path, w.Code)
		}

		// Unknown key.
		w = readerRequest(server, http.MethodGet, path, "", map[string]string{
			"Authorization": "GoogleLogin auth=wrongkey",
		})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s with unknown key: expected 401, got %d", path, w.Code)
		}
	}
}
