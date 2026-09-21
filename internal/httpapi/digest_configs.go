package httpapi

import (
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
		writeError(w, http.StatusInternalServerError, "failed to list digest configs")
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
		writeError(w, http.StatusInternalServerError, "failed to create digest config")
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

	results, err := s.queries.ListDigestResultsByConfigPaginated(r.Context(), store.ListDigestResultsByConfigPaginatedParams{
		ConfigID: configID,
		UserID:   userID,
		Limit:    int64(limit),
		Offset:   int64(offset),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list digest results")
		return
	}
	if results == nil {
		results = []store.ListDigestResultsByConfigPaginatedRow{}
	}

	hasMore := len(results) > limit
	if hasMore {
		results = results[:limit]
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
	result, err := s.queries.UpdateDigestResultStatus(r.Context(), store.UpdateDigestResultStatusParams{
		ID:     id,
		UserID: userID,
		Status: req.Status,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "digest result not found")
		return
	}
	// Spec: when a digest result transitions to read, all of its linked items
	// move from digested to read.
	if req.Status == "read" {
		if err := s.queries.MarkDigestResultItemsRead(r.Context(), result.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update linked items")
			return
		}
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
