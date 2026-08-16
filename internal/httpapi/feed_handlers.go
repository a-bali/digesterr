package httpapi

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"digesterr/internal/store"
)

type createFeedRequest struct {
	Category       string `json:"category"`
	Type           string `json:"type"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	Config         string `json:"config"`
	ExtractContent int64  `json:"extract_content"`
	MinScore       int64  `json:"min_score"`
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
	if req.MinScore < 0 {
		writeError(w, http.StatusBadRequest, "min_score must be zero or a positive integer")
		return
	}
	if req.Config == "" {
		req.Config = "{}"
	}
	feed, err := s.queries.CreateFeed(r.Context(), store.CreateFeedParams{
		UserID:         userID,
		Category:       req.Category,
		Type:           req.Type,
		Name:           req.Name,
		Url:            req.URL,
		Config:         req.Config,
		Status:         "active",
		ExtractContent: req.ExtractContent,
		MinScore:       req.MinScore,
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
		slog.Error("failed to list feeds", "user_id", userID, "error", err)
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
	Category       *string `json:"category"`
	Status         *string `json:"status"`
	ExtractContent *int64  `json:"extract_content"`
	MinScore       *int64  `json:"min_score"`
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
	category := existing.Category
	if req.Category != nil {
		category = *req.Category
	}
	status := existing.Status
	if req.Status != nil {
		status = *req.Status
	}
	extractContent := existing.ExtractContent
	if req.ExtractContent != nil {
		extractContent = *req.ExtractContent
	}
	minScore := existing.MinScore
	if req.MinScore != nil {
		if *req.MinScore < 0 {
			writeError(w, http.StatusBadRequest, "min_score must be zero or a positive integer")
			return
		}
		minScore = *req.MinScore
	}
	feed, err := s.queries.UpdateFeed(r.Context(), store.UpdateFeedParams{
		ID:             id,
		UserID:         userID,
		Name:           name,
		Url:            url,
		Config:         config,
		Category:       category,
		Status:         status,
		ExtractContent: extractContent,
		MinScore:       minScore,
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

	// min_score filters out items below the given score. A zero or absent
	// value disables the filter (mirrors the feed-level min_score semantics).
	minScore := int64(0)
	if ms := r.URL.Query().Get("min_score"); ms != "" {
		v, err := strconv.ParseInt(ms, 10, 64)
		if err != nil || v < 0 {
			writeError(w, http.StatusBadRequest, "invalid min_score; must be a non-negative integer")
			return
		}
		minScore = v
	}

	// sort selects the ordering; date_desc is the default and matches the
	// historical ordering (published_at DESC, created_at DESC).
	sort := r.URL.Query().Get("sort")
	if sort == "" {
		sort = "date_desc"
	}
	switch sort {
	case "date_desc", "date_asc", "score_desc", "score_asc":
	default:
		writeError(w, http.StatusBadRequest, "invalid sort; must be date_desc, date_asc, score_desc, or score_asc")
		return
	}

	var statusArg sql.NullString
	if status != "" {
		statusArg = sql.NullString{String: status, Valid: true}
	}

	// A limit selects the paginated response shape (items + has_more);
	// without one the query returns every matching item (LIMIT -1 = none).
	paginated := false
	limit := int64(0)
	offset := int64(0)
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		paginated = true
		l, err := strconv.Atoi(limitStr)
		if err != nil || l <= 0 {
			l = 50
		}
		if l > 100 {
			l = 100
		}
		if o := r.URL.Query().Get("offset"); o != "" {
			if v, err := strconv.Atoi(o); err == nil && v >= 0 {
				offset = int64(v)
			}
		}
		// Fetch one extra row to detect whether more items exist.
		limit = int64(l + 1)
	} else {
		limit = -1
	}

	var items []store.Item
	switch sort {
	case "date_asc":
		items, err = s.queries.ListItemsByFeedSortedDateAsc(r.Context(), store.ListItemsByFeedSortedDateAscParams{
			FeedID: id, Status: statusArg, MinScore: minScore, Limit: limit, Offset: offset,
		})
	case "score_desc":
		items, err = s.queries.ListItemsByFeedSortedScoreDesc(r.Context(), store.ListItemsByFeedSortedScoreDescParams{
			FeedID: id, Status: statusArg, MinScore: minScore, Limit: limit, Offset: offset,
		})
	case "score_asc":
		items, err = s.queries.ListItemsByFeedSortedScoreAsc(r.Context(), store.ListItemsByFeedSortedScoreAscParams{
			FeedID: id, Status: statusArg, MinScore: minScore, Limit: limit, Offset: offset,
		})
	default:
		items, err = s.queries.ListItemsByFeedSortedDateDesc(r.Context(), store.ListItemsByFeedSortedDateDescParams{
			FeedID: id, Status: statusArg, MinScore: minScore, Limit: limit, Offset: offset,
		})
	}
	if err != nil {
		writeServerError(w, "failed to list items", err)
		return
	}
	hasMore := false
	if paginated {
		hasMore = len(items) > int(limit-1)
		if hasMore {
			items = items[:int(limit-1)]
		}
	}
	if items == nil {
		items = []store.Item{}
	}

	if paginated {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"items":    items,
			"has_more": hasMore,
		})
		return
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
