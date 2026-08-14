package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	"digesterr/internal/store"
)

// readerContentsResponse is the top-level envelope for both stream/items/contents
// and stream/contents responses.
type readerContentsResponse struct {
	Direction string                    `json:"direction"`
	Items     []readerContentStreamItem `json:"items"`
}

// readerContentStreamItem is a single full content object, mirroring Google
// Reader's stream item content shape.
type readerContentStreamItem struct {
	ID            string            `json:"id"`
	CrawlTimeMsec string            `json:"crawlTimeMsec"`
	TimestampUsec string            `json:"timestampUsec"`
	Published     int64             `json:"published"`
	Updated       int64             `json:"updated"`
	Title         string            `json:"title"`
	Canonical     []readerCanonical `json:"canonical"`
	Alternate     []readerAlternate `json:"alternate"`
	Summary       readerContentBody `json:"summary"`
	Content       readerContentBody `json:"content"`
	Author        string            `json:"author"`
	Origin        readerOrigin      `json:"origin"`
	Categories    []string          `json:"categories"`
	Annotations   []string          `json:"annotations"`
}

// readerCanonical is the canonical URL reference; href is always present even
// when empty (Google Reader clients expect the key).
type readerCanonical struct {
	Href string `json:"href"`
}

type readerAlternate struct {
	Href string `json:"href"`
	Type string `json:"type"`
}

type readerContentBody struct {
	Content string `json:"content"`
}

type readerOrigin struct {
	StreamID string `json:"streamId"`
	Title    string `json:"title"`
	HTMLURL  string `json:"htmlUrl"`
}

// handleReaderStreamItemsContents implements
// GET /reader/api/0/stream/items/contents?i=<id>&i=<id>&output=json.
//
// Each i param resolves to a full content object; unknown or invalid ids are
// silently skipped while preserving input order.
func (s *Server) handleReaderStreamItemsContents(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	ids := r.URL.Query()["i"]
	items := make([]readerContentStreamItem, 0, len(ids))
	for _, rawID := range ids {
		if obj, ok := s.resolveContentObject(r, userID, rawID); ok {
			items = append(items, obj)
		}
	}

	writeJSON(w, http.StatusOK, readerContentsResponse{Direction: "ltr", Items: items})
}

// handleReaderStreamContents implements
// GET /reader/api/0/stream/contents/{streamID}?n=<count>&output=json.
//
// It reuses Task 4's stream resolution and renders full content objects for
// the first n entries instead of itemRefs. No continuation is needed here.
func (s *Server) handleReaderStreamContents(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	stream := r.PathValue("streamID")

	n := 20
	if v := r.URL.Query().Get("n"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}
	if n > 100 {
		n = 100
	}

	entries, err := s.streamEntries(r, userID, stream, false)
	if err != nil {
		writeServerError(w, "failed to load stream", err)
		return
	}
	if len(entries) > n {
		entries = entries[:n]
	}

	items := make([]readerContentStreamItem, 0, len(entries))
	for _, e := range entries {
		if obj, ok := s.resolveContentObject(r, userID, e.id); ok {
			items = append(items, obj)
		}
	}

	writeJSON(w, http.StatusOK, readerContentsResponse{Direction: "ltr", Items: items})
}

// resolveContentObject maps a stream-item id ("item/<id>" or "digest-item/<id>")
// to a full content object. It returns ok=false for unknown, malformed, or
// cross-user ids so callers can skip them silently.
func (s *Server) resolveContentObject(r *http.Request, userID int64, rawID string) (readerContentStreamItem, bool) {
	ctx := r.Context()

	if rest, ok := strings.CutPrefix(rawID, "digest-item/"); ok {
		id, err := strconv.ParseInt(rest, 10, 64)
		if err != nil {
			return readerContentStreamItem{}, false
		}
		dr, err := s.queries.GetDigestResult(ctx, store.GetDigestResultParams{ID: id, UserID: userID})
		if err != nil {
			return readerContentStreamItem{}, false
		}
		return s.digestContentObject(ctx, userID, dr), true
	}

	if rest, ok := strings.CutPrefix(rawID, "item/"); ok {
		id, err := strconv.ParseInt(rest, 10, 64)
		if err != nil {
			return readerContentStreamItem{}, false
		}
		item, err := s.queries.GetItemWithFeedOwner(ctx, store.GetItemWithFeedOwnerParams{ID: id, UserID: userID})
		if err != nil {
			return readerContentStreamItem{}, false
		}
		return s.itemContentObject(ctx, item), true
	}

	return readerContentStreamItem{}, false
}

// itemContentObject builds a content object for a regular feed item.
func (s *Server) itemContentObject(ctx context.Context, item store.Item) readerContentStreamItem {
	feedName := ""
	if f, err := s.queries.GetFeedByID(ctx, item.FeedID); err == nil {
		feedName = f.Name
	}

	var published int64
	if item.PublishedAt.Valid {
		published = item.PublishedAt.Time.Unix()
	}

	// Prefer extracted content when extraction completed successfully.
	body := item.Content
	if item.ExtractContent != "" && item.ExtractStatus == "done" {
		body = item.ExtractContent
	}

	ts := itemTimestamp(item)

	return readerContentStreamItem{
		ID:            itemStreamID(item.ID),
		CrawlTimeMsec: strconv.FormatInt(ts.UnixMilli(), 10),
		TimestampUsec: strconv.FormatInt(ts.UnixMicro(), 10),
		Published:     published,
		Updated:       item.UpdatedAt.Unix(),
		Title:         item.Title,
		Canonical:     []readerCanonical{{Href: item.Url}},
		Alternate:     []readerAlternate{{Href: item.Url, Type: "text/html"}},
		Summary:       readerContentBody{Content: item.Content},
		Content:       readerContentBody{Content: body},
		Author:        item.Author,
		Origin:        readerOrigin{StreamID: feedStreamID(item.FeedID), Title: feedName, HTMLURL: ""},
		Categories:    itemCategories(item.Status),
		Annotations:   []string{},
	}
}

