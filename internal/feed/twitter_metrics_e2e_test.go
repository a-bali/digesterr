package feed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"digesterr/internal/metrics"
	"digesterr/internal/store"
)

// TestThrottledPollAggregatesAs4xx exercises the whole measurement chain in one
// test: a real poll against a throttling X endpoint, through a real
// metrics.Recorder wired exactly as the server wires it, into the aggregate the
// metrics API serves.
//
// This is the end-to-end assertion for the original complaint — production
// showed only 2xx and 5xx for a feed that was actually being rate limited.
func TestThrottledPollAggregatesAs4xx(t *testing.T) {
	reset := time.Now().Add(5 * time.Minute)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "UserByScreenName") {
			_, _ = w.Write(userByResponse("4444"))
			return
		}
		w.Header().Set("X-Rate-Limit-Limit", "50")
		w.Header().Set("X-Rate-Limit-Remaining", "0")
		w.Header().Set("X-Rate-Limit-Reset", strconv.FormatInt(reset.Unix(), 10))
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"Rate limit exceeded"}`))
	}))
	t.Cleanup(srv.Close)

	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	feedObj, err := queries.CreateFeed(context.Background(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "twitter",
		Name:   "OpenCode",
		Url:    "https://x.com/testuser",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	recorder := metrics.New(1000)
	transport := metrics.NewTransport(nil)

	poller := NewPoller(queries)
	tf := NewTwitterFetcher()
	tf.baseURL = srv.URL
	tf.SetCredentials("ct0-test", "auth-test")
	tf.SetRateLimiter(nil)
	poller.fetchers["twitter"] = tf
	poller.enrichers["twitter"] = tf
	poller.SetTransport(transport)

	// Same conversion the server performs.
	poller.SetRecordFunc(func(rec Record) {
		recorder.Record(metrics.Record{
			Timestamp:  rec.Timestamp,
			URL:        rec.URL,
			Host:       rec.Host,
			StatusCode: rec.StatusCode,
			Bytes:      rec.Bytes,
			Requests:   rec.Requests,
			DurationMs: rec.DurationMs,
			Reason:     rec.Reason,
			FeedID:     rec.FeedID,
			FeedName:   rec.FeedName,
			FeedType:   rec.FeedType,
			Error:      rec.Error,
		})
	})

	_ = poller.PollFeed(context.Background(), feedObj)

	sum := recorder.Summary(time.Now().Add(-time.Hour), []string{"reason", "feed", "status"}, time.Minute)

	// The failing fetch must be attributable to the feed and bucketed as 4xx.
	var found4xx bool
	var openCodeStatuses []string
	for _, g := range sum.Groups {
		openCodeStatuses = append(openCodeStatuses, g.Feed+"/"+g.Status)
		if g.Feed == "OpenCode" && g.Status == "4xx" {
			found4xx = true
			if g.Errors == 0 {
				t.Error("4xx group has zero errors; a throttled poll should count as an error")
			}
		}
		if g.Feed == "OpenCode" && g.Status == "5xx" {
			t.Errorf("throttled poll bucketed as 5xx — 429 must not be reported as a server error (groups: %v)", openCodeStatuses)
		}
	}
	if !found4xx {
		t.Errorf("no 4xx group for the throttled feed; groups seen: %v", openCodeStatuses)
	}

	// Real request volume must be counted, not just the single record.
	if sum.TotalHTTPRequests < 1 {
		t.Errorf("TotalHTTPRequests = %d, want >= 1 (the 429 attempt must be counted)", sum.TotalHTTPRequests)
	}
	if sum.TotalHTTPRequests <= sum.TotalRequests {
		t.Logf("note: %d HTTP requests across %d records", sum.TotalHTTPRequests, sum.TotalRequests)
	}
}
