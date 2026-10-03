package linkify

import (
	"strings"
	"testing"
)

// realisticCommentsBlock mirrors what topCommentsHTML feeds into linkify.Text
// for one item: N comments of a few hundred characters, with a couple of bare
// URLs and a markdown link each (the shapes Reddit/HN comments actually carry).
func realisticCommentsBlock(n int) string {
	comment := "This is a fairly typical comment body. See https://example.com/some/path?x=1 for details, " +
		"and [the docs](https://docs.example.org/guide) too. Another link here: www.example.net/page. " +
		"Then some more prose to pad it out to a realistic length so the scan has work to do. " +
		"Also https://github.com/some/repo/pull/1234 and plain text after it.\n"
	return strings.Repeat(comment, n)
}

func BenchmarkText_CommentsBlock(b *testing.B) {
	s := realisticCommentsBlock(50) // ~50 comments in one block
	b.SetBytes(int64(len(s)))
	b.ReportAllocs()
	for b.Loop() {
		_ = Text(s)
	}
}

func BenchmarkText_LongComment(b *testing.B) {
	s := strings.Repeat(realisticCommentsBlock(1), 200)
	b.SetBytes(int64(len(s)))
	b.ReportAllocs()
	for b.Loop() {
		_ = Text(s)
	}
}

// BenchmarkText_NoLinks isolates the cost of the placeholder/restore machinery
// from the cost of the regex scans: no URLs at all means no generated tags.
func BenchmarkText_NoLinks(b *testing.B) {
	s := strings.Repeat("Just prose with no links at all in it, repeated to a realistic length. ", 400)
	b.SetBytes(int64(len(s)))
	b.ReportAllocs()
	for b.Loop() {
		_ = Text(s)
	}
}
