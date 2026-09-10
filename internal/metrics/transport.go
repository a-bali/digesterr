package metrics

import (
	"bytes"
	"io"
	"net/http"
	"sync/atomic"
)

// Transport is an http.RoundTripper wrapper that records outbound HTTP request
// count and response bytes.
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

	resp, err := t.inner.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return resp, err
	}
	t.respBytes.Add(int64(len(body)))
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

// Consume atomically reads and resets both counters. Callers take both at once
// so request and byte counts stay attributed to the same operation.
func (t *Transport) Consume() (respBytes, requests int64) {
	return t.respBytes.Swap(0), t.requests.Swap(0)
}

// ResetBytes atomically resets the byte counter and returns the value.
func (t *Transport) ResetBytes() int64 {
	return t.respBytes.Swap(0)
}

// BytesSnapshot returns the current byte count.
func (t *Transport) BytesSnapshot() int64 {
	return t.respBytes.Load()
}

// RequestsSnapshot returns the current outbound request count.
func (t *Transport) RequestsSnapshot() int64 {
	return t.requests.Load()
}
