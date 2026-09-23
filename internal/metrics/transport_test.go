package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// TestScopeAttributesConcurrentOperations is the regression test for
// cross-feed misattribution: feeds poll concurrently now, so reading one shared
// counter after each fetch let a worker claim another feed's requests and bytes.
//
// Each operation must get EXACTLY its own traffic, whichever order they finish
// in. The shared counter can never do this — the totals below are read from the
// per-operation scopes, and the assertion is on the difference, not the order.
func TestScopeAttributesConcurrentOperations(t *testing.T) {
	// Two endpoints with distinguishable payload sizes, so a mix-up is visible in
	// the byte totals and not just the counts.
	small := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("s"))
	}))
	t.Cleanup(small.Close)
	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("L", 500)))
	}))
	t.Cleanup(large.Close)

	tr := NewTransport(nil)
	client := &http.Client{Transport: tr}

	// Two operations running at the same time, as two feeds do.
	smallScope, largeScope := NewScope(), NewScope()
	var wg sync.WaitGroup
	for _, op := range []struct {
		scope *Scope
		url   string
		calls int
	}{
		{smallScope, small.URL, 3},
		{largeScope, large.URL, 5},
	} {
		wg.Add(1)
		go func(scope *Scope, url string, calls int) {
			defer wg.Done()
			ctx := WithScope(context.Background(), scope)
			for i := 0; i < calls; i++ {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
				if err != nil {
					t.Errorf("build request: %v", err)
					return
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Errorf("request: %v", err)
					return
				}
				resp.Body.Close()
			}
		}(op.scope, op.url, op.calls)
	}
	wg.Wait()

	if b, r := smallScope.Totals(); r != 3 || b != 3 {
		t.Errorf("small op = (%d bytes, %d requests), want (3, 3)", b, r)
	}
	if b, r := largeScope.Totals(); r != 5 || b != 500*5 {
		t.Errorf("large op = (%d bytes, %d requests), want (%d, 5)", b, r, 500*5)
	}

	// The process-wide counters still see the aggregate, so traffic on a path
	// that carries no scope is not lost from the summary.
	if _, r := tr.Consume(); r != 8 {
		t.Errorf("aggregate requests = %d, want 8", r)
	}
}

// TestScopeTotalsAreNotConsumed: a scope belongs to one operation and is read
// once at the end. Unlike Consume, reading it must not zero it, or a second read
// (e.g. a deferred metrics record) would report zero traffic.
func TestScopeTotalsAreNotConsumed(t *testing.T) {
	s := NewScope()
	s.AddRequest()
	s.AddBytes(10)

	if b, r := s.Totals(); b != 10 || r != 1 {
		t.Fatalf("first Totals() = (%d, %d), want (10, 1)", b, r)
	}
	if b, r := s.Totals(); b != 10 || r != 1 {
		t.Errorf("second Totals() = (%d, %d), want (10, 1) — reading must not reset", b, r)
	}
}

// TestScopeNilSafety: a nil scope is what a caller gets when an operation has no
// scope, so every method must tolerate it rather than panic in production.
func TestScopeNilSafety(t *testing.T) {
	var s *Scope
	s.AddRequest()
	s.AddBytes(5)
	if b, r := s.Totals(); b != 0 || r != 0 {
		t.Errorf("nil scope Totals() = (%d, %d), want (0, 0)", b, r)
	}
	// A context with no scope yields nil, and requests through it must not panic.
	if got := ScopeFrom(context.Background()); got != nil {
		t.Errorf("ScopeFrom(background) = %v, want nil", got)
	}
	if got := WithScope(context.Background(), nil); got == nil {
		t.Error("WithScope(ctx, nil) must return the original context, not nil")
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
