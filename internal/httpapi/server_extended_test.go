package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"digesterr/internal/store"
)

func TestSignout(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "signout@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	w := makeRequest(server, http.MethodPost, "/api/auth/signout", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["message"] != "signed out" {
		t.Fatalf("expected message 'signed out', got %s", resp["message"])
	}
}

func TestRefreshTokenFlow(t *testing.T) {
	server, _ := setupTestServer(t)
	_ = createTestUser(t, server.queries, "refresh@example.com", "password123")

	w := makeRequest(server, http.MethodPost, "/api/auth/signin", map[string]string{
		"email":    "refresh@example.com",
		"password": "password123",
	}, "")
	if w.Code != http.StatusOK {
		t.Fatalf("signin failed: %d: %s", w.Code, w.Body.String())
	}

	cookies := w.Result().Cookies()
	var refreshToken string
	for _, c := range cookies {
		if c.Name == "refresh_token" {
			refreshToken = c.Value
		}
	}
	if refreshToken == "" {
		t.Fatal("expected refresh_token cookie in signin response")
	}

	refreshReq := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)
	refreshReq.AddCookie(&http.Cookie{Name: "refresh_token", Value: refreshToken})
	refreshReq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, refreshReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for refresh, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["access_token"] == nil {
		t.Fatal("expected access_token in refresh response")
	}
}

func TestChangePassword(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "changepass@example.com", "oldpassword")
	token := getTestToken(t, server.auth, &user)

	w := makeRequest(server, http.MethodPost, "/api/auth/change-password", map[string]string{
		"old_password": "oldpassword",
		"new_password": "newpassword",
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = makeRequest(server, http.MethodPost, "/api/auth/signin", map[string]string{
		"email":    "changepass@example.com",
		"password": "newpassword",
	}, "")
	if w.Code != http.StatusOK {
		t.Fatalf("signin with new password failed: %d", w.Code)
	}
}

func TestChangePasswordWrongOld(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "wrongold@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	w := makeRequest(server, http.MethodPost, "/api/auth/change-password", map[string]string{
		"old_password": "wrongpassword",
		"new_password": "newpassword",
	}, token)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong old password, got %d", w.Code)
	}
}

func TestSigninNonexistentEmail(t *testing.T) {
	server, _ := setupTestServer(t)
	w := makeRequest(server, http.MethodPost, "/api/auth/signin", map[string]string{
		"email":    "nonexistent@example.com",
		"password": "password123",
	}, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for nonexistent email, got %d", w.Code)
	}
}

