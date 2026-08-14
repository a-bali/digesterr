package httpapi

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"digesterr/internal/store"
)

// Stream id constants understood by the /reader/api/0/stream/items/ids
// endpoint.
const (
	readerStreamReadingList = "user/-/state/com.google/reading-list"
	readerStreamFresh       = "user/-/state/com.google/fresh"
	readerStreamRead        = "user/-/state/com.google/read"
	readerStreamStarred     = "user/-/state/com.google/starred"
)

// Google Reader item-id wire formats, mirroring Miniflux so off-the-shelf
// clients (NetNewsWire, Reeder, Read You, …) can parse and round-trip ids.
const (
	readerItemIDPrefix = "tag:google.com,2005:reader/item/"
	readerItemIDFormat = "tag:google.com,2005:reader/item/%016x"
	// digestRefOffset shifts digest-result ids into a range disjoint from
	// item ids, so both share the reader's flat integer item-id space without
	// colliding. Clients echo these ids back verbatim (or re-encoded as a
	// tag: URI) and never inspect their value.
	digestRefOffset = int64(1) << 40
)

// itemStreamID returns the reader item-reference id (a plain decimal string)
// for a regular feed item. Google Reader clients store this verbatim and echo
// it back as the `i` parameter — sometimes re-encoded as a tag: URI, which
// parseReaderItemRef accepts.
func itemStreamID(id int64) string { return strconv.FormatInt(id, 10) }

// digestItemStreamID returns the reader item-reference id (a plain decimal
// string) for a digest result, offset into a range disjoint from item ids.
func digestItemStreamID(id int64) string { return strconv.FormatInt(id+digestRefOffset, 10) }

// readerContentID renders a reader reference id (already offset for digests)
// as the long-form content id carried in stream/items/contents responses.
func readerContentID(refID int64) string { return fmt.Sprintf(readerItemIDFormat, refID) }

// parseReaderItemRef parses an `i` parameter value into a reader reference id.
// It accepts the three forms real clients send:
//   - "tag:google.com,2005:reader/item/<hex>" (NetNewsWire, padded or not)
//   - a 16-char bare hex string (Reeder)
//   - a plain decimal string (Liferea)
func parseReaderItemRef(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if rest, ok := strings.CutPrefix(s, readerItemIDPrefix); ok {
		if n, err := strconv.ParseInt(rest, 16, 64); err == nil {
			return n, true
		}
		return 0, false
	}
	if len(s) == 16 {
		if n, err := strconv.ParseInt(s, 16, 64); err == nil {
			return n, true
		}
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, true
	}
	return 0, false
}

// splitReaderItemRef maps a reader reference id back to (kind, numeric id).
func splitReaderItemRef(ref int64) (readerEditKind, int64) {
	if ref >= digestRefOffset {
		return readerEditDigest, ref - digestRefOffset
	}
	return readerEditItem, ref
}

// readerItemRef is a single entry in the stream items/ids response.
type readerItemRef struct {
	ID              string   `json:"id"`
	TimestampUsec   string   `json:"timestampUsec"`
	DirectStreamIDs []string `json:"directStreamIds"`
}

type readerItemRefsResponse struct {
	ItemRefs     []readerItemRef `json:"itemRefs"`
	Continuation string          `json:"continuation,omitempty"`
}

// streamEntry is a normalized, timestamped entry in a reader stream. It may
// represent a feed item or a digest result; kind is encoded in the emitted id
// (item/<id> vs digest-item/<id>).
type streamEntry struct {
	id        string
	timestamp time.Time
}

// handleReaderStreamItemsIDs implements GET /reader/api/0/stream/items/ids.
//
// It routes the requested stream id to the appropriate query, merges items and
// digest results where required (reading-list and read), sorts by the entry
// timestamp, and applies simple offset pagination. The continuation string is
// the offset (index) of the next page; clients pass it back via c=.
func (s *Server) handleReaderStreamItemsIDs(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	q := r.URL.Query()
	stream := q.Get("s")

	n := 20
	if v := q.Get("n"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}
	if n > 100 {
		n = 100
	}

	oldestFirst := q.Get("r") == "o"

	offset := 0
	if v := q.Get("c"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			offset = parsed
		}
	}

	entries, err := s.streamEntries(r, userID, stream, oldestFirst)
	if err != nil {
		writeServerError(w, "failed to load stream", err)
		return
	}

	refs := make([]readerItemRef, 0, n)
	continuation := ""
	if offset < len(entries) {
		end := offset + n
		if end > len(entries) {
			end = len(entries)
		}
		for _, e := range entries[offset:end] {
			refs = append(refs, readerItemRef{
				ID:              e.id,
				TimestampUsec:   strconv.FormatInt(e.timestamp.UnixMicro(), 10),
				DirectStreamIDs: []string{},
			})
		}
		if end < len(entries) {
			continuation = strconv.Itoa(end)
		}
	}

	writeJSON(w, http.StatusOK, readerItemRefsResponse{ItemRefs: refs, Continuation: continuation})
}

