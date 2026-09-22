package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

func (s *Server) handleListMultiFeedItems(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)

	// Parse feed_ids (comma-separated) or category from query params.
	feedIDsRaw := r.URL.Query().Get("feed_ids")
	category := r.URL.Query().Get("category")

	if feedIDsRaw == "" && category == "" {
		writeError(w, http.StatusBadRequest, "feed_ids or category is required")
		return
	}
	if feedIDsRaw != "" && category != "" {
		writeError(w, http.StatusBadRequest, "provide either feed_ids or category, not both")
		return
	}

	// Parse common query params (same as handleListFeedItems).
	status := r.URL.Query().Get("status")
	// min_score: an explicit value is a single threshold across every feed in
	// the view (the FilterBar's own control). When absent, each feed's OWN
	// min_score applies per row — one number cannot represent a category whose
	// feeds have different thresholds, and the default must match what each
	// feed's single-feed view shows.
	minScoreExplicit := false
	minScore := int64(0)
	if ms := r.URL.Query().Get("min_score"); ms != "" {
		v, err := strconv.ParseInt(ms, 10, 64)
		if err != nil || v < 0 {
			writeError(w, http.StatusBadRequest, "invalid min_score")
			return
		}
		minScore = v
		minScoreExplicit = true
	}
	sort := r.URL.Query().Get("sort")
	if sort == "" {
		sort = "date_desc"
	}
	switch sort {
	case "date_desc", "date_asc", "score_desc", "score_asc":
	default:
		writeError(w, http.StatusBadRequest, "invalid sort")
		return
	}

	limit := int64(50)
	if l := r.URL.Query().Get("limit"); l != "" {
		v, err := strconv.Atoi(l)
		if err == nil && v > 0 && v <= 100 {
			limit = int64(v)
		}
	}
	offset := int64(0)
	if o := r.URL.Query().Get("offset"); o != "" {
		v, err := strconv.Atoi(o)
		if err == nil && v >= 0 {
			offset = int64(v)
		}
	}

	// Build dynamic query.
	var whereClause string
	var args []interface{}

	if feedIDsRaw != "" {
		parts := strings.Split(feedIDsRaw, ",")
		ids := make([]int64, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			v, err := strconv.ParseInt(p, 10, 64)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid feed_id: "+p)
				return
			}
			ids = append(ids, v)
		}
		if len(ids) == 0 {
			writeError(w, http.StatusBadRequest, "no valid feed_ids")
			return
		}
		if len(ids) > 50 {
			writeError(w, http.StatusBadRequest, "too many feed_ids (max 50)")
			return
		}
		placeholders := make([]string, len(ids))
		for i, id := range ids {
			placeholders[i] = "?"
			args = append(args, id)
		}
		whereClause = fmt.Sprintf("f.id IN (%s) AND f.user_id = ?", strings.Join(placeholders, ","))
		args = append(args, userID)
	} else {
		whereClause = "f.category = ? AND f.user_id = ?"
		args = append(args, category, userID)
	}

	// Add status, min_score filters. `status=saved` is the legacy shape for the
	// starred set, which is a flag now (matching handleListFeedItems).
	savedOnly := false
	if status == "saved" {
		status = ""
		savedOnly = true
		whereClause += " AND i.is_saved = 1"
	} else if status != "" {
		whereClause += " AND i.status = ?"
		args = append(args, status)
	} else {
		whereClause += " AND (i.status IS NULL OR i.status IS NOT NULL)"
	}
	// An explicit min_score is one threshold across the whole view.
	whereClause += " AND (i.points >= ? OR ? = 0)"
	args = append(args, minScore, minScore)
	// With no explicit threshold, each feed's OWN min_score applies per row:
	// one number cannot represent a category whose feeds carry different
	// thresholds, and the default must match what each feed's single-feed view
	// shows. Skipped for the starred set, which is never threshold-filtered.
	if !minScoreExplicit && !savedOnly {
		whereClause += " AND (f.min_score = 0 OR i.points >= f.min_score)"
	}

	// Sort clause. These orderings are mirrored by the Mark*ReadRelative*
	// queries in sql/queries/items.sql; keep the two in step.
	var orderClause string
	switch sort {
	case sortDateAsc:
		orderClause = "i.published_at ASC, i.created_at ASC, i.id ASC"
	case sortScoreDesc:
		orderClause = "i.points DESC, i.published_at DESC, i.created_at DESC, i.id DESC"
	case sortScoreAsc:
		orderClause = "i.points ASC, i.published_at DESC, i.created_at DESC, i.id DESC"
	default:
		orderClause = "i.published_at DESC, i.created_at DESC, i.id DESC"
	}

	// Fetch limit+1 to detect has_more. Only select columns needed for the
	// middle pane — excludes hash, url, author, and internal metadata. Bodies
	// are capped at 8192 characters, not 500: the row's excerpt is flattened
	// from this prefix below, and a cut is a CHARACTER count over RAW HTML, so
	// a body opening with inline SVG or a long srcset list can spend several KB
	// of markup before its first word of prose. At 500 chars those rows showed
	// an empty excerpt. The full body still comes from GET /api/items/{id}.
	//
	// The two substr() columns MUST be aliased: this handler maps columns by
	// NAME, so an unaliased expression is keyed as "substr(i.content, 1, 8192)"
	// and the row silently loses its body entirely.
	queryStr := fmt.Sprintf(
		"SELECT i.id, i.title, substr(i.content, 1, 8192) AS content, i.comment_url, i.points, i.comments_count, "+
			"i.published_at, i.status, i.is_saved, substr(i.extract_content, 1, 8192) AS extract_content, i.extract_status, "+
			"f.name AS feed_name, f.type AS feed_type FROM items i "+
			"JOIN feeds f ON f.id = i.feed_id WHERE %s ORDER BY %s LIMIT ? OFFSET ?",
		whereClause, orderClause,
	)
	args = append(args, limit+1, offset)

	ctx := r.Context()
	rows, err := s.db.QueryContext(ctx, queryStr, args...)
	if err != nil {
		writeServerError(w, "failed to list items", err)
		return
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		writeServerError(w, "failed to list items", err)
		return
	}

	var items []multiItemRow
	for rows.Next() {
		// Scan into interface{} values, then map by column name.
		values := make([]interface{}, len(cols))
		valuePtrs := make([]interface{}, len(cols))
		for i := range values {
			valuePtrs[i] = &values[i]
		}
		if err := rows.Scan(valuePtrs...); err != nil {
			writeServerError(w, "failed to list items", err)
			return
		}
		colMap := make(map[string]interface{}, len(cols))
		for i, c := range cols {
			colMap[c] = values[i]
		}
		item := multiItemRow{
			ID:             colInt64(colMap["id"]),
			Title:          colStr(colMap["title"]),
			Content:        colStr(colMap["content"]),
			CommentUrl:     colStr(colMap["comment_url"]),
			Points:         colInt64(colMap["points"]),
			CommentsCount:  colInt64(colMap["comments_count"]),
			PublishedAt:    colMap["published_at"],
			Status:         colStr(colMap["status"]),
			IsSaved:        colInt64(colMap["is_saved"]),
			ExtractContent: colStr(colMap["extract_content"]),
			ExtractStatus:  colStr(colMap["extract_status"]),
			FeedName:       colStr(colMap["feed_name"]),
			FeedType:       colStr(colMap["feed_type"]),
		}
		// Same server-side flattening as the single-feed list: the row shows
		// plain text, and the stored bodies are HTML.
		item.Excerpt = feed.ExcerptForPreview(item.Content, item.ExtractContent, item.ExtractStatus)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeServerError(w, "failed to list items", err)
		return
	}

	hasMore := len(items) > int(limit)
	if hasMore {
		items = items[:int(limit)]
	}

	var total int64
	{
		// Count total.
		countQuery := fmt.Sprintf(
			"SELECT CAST(COUNT(*) AS INTEGER) FROM items i JOIN feeds f ON f.id = i.feed_id WHERE %s",
			whereClause,
		)
		// args without the last 2 (limit, offset).
		countArgs := make([]interface{}, len(args)-2)
		copy(countArgs, args[:len(args)-2])
		err = s.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total)
		if err != nil {
			writeServerError(w, "failed to count items", err)
			return
		}
	}

	if items == nil {
		items = []multiItemRow{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items":    items,
		"has_more": hasMore,
		"total":    total,
	})
}

