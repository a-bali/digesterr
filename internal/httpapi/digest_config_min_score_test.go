package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"digesterr/internal/store"
)

// TestDigestConfigMinScoreValidation covers the API-level validation of the
// min_score config field and the persistence round-trip: create with a
// min_score, read it back, and update it.
func TestDigestConfigMinScoreValidation(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "minscore@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	t.Run("negative min_score rejected on create", func(t *testing.T) {
		w := makeRequest(server, http.MethodPost, "/api/digest-configs", map[string]string{
			"schedule": "0 5 * * *",
			"config":   `{"min_score": -1}`,
		}, token)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("invalid config JSON rejected on create", func(t *testing.T) {
		w := makeRequest(server, http.MethodPost, "/api/digest-configs", map[string]string{
			"schedule": "0 5 * * *",
			"config":   `{"min_score": `,
		}, token)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("valid min_score persists and round-trips", func(t *testing.T) {
		w := makeRequest(server, http.MethodPost, "/api/digest-configs", map[string]string{
			"name":     "Daily",
			"schedule": "0 5 * * *",
			"config":   `{"feeds":[{"id":1,"highlights":3,"comments":5}],"min_score":50}`,
		}, token)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}
		var cfg store.DigestConfig
		if err := json.NewDecoder(w.Body).Decode(&cfg); err != nil {
			t.Fatalf("decode created config: %v", err)
		}

		var saved struct {
			MinScore int `json:"min_score"`
		}
		if err := json.Unmarshal([]byte(cfg.Config), &saved); err != nil {
			t.Fatalf("parse saved config: %v", err)
		}
		if saved.MinScore != 50 {
			t.Fatalf("saved min_score = %d, want 50", saved.MinScore)
		}

		// Read back through the API.
		w = makeRequest(server, http.MethodGet, "/api/digest-configs/"+itoa(cfg.ID), nil, token)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var got store.DigestConfig
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Fatalf("decode fetched config: %v", err)
		}
		if err := json.Unmarshal([]byte(got.Config), &saved); err != nil {
			t.Fatalf("parse fetched config: %v", err)
		}
		if saved.MinScore != 50 {
			t.Fatalf("round-trip min_score = %d, want 50", saved.MinScore)
		}

		// Update to a new threshold.
		w = makeRequest(server, http.MethodPatch, "/api/digest-configs/"+itoa(cfg.ID), map[string]string{
			"name":     "Daily",
			"schedule": "0 5 * * *",
			"config":   `{"min_score": 100}`,
		}, token)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 on update, got %d: %s", w.Code, w.Body.String())
		}
		var updated store.DigestConfig
		if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
			t.Fatalf("decode updated config: %v", err)
		}
		if err := json.Unmarshal([]byte(updated.Config), &saved); err != nil {
			t.Fatalf("parse updated config: %v", err)
		}
		if saved.MinScore != 100 {
			t.Fatalf("updated min_score = %d, want 100", saved.MinScore)
		}
	})

	t.Run("negative min_score rejected on update", func(t *testing.T) {
		w := makeRequest(server, http.MethodPost, "/api/digest-configs", map[string]string{
			"schedule": "0 5 * * *",
			"config":   `{"min_score": 10}`,
		}, token)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}
		var cfg store.DigestConfig
		if err := json.NewDecoder(w.Body).Decode(&cfg); err != nil {
			t.Fatalf("decode created config: %v", err)
		}
		w = makeRequest(server, http.MethodPatch, "/api/digest-configs/"+itoa(cfg.ID), map[string]string{
			"name":     "Daily",
			"schedule": "0 5 * * *",
			"config":   `{"min_score": -5}`,
		}, token)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 on update, got %d: %s", w.Code, w.Body.String())
		}
	})
}
