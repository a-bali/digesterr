package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"digesterr/internal/store"
)

// Both flags clean the article and both write items.extract_content, so whichever
// ran last would decide what the reader sees. The API must refuse the combination
// rather than pick a winner.
func TestCreateFeedRejectsExtractAndReadabilityTogether(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "both@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	w := makeRequest(server, http.MethodPost, "/api/feeds", map[string]interface{}{
		"type": "rss", "name": "Both", "url": "https://example.com/feed",
		"extract_content": 1, "readability": 1,
	}, token)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "mutually exclusive") {
		t.Errorf("error should name the conflict, got %s", body)
	}
	// Nothing may be created: a rejected request must not leave a half-configured feed.
	feeds, err := queries.ListFeeds(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("list feeds: %v", err)
	}
	if len(feeds) != 0 {
		t.Fatalf("feed was created despite the rejection: %d feeds", len(feeds))
	}
}

// Each flag alone must work, and round-trip: this is the guard against "reject the
// combination" accidentally becoming "reject the flags".
func TestCreateFeedAcceptsEachFlagAlone(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload map[string]interface{}
		check   func(t *testing.T, raw []byte)
	}{
		{
			name: "readability only",
			payload: map[string]interface{}{
				"type": "rss", "name": "Rd", "url": "https://example.com/rd", "readability": 1,
			},
			check: func(t *testing.T, raw []byte) {
				var f map[string]interface{}
				if err := json.Unmarshal(raw, &f); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if f["readability"] != float64(1) {
					t.Errorf("readability = %v, want 1", f["readability"])
				}
				if f["extract_content"] != float64(0) {
					t.Errorf("extract_content = %v, want 0", f["extract_content"])
				}
			},
		},
		{
			name: "extraction only",
			payload: map[string]interface{}{
				"type": "rss", "name": "Ex", "url": "https://example.com/ex", "extract_content": 1,
			},
			check: func(t *testing.T, raw []byte) {
				var f map[string]interface{}
				if err := json.Unmarshal(raw, &f); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if f["extract_content"] != float64(1) {
					t.Errorf("extract_content = %v, want 1", f["extract_content"])
				}
				if f["readability"] != float64(0) {
					t.Errorf("readability = %v, want 0", f["readability"])
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, queries := setupTestServer(t)
			user := createTestUser(t, queries, "one@example.com", "password123")
			token := getTestToken(t, server.auth, &user)

			w := makeRequest(server, http.MethodPost, "/api/feeds", tc.payload, token)
			if w.Code != http.StatusOK && w.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 2xx (%s)", w.Code, w.Body.String())
			}
			tc.check(t, w.Body.Bytes())
		})
	}
}

// The same rule on the update path, which is how a user would actually reach the
// conflict: turn one on while the other is already on.
func TestUpdateFeedRejectsEnablingBothFlags(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "upd@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	// A feed with readability on.
	created := makeRequest(server, http.MethodPost, "/api/feeds", map[string]interface{}{
		"type": "rss", "name": "Rd", "url": "https://example.com/rd", "readability": 1,
	}, token)
	if created.Code != http.StatusOK && created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var feed map[string]interface{}
	if err := json.Unmarshal(created.Body.Bytes(), &feed); err != nil {
		t.Fatalf("decode created feed: %v", err)
	}
	id := int(feed["id"].(float64))

	// Now try to also turn extraction on.
	w := makeRequest(server, http.MethodPatch, "/api/feeds/"+strconv.Itoa(id), map[string]interface{}{
		"extract_content": 1,
	}, token)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", w.Code, w.Body.String())
	}

	// And the stored feed must be unchanged by the rejected request.
	feeds, err := queries.ListFeeds(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("list feeds: %v", err)
	}
	found := false
	for _, f := range feeds {
		if f.ID == int64(id) {
			found = true
			if f.Readability != 1 {
				t.Errorf("readability = %d, want 1 (unchanged)", f.Readability)
			}
			if f.ExtractContent != 0 {
				t.Errorf("extract_content = %d, want 0 (the update must not have applied)", f.ExtractContent)
			}
		}
	}
	if !found {
		t.Fatal("feed disappeared")
	}
}

// The update path must actually PERSIST the flag, not just validate it: turning
// readability on for an existing feed is the primary way a user reaches it.
func TestUpdateFeedPersistsReadability(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "persist@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	created := makeRequest(server, http.MethodPost, "/api/feeds", map[string]interface{}{
		"type": "rss", "name": "Plain", "url": "https://example.com/p",
	}, token)
	if created.Code != http.StatusOK && created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var feed map[string]interface{}
	if err := json.Unmarshal(created.Body.Bytes(), &feed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	id := int64(feed["id"].(float64))

	// Turn readability ON.
	w := makeRequest(server, http.MethodPatch, "/api/feeds/"+strconv.FormatInt(id, 10), map[string]interface{}{
		"readability": 1,
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	got, err := queries.GetFeed(t.Context(), store.GetFeedParams{ID: id, UserID: user.ID})
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if got.Readability != 1 {
		t.Fatalf("readability = %d after the update, want 1", got.Readability)
	}

	// And turning it OFF must also persist, or the flag would be a one-way switch.
	w = makeRequest(server, http.MethodPatch, "/api/feeds/"+strconv.FormatInt(id, 10), map[string]interface{}{
		"readability": 0,
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("second update: %d %s", w.Code, w.Body.String())
	}
	got, err = queries.GetFeed(t.Context(), store.GetFeedParams{ID: id, UserID: user.ID})
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if got.Readability != 0 {
		t.Fatalf("readability = %d after switching it off, want 0", got.Readability)
	}
}