func TestSignupResponseBody(t *testing.T) {
	server, _ := setupTestServer(t)
	w := makeRequest(server, http.MethodPost, "/api/auth/signup", map[string]string{
		"email":    "bodycheck@example.com",
		"password": "password123",
	}, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["access_token"] == nil {
		t.Fatal("expected access_token in signup response")
	}
	if resp["user"] == nil {
		t.Fatal("expected user in signup response")
	}
}

func TestUserIsolation(t *testing.T) {
	server, queries := setupTestServer(t)
	userA := createTestUser(t, queries, "userA@example.com", "password123")
	userB := createTestUser(t, queries, "userB@example.com", "password123")
	tokenA := getTestToken(t, server.auth, &userA)

	// Same category name on both users' feeds; the label stream must be
	// scoped per user.
	feedA, _ := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID:   userA.ID,
		Category: "Shared",
		Type:     "rss",
		Name:     "AFeed",
		Url:      "https://a.example.com/feed",
		Config:   "{}",
		Status:   "active",
	})
	feedB, _ := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID:   userB.ID,
		Category: "Shared",
		Type:     "rss",
		Name:     "BFeed",
		Url:      "https://b.example.com/feed",
		Config:   "{}",
		Status:   "active",
	})
	itemA, _ := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID:  feedA.ID,
		Hash:    "a-item-1",
		Title:   "A Item",
		Url:     "https://a.example.com/1",
		Content: "content",
		Status:  "unread",
	})
	itemB, _ := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID:  feedB.ID,
		Hash:    "b-item-1",
		Title:   "B Item",
		Url:     "https://b.example.com/1",
		Content: "content",
		Status:  "unread",
	})

	// Category items are scoped to the owning user even when names collide.
	itemsA, err := queries.ListItemsByCategory(t.Context(), store.ListItemsByCategoryParams{
		Category: "Shared",
		UserID:   userA.ID,
	})
	if err != nil {
		t.Fatalf("list items by category: %v", err)
	}
	if len(itemsA) != 1 || itemsA[0].ID != itemA.ID {
		t.Fatalf("user A should only see their own items in category 'Shared', got %v", itemsA)
	}
	itemsB, err := queries.ListItemsByCategory(t.Context(), store.ListItemsByCategoryParams{
		Category: "Shared",
		UserID:   userB.ID,
	})
	if err != nil {
		t.Fatalf("list items by category: %v", err)
	}
	if len(itemsB) != 1 || itemsB[0].ID != itemB.ID {
		t.Fatalf("user B should only see their own items in category 'Shared', got %v", itemsB)
	}

	// Feeds API returns only the authenticated user's feeds.
	w := makeRequest(server, http.MethodGet, "/api/feeds", nil, tokenA)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var feeds []store.Feed
	json.NewDecoder(w.Body).Decode(&feeds)
	if len(feeds) != 1 || feeds[0].Name != "AFeed" {
		t.Fatalf("user A should only see their own feeds, got %v", feeds)
	}

	w = makeRequest(server, http.MethodPatch, "/api/items/"+itoa(itemB.ID), map[string]string{"status": "read"}, tokenA)
	if w.Code != http.StatusNotFound {
		t.Fatalf("user A should not update user B's item, got %d", w.Code)
	}

	w = makeRequest(server, http.MethodPatch, "/api/items/"+itoa(itemA.ID), map[string]string{"status": "read"}, tokenA)
	if w.Code != http.StatusOK {
		t.Fatalf("user A should update their own item, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateFeed(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "updatefeed@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	w := makeRequest(server, http.MethodPost, "/api/feeds", map[string]interface{}{
		"category": "Tech",
		"type":     "rss",
		"name":     "OriginalFeed",
		"url":      "https://example.com/feed",
	}, token)
	var feed store.Feed
	json.NewDecoder(w.Body).Decode(&feed)

	w = makeRequest(server, http.MethodPatch, "/api/feeds/"+itoa(feed.ID), map[string]interface{}{
		"name": "UpdatedFeed",
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	json.NewDecoder(w.Body).Decode(&feed)
	if feed.Name != "UpdatedFeed" {
		t.Fatalf("expected name 'UpdatedFeed', got %s", feed.Name)
	}
	if feed.Category != "Tech" {
		t.Fatalf("expected category to be preserved, got %q", feed.Category)
	}

	// Moving a feed to a different category via PATCH.
	w = makeRequest(server, http.MethodPatch, "/api/feeds/"+itoa(feed.ID), map[string]interface{}{
		"category": "News",
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	json.NewDecoder(w.Body).Decode(&feed)
	if feed.Category != "News" {
		t.Fatalf("expected category 'News', got %q", feed.Category)
	}
}

func TestDeleteFeed(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "delfeed@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	w := makeRequest(server, http.MethodPost, "/api/feeds", map[string]interface{}{
		"category": "Tech",
		"type":     "rss",
		"name":     "ToDelete",
		"url":      "https://example.com/feed",
	}, token)
	var feed store.Feed
	json.NewDecoder(w.Body).Decode(&feed)

	w = makeRequest(server, http.MethodDelete, "/api/feeds/"+itoa(feed.ID), nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = makeRequest(server, http.MethodGet, "/api/feeds/"+itoa(feed.ID), nil, token)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", w.Code)
	}
}

func TestDeleteFeedPrunesDigestConfigs(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "delfeedprune@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	// Two feeds: one to delete (with its own category) and one that stays.
	feedA, _ := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Category: "GoneCat", Type: "hn", Name: "A",
		Url: "https://example.com/a", Config: "{}", Status: "active",
	})
	feedB, _ := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Category: "KeepCat", Type: "rss", Name: "B",
		Url: "https://example.com/b", Config: "{}", Status: "active",
	})

	// Config 1 references the doomed feed by ID; config 2 selects its
	// category; config 3 references neither and must stay untouched.
	mk := func(name string, config string) store.DigestConfig {
		w := makeRequest(server, http.MethodPost, "/api/digest-configs", map[string]interface{}{
			"name": name, "schedule": "0 8 * * *", "config": config,
		}, token)
		if w.Code != http.StatusOK && w.Code != http.StatusCreated {
			t.Fatalf("create config %s: got %d: %s", name, w.Code, w.Body.String())
		}
		var dc store.DigestConfig
		json.NewDecoder(w.Body).Decode(&dc)
		return dc
	}
	cfgFeeds := mk("by-feed", `{"feeds":[{"id":`+itoa(feedA.ID)+`,"highlights":3,"comments":5}]}`)
	cfgCats := mk("by-category", `{"categories":[{"name":"GoneCat","highlights":1,"comments":2}]}`)
	mk("untouched", `{"feeds":[{"id":`+itoa(feedB.ID)+`,"highlights":1,"comments":1}]}`)

	w := makeRequest(server, http.MethodDelete, "/api/feeds/"+itoa(feedA.ID), nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("delete failed: %d: %s", w.Code, w.Body.String())
	}

	check := func(dc store.DigestConfig, wantFeeds, wantCats string) {
		t.Helper()
		var cfg map[string]any
		row, err := queries.GetDigestConfig(t.Context(), store.GetDigestConfigParams{ID: dc.ID, UserID: user.ID})
		if err != nil {
			t.Fatalf("loading config %s: %v", dc.Name, err)
		}
		json.Unmarshal([]byte(row.Config), &cfg)
		fj, _ := json.Marshal(cfg["feeds"])
		cj, _ := json.Marshal(cfg["categories"])
		if string(fj) != wantFeeds || string(cj) != wantCats {
			t.Errorf("config %s: feeds=%s (want %s), categories=%s (want %s)",
				dc.Name, fj, wantFeeds, cj, wantCats)
		}
	}
	check(cfgFeeds, "[]", "null")
	check(cfgCats, "null", "[]")

	// The untouched config keeps its single feed reference.
	var sawUntouched bool
	rows, _ := queries.ListDigestConfigs(t.Context(), user.ID)
	for _, rc := range rows {
		if !strings.Contains(rc.Config, itoa(feedB.ID)) || strings.Contains(rc.Name, "untouched") {
			sawUntouched = strings.Contains(rc.Name, "untouched") && strings.Contains(rc.Config, `"feeds":[{"id":`)
		}
	}
	if !sawUntouched {
		t.Error("untouched config missing or modified")
	}
}

