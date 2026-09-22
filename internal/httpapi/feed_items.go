package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

func (s *Server) handleListFeedItems(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	feedObj, err := s.queries.GetFeed(r.Context(), store.GetFeedParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "feed not found")
		return
	}

	status := r.URL.Query().Get("status")

	// `status=saved` is a legacy request shape for the starred set, which is now
	// a flag rather than a status. Translate it here so every caller keeps
	// working while the query stays a plain status filter plus saved_only.
	savedOnly := int64(0)
	if status == "saved" {
		status = ""
		savedOnly = 1
	}

	// min_score filters out items below the given score. When the caller does
	// not pass one, the feed's own min_score applies: the threshold a user set
	// on the feed governs what its list shows, not only what is inserted.
	//
	// Two exemptions:
	//   - an explicit `min_score` (including `min_score=0`) always wins, so the
	//     FilterBar's own control still overrides;
	//   - the starred set is never threshold-filtered — the user chose to keep
	//     those items, and hiding one behind a threshold set later would make a
	//     saved item unreachable.
	minScore := int64(0)
	if ms := r.URL.Query().Get("min_score"); ms != "" {
		v, err := strconv.ParseInt(ms, 10, 64)
		if err != nil || v < 0 {
			writeError(w, http.StatusBadRequest, "invalid min_score; must be a non-negative integer")
			return
		}
		minScore = v
	} else if savedOnly == 0 {
		minScore = feedObj.MinScore
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

	// All 4 sort queries return identical columns; map to a common type.
	type itemPreview struct {
		ID             int64       `json:"id"`
		Title          string      `json:"title"`
		Status         string      `json:"status"`
		IsSaved        int64       `json:"is_saved"`
		PublishedAt    interface{} `json:"published_at"`
		Content        string      `json:"content"`
		ExtractContent string      `json:"extract_content"`
		ExtractStatus  string      `json:"extract_status"`
		// Excerpt is the plain text the list row displays: the stored bodies are
		// HTML, and flattening them is what keeps markup out of the row. It is
		// computed here, and NOT from a 500-char cut, because a character cap
		// over raw HTML can land inside an opening run of markup and leave the
		// row with nothing to show (see feed.ExcerptForPreview).
		Excerpt       string `json:"excerpt"`
		Points        int64  `json:"points"`
		CommentsCount int64  `json:"comments_count"`
		CommentUrl    string `json:"comment_url"`
	}
	var items []itemPreview
	var rawItems interface{}
	switch sort {
	case "date_asc":
		rawItems, err = s.queries.ListItemsByFeedSortedDateAsc(r.Context(), store.ListItemsByFeedSortedDateAscParams{
			FeedID: id, Status: statusArg, SavedOnly: savedOnly, MinScore: minScore, Limit: limit, Offset: offset,
		})
	case "score_desc":
		rawItems, err = s.queries.ListItemsByFeedSortedScoreDesc(r.Context(), store.ListItemsByFeedSortedScoreDescParams{
			FeedID: id, Status: statusArg, SavedOnly: savedOnly, MinScore: minScore, Limit: limit, Offset: offset,
		})
	case "score_asc":
		rawItems, err = s.queries.ListItemsByFeedSortedScoreAsc(r.Context(), store.ListItemsByFeedSortedScoreAscParams{
			FeedID: id, Status: statusArg, SavedOnly: savedOnly, MinScore: minScore, Limit: limit, Offset: offset,
		})
	default:
		rawItems, err = s.queries.ListItemsByFeedSortedDateDesc(r.Context(), store.ListItemsByFeedSortedDateDescParams{
			FeedID: id, Status: statusArg, SavedOnly: savedOnly, MinScore: minScore, Limit: limit, Offset: offset,
		})
	}
	if err != nil {
		writeServerError(w, "failed to list items", err)
		return
	}
	// Convert the sort-specific row type to the common preview type.
	switch rows := rawItems.(type) {
	case []store.ListItemsByFeedSortedDateDescRow:
		items = make([]itemPreview, len(rows))
		for i, r := range rows {
			items[i] = itemPreview{r.ID, r.Title, r.Status, r.IsSaved, r.PublishedAt, r.Content, r.ExtractContent, r.ExtractStatus, feed.ExcerptForPreview(r.Content, r.ExtractContent, r.ExtractStatus), r.Points, r.CommentsCount, r.CommentUrl}
		}
	case []store.ListItemsByFeedSortedDateAscRow:
		items = make([]itemPreview, len(rows))
		for i, r := range rows {
			items[i] = itemPreview{r.ID, r.Title, r.Status, r.IsSaved, r.PublishedAt, r.Content, r.ExtractContent, r.ExtractStatus, feed.ExcerptForPreview(r.Content, r.ExtractContent, r.ExtractStatus), r.Points, r.CommentsCount, r.CommentUrl}
		}
	case []store.ListItemsByFeedSortedScoreDescRow:
		items = make([]itemPreview, len(rows))
		for i, r := range rows {
			items[i] = itemPreview{r.ID, r.Title, r.Status, r.IsSaved, r.PublishedAt, r.Content, r.ExtractContent, r.ExtractStatus, feed.ExcerptForPreview(r.Content, r.ExtractContent, r.ExtractStatus), r.Points, r.CommentsCount, r.CommentUrl}
		}
	case []store.ListItemsByFeedSortedScoreAscRow:
		items = make([]itemPreview, len(rows))
		for i, r := range rows {
			items[i] = itemPreview{r.ID, r.Title, r.Status, r.IsSaved, r.PublishedAt, r.Content, r.ExtractContent, r.ExtractStatus, feed.ExcerptForPreview(r.Content, r.ExtractContent, r.ExtractStatus), r.Points, r.CommentsCount, r.CommentUrl}
		}
	}
	if err != nil {
		writeServerError(w, "failed to list items", err)
		return
	}
	hasMore := false
	var total int64
	if paginated {
		hasMore = len(items) > int(limit-1)
		if hasMore {
			items = items[:int(limit-1)]
		}
		// Count total matching items (without LIMIT/OFFSET) so the client
		// can display the full item count, not just the loaded subset.
		total, err = s.queries.CountItemsByFeed(r.Context(), store.CountItemsByFeedParams{
			FeedID: id, Status: statusArg, SavedOnly: savedOnly, MinScore: minScore,
		})
		if err != nil {
			writeServerError(w, "failed to count items", err)
			return
		}
	}
	if items == nil {
		items = []itemPreview{}
	}

	if paginated {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"items":    items,
			"has_more": hasMore,
			"total":    total,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items": items,
	})
}

