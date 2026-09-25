package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"digesterr/internal/digest"
	"digesterr/internal/store"
)

type createDigestConfigRequest struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Config   string `json:"config"`
}

func (s *Server) handleListDigestConfigs(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	configs, err := s.queries.ListDigestConfigs(r.Context(), userID)
	if err != nil {
		writeServerError(w, "failed to list digest configs", err)
		return
	}
	if configs == nil {
		configs = []store.DigestConfig{}
	}
	writeJSON(w, http.StatusOK, configs)
}

func (s *Server) handleCreateDigestConfig(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	var req createDigestConfigRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Schedule == "" {
		writeError(w, http.StatusBadRequest, "schedule is required")
		return
	}
	if err := digest.ValidateSchedule(req.Schedule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Config == "" {
		req.Config = "{}"
	}
	if err := digest.ValidateConfig(req.Config); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg, err := s.queries.CreateDigestConfig(r.Context(), store.CreateDigestConfigParams{
		UserID:   userID,
		Name:     req.Name,
		Schedule: req.Schedule,
		Config:   req.Config,
	})
	if err != nil {
		writeServerError(w, "failed to create digest config", err)
		return
	}
	writeJSON(w, http.StatusCreated, cfg)
}

func (s *Server) handleGetDigestConfig(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	cfg, err := s.queries.GetDigestConfig(r.Context(), store.GetDigestConfigParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "digest config not found")
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleUpdateDigestConfig(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req createDigestConfigRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if err := digest.ValidateSchedule(req.Schedule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Config == "" {
		req.Config = "{}"
	}
	if err := digest.ValidateConfig(req.Config); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg, err := s.queries.UpdateDigestConfig(r.Context(), store.UpdateDigestConfigParams{
		ID:       id,
		UserID:   userID,
		Name:     req.Name,
		Schedule: req.Schedule,
		Config:   req.Config,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "digest config not found")
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleDeleteDigestConfig(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	deleted, err := s.queries.DeleteDigestConfig(r.Context(), store.DeleteDigestConfigParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil || deleted == 0 {
		writeError(w, http.StatusNotFound, "digest config not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "deleted"})
}

// digestListStatusParam parses the status filter shared by the per-config and
// all-digests list endpoints.
//
// The API speaks 'unread'/'read'/'all'; the DB stores an unread result as 'new'
// (generator.go writes 'new', and the unread-count queries count status='new').
// Translating here keeps that storage detail behind the API instead of leaking
// it into the browser, which would otherwise have to know unread means new.
//
// An unrecognised value is treated as "no filter" rather than an error: a stale
// client sending ?status=digested should get results, not a 400.
func digestListStatusParam(r *http.Request) sql.NullString {
	switch r.URL.Query().Get("status") {
	case "unread":
		return sql.NullString{String: "new", Valid: true}
	case "read":
		return sql.NullString{String: "read", Valid: true}
	}
	return sql.NullString{}
}

// digestListSortIsOldest reports whether the client asked for oldest-first.
// The sort direction selects a whole STATEMENT rather than a bound parameter:
// SQLite cannot parameterise a sort direction, and sqlc drops parameters that
// appear only inside ORDER BY expressions (it emits a literal @name and binds
// nothing, which fails at runtime).
func digestListSortIsOldest(r *http.Request) bool {
	return r.URL.Query().Get("sort") == "oldest"
}

// oldestRowsToPreview maps the oldest-first row type onto the newest-first one.
// The two queries project identical columns; only the ORDER BY differs, so the
// handler can share one response-building path. A type assertion/conversion is
// not possible across distinct named struct types, hence the field-wise copy.
func oldestRowsToPreview(rows []store.ListDigestResultsByConfigOldestFirstPaginatedRow) []store.ListDigestResultsByConfigPaginatedRow {
	out := make([]store.ListDigestResultsByConfigPaginatedRow, len(rows))
	for i, r := range rows {
		out[i] = store.ListDigestResultsByConfigPaginatedRow{
			ID:        r.ID,
			UserID:    r.UserID,
			ConfigID:  r.ConfigID,
			Status:    r.Status,
			CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
			Title:     r.Title,
			ItemCount: r.ItemCount,
		}
	}
	return out
}

func (s *Server) handleListDigestResults(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	configID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

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
	status := digestListStatusParam(r)

	// Fetch one row beyond the page. has_more is derived from that extra row,
	// so it must be requested — asking for exactly `limit` rows makes
	// len(results) > limit unsatisfiable and pinned has_more to false, which
	// stopped every config at page one. The all-results endpoint next door
	// uses the same limit+1 shape.
	//
	// The sort direction picks the statement: see digestListSortIsOldest.
	var results []store.ListDigestResultsByConfigPaginatedRow
	if digestListSortIsOldest(r) {
		rows, err := s.queries.ListDigestResultsByConfigOldestFirstPaginated(r.Context(), store.ListDigestResultsByConfigOldestFirstPaginatedParams{
			ConfigID: configID,
			UserID:   userID,
			Status:   status,
			Limit:    int64(limit + 1),
			Offset:   int64(offset),
		})
		if err != nil {
			writeServerError(w, "failed to list digest results", err)
			return
		}
		results = oldestRowsToPreview(rows)
	} else {
		rows, err := s.queries.ListDigestResultsByConfigPaginated(r.Context(), store.ListDigestResultsByConfigPaginatedParams{
			ConfigID: configID,
			UserID:   userID,
			Status:   status,
			Limit:    int64(limit + 1),
			Offset:   int64(offset),
		})
		if err != nil {
			writeServerError(w, "failed to list digest results", err)
			return
		}
		results = rows
	}

	hasMore := len(results) > limit
	if hasMore {
		results = results[:limit]
	}
	if results == nil {
		results = []store.ListDigestResultsByConfigPaginatedRow{}
	}

	type resultPreview struct {
		ID        int64  `json:"id"`
		ConfigID  int64  `json:"config_id"`
		Status    string `json:"status"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
		Title     string `json:"title"`
		ItemCount int64  `json:"item_count"`
	}
	items := make([]resultPreview, len(results))
	for i, r := range results {
		items[i] = resultPreview{
			ID:        r.ID,
			ConfigID:  r.ConfigID,
			Status:    r.Status,
			CreatedAt: r.CreatedAt.Format(time.RFC3339),
			UpdatedAt: r.UpdatedAt.Format(time.RFC3339),
			Title:     r.Title,
			ItemCount: r.ItemCount,
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"results":  items,
		"has_more": hasMore,
	})
}

func (s *Server) handleGetDigestResult(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	result, err := s.queries.GetDigestResult(r.Context(), store.GetDigestResultParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "digest result not found")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type updateDigestResultStatusRequest struct {
	Status string `json:"status"`
}

func (s *Server) handleUpdateDigestResultStatus(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req updateDigestResultStatusRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Status != "new" && req.Status != "unread" && req.Status != "read" {
		writeError(w, http.StatusBadRequest, "status must be new/unread or read")
		return
	}
	if req.Status == "unread" {
		req.Status = "new"
	}
	// Status update and its cascade commit together. Returning the cascade error
	// was not enough on its own: the status update had already committed, so a
	// failed cascade still left the result read with its items digested.
	var result store.DigestResult
	err = s.inDigestTx(r.Context(), "update-digest-result-status", func(ctx context.Context, q *store.Queries) error {
		r, err := q.UpdateDigestResultStatus(ctx, store.UpdateDigestResultStatusParams{
			ID:     id,
			UserID: userID,
			Status: req.Status,
		})
		if err != nil {
			return err
		}
		result = r
		// Spec: when a digest result transitions to read, all of its linked items
		// move from digested to read.
		if req.Status == "read" {
			if err := q.MarkDigestResultItemsRead(ctx, result.ID); err != nil {
				return fmt.Errorf("flipping digest items: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "digest result not found")
			return
		}
		writeServerError(w, "failed to update digest result", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleDeleteDigestResult(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	deleted, err := s.queries.DeleteDigestResult(r.Context(), store.DeleteDigestResultParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil || deleted == 0 {
		writeError(w, http.StatusNotFound, "digest result not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "deleted"})
}

// handleMarkDigestResultsRead marks all new/unread digest results as read,
// optionally scoped to a specific config_id.
