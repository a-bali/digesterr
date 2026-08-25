package feed

import (
	"context"

	"golang.org/x/time/rate"
)

// Rate limiter defaults per enrichment provider. These are conservative
// ceilings chosen to stay well below each provider's documented limits
// while still allowing a full enrichment cycle to finish quickly.
const (
	// HN Firebase per-item fallback (Firebase handles high throughput).
	defaultHNRate  = 20.0
	defaultHNburst = 10
	// Reddit OAuth API: 60 req/min documented → 1 rps is safe.
	defaultRedditRate  = 1.0
	defaultRedditBurst = 5
	// Twitter GraphQL: unauthenticated cookie session, keep slow.
	defaultTwitterRate  = 1.0
	defaultTwitterBurst = 3
)

// waitLimiter blocks until the limiter allows the next call. A nil limiter
// means "no limiting" and returns immediately.
func waitLimiter(ctx context.Context, l *rate.Limiter) error {
	if l == nil {
		return nil
	}
	return l.Wait(ctx)
}
