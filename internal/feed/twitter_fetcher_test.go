package feed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"digesterr/internal/store"
)

func TestExtractTwitterScreenName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://x.com/elonmusk", "elonmusk"},
		{"https://www.twitter.com/elonmusk/", "elonmusk"},
		{"https://x.com/nasa/status/12345", "nasa"},
		{"@elonmusk", "elonmusk"},
		{"elonmusk", "elonmusk"},
		{"https://example.com/foo", ""},
		{"https://x.com/", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := extractTwitterScreenName(c.in); got != c.want {
			t.Errorf("extractTwitterScreenName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExtractTweetID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"twitter:1234567890", "1234567890"},
		{"https://x.com/elonmusk/status/1234567890", "1234567890"},
		{"https://twitter.com/nasa/statuses/98765/photo/1", "98765"},
		{"https://example.com/status/1", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := extractTweetID(c.in); got != c.want {
			t.Errorf("extractTweetID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseTwitterTime(t *testing.T) {
	got := parseTwitterTime("Sun Aug 23 10:57:54 +0000 2026")
	if got.IsZero() {
		t.Fatal("expected valid time")
	}
	if got.Year() != 2026 || got.Month().String() != "August" || got.Day() != 23 {
		t.Fatalf("unexpected parsed date: %v", got)
	}
	if !parseTwitterTime("garbage").IsZero() {
		t.Error("garbage input should yield zero time")
	}
}

// userByResponse builds a minimal UserByScreenName payload.
func userByResponse(id string) []byte {
	body := map[string]any{"data": map[string]any{"user": map[string]any{
		"result": map[string]any{"rest_id": id, "legacy": map[string]any{"screen_name": "testuser"}},
	}}}
	b, _ := json.Marshal(body)
	return b
}

// userTweetsResponse builds a UserTweets payload with one original tweet,
// one retweet (wrapped), one note tweet, and a non-tweet cursor entry.
func userTweetsResponse() []byte {
	tweet := func(id, screen, text, created string, fav, reply int) map[string]any {
		return map[string]any{
			"rest_id": id,
			"core":    map[string]any{"user_results": map[string]any{"result": map[string]any{"legacy": map[string]any{"screen_name": screen}}}},
			"legacy": map[string]any{
				"full_text": text, "created_at": created,
				"favorite_count": fav, "reply_count": reply,
			},
		}
	}
	retweet := tweet("222", "other", "RT @orig: retweeted text", "Sun Aug 23 10:30:00 +0000 2026", 0, 1)
	retweet["legacy"].(map[string]any)["retweeted_status_result"] = map[string]any{
		"result": tweet("223", "orig", "retweeted text", "Sun Aug 23 09:00:00 +0000 2026", 7, 2),
	}
	note := tweet("333", "testuser", "short trunc...", "Sun Aug 23 08:00:00 +0000 2026", 1, 0)
	note["note_tweet"] = map[string]any{"note_tweet": map[string]any{"text": "this is a very long form note tweet body"}}

	entry := func(id string, res map[string]any) map[string]any {
		return map[string]any{"entryId": id, "content": map[string]any{
			"itemContent": map[string]any{"tweet_results": map[string]any{"result": res}}}}
	}
	body := map[string]any{"data": map[string]any{"user": map[string]any{"result": map[string]any{
		"timeline_v2": map[string]any{"timeline": map[string]any{"instructions": []any{
			map[string]any{"type": "TimelineAddEntries", "entries": []any{
				entry("tweet-111", tweet("111", "testuser", "hello world", "Sun Aug 23 10:00:00 +0000 2026", 42, 5)),
				entry("tweet-222", retweet),
				entry("tweet-333", note),
				map[string]any{"entryId": "cursor-bottom-0", "content": map[string]any{"entryType": "TimelineTimelineCursor", "value": "abc"}},
			}},
		}}},
	}}}}
	b, _ := json.Marshal(body)
	return b
}

// userTweetsResponseWithPin builds a UserTweets payload with a pinned tweet:
// the pin instruction carries a separate TimelinePinEntry for tweet-444.
func userTweetsResponseWithPin() []byte {
	tweet := func(id, screen, text string) map[string]any {
		return map[string]any{
			"rest_id": id,
			"core":    map[string]any{"user_results": map[string]any{"result": map[string]any{"legacy": map[string]any{"screen_name": screen}}}},
			"legacy":  map[string]any{"full_text": text},
		}
	}
	entry := func(id string, res map[string]any) map[string]any {
		return map[string]any{"entryId": id, "content": map[string]any{
			"itemContent": map[string]any{"tweet_results": map[string]any{"result": res}}}}
	}
	pinEntry := entry("tweet-111", tweet("111", "testuser", "pinned announcement"))
	body := map[string]any{"data": map[string]any{"user": map[string]any{"result": map[string]any{
		"core": map[string]any{"user_results": map[string]any{"result": map[string]any{
			"legacy": map[string]any{"screen_name": "testuser"}}}},
		"timeline_v2": map[string]any{"timeline": map[string]any{"instructions": []any{
			map[string]any{"type": "TimelinePinEntry", "entry": pinEntry},
			map[string]any{"type": "TimelineAddEntries", "entries": []any{
				entry("tweet-222", tweet("222", "testuser", "regular tweet")),
			}},
		}}},
	}}}}
	b, _ := json.Marshal(body)
	return b
}

// tweetDetailResponse builds a TweetDetail (v2) payload: parent tweet plus
// conversation-thread entries holding replies.
func tweetDetailResponse() []byte {
	reply := func(id, screen, text string, fav int) map[string]any {
		return map[string]any{
			"rest_id": id,
			"core":    map[string]any{"user_results": map[string]any{"result": map[string]any{"legacy": map[string]any{"screen_name": screen}}}},
			"legacy": map[string]any{
				"full_text": text, "favorite_count": fav, "in_reply_to_status_id_str": "111",
			},
		}
	}
	parent := map[string]any{
		"rest_id": "111",
		"legacy":  map[string]any{"full_text": "parent tweet", "favorite_count": 42, "in_reply_to_status_id_str": ""},
	}
	tweetEntry := func(id string, res map[string]any) map[string]any {
		return map[string]any{"entryId": id, "content": map[string]any{
			"itemContent": map[string]any{"tweet_results": map[string]any{"result": res}}}}
	}
	body := map[string]any{"data": map[string]any{"threaded_conversation_with_injections_v2": map[string]any{
		"instructions": []any{map[string]any{"entries": []any{
			tweetEntry("tweet-111", parent),
			map[string]any{"entryId": "conversationthread-555", "content": map[string]any{
				"entryType": "ConversationTimelineModule", "items": []any{
					map[string]any{"entryId": "c-tweet-555", "item": map[string]any{
						"itemContent": map[string]any{"tweet_results": map[string]any{"result": reply("555", "alice", "first reply", 10)}}}},
				}}},
			tweetEntry("conversationthread-666", reply("666", "bob", "second reply with more likes", 99)),
		}}},
	}}}
	b, _ := json.Marshal(body)
	return b
}

func newTestTwitterFetcher(t *testing.T, userID string, tweetsBody []byte) (*TwitterFetcher, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "UserByScreenName"):
			_, _ = w.Write(userByResponse(userID))
		case strings.Contains(r.URL.Path, "UserTweets"):
			_, _ = w.Write(tweetsBody)
		default:
			http.NotFound(w, r)
		}
	}))
	f := NewTwitterFetcher()
	f.baseURL = srv.URL
	f.SetCredentials("ct0-test", "auth-test")
	return f, srv
}

