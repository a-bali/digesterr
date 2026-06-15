package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"digesterr/internal/store"
)

type createFeedRequest struct {
	CategoryID int64  `json:"category_id"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	Config     string `json:"config"`
}

func (s *Server) handleListFeeds(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	feeds, err := s.queries.ListFeeds(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list feeds")
		return
	}
	if feeds == nil {
		feeds = []store.Feed{}
	}
	writeJSON(w, http.StatusOK, feeds)
}

func (s *Server) handleCreateFeed(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req createFeedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.Type == "" {
		writeError(w, http.StatusBadRequest, "name and type are required")
		return
	}
	if req.Config == "" {
		req.Config = "{}"
	}
	feed, err := s.queries.CreateFeed(r.Context(), store.CreateFeedParams{
		UserID:     userID,
		CategoryID: req.CategoryID,
		Type:       req.Type,
		Name:       req.Name,
		Url:        req.URL,
		Config:     req.Config,
		Status:     "active",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create feed")
		return
	}
	writeJSON(w, http.StatusCreated, feed)
}

func (s *Server) handleGetFeed(w http.ResponseWriter, r *http.Request) {
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
	feed, err := s.queries.GetFeed(r.Context(), store.GetFeedParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "feed not found")
		return
	}
	writeJSON(w, http.StatusOK, feed)
}

type updateFeedRequest struct {
	Name       *string `json:"name"`
	URL        *string `json:"url"`
	Config     *string `json:"config"`
	CategoryID *int64  `json:"category_id"`
	Status     *string `json:"status"`
}

func (s *Server) handleUpdateFeed(w http.ResponseWriter, r *http.Request) {
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
	existing, err := s.queries.GetFeed(r.Context(), store.GetFeedParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "feed not found")
		return
	}
	var req updateFeedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := existing.Name
	if req.Name != nil {
		name = *req.Name
	}
	url := existing.Url
	if req.URL != nil {
		url = *req.URL
	}
	config := existing.Config
	if req.Config != nil {
		config = *req.Config
	}
	categoryID := existing.CategoryID
	if req.CategoryID != nil {
		categoryID = *req.CategoryID
	}
	status := existing.Status
	if req.Status != nil {
		status = *req.Status
	}
	feed, err := s.queries.UpdateFeed(r.Context(), store.UpdateFeedParams{
		ID:         id,
		UserID:     userID,
		Name:       name,
		Url:        url,
		Config:     config,
		CategoryID: categoryID,
		Status:     status,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update feed")
		return
	}
	writeJSON(w, http.StatusOK, feed)
}

func (s *Server) handleDeleteFeed(w http.ResponseWriter, r *http.Request) {
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
	deleted, err := s.queries.DeleteFeed(r.Context(), store.DeleteFeedParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil || deleted == 0 {
		writeError(w, http.StatusNotFound, "feed not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "deleted"})
}

func (s *Server) handleListFeedItems(w http.ResponseWriter, r *http.Request) {
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
	_, err = s.queries.GetFeed(r.Context(), store.GetFeedParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "feed not found")
		return
	}
	status := r.URL.Query().Get("status")
	var items []store.Item
	if status != "" {
		items, err = s.queries.ListItemsByFeedAndStatus(r.Context(), store.ListItemsByFeedAndStatusParams{
			FeedID: id,
			Status: status,
		})
	} else {
		items, err = s.queries.ListItemsByFeed(r.Context(), id)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list items")
		return
	}
	if items == nil {
		items = []store.Item{}
	}
	writeJSON(w, http.StatusOK, items)
}