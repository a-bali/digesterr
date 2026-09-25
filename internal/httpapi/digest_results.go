package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"digesterr/internal/digest"
	"digesterr/internal/store"
)

// inDigestTx runs fn in a transaction against the digest tables. Every digest
// read cascade belongs in one: the cascade flips linked items from digested to
// read, so running it as a separate statement after the result update leaves the
// two halves inconsistent whenever the cascade fails — results read, items still
// digested, and (before this) an error nobody looked at. Errors are returned,
// never discarded, so the handler can fail the request and the rollback undoes
// the result update with it.
func (s *Server) inDigestTx(ctx context.Context, what string, fn func(context.Context, *store.Queries) error) error {
	db := s.queries.DB()
	if db == nil {
		return fmt.Errorf("%s requires a *sql.DB-backed Queries", what)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning %s: %w", what, err)
	}
	defer tx.Rollback()
	if err := fn(ctx, s.queries.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing %s: %w", what, err)
	}
	return nil
}

// markDigestResultsRead flips digest results read and cascades their linked
// items, in one transaction. configID nil means every result the user owns.
func (s *Server) markDigestResultsRead(ctx context.Context, userID int64, configID *int64) error {
	return s.inDigestTx(ctx, "mark-digest-read", func(ctx context.Context, q *store.Queries) error {
		if configID != nil {
			if err := q.MarkDigestConfigRead(ctx, store.MarkDigestConfigReadParams{
				ConfigID: *configID,
				UserID:   userID,
			}); err != nil {
				return fmt.Errorf("marking digest config read: %w", err)
			}
		} else {
			if err := q.MarkAllUserDigestResultsRead(ctx, userID); err != nil {
				return fmt.Errorf("marking all digest results read: %w", err)
			}
		}
		params := store.MarkDigestConfigResultItemsReadParams{UserID: userID}
		if configID != nil {
			params.Column2 = *configID
		}
		if _, err := q.MarkDigestConfigResultItemsRead(ctx, params); err != nil {
			return fmt.Errorf("flipping digest items: %w", err)
		}
		return nil
	})
}

