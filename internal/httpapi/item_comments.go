package httpapi

import (
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"digesterr/internal/feed"
	"digesterr/internal/linkify"
	"digesterr/internal/logging"
	"digesterr/internal/metrics"
	"digesterr/internal/store"
)

type itemComment struct {
	Author      string `json:"author"`
	Text        string `json:"text"`
	Points      int    `json:"points"`
	ValidPoints bool   `json:"valid_points"`
	URL         string `json:"url"`
}

// commentCache is a simple in-memory cache for fetched comments with TTL.
type commentCache struct {
	mu    sync.RWMutex
	items map[int64]commentCacheEntry
	ttl   time.Duration

	// stop is closed by Close to end the background cleanup goroutine.
	// stopOnce makes Close idempotent so shutdown paths need not coordinate.
	stop     chan struct{}
	stopOnce sync.Once
	// startOnce guards the lazily-started sweeper (see startSweeper).
	startOnce sync.Once
}

type commentCacheEntry struct {
	comments []feed.Comment
	expires  time.Time
}

func newCommentCache(ttl time.Duration) *commentCache {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	c := &commentCache{
		items: make(map[int64]commentCacheEntry),
		ttl:   ttl,
		stop:  make(chan struct{}),
	}
	return c
}

// startSweeper launches the background expiry sweep, once, on first use.
//
// It is lazy so that a commentCache which never stores anything owns no
// goroutine: every Server constructs one, and a process that makes no comment
// fetches (most of the test suite, and any deployment with
// FEED_TOP_COMMENTS=0) would otherwise run a sweeper that can never have work.
func (c *commentCache) startSweeper() {
	c.startOnce.Do(func() {
		go c.cleanup()
	})
}

// Close ends the background cleanup goroutine. It is idempotent, so production
// shutdown and test cleanup can both call it without coordinating.
//
// Without this, every commentCache — and therefore every Server — leaks one
// goroutine for the life of the process: the cleanup loop blocked on its ticker
// forever. The test suite constructs hundreds of servers, which is how the leak
// surfaced (a goroutine dump on test timeout was wall-to-wall cleanup loops).
func (c *commentCache) Close() {
	c.stopOnce.Do(func() {
		if c.stop != nil {
			close(c.stop)
		}
	})
}

func (c *commentCache) get(itemID int64) ([]feed.Comment, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.items[itemID]
	if !ok || time.Now().After(entry.expires) {
		return nil, false
	}
	// Return a copy so callers can't mutate the cache.
	out := make([]feed.Comment, len(entry.comments))
	copy(out, entry.comments)
	return out, true
}

func (c *commentCache) set(itemID int64, comments []feed.Comment) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[itemID] = commentCacheEntry{
		comments: comments,
		expires:  time.Now().Add(c.ttl),
	}
	// First insert starts the sweeper; without it nothing would ever expire, and
	// starting it here means a cache that is never written owns no goroutine.
	c.startSweeper()
}

func (c *commentCache) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
		}
		c.mu.Lock()
		now := time.Now()
		for id, entry := range c.items {
			if now.After(entry.expires) {
				delete(c.items, id)
			}
		}
		c.mu.Unlock()
	}
}

