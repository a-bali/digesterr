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

const twitterUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:134.0) Gecko/20100101 Firefox/134.0"

const twitterFeatures = `{"rweb_tipjar_consumption_enabled":true,"responsive_web_graphql_exclude_directive_enabled":true,"verified_phone_label_enabled":false,"creator_subscriptions_tweet_preview_api_enabled":true,"responsive_web_graphql_timeline_navigation_enabled":true,"responsive_web_graphql_skip_user_profile_image_extensions_enabled":false,"communities_web_enable_tweet_community_results_fetch":true,"articles_preview_enabled":true,"longform_notetweets_inline_media_enabled":true,"articles_preview_20251118_enabled":true,"rweb_video_screen_enabled":false,"responsive_web_edit_tweet_api_enabled":true,"tweetypie_unmention_optimization_enabled":true,"responsive_text_only_conversations_in_reply_tab_enabled":true,"freedom_of_speech_not_reach_fetch_enabled":true,"standardized_nudges_misinfo":true,"tweet_with_visibility_results_prefer_gql_limited_actions_policy_enabled":true,"longform_notetweets_rich_text_read_enabled":true,"longform_notetweets_creation_enabled":true,"profile_label_editable":true,"immersive_video_status_ring_live_survey":false,"content_disclosure_indicator_enabled":true,"super_follow_badge_enabled":true,"subscriptions_verification_info_verified_since_enabled":true}`

// TwitterFetcher fetches a user's tweets via Twitter/X's internal GraphQL
// API, authenticated with browser session cookies (ct0 CSRF token + auth_token).
type TwitterFetcher struct {
	client    *http.Client
	baseURL   string
	ct0       string
	authToken string
	bearer    string
}

func NewTwitterFetcher() *TwitterFetcher {
	return &TwitterFetcher{
		client:  &http.Client{Timeout: 15 * time.Second},
		baseURL: "https://x.com",
		bearer:  twitterPublicBearer,
	}
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
	req.Header.Set("User-Agent", twitterUserAgent)

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

	variables := fmt.Sprintf(`{"userId":%q,"count":20,"includePromotedContent":false,"withQuickPromoteEligibilityTelemetry":true,"withVoice":true}`, userID)
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
	Core struct {
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

type twitterTweetLegacy struct {
	FullText        string `json:"full_text"`
	CreatedAt       string `json:"created_at"`
	FavoriteCount   int    `json:"favorite_count"`
	RetweetCount    int    `json:"retweet_count"`
	ReplyCount      int    `json:"reply_count"`
	InReplyToStatusID string `json:"in_reply_to_status_id_str"`
	InReplyToScreenName string `json:"in_reply_to_screen_name"`
	RetweetedStatus *twitterTweetWrapper `json:"retweeted_status_result"`
	Entities        struct {
		Media []struct {
			Type string `json:"type"`
			MediaURLHTTPS string `json:"media_url_https"`
			VideoInfo *struct {
				Variants []struct {
					ContentType string `json:"content_type"`
					Bitrate     int64  `json:"bitrate"`
					URL         string `json:"url"`
				} `json:"variants"`
			} `json:"video_info"`
		} `json:"media"`
	} `json:"entities"`
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
	Type    string           `json:"type"`
	Entries []twitterEntry   `json:"entries"`
	Entry   *twitterEntry    `json:"entry"` // pinned tweets arrive as a single entry
}

type twitterEntry struct {
	EntryID    string `json:"entryId"`
	Content    struct {
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

	var out []*twitterTweetResult
	appendTweet := func(r twitterTweetResult) {
		if r.Legacy.RetweetedStatus != nil && r.Legacy.RetweetedStatus.Result.RestID != "" {
			r = r.Legacy.RetweetedStatus.Result
		}
		if r.RestID == "" || r.Legacy.FullText == "" {
			return // promoted/filtered placeholders
		}
		out = append(out, &r)
	}
	for _, ins := range instructions {
		if ins.Type == "TimelineAddEntries" {
			for _, e := range ins.Entries {
				if strings.HasPrefix(e.EntryID, "tweet-") {
					appendTweet(e.Content.ItemContent.TweetResults.Result)
				}
				for _, it := range e.Content.Items { // conversation threads
					appendTweet(it.Item.ItemContent.TweetResults.Result)
				}
			}
		}
	}
	return out, nil
}

// item converts a parsed tweet into a feed Item.
func (t *twitterTweetResult) item(feedScreenName string) Item {
	text := t.text()
	author := t.author()
	title := text
	if len(title) > 120 {
		title = title[:117] + "..."
	}
	title = strings.ReplaceAll(title, "\n", " ")

	itemURL := fmt.Sprintf("https://x.com/%s/status/%s", author, t.RestID)

	return Item{
		GUID:        "twitter:" + t.RestID,
		Title:       title,
		URL:         itemURL,
		CommentURL:  itemURL,
		Content:     "<p>" + htmlEscaped(text) + "</p>",
		Author:      "@" + author,
		PublishedAt: parseTwitterTime(t.Legacy.CreatedAt),
		Points:      t.Legacy.FavoriteCount,
		CommentsCnt: t.Legacy.ReplyCount,
	}
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

// htmlEscaped escapes tweet text for safe inclusion in the HTML content.
func htmlEscaped(s string) string { return html.EscapeString(s) }

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