func TestUpdateDigestResultStatus(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "itemstatus@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feed, _ := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID:   user.ID,
		Category: "Tech",
		Type:     "rss",
		Name:     "Test",
		Url:      "https://example.com/feed",
		Config:   "{}",
		Status:   "active",
	})
	item, _ := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID:  feed.ID,
		Hash:    "item-1",
		Title:   "Test Item",
		Url:     "https://example.com/1",
		Content: "content",
		Status:  "unread",
	})

	w := makeRequest(server, http.MethodPatch, "/api/items/"+itoa(item.ID), map[string]string{"status": "read"}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated store.Item
	json.NewDecoder(w.Body).Decode(&updated)
	if updated.Status != "read" {
		t.Fatalf("expected status 'read', got %s", updated.Status)
	}

	w = makeRequest(server, http.MethodPatch, "/api/items/"+itoa(item.ID), map[string]string{"status": "saved"}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	json.NewDecoder(w.Body).Decode(&updated)
	if updated.Status != "saved" {
		t.Fatalf("expected status 'saved', got %s", updated.Status)
	}
}

func TestUpdateItemStatusInvalid(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "iteminvalid@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feed, _ := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID:   user.ID,
		Category: "Tech",
		Type:     "rss",
		Name:     "Test",
		Url:      "https://example.com/feed",
		Config:   "{}",
		Status:   "active",
	})
	item, _ := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID:  feed.ID,
		Hash:    "item-2",
		Title:   "Test Item",
		Url:     "https://example.com/1",
		Content: "content",
		Status:  "unread",
	})

	w := makeRequest(server, http.MethodPatch, "/api/items/"+itoa(item.ID), map[string]string{"status": "invalid"}, token)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid status, got %d", w.Code)
	}
}

