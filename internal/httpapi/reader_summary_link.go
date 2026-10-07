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
func (s *Server) summaryLinkHTML(r *http.Request, item store.Item) string {
	if s.summaryLinkMin <= 0 || itemVisibleTextLen(item) < s.summaryLinkMin {
		return ""
	}
	href := fmt.Sprintf("%s/items/%d/summary", s.webBaseURL(r), item.ID)
	return `<p class="digest-meta"><a href="` + html.EscapeString(href) + `" target="_blank" rel="noopener noreferrer">summarize</a></p>` + "\n"
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
