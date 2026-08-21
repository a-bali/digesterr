package digest

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
)

// digestRenderContent is the shape of a digest result's stored Content JSON.
// It mirrors digestResultContent (snake_case field names) but is defined
// separately so rendering works from raw stored JSON without depending on the
// generator's in-memory types.
type digestRenderContent struct {
	Title      string              `json:"title,omitempty"`
	Summary    string              `json:"summary"`
	Sections   []digestSectionJSON `json:"sections,omitempty"`
	Highlights []digestContentRef  `json:"highlights"`
	Items      []digestContentRef  `json:"items"`
}

// digestSectionJSON mirrors internal/digest.digestSection: category sections
// nest feed sections, feed sections carry the highlights/items.
type digestSectionJSON struct {
	Name       string              `json:"name"`
	Kind       string              `json:"kind"`
	FeedID     int64               `json:"feed_id,omitempty"`
	Sections   []digestSectionJSON `json:"sections,omitempty"`
	Highlights []digestContentRef  `json:"highlights,omitempty"`
	Items      []digestContentRef  `json:"items,omitempty"`
}

type digestContentRef struct {
	ItemID        int64                  `json:"item_id"`
	FeedID        int64                  `json:"feed_id"`
	FeedName      string                 `json:"feed_name"`
	Title         string                 `json:"title"`
	URL           string                 `json:"url"`
	CommentURL    string                 `json:"comment_url"`
	Points        int64                  `json:"points"`
	CommentsCount int64                  `json:"comments_count"`
	PublishedAt   string                 `json:"published_at"`
	LLMSummary    string                 `json:"llm_summary"`
	TopComments   []digestContentComment `json:"top_comments"`
}

type digestContentComment struct {
	Author string `json:"author"`
	Text   string `json:"text"`
	Points *int   `json:"points"`
	URL    string `json:"url"`
}

// RenderDigestHTML renders a digest result's content JSON as HTML. All
// user-controlled strings are escaped to prevent HTML injection. When the
// content carries sections (category/feed grouping), they are rendered in
// order; older content without sections falls back to the flat layout.
// feedBaseURL is the digesterr web UI base URL used for feed-name links.
func RenderDigestHTML(contentJSON, feedBaseURL string) string {
	var c digestRenderContent
	if err := json.Unmarshal([]byte(contentJSON), &c); err != nil {
		c = digestRenderContent{}
	}

	var b strings.Builder

	if c.Title != "" {
		fmt.Fprintf(&b, `<h1 class="digest-title">%s</h1>`+"\n", html.EscapeString(c.Title))
	}

	if c.Summary != "" {
		fmt.Fprintf(&b, `<div class="digest-summary">%s</div>`+"\n", html.EscapeString(c.Summary))
	}

	if len(c.Sections) > 0 {
		for _, sec := range c.Sections {
			renderDigestSectionHTML(&b, sec, 2, feedBaseURL)
		}
	} else {
		renderDigestFlatHTML(&b, c.Highlights, c.Items, feedBaseURL)
	}

	return b.String()
}

// renderDigestSectionHTML renders one digest section (category or feed) as
// HTML. Category sections emit a heading and recurse into their feed sections;
// feed sections emit a heading followed by highlights and the item list.
func renderDigestSectionHTML(b *strings.Builder, sec digestSectionJSON, level int, feedBaseURL string) {
	name := html.EscapeString(sec.Name)
	if sec.Kind == "category" {
		fmt.Fprintf(b, `<h%d>%s</h%d>`+"\n", level, name, level)
		for _, sub := range sec.Sections {
			renderDigestSectionHTML(b, sub, level+1, feedBaseURL)
		}
		return
	}
	// Feed section.
	fmt.Fprintf(b, `<h%d>%s</h%d>`+"\n", level, name, level)
	renderDigestFlatHTML(b, sec.Highlights, sec.Items, feedBaseURL)
}

