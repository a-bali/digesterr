package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"digesterr/internal/store"
)

// Twitter's public web-client bearer token. It is not a secret: it is
// embedded in the JavaScript bundle served to every browser session and is
// the same token Nitter uses. Session authorization comes from the ct0 +
// auth_token cookies instead.
const twitterPublicBearer = "AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs%3D1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA"

// GraphQL query hashes. Twitter pins client versions by these hashes; when
// X ships a new query version the old hash starts returning 404 "Query not
// found" and the constants must be bumped to the hashes used by the current
// web client (visible in x.com's JS bundle or Nitter's source).
const (
	twitterUserByScreenNameQuery = "G3KGOASz96M-Qu0nwmGXNg/UserByScreenName"
	twitterUserTweetsQuery       = "E3opETHurmVJflFsUBVuUQ/UserTweets"
	twitterTweetDetailQuery      = "XMOz5h24KAZ86qKffKTLdQ/TweetDetail"
)


const twitterFeatures = `{"rweb_tipjar_consumption_enabled":true,"responsive_web_graphql_exclude_directive_enabled":true,"verified_phone_label_enabled":false,"creator_subscriptions_tweet_preview_api_enabled":true,"responsive_web_graphql_timeline_navigation_enabled":true,"responsive_web_graphql_skip_user_profile_image_extensions_enabled":false,"communities_web_enable_tweet_community_results_fetch":true,"articles_preview_enabled":true,"longform_notetweets_inline_media_enabled":true,"articles_preview_20251118_enabled":true,"rweb_video_screen_enabled":false,"responsive_web_edit_tweet_api_enabled":true,"tweetypie_unmention_optimization_enabled":true,"responsive_text_only_conversations_in_reply_tab_enabled":true,"freedom_of_speech_not_reach_fetch_enabled":true,"standardized_nudges_misinfo":true,"tweet_with_visibility_results_prefer_gql_limited_actions_policy_enabled":true,"longform_notetweets_rich_text_read_enabled":true,"longform_notetweets_creation_enabled":true,"profile_label_editable":true,"immersive_video_status_ring_live_survey":false,"content_disclosure_indicator_enabled":true,"super_follow_badge_enabled":true,"subscriptions_verification_info_verified_since_enabled":true}`

// TwitterFetcher fetches a user's tweets via Twitter/X's internal GraphQL
// API, authenticated with browser session cookies (ct0 CSRF token + auth_token).
type TwitterFetcher struct {
	client    *http.Client
	baseURL   string
	ct0       string
	authToken string
	bearer    string
	limiter   *rate.Limiter
	userAgent string
	count     int // tweets per request (max 20)
}

func NewTwitterFetcher() *TwitterFetcher {
	return &TwitterFetcher{
		client:    &http.Client{Timeout: 15 * time.Second},
		baseURL:   "https://x.com",
		bearer:    twitterPublicBearer,
		limiter:   rate.NewLimiter(defaultTwitterRate, defaultTwitterBurst),
		userAgent: DefaultUserAgent,
		count:     20,
	}
}

// SetRateLimiter overrides the GraphQL call limiter (nil disables limiting).
func (f *TwitterFetcher) SetRateLimiter(l *rate.Limiter) { f.limiter = l }

// SetUserAgent overrides the User-Agent header for this fetcher.
func (f *TwitterFetcher) SetUserAgent(ua string) { f.userAgent = ua }

// SetCount overrides how many tweets to fetch per request (max 20).
func (f *TwitterFetcher) SetCount(n int) {
	if n < 1 {
		n = 1
	}
	if n > 20 {
		n = 20
	}
	f.count = n
}

// Enrich implements the Enricher interface via EnrichFeed (single item).
func (f *TwitterFetcher) Enrich(ctx context.Context, item *store.Item) error {
	return f.EnrichFeed(ctx, item.CommentUrl, []*store.Item{item})
}

// EnrichFeed re-fetches a user timeline and updates scores (favorites) and
// reply counts for matching items in-place. One GraphQL request covers all
// tweets still visible on the timeline; older items keep their last known
// scores.
func (f *TwitterFetcher) EnrichFeed(ctx context.Context, feedURL string, items []*store.Item) error {
	screenName := extractTwitterScreenName(feedURL)
	if screenName == "" {
		screenName = extractTwitterScreenName(itemSourceURL(items))
	}
	if screenName == "" {
		return fmt.Errorf("could not extract Twitter screen name")
	}
	userID, err := f.userID(ctx, screenName)
	if err != nil {
		return fmt.Errorf("resolving user %s: %w", screenName, err)
	}
	variables := fmt.Sprintf(`{"userId":%q,"count":%d,"includePromotedContent":false,"withQuickPromoteEligibilityTelemetry":true,"withVoice":true}`, userID, f.count)
	body, err := f.gqlGet(ctx, twitterUserTweetsQuery, variables)
	if err != nil {
		return err
	}
	tweets, err := parseUserTweets(body)
	if err != nil {
		return err
	}
	byID := make(map[string]*twitterTweetResult, len(tweets))
	for _, t := range tweets {
		byID[t.RestID] = t
	}
	matched := 0
	for _, item := range items {
		tweetID := extractTweetID(item.Hash, item.CommentUrl, item.Url)
		if tweetID == "" {
			continue
		}
		t, ok := byID[tweetID]
		if !ok || t.RestID == "" {
			continue // no longer on the timeline — keep last known scores
		}
		item.Points = int64(t.Legacy.FavoriteCount)
		item.CommentsCount = int64(t.Legacy.ReplyCount)
		matched++
	}
	slog.Debug("twitter enrichment completed", "screen_name", screenName, "requested", len(items), "matched", matched)
	return nil
}

