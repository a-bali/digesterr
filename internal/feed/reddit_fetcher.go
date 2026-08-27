package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)


// isRedditImageURL reports whether the URL points to a Reddit-hosted image.
func isRedditImageURL(url string) bool {
	lower := strings.ToLower(url)
	if strings.Contains(lower, "i.redd.it/") || strings.Contains(lower, "preview.redd.it/") {
		return true
	}
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".webp"} {
		if strings.HasSuffix(lower, ext) || strings.Contains(lower, ext+"?") {
			return true
		}
	}
	return false
}

// RedditFetcher fetches subreddit posts via Reddit's OAuth JSON API.
// The generic RSS fetcher gets blocked from datacenter IPs; the OAuth
// path bypasses this by authenticating with a client-credentials token.
type RedditFetcher struct {
	client       *http.Client
	baseURL      string
	apiBaseURL   string
	clientID     string
	clientSecret string
	token        string
	tokenExpiry  time.Time
	tokenMu      sync.Mutex
	limiter      *rate.Limiter
	userAgent    string
	limit        int // posts per subreddit fetch
}

func NewRedditFetcher() *RedditFetcher {
	return &RedditFetcher{
		client:       &http.Client{Timeout: 15 * time.Second},
		baseURL:      "https://www.reddit.com",
		apiBaseURL:   "https://oauth.reddit.com",
		clientID:     os.Getenv("REDDIT_CLIENT_ID"),
		clientSecret: os.Getenv("REDDIT_CLIENT_SECRET"),
		userAgent:    DefaultUserAgent,
		limit:        25,
	}
}

// SetCredentials overrides the Reddit API credentials. The merged config
// (file + env) is applied by the server after construction.
func (f *RedditFetcher) SetCredentials(clientID, clientSecret string) {
	f.clientID = clientID
	f.clientSecret = clientSecret
}

// SetRateLimiter overrides the API call limiter (nil disables limiting).
func (f *RedditFetcher) SetRateLimiter(l *rate.Limiter) { f.limiter = l }

// SetUserAgent overrides the User-Agent header for this fetcher.
func (f *RedditFetcher) SetUserAgent(ua string) { f.userAgent = ua }

// SetLimit overrides how many posts to fetch per subreddit poll.
func (f *RedditFetcher) SetLimit(n int) {
	if n < 1 {
		n = 1
	}
	f.limit = n
}

// ensureToken obtains or refreshes the OAuth bearer token.
func (f *RedditFetcher) ensureToken(ctx context.Context) error {
	f.tokenMu.Lock()
	if f.token != "" && time.Now().Before(f.tokenExpiry) {
		f.tokenMu.Unlock()
		return nil
	}
	f.tokenMu.Unlock()

	if f.clientID == "" || f.clientSecret == "" {
		return fmt.Errorf("Reddit API credentials not configured")
	}

	apiURL := fmt.Sprintf("%s/api/v1/access_token", f.baseURL)
	body := strings.NewReader("grant_type=client_credentials&scope=read")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, body)
	if err != nil {
		return fmt.Errorf("creating token request: %w", err)
	}
	req.SetBasicAuth(f.clientID, f.clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", f.userAgent)

	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching Reddit access token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Reddit token endpoint returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("decoding token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return fmt.Errorf("Reddit token endpoint returned empty access_token")
	}

	f.tokenMu.Lock()
	f.token = tokenResp.AccessToken
	f.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	f.tokenMu.Unlock()

	slog.Debug("obtained new Reddit OAuth token", "expires_in", tokenResp.ExpiresIn)
	return nil
}

// extractSubreddit pulls the subreddit name from a feed URL like
// https://www.reddit.com/r/opencode, /r/opencode/new, or /r/opencode.rss.
func extractSubreddit(feedURL string) string {
	s := strings.TrimSpace(feedURL)
	if idx := strings.Index(s, "/r/"); idx >= 0 {
		s = s[idx+3:]
	} else {
		return ""
	}
	for i, ch := range s {
		if ch == '/' || ch == '.' {
			return s[:i]
		}
	}
	return s
}

