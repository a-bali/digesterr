package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	urlpkg "net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"digesterr/internal/linkify"
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
	_, ok := linkify.RedditVideoMediaID(url)
	return ok
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
	// quotaMu guards the last rate-limit headers Reddit advertised. Reddit
	// meters the OAuth client process-wide over a shared window, so this is
	// intentionally shared across every subreddit feed.
	quotaMu        sync.Mutex
	quotaUsed      int
	quotaRemaining int
	quotaReset     time.Time
	quotaCheckedAt time.Time
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

// quotaHeaderInt reads an integer-valued rate-limit header that Reddit may
// format as a float. Reddit really does send X-Ratelimit-Remaining as
// "999.0"; a plain strconv.Atoi on that fails and silently left the quota
// unmeasured, so the metrics dialog read "no response observed yet" forever.
// Parse as a float and truncate when Atoi does not apply.
func quotaHeaderInt(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	if n, err := strconv.Atoi(raw); err == nil {
		return n, true
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return int(f), true
	}
	return 0, false
}

// recordQuotaHeaders stores the rate-limit headers Reddit returns on every
// API response. Reddit advertises the remaining budget for the shared OAuth
// window (x-ratelimit-remaining) alongside the seconds until it resets, so
// tracking it costs nothing extra. It never reports an absolute limit, so the
// window size is derived from used + remaining.
func (f *RedditFetcher) recordQuotaHeaders(h http.Header) {
	remaining, ok := quotaHeaderInt(h.Get("X-Ratelimit-Remaining"))
	if !ok {
		return
	}
	used, _ := quotaHeaderInt(h.Get("X-Ratelimit-Used"))
	resetSeconds, err := strconv.ParseFloat(strings.TrimSpace(h.Get("X-Ratelimit-Reset")), 64)
	if err != nil || resetSeconds < 0 {
		resetSeconds = 0
	}

	f.quotaMu.Lock()
	f.quotaUsed = used
	f.quotaRemaining = remaining
	if resetSeconds > 0 {
		f.quotaReset = time.Now().Add(time.Duration(resetSeconds * float64(time.Second)))
	}
	f.quotaCheckedAt = time.Now()
	f.quotaMu.Unlock()
}

