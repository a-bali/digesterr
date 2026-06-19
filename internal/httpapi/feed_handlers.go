package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"digesterr/internal/store"
)

type createFeedRequest struct {
	CategoryID     int64  `json:"category_id"`
	CategoryName   string `json:"category_name"`
	Type           string `json:"type"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	Config         string `json:"config"`
	ExtractContent int64  `json:"extract_content"`
}

func (s *Server) resolveCategory(ctx context.Context, userID int64, req createFeedRequest) (int64, error) {
	if req.CategoryID > 0 {
		return req.CategoryID, nil
	}
	if req.CategoryName != "" {
		cat, err := s.queries.CreateCategory(ctx, store.CreateCategoryParams{
			UserID: userID,
			Name:   req.CategoryName,
		})
		if err == nil {
			return cat.ID, nil
		}
	}
	cat, err := s.queries.GetCategoryByName(ctx, store.GetCategoryByNameParams{
		UserID: userID,
		Name:   "FEEDS",
	})
	if err == nil {
		return cat.ID, nil
	}
	cat, err = s.queries.CreateCategory(ctx, store.CreateCategoryParams{
		UserID: userID,
		Name:   "FEEDS",
	})
	if err != nil {
		return 0, err
	}
	return cat.ID, nil
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
	categoryID, err := s.resolveCategory(r.Context(), userID, req)
	if err != nil {
		writeServerError(w, "failed to resolve category", err)
		return
	}
	feed, err := s.queries.CreateFeed(r.Context(), store.CreateFeedParams{
		UserID:         userID,
		CategoryID:     categoryID,
		Type:           req.Type,
		Name:           req.Name,
		Url:            req.URL,
		Config:         req.Config,
		Status:         "active",
		ExtractContent: req.ExtractContent,
	})
	if err != nil {
		writeServerError(w, "failed to create feed", err)
		return
	}
	writeJSON(w, http.StatusCreated, feed)
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
	Name           *string `json:"name"`
	URL            *string `json:"url"`
	Config         *string `json:"config"`
	CategoryID     *int64  `json:"category_id"`
	Status         *string `json:"status"`
	ExtractContent *int64  `json:"extract_content"`
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
	extractContent := existing.ExtractContent
	if req.ExtractContent != nil {
		extractContent = *req.ExtractContent
	}
	feed, err := s.queries.UpdateFeed(r.Context(), store.UpdateFeedParams{
		ID:             id,
		UserID:         userID,
		Name:           name,
		Url:            url,
		Config:         config,
		CategoryID:     categoryID,
		Status:         status,
		ExtractContent: extractContent,
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

	feed, err := s.queries.GetFeedByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "feed not found")
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

	if feed.CategoryID != 0 {
			cnt, cerr := s.queries.CountFeedsInCategory(r.Context(), feed.CategoryID)
			if cerr == nil && cnt == 0 {
				cat, cerr := s.queries.GetCategory(r.Context(), store.GetCategoryParams{
					ID:     feed.CategoryID,
					UserID: userID,
				})
				if cerr == nil && cat.Name != "FEEDS" {
					s.queries.DeleteCategory(r.Context(), store.DeleteCategoryParams{
						ID:     feed.CategoryID,
						UserID: userID,
					})
				}
			}
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
	limitStr := r.URL.Query().Get("limit")
	var items []store.Item
	if limitStr != "" {
		limit, err := strconv.Atoi(limitStr)
		if err != nil || limit <= 0 {
			limit = 50
		}
		if limit > 100 {
			limit = 100
		}
		offset := 0
		if o := r.URL.Query().Get("offset"); o != "" {
			if v, err := strconv.Atoi(o); err == nil && v >= 0 {
				offset = v
			}
		}
		queryLimit := limit + 1
		if status != "" {
			items, err = s.queries.ListItemsByFeedAndStatusPaginated(r.Context(), store.ListItemsByFeedAndStatusPaginatedParams{
				FeedID: id,
				Status: status,
				Limit:  int64(queryLimit),
				Offset: int64(offset),
			})
		} else {
			items, err = s.queries.ListItemsByFeedPaginated(r.Context(), store.ListItemsByFeedPaginatedParams{
				FeedID: id,
				Limit:  int64(queryLimit),
				Offset: int64(offset),
			})
		}
		if err != nil {
			writeServerError(w, "failed to list items", err)
			return
		}
		hasMore := len(items) > limit
		if hasMore {
			items = items[:limit]
		}
		if items == nil {
			items = []store.Item{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"items":    items,
			"has_more": hasMore,
		})
		return
	}
	if status != "" {
		items, err = s.queries.ListItemsByFeedAndStatus(r.Context(), store.ListItemsByFeedAndStatusParams{
			FeedID: id,
			Status: status,
		})
	} else {
		items, err = s.queries.ListItemsByFeed(r.Context(), id)
	}
	if err != nil {
		writeServerError(w, "failed to list items", err)
		return
	}
	if items == nil {
		items = []store.Item{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items": items,
	})
}

func (s *Server) handleFetchFeed(w http.ResponseWriter, r *http.Request) {
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
	f, err := s.queries.GetFeed(r.Context(), store.GetFeedParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "feed not found")
		return
	}

	if err := s.poller.PollFeed(r.Context(), f); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch feed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "feed fetched"})
}

func (s *Server) handleUnreadCounts(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	counts, err := s.queries.GetUnreadCounts(r.Context(), userID)
	if err != nil {
		writeServerError(w, "failed to get unread counts", err)
		return
	}
	if counts == nil {
		counts = []store.GetUnreadCountsRow{}
	}
	result := make(map[int64]int64, len(counts))
	for _, c := range counts {
		result[c.FeedID] = c.UnreadCount
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleMarkFeedRead(w http.ResponseWriter, r *http.Request) {
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
	if err := s.queries.MarkFeedRead(r.Context(), id); err != nil {
		writeServerError(w, "failed to mark feed read", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "feed marked read"})
}