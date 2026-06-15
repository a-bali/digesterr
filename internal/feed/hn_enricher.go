package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"digesterr/internal/store"
)

type HNEnricher struct {
	client  *http.Client
	baseURL string
}

func NewHNEnricher() *HNEnricher {
	return &HNEnricher{
		client:  &http.Client{Timeout: 10 * time.Second},
		baseURL: "https://hacker-news.firebaseio.com",
	}
}

func (e *HNEnricher) Enrich(ctx context.Context, item *store.Item) error {
	hnID := extractHNID(item.CommentUrl, item.Url)
	if hnID == 0 {
		return fmt.Errorf("could not extract HN item ID")
	}

	apiURL := fmt.Sprintf("%s/v0/item/%d.json", e.baseURL, hnID)
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
		ID          int    `json:"id"`
		Score       int    `json:"score"`
		Descendants int    `json:"descendants"`
		Title       string `json:"title"`
		URL         string `json:"url"`
		By          string `json:"by"`
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