func TestTwitterFetch(t *testing.T) {
	f, srv := newTestTwitterFetcher(t, "4444", userTweetsResponse())
	defer srv.Close()

	items, err := f.Fetch(context.Background(), "https://x.com/testuser")
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}

	first := items[0]
	if first.GUID != "twitter:111" {
		t.Errorf("GUID = %q, want twitter:111", first.GUID)
	}
	if first.URL != "https://x.com/testuser/status/111" {
		t.Errorf("URL = %q", first.URL)
	}
	if first.Points != 42 || first.CommentsCnt != 5 {
		t.Errorf("points/comments = %d/%d, want 42/5", first.Points, first.CommentsCnt)
	}
	if first.Author != "@testuser" {
		t.Errorf("author = %q", first.Author)
	}
	if first.PublishedAt.IsZero() {
		t.Error("published date not parsed")
	}

	// Retweet unwraps to the original tweet's identity and score, with
	// retweet provenance recorded.
	rt := items[1]
	if rt.GUID != "twitter:223" {
		t.Errorf("retweet GUID = %q, want twitter:223 (unwrapped)", rt.GUID)
	}
	if rt.Points != 7 {
		t.Errorf("retweet points = %d, want 7", rt.Points)
	}
	if !strings.Contains(rt.Title, "[RT @other]") {
		t.Errorf("retweet title missing RT marker: %q", rt.Title)
	}
	if !strings.Contains(rt.Content, "retweeted</em>") || !strings.Contains(rt.Content, "@other") {
		t.Errorf("retweet content missing provenance: %s", rt.Content)
	}

	// Note tweet uses long-form text as content and title source.
	note := items[2]
	if !strings.Contains(note.Content, "long form note") {
		t.Errorf("note tweet content = %q", note.Content)
	}
	if strings.Contains(note.Title, "trunc") {
		t.Errorf("title should come from note text, got %q", note.Title)
	}
}

