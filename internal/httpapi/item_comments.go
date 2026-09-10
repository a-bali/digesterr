package httpapi

import (
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"digesterr/internal/feed"
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
	}
	// Background cleanup every minute.
	go c.cleanup()
	return c
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
}

func (c *commentCache) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
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

	comments, err := cf.FetchComments(ctx, item, limit)
	// Record comment fetch metrics with the correct target URL.
	if s.metrics != nil {
		statusCode := metrics.StatusFor(err)
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		// Comment fetching goes through the same shared transport as polling,
		// so take both counters to keep bytes and requests attributed together.
		var respBytes int64
		var reqs int
		if s.poller != nil {
			respBytes, reqs = s.poller.ConsumeCounters()
		}
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

// imgTagRe matches markdown image syntax: ![alt](url)
var imgTagRe = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)

// redditGiphyRe matches Reddit's internal giphy syntax: ![alt](giphy|ID)
var redditGiphyRe = regexp.MustCompile(`!\[([^\]]*)\]\(giphy\|([^)]+)\)`)

// bareImageURLRe matches bare image URLs from common image hosts.
// Split into giphy (multiple subdomains, share URLs) and other hosts.
var bareImageURLRe = regexp.MustCompile(
	`https?://` +
		`(?:media\d*\.giphy\.com/\S+\.(?:gif|webp|png|jpg|jpeg)(?:\?\S*)?` +
		`|giphy\.com/gifs/\S+` +
		`|[^/\s]*\.?(?:i\.redd\.it|preview\.redd\.it|imgur\.com|i\.imgur\.com|files\.catbox\.moe|cdn\.discordapp\.com)/\S+\.(?:jpg|jpeg|png|gif|webp|svg)(?:\?\S*)?)`,
)

// renderCommentText converts raw comment text to HTML for the Reader API.
// For Reddit comments it converts markdown image syntax and bare image URLs
// to sized <img> tags. Other feed types get plain escaped text.
func renderCommentText(text string, feedType string) string {
	if feedType != "reddit" {
		escaped := html.EscapeString(text)
		return strings.ReplaceAll(escaped, "\n", "<br>")
	}

	// Phase 0: Convert Reddit's internal giphy syntax ![alt](giphy|ID) to real URLs.
	result := redditGiphyRe.ReplaceAllStringFunc(text, func(match string) string {
		parts := redditGiphyRe.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		alt := parts[1]
		giphyID := parts[2]
		return fmt.Sprintf("![%s](https://media.giphy.com/media/%s/giphy.gif)", alt, giphyID)
	})

	// Phase 1: Replace markdown images with placeholders.
	imgTags := make(map[string]string) // placeholder → <img> tag
	imgIdx := 0
	result = imgTagRe.ReplaceAllStringFunc(result, func(match string) string {
		parts := imgTagRe.FindStringSubmatch(match)
		if len(parts) < 3 {
			return html.EscapeString(match)
		}
		alt := html.EscapeString(parts[1])
		src := html.EscapeString(parts[2])
		key := fmt.Sprintf("\x00IMG%d\x00", imgIdx)
		imgTags[key] = fmt.Sprintf(
			`<img src="%s" alt="%s" style="max-width:400px;max-height:300px;width:auto;height:auto;border-radius:4px;margin:4px 0" loading="lazy" />`,
			src, alt,
		)
		imgIdx++
		return key
	})

	// Phase 2: Replace bare image URLs with placeholders.
	result = bareImageURLRe.ReplaceAllStringFunc(result, func(match string) string {
		key := fmt.Sprintf("\x00IMG%d\x00", imgIdx)
		imgTags[key] = fmt.Sprintf(
			`<img src="%s" alt="" style="max-width:400px;max-height:300px;width:auto;height:auto;border-radius:4px;margin:4px 0" loading="lazy" />`,
			html.EscapeString(match),
		)
		imgIdx++
		return key
	})

	// Phase 3: Escape remaining HTML (placeholders are null-byte delimited, unaffected).
	result = html.EscapeString(result)

	// Phase 3b: Decode Reddit's HTML entities that were in the ORIGINAL text.
	// These were escaped by Phase 3 (e.g. &gt; → &amp;gt;) so we decode the
	// double-escaped form back to the original entity, then decode the entity.
	// Reddit uses &gt; &lt; &amp; &quot; &#39; in comment bodies.
	result = strings.ReplaceAll(result, "&amp;gt;", ">")
	result = strings.ReplaceAll(result, "&amp;lt;", "<")
	result = strings.ReplaceAll(result, "&amp;quot;", "\"")
	result = strings.ReplaceAll(result, "&amp;#39;", "'")
	result = strings.ReplaceAll(result, "&amp;amp;", "&")

	// Phase 4: Restore img tags from placeholders.
	for key, tag := range imgTags {
		result = strings.ReplaceAll(result, key, tag)
	}
	return result
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
		author := html.EscapeString(c.Author)
		if c.URL != "" {
			author = fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(c.URL), author)
		}
		points := ""
		if c.ValidPoints && c.Points > 0 {
			points = fmt.Sprintf(" <span style=\"color:#888;font-size:0.85em\">(%d)</span>", c.Points)
		}
		rendered := renderCommentText(c.Text, feedType)
		b.WriteString(fmt.Sprintf(`<li style="margin-bottom:0.75em;padding-left:0.5em;border-left:2px solid #ddd"><strong>%s</strong>%s<br/><span style="font-size:0.9em">%s</span></li>`+"\n",
			author, points, rendered))
	}
	b.WriteString(`</ul>` + "\n")
	b.WriteString(`</div>` + "\n")
	return b.String()
}
