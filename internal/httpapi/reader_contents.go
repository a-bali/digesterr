package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"digesterr/internal/feed"
	"digesterr/internal/linkify"
	"digesterr/internal/store"
)

// readerContentsResponse is the top-level envelope for both stream/items/contents
// and stream/contents responses. The id and updated fields are mandatory:
// NetNewsWire's ReaderAPIEntryWrapper decodes them as non-optional, so omitting
// them makes the whole response fail to decode and no articles are created.
type readerContentsResponse struct {
	Direction string                    `json:"direction"`
	ID        string                    `json:"id"`
	Title     string                    `json:"title"`
	Self      []readerHref              `json:"self"`
	Updated   int64                     `json:"updated"`
	Author    string                    `json:"author"`
	Items     []readerContentStreamItem `json:"items"`
}

// readerHref is a bare href reference used by the self link.
type readerHref struct {
	Href string `json:"href"`
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
// GET/POST /reader/api/0/stream/items/contents?i=<id>&i=<id>&output=json.
//
// NetNewsWire POSTs form-encoded (T=<token>&output=json&i=<id>&…), other
// clients GET with query params, so the handler parses both. Each i param
// resolves to a full content object; unknown or invalid ids are silently
// skipped while preserving input order.
func (s *Server) handleReaderStreamItemsContents(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// ParseForm merges URL query and (for POST) form body into r.Form.
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid form body")
		return
	}

	rawIDs := r.Form["i"]
	refs := make([]int64, 0, len(rawIDs))
	for _, rawID := range rawIDs {
		if ref, ok := parseReaderItemRef(rawID); ok {
			refs = append(refs, ref)
		}
	}
	items := s.resolveContentObjects(r, userID, refs)

	writeJSON(w, http.StatusOK, readerContentsResponse{
		Direction: "ltr",
		ID:        readerStreamReadingList,
		Title:     "Reading List",
		Self:      []readerHref{},
		Updated:   time.Now().Unix(),
		Items:     items,
	})
}

// resolveContentObjects resolves every ref concurrently and returns the
// objects in input order, skipping refs that do not resolve.
//
// Comment fetching happens per item and is one upstream round-trip each, so
// resolving serially made a 98-item request take 40-400s and blow past the 60s
// request timeout NetNewsWire uses — the client saw no response at all, which
// is why the failure was silent. Warm items (comments already cached) return
// without touching the network.
//
// Comment fetches run on a context detached from the request: if a client
// gives up mid-request, the fetches it paid for still land in the cache, so the
// retry does not start from zero. This mirrors the detached per-item extraction
// precedent. The work is still bounded by the server's commentFetchSem, so a
// flood of reader requests cannot spawn unbounded upstream calls.
func (s *Server) resolveContentObjects(r *http.Request, userID int64, refs []int64) []readerContentStreamItem {
	if len(refs) == 0 {
		return []readerContentStreamItem{}
	}

	r = r.WithContext(context.WithoutCancel(r.Context()))

	// Indexed slots so each worker writes a disjoint element and the results
	// can be reassembled in input order without locking.
	slots := make([]*readerContentStreamItem, len(refs))

	sem := s.commentFetchSem
	if sem == nil {
		// Server built outside NewServer (tests): keep a local bound rather
		// than falling back to unbounded fan-out.
		sem = make(chan struct{}, defaultCommentFetchConcurrency)
	}

	var wg sync.WaitGroup
	for i, ref := range refs {
		wg.Add(1)
		go func(i int, ref int64) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if obj, ok := s.resolveContentObject(r, userID, ref); ok {
				slots[i] = &obj
			}
		}(i, ref)
	}
	wg.Wait()

	items := make([]readerContentStreamItem, 0, len(refs))
	for _, slot := range slots {
		if slot != nil {
			items = append(items, *slot)
		}
	}
	return items
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

	entries, _, err := s.streamEntries(r, userID, stream, false, 0, n)
	if err != nil {
		writeServerError(w, "failed to load stream", err)
		return
	}

	refs := make([]int64, 0, len(entries))
	for _, e := range entries {
		if ref, ok := parseReaderItemRef(e.id); ok {
			refs = append(refs, ref)
		}
	}
	items := s.resolveContentObjects(r, userID, refs)

	writeJSON(w, http.StatusOK, readerContentsResponse{
		Direction: "ltr",
		ID:        stream,
		Title:     stream,
		Self:      []readerHref{},
		Updated:   time.Now().Unix(),
		Items:     items,
	})
}

