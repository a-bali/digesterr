package feed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// newBareTwitterFetcher returns a fetcher pointed at a mock X server.
func newBareTwitterFetcher(t *testing.T, handler http.HandlerFunc) *TwitterFetcher {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	f := NewTwitterFetcher()
	f.baseURL = srv.URL
	f.SetCredentials("ct0-test", "auth-test")
	f.SetRateLimiter(nil)
	return f
}

// quotaHandler serves UserByScreenName with the given X-Rate-Limit-* headers.
func quotaHandler(remaining string, reset time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Rate-Limit-Limit", "50")
		w.Header().Set("X-Rate-Limit-Remaining", remaining)
		w.Header().Set("X-Rate-Limit-Reset", strconv.FormatInt(reset.Unix(), 10))
		_, _ = w.Write(userByResponse("4444"))
	}
}

// TestTwitter429SetsBlockedUntil verifies a 429 stops further calls until the
// advertised reset, without issuing another HTTP request.
func TestTwitter429SetsBlockedUntil(t *testing.T) {
	var mu sync.Mutex
	calls := 0

	reset := time.Now().Add(2 * time.Minute)

	f := newBareTwitterFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "UserByScreenName"):
			_, _ = w.Write(userByResponse("4444"))
		case strings.Contains(r.URL.Path, "UserTweets"):
			w.Header().Set("X-Rate-Limit-Limit", "50")
			w.Header().Set("X-Rate-Limit-Remaining", "0")
			w.Header().Set("X-Rate-Limit-Reset", strconv.FormatInt(reset.Unix(), 10))
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"Rate limit exceeded"}`))
		default:
			http.NotFound(w, r)
		}
	})

	// First fetch: resolves the user, then hits a 429 on the timeline.
	_, err := f.Fetch(context.Background(), "https://x.com/testuser")
	if err == nil {
		t.Fatal("expected an error from the 429 fetch")
	}
	if code := StatusCodeOf(err); code != http.StatusTooManyRequests {
		t.Errorf("StatusCodeOf(err) = %d, want 429 (err: %v)", code, err)
	}
	until, blocked := f.Blocked()
	if !blocked {
		t.Fatal("expected the client to be blocked after a 429")
	}
	if until.Before(reset.Add(-time.Second)) {
		t.Errorf("blocked until %s, want at least the header reset %s", until, reset)
	}

	// Second fetch must not issue any further HTTP request.
	mu.Lock()
	before := calls
	mu.Unlock()

	_, err = f.Fetch(context.Background(), "https://x.com/testuser")
	if err == nil {
		t.Fatal("expected the blocked fetch to fail")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("blocked fetch error = %q, want it to mention rate limiting", err)
	}

	mu.Lock()
	after := calls
	mu.Unlock()
	if after != before {
		t.Errorf("blocked client issued %d more HTTP requests, want 0", after-before)
	}
}

// TestTwitterRecordsQuota verifies the X-Rate-Limit-* headers are parsed and
// drive BudgetLow.
func TestTwitterRecordsQuota(t *testing.T) {
	reset := time.Now().Add(10 * time.Minute)

	f := newBareTwitterFetcher(t, quotaHandler("40", reset))
	if _, err := f.userID(context.Background(), "testuser"); err != nil {
		t.Fatalf("userID: %v", err)
	}
	limit, remaining, gotReset := f.Quota()
	if limit != 50 || remaining != 40 {
		t.Errorf("Quota() = (%d, %d), want (50, 40)", limit, remaining)
	}
	if gotReset.IsZero() {
		t.Error("Quota() reset is zero, want the header value")
	}
	if f.BudgetLow(10) {
		t.Error("BudgetLow(10) = true with 40 remaining, want false")
	}

	// A client that has observed a low remaining value reports low budget.
	low := newBareTwitterFetcher(t, quotaHandler("2", reset))
	if _, err := low.userID(context.Background(), "testuser"); err != nil {
		t.Fatalf("userID: %v", err)
	}
	if !low.BudgetLow(10) {
		t.Error("BudgetLow(10) = false with 2 remaining, want true")
	}
}

// TestTwitterBudgetLowWithoutData asserts an unmeasured client is never
// suppressed: absence of quota data must not block enrichment.
func TestTwitterBudgetLowWithoutData(t *testing.T) {
	if NewTwitterFetcher().BudgetLow(10) {
		t.Error("BudgetLow on a client with no quota data = true, want false")
	}
}
