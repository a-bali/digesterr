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

// itemStreamID builds the Google Reader stream-item id for a regular feed
// item. The contents endpoint (Task 5) resolves these back to items.
func itemStreamID(id int64) string { return fmt.Sprintf("item/%d", id) }

// digestItemStreamID builds the Google Reader stream-item id for a digest
// result, which surfaces as a synthetic item in merged streams.
func digestItemStreamID(id int64) string { return fmt.Sprintf("digest-item/%d", id) }

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
