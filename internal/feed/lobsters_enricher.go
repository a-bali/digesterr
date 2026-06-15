package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
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
	shortID := extractLobstersShortID(item.CommentUrl, item.Url, item.GUID)
	if shortID == "" {
		return fmt.Errorf("could not extract Lobste.rs short ID")
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
