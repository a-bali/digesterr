package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"digesterr/internal/store"
)

type redditReplies struct {
	Data struct {
		Children []redditChild `json:"children"`
	} `json:"data"`
}

func (r *redditReplies) UnmarshalJSON(b []byte) error {
	if string(b) == `""` {
		return nil
	}
	type alias redditReplies
	return json.Unmarshal(b, (*alias)(r))
}

// redditMediaSource is a single resolution variant inside media_metadata.
// Static images have "u" (URL); animated images have "gif" and "mp4" instead.
type redditMediaSource struct {
	URL string `json:"u"`
	GIF string `json:"gif"`
	MP4 string `json:"mp4"`
}

// redditMediaItem represents one gallery image/video entry from media_metadata.
type redditMediaItem struct {
	Status string           `json:"status"`
	E      string           `json:"e"` // media type: "Image", "AnimatedImage", "RedditVideo"
	M      string           `json:"m"` // MIME type
	S      redditMediaSource `json:"s"`
}

// redditGalleryItem is one entry in gallery_data.items (determines display order).
type redditGalleryItem struct {
	MediaID string `json:"media_id"`
}

// redditVideo holds the video metadata from Reddit's API media.reddit_video.
type redditVideo struct {
	FallbackURL string `json:"fallback_url"`
	Height      int    `json:"height"`
	Width       int    `json:"width"`
	IsGif       bool   `json:"is_gif"`
}

// redditMedia holds the media object from a Reddit post.
type redditMedia struct {
	RedditVideo *redditVideo `json:"reddit_video"`
}

type redditPostData struct {
	ID          string        `json:"id"`
	Score       int           `json:"score"`
	NumComments int           `json:"num_comments"`
	URL         string        `json:"url"`
	Author      string        `json:"author"`
	Title       string        `json:"title"`
	Body        string        `json:"body"`
	Permalink   string        `json:"permalink"`
	Depth       int           `json:"depth"`
	Replies     redditReplies `json:"replies"`
	// Post-specific fields (used by RedditFetcher, ignored by enricher)
	Selftext     string                          `json:"selftext"`
	CreatedUTC   float64                         `json:"created_utc"`
	IsSelf       bool                            `json:"is_self"`
	Over18       bool                            `json:"over_18"`
	Stickied     bool                            `json:"stickied"`
	ScoreHidden  bool                            `json:"score_hidden"`
	IsVideo      bool                            `json:"is_video"`
	IsGallery    bool                            `json:"is_gallery"`
	Media        redditMedia                     `json:"media"`
	MediaMetadata map[string]redditMediaItem     `json:"media_metadata"`
	GalleryData  struct {
		Items []redditGalleryItem `json:"items"`
	} `json:"gallery_data"`
	// CrosspostParentList carries the POST being crossposted. A crosspost's own
	// payload is a stub (empty selftext, no media keys); the renderable body
	// lives here. Only populated for crossposts, so it is a no-op elsewhere.
	CrosspostParentList []redditPostData `json:"crosspost_parent_list"`
}

type redditChild struct {
	Kind string         `json:"kind"`
	Data redditPostData `json:"data"`
}

type redditListing struct {
	Data struct {
		Children []redditChild `json:"children"`
	} `json:"data"`
}

type RedditEnricher struct {
	client       *http.Client
	baseURL      string
	apiBaseURL   string
	queries      *store.Queries
	clientID     string
	clientSecret string
	token        string
	tokenExpiry  time.Time
	tokenMu      sync.Mutex
	limiter      *rate.Limiter
	userAgent    string
}

func NewRedditEnricher(queries *store.Queries) *RedditEnricher {
	return &RedditEnricher{
		client:       &http.Client{Timeout: 10 * time.Second},
		baseURL:      "https://www.reddit.com",
		apiBaseURL:   "https://oauth.reddit.com",
		queries:      queries,
		clientID:     os.Getenv("REDDIT_CLIENT_ID"),
		clientSecret: os.Getenv("REDDIT_CLIENT_SECRET"),
		limiter:      rate.NewLimiter(defaultRedditRate, defaultRedditBurst),
		userAgent:    DefaultUserAgent,
	}
}