// QuotaSnapshot reports the last observed Reddit rate-limit state for the
// metrics API. reserve is the headroom kept for ingestion; BudgetLow is true
// once the remaining budget falls below it.
func (f *RedditFetcher) QuotaSnapshot(reserve int) QuotaSnapshot {
	f.quotaMu.Lock()
	used, remaining := f.quotaUsed, f.quotaRemaining
	reset, checkedAt := f.quotaReset, f.quotaCheckedAt
	f.quotaMu.Unlock()

	s := QuotaSnapshot{
		Provider:  "reddit",
		Measured:  !checkedAt.IsZero(),
		Limit:     used + remaining,
		Remaining: remaining,
		Reserve:   reserve,
	}
	if s.Measured {
		s.Reset, s.CheckedAt = &reset, &checkedAt
		s.BudgetLow = reserve > 0 && remaining <= reserve
	}
	return s
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
	f.recordQuotaHeaders(resp.Header)

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

	apiURL := fmt.Sprintf("%s/r/%s/new.json?limit=%d&raw_json=1", f.apiBaseURL, subreddit, f.limit)
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
	f.recordQuotaHeaders(resp.Header)

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

// SubredditName resolves a subreddit feed URL to its display name in the
// "r/<name>" form (e.g. "r/golang"), taken from Reddit's own about endpoint.
// Used to prefill a feed's name when the user only supplies the subreddit.
func (f *RedditFetcher) SubredditName(ctx context.Context, feedURL string) (string, error) {
	subreddit := extractSubreddit(feedURL)
	if subreddit == "" {
		return "", fmt.Errorf("could not extract subreddit from %q", feedURL)
	}

	if err := f.ensureToken(ctx); err != nil {
		return "", fmt.Errorf("auth: %w", err)
	}

	if err := waitLimiter(ctx, f.limiter); err != nil {
		return "", fmt.Errorf("rate limit: %w", err)
	}

	// raw_json=1 keeps the name unescaped (no &amp; etc. in display_name).
	apiURL := fmt.Sprintf("%s/r/%s/about.json?raw_json=1", f.apiBaseURL, subreddit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("User-Agent", f.userAgent)

	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching subreddit %s: %w", subreddit, err)
	}
	defer resp.Body.Close()
	f.recordQuotaHeaders(resp.Header)

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Reddit API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	var about struct {
		Data struct {
			DisplayNamePrefixed string `json:"display_name_prefixed"`
			DisplayName         string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &about); err != nil {
		return "", fmt.Errorf("decoding subreddit about response: %w", err)
	}
	name := strings.TrimSpace(about.Data.DisplayNamePrefixed)
	if name == "" && about.Data.DisplayName != "" {
		name = "r/" + strings.TrimSpace(about.Data.DisplayName)
	}
	if name == "" {
		return "", fmt.Errorf("subreddit %q has no display name", subreddit)
	}
	return name, nil
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
		// fall back to GIF. Static images always get <img>. URLs come from the
		// Reddit API payload (untrusted): scheme-check and escape before
		// interpolating, or a crafted media URL breaks out of the attribute.
		// All media URLs here are absolute https CDN links, so the absolute-URL
		// variant applies.
		if mi.E == "AnimatedImage" && mi.S.MP4 != "" {
			mp4 := html.UnescapeString(mi.S.MP4)
			mp4Safe, srcSafe := safeRedditURL(mp4), safeRedditURL(src)
			if mp4Safe == "" || srcSafe == "" {
				continue
			}
			fmt.Fprintf(&b, `<p><video src="%s" autoplay loop muted playsinline style="max-width:100%%;border-radius:4px"><a href="%s" target="_blank" rel="noopener noreferrer">Watch video ↗</a></video></p>`, redditAttrEscape(mp4Safe), redditAttrEscape(srcSafe))
		} else {
			srcSafe := safeRedditURL(src)
			if srcSafe == "" {
				continue
			}
			fmt.Fprintf(&b, `<p><img src="%s" loading="lazy" style="max-width:100%%;border-radius:4px" /></p>`, redditAttrEscape(srcSafe))
		}
	}
	return b.String()
}

// redditVideoFallback returns the playable MP4 for a Reddit video post, looking
// at the post's own media and — for a crosspost — at the parent's, because that
// is where Reddit puts media.reddit_video when the video was crossposted: the
// crosspost itself carries media=null and is_video=false. The depth is bounded
// (a crosspost is one level) but a nesting guard keeps a malformed payload from
// recursing.
func redditVideoFallback(p redditPostData) string {
	if video := redditVideoMetadata(p); video != nil {
		return video.FallbackURL
	}
	return ""
}

// Resolve URL and GIF flag from the same payload, including crossposts.
func redditVideoMetadata(p redditPostData) *redditVideo {
	const maxDepth = 3
	for depth := 0; depth < maxDepth; depth++ {
		if p.Media.RedditVideo != nil && p.Media.RedditVideo.FallbackURL != "" {
			return p.Media.RedditVideo
		}
		if len(p.CrosspostParentList) == 0 {
			return nil
		}
		p = p.CrosspostParentList[0]
	}
	return nil
}

// renderPostMedia renders a post's inline media: gallery, video, or image.
// Returns "" when the payload carries nothing renderable. A video is only
// rendered when a playable MP4 exists — the bare v.redd.it/<id> URL answers with
// a redirect to the post's HTML page, so a <video> pointed at it is a player
// that can never start; the caller degrades to a link instead.
func renderPostMedia(p redditPostData, mediaURL string) string {
	if p.IsGallery {
		if g := renderGalleryContent(p); g != "" {
			return g
		}
	}
	if isRedditVideoURL(mediaURL) {
		fallback := redditVideoFallback(p)
		fallbackURL, err := urlpkg.Parse(fallback)
		if err != nil {
			return ""
		}
		if id, ok := linkify.RedditVideoMediaID(fallback); ok && strings.HasSuffix(fallbackURL.Path, ".mp4") {
			if video := redditVideoMetadata(p); video != nil && video.IsGif {
				return `<p><video data-reddit-gif src="` + redditAttrEscape(fallback) + `" controls style="max-width:100%;border-radius:4px"></video><a href="` + redditAttrEscape(fallback) + `" target="_blank" rel="noopener noreferrer">Watch video ↗</a></p>`
			}
			player := linkify.CommentVideoHTML(id)
			// Retain the API's known progressive rendition after the audio master.
			player = strings.Replace(player, `</video>`, `<source src="`+redditAttrEscape(fallback)+`" type="video/mp4" /></video>`, 1)
			return `<p>` + player + `</p>`
		}
		return ""
	}
	if isRedditImageURL(mediaURL) {
		if mediaSafe := safeRedditURL(mediaURL); mediaSafe != "" {
			return `<p><img src="` + redditAttrEscape(mediaSafe) + `" loading="lazy" style="max-width:100%;border-radius:4px" /></p>`
		}
	}
	return ""
}

// renderPostSelftext renders a post's own body text after any media. A post
// often includes explanatory text that must not be silently dropped.
//
// The body is NOT wrapped in a paragraph. renderRedditMarkdown already emits
// complete top-level blocks — paragraphs included — so adding a wrapper would
// produce <p><p>text</p><h2>…</h2></p>: invalid markup that the browser
// repairs by hoisting the headings and lists out of the paragraph.
func renderPostSelftext(p redditPostData) string {
	if p.Selftext == "" {
		return ""
	}
	return renderRedditMarkdown(p.Selftext)
}

// renderMediaOrParent renders the playable media for a post, following a
// crosspost to its parent when the post's own payload has none. Reddit strips
// the media keys from a crosspost and keeps them on the parent (verified live:
// outer media=null/is_video=false, crosspost_parent_list[0].media.reddit_video
// present), so a video post's fallback_url only exists one level down.
//
// Exactly ONE payload contributes media: rendering the stub's media as well
// produced two players for a single video.
func renderMediaOrParent(p redditPostData, mediaURL string) string {
	if m := renderPostMedia(p, mediaURL); m != "" {
		return m
	}
	if parent, ok := crosspostParent(p); ok {
		parentURL := absoluteRedditURL(parent.URL)
		if parentURL == "" {
			parentURL = mediaURL
		}
		return renderPostMedia(parent, parentURL)
	}
	return ""
}

// renderPostContent renders a post's own payload — media plus selftext. Used
// where the crosspost chain is irrelevant (comments, tests).
// Returns "" when the post carries nothing renderable, letting the caller pick a
// fallback.
func renderPostContent(p redditPostData, mediaURL string) string {
	return renderMediaOrParent(p, mediaURL) + renderPostSelftext(p)
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

		content := renderMediaOrParent(p, itemURL)
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
			// The MEDIA comes from exactly one payload — see renderMediaOrParent.
			if content == "" {
				content = renderPostMedia(parent, parentURL)
			}
			content += renderPostSelftext(parent)
			linkURL = parentURL
		}
		content = renderPostSelftext(p) + content

		// Still nothing renderable (e.g. a plain link post): fall back to a link.
		// linkURL is an absolute post/media URL (possibly attacker-influenced via
		// the payload): escape and scheme-check before interpolating. Absolute
		// links only — a relative path would resolve against the reader.
		if content == "" && !p.IsSelf && linkURL != "" {
			if linkSafe := safeRedditURL(linkURL); linkSafe != "" {
				if isRedditImageURL(linkSafe) {
					content = `<p><img src="` + redditAttrEscape(linkSafe) + `" loading="lazy" style="max-width:100%;border-radius:4px" /></p>`
				} else {
					content = `<p><a href="` + redditAttrEscape(linkSafe) + `" target="_blank" rel="noopener noreferrer">` + redditTextEscape(linkSafe) + `</a></p>`
				}
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

// redditTextEscape escapes user-derived text for use as HTML element content.
// It must be applied *before* the fragment is stashed in a placeholder: the
// escaping pass at the end of renderRedditMarkdown only covers the
// non-placeholder remainder, so anything already inside a placeholder would
// otherwise reach the page raw.
func redditTextEscape(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	).Replace(s)
}

// redditAttrEscape escapes user-derived text for a double-quoted HTML attribute
// value. Quotes and apostrophes are included so the value cannot close the
// attribute and inject new markup.
func redditAttrEscape(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	).Replace(s)
}

// safeMarkdownURL returns the URL when it is safe to place in an href/src
// attribute, and "" when it is not. Absolute http(s) and mailto targets are
// allowed; javascript:, data: and similar would execute in the reader, since
// this HTML is inserted with v-html. Scheme-less relative links ("", "/path",
// "#frag") are safe to render but cannot be judged here — they resolve against
// the document base, which differs per caller (newsletter archive vs reddit) —
// so they pass through unchanged and each caller decides whether its base makes
// them meaningful. The URL must not contain whitespace or the quote /
// angle-bracket characters that would break out of a double-quoted attribute,
// even when the caller escapes afterwards.
func safeMarkdownURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.ContainsAny(trimmed, " \t\r\n\"'<>") {
		return ""
	}
	u, err := urlpkg.Parse(trimmed)
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "", "http", "https", "mailto":
		return trimmed
	default:
		return ""
	}
}