// itemSourceURL returns the first non-empty item URL as a heuristic source
// for screen-name extraction when the feed URL itself is unavailable.
func itemSourceURL(items []*store.Item) string {
	for _, it := range items {
		for _, u := range []string{it.Url, it.CommentUrl} {
			if u != "" && (strings.Contains(u, "x.com") || strings.Contains(u, "twitter.com")) {
				return u
			}
		}
	}
	return ""
}

// SetCredentials overrides the session cookies (and optionally the bearer
// token). The merged config (file + env) is applied by the server after
// construction.
func (f *TwitterFetcher) SetCredentials(ct0, authToken string) {
	f.ct0 = ct0
	f.authToken = authToken
}

// extractTwitterScreenName pulls the @handle from a feed URL like
// https://x.com/elonmusk, https://twitter.com/elonmusk, or a bare handle.
func extractTwitterScreenName(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = strings.TrimPrefix(s, "@")
	if strings.Contains(s, "://") {
		// URL form: must be x.com or twitter.com with a non-empty path.
		u, err := url.Parse(s)
		if err != nil || u.Host == "" ||
			(!strings.Contains(u.Host, "twitter.com") && !strings.Contains(u.Host, "x.com")) {
			return ""
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			return ""
		}
		s = parts[0]
	}
	s = strings.TrimSuffix(s, "/")
	if s == "" || strings.ContainsAny(s, "/ ?#") {
		return ""
	}
	// Bare-handle form: must be a plausible Twitter handle.
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
			return ""
		}
	}
	return s
}

// gqlGet performs an authenticated GET against the GraphQL endpoint.
func (f *TwitterFetcher) gqlGet(ctx context.Context, queryHash string, variables string) ([]byte, error) {
	if err := waitLimiter(ctx, f.limiter); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}
	if f.ct0 == "" || f.authToken == "" {
		return nil, fmt.Errorf("Twitter credentials not configured (twitter.ct0 / TWITTER_CT0, twitter.auth_token / TWITTER_AUTH_TOKEN)")
	}
	u := fmt.Sprintf("%s/i/api/graphql/%s?variables=%s&features=%s",
		f.baseURL, queryHash, url.QueryEscape(variables), url.QueryEscape(twitterFeatures))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+f.bearer)
	req.Header.Set("Cookie", fmt.Sprintf("ct0=%s; auth_token=%s", f.ct0, f.authToken))
	req.Header.Set("X-CSRF-Token", f.ct0)
	req.Header.Set("X-Twitter-Active-User", "yes")
	req.Header.Set("X-Twitter-Client-Language", "en")
	req.Header.Set("User-Agent", f.userAgent)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching GraphQL %s: %w", queryHash, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Twitter API returned status %d for %s: %s", resp.StatusCode, queryHash, truncateForLog(body))
	}
	return body, nil
}

