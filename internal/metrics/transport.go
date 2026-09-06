package metrics

import (
	"bytes"
	"io"
	"net/http"
	"sync/atomic"
)

// Transport is an http.RoundTripper wrapper that records response bytes
// for every outbound HTTP request passing through it.
type Transport struct {
	inner http.RoundTripper
	bYTES atomic.Int64
}

// NewTransport wraps an http.RoundTripper to track response bytes.
func NewTransport(inner http.RoundTripper) *Transport {
	if inner == nil {
		inner = http.DefaultTransport
	}
	return &Transport{inner: inner}
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.inner.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return resp, err
	}
	t.bYTES.Add(int64(len(body)))
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

// ResetBytes atomically resets the byte counter and returns the value.
func (t *Transport) ResetBytes() int64 {
	return t.bYTES.Swap(0)
}

// BytesSnapshot returns the current byte count.
func (t *Transport) BytesSnapshot() int64 {
	return t.bYTES.Load()
}