// safeRedditURL is safeMarkdownURL restricted to URLs that resolve meaningfully
// from the digesterr origin: only absolute http(s)/mailto targets. Reddit
// selftext can contain scheme-less relative paths ("/r/foo"), which would
// resolve against the reader's own host (the SPA) rather than reddit, so they
// degrade to plain text instead of becoming broken links.
func safeRedditURL(raw string) string {
	v := safeMarkdownURL(raw)
	if v == "" {
		return ""
	}
	lower := strings.ToLower(v)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "mailto:") {
		return v
	}
	return ""
}

// redditEscapablePunctChars are the ASCII punctuation characters CommonMark
// lets a backslash escape.
const redditEscapablePunctChars = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"

// redditEscapablePunct reports whether c is a character a backslash escapes.
func redditEscapablePunct(c byte) bool {
	return strings.IndexByte(redditEscapablePunctChars, c) >= 0
}

// redditEscapedMarkChars are the escaped characters that must be hidden from
// the later inline passes, because those passes would otherwise read the
// resolved character as the syntax it was escaped to avoid: `\*x\*` must not
// become emphasis, `\[x](u)` must not become a link. Everything else escapable
// is emitted as the plain character, which matters for URLs — Reddit slugs carry
// escaped underscores (`oc\_aki`), and hiding those in a placeholder makes the
// destination unparseable and drops the link.
const redditEscapedMarkChars = "`*~![]()"