// SetRateLimiter overrides the API call limiter (nil disables limiting).
func (e *RedditEnricher) SetRateLimiter(l *rate.Limiter) { e.limiter = l }

// SetCredentials overrides the Reddit API credentials. The merged config
// (file + env) is applied by the server after construction; the constructor
// keeps the env fallback for direct use in tests.
func (e *RedditEnricher) SetCredentials(clientID, clientSecret string) {
	e.clientID = clientID
	e.clientSecret = clientSecret
}

// SetUserAgent overrides the User-Agent header for this enricher.
func (e *RedditEnricher) SetUserAgent(ua string) { e.userAgent = ua }

func (e *RedditEnricher) ensureToken(ctx context.Context) error {
	e.tokenMu.Lock()
	if e.token != "" && time.Now().Before(e.tokenExpiry) {
		e.tokenMu.Unlock()
		return nil
	}
	e.tokenMu.Unlock()

	if e.clientID == "" || e.clientSecret == "" {
		slog.Error("Reddit API credentials not configured (reddit.client_id / REDDIT_CLIENT_ID) — enrichment will fail")
		return nil
	}

	apiURL := fmt.Sprintf("%s/api/v1/access_token", e.baseURL)
	body := strings.NewReader("grant_type=client_credentials&scope=read")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, body)
	if err != nil {
		return fmt.Errorf("creating token request: %w", err)
	}
	req.SetBasicAuth(e.clientID, e.clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", e.userAgent)

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching Reddit access token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Reddit token endpoint returned status %d: %s", resp.StatusCode, string(bodyBytes))
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

	e.tokenMu.Lock()
	e.token = tokenResp.AccessToken
	e.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	e.tokenMu.Unlock()

	slog.Debug("obtained new Reddit OAuth token", "expires_in", tokenResp.ExpiresIn)
	return nil
}

