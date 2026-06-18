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
	"time"

	"digesterr/internal/store"
)

type LobstersEnricher struct {
	client  *http.Client
	baseURL string
}

func NewLobstersEnricher() *LobstersEnricher {
	return &LobstersEnricher{
		client:  &http.Client{Timeout: 10 * time.Second},
		baseURL: "https://lobste.rs",
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
	enriched := 0
	errors := 0
	for _, item := range items {
		shortID := extractLobstersShortID(item.CommentUrl, item.Url, item.Hash)
		if shortID == "" {
			continue
		}
		if err := e.enrichOne(ctx, item, shortID); err != nil {
			slog.Debug("failed to enrich Lobste.rs item", "item_id", item.ID, "short_id", shortID, "error", err)
			errors++
			continue
		}
		enriched++
	}
	slog.Info("Lobste.rs bulk enrich", "enriched", enriched, "errors", errors, "total", len(items))
	return nil
}

type lobstersStory struct {
	ShortID      string            `json:"short_id"`
	Score        int               `json:"score"`
	CommentCount int               `json:"comment_count"`
	Comments     []lobstersComment `json:"comments"`
}

type lobstersComment struct {
	ShortID       string            `json:"short_id"`
	Comment       string            `json:"comment"`
	Score         int               `json:"score"`
	CommentingUser string           `json:"commenting_user"`
	Children      []lobstersComment `json:"children"`
}

func (e *LobstersEnricher) enrichOne(ctx context.Context, item *store.Item, shortID string) error {
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
			Author: c.CommentingUser,
			Text:   stripHTMLTags(c.Comment),
			Points: c.Score,
			Depth:  depth,
			URL:    fmt.Sprintf("https://lobste.rs/s/%s/%s", shortID, c.ShortID),
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
