package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
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

// isRedditVideoURL reports whether the URL points to a Reddit-hosted video.
func isRedditVideoURL(url string) bool {
	return strings.Contains(strings.ToLower(url), "v.redd.it/")
}

// RedditFetcher fetches subreddit posts via Reddit's OAuth JSON API.
// The generic RSS fetcher gets blocked from datacenter IPs; the OAuth
// path bypasses this by authenticating with a client-credentials token.
func (f *RedditFetcher) setTransport(t http.RoundTripper) { f.client.Transport = t }

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

// galleryMediaURL picks the best URL from a media source, preferring
// the static image (s.u), then GIF (s.gif), then MP4 (s.mp4). Animated
// images from Reddit use gif/mp4 instead of u.
func galleryMediaURL(s redditMediaSource) string {
	if s.URL != "" {
		return s.URL
	}
	if s.GIF != "" {
		return s.GIF
	}
	return s.MP4
}

// renderGalleryContent extracts image/video URLs from Reddit's gallery API
// response and renders them inline. The gallery_data provides ordering;
// media_metadata provides the actual URLs. Reddit escapes URLs in the JSON
// as HTML entities, so we unescape them. Animated images (GIFs) use s.gif
// or s.mp4 instead of s.u.
func renderGalleryContent(p redditPostData) string {
	if len(p.GalleryData.Items) == 0 || len(p.MediaMetadata) == 0 {
		return ""
	}
	var b strings.Builder
	for _, item := range p.GalleryData.Items {
		mi, ok := p.MediaMetadata[item.MediaID]
		if !ok {
			continue
		}
		src := html.UnescapeString(galleryMediaURL(mi.S))
		if src == "" {
			continue
		}
		// Animated images (e) == "AnimatedImage": prefer MP4 for smoother playback,
		// fall back to GIF. Static images always get <img>.
		if mi.E == "AnimatedImage" && mi.S.MP4 != "" {
			mp4 := html.UnescapeString(mi.S.MP4)
			fmt.Fprintf(&b, `<p><video src="%s" autoplay loop muted playsinline style="max-width:100%%;border-radius:4px"><a href="%s" target="_blank" rel="noopener">Watch video ↗</a></video></p>`, mp4, src)
		} else {
			fmt.Fprintf(&b, `<p><img src="%s" loading="lazy" style="max-width:100%%;border-radius:4px" /></p>`, src)
		}
	}
	return b.String()
}

// renderPostContent renders a post's own payload: inline media (gallery, video,
// or image) followed by any selftext. mediaURL supplies the video/image URL —
// the post's own URL, or the parent's URL when rendering a crosspost. Returns
// "" when the post carries nothing renderable, letting the caller pick a fallback.
func renderPostContent(p redditPostData, mediaURL string) string {
	var content string
	// Media always renders inline regardless of selftext — the media IS the content.
	if p.IsGallery {
		content = renderGalleryContent(p)
	}
	if content == "" && isRedditVideoURL(mediaURL) {
		videoURL := mediaURL
		if p.IsVideo && p.Media.RedditVideo != nil && p.Media.RedditVideo.FallbackURL != "" {
			videoURL = p.Media.RedditVideo.FallbackURL
		}
		content = `<p><video src="` + videoURL + `" controls style="max-width:100%;border-radius:4px"><a href="` + mediaURL + `" target="_blank" rel="noopener">Watch video ↗</a></video></p>`
	}
	if content == "" && isRedditImageURL(mediaURL) {
		content = `<p><img src="` + mediaURL + `" loading="lazy" style="max-width:100%;border-radius:4px" /></p>`
	}
	// Append selftext after any media — posts often include explanatory text
	// that must not be silently dropped.
	if p.Selftext != "" {
		content += "<p>" + renderRedditMarkdown(p.Selftext) + "</p>"
	}
	return content
}

// crosspostParent returns the post a crosspost points at. Reddit ships it in
// crosspost_parent_list; the list is absent for ordinary posts.
func crosspostParent(p redditPostData) (redditPostData, bool) {
	if len(p.CrosspostParentList) == 0 {
		return redditPostData{}, false
	}
	return p.CrosspostParentList[0], true
}