// handleMarkItemReadAbove marks all items above (or at) the given item as read.
func (s *Server) handleMarkItemReadAbove(w http.ResponseWriter, r *http.Request) {
	s.handleMarkItemRelative(w, r, "above")
}

func (s *Server) handleMarkItemReadBelow(w http.ResponseWriter, r *http.Request) {
	s.handleMarkItemRelative(w, r, "below")
}

func (s *Server) handleMarkItemRelative(w http.ResponseWriter, r *http.Request, direction string) {
	userID := authedUserID(r)
	itemID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	// Parse scope and view ordering from the request body.
	var body struct {
		Category *string `json:"category"`
		FeedIDs  *string `json:"feed_ids"` // comma-separated, for "Feeds" section
		// Sort mirrors the middle pane's ordering and MinScore mirrors its
		// minimum-score filter, so "above" and "below" mean exactly what the
		// user is looking at — including items not loaded yet.
		Sort     string `json:"sort"`
		MinScore int64  `json:"min_score"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	sort := normalizeItemSort(body.Sort)

	// Look up the item to get boundary values.
	item, err := s.queries.GetItemWithFeedOwner(r.Context(), store.GetItemWithFeedOwnerParams{ID: itemID, UserID: userID})
	if err != nil {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}

	// The section the client is viewing decides the scope:
	//   label        -> every feed in that category
	//   "Feeds" list -> every feed the user owns
	//   neither      -> the item's own feed
	//
	// feed_ids is still accepted so the "Feeds" section is recognised, but it is
	// no longer parsed: the client always sends its complete feed list, so a
	// supplied list can never narrow the update below the user-wide scope. An
	// empty category is treated as user-wide too, matching the previous
	// behaviour.
	//
	// The boundary row and its sort keys are resolved inside SQL, not bound from
	// Go — see MarkFeedReadRelativeDateDesc in sql/queries/items.sql. Neither
	// direction marks the boundary item itself.
	above := direction == "above"
	switch {
	case body.Category != nil && *body.Category != "":
		err = s.markRelativeByCategory(r.Context(), userID, *body.Category, itemID, above, sort, body.MinScore)
	case body.Category != nil || body.FeedIDs != nil:
		err = s.markRelativeByUser(r.Context(), userID, itemID, above, sort, body.MinScore)
	default:
		err = s.markRelativeByFeed(r.Context(), item.FeedID, itemID, above, sort, body.MinScore)
	}
	if err != nil {
		writeServerError(w, "failed to mark items read", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "items marked read"})
}

// Sort keys accepted by the item list. "above"/"below" are meaningless
// without one: the range has to follow the ordering the pane is showing, so
// each scope has a query per sort order.
const (
	sortDateDesc  = "date_desc"
	sortDateAsc   = "date_asc"
	sortScoreDesc = "score_desc"
	sortScoreAsc  = "score_asc"
)

// normalizeItemSort maps an unknown or empty sort onto the default, matching
// the list endpoint's fallback.
func normalizeItemSort(sort string) string {
	switch sort {
	case sortDateAsc, sortScoreDesc, sortScoreAsc:
		return sort
	default:
		return sortDateDesc
	}
}

// aboveFlag converts the direction into the SQL flag the Mark*ReadRelative*
// queries take: 1 is everything strictly before the boundary item in the list
// order, 0 everything strictly after it. Both exclude the boundary item, so the
// two directions never overlap and the hovered row is left alone.
func aboveFlag(above bool) int64 {
	if above {
		return 1
	}
	return 0
}

// markRelativeByFeed marks items in a single feed at/above or below the
// boundary item.
func (s *Server) markRelativeByFeed(ctx context.Context, feedID, boundaryID int64, above bool, sort string, minScore int64) error {
	flag := aboveFlag(above)
	switch sort {
	case sortDateAsc:
		return s.queries.MarkFeedReadRelativeDateAsc(ctx, store.MarkFeedReadRelativeDateAscParams{
			FeedID: feedID, BoundaryID: boundaryID, Above: flag, MinScore: minScore})
	case sortScoreDesc:
		return s.queries.MarkFeedReadRelativeScoreDesc(ctx, store.MarkFeedReadRelativeScoreDescParams{
			FeedID: feedID, BoundaryID: boundaryID, Above: flag, MinScore: minScore})
	case sortScoreAsc:
		return s.queries.MarkFeedReadRelativeScoreAsc(ctx, store.MarkFeedReadRelativeScoreAscParams{
			FeedID: feedID, BoundaryID: boundaryID, Above: flag, MinScore: minScore})
	default:
		return s.queries.MarkFeedReadRelativeDateDesc(ctx, store.MarkFeedReadRelativeDateDescParams{
			FeedID: feedID, BoundaryID: boundaryID, Above: flag, MinScore: minScore})
	}
}

// markRelativeByCategory marks items across every feed in one category.
func (s *Server) markRelativeByCategory(ctx context.Context, userID int64, category string, boundaryID int64, above bool, sort string, minScore int64) error {
	flag := aboveFlag(above)
	switch sort {
	case sortDateAsc:
		return s.queries.MarkCategoryReadRelativeDateAsc(ctx, store.MarkCategoryReadRelativeDateAscParams{
			UserID: userID, Category: category, BoundaryID: boundaryID, Above: flag, MinScore: minScore})
	case sortScoreDesc:
		return s.queries.MarkCategoryReadRelativeScoreDesc(ctx, store.MarkCategoryReadRelativeScoreDescParams{
			UserID: userID, Category: category, BoundaryID: boundaryID, Above: flag, MinScore: minScore})
	case sortScoreAsc:
		return s.queries.MarkCategoryReadRelativeScoreAsc(ctx, store.MarkCategoryReadRelativeScoreAscParams{
			UserID: userID, Category: category, BoundaryID: boundaryID, Above: flag, MinScore: minScore})
	default:
		return s.queries.MarkCategoryReadRelativeDateDesc(ctx, store.MarkCategoryReadRelativeDateDescParams{
			UserID: userID, Category: category, BoundaryID: boundaryID, Above: flag, MinScore: minScore})
	}
}

// markRelativeByUser marks items across every feed the user owns.
func (s *Server) markRelativeByUser(ctx context.Context, userID, boundaryID int64, above bool, sort string, minScore int64) error {
	flag := aboveFlag(above)
	switch sort {
	case sortDateAsc:
		return s.queries.MarkAllFeedsReadRelativeDateAsc(ctx, store.MarkAllFeedsReadRelativeDateAscParams{
			UserID: userID, BoundaryID: boundaryID, Above: flag, MinScore: minScore})
	case sortScoreDesc:
		return s.queries.MarkAllFeedsReadRelativeScoreDesc(ctx, store.MarkAllFeedsReadRelativeScoreDescParams{
			UserID: userID, BoundaryID: boundaryID, Above: flag, MinScore: minScore})
	case sortScoreAsc:
		return s.queries.MarkAllFeedsReadRelativeScoreAsc(ctx, store.MarkAllFeedsReadRelativeScoreAscParams{
			UserID: userID, BoundaryID: boundaryID, Above: flag, MinScore: minScore})
	default:
		return s.queries.MarkAllFeedsReadRelativeDateDesc(ctx, store.MarkAllFeedsReadRelativeDateDescParams{
			UserID: userID, BoundaryID: boundaryID, Above: flag, MinScore: minScore})
	}
}

// colInt64 extracts an int64 from a scanned column value.
func colInt64(v interface{}) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case int:
		return int64(val)
	case float64:
		return int64(val)
	case []byte:
		n, _ := strconv.ParseInt(string(val), 10, 64)
		return n
	case string:
		n, _ := strconv.ParseInt(val, 10, 64)
		return n
	default:
		return 0
	}
}

// colStr extracts a string from a scanned column value.
func colStr(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case []byte:
		return string(val)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", val)
	}
}
