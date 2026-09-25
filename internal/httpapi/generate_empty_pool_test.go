package httpapi

import (
	"log/slog"
	"net/http"
	"strconv"
	"testing"

	"digesterr/internal/store"
	"digesterr/internal/testutil"
)

// An empty item pool is a normal outcome, not a failure. The manual "Generate
// now" path used to report it as a 500 and store it in the config's last_error,
// which put a red mark on a healthy config and — because nothing else clears
// that column until the next success, which may be a day away — made the mark
// look permanent.
func TestGenerateNoUnreadItemsIsNotAnError(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "nopool@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Quiet Feed",
		Url:    "https://example.com/quiet.xml",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	// No items: the pool is empty.
	cfg, err := queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Quiet Config",
		Schedule: "0 5 * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":2,"comments":5}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	capture := testutil.CaptureSlog(t, slog.LevelInfo)
	w := makeRequest(server, http.MethodPost, "/api/digest-configs/"+itoa(cfg.ID)+"/generate", nil, token)

	// A conflict, not a server error: nothing broke.
	if w.Code == http.StatusInternalServerError {
		t.Errorf("empty pool reported as a 500: %s", w.Body.String())
	}
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d (conflict): %s", w.Code, http.StatusConflict, w.Body.String())
	}

	// The config must not be left carrying an error.
	after, err := queries.GetDigestConfig(t.Context(), store.GetDigestConfigParams{
		ID:     cfg.ID,
		UserID: user.ID,
	})
	if err != nil {
		t.Fatalf("get digest config: %v", err)
	}
	if after.LastError != "" {
		t.Errorf("last_error = %q, want empty: an empty pool is not a failure", after.LastError)
	}

	// And it must not be logged as an error either.
	if _, ok := capture.Find("digest generation failed"); ok {
		t.Error("'digest generation failed' was logged for an empty item pool")
	}
}

// The manual path must also CLEAR a stale error, so a config that already
// carried the bogus mark recovers on the next quiet run.
func TestGenerateNoUnreadItemsClearsStaleError(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "nopoolexisting@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Quiet Feed",
		Url:    "https://example.com/quiet.xml",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	cfg, err := queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Quiet Config",
		Schedule: "0 5 * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":2,"comments":5}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}
	if err := queries.UpdateDigestConfigError(t.Context(), store.UpdateDigestConfigErrorParams{
		ID: cfg.ID, Error: "no unread items found for digest config 1",
	}); err != nil {
		t.Fatalf("seed error: %v", err)
	}

	w := makeRequest(server, http.MethodPost, "/api/digest-configs/"+itoa(cfg.ID)+"/generate", nil, token)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body.String())
	}

	after, err := queries.GetDigestConfig(t.Context(), store.GetDigestConfigParams{
		ID:     cfg.ID,
		UserID: user.ID,
	})
	if err != nil {
		t.Fatalf("get digest config: %v", err)
	}
	if after.LastError != "" {
		t.Errorf("last_error = %q, want empty: a quiet run must clear the stale mark", after.LastError)
	}
}