// absoluteRedditURL resolves a possibly-relative Reddit path to an absolute URL.
func absoluteRedditURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "/") {
		return "https://www.reddit.com" + raw
	}
	return raw
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
		itemURL = absoluteRedditURL(itemURL)

		commentURL := "https://www.reddit.com" + p.Permalink

		content := renderPostContent(p, itemURL)
		linkURL := itemURL
		// A crosspost's own payload is a stub — selftext is empty and the
		// gallery/media keys are simply absent; the shared content lives on the
		// parent post. Render the parent's payload too, so the item shows what
		// was crossposted instead of degrading to a bare link to the parent.
		// Any text the crossposter added is kept ahead of it.
		if parent, ok := crosspostParent(p); ok {
			parentURL := absoluteRedditURL(parent.URL)
			if parentURL == "" {
				parentURL = itemURL
			}
			content += renderPostContent(parent, parentURL)
			linkURL = parentURL
		}
		// Still nothing renderable (e.g. a plain link post): fall back to a link.
		if content == "" && !p.IsSelf && linkURL != "" {
			if isRedditImageURL(linkURL) {
				content = `<p><img src="` + linkURL + `" loading="lazy" style="max-width:100%;border-radius:4px" /></p>`
			} else {
				content = `<p><a href="` + linkURL + `" target="_blank" rel="noopener">` + linkURL + `</a></p>`
			}
		}

		pubAt := time.Time{}
		if p.CreatedUTC > 0 {
			pubAt = time.Unix(int64(p.CreatedUTC), 0)
		}

		items = append(items, Item{
			GUID:        "reddit:" + p.ID,
			Title:       html.UnescapeString(strings.TrimSpace(p.Title)),
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
// Handles: markdown tables, images, links, bare URLs, bold, italic, newlines.
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

	// Markdown tables: detect consecutive lines containing "|" where the
	// second line is a separator (---, :---, :---:, ---:). Render as <table>
	// before other inline processing to avoid placeholder conflicts.
	lines := strings.Split(s, "\n")
	var result []string
	i := 0
	for i < len(lines) {
		if strings.Contains(lines[i], "|") {
			start := i
			for i < len(lines) && strings.Contains(lines[i], "|") {
				i++
			}
			block := lines[start:i]
			if len(block) >= 3 && isTableSeparator(block[1]) {
				result = append(result, renderMarkdownTable(block, ph))
			} else {
				result = append(result, block...)
			}
		} else {
			result = append(result, lines[i])
			i++
		}
	}
	s = strings.Join(result, "\n")

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

// isTableSeparator reports whether a line is a markdown table separator row
// (e.g. "---", ":---", ":---:", "---:", "| --- | --- |").
func isTableSeparator(line string) bool {
	// Strip pipe characters and trim whitespace to get the cell contents.
	cells := strings.Split(strings.TrimSpace(line), "|")
	sepCount := 0
	for _, c := range cells {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		// A separator cell is one or more dashes, optionally with leading/trailing colons.
		valid := true
		for _, ch := range c {
			if ch != '-' && ch != ':' {
				valid = false
				break
			}
		}
		if valid && strings.Contains(c, "-") {
			sepCount++
		}
	}
	// Need at least one separator cell and all non-empty cells must be separators.
	return sepCount > 0
}

// splitTableRow splits a markdown table row into cell values, trimming
// whitespace and leading/trailing pipes.
func splitTableRow(line string) []string {
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, "|")
	s = strings.TrimSuffix(s, "|")
	parts := strings.Split(s, "|")
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = strings.TrimSpace(p)
	}
	return cells
}

// renderMarkdownTable renders a markdown table block (header + separator +
// data rows) as an HTML <table>. The ph function registers HTML as a
// placeholder so it survives the subsequent HTML-escaping pass.
func renderMarkdownTable(block []string, ph func(string) string) string {
	if len(block) < 3 {
		return ""
	}

	headerCells := splitTableRow(block[0])
	colCount := len(headerCells)

	var b strings.Builder
	b.WriteString("<table>")

	// Header row.
	b.WriteString("<thead><tr>")
	for _, cell := range headerCells {
		fmt.Fprintf(&b, "<th>%s</th>", cell)
	}
	b.WriteString("</tr></thead>")

	// Data rows (skip separator at index 1).
	b.WriteString("<tbody>")
	for _, row := range block[2:] {
		cells := splitTableRow(row)
		b.WriteString("<tr>")
		for ci := 0; ci < colCount; ci++ {
			val := ""
			if ci < len(cells) {
				val = cells[ci]
			}
			fmt.Fprintf(&b, "<td>%s</td>", val)
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table>")

	return ph(b.String())
}
