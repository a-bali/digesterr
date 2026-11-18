package httpapi

import (
	"fmt"
	"html"
	"net/http"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

// summaryLinkHTML renders a Reader-API link when the item has enough visible
// text to justify a summary. The target is the authenticated digesterr SPA,
// since a browser tab opened by a feed reader cannot carry the Reader API key.
//
// The label is the same glyph the web UI puts on its Summarize control (a
// U+2726 four-pointed star), so the Reader API and the app read as one feature
// rather than two. It is a single character because this link sits at the top
// of the served body, and a Reader client renders `summary` as both the
// list-row snippet and the article — a word there is noise in the first
// context. The title/aria-label carry the meaning for anything that does not
// render the glyph, and for screen readers.
func (s *Server) summaryLinkHTML(r *http.Request, item store.Item) string {
	if s.summaryLinkMin <= 0 || itemVisibleTextLen(item) < s.summaryLinkMin {
		return ""
	}
	href := fmt.Sprintf("%s/items/%d/summary", s.webBaseURL(r), item.ID)
	return `<p class="digest-meta"><a href="` + html.EscapeString(href) +
		`" target="_blank" rel="noopener noreferrer" title="Summarize" aria-label="Summarize">` +
		"\u2726" + `</a></p>` + "\n"
}

// itemVisibleTextLen measures the same visible body that the Reader API serves,
// preferring a completed extraction and falling back to the original content.
func itemVisibleTextLen(item store.Item) int {
	if item.ExtractStatus == "done" && item.ExtractContent != "" {
		if n := feed.ExcerptTextLen(item.ExtractContent); n > 0 {
			return n
		}
	}
	return feed.ExcerptTextLen(item.Content)
}