// streamEntries resolves a stream id to its ordered entries. Unknown or
// unresolvable stream ids yield an empty slice (not an error), matching Google
// Reader's behavior of returning an empty stream for unknown ids.
func (s *Server) streamEntries(r *http.Request, userID int64, stream string, oldestFirst bool) ([]streamEntry, error) {
	ctx := r.Context()

	switch {
	case stream == readerStreamReadingList || stream == readerStreamFresh:
		// Unread items across all the user's feeds, merged with 'new' digest
		// results (fresh is an alias of reading-list).
		items, err := s.queries.ListItemsByUserStatus(ctx, store.ListItemsByUserStatusParams{
			UserID: userID,
			Status: "unread",
		})
		if err != nil {
			return nil, err
		}
		results, err := s.queries.ListDigestResultsByUserStatus(ctx, store.ListDigestResultsByUserStatusParams{
			UserID: userID,
			Status: "new",
		})
		if err != nil {
			return nil, err
		}
		entries := mergeStreamEntries(items, results)
		sortStreamEntries(entries, oldestFirst)
		return entries, nil

	case stream == readerStreamRead:
		// Items with status 'read' or 'digested' (digested maps to read),
		// merged with 'read' digest results.
		items, err := s.queries.ListItemsByUserRead(ctx, userID)
		if err != nil {
			return nil, err
		}
		results, err := s.queries.ListDigestResultsByUserStatus(ctx, store.ListDigestResultsByUserStatusParams{
			UserID: userID,
			Status: "read",
		})
		if err != nil {
			return nil, err
		}
		entries := mergeStreamEntries(items, results)
		sortStreamEntries(entries, oldestFirst)
		return entries, nil

	case stream == readerStreamStarred:
		items, err := s.queries.ListItemsByUserStatus(ctx, store.ListItemsByUserStatusParams{
			UserID: userID,
			Status: "saved",
		})
		if err != nil {
			return nil, err
		}
		entries := itemStreamEntries(items)
		sortStreamEntries(entries, oldestFirst)
		return entries, nil

	case strings.HasPrefix(stream, "feed/"):
		id, err := strconv.ParseInt(strings.TrimPrefix(stream, "feed/"), 10, 64)
		if err != nil {
			return nil, nil
		}
		// Verify the feed belongs to the authenticated user.
		if _, err := s.queries.GetFeed(ctx, store.GetFeedParams{ID: id, UserID: userID}); err != nil {
			return nil, nil
		}
		items, err := s.queries.ListItemsByFeed(ctx, id)
		if err != nil {
			return nil, err
		}
		entries := itemStreamEntries(items)
		sortStreamEntries(entries, oldestFirst)
		return entries, nil

	case strings.HasPrefix(stream, "digest/"):
		id, err := strconv.ParseInt(strings.TrimPrefix(stream, "digest/"), 10, 64)
		if err != nil {
			return nil, nil
		}
		results, err := s.queries.ListDigestResultsByConfig(ctx, store.ListDigestResultsByConfigParams{
			ConfigID: id,
			UserID:   userID,
		})
		if err != nil {
			return nil, err
		}
		entries := digestStreamEntries(results)
		sortStreamEntries(entries, oldestFirst)
		return entries, nil

	case strings.HasPrefix(stream, "user/-/label/"):
		name := strings.TrimPrefix(stream, "user/-/label/")
		items, err := s.queries.ListItemsByCategory(ctx, store.ListItemsByCategoryParams{
			Category: name,
			UserID:   userID,
		})
		if err != nil {
			return nil, err
		}
		entries := itemStreamEntries(items)
		sortStreamEntries(entries, oldestFirst)
		return entries, nil

	default:
		return nil, nil
	}
}

// itemTimestamp returns the sort timestamp for an item: published_at when
// present, falling back to fetched_at and finally created_at.
func itemTimestamp(i store.Item) time.Time {
	if i.PublishedAt.Valid {
		return i.PublishedAt.Time
	}
	if !i.FetchedAt.IsZero() {
		return i.FetchedAt
	}
	return i.CreatedAt
}

func itemStreamEntries(items []store.Item) []streamEntry {
	entries := make([]streamEntry, 0, len(items))
	for _, it := range items {
		entries = append(entries, streamEntry{
			id:        itemStreamID(it.ID),
			timestamp: itemTimestamp(it),
		})
	}
	return entries
}

func digestStreamEntries(results []store.DigestResult) []streamEntry {
	entries := make([]streamEntry, 0, len(results))
	for _, dr := range results {
		entries = append(entries, streamEntry{
			id:        digestItemStreamID(dr.ID),
			timestamp: dr.CreatedAt,
		})
	}
	return entries
}

func mergeStreamEntries(items []store.Item, results []store.DigestResult) []streamEntry {
	entries := make([]streamEntry, 0, len(items)+len(results))
	entries = append(entries, itemStreamEntries(items)...)
	entries = append(entries, digestStreamEntries(results)...)
	return entries
}

// sortStreamEntries orders entries by timestamp, newest-first by default or
// oldest-first when oldestFirst is set. SliceStable keeps entries with equal
// timestamps in their original (item-before-digest) order.
func sortStreamEntries(entries []streamEntry, oldestFirst bool) {
	if oldestFirst {
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].timestamp.Before(entries[j].timestamp)
		})
		return
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].timestamp.After(entries[j].timestamp)
	})
}
