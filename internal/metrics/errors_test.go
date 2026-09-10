package metrics

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

var errPlain = errors.New("boom")

// TestStatusForBucketing documents the single decision point that keeps 4xx and
// 5xx distinguishable in recorded metrics.
func TestStatusForBucketing(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"no error", nil, 200},
		{"429 rate limit", StatusErrorf(http.StatusTooManyRequests, "rate limited"), 429},
		{"401 auth", StatusErrorf(http.StatusUnauthorized, "unauthorized"), 401},
		{"503 upstream", StatusErrorf(http.StatusServiceUnavailable, "unavailable"), 503},
		{"wrapped 429", WrapStatus(http.StatusTooManyRequests, errPlain), 429},
		{"plain error is 500", errPlain, 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StatusFor(tc.err); got != tc.want {
				t.Errorf("StatusFor(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// TestStatusCodeOf checks extraction, including through a wrapping error.
func TestStatusCodeOf(t *testing.T) {
	if got := StatusCodeOf(nil); got != 0 {
		t.Errorf("StatusCodeOf(nil) = %d, want 0", got)
	}
	if got := StatusCodeOf(errPlain); got != 0 {
		t.Errorf("StatusCodeOf(plain) = %d, want 0 (no status attached)", got)
	}
	se := StatusErrorf(429, "rate limited")
	if got := StatusCodeOf(se); got != 429 {
		t.Errorf("StatusCodeOf(StatusError) = %d, want 429", got)
	}
	// The message must survive verbatim so log greps keep working.
	if got := se.Error(); got != "rate limited" {
		t.Errorf("Error() = %q, want %q", got, "rate limited")
	}

	// Wrapping must not lose the status (errors.As traversal).
	wrapped := fmt.Errorf("poll feed: %w", se)
	if got := StatusCodeOf(wrapped); got != 429 {
		t.Errorf("StatusCodeOf(wrapped) = %d, want 429", got)
	}
}

// TestWrapStatusPreservesMessage asserts WrapStatus keeps the original text.
func TestWrapStatusPreservesMessage(t *testing.T) {
	orig := errors.New("dial tcp: connection refused")
	w := WrapStatus(http.StatusBadGateway, orig)
	if w.Error() != orig.Error() {
		t.Errorf("WrapStatus message = %q, want %q", w.Error(), orig.Error())
	}
	if got := StatusCodeOf(w); got != http.StatusBadGateway {
		t.Errorf("StatusCodeOf(WrapStatus) = %d, want 502", got)
	}
	if WrapStatus(500, nil) != nil {
		t.Error("WrapStatus(nil) should return nil")
	}
}
