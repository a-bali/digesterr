package httpapi

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"digesterr/internal/digest"
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
	items := make([]readerContentStreamItem, 0, len(rawIDs))
	for _, rawID := range rawIDs {
		ref, ok := parseReaderItemRef(rawID)
		if !ok {
			continue
		}
		if obj, ok := s.resolveContentObject(r, userID, ref); ok {
			items = append(items, obj)
		}
	}

	writeJSON(w, http.StatusOK, readerContentsResponse{
		Direction: "ltr",
		ID:        readerStreamReadingList,
		Title:     "Reading List",
		Self:      []readerHref{},
		Updated:   time.Now().Unix(),
		Items:     items,
	})
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
		ref, ok := parseReaderItemRef(e.id)
		if !ok {
			continue
		}
		if obj, ok := s.resolveContentObject(r, userID, ref); ok {
			items = append(items, obj)
		}
	}

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

	// Prepend the points/comments meta line when the item carries them.
	if meta := readerItemMetaHTML(item); meta != "" {
		body = meta + body
	}

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
		Summary:     readerContentBody{Content: body},
		Content:     readerContentBody{Content: body},
		Author:      item.Author,
		Origin:      readerOrigin{StreamID: feedStreamID(item.FeedID), Title: feedName, HTMLURL: s.feedWebURL(r, item.FeedID)},
		Categories:  itemCategories(item.Status),
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
	rendered := digest.RenderDigestHTML(dr.Content, s.webBaseURL(r))

	// Prefer the generated digest title; fall back to "Digest: <config name>"
	// for older results that predate titles.
	itemTitle := "Digest: " + configName
	var content struct {
		Title string `json:"title"`
	}
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