// digestContentObject builds a content object for a digest result.
func (s *Server) digestContentObject(ctx context.Context, userID int64, dr store.DigestResult) readerContentStreamItem {
	configName := fmt.Sprintf("Digest %d", dr.ConfigID)
	if cfg, err := s.queries.GetDigestConfig(ctx, store.GetDigestConfigParams{ID: dr.ConfigID, UserID: userID}); err == nil {
		if cfg.Name != "" {
			configName = cfg.Name
		}
	}

	summary := digestSummary(dr.Content)

	created := dr.CreatedAt

	return readerContentStreamItem{
		ID:            digestItemStreamID(dr.ID),
		CrawlTimeMsec: strconv.FormatInt(created.UnixMilli(), 10),
		TimestampUsec: strconv.FormatInt(created.UnixMicro(), 10),
		Published:     created.Unix(),
		Updated:       created.Unix(),
		Title:         "Digest: " + configName,
		Canonical:     []readerCanonical{},
		Alternate:     []readerAlternate{},
		Summary:       readerContentBody{Content: summary},
		Content:       readerContentBody{Content: renderDigestHTML(dr.Content)},
		Author:        "",
		Origin:        readerOrigin{StreamID: digestStreamID(dr.ConfigID), Title: configName, HTMLURL: ""},
		Categories:    digestCategories(dr.Status),
		Annotations:   []string{},
	}
}

// itemCategories maps an item status to its Google Reader state tags.
func itemCategories(status string) []string {
	switch status {
	case "unread":
		return []string{readerStreamReadingList}
	case "saved":
		return []string{readerStreamRead, readerStreamStarred}
	default: // read, digested
		return []string{readerStreamRead}
	}
}

// digestCategories maps a digest result status to its Google Reader state tags.
func digestCategories(status string) []string {
	if status == "read" {
		return []string{readerStreamRead}
	}
	return []string{readerStreamReadingList}
}

// digestContentJSON is the shape of a digest result's stored Content JSON. It
// mirrors internal/digest.digestResultContent (snake_case field names).
type digestContentJSON struct {
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

// digestSummary returns the digest's summary field, tolerating malformed JSON.
func digestSummary(contentJSON string) string {
	var c digestContentJSON
	if err := json.Unmarshal([]byte(contentJSON), &c); err != nil {
		return ""
	}
	return c.Summary
}

// renderDigestHTML renders a digest result's content JSON as HTML. All
// user-controlled strings are escaped to prevent HTML injection. When the
// content carries sections (category/feed grouping), they are rendered in
// order; older content without sections falls back to the flat layout.
func renderDigestHTML(contentJSON string) string {
	var c digestContentJSON
	if err := json.Unmarshal([]byte(contentJSON), &c); err != nil {
		c = digestContentJSON{}
	}

	var b strings.Builder

	if c.Summary != "" {
		fmt.Fprintf(&b, `<div class="digest-summary">%s</div>`+"\n", html.EscapeString(c.Summary))
	}

	if len(c.Sections) > 0 {
		for _, sec := range c.Sections {
			renderDigestSectionHTML(&b, sec, 2)
		}
	} else {
		renderDigestFlatHTML(&b, c.Highlights, c.Items)
	}

	return b.String()
}

// renderDigestSectionHTML renders one digest section (category or feed) as
// HTML. Category sections emit a heading and recurse into their feed sections;
// feed sections emit a heading followed by highlights and the item list.
func renderDigestSectionHTML(b *strings.Builder, sec digestSectionJSON, level int) {
	name := html.EscapeString(sec.Name)
	if sec.Kind == "category" {
		fmt.Fprintf(b, `<h%d>%s</h%d>`+"\n", level, name, level)
		for _, sub := range sec.Sections {
			renderDigestSectionHTML(b, sub, level+1)
		}
		return
	}
	// Feed section.
	fmt.Fprintf(b, `<h%d>%s</h%d>`+"\n", level, name, level)
	renderDigestFlatHTML(b, sec.Highlights, sec.Items)
}

// renderDigestFlatHTML renders highlight cards followed by the plain item
// list, mirroring the historical digest layout.
func renderDigestFlatHTML(b *strings.Builder, highlights, items []digestContentRef) {
	for _, h := range highlights {
		title := html.EscapeString(h.Title)
		if h.URL != "" {
			fmt.Fprintf(b, `<h3><a href="%s">%s</a></h3>`+"\n", html.EscapeString(h.URL), title)
		} else {
			fmt.Fprintf(b, `<h3>%s</h3>`+"\n", title)
		}

		var meta []string
		if h.Points > 0 {
			meta = append(meta, fmt.Sprintf("%d points", h.Points))
		}
		if h.CommentsCount > 0 {
			meta = append(meta, fmt.Sprintf("%d comments", h.CommentsCount))
		}
		if len(meta) > 0 {
			fmt.Fprintf(b, `<p class="digest-meta">%s</p>`+"\n", html.EscapeString(strings.Join(meta, " · ")))
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
				fmt.Fprintf(b, `<li><a href="%s">%s</a></li>`, html.EscapeString(it.URL), title)
			} else {
				fmt.Fprintf(b, `<li>%s</li>`, title)
			}
		}
		b.WriteString(`</ul>` + "\n")
	}
}
