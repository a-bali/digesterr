package httpapi

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"digesterr/internal/store"
)

// readerSortID is a stable, opaque value used for Google Reader's "sortid"
// field. Compatibility with Google Reader clients only requires the field to
// be present and consistent; the exact value is not interpreted.
const readerSortID = "00000000"

// feedStreamID builds the Google Reader stream id for a feed.
func feedStreamID(id int64) string { return fmt.Sprintf("feed/%d", id) }

// digestStreamID builds the Google Reader stream id for a digest config,
// which surfaces as a subscription in the reader.
func digestStreamID(id int64) string { return fmt.Sprintf("digest/%d", id) }

// readerLabelID builds the Google Reader label/tag id for a category name.
func readerLabelID(name string) string { return readerLabelPrefix + name }

// readerLabelPrefix is the stream-id prefix shared by every category label.
// Kept in one place so the handlers that PARSE a label id cannot drift from the
// one that builds it.
const readerLabelPrefix = "user/-/label/"

type readerCategory struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type readerSubscription struct {
	ID            string           `json:"id"`
	Title         string           `json:"title"`
	URL           string           `json:"url,omitempty"`
	HTMLURL       string           `json:"htmlUrl,omitempty"`
	IconURL       string           `json:"iconUrl,omitempty"`
	Categories    []readerCategory `json:"categories"`
	SortID        string           `json:"sortid"`
	FirstItemMsec string           `json:"firstitemmsec"`
}

// readerFeedHomePage is the subscription `htmlUrl`: the address reader clients
// should treat as the feed's home page.
//
// It must be the SITE, not digesterr's own feed page. NetNewsWire resolves a
// feed's icon from the home page it is given — first the page's apple-touch-icon
// / og:image, then its rel=icon — so pointing htmlUrl at digesterr made every
// subscription advertise digesterr's own logo (or, before the web UI had a
// favicon, a non-image that made the client cache "no icon" and draw a
// placeholder). See internal/feed/rss.go: site_url is the <link> the feed
// declares, learned at poll time.
//
// When site_url is unknown (no poll yet, or a type whose payload is not a feed)
// the feed URL's origin is the best available answer, and it is exactly what
// readers deriving a home page themselves would use.
func readerFeedHomePage(f store.Feed) string {
	if f.SiteUrl != "" {
		return f.SiteUrl
	}
	// For a twitter feed the feed URL IS the profile page, and that page's
	// og:image is the account's own avatar — a real per-feed icon, which the
	// bare origin (x.com) would collapse into one generic logo for every
	// account. For rss the URL is a feed document, not a page, so only the
	// origin is meaningful there.
	if f.Type == "twitter" {
		if u := absoluteHTTPURL(f.Url); u != "" {
			return u
		}
	}
	return originOfURL(f.Url)
}

// readerFeedIconURL is the subscription `iconUrl`. NetNewsWire ignores it (it
// resolves icons from htmlUrl itself), but other Google Reader clients read it,
// and Miniflux — which this API mirrors — serves one. Empty for feed types with
// no favicon to point at.
func readerFeedIconURL(f store.Feed) string {
	switch f.Type {
	case "hn":
		return "https://news.ycombinator.com/favicon.ico"
	case "lobsters":
		return "https://lobste.rs/favicon.ico"
	}
	host := originOfURL(f.Url)
	if host == "" {
		return ""
	}
	return host + "/favicon.ico"
}

// originOfURL returns scheme://host for an absolute http(s) URL, or "" when the
// value is not one (an email address, an empty URL, a malformed string).
func originOfURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// absoluteHTTPURL returns raw when it is an absolute http(s) URL, else "".
func absoluteHTTPURL(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return raw
}

type readerSubscriptionList struct {
	Subscriptions []readerSubscription `json:"subscriptions"`
}

