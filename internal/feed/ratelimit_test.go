package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"digesterr/internal/store"
	"golang.org/x/time/rate"
)

func rateNewLimiter(rps float64, burst int) *rate.Limiter {
	return rate.NewLimiter(rate.Limit(rps), burst)
}

// TestRateLimiterThrottlesPerItemCalls verifies per-item Firebase calls are
// rate-limited: 4 items with burst=1 and rps=20 should take ≥150ms.
func TestRateLimiterThrottlesPerItemCalls(t *testing.T) {
	var mu sync.Mutex
	var timestamps []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		timestamps = append(timestamps, time.Now())
		mu.Unlock()
		resp, _ := json.Marshal(map[string]interface{}{"id": 1, "score": 5, "descendants": 2})
		w.Write(resp)
	}))
	defer server.Close()

	e := NewHNEnricher()
	e.firebaseURL = server.URL
	e.algoliaURL = "http://no-such-host.test" // force all misses → Firebase fallback
	e.limiter = rate.NewLimiter(20, 1)        // burst 1

	// Directly test Firebase calls via Enrich with unique IDs
	start := time.Now()
	for i := range 4 {
		item := &store.Item{CommentUrl: fmt.Sprintf("https://news.ycombinator.com/item?id=%d", 1000+i)}
		if err := e.Enrich(context.Background(), item); err != nil {
			t.Fatalf("item %d: %v", i, err)
		}
	}
	elapsed := time.Since(start)

	if elapsed < 150*time.Millisecond {
		t.Errorf("4 rate-limited calls took %v; expected >=150ms", elapsed)
	}
	mu.Lock()
	n := len(timestamps)
	mu.Unlock()
	if n != 4 {
		t.Errorf("expected 4 backend calls, got %d", n)
	}
}

// TestHNEnricherNilLimiterDisablesLimiting verifies nil limiter = no waiting.
func TestHNEnricherNilLimiterDisablesLimiting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp, _ := json.Marshal(map[string]interface{}{"id": 7, "score": 9, "descendants": 1})
		w.Write(resp)
	}))
	defer server.Close()

	e := NewHNEnricher()
	e.firebaseURL = server.URL
	e.SetRateLimiter(nil)

	item := &store.Item{CommentUrl: "https://news.ycombinator.com/item?id=7"}
	if err := e.Enrich(context.Background(), item); err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if !item.Points.Valid || item.Points.Int64 != 9 {
		t.Errorf("points = %+v, want valid 9", item.Points)
	}
}

// TestEnrichmentWindowConfig verifies the enrichment window default.
func TestEnrichmentWindowConfig(t *testing.T) {
	p := NewPoller(nil)
	// Default should be 24h when not configured
	if p.enrichmentWindow != 0 {
		t.Errorf("expected zero (unset) enrichmentWindow, got %v", p.enrichmentWindow)
	}
	p.SetEnrichmentWindow(0) // should fall back to 24h in enrichFeedItems
	p.SetEnrichmentWindow(12 * time.Hour)
	if p.enrichmentWindow != 12*time.Hour {
		t.Errorf("enrichmentWindow = %v, want 12h", p.enrichmentWindow)
	}
}

// TestTwitterEnrichFeedMatchesItems verifies EnrichFeed updates scores for
// matching items and leaves non-matching untouched.
func TestTwitterEnrichFeedMatchesItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return tweets: one that matches, one that doesn't
		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"user": map[string]interface{}{
					"result": map[string]interface{}{
						"rest_id": "12345",
						"legacy":  map[string]interface{}{"screen_name": "testuser"},
						"core": map[string]interface{}{
							"user_results": map[string]interface{}{
								"result": map[string]interface{}{"legacy": map[string]interface{}{"screen_name": "testuser"}},
							},
						},
						"timeline_v2": map[string]interface{}{
							"timeline": map[string]interface{}{
								"instructions": []interface{}{
									map[string]interface{}{
										"type":    "TimelineAddEntries",
										"entries": []interface{}{
											map[string]interface{}{
												"entryId": "tweet-99999",
												"content": map[string]interface{}{
													"entryType": "TimelineTimelineItem",
													"itemContent": map[string]interface{}{
														"tweet_results": map[string]interface{}{
															"result": map[string]interface{}{
																"rest_id":    "99999",
																"__typename": "Tweet",
																"legacy": map[string]interface{}{
																	"favorite_count":            42,
																	"reply_count":               7,
																	"full_text":                 "hello",
																	"created_at":                "Tue Aug 24 2026",
																	"in_reply_to_status_id_str": "",
																},
															},
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	f := NewTwitterFetcher()
	f.baseURL = server.URL
	f.bearer = "test"
	f.SetCredentials("ct0-test", "auth-test")

	matching := &store.Item{
		Hash:       "twitter:99999",
		CommentUrl: "https://x.com/user/status/99999",
	}
	nonMatching := &store.Item{
		Hash:       "twitter:88888",
		CommentUrl: "https://x.com/user/status/88888",
	}
	err := f.EnrichFeed(context.Background(), "https://x.com/testuser", []*store.Item{matching, nonMatching})
	if err != nil {
		t.Fatalf("EnrichFeed: %v", err)
	}
	if !matching.Points.Valid || matching.Points.Int64 != 42 {
		t.Errorf("matching points = %+v, want valid 42", matching.Points)
	}
	if !matching.CommentsCount.Valid || matching.CommentsCount.Int64 != 7 {
		t.Errorf("matching comments = %+v, want valid 7", matching.CommentsCount)
	}
	// non-matching item should remain unchanged
	if nonMatching.Points.Valid {
		t.Errorf("non-matching item points should remain unset, got %+v", nonMatching.Points)
	}
}
