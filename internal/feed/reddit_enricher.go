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

type redditPostData struct {
	ID          string `json:"id"`
	Score       int    `json:"score"`
	NumComments int    `json:"num_comments"`
	URL         string `json:"url"`
	Author      string `json:"author"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	Permalink   string `json:"permalink"`
	Depth       int    `json:"depth"`
	Replies     struct {
		Data struct {
			Children []redditChild `json:"children"`
		} `json:"data"`
	} `json:"replies"`
}

type redditChild struct {
	Kind string        `json:"kind"`
	Data redditPostData `json:"data"`
}

type redditListing struct {
	Data struct {
		Children []redditChild `json:"children"`
	} `json:"data"`
}

type RedditEnricher struct {
	client  *http.Client
	baseURL string
	queries *store.Queries
}

func NewRedditEnricher(queries *store.Queries) *RedditEnricher {
	return &RedditEnricher{
		client:  &http.Client{Timeout: 10 * time.Second},
		baseURL: "https://www.reddit.com",
		queries: queries,
	}
}

func (e *RedditEnricher) Enrich(ctx context.Context, item *store.Item) error {
	postID := extractRedditPostID(item.Hash, item.CommentUrl, item.Url)
	if postID == "" {
		return fmt.Errorf("could not extract Reddit post ID")
	}

	apiURL := fmt.Sprintf("%s/comments/%s.json?limit=1", e.baseURL, postID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return fmt.Errorf("creating Reddit API request: %w", err)
	}
	req.Header.Set("User-Agent", "Digesterr/1.0 (by u/dre_is)")

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching Reddit post %s: %w", postID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Reddit API returned status %d for post %s", resp.StatusCode, postID)
	}

	var listings []redditListing
	if err := json.NewDecoder(resp.Body).Decode(&listings); err != nil {
		return fmt.Errorf("decoding Reddit API response: %w", err)
	}
	if len(listings) == 0 || len(listings[0].Data.Children) == 0 {
		return fmt.Errorf("Reddit post %s not found", postID)
	}

	post := listings[0].Data.Children[0].Data
	if post.ID == "" {
		return fmt.Errorf("Reddit post %s not found", postID)
	}

	item.Points = int64(post.Score)
	item.CommentsCount = int64(post.NumComments)

	if post.URL != "" && !strings.Contains(post.URL, "reddit.com") {
		if err := e.queries.UpdateItemURL(ctx, struct {
			Url string `json:"url"`
			ID  int64  `json:"id"`
		}{Url: post.URL, ID: item.ID}); err != nil {
			slog.Warn("failed to update Reddit item URL", "item_id", item.ID, "error", err)
		} else {
			item.Url = post.URL
		}
	}

	return nil
}

func (e *RedditEnricher) BulkEnrich(ctx context.Context, items []*store.Item) error {
	if len(items) == 0 {
		return nil
	}

	// Build comma-separated t3_ IDs: t3_1tlh5aj,t3_1u5v9pd
	var ids []string
	idToItem := make(map[string]*store.Item)
	for _, item := range items {
		postID := extractRedditPostID(item.Hash, item.CommentUrl, item.Url)
		if postID == "" {
			continue
		}
		fullName := "t3_" + postID
		ids = append(ids, fullName)
		idToItem[postID] = item
	}
	if len(ids) == 0 {
		slog.Debug("reddit bulk enrich: no items with extractable post IDs")
		return nil
	}

	apiURL := fmt.Sprintf("%s/api/info.json?id=%s", e.baseURL, strings.Join(ids, ","))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return fmt.Errorf("creating Reddit API request: %w", err)
	}
	req.Header.Set("User-Agent", "Digesterr/1.0 (by u/dre_is)")

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching Reddit bulk info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Reddit API returned status %d for bulk info", resp.StatusCode)
	}

	var info struct {
		Data struct {
			Children []redditChild `json:"children"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return fmt.Errorf("decoding Reddit info response: %w", err)
	}

	matched := 0
	for _, child := range info.Data.Children {
		if child.Kind != "t3" {
			continue
		}
		item, ok := idToItem[child.Data.ID]
		if !ok {
			continue
		}
		matched++
		item.Points = int64(child.Data.Score)
		item.CommentsCount = int64(child.Data.NumComments)
		if child.Data.URL != "" && !strings.Contains(child.Data.URL, "reddit.com") {
			if err := e.queries.UpdateItemURL(ctx, struct {
				Url string `json:"url"`
				ID  int64  `json:"id"`
			}{Url: child.Data.URL, ID: item.ID}); err != nil {
				slog.Warn("failed to update Reddit item URL", "item_id", item.ID, "error", err)
			} else {
				item.Url = child.Data.URL
			}
		}
	}
	slog.Debug("reddit bulk enrich", "requested", len(ids), "matched", matched)
	if matched == 0 {
		slog.Warn("reddit bulk enrich matched no items — API response may be empty or rate-limited")
	}
	return nil
}

func (e *RedditEnricher) FetchComments(ctx context.Context, item store.Item, maxComments int) ([]Comment, error) {
	postID := extractRedditPostID(item.Hash, item.CommentUrl, item.Url)
	if postID == "" {
		return nil, fmt.Errorf("could not extract Reddit post ID")
	}

	apiURL := fmt.Sprintf("%s/comments/%s.json", e.baseURL, postID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating Reddit API request: %w", err)
	}
	req.Header.Set("User-Agent", "Digesterr/1.0 (by u/dre_is)")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching Reddit comments for %s: %w", postID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Reddit API returned status %d for post %s", resp.StatusCode, postID)
	}

	var listings []redditListing
	if err := json.NewDecoder(resp.Body).Decode(&listings); err != nil {
		return nil, fmt.Errorf("decoding Reddit comments response: %w", err)
	}
	if len(listings) < 2 {
		return nil, nil
	}

	var comments []Comment
	for _, child := range listings[1].Data.Children {
		flattenRedditComment(child, 0, &comments)
	}
	sort.Slice(comments, func(i, j int) bool { return comments[i].Points > comments[j].Points })

	if maxComments > 0 && len(comments) > maxComments {
		comments = comments[:maxComments]
	}
	return comments, nil
}

func flattenRedditComment(child redditChild, depth int, out *[]Comment) {
	if child.Kind != "t1" || child.Data.Body == "" {
		return
	}
	*out = append(*out, Comment{
		Author: child.Data.Author,
		Text:   child.Data.Body,
		Points: child.Data.Score,
		Depth:  depth,
	})
	for _, reply := range child.Data.Replies.Data.Children {
		flattenRedditComment(reply, depth+1, out)
	}
}

func extractRedditPostID(ids ...string) string {
	for _, s := range ids {
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "t3_") {
			if id := strings.TrimPrefix(s, "t3_"); id != "" {
				return id
			}
		}
		u, err := url.Parse(s)
		if err != nil {
			continue
		}
		if !strings.Contains(u.Host, "reddit.com") {
			continue
		}
		parts := strings.SplitN(path.Clean(u.Path), "/", -1)
		for i, p := range parts {
			if p == "comments" && i+1 < len(parts) && parts[i+1] != "" {
				return parts[i+1]
			}
		}
	}
	return ""
}
