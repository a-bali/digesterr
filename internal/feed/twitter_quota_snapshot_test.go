package feed

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestTwitterQuotaSnapshotExposed asserts the metrics API can see the twitter
// client's rate-limit headroom, so quota exhaustion is diagnosable from the
// API instead of only from the absence of traffic.
func TestTwitterQuotaSnapshotExposed(t *testing.T) {
	reset := time.Now().Add(10 * time.Minute)
	srv := httptest.NewServer(quotaHandler("3", reset))
	t.Cleanup(srv.Close)

	tf := NewTwitterFetcher()
	tf.baseURL = srv.URL
	tf.SetCredentials("ct0-test", "auth-test")
	tf.SetRateLimiter(nil)

	poller := NewPoller(nil)
	// A freshly constructed poller registers a twitter client but has not yet
	// observed any response, so it must report configured-but-unmeasured.
	snap := poller.TwitterQuota()
	if !snap.Configured {
		t.Fatal("Configured = false with the default twitter fetcher registered")
	}
	if snap.Measured {
		t.Error("Measured = true before any response was seen")
	}
	if snap.BudgetLow {
		t.Error("BudgetLow = true with no measurement — an unmeasured client must not look exhausted")
	}

	// Register the mock-backed client, then observe a real response so the
	// X-Rate-Limit-* headers get parsed.
	poller.enrichers["twitter"] = tf
	if _, err := tf.userID(t.Context(), "testuser"); err != nil {
		t.Fatalf("userID: %v", err)
	}

	snap = poller.TwitterQuota()
	if !snap.Measured {
		t.Fatal("Measured = false after a response carrying quota headers")
	}
	if snap.Limit != 50 || snap.Remaining != 3 {
		t.Errorf("Limit/Remaining = %d/%d, want 50/3", snap.Limit, snap.Remaining)
	}
	if !snap.BudgetLow {
		t.Errorf("BudgetLow = false with 3 remaining and reserve %d", snap.Reserve)
	}
	if snap.Blocked {
		t.Error("Blocked = true without a 429")
	}

	// The snapshot must survive JSON encoding for the API response.
	blob, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"configured":true`, `"measured":true`, `"remaining":3`, `"budget_low":true`} {
		if !strings.Contains(string(blob), want) {
			t.Errorf("snapshot JSON %s missing %s", blob, want)
		}
	}
}

// TestTwitterQuotaSnapshotBlocked asserts a 429 is visible as blocked state
// with the reset time the API consumer needs.
func TestTwitterQuotaSnapshotBlocked(t *testing.T) {
	reset := time.Now().Add(3 * time.Minute)
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

	tf := NewTwitterFetcher()
	tf.baseURL = srv.URL
	tf.SetCredentials("ct0-test", "auth-test")
	tf.SetRateLimiter(nil)

	poller := NewPoller(nil)
	poller.enrichers["twitter"] = tf

	_, _ = tf.Fetch(t.Context(), "https://x.com/testuser")

	snap := poller.TwitterQuota()
	if !snap.Blocked {
		t.Fatal("Blocked = false after a 429")
	}
	if snap.BlockedUntil.IsZero() {
		t.Error("BlockedUntil is zero; the API consumer needs the reset time")
	}
	if snap.BlockedUntil.Before(reset.Add(-time.Second)) {
		t.Errorf("BlockedUntil = %s, want at least the header reset %s", snap.BlockedUntil, reset)
	}
}
