package feed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"digesterr/internal/store"
)

// TestTwitter429RecordedAs4xx is the regression test for the metrics defect:
// a rate-limited poll must be recorded with its real status (429 → the 4xx
// bucket) instead of being flattened to 500, so throttling is distinguishable
// from a genuine upstream outage in the metrics UI.
func TestTwitter429RecordedAs4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "UserByScreenName"):
			_, _ = w.Write(userByResponse("4444"))
		default:
			// X returns 429 with a rate-limit body once the window is spent.
			w.Header().Set("X-Rate-Limit-Limit", "50")
			w.Header().Set("X-Rate-Limit-Remaining", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"Rate limit exceeded"}`))
		}
	}))
	t.Cleanup(srv.Close)

	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	feedObj, err := queries.CreateFeed(context.Background(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "twitter",
		Name:   "Throttled",
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
	tf.SetRateLimiter(nil)
	poller.fetchers["twitter"] = tf
	poller.enrichers["twitter"] = tf

	var mu sync.Mutex
	var recorded []Record
	poller.SetRecordFunc(func(rec Record) {
		mu.Lock()
		defer mu.Unlock()
		recorded = append(recorded, rec)
	})

	// A 429 may surface as an error from PollFeed or be absorbed into feed
	// status; either way the fetch attempt must have been recorded.
	_ = poller.PollFeed(context.Background(), feedObj)

	mu.Lock()
	defer mu.Unlock()

	if len(recorded) == 0 {
		t.Fatal("no metrics recorded for a throttled poll — the 429 would be invisible")
	}

	var saw429 bool
	for _, rec := range recorded {
		if rec.Reason != "fetch" {
			continue
		}
		if rec.StatusCode == 500 {
			t.Errorf("fetch recorded as 500 for a 429 response — rate limiting is masked as a server error (error: %s)", rec.Error)
		}
		if rec.StatusCode == http.StatusTooManyRequests {
			saw429 = true
		}
	}
	if !saw429 {
		for _, rec := range recorded {
			t.Logf("recorded: reason=%s host=%s status=%d err=%q", rec.Reason, rec.Host, rec.StatusCode, rec.Error)
		}
		t.Errorf("no fetch record carried status 429; want the real upstream status preserved")
	}
}