// redditEscapedMark reports whether resolving an escaped c needs it stashed in a
// placeholder rather than emitted as plain text.
func redditEscapedMark(c byte) bool {
	return strings.IndexByte(redditEscapedMarkChars, c) >= 0
}

// redditUnescapeBackslashes resolves CommonMark backslash escapes in one block of
// Reddit text.
//
// Reddit stores selftext with the escapes verbatim, so an author writing
// `warm\_compaction` (to keep the underscore literal) sends the backslash too.
// Without this pass the reader shows `warm\_compaction`. Rules: a backslash
// before ASCII punctuation yields that character alone; a backslash before
// anything else is a literal backslash (`\a` stays `\a`).
//
// emit receives the escaped character and returns its replacement. The inline
// pass uses it to stash the characters the mark passes would misread (see
// redditEscapedMark) while emitting the rest verbatim.
func redditUnescapeBackslashes(text string, emit func(byte) string) string {
	if !strings.Contains(text, `\`) {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' && i+1 < len(text) && redditEscapablePunct(text[i+1]) {
			b.WriteString(emit(text[i+1]))
			i++
			continue
		}
		b.WriteByte(text[i])
	}
	return b.String()
}

// renderRedditInline renders the INLINE marks inside one block of Reddit text:
// code spans, images, links, bare URLs, emphasis and strikethrough. Block
// structure (headings, lists, quotes, fences, tables, rules) is handled by
// renderRedditMarkdown in reddit_markdown.go, which calls this once per block.
//
// All HTML-producing elements are extracted to placeholders before escaping,
// then restored after — so user content in URLs/text stays safe.
func renderRedditInline(text string) string {
	type placeholder struct{ html string }
	var phs []placeholder

	ph := func(html string) string {
		idx := len(phs)
		phs = append(phs, placeholder{html: html})
		return fmt.Sprintf("\x00PH_%d\x00", idx)
	}

	s := text

	// An escaped backtick is a LITERAL backtick, not a code-span delimiter.
	// Stash it before the code-span pass so it cannot open or close a span;
	// resolving it here (rather than in the general escape pass below) is what
	// keeps `\`code\`` from pairing into a span it was escaped to avoid.
	s = strings.ReplaceAll(s, "\\`", ph(redditTextEscape("`")))

	// Code spans run before the general escape pass, so their content is
	// verbatim: CommonMark does not apply backslash escapes inside a code span,
	// and a technical post's `` `C:\Users` `` or `` `\d+` `` must keep the
	// backslash. Nothing else inside backticks is read as markdown either:
	// `**x**` is a code sample, not bold, and `<b>` in a sample is escaped
	// rather than dropped. A span ends at the next backtick — deliberately the
	// original single-backtick rule, not CommonMark's double-backtick escaping.
	reCode := regexp.MustCompile("`([^`\n]+)`")
	s = reCode.ReplaceAllStringFunc(s, func(m string) string {
		return ph("<code>" + redditTextEscape(reCode.FindStringSubmatch(m)[1]) + "</code>")
	})

	// Strip fence remnants that the block layer did not pair (a mid-line fence,
	// or an odd marker inside a line). Not doing this leaves literal ``` in the
	// stored body, which reads to the user as garbage.
	s = strings.ReplaceAll(s, "```", "")
	s = strings.ReplaceAll(s, "~~~", "")

	// CommonMark backslash escapes, resolved before every remaining mark that
	// can read a punctuation character as syntax. Reddit stores the escape
	// verbatim in selftext, so an author writing `warm\_compaction` (to keep the
	// underscore literal) sends the backslash too, and without this pass the
	// reader shows the literal `\_` — the reported r/hermesagent bug. Resolving
	// before the emphasis/link passes is also what makes escaping SUPPRESS the
	// mark it escapes: `\*text\*` must not reach the emphasis pass as `*text*`.
	//
	// The characters those passes would misread are stashed in placeholders
	// (redditEscapedMark); the rest — underscores, dashes, dots — are emitted as
	// plain text, which URLs need: Reddit slugs carry escapes like `oc\_aki`,
	// and stashing those would make the destination unparseable and drop the
	// link.
	s = redditUnescapeBackslashes(s, func(c byte) string {
		if redditEscapedMark(c) {
			return ph(redditTextEscape(string(c)))
		}
		return string(c)
	})

	// Markdown images: ![alt](url)
	reImg := regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
	s = reImg.ReplaceAllStringFunc(s, func(m string) string {
		parts := reImg.FindStringSubmatch(m)
		src := safeRedditURL(parts[2])
		if src == "" {
			// Unsafe scheme (or a relative path that would resolve against
			// the reader): drop the image but keep the alt text readable.
			return ph(redditTextEscape(parts[1]))
		}
		return ph(`<img src="` + redditAttrEscape(src) + `" alt="` + redditAttrEscape(parts[1]) + `" loading="lazy" style="max-width:100%;border-radius:4px" />`)
	})

	// Markdown links: [text](url). The label is optional: Reddit authors write
// `[](url)` to attach a bare image or link, and requiring a label left the
// literal `[](...)` in the body for the bare-URL pass to then linkify inside
// its parentheses, producing `[](<a href="...">...`). An empty label falls
// back to the URL itself, matching internal/linkify on the other surfaces.
	reLink := regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)
	s = reLink.ReplaceAllStringFunc(s, func(m string) string {
		parts := reLink.FindStringSubmatch(m)
		label := parts[1]
		href := safeRedditURL(parts[2])
		if href == "" {
			// Unsafe scheme (or a relative path that would resolve against
			// the reader): keep the label as plain text, no link. With no
			// label there is nothing to keep, so leave the construct as
			// written rather than deleting it.
			if strings.TrimSpace(label) == "" {
				return m
			}
			return ph(redditTextEscape(label))
		}
		if strings.TrimSpace(label) == "" {
			label = href
		}
		return ph(`<a href="` + redditAttrEscape(href) + `" target="_blank" rel="noopener">` + redditTextEscape(label) + `</a>`)
	})

	// Bare image URLs (i.redd.it, preview.redd.it)
	reBareImg := regexp.MustCompile(`https?://(?:i\.redd\.it|preview\.redd\.it)/\S+\.(?:png|jpe?g|gif|webp)(?:\?\S*)?`)
	s = reBareImg.ReplaceAllStringFunc(s, func(m string) string {
		return ph(`<img src="` + redditAttrEscape(m) + `" loading="lazy" style="max-width:100%;border-radius:4px" />`)
	})

	// Bare URLs (http/https, not inside href/src attributes). The character
	// class excludes `*`, so a URL written in emphasis (`**https://x**`) does
	// not absorb the closing marker into its href. That single character is the
	// difference between `<strong><a href="https://x">x</a></strong>` and a
	// link whose href and label both end in `**` — the emphasis pass runs after
	// this one, so it can only wrap what is left here, never repair it.
	reURL := regexp.MustCompile(`(https?://[^\s<>"\)\]\*]+)`)
	s = reURL.ReplaceAllStringFunc(s, func(m string) string {
		// Skip URLs that look like they're inside an already-processed tag
		// (preceded by a quote or = which indicates an attribute value)
		return ph(`<a href="` + redditAttrEscape(m) + `" target="_blank" rel="noopener">` + redditTextEscape(m) + `</a>`)
	})

	// Strikethrough: ~~text~~. Runs before the emphasis passes so a `~~` pair is
	// not left for the italic rule to read as a single `*`-style marker.
	reStrike := regexp.MustCompile(`~~([^~\n]+)~~`)
	s = reStrike.ReplaceAllStringFunc(s, func(m string) string {
		parts := reStrike.FindStringSubmatch(m)
		return ph("<del>" + redditTextEscape(parts[1]) + "</del>")
	})

	// Bold: **text**. The replacement is registered through ph and the inner
	// text is only escaped, never re-registered: nesting the marker inside the
	// generated <strong> is what lets an inner element (an image or link
	// placeholder) survive. See the restore loop for why.
	reBold := regexp.MustCompile(`\*\*([^*]+)\*\*`)
	s = reBold.ReplaceAllStringFunc(s, func(m string) string {
		parts := reBold.FindStringSubmatch(m)
		return ph(`<strong>` + redditTextEscape(parts[1]) + `</strong>`)
	})

	// Italic: *text* (after bold is processed, remaining single asterisks).
	reItalic := regexp.MustCompile(`\*([^*\n]+)\*`)
	s = reItalic.ReplaceAllStringFunc(s, func(m string) string {
		parts := reItalic.FindStringSubmatch(m)
		return ph(`<em>` + redditTextEscape(parts[1]) + `</em>`)
	})

	// HTML escape everything
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\n", "<br />")

	// Restore placeholders, INNERMOST FIRST (reverse creation order).
	//
	// The emphasis passes above wrap a marker rather than re-registering it, so
	// a tag can be nested inside a later one: `**![alt](u)**` leaves the image
	// as PH_0 and the <strong> wrapper as PH_1, whose html embeds the PH_0
	// marker. Restoring in creation order would run PH_0 before that marker
	// existed in s, leaving a literal NUL in the body — which is exactly how
	// `🔗 <strong>\x00PH_0\x00</strong>` reached the database for 24 items.
	// Reverse order inserts the outer wrapper first and then substitutes the
	// inner markers inside it.
	//
	// ReplaceAll, not Replace(..., 1): each placeholder is unique and now
	// restored exactly once, so either works — ReplaceAll is used to state the
	// invariant outright, since a placeholder surviving restoration is what a
	// reader sees as garbage.
	for i := len(phs) - 1; i >= 0; i-- {
		s = strings.ReplaceAll(s, fmt.Sprintf("\x00PH_%d\x00", i), phs[i].html)
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
		fmt.Fprintf(&b, "<th>%s</th>", redditTextEscape(cell))
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
			fmt.Fprintf(&b, "<td>%s</td>", redditTextEscape(val))
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table>")

	return ph(b.String())
}
