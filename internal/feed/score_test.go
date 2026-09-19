package feed

import (
	"testing"

	"digesterr/internal/store"
)

// A 0 from any endpoint means "unknown here", never a real score: applying a
// zero must not destroy a known score, while any nonzero score — up, down or
// equal — always applies.
func TestApplyScoreNeverRegressesToZero(t *testing.T) {
	cases := []struct {
		name string
		old  int64
		new  int64
		want int64
	}{
		{"zero stays zero", 0, 0, 0},
		{"zero takes score", 0, 12, 12},
		{"score survives zero", 12, 0, 12},
		{"score updates up", 12, 27, 27},
		{"score updates down", 27, 9, 9},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			item := &store.Item{Points: c.old}
			applyScore(item, c.new)
			if item.Points != c.want {
				t.Fatalf("applyScore(%d, %d) = %d, want %d", c.old, c.new, item.Points, c.want)
			}
		})
	}
}
