package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestTransportCountsRequestsNotRecords is the regression test for the
// measurement gap: the metrics UI reported the number of recorded operations
// as if it were the number of HTTP requests, under-reporting real traffic by
// more than 3x (twitter enrichment fans out to one request per item).
func TestTransportCountsRequestsNotRecords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	t.Cleanup(srv.Close)

	tr := NewTransport(nil)
	client := &http.Client{Transport: tr}

	const calls = 7
	for i := 0; i < calls; i++ {
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
	}

	if got := tr.RequestsSnapshot(); got != calls {
		t.Errorf("RequestsSnapshot() = %d, want %d", got, calls)
	}

	respBytes, requests := tr.Consume()
	if requests != calls {
		t.Errorf("Consume() requests = %d, want %d", requests, calls)
	}
	if respBytes != int64(len("hello")*calls) {
		t.Errorf("Consume() bytes = %d, want %d", respBytes, len("hello")*calls)
	}

	// Consume must reset both counters.
	if b, r := tr.Consume(); b != 0 || r != 0 {
		t.Errorf("second Consume() = (%d, %d), want (0, 0)", b, r)
	}
}

// TestTransportCountsFailedRequests asserts a request that never gets a
// response is still counted — undercounting those is what hid the 429s.
func TestTransportCountsFailedRequests(t *testing.T) {
	tr := NewTransport(nil)
	client := &http.Client{Transport: tr}

	// A refused connection: no response at all.
	if resp, err := client.Get("http://127.0.0.1:1/"); err == nil {
		resp.Body.Close()
		t.Fatal("expected the request to fail")
	}

	if got := tr.RequestsSnapshot(); got != 1 {
		t.Errorf("RequestsSnapshot() = %d, want 1 (a failed request is still a request)", got)
	}
}

// TestSummaryReportsHTTPRequests asserts the summary exposes real request
// counts alongside the record count.
func TestSummaryReportsHTTPRequests(t *testing.T) {
	r := New(10)
	now := time.Now()
	r.Record(Record{Timestamp: now, StatusCode: 200, Reason: "fetch", Requests: 1})
	r.Record(Record{Timestamp: now, StatusCode: 200, Reason: "enrichment", Requests: 44})

	sum := r.Summary(now.Add(-time.Second), []string{"reason"}, time.Minute)
	if sum.TotalRequests != 2 {
		t.Fatalf("TotalRequests = %d, want 2 records", sum.TotalRequests)
	}
	if sum.TotalHTTPRequests != 45 {
		t.Errorf("TotalHTTPRequests = %d, want 45 (1 + 44)", sum.TotalHTTPRequests)
	}

	var fetchReqs, enrichReqs int64
	for _, g := range sum.Groups {
		switch g.Reason {
		case "fetch":
			fetchReqs = g.HTTPRequests
		case "enrichment":
			enrichReqs = g.HTTPRequests
		}
	}
	if fetchReqs != 1 || enrichReqs != 44 {
		t.Errorf("group HTTPRequests — fetch=%d enrichment=%d, want 1 and 44", fetchReqs, enrichReqs)
	}
}

// TestResetClearsBothCounters is the regression test for a metrics reset that
// only zeroed the byte counter: the stale request count then got attributed to
// the first poll after the reset, over-reporting outbound requests.
func TestResetClearsBothCounters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	t.Cleanup(srv.Close)

	rec := New(10)
	client := &http.Client{Transport: rec.Transport()}
	if _, err := client.Get(srv.URL); err != nil {
		t.Fatalf("request: %v", err)
	}

	rec.Record(Record{Timestamp: time.Now(), StatusCode: 200, Reason: "fetch", Requests: 1})

	rec.Reset()

	if bytes, requests := rec.Transport().Consume(); bytes != 0 || requests != 0 {
		t.Errorf("after Reset, Consume() = (%d, %d), want (0, 0) — a reset must clear both counters", bytes, requests)
	}

	// The records are gone too, so a summary right after the reset is empty.
	sum := rec.Summary(time.Now().Add(-time.Hour), []string{"reason"}, time.Minute)
	if sum.TotalRequests != 0 || sum.TotalHTTPRequests != 0 {
		t.Errorf("after Reset, summary = (%d records, %d http requests), want (0, 0)",
			sum.TotalRequests, sum.TotalHTTPRequests)
	}
}