// handleReaderSubscriptionList returns the user's feeds and digest configs as
// Google Reader subscriptions.
func (s *Server) handleReaderSubscriptionList(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	feeds, err := s.queries.ListFeeds(r.Context(), userID)
	if err != nil {
		writeServerError(w, "failed to list feeds", err)
		return
	}

	configs, err := s.queries.ListDigestConfigs(r.Context(), userID)
	if err != nil {
		writeServerError(w, "failed to list digest configs", err)
		return
	}

	subs := make([]readerSubscription, 0, len(feeds)+len(configs))
	for _, f := range feeds {
		sub := readerSubscription{
			ID:            feedStreamID(f.ID),
			Title:         f.Name,
			URL:           f.Url,
			HTMLURL:       readerFeedHomePage(f),
			IconURL:       readerFeedIconURL(f),
			Categories:    []readerCategory{},
			SortID:        readerSortID,
			FirstItemMsec: "0",
		}
		if f.Category != "" {
			sub.Categories = []readerCategory{{ID: readerLabelID(f.Category), Label: f.Category}}
		}
		subs = append(subs, sub)
	}

	for _, cfg := range configs {
		title := cfg.Name
		if title == "" {
			title = fmt.Sprintf("Digest %d", cfg.ID)
		}
		subs = append(subs, readerSubscription{
			ID:            digestStreamID(cfg.ID),
			Title:         title,
			HTMLURL:       s.digestConfigWebURL(r, cfg.ID),
			Categories:    []readerCategory{},
			SortID:        readerSortID,
			FirstItemMsec: "0",
		})
	}

	writeJSON(w, http.StatusOK, readerSubscriptionList{Subscriptions: subs})
}

type readerTag struct {
	ID     string `json:"id"`
	SortID string `json:"sortid"`
	Type   string `json:"type"`
}

type readerTagList struct {
	Tags []readerTag `json:"tags"`
}

// handleReaderTagList returns the standard Google Reader state tags plus one
// label tag per category (derived from feeds that carry that category).
func (s *Server) handleReaderTagList(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	tags := []readerTag{
		{ID: "user/-/state/com.google/reading-list", SortID: readerSortID, Type: "tag"},
		{ID: "user/-/state/com.google/read", SortID: readerSortID, Type: "tag"},
		{ID: "user/-/state/com.google/starred", SortID: readerSortID, Type: "tag"},
	}

	categories, err := s.queries.ListCategories(r.Context(), userID)
	if err != nil {
		writeServerError(w, "failed to list categories", err)
		return
	}
	for _, name := range categories {
		tags = append(tags, readerTag{ID: readerLabelID(name), SortID: readerSortID, Type: "tag"})
	}

	writeJSON(w, http.StatusOK, readerTagList{Tags: tags})
}

// escapeXMLAttr escapes a string for safe inclusion in an XML attribute.
func escapeXMLAttr(s string) string {
	var buf bytes.Buffer
	xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

// writeOPMLOutline writes a single <outline> element for a feed or digest.
func writeOPMLOutline(b *strings.Builder, title, xmlURL string) {
	t := escapeXMLAttr(title)
	u := escapeXMLAttr(xmlURL)
	fmt.Fprintf(b, `<outline type="rss" text="%s" title="%s" xmlUrl="%s"/>`, t, t, u)
}

// handleReaderSubscriptionExport returns the user's subscriptions as OPML.
// Feeds with a category are nested inside a category outline; uncategorized
// feeds and digest configs are emitted at the top level. Digest configs have
// no web URL, so their xmlUrl is empty.
func (s *Server) handleReaderSubscriptionExport(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	feeds, err := s.queries.ListFeeds(r.Context(), userID)
	if err != nil {
		writeServerError(w, "failed to list feeds", err)
		return
	}

	configs, err := s.queries.ListDigestConfigs(r.Context(), userID)
	if err != nil {
		writeServerError(w, "failed to list digest configs", err)
		return
	}

	grouped := make(map[string][]store.Feed)
	var topLevel []store.Feed
	for _, f := range feeds {
		if f.Category != "" {
			grouped[f.Category] = append(grouped[f.Category], f)
			continue
		}
		topLevel = append(topLevel, f)
	}

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(`<opml version="1.0"><head><title>Digesterr Subscriptions</title></head><body>`)

	for _, f := range topLevel {
		writeOPMLOutline(&b, f.Name, f.Url)
	}

	catNames := make([]string, 0, len(grouped))
	for name := range grouped {
		catNames = append(catNames, name)
	}
	sort.Strings(catNames)
	for _, name := range catNames {
		fs := grouped[name]
		if len(fs) == 0 {
			continue
		}
		escaped := escapeXMLAttr(name)
		fmt.Fprintf(&b, `<outline text="%s" title="%s">`, escaped, escaped)
		for _, f := range fs {
			writeOPMLOutline(&b, f.Name, f.Url)
		}
		b.WriteString(`</outline>`)
	}

	for _, cfg := range configs {
		title := cfg.Name
		if title == "" {
			title = fmt.Sprintf("Digest %d", cfg.ID)
		}
		writeOPMLOutline(&b, title, "")
	}

	b.WriteString(`</body></opml>`)

	w.Header().Set("Content-Type", "text/x-opml")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, b.String())
}
