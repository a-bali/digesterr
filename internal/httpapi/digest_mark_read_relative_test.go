package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"digesterr/internal/store"
)

// TestDigestMarkReadRelativeExcludesBoundary pins the same contract the item
// endpoints have: neither direction marks the result being hovered.
//
// The digest list orders by created_at DESC alone, so results created in the
// same second are separated only by the id comparison inside the query.
func TestDigestMarkReadRelativeExcludesBoundary(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "digest-relative@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	cfg, err := queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "cfg",
		Schedule: "0 8 * * *",
		Config:   "{}",
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	// Three results. created_at comes from the database, so all three land in
	// the same second and only the id comparison separates them.
	var results []store.DigestResult
	for i := 0; i < 3; i++ {
		r, err := queries.CreateDigestResult(t.Context(), store.CreateDigestResultParams{
			UserID:         user.ID,
			ConfigID:       cfg.ID,
			ConfigSnapshot: fmt.Sprintf("r%d", i),
			Content:        "{}",
			Status:         "new",
		})
		if err != nil {
			t.Fatalf("create digest result %d: %v", i, err)
		}
		results = append(results, r)
	}

	// Newest first: r2, r1, r0 — the boundary is the middle result.
	boundary := results[1]
	newestFirst := []store.DigestResult{results[2], results[1], results[0]}

	for _, tt := range []struct {
		direction string
		want      []string
	}{
		{direction: "above", want: []string{"r2"}},
		{direction: "below", want: []string{"r0"}},
	} {
		t.Run(tt.direction, func(t *testing.T) {
			for _, r := range results {
				if _, err := queries.UpdateDigestResultStatus(context.Background(), store.UpdateDigestResultStatusParams{
					Status: "new", ID: r.ID, UserID: user.ID,
				}); err != nil {
					t.Fatalf("reset result %d: %v", r.ID, err)
				}
			}

			rec := makeRequest(server, http.MethodPost,
				fmt.Sprintf("/api/digest-results/%d/mark-read-%s", boundary.ID, tt.direction),
				map[string]any{}, token)
			if rec.Code != http.StatusOK {
				t.Fatalf("mark-read-%s returned %d, body: %s", tt.direction, rec.Code, rec.Body.String())
			}

			var got []string
			for _, r := range newestFirst {
				row, err := queries.GetDigestResult(context.Background(),
					store.GetDigestResultParams{ID: r.ID, UserID: user.ID})
				if err != nil {
					t.Fatalf("get result %d: %v", r.ID, err)
				}
				if row.Status == "read" {
					got = append(got, row.ConfigSnapshot)
				}
			}
			if !equalStrings(got, tt.want) {
				t.Errorf("marked %v, want %v", got, tt.want)
			}

			row, err := queries.GetDigestResult(context.Background(),
				store.GetDigestResultParams{ID: boundary.ID, UserID: user.ID})
			if err != nil {
				t.Fatalf("get boundary: %v", err)
			}
			if row.Status == "read" {
				t.Errorf("boundary result %q was marked by %s, want unread — both directions exclude the hovered row",
					boundary.ConfigSnapshot, tt.direction)
			}
		})
	}
}