func (s *Server) handleMarkDigestResultsRead(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	var body struct {
		ConfigID *int64 `json:"config_id"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	if err := s.markDigestResultsRead(r.Context(), userID, body.ConfigID); err != nil {
		writeServerError(w, "failed to mark digest results read", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "digest results marked read"})
}

// handleMarkDigestReadAbove marks all digest results above (or at) the given result as read.
func (s *Server) handleMarkDigestReadAbove(w http.ResponseWriter, r *http.Request) {
	s.handleMarkDigestRelative(w, r, "above")
}

func (s *Server) handleMarkDigestReadBelow(w http.ResponseWriter, r *http.Request) {
	s.handleMarkDigestRelative(w, r, "below")
}

func (s *Server) handleMarkDigestRelative(w http.ResponseWriter, r *http.Request, direction string) {
	userID := authedUserID(r)
	resultID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	// Parse scope from request body.
	var body struct {
		ConfigID *int64 `json:"config_id"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	// Confirm the result exists and belongs to the caller.
	if _, err := s.queries.GetDigestResult(r.Context(), store.GetDigestResultParams{ID: resultID, UserID: userID}); err != nil {
		writeError(w, http.StatusNotFound, "digest result not found")
		return
	}

	// Results and their cascade commit together: a failed cascade must not
	// leave results read with their items still digested.
	err = s.inDigestTx(r.Context(), "mark-digest-read-relative", func(ctx context.Context, q *store.Queries) error {
		if body.ConfigID != nil {
			if direction == "above" {
				if err := q.MarkDigestConfigReadAbove(ctx, store.MarkDigestConfigReadAboveParams{
					ConfigID:   *body.ConfigID,
					UserID:     userID,
					BoundaryID: resultID,
				}); err != nil {
					return fmt.Errorf("marking digest config read above: %w", err)
				}
			} else {
				if err := q.MarkDigestConfigReadBelow(ctx, store.MarkDigestConfigReadBelowParams{
					ConfigID:   *body.ConfigID,
					UserID:     userID,
					BoundaryID: resultID,
				}); err != nil {
					return fmt.Errorf("marking digest config read below: %w", err)
				}
			}
		} else {
			if direction == "above" {
				if err := q.MarkAllDigestReadAbove(ctx, store.MarkAllDigestReadAboveParams{
					UserID:     userID,
					BoundaryID: resultID,
				}); err != nil {
					return fmt.Errorf("marking digest read above: %w", err)
				}
			} else {
				if err := q.MarkAllDigestReadBelow(ctx, store.MarkAllDigestReadBelowParams{
					UserID:     userID,
					BoundaryID: resultID,
				}); err != nil {
					return fmt.Errorf("marking digest read below: %w", err)
				}
			}
		}

		// Cascade: flip digested items for the newly read results.
		params := store.MarkDigestConfigResultItemsReadParams{UserID: userID}
		if body.ConfigID != nil {
			params.Column2 = *body.ConfigID
		}
		if _, err := q.MarkDigestConfigResultItemsRead(ctx, params); err != nil {
			return fmt.Errorf("flipping digest items: %w", err)
		}
		return nil
	})
	if err != nil {
		writeServerError(w, "failed to mark digest results read", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "digest results marked read"})
}

func (s *Server) handleGenerateDigest(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	configID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	cfg, err := s.queries.GetDigestConfig(r.Context(), store.GetDigestConfigParams{
		ID:     configID,
		UserID: userID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "digest config not found")
		return
	}

	if s.generator == nil {
		writeError(w, http.StatusServiceUnavailable, "digest generation not available")
		return
	}

	result, err := s.generator.Generate(r.Context(), cfg, s.resolveLLMClient(r.Context(), userID))
	if errors.Is(err, digest.ErrGenerationInProgress) {
		// A run for this config is already going (typically a scheduled tick
		// that is still on its LLM call). Report it as a conflict rather than
		// a server error, and do NOT write it to the config's last_error: the
		// config is healthy, the request was simply too early.
		writeError(w, http.StatusConflict, "a digest generation for this config is already in progress")
		return
	}
	if errors.Is(err, digest.ErrNoUnreadItems) {
		// Nothing to digest. Not a failure, so it must not be stored as one —
		// and the response says so, so the UI can report it as a quiet no-op
		// instead of "Digest generation failed" on a healthy config.
		s.queries.ClearDigestConfigError(r.Context(), configID)
		writeError(w, http.StatusConflict, "no unread items to digest")
		return
	}
	if err != nil {
		// The full error is server-side only. Upstream LLM bodies can be long
		// and carry provider internals, and the config error column is surfaced
		// in the UI — so both the client and the stored copy get a stable,
		// truncated message instead of the raw error chain.
		slog.Error("digest generation failed", "config_id", configID, "config_name", cfg.Name, "user_id", userID, "error", err)
		s.queries.UpdateDigestConfigError(r.Context(), store.UpdateDigestConfigErrorParams{
			ID:    configID,
			Error: truncateError(err, 500),
		})
		writeError(w, http.StatusInternalServerError, "failed to generate digest")
		return
	}
	s.queries.ClearDigestConfigError(r.Context(), configID)
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) handleListDigestResultItemsWithData(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	resultID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	result, err := s.queries.GetDigestResult(r.Context(), store.GetDigestResultParams{
		ID:     resultID,
		UserID: userID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "digest result not found")
		return
	}
	_ = result

	items, err := s.queries.ListDigestResultItemsWithData(r.Context(), resultID)
	if err != nil {
		writeServerError(w, "failed to list digest result items", err)
		return
	}
	if items == nil {
		items = []store.ListDigestResultItemsWithDataRow{}
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleDigestUnreadCounts(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	counts, err := s.queries.GetDigestUnreadCounts(r.Context(), userID)
	if err != nil {
		writeServerError(w, "failed to get digest unread counts", err)
		return
	}
	if counts == nil {
		counts = []store.GetDigestUnreadCountsRow{}
	}
	result := make(map[int64]int64, len(counts))
	for _, c := range counts {
		result[c.ConfigID] = c.UnreadCount
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleListAllDigestResults(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)

	limit := 50
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 100 {
			limit = v
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			offset = v
		}
	}

	results, err := s.queries.ListAllDigestResultsByUser(r.Context(), store.ListAllDigestResultsByUserParams{
		UserID: userID,
		Limit:  int64(limit + 1),
		Offset: int64(offset),
	})
	if err != nil {
		writeServerError(w, "failed to list digest results", err)
		return
	}

	hasMore := len(results) > limit
	if hasMore {
		results = results[:limit]
	}

	var total int64
	if hasMore || offset > 0 {
		total, err = s.queries.CountAllDigestResultsByUser(r.Context(), userID)
		if err != nil {
			writeServerError(w, "failed to count digest results", err)
			return
		}
	}

	if results == nil {
		results = []store.ListAllDigestResultsByUserRow{}
	}

	type resultPreview struct {
		ID         int64  `json:"id"`
		ConfigID   int64  `json:"config_id"`
		Status     string `json:"status"`
		CreatedAt  string `json:"created_at"`
		UpdatedAt  string `json:"updated_at"`
		ConfigName string `json:"config_name"`
		Title      string `json:"title"`
		ItemCount  int64  `json:"item_count"`
	}
	items := make([]resultPreview, len(results))
	for i, r := range results {
		items[i] = resultPreview{
			ID:         r.ID,
			ConfigID:   r.ConfigID,
			Status:     r.Status,
			CreatedAt:  r.CreatedAt.Format(time.RFC3339),
			UpdatedAt:  r.UpdatedAt.Format(time.RFC3339),
			ConfigName: r.ConfigName,
			Title:      r.Title,
			ItemCount:  r.ItemCount,
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"results":  items,
		"has_more": hasMore,
		"total":    total,
	})
}