// renderDigestFlatHTML renders highlight cards followed by the plain item
// list, mirroring the historical digest layout. Each highlight shows the
// title (linking out), a feed-name link into digesterr, a date-time and
// points/comments line, the LLM summary, and top comments.
func renderDigestFlatHTML(b *strings.Builder, highlights, items []digestContentRef, feedBaseURL string) {
	for _, h := range highlights {
		title := html.EscapeString(h.Title)
		if h.URL != "" {
			fmt.Fprintf(b, `<h3><a href="%s">%s</a></h3>`+"\n", html.EscapeString(h.URL), title)
		} else {
			fmt.Fprintf(b, `<h3>%s</h3>`+"\n", title)
		}

		// Feed-name link into digesterr (next to the title).
		if h.FeedID > 0 {
			feedURL := fmt.Sprintf("%s/feeds/%d", feedBaseURL, h.FeedID)
			feedName := html.EscapeString(h.FeedName)
			if feedName == "" {
				feedName = "feed"
			}
			fmt.Fprintf(b, `<p class="digest-feed"><a href="%s">%s</a></p>`+"\n", html.EscapeString(feedURL), feedName)
		}

		// Date-time, then points and comments, on one line.
		var parts []string
		if h.PublishedAt != "" {
			parts = append(parts, html.EscapeString(h.PublishedAt))
		}
		if h.Points > 0 {
			parts = append(parts, fmt.Sprintf("%d points", h.Points))
		}
		if h.CommentsCount > 0 {
			commentsLabel := fmt.Sprintf("%d comments", h.CommentsCount)
			if h.CommentURL != "" {
				parts = append(parts, fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(h.CommentURL), commentsLabel))
			} else {
				parts = append(parts, commentsLabel)
			}
		}
		if len(parts) > 0 {
			fmt.Fprintf(b, `<p class="digest-meta">%s</p>`+"\n", strings.Join(parts, " · "))
		}

		if h.LLMSummary != "" {
			fmt.Fprintf(b, `<p>%s</p>`+"\n", html.EscapeString(h.LLMSummary))
		}

		if len(h.TopComments) > 0 {
			b.WriteString(`<h4>Top comments</h4>` + "\n")
			b.WriteString(`<ul>`)
			for _, tc := range h.TopComments {
				author := html.EscapeString(tc.Author)
				if tc.URL != "" {
					author = fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(tc.URL), author)
				}
				points := ""
				if tc.Points != nil {
					points = fmt.Sprintf(" (%d)", *tc.Points)
				}
				fmt.Fprintf(b, `<li>%s — %s%s</li>`, author, html.EscapeString(tc.Text), points)
			}
			b.WriteString(`</ul>` + "\n")
		}
	}

	if len(items) > 0 {
		b.WriteString(`<hr/>` + "\n")
		b.WriteString(`<h4>All items</h4>` + "\n")
		b.WriteString(`<ul>`)
		for _, it := range items {
			title := html.EscapeString(it.Title)
			if it.URL != "" {
				fmt.Fprintf(b, `<li><a href="%s">%s</a>`, html.EscapeString(it.URL), title)
			} else {
				fmt.Fprintf(b, `<li>%s`, title)
			}

			// Meta line with date, score and comments, mirroring the
			// highlight layout (see the highlight block above).
			var parts []string
			if it.PublishedAt != "" {
				parts = append(parts, html.EscapeString(it.PublishedAt))
			}
			if it.Points > 0 {
				parts = append(parts, fmt.Sprintf("%d points", it.Points))
			}
			if it.CommentsCount > 0 {
				commentsLabel := fmt.Sprintf("%d comments", it.CommentsCount)
				if it.CommentURL != "" {
					parts = append(parts, fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(it.CommentURL), commentsLabel))
				} else {
					parts = append(parts, commentsLabel)
				}
			}
			if len(parts) > 0 {
				fmt.Fprintf(b, ` <span class="digest-meta">%s</span>`, strings.Join(parts, " · "))
			}
			b.WriteString(`</li>` + "\n")
		}
		b.WriteString(`</ul>` + "\n")
	}
}
