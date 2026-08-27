package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"digesterr/internal/store"
)

type HNEnricher struct {
	client      *http.Client
	firebaseURL string
	algoliaURL  string
	// limiter throttles the per-item Firebase fallback calls.
	limiter  *rate.Limiter
	maxHits  int // 0 = unbounded (resolve all pending items)
}

func NewHNEnricher() *HNEnricher {
	return &HNEnricher{
		client:      &http.Client{Timeout: 10 * time.Second},
		firebaseURL: "https://hacker-news.firebaseio.com",
		algoliaURL:  "https://hn.algolia.com",
		limiter:     rate.NewLimiter(defaultHNRate, defaultHNburst),
	}
}

// SetRateLimiter overrides the per-item call limiter (nil disables limiting).
func (e *HNEnricher) SetRateLimiter(l *rate.Limiter) { e.limiter = l }

// SetMaxHits overrides the max number of items to resolve per enrichment
// batch. Zero means unbounded.
func (e *HNEnricher) SetMaxHits(n int) { e.maxHits = n }

// Enrich fetches a single item from the Firebase API.
func (e *HNEnricher) Enrich(ctx context.Context, item *store.Item) error {
	hnID := extractHNID(item.CommentUrl, item.Url)
	if hnID == 0 {
		return fmt.Errorf("could not extract HN item ID")
	}

	if err := waitLimiter(ctx, e.limiter); err != nil {
		return fmt.Errorf("rate limiter: %w", err)
	}
	apiURL := fmt.Sprintf("%s/v0/item/%d.json", e.firebaseURL, hnID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return fmt.Errorf("creating HN API request: %w", err)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching HN item %d: %w", hnID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HN API returned status %d for item %d", resp.StatusCode, hnID)
	}

	var hnItem struct {
		ID          int `json:"id"`
		Score       int `json:"score"`
		Descendants int `json:"descendants"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&hnItem); err != nil {
		return fmt.Errorf("decoding HN API response: %w", err)
	}
	if hnItem.ID == 0 {
		return fmt.Errorf("HN item %d not found", hnID)
	}

	item.Points = int64(hnItem.Score)
	item.CommentsCount = int64(hnItem.Descendants)
	return nil
}

// BulkEnrich fetches current points/comments for multiple items via Algolia search.
func (e *HNEnricher) BulkEnrich(ctx context.Context, items []*store.Item) error {
	if len(items) == 0 {
		return nil
	}

	// Build the Algolia filter: objectID:12345 OR objectID:67890
	filters := make([]string, 0, len(items))
	idToItem := make(map[string]*store.Item, len(items))
	for _, item := range items {
		hnID := extractHNID(item.CommentUrl, item.Url)
		if hnID == 0 {
			continue
		}
		sID := strconv.Itoa(hnID)
		filters = append(filters, "objectID:"+sID)
		idToItem[sID] = item
	}
	if len(filters) == 0 {
		return nil
	}
	// Cap the batch size when a limit is configured.
	hitsPerPage := len(filters)
	if e.maxHits > 0 && hitsPerPage > e.maxHits {
		hitsPerPage = e.maxHits
		filters = filters[:hitsPerPage]
		idToItemTrimmed := make(map[string]*store.Item, hitsPerPage)
		for _, f := range filters {
			id := strings.TrimPrefix(f, "objectID:")
			if item, ok := idToItem[id]; ok {
				idToItemTrimmed[id] = item
			}
		}
		idToItem = idToItemTrimmed
	}

	apiURL := fmt.Sprintf("%s/api/v1/search?query=&tags=story&filters=%s&hitsPerPage=%d",
		e.algoliaURL, url.QueryEscape(strings.Join(filters, " OR ")), hitsPerPage)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return fmt.Errorf("creating Algolia request: %w", err)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching Algolia bulk: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Algolia returned status %d", resp.StatusCode)
	}

	var algoliaResp struct {
		Hits []struct {
			ObjectID    string `json:"objectID"`
			Points      int    `json:"points"`
			NumComments int    `json:"num_comments"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&algoliaResp); err != nil {
		return fmt.Errorf("decoding Algolia response: %w", err)
	}

	for _, hit := range algoliaResp.Hits {
		if item, ok := idToItem[hit.ObjectID]; ok {
			item.Points = int64(hit.Points)
			item.CommentsCount = int64(hit.NumComments)
		}
	}

	// Detect misses: the Algolia index can lag or stall entirely (seen in
	// production Aug 2026 — fresh stories absent from search results while
	// the Firebase API served them fine). Fill individual misses from the
	// Firebase API and surface a warning instead of silently storing zeros.
	var missed []*store.Item
	for _, item := range items {
		if item.Points > 0 {
			continue
		}
		if extractHNID(item.CommentUrl, item.Url) != 0 {
			missed = append(missed, item)
		}
	}
	if len(missed) > 0 {
		slog.Warn("hn algolia bulk enrichment incomplete, falling back to firebase",
			"requested", len(filters), "matched", len(algoliaResp.Hits), "fallback", len(missed))
		var firstErr error
		for _, item := range missed {
			if err := e.Enrich(ctx, item); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if firstErr != nil {
			return fmt.Errorf("hn enrichment incomplete (%d/%d matched via algolia): %w",
				len(algoliaResp.Hits), len(filters), firstErr)
		}
	}

	slog.Debug("hn bulk enrichment completed", "requested", len(filters), "matched", len(algoliaResp.Hits))

	return nil
}