func TestTwitterPinnedBadge(t *testing.T) {
	// Build a timeline whose pinned entry (333) also carries the Pinned flag
	// through parseUserTweets.
	body := userTweetsResponseWithPin()
	f, srv := newTestTwitterFetcher(t, "4444", body)
	defer srv.Close()

	items, err := f.Fetch(context.Background(), "https://x.com/testuser")
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	var pinned *Item
	for i := range items {
		if items[i].GUID == "twitter:111" && strings.Contains(items[i].Title, "📌") {
			pinned = &items[i]
		} else if strings.Contains(items[i].Title, "📌") {
			t.Errorf("non-pinned item %s has 📌 badge", items[i].GUID)
		}
	}
	if pinned == nil {
		t.Fatal("pinned item not found with 📌 badge")
	}
}

func TestTwitterFetchMissingCredentials(t *testing.T) {
	f := NewTwitterFetcher()
	_, err := f.Fetch(context.Background(), "https://x.com/testuser")
	if err == nil || !strings.Contains(err.Error(), "credentials not configured") {
		t.Fatalf("expected credentials error, got %v", err)
	}
}

func TestTwitterFetchBadURL(t *testing.T) {
	f, srv := newTestTwitterFetcher(t, "4444", userTweetsResponse())
	defer srv.Close()
	if _, err := f.Fetch(context.Background(), "not-a-twitter-url"); err == nil {
		t.Fatal("expected screen-name extraction error")
	}
}

func TestTwitterUserNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()
	f := NewTwitterFetcher()
	f.baseURL = srv.URL
	f.SetCredentials("c", "a")
	if _, err := f.userID(context.Background(), "ghost"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestTwitterFetchComments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "TweetDetail"):
			_, _ = w.Write(tweetDetailResponse())
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	f := NewTwitterFetcher()
	f.baseURL = srv.URL
	f.SetCredentials("ct0-test", "auth-test")

	comments, err := f.FetchComments(context.Background(), storeItemForTwitter("twitter:111"), 50)
	if err != nil {
		t.Fatalf("FetchComments failed: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("got %d comments, want 2", len(comments))
	}
	// Sorted by likes: bob (99) before alice (10).
	if comments[0].Author != "@bob" || comments[0].Points != 99 {
		t.Errorf("comments[0] = %+v", comments[0])
	}
	if comments[1].Author != "@alice" || comments[1].Points != 10 {
		t.Errorf("comments[1] = %+v", comments[1])
	}
	if comments[1].URL != "https://x.com/alice/status/555" {
		t.Errorf("comment URL = %q", comments[1].URL)
	}

	// maxComments caps the result after sorting; URL-based ID extraction path.
	capped, err := f.FetchComments(context.Background(), storeItemForTwitter("https://x.com/a/status/111"), 1)
	if err != nil {
		t.Fatalf("FetchComments (url) failed: %v", err)
	}
	if len(capped) != 1 {
		t.Fatalf("capped length = %d, want 1", len(capped))
	}
}

