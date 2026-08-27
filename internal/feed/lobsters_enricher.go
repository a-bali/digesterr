package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"digesterr/internal/store"
)

type LobstersEnricher struct {
	client   *http.Client
	baseURL  string
	limiter  *rate.Limiter
	maxItems int // 0 = unbounded (enrich all items)
}

// SetRateLimiter overrides the per-item call limiter (nil disables limiting).
func (e *LobstersEnricher) SetRateLimiter(l *rate.Limiter) { e.limiter = l }

// SetMaxItems overrides the max number of stories to enrich per poll.
// Zero means all items returned by the RSS feed.
func (e *LobstersEnricher) SetMaxItems(n int) { e.maxItems = n }

func NewLobstersEnricher() *LobstersEnricher {
	return &LobstersEnricher{
		client:  &http.Client{Timeout: 10 * time.Second},
		baseURL: "https://lobste.rs",
		limiter: rate.NewLimiter(4, 4),
	}
}

func (e *LobstersEnricher) Enrich(ctx context.Context, item *store.Item) error {
	shortID := extractLobstersShortID(item.CommentUrl, item.Url, item.Hash)
	if shortID == "" {
		return fmt.Errorf("could not extract Lobste.rs short ID")
	}
	return e.enrichOne(ctx, item, shortID)
}

func (e *LobstersEnricher) BulkEnrich(ctx context.Context, items []*store.Item) error {
	// Resolve short IDs first, then enrich concurrently with a bounded
	// worker pool instead of a serial 1s sleep per item (O(n) seconds).
	type target struct {
		item    *store.Item
		shortID string
	}
	var targets []target
	for _, item := range items {
		if shortID := extractLobstersShortID(item.CommentUrl, item.Url, item.Hash); shortID != "" {
			targets = append(targets, target{item: item, shortID: shortID})
		}
	}
	if len(targets) == 0 {
		return nil
	}
	// Cap the batch size when a limit is configured.
	if e.maxItems > 0 && len(targets) > e.maxItems {
		targets = targets[:e.maxItems]
	}

	const workers = 4
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	enriched, errors := 0, 0
	for _, t := range targets {
		wg.Add(1)
		go func(t target) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			if err := e.enrichOne(ctx, t.item, t.shortID); err != nil {
				slog.Debug("failed to enrich Lobste.rs item", "item_id", t.item.ID, "short_id", t.shortID, "error", err)
				mu.Lock()
				errors++
				mu.Unlock()
				return
			}
			mu.Lock()
			enriched++
			mu.Unlock()
		}(t)
	}
	wg.Wait()
	slog.Debug("lobste.rs bulk enrichment completed", "success", enriched, "error", errors, "total", len(items))
	return nil
}

type lobstersStory struct {
	ShortID      string            `json:"short_id"`
	Score        int               `json:"score"`
	CommentCount int               `json:"comment_count"`
	Comments     []lobstersComment `json:"comments"`
}

type lobstersComment struct {
	ShortID        string            `json:"short_id"`
	Comment        string            `json:"comment"`
	Score          int               `json:"score"`
	CommentingUser string            `json:"commenting_user"`
	Children       []lobstersComment `json:"children"`
}

func (e *LobstersEnricher) enrichOne(ctx context.Context, item *store.Item, shortID string) error {
	if err := waitLimiter(ctx, e.limiter); err != nil {
		return err
	}
	apiURL := fmt.Sprintf("%s/s/%s.json", e.baseURL, shortID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return fmt.Errorf("creating Lobste.rs API request: %w", err)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching Lobste.rs story %s: %w", shortID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Lobste.rs API returned status %d for story %s", resp.StatusCode, shortID)
	}

	var lsItem struct {
		ShortID      string `json:"short_id"`
		Score        int    `json:"score"`
		CommentCount int    `json:"comment_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&lsItem); err != nil {
		return fmt.Errorf("decoding Lobste.rs API response: %w", err)
	}
	if lsItem.ShortID == "" {
		return fmt.Errorf("Lobste.rs story %s not found", shortID)
	}

	item.Points = int64(lsItem.Score)
	item.CommentsCount = int64(lsItem.CommentCount)
	return nil
}

func (e *LobstersEnricher) FetchComments(ctx context.Context, item store.Item, maxComments int) ([]Comment, error) {
	shortID := extractLobstersShortID(item.CommentUrl, item.Url, item.Hash)
	if shortID == "" {
		return nil, fmt.Errorf("could not extract Lobste.rs short ID")
	}

	if err := waitLimiter(ctx, e.limiter); err != nil {
		return nil, err
	}
	apiURL := fmt.Sprintf("%s/s/%s.json", e.baseURL, shortID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating Lobste.rs API request: %w", err)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching Lobste.rs story %s: %w", shortID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Lobste.rs API returned status %d for story %s", resp.StatusCode, shortID)
	}

	var story lobstersStory
	if err := json.NewDecoder(resp.Body).Decode(&story); err != nil {
		return nil, fmt.Errorf("decoding Lobste.rs API response: %w", err)
	}

	var comments []Comment
	flattenLobstersComments(story.Comments, shortID, 0, &comments)
	sort.Slice(comments, func(i, j int) bool { return comments[i].Points > comments[j].Points })

	if maxComments > 0 && len(comments) > maxComments {
		comments = comments[:maxComments]
	}
	return comments, nil
}

func flattenLobstersComments(children []lobstersComment, shortID string, depth int, out *[]Comment) {
	for _, c := range children {
		if c.Comment == "" {
			flattenLobstersComments(c.Children, shortID, depth+1, out)
			continue
		}
		*out = append(*out, Comment{
			Author:      c.CommentingUser,
			Text:        stripHTMLTags(c.Comment),
			Points:      c.Score,
			ValidPoints: true,
			Depth:       depth,
			URL:         fmt.Sprintf("https://lobste.rs/s/%s#c_%s", shortID, c.ShortID),
		})
		flattenLobstersComments(c.Children, shortID, depth+1, out)
	}
}

func extractLobstersShortID(urls ...string) string {
	for _, s := range urls {
		if s == "" {
			continue
		}
		u, err := url.Parse(s)
		if err != nil {
			continue
		}
		if !strings.Contains(u.Host, "lobste.rs") {
			continue
		}
		p := path.Clean(u.Path)
		if !strings.HasPrefix(p, "/s/") {
			continue
		}
		parts := strings.SplitN(p, "/", 4)
		if len(parts) >= 3 && parts[2] != "" {
			return parts[2]
		}
	}
	return ""
}