// FetchComments fetches the comment tree from Algolia and ranks by descendant count.
func (e *HNEnricher) FetchComments(ctx context.Context, item store.Item, maxComments int) ([]Comment, error) {
	hnID := extractHNID(item.CommentUrl, item.Url)
	if hnID == 0 {
		return nil, fmt.Errorf("could not extract HN item ID")
	}

	apiURL := fmt.Sprintf("%s/api/v1/items/%d", e.algoliaURL, hnID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating Algolia items request: %w", err)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching Algolia item %d: %w", hnID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Algolia returned status %d for item %d", resp.StatusCode, hnID)
	}

	var algoliaResp struct {
		Children []algoliaComment `json:"children"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&algoliaResp); err != nil {
		return nil, fmt.Errorf("decoding Algolia item response: %w", err)
	}

	var comments []Comment
	flattenComments(algoliaResp.Children, 0, &comments)

	// Rank by total descendant count (HN doesn't expose comment scores).
	childrenOf := make(map[int64][]algoliaComment)
	var indexDescendants func(children []algoliaComment)
	indexDescendants = func(children []algoliaComment) {
		for _, c := range children {
			childrenOf[c.ID] = c.Children
			indexDescendants(c.Children)
		}
	}
	indexDescendants(algoliaResp.Children)

	var countDesc func(id int64) int
	countDesc = func(id int64) int {
		total := 0
		for _, child := range childrenOf[id] {
			total += 1 + countDesc(child.ID)
		}
		return total
	}

	sort.Slice(comments, func(i, j int) bool {
		return countDesc(comments[i].ID) > countDesc(comments[j].ID)
	})

	// HN doesn't expose per-comment scores; the descendant count is what we
	// rank by, so expose it as the comment's score for display in digests.
	for i := range comments {
		comments[i].Points = countDesc(comments[i].ID)
		comments[i].ValidPoints = true
	}

	if maxComments > 0 && len(comments) > maxComments {
		comments = comments[:maxComments]
	}
	return comments, nil
}

type algoliaComment struct {
	ID       int64            `json:"id"`
	Author   string           `json:"author"`
	Text     string           `json:"text"`
	Points   int              `json:"points"`
	Children []algoliaComment `json:"children"`
}

func flattenComments(children []algoliaComment, depth int, out *[]Comment) {
	for _, c := range children {
		if c.Text == "" {
			flattenComments(c.Children, depth+1, out)
			continue
		}
		*out = append(*out, Comment{
			ID:     c.ID,
			Author: c.Author,
			Text:   stripHTMLTags(c.Text),
			Points: c.Points,
			Depth:  depth,
			URL:    fmt.Sprintf("https://news.ycombinator.com/item?id=%d", c.ID),
		})
		flattenComments(c.Children, depth+1, out)
	}
}

func stripHTMLTags(s string) string {
	var buf strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			buf.WriteRune(r)
		}
	}
	return html.UnescapeString(strings.TrimSpace(buf.String()))
}

func extractHNID(commentURL, itemURL string) int {
	for _, s := range []string{commentURL, itemURL} {
		if s == "" {
			continue
		}
		u, err := url.Parse(s)
		if err != nil {
			continue
		}
		if !strings.Contains(u.Host, "ycombinator.com") && !strings.Contains(u.Host, "hn.algolia.com") {
			continue
		}
		idStr := u.Query().Get("id")
		if idStr == "" {
			continue
		}
		id, err := strconv.Atoi(idStr)
		if err == nil && id > 0 {
			return id
		}
	}
	return 0
}
