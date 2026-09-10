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

	// Every provider with a registered client is reported, even before any
	// response has been observed, so both twitter and reddit must be present.
	quotasRaw, ok := body["quotas"]
	if !ok {
		t.Fatalf("response missing quotas; got keys %v", keysOf(body))
	}
	quotas, ok := quotasRaw.([]any)
	if !ok {
		t.Fatalf("quotas is %T, want an array", quotasRaw)
	}
	byProvider := map[string]map[string]any{}
	for _, raw := range quotas {
		q, _ := raw.(map[string]any)
		if name, _ := q["provider"].(string); name != "" {
			byProvider[name] = q
		}
	}
	for _, provider := range []string{"twitter", "reddit"} {
		quota, ok := byProvider[provider]
		if !ok {
			t.Fatalf("no %s entry in quotas: %v", provider, quotas)
		}
		if quota["measured"] != false {
			t.Errorf("%s.measured = %v, want false before any response", provider, quota["measured"])
		}
		if quota["budget_low"] != false {
			t.Errorf("%s.budget_low = %v, want false without a measurement", provider, quota["budget_low"])
		}
		for _, key := range []string{"limit", "remaining", "reserve", "blocked"} {
			if _, ok := quota[key]; !ok {
				t.Errorf("%s missing %q; got %v", provider, key, quota)
			}
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
