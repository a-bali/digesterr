package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"digesterr/internal/store"
)

func (s *Server) handleMarkDigestResultsRead(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	var body struct {
		ConfigID *int64 `json:"config_id"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	ctx := r.Context()
	if body.ConfigID != nil {
		if err := s.queries.MarkDigestConfigRead(ctx, store.MarkDigestConfigReadParams{
			ConfigID: *body.ConfigID,
			UserID:   userID,
		}); err != nil {
			writeServerError(w, "failed to mark digest results read", err)
			return
		}
		// Cascade: flip digested items for this config's results.
		_, _ = s.queries.MarkDigestConfigResultItemsRead(ctx, store.MarkDigestConfigResultItemsReadParams{
			UserID: userID,
			Column2: body.ConfigID,
		})
	} else {
		if err := s.queries.MarkAllUserDigestResultsRead(ctx, userID); err != nil {
			writeServerError(w, "failed to mark all digest results read", err)
			return
		}
		// Cascade: flip all digested items.
		_, _ = s.queries.MarkDigestConfigResultItemsRead(ctx, store.MarkDigestConfigResultItemsReadParams{
			UserID: userID,
			Column2: nil,
		})
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

	if body.ConfigID != nil {
		if direction == "above" {
			err = s.queries.MarkDigestConfigReadAbove(r.Context(), store.MarkDigestConfigReadAboveParams{
				ConfigID:   *body.ConfigID,
				UserID:     userID,
				BoundaryID: resultID,
			})
		} else {
			err = s.queries.MarkDigestConfigReadBelow(r.Context(), store.MarkDigestConfigReadBelowParams{
				ConfigID:   *body.ConfigID,
				UserID:     userID,
				BoundaryID: resultID,
			})
		}
	} else {
		if direction == "above" {
			err = s.queries.MarkAllDigestReadAbove(r.Context(), store.MarkAllDigestReadAboveParams{
				UserID:     userID,
				BoundaryID: resultID,
			})
		} else {
			err = s.queries.MarkAllDigestReadBelow(r.Context(), store.MarkAllDigestReadBelowParams{
				UserID:     userID,
				BoundaryID: resultID,
			})
		}
	}
	if err != nil {
		writeServerError(w, "failed to mark digest results read", err)
		return
	}

	// Cascade: flip digested items for the newly read results.
	if body.ConfigID != nil {
		_, _ = s.queries.MarkDigestConfigResultItemsRead(r.Context(), store.MarkDigestConfigResultItemsReadParams{
			UserID:  userID,
			Column2: body.ConfigID,
		})
	} else {
		_, _ = s.queries.MarkDigestConfigResultItemsRead(r.Context(), store.MarkDigestConfigResultItemsReadParams{
			UserID:  userID,
			Column2: nil,
		})
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
		ID        int64  `json:"id"`
		ConfigID  int64  `json:"config_id"`
		Status    string `json:"status"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
		ConfigName string `json:"config_name"`
		Title     string `json:"title"`
		ItemCount int64  `json:"item_count"`
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