// webBaseURL returns the digesterr web UI base URL for in-app links surfaced
// to Reader clients. It prefers PUBLIC_BASE_URL (absolute) and otherwise
// derives the origin from the request so links resolve against the same host
// the client is syncing with.
func (s *Server) webBaseURL(r *http.Request) string {
	if s.publicBaseURL != "" {
		return s.publicBaseURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// feedWebURL builds the digesterr web UI URL for a feed.
func (s *Server) feedWebURL(r *http.Request, feedID int64) string {
	return fmt.Sprintf("%s/feeds/%d", s.webBaseURL(r), feedID)
}

// digestResultWebURL builds the digesterr web UI URL for a digest result.
func (s *Server) digestResultWebURL(r *http.Request, configID, resultID int64) string {
	return fmt.Sprintf("%s/digests/%d/%d", s.webBaseURL(r), configID, resultID)
}

// digestConfigWebURL builds the digesterr web UI URL for a digest config.
func (s *Server) digestConfigWebURL(r *http.Request, configID int64) string {
	return fmt.Sprintf("%s/digests/%d", s.webBaseURL(r), configID)
}

// resolveContentObject maps a reader reference id (item id, or digest-result
// id offset by digestRefOffset) to a full content object. It returns ok=false
// for unknown, malformed, or cross-user ids so callers can skip them silently.
func (s *Server) resolveContentObject(r *http.Request, userID int64, ref int64) (readerContentStreamItem, bool) {
	ctx := r.Context()
	kind, id := splitReaderItemRef(ref)

	if kind == readerEditDigest {
		dr, err := s.queries.GetDigestResult(ctx, store.GetDigestResultParams{ID: id, UserID: userID})
		if err != nil {
			return readerContentStreamItem{}, false
		}
		return s.digestContentObject(r, userID, dr), true
	}

	item, err := s.queries.GetItemWithFeedOwner(ctx, store.GetItemWithFeedOwnerParams{ID: id, UserID: userID})
	if err != nil {
		return readerContentStreamItem{}, false
	}
	return s.itemContentObject(r, item), true
}

// itemContentObject builds a content object for a regular feed item.
func (s *Server) itemContentObject(r *http.Request, item store.Item) readerContentStreamItem {
	ctx := r.Context()
	feedName := ""
	feedType := ""
	if f, err := s.queries.GetFeedByID(ctx, item.FeedID); err == nil {
		feedName = f.Name
		feedType = f.Type
	}

	var published int64
	if item.PublishedAt.Valid {
		published = item.PublishedAt.Time.Unix()
	}

	// Prefer extracted content when extraction completed successfully.
	article := item.Content
	if item.ExtractContent != "" && item.ExtractStatus == "done" {
		article = item.ExtractContent
	}
	// Repair a body written before the image/filler rules existed. This is a
	// read-time upgrade, not a rewrite: the stored row is left alone, so it
	// stays auditable and the next deploy can change its mind about the markup.
	// Without it, everything stored before the change keeps its oversized
	// images and unbreakable filler run forever — the poller only ever looks at
	// items it has not seen.
	article = feed.UpgradeStoredBody(article)

	// Prepend the points/comments meta line when the item carries them.
	meta := readerItemMetaHTML(item)

	// Put the Reader-only summarize link at the very top of the served body.
	// It is never stored, so the web UI's original item content is unchanged.
	link := s.summaryLinkHTML(r, item)

	// The link goes in BOTH fields, not just `content`. Google Reader clients
	// — NetNewsWire among them — render `summary` as the article body, so
	// keeping the link out of `summary` made the control invisible to the very
	// clients it exists for. v0.7.9 shipped that regression; this is the revert.
	//
	// The trade-off is real and accepted: `summary` also serves as the
	// list-row snippet, so the glyph appears in both places. It is a single
	// character (the same one the web UI's Summarize control uses) precisely
	// because of that — a word would be noise in the list view. There is no way
	// to give one field different bytes to two consumers.
	body := meta + link + article

	// Append top comments when experimental comment fetching is enabled.
	if topComments, _ := s.fetchTopComments(r, item, s.topComments, false); len(topComments) > 0 {
		body += "\n" + topCommentsHTML(topComments, feedType)
	}

	body = linkify.UpgradeMediaPlayers(body)
	ts := itemTimestamp(item)

	return readerContentStreamItem{
		ID:            readerContentID(item.ID),
		CrawlTimeMsec: strconv.FormatInt(ts.UnixMilli(), 10),
		TimestampUsec: strconv.FormatInt(ts.UnixMicro(), 10),
		Published:     published,
		Updated:       item.UpdatedAt.Unix(),
		Title:         item.Title,
		Canonical:     []readerCanonical{{Href: item.Url}},
		Alternate:     []readerAlternate{{Href: item.Url, Type: "text/html"}},
		// NetNewsWire reads only `summary` as the article body, so both fields
		// carry the full body (extracted when available), matching Miniflux.
		// The summarize link is in both, because this is the field clients
		// actually render.
		Summary:     readerContentBody{Content: body},
		Content:     readerContentBody{Content: body},
		Author:      item.Author,
		Origin:      readerOrigin{StreamID: feedStreamID(item.FeedID), Title: feedName, HTMLURL: s.feedWebURL(r, item.FeedID)},
		Categories:  itemCategories(item.Status, item.IsSaved != 0),
		Annotations: []string{},
	}
}

// readerItemMetaHTML builds the points/comments meta line prepended to a feed
// item's body, mirroring the digest renderer: "N points · N comments" with the
// comments label linked to the item's comment URL when present. Items without
// points, comments, or a comments link get no meta line (returns "").
func readerItemMetaHTML(item store.Item) string {
	var parts []string
	if item.Points > 0 {
		parts = append(parts, fmt.Sprintf("%d points", item.Points))
	}
	if item.CommentsCount > 0 || item.CommentUrl != "" {
		commentsLabel := fmt.Sprintf("%d comments", item.CommentsCount)
		if item.CommentsCount == 0 {
			commentsLabel = "Comments"
		}
		if item.CommentUrl != "" {
			commentsLabel = fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(item.CommentUrl), commentsLabel)
		}
		parts = append(parts, commentsLabel)
	}
	if len(parts) == 0 {
		return ""
	}
	return `<p class="digest-meta">` + strings.Join(parts, " · ") + `</p>` + "\n"
}

// digestContentObject builds a content object for a digest result.
func (s *Server) digestContentObject(r *http.Request, userID int64, dr store.DigestResult) readerContentStreamItem {
	ctx := r.Context()
	configName := fmt.Sprintf("Digest %d", dr.ConfigID)
	if cfg, err := s.queries.GetDigestConfig(ctx, store.GetDigestConfigParams{ID: dr.ConfigID, UserID: userID}); err == nil {
		if cfg.Name != "" {
			configName = cfg.Name
		}
	}

	// NetNewsWire reads only `summary` as the article body, so render the full
	// digest once and put it in both fields (matching the item content path).
	rendered := linkify.UpgradeMediaPlayers(renderDigestHTML(dr.Content, s.webBaseURL(r)))

	// Prefer the generated digest title; fall back to "Digest: <config name>"
	// for older results that predate titles.
	itemTitle := "Digest: " + configName
	var content digestContentJSON
	if err := json.Unmarshal([]byte(dr.Content), &content); err == nil && content.Title != "" {
		itemTitle = content.Title
	}

	digestURL := s.digestResultWebURL(r, dr.ConfigID, dr.ID)
	created := dr.CreatedAt

	return readerContentStreamItem{
		ID:            readerContentID(dr.ID + digestRefOffset),
		CrawlTimeMsec: strconv.FormatInt(created.UnixMilli(), 10),
		TimestampUsec: strconv.FormatInt(created.UnixMicro(), 10),
		Published:     created.Unix(),
		Updated:       created.Unix(),
		Title:         itemTitle,
		Canonical:     []readerCanonical{{Href: digestURL}},
		Alternate:     []readerAlternate{{Href: digestURL, Type: "text/html"}},
		Summary:       readerContentBody{Content: rendered},
		Content:       readerContentBody{Content: rendered},
		Author:        "",
		Origin:        readerOrigin{StreamID: digestStreamID(dr.ConfigID), Title: configName, HTMLURL: s.digestConfigWebURL(r, dr.ConfigID)},
		Categories:    digestCategories(dr.Status),
		Annotations:   []string{},
	}
}

// itemCategories maps an item to its Google Reader state tags. Starred comes
// from the is_saved FLAG, not from the status: the two are independent, so an
// item can be starred-and-unread. Before the flag existed, starring an item set
// status='saved' (which also read it) and any later read toggle silently
// unstarred it — the reader API's starred state was a side effect of read state.
func itemCategories(status string, isSaved bool) []string {
	cats := []string{readerStreamRead}
	if status == "unread" {
		cats = []string{readerStreamReadingList}
	}
	if isSaved {
		cats = append(cats, readerStreamStarred)
	}
	return cats
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

// renderDigestHTML renders a digest result's content JSON as HTML. All
// user-controlled strings are escaped to prevent HTML injection. When the
// content carries sections (category/feed grouping), they are rendered in
// order; older content without sections falls back to the flat layout.
// feedBaseURL is the digesterr web UI base URL used for feed-name links.
func renderDigestHTML(contentJSON, feedBaseURL string) string {
	var c digestContentJSON
	if err := json.Unmarshal([]byte(contentJSON), &c); err != nil {
		c = digestContentJSON{}
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
			fmt.Fprintf(b, `<p>%s</p>`+"\n", linkify.Text(h.LLMSummary))
		}

		if len(h.TopComments) > 0 {
			// Reuse the feed-item comment renderer so a digest highlight's
			// comments look identical to a feed item's comments: same quote-bar
			// list, same tight spacing. Previously this emitted its own
			// `<h4>` + bare `<ul>` with default list indentation, which read as
			// a different, more padded component than comments elsewhere.
			b.WriteString(`<div class="digest-comments" style="margin-top:1em;border-top:1px solid #ccc;padding-top:0.5em">` + "\n")
			b.WriteString(`<h4 style="font-size:0.85em;margin:0 0 0.5em">Top comments</h4>` + "\n")
			b.WriteString(`<ul style="list-style:none;padding:0;margin:0">` + "\n")
			for _, tc := range h.TopComments {
				// A nil Points means the digest never recorded a score for this
				// comment; 0 would render a misleading "(0)".
				validPoints := tc.Points != nil
				points := 0
				if validPoints {
					points = *tc.Points
				}
				b.WriteString(commentListItemHTML(tc.Author, tc.Text, points, validPoints, tc.URL, "") + "\n")
			}
			b.WriteString(`</ul>` + "\n")
			b.WriteString(`</div>` + "\n")
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