func TestDigestConfigCRUD(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "digestcfg@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	w := makeRequest(server, http.MethodPost, "/api/digest-configs", map[string]string{
		"schedule": "0 5 * * *",
		"config":   `{"feeds": [{"id": 1}]}`,
	}, token)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var cfg store.DigestConfig
	json.NewDecoder(w.Body).Decode(&cfg)
	if cfg.Schedule != "0 5 * * *" {
		t.Fatalf("expected schedule '0 5 * * *', got %s", cfg.Schedule)
	}

	w = makeRequest(server, http.MethodGet, "/api/digest-configs/"+itoa(cfg.ID), nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	w = makeRequest(server, http.MethodPatch, "/api/digest-configs/"+itoa(cfg.ID), map[string]string{
		"schedule": "0 */4 * * *",
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	json.NewDecoder(w.Body).Decode(&cfg)
	if cfg.Schedule != "0 */4 * * *" {
		t.Fatalf("expected schedule '0 */4 * * *', got %s", cfg.Schedule)
	}

	w = makeRequest(server, http.MethodDelete, "/api/digest-configs/"+itoa(cfg.ID), nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = makeRequest(server, http.MethodGet, "/api/digest-configs/"+itoa(cfg.ID), nil, token)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", w.Code)
	}
}

func TestDigestResultReadCascade(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "cascade@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feed, _ := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID:   user.ID,
		Category: "Tech",
		Type:     "rss",
		Name:     "Test",
		Url:      "https://example.com/feed",
		Config:   "{}",
		Status:   "active",
	})

	item1, _ := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID:  feed.ID,
		Hash:    "cascade-1",
		Title:   "Item 1",
		Url:     "https://example.com/1",
		Content: "content 1",
		Status:  "unread",
	})
	item2, _ := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID:  feed.ID,
		Hash:    "cascade-2",
		Title:   "Item 2",
		Url:     "https://example.com/2",
		Content: "content 2",
		Status:  "unread",
	})

	_, _ = queries.UpdateItemStatus(t.Context(), store.UpdateItemStatusParams{
		ID:     item1.ID,
		Status: "digested",
	})
	_, _ = queries.UpdateItemStatus(t.Context(), store.UpdateItemStatusParams{
		ID:     item2.ID,
		Status: "digested",
	})

	cfg, _ := queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID:   user.ID,
		Schedule: "0 5 * * *",
		Config:   "{}",
	})
	result, _ := queries.CreateDigestResult(t.Context(), store.CreateDigestResultParams{
		UserID:         user.ID,
		ConfigID:       cfg.ID,
		ConfigSnapshot: "{}",
		Content:        "{}",
		Status:         "new",
	})
	queries.CreateDigestResultItem(t.Context(), store.CreateDigestResultItemParams{
		ResultID:    result.ID,
		ItemID:      item1.ID,
		IsHighlight: 1,
	})
	queries.CreateDigestResultItem(t.Context(), store.CreateDigestResultItemParams{
		ResultID:    result.ID,
		ItemID:      item2.ID,
		IsHighlight: 0,
	})

	w := makeRequest(server, http.MethodPatch, "/api/digest-results/"+itoa(result.ID), map[string]string{"status": "read"}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Spec: marking a digest result read flips its linked items from
	// digested to read.
	updated1, _ := queries.GetItem(t.Context(), item1.ID)
	if updated1.Status != "read" {
		t.Fatalf("expected item1 status 'read' after cascade, got %s", updated1.Status)
	}
	updated2, _ := queries.GetItem(t.Context(), item2.ID)
	if updated2.Status != "read" {
		t.Fatalf("expected item2 status 'read' after cascade, got %s", updated2.Status)
	}
}

func TestFeedItemsByStatus(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "feeditems@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	feed, _ := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID:   user.ID,
		Category: "Tech",
		Type:     "rss",
		Name:     "Test",
		Url:      "https://example.com/feed",
		Config:   "{}",
		Status:   "active",
	})
	queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID:  feed.ID,
		Hash:    "feedstatus-1",
		Title:   "Unread Item",
		Url:     "https://example.com/1",
		Content: "content",
		Status:  "unread",
	})
	queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID:  feed.ID,
		Hash:    "feedstatus-2",
		Title:   "Read Item",
		Url:     "https://example.com/2",
		Content: "content",
		Status:  "read",
	})

	w := makeRequest(server, http.MethodGet, "/api/feeds/"+itoa(feed.ID)+"/items?status=unread", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result struct {
		Items []store.Item `json:"items"`
	}
	json.NewDecoder(w.Body).Decode(&result)
	if len(result.Items) != 1 || result.Items[0].Status != "unread" {
		t.Fatalf("expected 1 unread item, got %d", len(result.Items))
	}

	w = makeRequest(server, http.MethodGet, "/api/feeds/"+itoa(feed.ID)+"/items", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	json.NewDecoder(w.Body).Decode(&result)
	if len(result.Items) != 2 {
		t.Fatalf("expected 2 items total, got %d", len(result.Items))
	}
}

func TestInvalidPathParams(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "badparam@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	w := makeRequest(server, http.MethodGet, "/api/feeds/abc", nil, token)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-numeric id, got %d", w.Code)
	}

	w = makeRequest(server, http.MethodGet, "/api/feeds/99999", nil, token)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for nonexistent id, got %d", w.Code)
	}
}

func TestFetchFeedUnauthenticated(t *testing.T) {
	server, _ := setupTestServer(t)
	w := makeRequest(server, http.MethodPost, "/api/feeds/1/fetch", nil, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestFetchFeedInvalidID(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "fetchbad@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	w := makeRequest(server, http.MethodPost, "/api/feeds/abc/fetch", nil, token)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-numeric id, got %d", w.Code)
	}

	w = makeRequest(server, http.MethodPost, "/api/feeds/99999/fetch", nil, token)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for nonexistent feed, got %d", w.Code)
	}
}

func TestFetchFeedAnotherUsersFeed(t *testing.T) {
	server, queries := setupTestServer(t)
	userA := createTestUser(t, queries, "fetcha@example.com", "password123")
	userB := createTestUser(t, queries, "fetchb@example.com", "password123")
	tokenA := getTestToken(t, server.auth, &userA)

	feed, _ := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID:   userB.ID,
		Category: "B",
		Type:     "rss",
		Name:     "BFeed",
		Url:      "https://b.example.com/feed",
		Config:   "{}",
		Status:   "active",
	})
	w := makeRequest(server, http.MethodPost, "/api/feeds/"+itoa(feed.ID)+"/fetch", nil, tokenA)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