func truncateForLog(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// Fetch implements Fetcher. The feedURL holds the account handle or profile
// URL; each tweet becomes one item.
func (f *TwitterFetcher) Fetch(ctx context.Context, feedURL string) ([]Item, error) {
	screenName := extractTwitterScreenName(feedURL)
	if screenName == "" {
		return nil, fmt.Errorf("could not extract Twitter screen name from %q", feedURL)
	}

	userID, err := f.userID(ctx, screenName)
	if err != nil {
		return nil, err
	}

	variables := fmt.Sprintf(`{"userId":%q,"count":%d,"includePromotedContent":false,"withQuickPromoteEligibilityTelemetry":true,"withVoice":true}`, userID, f.count)
	body, err := f.gqlGet(ctx, twitterUserTweetsQuery, variables)
	if err != nil {
		return nil, err
	}

	tweets, err := parseUserTweets(body)
	if err != nil {
		return nil, err
	}

	items := make([]Item, 0, len(tweets))
	for _, t := range tweets {
		items = append(items, t.item(screenName))
	}
	slog.Debug("twitter feed fetched", "screen_name", screenName, "tweets", len(items))
	return items, nil
}

// userID resolves a screen name to Twitter's numeric user ID.
func (f *TwitterFetcher) userID(ctx context.Context, screenName string) (string, error) {
	variables := fmt.Sprintf(`{"screen_name":%q,"withGrokTranslatedBio":false}`, screenName)
	body, err := f.gqlGet(ctx, twitterUserByScreenNameQuery, variables)
	if err != nil {
		return "", err
	}
	var resp struct {
		Data struct {
			User struct {
				Result struct {
					ID     string `json:"rest_id"`
					Legacy struct {
						ScreenName string `json:"screen_name"`
					} `json:"legacy"`
				} `json:"result"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("decoding UserByScreenName response: %w", err)
	}
	if resp.Data.User.Result.ID == "" {
		return "", fmt.Errorf("Twitter user %q not found", screenName)
	}
	return resp.Data.User.Result.ID, nil
}

// twitterTweetResult mirrors the subset of a TweetResults GraphQL node the
// fetcher needs. Retweets wrap the original tweet in
// legacy.retweeted_status_result; quoted tweets appear under
// quoted_status_result and are rendered as a prefixed excerpt.
type twitterTweetResult struct {
	RestID string             `json:"rest_id"`
	Legacy twitterTweetLegacy `json:"legacy"`
	Note   *struct {
		NoteTweet struct {
			Text string `json:"text"`
		} `json:"note_tweet"`
	} `json:"note_tweet"`
	Quoted *twitterTweetWrapper `json:"quoted_status_result"`
	// RetweetedBy holds the feed account's screen name when this entry is a
	// retweet (not part of the API payload; set by parseUserTweets).
	RetweetedBy string `json:"-"`
	// LinkedBy holds the screen name of the account whose tweet linked to
	// this one, when the entry was reached via a link-only tweet (not part
	// of the API payload; set by parseUserTweets).
	LinkedBy string `json:"-"`
	// QuotedBy holds the screen name of the quoting account when this tweet
	// was reached through an empty-text quote wrapper (not part of the API
	// payload; set by item()).
	QuotedBy string `json:"-"`
	// Pinned marks the profile's pinned tweet (not part of the API payload;
	// set by parseUserTweets).
	Pinned bool `json:"-"`
	// linkCandidates holds every tweet node from the same response payload
	// (not part of the API payload; set by parseUserTweets) so link-only
	// tweets can be resolved to their target tweet.
	linkCandidates []*twitterTweetResult `json:"-"`
	Core           struct {
		UserResults struct {
			Result struct {
				Legacy struct {
					ScreenName string `json:"screen_name"`
				} `json:"legacy"`
				// Modern (non-legacy) user shape: screen_name sits directly
				// on the user result's core object.
				Core struct {
					ScreenName string `json:"screen_name"`
				} `json:"core"`
			} `json:"result"`
		} `json:"user_results"`
	} `json:"core"`
}

// author returns the tweet author's screen name, handling both response
// shapes: legacy (screen_name nested in legacy) and modern (screen_name
// directly on core).
func (t *twitterTweetResult) author() string {
	if n := t.Core.UserResults.Result.Legacy.ScreenName; n != "" {
		return n
	}
	return t.Core.UserResults.Result.Core.ScreenName
}

type twitterMedia struct {
	Type          string `json:"type"`
	URLTCo        string `json:"url"`
	MediaURLHTTPS string `json:"media_url_https"`
	VideoInfo     *struct {
		Variants []struct {
			ContentType string `json:"content_type"`
			Bitrate     int64  `json:"bitrate"`
			URL         string `json:"url"`
		} `json:"variants"`
	} `json:"video_info"`
}

type twitterURLEntity struct {
	URL         string `json:"url"`          // t.co short link as it appears in full_text
	ExpandedURL string `json:"expanded_url"` // real destination
	DisplayURL  string `json:"display_url"`
}

type twitterTweetLegacy struct {
	FullText            string               `json:"full_text"`
	CreatedAt           string               `json:"created_at"`
	FavoriteCount       int                  `json:"favorite_count"`
	RetweetCount        int                  `json:"retweet_count"`
	ReplyCount          int                  `json:"reply_count"`
	InReplyToStatusID   string               `json:"in_reply_to_status_id_str"`
	InReplyToScreenName string               `json:"in_reply_to_screen_name"`
	RetweetedStatus     *twitterTweetWrapper `json:"retweeted_status_result"`
	Entities            struct {
		Media []twitterMedia     `json:"media"`
		URLs  []twitterURLEntity `json:"urls"`
	} `json:"entities"`
	// Photos, videos and GIFs live in extended_entities; entities.media only
	// carries the first attachment.
	ExtendedEntities struct {
		Media []twitterMedia `json:"media"`
	} `json:"extended_entities"`
}

// media returns the tweet's attachments: extended_entities when present,
// falling back to entities.
func (t *twitterTweetLegacy) media() []twitterMedia {
	if len(t.ExtendedEntities.Media) > 0 {
		return t.ExtendedEntities.Media
	}
	return t.Entities.Media
}

// displayText returns full_text with t.co links replaced by their real
// destinations. When embedMedia is true, t.co links that wrap this tweet's
// own media attachments are dropped — the embedded image/video already IS
// the link target and keeping the raw t.co URL would duplicate it.
func (t *twitterTweetLegacy) displayText(body string, embedMedia bool) string {
	return expandLinks(body, t, embedMedia)
}

// expandLinks replaces t.co URLs in a tweet body with their expanded
// destinations per the tweet's entities. Media links are dropped when the
// media is embedded.
func expandLinks(body string, leg *twitterTweetLegacy, embedMedia bool) string {
	repl := make([]string, 0, (len(leg.Entities.URLs)+len(leg.media()))*4)
	// Media attachments: X often leaves the raw t.co media link in
	// full_text with NO entities.urls entry. When the media is embedded,
	// drop those links outright; otherwise expand them to the image URL.
	for _, m := range leg.media() {
		if m.URLTCo == "" || !strings.Contains(body, m.URLTCo) {
			continue
		}
		if embedMedia {
			repl = append(repl, m.URLTCo, "")
		} else if m.MediaURLHTTPS != "" {
			repl = append(repl, m.URLTCo, `<a href="`+htmlEscaped(m.MediaURLHTTPS)+`">`+htmlEscaped(m.MediaURLHTTPS)+`</a>`)
		}
	}
	for _, u := range leg.Entities.URLs {
		if u.URL == "" || !strings.Contains(body, u.URL) {
			continue
		}
		if embedMedia && leg.isMediaLink(u) {
			repl = append(repl, u.URL, "")
			continue
		}
		target := u.ExpandedURL
		if target == "" {
			target = u.DisplayURL
		}
		if target == "" {
			continue
		}
		label := target
		if strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "http://") {
			label = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(target, "https://"), "http://"), "/")
		}
		repl = append(repl, u.URL, `<a href="`+htmlEscaped(target)+`">`+htmlEscaped(label)+`</a>`)
	}
	if len(repl) > 0 {
		body = strings.NewReplacer(repl...).Replace(body)
	}
	return body
}

// isMediaLink reports whether a t.co URL entity wraps one of this tweet's
// own media attachments. X lists the media link both in entities.urls and
// entities.media with the same t.co URL.
func (t *twitterTweetLegacy) isMediaLink(u twitterURLEntity) bool {
	for _, m := range t.Entities.Media {
		if m.URLTCo == u.URL && u.URL != "" {
			return true
		}
	}
	return false
}

type twitterTweetWrapper struct {
	Result twitterTweetResult `json:"result"`
}

// twitterTimeline mirrors the UserTweets response envelope. Depending on
// client version the timeline sits under result.timeline (v1) or
// result.timeline_v2; both are decoded.
type twitterTimeline struct {
	Data struct {
		User struct {
			Result struct {
				Core struct {
					UserResults struct {
						Result struct {
							Legacy struct {
								ScreenName string `json:"screen_name"`
							} `json:"legacy"`
							Core struct {
								ScreenName string `json:"screen_name"`
							} `json:"core"`
						} `json:"result"`
					} `json:"user_results"`
				} `json:"core"`
				TimelineV2 *struct {
					Timeline struct {
						Instructions []twitterInstruction `json:"instructions"`
					} `json:"timeline"`
				} `json:"timeline_v2"`
				Timeline *struct {
					Timeline struct {
						Instructions []twitterInstruction `json:"instructions"`
					} `json:"timeline"`
				} `json:"timeline"`
			} `json:"result"`
		} `json:"user"`
	} `json:"data"`
}

type twitterInstruction struct {
	Type    string         `json:"type"`
	Entries []twitterEntry `json:"entries"`
	Entry   *twitterEntry  `json:"entry"` // pinned tweets arrive as a single entry
}

type twitterEntry struct {
	EntryID string `json:"entryId"`
	Content struct {
		ItemContent struct {
			TweetResults struct {
				Result twitterTweetResult `json:"result"`
			} `json:"tweet_results"`
		} `json:"itemContent"`
		Items []struct { // conversation modules wrap tweets in items[]
			Item struct {
				ItemContent struct {
					TweetResults struct {
						Result twitterTweetResult `json:"result"`
					} `json:"tweet_results"`
				} `json:"itemContent"`
			} `json:"item"`
		} `json:"items"`
	} `json:"content"`
}

// parseUserTweets extracts top-level tweets from a UserTweets response,
// unwrapping retweets to their original tweet.
func parseUserTweets(body []byte) ([]*twitterTweetResult, error) {
	var tl twitterTimeline
	if err := json.Unmarshal(body, &tl); err != nil {
		return nil, fmt.Errorf("decoding UserTweets response: %w", err)
	}
	res := tl.Data.User.Result
	var instructions []twitterInstruction
	switch {
	case res.TimelineV2 != nil:
		instructions = res.TimelineV2.Timeline.Instructions
	case res.Timeline != nil:
		instructions = res.Timeline.Timeline.Instructions
	}
	feedAuthor := res.Core.UserResults.Result.Legacy.ScreenName
	if feedAuthor == "" {
		feedAuthor = res.Core.UserResults.Result.Core.ScreenName
	}

	// First pass: collect all tweet nodes from the payload (including pinned
	// and conversation modules). These act as link-resolution candidates.
	var nodes []*twitterTweetResult
	collect := func(r twitterTweetResult) {
		if r.RestID == "" || r.Legacy.FullText == "" {
			return // promoted/filtered placeholders
		}
		if r.Legacy.RetweetedStatus != nil && r.Legacy.RetweetedStatus.Result.RestID != "" {
			r = r.Legacy.RetweetedStatus.Result
			if r.RestID == "" || r.Legacy.FullText == "" {
				return
			}
		}
		nodes = append(nodes, &r)
	}
	for _, ins := range instructions {
		if ins.Type == "TimelineAddEntries" || ins.Type == "TimelinePinEntry" {
			for _, e := range ins.Entries {
				if strings.HasPrefix(e.EntryID, "tweet-") {
					collect(e.Content.ItemContent.TweetResults.Result)
				}
				for _, it := range e.Content.Items { // conversation threads
					collect(it.Item.ItemContent.TweetResults.Result)
				}
			}
			if ins.Type == "TimelinePinEntry" && ins.Entry != nil &&
				strings.HasPrefix(ins.Entry.EntryID, "tweet-") {
				collect(ins.Entry.Content.ItemContent.TweetResults.Result)
			}
		}
	}
	for _, n := range nodes {
		n.linkCandidates = nodes
	}

	// Second pass: emit items. Link-only tweets (thread continuations whose
	// body is just a link to another tweet) are treated like retweets: the
	// linked tweet is emitted instead, with provenance naming the linker.
	out := make([]*twitterTweetResult, 0, len(nodes))
	seen := map[string]bool{}
	markSeen := func(r *twitterTweetResult) { seen[r.RestID] = true }
	pinnedIDs := map[string]bool{}
	for _, ins := range instructions {
		if ins.Type == "TimelinePinEntry" && ins.Entry != nil &&
			strings.HasPrefix(ins.Entry.EntryID, "tweet-") {
			if r := ins.Entry.Content.ItemContent.TweetResults.Result; r.RestID != "" {
				if r.Legacy.RetweetedStatus != nil && r.Legacy.RetweetedStatus.Result.RestID != "" {
					r = r.Legacy.RetweetedStatus.Result
				}
				pinnedIDs[r.RestID] = true
			}
		}
	}
	appendTweet := func(r twitterTweetResult) {
		r.linkCandidates = nodes // fresh decode: attach payload-wide candidates
		retweetedBy := ""
		if r.Legacy.RetweetedStatus != nil && r.Legacy.RetweetedStatus.Result.RestID != "" {
			retweetedBy = r.author()
			r = r.Legacy.RetweetedStatus.Result
		}
		if r.RestID == "" || seen[r.RestID] {
			return // promoted/filtered placeholders or already emitted
		}
		markSeen(&r)
		if target := r.linkTargetTweet(); target != "" {
			// Link-only entry: fold into its target instead.
			if linked := r.linkedTweetNode(target); linked != nil && !seen[linked.RestID] {
				copied := *linked
				copied.LinkedBy = r.author()
				markSeen(&copied)
				copied.Pinned = pinnedIDs[copied.RestID]
				out = append(out, &copied)
			}
			return
		}
		r.RetweetedBy = retweetedBy
		r.Pinned = pinnedIDs[r.RestID]
		out = append(out, &r)
	}
	for _, ins := range instructions {
		if ins.Type == "TimelineAddEntries" || ins.Type == "TimelinePinEntry" {
			for _, e := range ins.Entries {
				if strings.HasPrefix(e.EntryID, "tweet-") {
					appendTweet(e.Content.ItemContent.TweetResults.Result)
				}
				for _, it := range e.Content.Items { // conversation threads
					appendTweet(it.Item.ItemContent.TweetResults.Result)
				}
			}
			if ins.Type == "TimelinePinEntry" && ins.Entry != nil &&
				strings.HasPrefix(ins.Entry.EntryID, "tweet-") {
				appendTweet(ins.Entry.Content.ItemContent.TweetResults.Result)
			}
		}
	}
	return out, nil
}

// linkTargetTweet returns the tweet ID if all of the tweet's URL entities
// point at x.com/twitter.com status URLs, or "" otherwise.
func (t *twitterTweetResult) linkTargetTweet() string {
	// Quote tweets often carry the quoted status as their only URL entity,
	// but they are real content (the quote text matters), not thread
	// continuations — never fold them into their target.
	if t.Quoted != nil {
		return ""
	}
	urls := t.Legacy.Entities.URLs
	if len(urls) == 0 {
		return ""
	}
	var tweetID string
	for _, u := range urls {
		parts := strings.Split(u.ExpandedURL, "/")
		for i, p := range parts {
			if (p == "status" || p == "statuses") && i+1 < len(parts) {
				tweetID = parts[i+1]
				break
			}
		}
		if tweetID == "" {
			return "" // at least one non-tweet link → real content
		}
	}
	return tweetID
}

// linkedTweetNode looks up the tweet node for a thread-continuation target.
// The conversation payload (TweetDetail responses embed the whole thread)
// usually carries it in the same instruction set as the linking tweet, so
// callers pass all parsed nodes here. Returns nil when the target is not in
// the payload — the caller then simply drops the link-only entry.
func (t *twitterTweetResult) linkedTweetNode(target string) *twitterTweetResult {
	for _, cand := range t.linkCandidates {
		if cand.RestID == target && cand.Legacy.FullText != "" {
			return cand
		}
	}
	return nil
}

// stripAnchors removes <a ...>...</a> markup from text produced by
// expandLinks.
func stripAnchors(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "<a ")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		j := strings.Index(s[i:], "</a>")
		if j < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		s = s[i+j+len("</a>"):]
	}
}

// titleAuthorForBadge picks the @handle the badge should name: the tweet's
// own author when it differs from the referrer (e.g. a quote of someone
// else's tweet), otherwise the referrer themselves (self-quotes/threads).
func titleAuthorForBadge(referrer string, t *twitterTweetResult) string {
	if a := t.author(); a != "" && a != referrer {
		return a
	}
	return referrer
}

// item converts a parsed tweet into a feed Item.
func (t *twitterTweetResult) item(feedScreenName string) Item {
	hasMedia := len(t.Legacy.media()) > 0
	// Media links are dropped from the text only when the media is actually
	// embedded below.
	text := t.displayText(hasMedia)
	author := t.author()

	// Quote tweets keep the quoter's own commentary as the primary content,
	// with the quoted tweet appended below as context. (Previously the own
	// text was discarded in favor of the quoted tweet, hiding what the
	// followed account actually said.) A quote with no own text folds
	// completely into the quoted tweet.
	if t.Quoted != nil {
		q := &t.Quoted.Result
		if q.RestID != "" && q.Legacy.FullText != "" {
			// No own text at all: fold completely into the quoted tweet.
			if strings.TrimSpace(text) == "" {
				copied := *q
				copied.QuotedBy = author
				return copied.item(feedScreenName)
			}
			copied := *q
			copied.QuotedBy = ""
			copied.RetweetedBy = ""
			base := copied.item(feedScreenName)

			title := text
			if len(title) > 120 {
				title = title[:117] + "..."
			}
			title = strings.ReplaceAll(title, "\n", " ")
			title = stripRe.ReplaceAllString(title, "")

			return Item{
				GUID:        "twitter:" + t.RestID,
				Title:       fmt.Sprintf("[Quote @%s] %s", q.author(), title),
				URL:         fmt.Sprintf("https://x.com/%s/status/%s", author, t.RestID),
				CommentURL:  fmt.Sprintf("https://x.com/%s/status/%s", author, t.RestID),
				Content: "<p>" + textToHTML(text) + "</p>" + t.mediaHTML() +
					fmt.Sprintf("<p><em>💬 quoting <a href=\"%s\">@%s</a>:</em></p><blockquote>%s</blockquote>",
						htmlEscaped(base.URL), htmlEscaped(q.author()), base.Content),
				Author:      "@" + author,
				PublishedAt: parseTwitterTime(t.Legacy.CreatedAt),
				Points:      t.Legacy.FavoriteCount,
				CommentsCnt: t.Legacy.ReplyCount,
			}
		}
	}

	title := text
	if len(title) > 120 {
		title = title[:117] + "..."
	}
	title = strings.ReplaceAll(title, "\n", " ")
	// Strip any residual HTML from the title for plain display.
	title = stripRe.ReplaceAllString(title, "")

	itemURL := fmt.Sprintf("https://x.com/%s/status/%s", author, t.RestID)

	content := "<p>" + textToHTML(text) + "</p>" + t.mediaHTML()

	// Retweets: mark provenance so readers know why this appears in the
	// feed (the author is the original tweeter, not the followed account).
	if t.RetweetedBy != "" && t.RetweetedBy != author {
		title = fmt.Sprintf("[Retweet @%s] %s", author, title)
		content = fmt.Sprintf("<p><em>🔁 retweeted by <strong>@%s</strong></em></p>", htmlEscaped(t.RetweetedBy)) + content
	}
	// Quote tweets: note whose tweet is being quoted.
	if t.QuotedBy != "" {
		title = fmt.Sprintf("[Quote @%s] %s", titleAuthorForBadge(t.QuotedBy, t), title)
		content = fmt.Sprintf("<p><em>💬 quoted by <strong>@%s</strong></em></p>", htmlEscaped(t.QuotedBy)) + content
	}
	// Link-only thread continuations resolved to their target tweet: the
	// linker surfaced this tweet by posting its URL. Badge names the linker
	// (the link tweet's author).
	if t.LinkedBy != "" {
		title = fmt.Sprintf("[Link @%s] %s", t.LinkedBy, title)
		content = fmt.Sprintf("<p><em>🔗 linked by <strong>@%s</strong></em></p>", htmlEscaped(t.LinkedBy)) + content
	}
	// Pinned profile tweet: prefix the title. The poller refreshes twitter
	// item titles on every fetch, so the badge disappears when unpinned.
	if t.Pinned {
		title = "📌 " + title
	}

	return Item{
		GUID:        "twitter:" + t.RestID,
		Title:       title,
		URL:         itemURL,
		CommentURL:  itemURL,
		Content:     content,
		Author:      "@" + author,
		PublishedAt: parseTwitterTime(t.Legacy.CreatedAt),
		Points:      t.Legacy.FavoriteCount,
		CommentsCnt: t.Legacy.ReplyCount,
	}
}

// mediaHTML renders the tweet's photo/video/GIF attachments as HTML.
// Photos embed the image directly (with a link to the full-size version);
// videos and GIFs embed the highest-bitrate MP4 variant in a <video> tag.
func (t *twitterTweetResult) mediaHTML() string {
	var b strings.Builder
	for _, m := range t.Legacy.media() {
		switch m.Type {
		case "photo":
			if m.MediaURLHTTPS == "" {
				continue
			}
			fmt.Fprintf(&b, `<p><a href="%s"><img src="%s" alt="tweet image" loading="lazy" /></a></p>`,
				htmlEscaped(m.MediaURLHTTPS+"?name=large"), htmlEscaped(m.MediaURLHTTPS))
		case "video", "animated_gif":
			if m.VideoInfo == nil {
				continue
			}
			best := ""
			var bestBitrate int64 = -1
			for _, v := range m.VideoInfo.Variants {
				if v.ContentType == "video/mp4" && v.Bitrate > bestBitrate {
					best, bestBitrate = v.URL, v.Bitrate
				}
			}
			if best == "" {
				continue
			}
			poster := ""
			if m.MediaURLHTTPS != "" {
				poster = fmt.Sprintf(` poster="%s"`, htmlEscaped(m.MediaURLHTTPS))
			}
			fmt.Fprintf(&b, `<p><video controls preload="metadata"%s><source src="%s" type="video/mp4" /></video></p>`,
				poster, htmlEscaped(best))
		}
	}
	return b.String()
}

// text returns the best available body text: long-form note tweets win over
// the truncated legacy full_text; retweets resolve to the original tweet's
// text with an "RT @author:" prefix preserved from full_text.
func (t *twitterTweetResult) text() string {
	if t.Note != nil && t.Note.NoteTweet.Text != "" {
		return t.Note.NoteTweet.Text
	}
	return t.Legacy.FullText
}

// displayText is text() with t.co links expanded to their real destinations
// (and media links dropped when the media itself is embedded).
func (t *twitterTweetResult) displayText(embedMedia bool) string {
	if t.Note != nil && t.Note.NoteTweet.Text != "" {
		// Note tweets keep their full text; still expand any t.co links.
		return expandLinks(t.Note.NoteTweet.Text, &t.Legacy, embedMedia)
	}
	return expandLinks(t.Legacy.FullText, &t.Legacy, embedMedia)
}

// htmlEscaped escapes plain text for safe inclusion in HTML content.
func htmlEscaped(s string) string { return html.EscapeString(s) }

// textToHTML escapes tweet text (preserving <a> anchors from link
// expansion) and converts newlines to <br> so line breaks survive HTML
// rendering instead of collapsing into spaces.
func textToHTML(s string) string {
	escaped := htmlEscapedText(s)
	return strings.ReplaceAll(escaped, "\n", "<br />")
}

// htmlEscapedText escapes tweet text that may already contain <a> link
// markup produced by displayText: existing anchors are preserved, everything
// else is escaped.
func htmlEscapedText(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "<a ")
		if i < 0 {
			b.WriteString(html.EscapeString(s))
			return b.String()
		}
		j := strings.Index(s[i:], "</a>")
		if j < 0 {
			b.WriteString(html.EscapeString(s))
			return b.String()
		}
		end := i + j + len("</a>")
		b.WriteString(html.EscapeString(s[:i]))
		b.WriteString(s[i:end]) // anchor already safe (built from escaped parts)
		s = s[end:]
	}
}

// parseTwitterTime parses Twitter's "Mon Jan 02 15:04:05 -0700 2006" dates.
func parseTwitterTime(s string) time.Time {
	const layout = "Mon Jan 02 15:04:05 -0700 2006"
	t, err := time.Parse(layout, s)
	if err != nil {
		slog.Debug("failed to parse Twitter date", "date", s, "error", err)
		return time.Time{}
	}
	return t
}

// FetchComments implements CommentFetcher: it pulls the conversation thread
// for a tweet and returns its replies (flattened, sorted by likes).
func (f *TwitterFetcher) setTransport(t http.RoundTripper) { f.client.Transport = t }
func (f *TwitterFetcher) CommentTargetURL() string { return f.baseURL }

func (f *TwitterFetcher) FetchComments(ctx context.Context, item store.Item, maxComments int) ([]Comment, error) {
	tweetID := extractTweetID(item.Hash, item.CommentUrl, item.Url)
	if tweetID == "" {
		return nil, fmt.Errorf("could not extract tweet ID")
	}

	variables := fmt.Sprintf(`{"focalTweetId":%q,"referrer":"profile","with_rux_injections":false,"rankingMode":"Relevance","includePromotedContent":true,"withCommunity":true,"withQuickPromoteEligibilityTweetFields":true,"withBirdwatchNotes":true,"withVoice":true}`, tweetID)
	body, err := f.gqlGet(ctx, twitterTweetDetailQuery, variables)
	if err != nil {
		return nil, err
	}

	// TweetDetail (v2 shape) nests the conversation under
	// data.threaded_conversation_with_injections_v2, whose instructions hold
	// entries directly.
	var alt struct {
		Data struct {
			ThreadedConversationWithInjections struct {
				Instructions []twitterInstruction `json:"instructions"`
			} `json:"threaded_conversation_with_injections_v2"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &alt); err != nil {
		return nil, fmt.Errorf("decoding TweetDetail response: %w", err)
	}
	instructions := alt.Data.ThreadedConversationWithInjections.Instructions

	var comments []Comment
	appendReply := func(r twitterTweetResult) {
		if r.RestID == "" || r.Legacy.FullText == "" {
			return
		}
		if r.Legacy.InReplyToStatusID == "" {
			return // the requested tweet itself, not a reply
		}
		authorURL := ""
		if name := r.author(); name != "" {
			authorURL = "https://x.com/" + name + "/status/" + r.RestID
		}
		commentAuthor := r.author()
		if commentAuthor == "" {
			commentAuthor = strings.TrimPrefix(r.Legacy.FullText, "RT @")
			if idx := strings.Index(commentAuthor, ":"); idx > 0 && commentAuthor != r.Legacy.FullText {
				commentAuthor = commentAuthor[:idx]
			} else {
				commentAuthor = ""
			}
		}
		comments = append(comments, Comment{
			Author:      "@" + commentAuthor,
			Text:        r.text(),
			Points:      r.Legacy.FavoriteCount,
			ValidPoints: true,
			Depth:       0,
			URL:         authorURL,
		})
	}
	for _, ins := range instructions {
		for _, e := range ins.Entries {
			// Replies appear either as top-level tweet/conversationthread
			// entries or wrapped inside a conversation module's items[].
			if strings.HasPrefix(e.EntryID, "tweet-") || strings.HasPrefix(e.EntryID, "conversationthread-") {
				appendReply(e.Content.ItemContent.TweetResults.Result)
			}
			for _, it := range e.Content.Items {
				appendReply(it.Item.ItemContent.TweetResults.Result)
			}
		}
	}

	sort.Slice(comments, func(i, j int) bool { return comments[i].Points > comments[j].Points })
	if maxComments > 0 && len(comments) > maxComments {
		comments = comments[:maxComments]
	}
	return comments, nil
}

// extractTweetID finds a status ID in a twitter:<id> GUID or an x.com /
// twitter.com status URL.
func extractTweetID(ids ...string) string {
	for _, s := range ids {
		if s == "" {
			continue
		}
		if id := strings.TrimPrefix(s, "twitter:"); strings.Contains(s, "twitter:") && id != "" {
			return id
		}
		u, err := url.Parse(s)
		if err != nil {
			continue
		}
		if !strings.Contains(u.Host, "twitter.com") && !strings.Contains(u.Host, "x.com") {
			continue
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		for i, p := range parts {
			if p == "status" || p == "statuses" {
				if i+1 < len(parts) && parts[i+1] != "" {
					return parts[i+1]
				}
			}
		}
	}
	return ""
}
