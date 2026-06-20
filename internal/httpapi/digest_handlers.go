package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"digesterr/internal/store"
)

type createDigestConfigRequest struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Config   string `json:"config"`
}

func (s *Server) handleListDigestConfigs(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
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
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req createDigestConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Schedule == "" {
		writeError(w, http.StatusBadRequest, "schedule is required")
		return
	}
	if req.Config == "" {
		req.Config = "{}"
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
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
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
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req createDigestConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
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
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
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
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	configID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	results, err := s.queries.ListDigestResultsByConfig(r.Context(), store.ListDigestResultsByConfigParams{
		ConfigID: configID,
		UserID:   userID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list digest results")
		return
	}
	if results == nil {
		results = []store.DigestResult{}
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) handleGetDigestResult(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
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
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req updateDigestResultStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
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
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleDeleteDigestResult(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
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

func (s *Server) handleGenerateDigest(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
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

	result, err := s.generator.Generate(r.Context(), cfg)
	if err != nil {
		slog.Error("digest generation failed", "config_id", configID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to generate digest: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) handleListDigestResultItemsWithData(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
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
		writeError(w, http.StatusInternalServerError, "failed to list digest result items")
		return
	}
	if items == nil {
		items = []store.ListDigestResultItemsWithDataRow{}
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleDigestUnreadCounts(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
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