func TestTwitterMediaRendering(t *testing.T) {
	photo := map[string]any{"type": "photo", "media_url_https": "https://pbs.twimg.com/media/abc.jpg"}
	video := map[string]any{
		"type":             "video",
		"media_url_https":  "https://pbs.twimg.com/media/vid.jpg",
		"video_info": map[string]any{"variants": []any{
			map[string]any{"content_type": "video/mp4", "bitrate": 320000, "url": "https://video.twimg.com/low.mp4"},
			map[string]any{"content_type": "video/mp4", "bitrate": 2176000, "url": "https://video.twimg.com/high.mp4"},
			map[string]any{"content_type": "application/x-mpegURL", "bitrate": 0, "url": "https://video.twimg.com/pl.m3u8"},
		}},
	}
	tw := &twitterTweetResult{RestID: "999"}
	tw.Legacy.FullText = "tweet with media"
	tw.Legacy.ExtendedEntities.Media = []twitterMedia{}
	// Rebuild through JSON to exercise the struct tags.
	raw, _ := json.Marshal(map[string]any{
		"rest_id":          "999",
		"legacy": map[string]any{
			"full_text":         "tweet with media",
			"extended_entities": map[string]any{"media": []any{photo, video}},
			// entities.media should be ignored when extended_entities exists
			"entities": map[string]any{"media": []any{
				map[string]any{"type": "photo", "media_url_https": "https://pbs.twimg.com/media/first.jpg"},
			}},
		},
	})
	if err := json.Unmarshal(raw, tw); err != nil {
		t.Fatal(err)
	}
	html := tw.mediaHTML()
	if !strings.Contains(html, `src="https://pbs.twimg.com/media/abc.jpg"`) {
		t.Errorf("photo not rendered: %s", html)
	}
	if strings.Contains(html, "first.jpg") {
		t.Error("entities.media must be skipped when extended_entities is present")
	}
	// Highest-bitrate MP4 wins; HLS variant ignored.
	if !strings.Contains(html, `src="https://video.twimg.com/high.mp4"`) {
		t.Errorf("video variant not rendered: %s", html)
	}
	if !strings.Contains(html, `poster="https://pbs.twimg.com/media/vid.jpg"`) {
		t.Errorf("video poster missing: %s", html)
	}

	// Fallback: media only in entities.
	tw2 := &twitterTweetResult{}
	raw2, _ := json.Marshal(map[string]any{"legacy": map[string]any{
		"full_text": "x",
		"entities":  map[string]any{"media": []any{photo}},
	}})
	json.Unmarshal(raw2, tw2)
	if !strings.Contains(tw2.mediaHTML(), "abc.jpg") {
		t.Errorf("entities fallback failed: %s", tw2.mediaHTML())
	}
}

func TestTwitterLinkExpansion(t *testing.T) {
	// Case 1: media t.co link with NO entities.urls entry (common shape) —
	// must be dropped when the media is embedded.
	tw := &twitterTweetResult{}
	raw, _ := json.Marshal(map[string]any{
		"rest_id": "2",
		"legacy": map[string]any{
			"full_text": "chart says it all https://t.co/JWjZnDMd6G",
			"extended_entities": map[string]any{"media": []any{
				map[string]any{"type": "photo", "url": "https://t.co/JWjZnDMd6G", "media_url_https": "https://pbs.twimg.com/media/x.jpg"},
			}},
		},
	})
	json.Unmarshal(raw, tw)
	text := tw.displayText(true)
	if strings.Contains(text, "t.co") {
		t.Errorf("media t.co link should be dropped when media embedded: %s", text)
	}
	if !strings.Contains(text, "chart says it all") {
		t.Errorf("body text lost: %s", text)
	}
	// Without embed, expand to the image URL instead of the opaque t.co.
	textNoEmbed := tw.displayText(false)
	if strings.Contains(textNoEmbed, "t.co") || !strings.Contains(textNoEmbed, "pbs.twimg.com/media/x.jpg") {
		t.Errorf("media link should expand when no embed: %s", textNoEmbed)
	}

	// Case 2: media link listed in entities.urls too, plus a normal URL.
	tw2 := &twitterTweetResult{}
	raw, _ = json.Marshal(map[string]any{
		"rest_id": "1",
		"legacy": map[string]any{
			"full_text": "chart says it all https://t.co/JWjZnDMd6G and read https://t.co/abc123",
			"entities": map[string]any{
				"media": []any{map[string]any{"type": "photo", "url": "https://t.co/JWjZnDMd6G", "media_url_https": "https://pbs.twimg.com/media/x.jpg"}},
				"urls": []any{
					map[string]any{"url": "https://t.co/JWjZnDMd6G", "expanded_url": "https://pbs.twimg.com/media/x.jpg", "display_url": "pic.x.com/x"},
					map[string]any{"url": "https://t.co/abc123", "expanded_url": "https://example.com/blog/post", "display_url": "example.com/blog/post"},
				},
			},
			"extended_entities": map[string]any{"media": []any{
				map[string]any{"type": "photo", "url": "https://t.co/JWjZnDMd6G", "media_url_https": "https://pbs.twimg.com/media/x.jpg"},
			}},
		},
	})
	json.Unmarshal(raw, tw2)

	text = tw2.displayText(true)
	if strings.Contains(text, "t.co") {
		t.Errorf("media t.co link should be dropped when media embedded: %s", text)
	}
	if !strings.Contains(text, `<a href="https://example.com/blog/post">`) {
		t.Errorf("external link should be expanded: %s", text)
	}
	if strings.Contains(text, "t.co/abc123") {
		t.Errorf("external t.co link should be replaced: %s", text)
	}

	// Without embedded media the media link stays (as an expanded anchor).
	text2 := tw2.displayText(false)
	if !strings.Contains(text2, `href="https://pbs.twimg.com/media/x.jpg"`) {
		t.Errorf("media link should be kept when no embed: %s", text2)
	}
}

