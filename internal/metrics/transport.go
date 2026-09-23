package metrics

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
)

// opKey is the context key under which an operation's Scope is carried.
// Unexported so nothing outside this package can collide with it.
type opKey struct{}

// Scope accumulates the request count and response bytes of ONE logical
// operation (a feed fetch, an enrichment pass, an extraction, an email sync).
//
// Why this exists: the poller used to read a single process-wide counter after
// each step. That works only while one operation runs at a time. Feeds now poll
// concurrently, so requests from different feeds are genuinely in flight
// together — whoever calls Consume first takes whatever happens to be in the
// counter, which can include another feed's requests and bytes. Per-operation
// attribution has to come from the REQUEST, not from a global tally.
//
// A Scope is safe for concurrent use because one operation may fan out (a
// Twitter timeline fetch per item runs as several requests).
type Scope struct {
	mu       sync.Mutex
	bytes    int64
	requests int64
}

// AddRequest records that one request was issued by this operation.
func (s *Scope) AddRequest() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.requests++
	s.mu.Unlock()
}

// AddBytes credits response bytes to this operation.
func (s *Scope) AddBytes(n int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.bytes += n
	s.mu.Unlock()
}

// Totals returns the accumulated response bytes and request count for this
// operation. It does NOT reset: a Scope belongs to one operation, is read once
// at the end, and is then discarded along with its context.
//
// The request count is an int to mirror Transport.Consume, so a scope is a
// drop-in replacement at the call sites that already consume counters.
func (s *Scope) Totals() (respBytes int64, requests int) {
	if s == nil {
		return 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bytes, int(s.requests)
}

// WithScope returns a context carrying scope, so every request built from it is
// attributed to that operation.
func WithScope(ctx context.Context, scope *Scope) context.Context {
	if scope == nil {
		return ctx
	}
	return context.WithValue(ctx, opKey{}, scope)
}

// ScopeFrom returns the Scope a context carries, or nil when it carries none.
func ScopeFrom(ctx context.Context) *Scope {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(opKey{}).(*Scope)
	return s
}

// NewScope starts a fresh operation scope.
func NewScope() *Scope { return &Scope{} }

// Transport is an http.RoundTripper wrapper that records outbound HTTP request
// count and response bytes.
//
// Attribution is per-operation when the request carries a Scope on its context;
// otherwise the traffic still moves the process-wide counters, so a path that
// has not adopted scopes is counted in the aggregate rather than silently lost.
//
// The request counter makes "how many requests are we making" a measurable fact
// instead of an estimate. One poll operation produces one metric record but can
// fan out into dozens of HTTP requests, so before this counter existed the
// metrics UI reported the record count as if it were the request count and
// under-reported real traffic by more than 3x.
type Transport struct {
	inner     http.RoundTripper
	respBytes atomic.Int64
	requests  atomic.Int64
}

// NewTransport wraps an http.RoundTripper to track request count and bytes.
func NewTransport(inner http.RoundTripper) *Transport {
	if inner == nil {
		inner = http.DefaultTransport
	}
	return &Transport{inner: inner}
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Count the attempt regardless of outcome. A refused dial or a timeout is
	// still a request we issued, and silently undercounting those is exactly
	// what hid the rate-limit problem.
	t.requests.Add(1)
	scope := ScopeFrom(req.Context())
	scope.AddRequest()

	resp, err := t.inner.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return resp, err
	}
	n := int64(len(body))
	t.respBytes.Add(n)
	scope.AddBytes(n)
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

// Consume atomically reads and resets both process-wide counters.
//
// With concurrent operations these counters cannot be attributed to any single
// one, so per-operation callers should carry a Scope instead. The aggregate
// remains meaningful, and it is the only place traffic from paths that carry no
// Scope is visible.
func (t *Transport) Consume() (respBytes, requests int64) {
	return t.respBytes.Swap(0), t.requests.Swap(0)
}

// Reset atomically clears both counters. Resetting only the byte counter
// would leave a stale request count behind, and the next poll would then report
// the previous window's requests as its own.
func (t *Transport) Reset() {
	t.respBytes.Store(0)
	t.requests.Store(0)
}

// RequestsSnapshot returns the current outbound request count.
func (t *Transport) RequestsSnapshot() int64 {
	return t.requests.Load()
}