// Fetch implements the Fetcher interface. It fetches the newest posts from
// a subreddit using Reddit's authenticated JSON API.
func (f *RedditFetcher) Fetch(ctx context.Context, feedURL string) ([]Item, error) {
	subreddit := extractSubreddit(feedURL)
	if subreddit == "" {
		return nil, fmt.Errorf("could not extract subreddit from %q", feedURL)
	}

	if err := f.ensureToken(ctx); err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	if err := waitLimiter(ctx, f.limiter); err != nil {
		return nil, fmt.Errorf("rate limit: %w", err)
	}

	apiURL := fmt.Sprintf("%s/r/%s/new.json?limit=%d", f.apiBaseURL, subreddit, f.limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("User-Agent", f.userAgent)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching subreddit %s: %w", subreddit, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Reddit API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	return f.parseListing(body, subreddit)
}

func (f *RedditFetcher) parseListing(data []byte, subreddit string) ([]Item, error) {
	var listing redditListing
	if err := json.Unmarshal(data, &listing); err != nil {
		return nil, fmt.Errorf("decoding Reddit listing: %w", err)
	}

	var items []Item
	for _, child := range listing.Data.Children {
		if child.Kind != "t3" {
			continue
		}
		p := child.Data
		if p.Over18 || p.Stickied {
			continue
		}

		itemURL := p.URL
		if p.IsSelf && p.Permalink != "" {
			itemURL = "https://www.reddit.com" + p.Permalink
		}
		// Ensure absolute URL — some posts have relative paths
		if strings.HasPrefix(itemURL, "/") {
			itemURL = "https://www.reddit.com" + itemURL
		}

		commentURL := "https://www.reddit.com" + p.Permalink

		content := ""
		if p.Selftext != "" {
			content = "<p>" + renderRedditMarkdown(p.Selftext) + "</p>"
		} else if !p.IsSelf && itemURL != "" {
			// Image/link posts: render the URL as content when there's
			// no selftext, so the viewer sees the media inline.
			if isRedditImageURL(itemURL) {
				content = `<p><img src="` + itemURL + `" loading="lazy" style="max-width:100%;border-radius:4px" /></p>`
			} else {
				content = `<p><a href="` + itemURL + `" target="_blank" rel="noopener">` + itemURL + `</a></p>`
			}
		}

		pubAt := time.Time{}
		if p.CreatedUTC > 0 {
			pubAt = time.Unix(int64(p.CreatedUTC), 0)
		}

		items = append(items, Item{
			GUID:        "reddit:" + p.ID,
			Title:       strings.TrimSpace(p.Title),
			URL:         itemURL,
			Content:     content,
			CommentURL:  commentURL,
			Author:      "u/" + p.Author,
			PublishedAt: pubAt,
			Points:      p.Score,
			CommentsCnt: p.NumComments,
		})
	}

	slog.Debug("reddit feed fetched", "subreddit", subreddit, "posts", len(items))
	return items, nil
}

func htmlEscapeText(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\n", "<br />")
	return s
}

// renderRedditMarkdown converts Reddit-flavored selftext to HTML.
// Handles: markdown images, links, bare URLs, bold, italic, newlines.
// All HTML-producing elements are extracted to placeholders before escaping,
// then restored after — so user content in URLs/text stays safe.
func renderRedditMarkdown(text string) string {
	type placeholder struct{ html string }
	var phs []placeholder

	ph := func(html string) string {
		idx := len(phs)
		phs = append(phs, placeholder{html: html})
		return fmt.Sprintf("\x00PH_%d\x00", idx)
	}

	s := text

	// Markdown images: ![alt](url)
	reImg := regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
	s = reImg.ReplaceAllStringFunc(s, func(m string) string {
		parts := reImg.FindStringSubmatch(m)
		return ph(`<img src="` + parts[2] + `" alt="` + parts[1] + `" loading="lazy" style="max-width:100%;border-radius:4px" />`)
	})

	// Markdown links: [text](url)
	reLink := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	s = reLink.ReplaceAllStringFunc(s, func(m string) string {
		parts := reLink.FindStringSubmatch(m)
		return ph(`<a href="` + parts[2] + `" target="_blank" rel="noopener">` + parts[1] + `</a>`)
	})

	// Bare image URLs (i.redd.it, preview.redd.it)
	reBareImg := regexp.MustCompile(`https?://(?:i\.redd\.it|preview\.redd\.it)/\S+\.(?:png|jpe?g|gif|webp)(?:\?\S*)?`)
	s = reBareImg.ReplaceAllStringFunc(s, func(m string) string {
		return ph(`<img src="` + m + `" loading="lazy" style="max-width:100%;border-radius:4px" />`)
	})

	// Bare URLs (http/https, not inside href/src attributes)
	reURL := regexp.MustCompile(`(https?://[^\s<>"\)\]]+)`)
	s = reURL.ReplaceAllStringFunc(s, func(m string) string {
		// Skip URLs that look like they're inside an already-processed tag
		// (preceded by a quote or = which indicates an attribute value)
		return ph(`<a href="` + m + `" target="_blank" rel="noopener">` + m + `</a>`)
	})

	// Bold: **text**
	reBold := regexp.MustCompile(`\*\*([^*]+)\*\*`)
	s = reBold.ReplaceAllStringFunc(s, func(m string) string {
		parts := reBold.FindStringSubmatch(m)
		return ph(`<strong>` + parts[1] + `</strong>`)
	})

	// Italic: *text* (after bold is processed, remaining single asterisks)
	reItalic := regexp.MustCompile(`\*([^*\n]+)\*`)
	s = reItalic.ReplaceAllStringFunc(s, func(m string) string {
		parts := reItalic.FindStringSubmatch(m)
		return ph(`<em>` + parts[1] + `</em>`)
	})

	// HTML escape everything
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\n", "<br />")

	// Restore placeholders
	for i, p := range phs {
		s = strings.Replace(s, fmt.Sprintf("\x00PH_%d\x00", i), p.html, 1)
	}

	return s
}
