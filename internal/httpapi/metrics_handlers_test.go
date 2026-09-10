package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestMetricsAPIShape pins the /api/metrics response contract that the UI
// depends on: real request counts and twitter quota state are part of the
// payload, so a throttled feed is diagnosable from the API alone.
func TestMetricsAPIShape(t *testing.T) {
	server, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
	rec := httptest.NewRecorder()
	server.handleGetMetrics(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// A fresh server has no traffic, but the keys must exist so the UI can
	// distinguish "no data" from "field missing".
	for _, key := range []string{
		"total_requests",
		"total_http_requests",
		"total_bytes",
		"success_count",
		"error_count",
		"avg_duration_ms",
		"groups",
		"buckets",
	} {
		if _, ok := body[key]; !ok {
			t.Errorf("response missing %q; got keys %v", key, keysOf(body))
		}
	}

	// A poller always registers a twitter client, so quota state is reported
	// even before any response has been observed.
	quotaRaw, ok := body["twitter_quota"]
	if !ok {
		t.Fatalf("response missing twitter_quota; got keys %v", keysOf(body))
	}
	quota, ok := quotaRaw.(map[string]any)
	if !ok {
		t.Fatalf("twitter_quota is %T, want an object", quotaRaw)
	}
	if quota["configured"] != true {
		t.Errorf("twitter_quota.configured = %v, want true", quota["configured"])
	}
	if quota["measured"] != false {
		t.Errorf("twitter_quota.measured = %v, want false before any response", quota["measured"])
	}
	if quota["budget_low"] != false {
		t.Errorf("twitter_quota.budget_low = %v, want false without a measurement", quota["budget_low"])
	}
	for _, key := range []string{"limit", "remaining", "reserve", "blocked"} {
		if _, ok := quota[key]; !ok {
			t.Errorf("twitter_quota missing %q; got %v", key, quota)
		}
	}
}

// TestMetricsAPIRejectsBadSince keeps the existing validation behaviour intact.
func TestMetricsAPIRejectsBadSince(t *testing.T) {
	server, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/metrics?since=24h", nil)
	rec := httptest.NewRecorder()
	server.handleGetMetrics(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a non-RFC3339 since parameter", rec.Code)
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
