package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"digesterr/internal/store"
)

// TestDigestConfigScheduleValidation covers the API-level rejection of a cron
// expression that can never run. The scheduler silently ignores such an
// expression, so an accepted-but-invalid schedule is a digest that never fires
// and never explains itself.
func TestDigestConfigScheduleValidation(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "schedule@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	bad := []struct {
		name     string
		schedule string
	}{
		{"four fields", "0 8 * *"},
		{"six fields", "0 8 * * * *"},
		{"minute out of range", "60 8 * * *"},
		{"hour out of range", "0 24 * * *"},
		{"day out of range", "0 8 32 * *"},
		{"month out of range", "0 8 * 13 *"},
		{"weekday out of range", "0 8 * * 8"},
		{"not a number", "abc * * * *"},
		{"zero step", "*/0 * * * *"},
	}
	for _, tc := range bad {
		t.Run("create rejects "+tc.name, func(t *testing.T) {
			w := makeRequest(server, http.MethodPost, "/api/digest-configs", map[string]string{
				"name":     "Bad Schedule",
				"schedule": tc.schedule,
				"config":   `{}`,
			}, token)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for schedule %q, got %d: %s", tc.schedule, w.Code, w.Body.String())
			}
		})
	}

	t.Run("create accepts a valid schedule", func(t *testing.T) {
		w := makeRequest(server, http.MethodPost, "/api/digest-configs", map[string]string{
			"name":     "Good Schedule",
			"schedule": "0 8 * * 1-5",
			"config":   `{}`,
		}, token)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}
		var cfg store.DigestConfig
		if err := json.NewDecoder(w.Body).Decode(&cfg); err != nil {
			t.Fatalf("decode created config: %v", err)
		}

		t.Run("update rejects an invalid schedule", func(t *testing.T) {
			w := makeRequest(server, http.MethodPatch, "/api/digest-configs/"+itoa(cfg.ID), map[string]string{
				"name":     "Good Schedule",
				"schedule": "0 8 * * * *",
				"config":   `{}`,
			}, token)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 on update, got %d: %s", w.Code, w.Body.String())
			}
		})

		t.Run("update accepts a valid schedule", func(t *testing.T) {
			w := makeRequest(server, http.MethodPatch, "/api/digest-configs/"+itoa(cfg.ID), map[string]string{
				"name":     "Good Schedule",
				"schedule": "30 7 * * *",
				"config":   `{}`,
			}, token)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
			}
		})
	})
}