func (s *Server) handleGetItemComments(w http.ResponseWriter, r *http.Request) {
	if s.topComments <= 0 {
		writeJSON(w, http.StatusOK, itemCommentsResponse{Comments: []itemComment{}, Cached: false})
		return
	}

	userID := authedUserID(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	item, err := s.queries.GetItemWithFeedOwner(r.Context(), store.GetItemWithFeedOwnerParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}

	bypassCache := r.URL.Query().Get("refresh") == "true"
	comments, cached := s.fetchTopComments(r, item, s.topComments, bypassCache)
	if v := r.URL.Query().Get("limit"); v != "" && comments != nil {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n < len(comments) {
			comments = comments[:n]
		}
	}

	result := make([]itemComment, len(comments))
	for i, c := range comments {
		result[i] = itemComment{
			Author:      c.Author,
			Text:        c.Text,
			Points:      c.Points,
			ValidPoints: c.ValidPoints,
			URL:         c.URL,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(itemCommentsResponse{Comments: result, Cached: cached})
}

type itemCommentsResponse struct {
	Comments []itemComment `json:"comments"`
	Cached   bool          `json:"cached"`
}

// fetchTopComments fetches the top comments for an item, using the in-memory
// cache when available. Returns the comments and whether they were served
// from cache. When bypassCache is true, the cache is skipped and refreshed.
func (s *Server) fetchTopComments(r *http.Request, item store.Item, limit int, bypassCache bool) ([]feed.Comment, bool) {
	start := time.Now()
	if s.topComments <= 0 || limit <= 0 {
		return nil, false
	}

	// Check cache first (unless bypassed).
	if !bypassCache && s.commentCache != nil {
		if cached, ok := s.commentCache.get(item.ID); ok {
			if len(cached) > limit {
				return cached[:limit], true
			}
			return cached, true
		}
	}

	ctx := r.Context()
	feedObj, err := s.queries.GetFeedByID(ctx, item.FeedID)
	if err != nil {
		return nil, false
	}

	cf, ok := s.poller.CommentFetchers()[feedObj.Type]
	if !ok || isNoopCommentFetcher(cf) {
		return nil, false
	}

	// Comment fetching goes through the same transport as polling, and this
	// handler runs concurrently with the poller: attribute from this request's own
	// traffic rather than from the shared counters, which another feed's requests
	// can be sitting in.
	scope := metrics.NewScope()
	comments, err := cf.FetchComments(metrics.WithScope(ctx, scope), item, limit)
	// Record comment fetch metrics with the correct target URL.
	if s.metrics != nil {
		statusCode := metrics.StatusFor(err)
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		respBytes, reqs := scope.Totals()
		s.metrics.Record(metrics.Record{
			Timestamp:  time.Now(),
			URL:        cf.CommentTargetURL(),
			StatusCode: statusCode,
			Bytes:      respBytes,
			Requests:   reqs,
			DurationMs: time.Since(start).Milliseconds(),
			Reason:     "enrichment",
			FeedID:     feedObj.ID,
			FeedName:   feedObj.Name,
			FeedType:   feedObj.Type,
			Error:      errStr,
		})
	}
	if err != nil {
		slog.Warn("failed to fetch comments",
			logging.CategoryAttr(logging.CategoryItem),
			"item_id", item.ID, "feed_type", feedObj.Type, "error", err)
		return nil, false
	}

	sort.Slice(comments, func(a, b int) bool { return comments[a].Points > comments[b].Points })

	// Store full result in cache (untrimmed), trim on return.
	if s.commentCache != nil {
		s.commentCache.set(item.ID, comments)
	}

	if len(comments) > limit {
		comments = comments[:limit]
	}
	return comments, false
}

// isNoopCommentFetcher returns true if the CommentFetcher is a NoopEnricher.
func isNoopCommentFetcher(cf feed.CommentFetcher) bool {
	_, ok := cf.(*feed.NoopEnricher)
	return ok
}

// renderCommentText converts raw comment text to HTML for the Reader API.
// The conversion itself lives in internal/linkify, shared with every other
// surface that shows comment text (web item pane, web digest view, Reader API
// digest bodies) so a URL is linkified the same way everywhere.
//
// feedType is kept for the call sites; linkification is now feed-type
// independent (HN and Lobsters comments carry bare URLs too, and a link is the
// right rendering for any of them).
func renderCommentText(text string, feedType string) string {
	_ = feedType
	return linkify.Text(text)
}

// topCommentsHTML renders top comments as an HTML block suitable for
// appending to an item's content body in the Reader API.
func topCommentsHTML(comments []feed.Comment, feedType string) string {
	if len(comments) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(`<div class="digest-comments" style="margin-top:1em;border-top:1px solid #ccc;padding-top:0.5em">` + "\n")
	b.WriteString(`<h4 style="font-size:0.85em;margin:0 0 0.5em">Top comments</h4>` + "\n")
	b.WriteString(`<ul style="list-style:none;padding:0;margin:0">` + "\n")
	for _, c := range comments {
		b.WriteString(commentListItemHTML(c.Author, c.Text, c.Points, c.ValidPoints, c.URL, feedType) + "\n")
	}
	b.WriteString(`</ul>` + "\n")
	b.WriteString(`</div>` + "\n")
	return b.String()
}

// commentListItemHTML renders one comment as a list item with the quote-bar
// styling used everywhere comments appear in the Reader API: a left border
// instead of bullet indentation, the author in bold, the score as a muted
// parenthetical, and the body at a slightly reduced size.
//
// It is the single definition of "a comment looks like this" so a feed item's
// comments and a digest highlight's comments cannot drift apart. Points are
// only shown when the source actually carried a score (validPoints), which is
// how a zero-vote comment avoids rendering a misleading "(0)".
func commentListItemHTML(author, text string, points int, validPoints bool, url, feedType string) string {
	renderedAuthor := html.EscapeString(author)
	if url != "" {
		renderedAuthor = fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(url), renderedAuthor)
	}
	renderedPoints := ""
	if validPoints && points > 0 {
		renderedPoints = fmt.Sprintf(" <span style=\"color:#888;font-size:0.85em\">(%d)</span>", points)
	}
	renderedText := renderCommentText(text, feedType)
	return fmt.Sprintf(`<li style="margin-bottom:0.75em;padding-left:0.5em;border-left:2px solid #ddd"><strong>%s</strong>%s<br/><span style="font-size:0.9em">%s</span></li>`,
		renderedAuthor, renderedPoints, renderedText)
}
