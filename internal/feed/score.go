package feed

import (
	"digesterr/internal/store"
)

// applyScore assigns a new score to an item without ever regressing a known
// score to zero. Vote counts are noisy across providers and endpoints (Reddit
// vote fuzzing: the listing says 12 while the comments endpoint says 0 for
// the same post), so 0 means "unknown here", not "the post is worthless".
func applyScore(item *store.Item, score int64) {
	if score > 0 || item.Points == 0 {
		item.Points = score
	}
}
