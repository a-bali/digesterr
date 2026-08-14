package httpapi

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/http"
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
func readerLabelID(name string) string { return fmt.Sprintf("user/-/label/%s", name) }

type readerCategory struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type readerSubscription struct {
	ID            string           `json:"id"`
	Title         string           `json:"title"`
	Categories    []readerCategory `json:"categories"`
	SortID        string           `json:"sortid"`
	FirstItemMsec string           `json:"firstitemmsec"`
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
		writeError(w, http.StatusInternalServerError, "failed to list feeds")
		return
	}

	configs, err := s.queries.ListDigestConfigs(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list digest configs")
		return
	}

	subs := make([]readerSubscription, 0, len(feeds)+len(configs))
	for _, f := range feeds {
		sub := readerSubscription{
			ID:            feedStreamID(f.ID),
			Title:         f.Name,
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
		writeError(w, http.StatusInternalServerError, "failed to list categories")
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
		writeError(w, http.StatusInternalServerError, "failed to list feeds")
		return
	}

	configs, err := s.queries.ListDigestConfigs(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list digest configs")
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
