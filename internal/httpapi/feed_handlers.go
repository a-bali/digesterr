package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"digesterr/internal/feed"
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
	userID := authedUserID(r)
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
	// Email feeds carry a plus-address in a namespace shared by every user
	// (the AgentMail inbox is instance-wide), so the address is normalised and
	// checked before it reaches the database.
	if req.Type == "email" {
		addr, err := s.normalizeEmailFeedURL(req.URL)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		req.URL = addr
		if taken, err := s.emailAddressTaken(req.URL, 0); err != nil {
			writeServerError(w, "failed to check email address", err)
			return
		} else if taken {
			writeError(w, http.StatusBadRequest, "address already in use")
			return
		}
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

// discoverFeedRequest asks for the name a feed declares for itself, so the
// add-feed form can prefill the name field from the URL alone.
type discoverFeedRequest struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// discoverTimeout bounds the upstream fetch behind name discovery.
const discoverTimeout = 20 * time.Second

// handleDiscoverFeed returns the name a feed gives itself ({type, url} ->
// {name}). Discovery is a convenience for the add-feed form, so an unreachable
// feed or a type without configured credentials answers with an empty name
// rather than an error — the user simply types the name.
func (s *Server) handleDiscoverFeed(w http.ResponseWriter, r *http.Request) {
	var req discoverFeedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Type = strings.TrimSpace(req.Type)
	req.URL = strings.TrimSpace(req.URL)
	if req.Type == "" || req.URL == "" {
		writeError(w, http.StatusBadRequest, "type and url are required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), discoverTimeout)
	defer cancel()
	// An email feed's name comes from its own suffix, with no network call:
	// AgentMail has no "name this address" endpoint, and a lookup that could
	// time out would present as a broken form. Discovery here is a pure
	// convenience, matching the contract that a failed lookup returns "".
	if req.Type == "email" {
		if suffix, ok := feed.EmailSplitAddress(req.URL, s.emailInbox); ok {
			writeJSON(w, http.StatusOK, map[string]string{"name": feed.EmailSuffixToName(suffix)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"name": ""})
		return
	}
	name, err := s.poller.DiscoverFeedName(ctx, req.Type, req.URL)
	if err != nil {
		slog.Debug("feed name discovery failed", "type", req.Type, "url", req.URL, "error", err)
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

// normalizeEmailFeedURL validates and canonicalises an email feed's address.
//
// The UI sends the full address it composed from the configured prefix and the
// user's suffix, so this is the boundary where a bad address must be caught:
// a wrong suffix is not a harmless typo (there is no catch-all on AgentMail), it
// is a feed that silently never receives anything.
func (s *Server) normalizeEmailFeedURL(raw string) (string, error) {
	if !s.emailConfigured {
		return "", fmt.Errorf("email feeds are not configured on this server")
	}
	suffix, ok := feed.EmailSplitAddress(raw, s.emailInbox)
	if !ok {
		return "", fmt.Errorf("invalid email feed address: it must be %s<suffix>%s with a suffix of letters, digits, dots, dashes or underscores",
			feed.EmailInboxPrefix(s.emailInbox), s.emailDomain)
	}
	return feed.EmailBuildAddress(s.emailInbox, suffix), nil
}

// emailAddressTaken reports whether another feed already claims this address.
//
// The namespace is INSTANCE-WIDE, not per-user, because one AgentMail inbox and
// API key serve the whole instance: two feeds on one address would both read the
// same mail, and if they belong to different users, one user would read the
// other's subscription. The caller must not reveal WHICH feed holds the address
// — it may belong to someone else — so the response is a bare "already in use".
//
// excludeID skips a feed's own row so re-saving an unchanged address is not a
// conflict with itself.
func (s *Server) emailAddressTaken(url string, excludeID int64) (bool, error) {
	feeds, err := s.queries.ListEmailFeedURLs(context.Background())
	if err != nil {
		return false, err
	}
	for _, f := range feeds {
		if f.Url == url && f.ID != excludeID {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) handleListFeeds(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
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
	userID := authedUserID(r)
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
	userID := authedUserID(r)
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
	// Re-normalise an email feed's address on update, and re-check uniqueness
	// when it changed. The check ignores this feed's own row so saving without
	// touching the address is not a conflict with itself.
	if existing.Type == "email" {
		addr, err := s.normalizeEmailFeedURL(url)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		url = addr
		if taken, err := s.emailAddressTaken(url, existing.ID); err != nil {
			writeServerError(w, "failed to check email address", err)
			return
		} else if taken {
			writeError(w, http.StatusBadRequest, "address already in use")
			return
		}
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

	// If the feed left its category (moved or cleared), digest configs
	// selecting that category should no longer include it.
	if existing.Category != "" && category != existing.Category {
		if removed, err := s.pruneDigestConfigSources(r.Context(), userID, nil, existing.Category); err != nil {
			slog.Warn("failed to prune digest config sources after category change",
				"feed_id", id, "category", existing.Category, "error", err)
		} else if removed > 0 {
			slog.Info("pruned digest config sources after category change",
				"feed_id", id, "category", existing.Category, "configs_updated", removed)
		}
	}

	writeJSON(w, http.StatusOK, feed)
}

func (s *Server) handleDeleteFeed(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	// Capture the feed before deletion so digest configs referencing it (by
	// feed ID or by its category) can be cleaned up afterwards.
	feed, err := s.queries.GetFeed(r.Context(), store.GetFeedParams{ID: id, UserID: userID})
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

	if removed, err := s.pruneDigestConfigSources(r.Context(), userID, []int64{feed.ID}, feed.Category); err != nil {
		slog.Warn("failed to prune digest config sources after feed deletion",
			"feed_id", id, "category", feed.Category, "error", err)
	} else if removed > 0 {
		slog.Info("pruned digest config sources after feed deletion",
			"feed_id", id, "category", feed.Category, "configs_updated", removed)
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "deleted"})
}

// pruneDigestConfigSources removes the given feed IDs and category from every
// digest config of the user that references them. A config left with no
// sources at all gets both lists cleared (it stays but generates nothing
// until reconfigured). Returns the number of configs updated.
func (s *Server) pruneDigestConfigSources(ctx context.Context, userID int64, feedIDs []int64, category string) (int, error) {
	configs, err := s.queries.ListDigestConfigs(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("listing digest configs: %w", err)
	}
	idSet := make(map[int64]bool, len(feedIDs))
	for _, fid := range feedIDs {
		idSet[fid] = true
	}
	updated := 0
	for _, cfg := range configs {
		var parsed struct {
			Feeds []struct {
				ID         int `json:"id"`
				Highlights int `json:"highlights"`
				Comments   int `json:"comments"`
			} `json:"feeds"`
			Categories []struct {
				Name       string `json:"name"`
				Highlights int    `json:"highlights"`
				Comments   int    `json:"comments"`
			} `json:"categories"`
		}
		if err := json.Unmarshal([]byte(cfg.Config), &parsed); err != nil {
			continue // unparseable config — leave untouched
		}
		feedsChanged, catsChanged := false, false
		feeds := parsed.Feeds[:0]
		for _, f := range parsed.Feeds {
			if idSet[int64(f.ID)] {
				feedsChanged = true
				continue
			}
			feeds = append(feeds, f)
		}
		categories := parsed.Categories[:0]
		for _, c := range parsed.Categories {
			if category != "" && c.Name == category {
				catsChanged = true
				continue
			}
			categories = append(categories, c)
		}
		if !feedsChanged && !catsChanged {
			continue
		}
		parsed.Feeds = feeds
		parsed.Categories = categories
		blob, err := json.Marshal(parsed)
		if err != nil {
			return updated, fmt.Errorf("marshaling config %d: %w", cfg.ID, err)
		}
		if _, err := s.queries.UpdateDigestConfig(ctx, store.UpdateDigestConfigParams{
			ID:       cfg.ID,
			UserID:   userID,
			Name:     cfg.Name,
			Schedule: cfg.Schedule,
			Config:   string(blob),
		}); err != nil {
			return updated, fmt.Errorf("updating config %d: %w", cfg.ID, err)
		}
		updated++
	}
	return updated, nil
}

func (s *Server) handleListFeedItems(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
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

	// All 4 sort queries return identical columns; map to a common type.
	type itemPreview struct {
		ID             int64       `json:"id"`
		Title          string      `json:"title"`
		Status         string      `json:"status"`
		PublishedAt    interface{} `json:"published_at"`
		Content        string      `json:"content"`
		ExtractContent string      `json:"extract_content"`
		ExtractStatus  string      `json:"extract_status"`
		Points         int64       `json:"points"`
		CommentsCount  int64       `json:"comments_count"`
		CommentUrl     string      `json:"comment_url"`
	}
	var items []itemPreview
	var rawItems interface{}
	switch sort {
	case "date_asc":
		rawItems, err = s.queries.ListItemsByFeedSortedDateAsc(r.Context(), store.ListItemsByFeedSortedDateAscParams{
			FeedID: id, Status: statusArg, MinScore: minScore, Limit: limit, Offset: offset,
		})
	case "score_desc":
		rawItems, err = s.queries.ListItemsByFeedSortedScoreDesc(r.Context(), store.ListItemsByFeedSortedScoreDescParams{
			FeedID: id, Status: statusArg, MinScore: minScore, Limit: limit, Offset: offset,
		})
	case "score_asc":
		rawItems, err = s.queries.ListItemsByFeedSortedScoreAsc(r.Context(), store.ListItemsByFeedSortedScoreAscParams{
			FeedID: id, Status: statusArg, MinScore: minScore, Limit: limit, Offset: offset,
		})
	default:
		rawItems, err = s.queries.ListItemsByFeedSortedDateDesc(r.Context(), store.ListItemsByFeedSortedDateDescParams{
			FeedID: id, Status: statusArg, MinScore: minScore, Limit: limit, Offset: offset,
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
			items[i] = itemPreview{r.ID, r.Title, r.Status, r.PublishedAt, r.Content, r.ExtractContent, r.ExtractStatus, r.Points, r.CommentsCount, r.CommentUrl}
		}
	case []store.ListItemsByFeedSortedDateAscRow:
		items = make([]itemPreview, len(rows))
		for i, r := range rows {
			items[i] = itemPreview{r.ID, r.Title, r.Status, r.PublishedAt, r.Content, r.ExtractContent, r.ExtractStatus, r.Points, r.CommentsCount, r.CommentUrl}
		}
	case []store.ListItemsByFeedSortedScoreDescRow:
		items = make([]itemPreview, len(rows))
		for i, r := range rows {
			items[i] = itemPreview{r.ID, r.Title, r.Status, r.PublishedAt, r.Content, r.ExtractContent, r.ExtractStatus, r.Points, r.CommentsCount, r.CommentUrl}
		}
	case []store.ListItemsByFeedSortedScoreAscRow:
		items = make([]itemPreview, len(rows))
		for i, r := range rows {
			items[i] = itemPreview{r.ID, r.Title, r.Status, r.PublishedAt, r.Content, r.ExtractContent, r.ExtractStatus, r.Points, r.CommentsCount, r.CommentUrl}
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
			FeedID: id, Status: statusArg, MinScore: minScore,
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
		writeError(w, http.StatusInternalServerError, "failed to fetch feed")
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

func (s *Server) handleMarkFeedRead(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
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
	ExtractContent string      `json:"extract_content"`
	ExtractStatus  string      `json:"extract_status"`
	FeedName       string      `json:"feed_name"`
	FeedType       string      `json:"feed_type"`
}

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
	minScore := int64(0)
	if ms := r.URL.Query().Get("min_score"); ms != "" {
		v, err := strconv.ParseInt(ms, 10, 64)
		if err != nil || v < 0 {
			writeError(w, http.StatusBadRequest, "invalid min_score")
			return
		}
		minScore = v
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

	// Add status, min_score filters.
	if status != "" {
		whereClause += " AND i.status = ?"
		args = append(args, status)
	} else {
		whereClause += " AND (i.status IS NULL OR i.status IS NOT NULL)"
	}
	whereClause += " AND (i.points >= ? OR ? = 0)"
	args = append(args, minScore, minScore)

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
	// middle pane — excludes hash, url, author, and internal metadata.
	queryStr := fmt.Sprintf(
		"SELECT i.id, i.title, i.content, i.comment_url, i.points, i.comments_count, "+
			"i.published_at, i.status, i.extract_content, i.extract_status, "+
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
			ExtractContent: colStr(colMap["extract_content"]),
			ExtractStatus:  colStr(colMap["extract_status"]),
			FeedName:       colStr(colMap["feed_name"]),
			FeedType:       colStr(colMap["feed_type"]),
		}
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