func TestTwitterSelfLinkOnlyResolvesToTarget(t *testing.T) {
	// Thread-continuation reply: body is ONLY a t.co link to the parent tweet
	// (111), which is also present in the payload. The item must be emitted
	// for the PARENT (its content), marked as linked-by the continuation.
	linkOnly := map[string]any{
		"rest_id": "777",
		"core":    map[string]any{"user_results": map[string]any{"result": map[string]any{"legacy": map[string]any{"screen_name": "testuser"}}}},
		"legacy": map[string]any{
			"full_text": "https://t.co/2vt7q5WCmm",
			"entities": map[string]any{"urls": []any{
				map[string]any{"url": "https://t.co/2vt7q5WCmm", "expanded_url": "https://x.com/testuser/status/111", "display_url": "x.com/testuser/statu…"},
			}},
		},
	}
	parent := map[string]any{
		"rest_id": "111",
		"core":    map[string]any{"user_results": map[string]any{"result": map[string]any{"legacy": map[string]any{"screen_name": "testuser"}}}},
		"legacy": map[string]any{
			"full_text":     "the actual thread root text",
			"favorite_count": 10,
		},
	}
	entry := func(id string, res map[string]any) map[string]any {
		return map[string]any{"entryId": id, "content": map[string]any{
			"itemContent": map[string]any{"tweet_results": map[string]any{"result": res}}}}
	}
	body := map[string]any{"data": map[string]any{"user": map[string]any{"result": map[string]any{
		"timeline_v2": map[string]any{"timeline": map[string]any{"instructions": []any{
			map[string]any{"type": "TimelineAddEntries", "entries": []any{
				entry("tweet-777", linkOnly),
				entry("tweet-111", parent),
			}},
		}}},
	}}}}
	raw, _ := json.Marshal(body)

	tweets, err := parseUserTweets(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(tweets) != 1 {
		t.Fatalf("got %d tweets, want 1 (link-only collapsed into parent)", len(tweets))
	}
	got := tweets[0]
	if got.RestID != "111" {
		t.Errorf("emitted tweet = %s, want parent 111", got.RestID)
	}
	if got.LinkedBy != "testuser" {
		t.Errorf("LinkedBy = %q, want testuser", got.LinkedBy)
	}

	item := got.item("testuser")
	if !strings.Contains(item.Title, "[from @testuser]") {
		t.Errorf("title missing link provenance: %q", item.Title)
	}
	if !strings.Contains(item.Content, "linked by") || !strings.Contains(item.Content, "the actual thread root text") {
		t.Errorf("content missing target text or provenance: %s", item.Content)
	}

	// When the target is NOT in the payload, the link-only entry is dropped
	// entirely (nothing useful to show).
	solo := map[string]any{"data": map[string]any{"user": map[string]any{"result": map[string]any{
		"timeline_v2": map[string]any{"timeline": map[string]any{"instructions": []any{
			map[string]any{"type": "TimelineAddEntries", "entries": []any{
				entry("tweet-777", linkOnly),
			}},
		}}},
	}}}}
	rawSolo, _ := json.Marshal(solo)
	tweetsSolo, err := parseUserTweets(rawSolo)
	if err != nil {
		t.Fatal(err)
	}
	if len(tweetsSolo) != 0 {
		t.Errorf("link-only tweet without resolvable target should be dropped, got %d", len(tweetsSolo))
	}
}

func TestTwitterAuthHeadersSent(t *testing.T) {
	var gotCookie, gotCSRF, gotBearer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		gotCSRF = r.Header.Get("X-CSRF-Token")
		gotBearer = r.Header.Get("Authorization")
		_, _ = w.Write(userByResponse("1"))
	}))
	defer srv.Close()
	f := NewTwitterFetcher()
	f.baseURL = srv.URL
	f.SetCredentials("my-ct0", "my-auth")
	if _, err := f.userID(context.Background(), "testuser"); err != nil {
		t.Fatalf("userID failed: %v", err)
	}
	if !strings.Contains(gotCookie, "ct0=my-ct0") || !strings.Contains(gotCookie, "auth_token=my-auth") {
		t.Errorf("cookie header = %q", gotCookie)
	}
	if gotCSRF != "my-ct0" {
		t.Errorf("csrf token = %q", gotCSRF)
	}
	if !strings.HasPrefix(gotBearer, "Bearer ") {
		t.Errorf("authorization = %q", gotBearer)
	}
}

// storeItemForTwitter builds a store.Item for comment-fetch tests.
func storeItemForTwitter(guidOrURL string) store.Item {
	return store.Item{ID: 1, Hash: guidOrURL, CommentUrl: guidOrURL, Url: guidOrURL}
}