func (e *RedditEnricher) Enrich(ctx context.Context, item *store.Item) error {
	postID := extractRedditPostID(item.Hash, item.CommentUrl, item.Url)
	if postID == "" {
		return fmt.Errorf("could not extract Reddit post ID")
	}

	if err := e.ensureToken(ctx); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	// raw_json=1 keeps string fields unescaped. Without it Reddit
	// HTML-escapes the payload, so an ampersand in a title or URL arrives as
	// &amp; and every later escape pass corrupts it further.
	apiURL := fmt.Sprintf("%s/comments/%s.json?limit=1&raw_json=1", e.apiBaseURL, postID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return fmt.Errorf("creating Reddit API request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	req.Header.Set("User-Agent", e.userAgent)

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

	// Never regress a known score to 0: a 0 from any endpoint means
	// "unknown here" (vote fuzzing), not "the post is worthless".
	applyScore(item, int64(post.Score))
	item.CommentsCount = int64(post.NumComments)

	if post.Permalink != "" {
		item.CommentUrl = "https://www.reddit.com" + post.Permalink
	}

	if post.URL != "" && !strings.Contains(post.URL, "reddit.com") {
		if err := e.queries.UpdateItemURL(ctx, struct {
			Url string `json:"url"`
			ID  int64  `json:"id"`
		}{Url: post.URL, ID: item.ID}); err != nil {
			slog.Warn("failed to update Reddit item URL", "item_id", item.ID, "item_url", item.Url, "error", err)
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

	if err := e.ensureToken(ctx); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if err := waitLimiter(ctx, e.limiter); err != nil {
		return fmt.Errorf("rate limiter: %w", err)
	}

	apiURL := fmt.Sprintf("%s/api/info.json?id=%s&raw_json=1", e.apiBaseURL, strings.Join(ids, ","))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return fmt.Errorf("creating Reddit API request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	req.Header.Set("User-Agent", e.userAgent)

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
		// Never regress a known score to 0: a 0 from any endpoint means
		// "unknown here" (vote fuzzing), not "the post is worthless".
		applyScore(item, int64(child.Data.Score))
		item.CommentsCount = int64(child.Data.NumComments)
		if child.Data.Permalink != "" {
			item.CommentUrl = "https://www.reddit.com" + child.Data.Permalink
		}
		if child.Data.URL != "" && !strings.Contains(child.Data.URL, "reddit.com") {
			if err := e.queries.UpdateItemURL(ctx, struct {
				Url string `json:"url"`
				ID  int64  `json:"id"`
			}{Url: child.Data.URL, ID: item.ID}); err != nil {
				slog.Warn("failed to update Reddit item URL", "item_id", item.ID, "item_url", item.Url, "error", err)
			} else {
				item.Url = child.Data.URL
			}
		}
	}
	slog.Debug("reddit bulk enrichment completed", "requested", len(ids), "matched", matched)
	if matched == 0 {
		slog.Warn("reddit bulk enrich matched no items — API response may be empty or rate-limited", "requested", len(ids))
	}
	return nil
}

// redditCommentFetchLimit caps the comment tree fetch. Reddit's /comments
// endpoint accepts limit up to 100 and applies it to top-level comments (their
// reply subtrees are included). An uncapped fetch downloads the whole tree —
// multi-MB on busy threads — and its default "confidence" sort is not score
// order. sort=top biases the window toward high-scoring top-level comments;
// the caller re-sorts by raw score afterwards.
const redditCommentFetchLimit = 100

func (e *RedditEnricher) setTransport(t http.RoundTripper) { e.client.Transport = t }
func (e *RedditEnricher) CommentTargetURL() string { return e.apiBaseURL }

func (e *RedditEnricher) FetchComments(ctx context.Context, item store.Item, maxComments int) ([]Comment, error) {
	postID := extractRedditPostID(item.Hash, item.CommentUrl, item.Url)
	if postID == "" {
		return nil, fmt.Errorf("could not extract Reddit post ID")
	}

	var listings []redditListing
	var lastErr error

	// Try OAuth endpoint first, fall back to public endpoint.
	// Note: Reddit's client_credentials grant returns score=1 for all comments,
	// hiding real vote counts. The public endpoint returns real scores but may
	// be blocked from datacenter IPs.
	commentsURL := func(base string) string {
		// raw_json=1 keeps comment bodies unescaped: without it Reddit sends
		// &amp; inside bodies, which the reader then escapes again and
		// displays literally as "&amp;" (verified live on r/stocks threads).
		return fmt.Sprintf("%s/comments/%s.json?sort=top&threaded=true&limit=%d&raw_json=1", base, postID, redditCommentFetchLimit)
	}
	for _, attempt := range []struct {
		url  string
		auth bool
	}{
		{commentsURL(e.apiBaseURL), true},
		{commentsURL(e.baseURL), false},
	} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, attempt.url, nil)
		if err != nil {
			return nil, fmt.Errorf("creating Reddit API request: %w", err)
		}
		req.Header.Set("User-Agent", e.userAgent)
		if attempt.auth {
			if err := e.ensureToken(ctx); err != nil {
				lastErr = fmt.Errorf("auth: %w", err)
				continue
			}
			req.Header.Set("Authorization", "Bearer "+e.token)
		}

		resp, err := e.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("fetching Reddit comments for %s: %w", postID, err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("Reddit API returned status %d for post %s", resp.StatusCode, postID)
			continue
		}

		if err := json.NewDecoder(resp.Body).Decode(&listings); err != nil {
			resp.Body.Close()
			lastErr = fmt.Errorf("decoding Reddit comments response: %w", err)
			continue
		}
		resp.Body.Close()

		if len(listings) < 2 {
			listings = nil
			lastErr = nil
		}
		break
	}

	if lastErr != nil {
		return nil, lastErr
	}
	if listings == nil {
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
		if child.Kind == "t1" {
			for _, reply := range child.Data.Replies.Data.Children {
				flattenRedditComment(reply, depth+1, out)
			}
		}
		return
	}
	commentURL := ""
	if child.Data.Permalink != "" {
		commentURL = "https://www.reddit.com" + child.Data.Permalink
	}
	*out = append(*out, Comment{
		ID:          0,
		Author:      child.Data.Author,
		Text:        child.Data.Body,
		Points:      child.Data.Score,
		ValidPoints: !child.Data.ScoreHidden && child.Data.Score > 0,
		Depth:       depth,
		URL:         commentURL,
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
