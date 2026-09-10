package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"digesterr/internal/store"
)

// buildTimeline builds a UserTweets payload carrying n original tweets.
func buildTimeline(n int) []byte {
	entries := make([]any, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%02d", i)
		entries = append(entries, map[string]any{
			"entryId": "tweet-" + id,
			"content": map[string]any{
				"entryType": "TimelineTimelineItem",
				"itemContent": map[string]any{
					"tweet_results": map[string]any{"result": map[string]any{
						"rest_id": "900" + id,
						"core": map[string]any{"user_results": map[string]any{
							"result": map[string]any{"legacy": map[string]any{"screen_name": "testuser"}}}},
						"legacy": map[string]any{
							"full_text":      "measured tweet " + id,
							"created_at":     "Sun Aug 23 10:57:54 +0000 2026",
							"favorite_count": 100 + i,
							"reply_count":    i,
						},
					}},
				},
			},
		})
	}
	body := map[string]any{"data": map[string]any{"user": map[string]any{"result": map[string]any{
		"timeline_v2": map[string]any{"timeline": map[string]any{"instructions": []any{
			map[string]any{"type": "TimelineAddEntries", "entries": entries},
		}}},
	}}}}
	b, _ := json.Marshal(body)
	return b
}

// newCountingTwitterPoller wires a poller whose twitter fetcher points at a mock
// X server that counts every GraphQL request, so the real request volume of a
// poll cycle can be asserted without touching the live API.
func newCountingTwitterPoller(t *testing.T, timelineSize int) (*Poller, *TwitterFetcher, store.Feed, func() int) {
	t.Helper()

	var mu sync.Mutex
	total := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		total++
		mu.Unlock()
		switch {
		case strings.Contains(r.URL.Path, "UserByScreenName"):
			_, _ = w.Write(userByResponse("4444"))
		case strings.Contains(r.URL.Path, "UserTweets"):
			_, _ = w.Write(buildTimeline(timelineSize))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	feedObj, err := queries.CreateFeed(context.Background(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "twitter",
		Name:   "Budget",
		Url:    "https://x.com/testuser",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	poller := NewPoller(queries)
	tf := NewTwitterFetcher()
	tf.baseURL = srv.URL
	tf.SetCredentials("ct0-test", "auth-test")
	tf.SetRateLimiter(nil) // count requests, do not pace them
	poller.fetchers["twitter"] = tf
	poller.enrichers["twitter"] = tf

	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return total
	}
	return poller, tf, feedObj, count
}

// TestTwitterPollRequestBudget asserts one poll of a 20-tweet timeline stays
// well inside X's UserTweets budget.
//
// X enforces per-endpoint limits in 15-minute windows: UserByScreenName 150 and
// UserTweets 50. The poller runs every 15 minutes and shares that budget across
// every twitter feed, so a single feed's cycle must cost a small number of
// requests — not one timeline fetch per item, which is what a missing
// BulkEnrich implementation causes.
func TestTwitterPollRequestBudget(t *testing.T) {
	const timelineSize = 20
	const maxRequestsPerPoll = 6

	poller, _, feedObj, count := newCountingTwitterPoller(t, timelineSize)

	if err := poller.PollFeed(context.Background(), feedObj); err != nil {
		t.Fatalf("PollFeed: %v", err)
	}

	got := count()
	if got > maxRequestsPerPoll {
		t.Errorf("a %d-tweet poll made %d GraphQL requests, want <= %d.\n"+
			"X allows 50 UserTweets per 15-min window and the poller runs every 15 min,\n"+
			"so the per-item Enrich fallback (2 requests per item, each re-downloading\n"+
			"the whole timeline) exhausts the budget. TwitterFetcher must implement\n"+
			"BulkEnricher so one timeline fetch scores every item.",
			timelineSize, got, maxRequestsPerPoll)
	}
	t.Logf("requests for a %d-tweet poll: %d (budget %d)", timelineSize, got, maxRequestsPerPoll)
}

// TestTwitterUserIDCache asserts the second poll does not re-resolve the
// screen name, so the cached user ID path is actually used.
func TestTwitterUserIDCache(t *testing.T) {
	poller, _, feedObj, count := newCountingTwitterPoller(t, 20)

	if err := poller.PollFeed(context.Background(), feedObj); err != nil {
		t.Fatalf("first PollFeed: %v", err)
	}
	first := count()

	// Second poll on the same fetcher: the screen name -> user ID mapping is
	// already known, so it must not issue another UserByScreenName request.
	if err := poller.PollFeed(context.Background(), feedObj); err != nil {
		t.Fatalf("second PollFeed: %v", err)
	}
	second := count() - first

	if second >= first {
		t.Errorf("second poll made %d requests, first made %d — cached user ID not used",
			second, first)
	}
	t.Logf("first poll: %d requests, second poll: %d requests", first, second)
}
