package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"digesterr/internal/store"
)

// Reddit JSON API returns an array of two listings: post data, then comments.
type redditListing struct {
	Data struct {
		Children []struct {
			Data struct {
				ID          string `json:"id"`
				Score       int    `json:"score"`
				NumComments int    `json:"num_comments"`
				URL         string `json:"url"`
			} `json:"data"`
		} `json:"children"`
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

	// Reddit RSS <link> points to the discussion page, not the article.
	// Fix the URL from the API response.
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

func extractRedditPostID(ids ...string) string {
	for _, s := range ids {
		if s == "" {
			continue
		}
		// Strip t3_ prefix from fullname like "t3_1tlh5aj"
		if strings.HasPrefix(s, "t3_") {
			if id := strings.TrimPrefix(s, "t3_"); id != "" {
				return id
			}
		}
		// Parse from URL: /comments/{post_id}/
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