func (s *Server) handleFetchFeed(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
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
		writeServerError(w, "failed to fetch feed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "feed fetched"})
}

func (s *Server) handleUnreadCounts(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
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

// handleSavedCount reports how many items the user has saved, across all
// feeds. The sidebar badge needs a total that does not depend on which feeds
// the client has loaded, and the saved view itself is assembled client-side
// from per-feed requests, so its array length is only the loaded page.
func (s *Server) handleSavedCount(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	count, err := s.queries.CountStarredItems(r.Context(), userID)
	if err != nil {
		writeServerError(w, "failed to count saved items", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"count": count})
}

// handleListSavedItems returns the user's saved items with feed metadata in
// one query. Replaces the client's N sequential per-feed requests, and works
// through archive feeds (orphaned items of deleted feeds keep their
// "Saved -- <name>" provenance).
func (s *Server) handleListSavedItems(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	rows, err := s.queries.ListSavedItemsByUser(r.Context(), userID)
	if err != nil {
		writeServerError(w, "failed to list saved items", err)
		return
	}
	items := make([]savedItemRow, len(rows))
	for i, row := range rows {
		items[i] = savedItemRow{
			ListSavedItemsByUserRow: row,
			Excerpt:                 feed.ExcerptForPreview(row.Content, row.ExtractContent, row.ExtractStatus),
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
}

func (s *Server) handleMarkFeedRead(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
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

// handleMarkRead marks all unread items as read for the authenticated user,
// scoped to an optional category. Used by the multi-view mark-all-read action.
func (s *Server) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	var body struct {
		Category *string `json:"category"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	ctx := r.Context()
	if body.Category != nil && *body.Category != "" {
		if err := s.queries.MarkCategoryItemsRead(ctx, store.MarkCategoryItemsReadParams{
			UserID:   userID,
			Category: *body.Category,
		}); err != nil {
			writeServerError(w, "failed to mark items read", err)
			return
		}
	} else {
		if err := s.queries.MarkAllUserItemsRead(ctx, userID); err != nil {
			writeServerError(w, "failed to mark items read", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "items marked read"})
}

// multiItemRow is an Item with feed metadata for multi-feed/category views.
// Only includes columns needed for the middle pane list display.
type multiItemRow struct {
	ID             int64       `json:"id"`
	Title          string      `json:"title"`
	Content        string      `json:"content"`
	CommentUrl     string      `json:"comment_url"`
	Points         int64       `json:"points"`
	CommentsCount  int64       `json:"comments_count"`
	PublishedAt    interface{} `json:"published_at"`
	Status         string      `json:"status"`
	IsSaved        int64       `json:"is_saved"`
	ExtractContent string      `json:"extract_content"`
	ExtractStatus  string      `json:"extract_status"`
	// Excerpt is the plain text the list row displays, flattened server-side
	// from the two HTML bodies above (see feed.ExcerptForPreview).
	Excerpt  string `json:"excerpt"`
	FeedName string `json:"feed_name"`
	FeedType string `json:"feed_type"`
}

// savedItemRow is a saved item with feed metadata and its display excerpt. The
// excerpt is added here rather than in the query because it is flattened from
// the stored HTML bodies, which the list response already carries.
type savedItemRow struct {
	store.ListSavedItemsByUserRow
	Excerpt string `json:"excerpt"`
}
