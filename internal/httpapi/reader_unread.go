package httpapi

import (
	"net/http"
	"strconv"
	"time"
)

// readerUnreadCountEntry is a single entry in the unread-count response.
type readerUnreadCountEntry struct {
	ID                      string `json:"id"`
	Count                   int64  `json:"count"`
	NewestItemTimestampUsec string `json:"newestItemTimestampUsec"`
}

type readerUnreadCountResponse struct {
	Max          string                   `json:"max"`
	UnreadCounts []readerUnreadCountEntry `json:"unreadcounts"`
}

// handleReaderUnreadCount returns unread counts for the user's feeds and
// digest configs, plus the global reading-list and starred totals, in the
// shape Google Reader clients poll. Per-feed and per-digest entries are only
// emitted when their count is non-zero; the two global entries are always
// present.
//
// Reader counts are UNFILTERED by min_score: the protocol has no min_score
// parameter, so the threshold the web UI applies by default cannot be
// selectively lifted here. Third-party clients see every unread item.
func (s *Server) handleReaderUnreadCount(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	feedCounts, err := s.queries.GetReaderUnreadCounts(r.Context(), userID)
	if err != nil {
		writeServerError(w, "failed to get unread counts", err)
		return
	}

	newestRows, err := s.queries.NewestUnreadItemPerFeed(r.Context(), userID)
	if err != nil {
		writeServerError(w, "failed to get newest unread items", err)
		return
	}
	newestSec := make(map[int64]int64, len(newestRows))
	for _, row := range newestRows {
		newestSec[row.FeedID] = row.NewestSec
	}

	digestCounts, err := s.queries.GetDigestUnreadCounts(r.Context(), userID)
	if err != nil {
		writeServerError(w, "failed to get digest unread counts", err)
		return
	}

	starred, err := s.queries.CountStarredItems(r.Context(), userID)
	if err != nil {
		writeServerError(w, "failed to count starred items", err)
		return
	}

	entries := make([]readerUnreadCountEntry, 0, len(feedCounts)+len(digestCounts)+2)

	var readingList int64
	for _, fc := range feedCounts {
		readingList += fc.UnreadCount
		if fc.UnreadCount == 0 {
			continue
		}
		entries = append(entries, readerUnreadCountEntry{
			ID:                      feedStreamID(fc.FeedID),
			Count:                   fc.UnreadCount,
			NewestItemTimestampUsec: strconv.FormatInt(newestSec[fc.FeedID]*1_000_000, 10),
		})
	}

	for _, dc := range digestCounts {
		readingList += dc.UnreadCount
		if dc.UnreadCount == 0 {
			continue
		}
		entries = append(entries, readerUnreadCountEntry{
			ID:                      digestStreamID(dc.ConfigID),
			Count:                   dc.UnreadCount,
			NewestItemTimestampUsec: "0",
		})
	}

	entries = append(entries,
		readerUnreadCountEntry{
			ID:                      "user/-/state/com.google/reading-list",
			Count:                   readingList,
			NewestItemTimestampUsec: "0",
		},
		readerUnreadCountEntry{
			ID:                      "user/-/state/com.google/starred",
			Count:                   starred,
			NewestItemTimestampUsec: "0",
		},
	)

	writeJSON(w, http.StatusOK, readerUnreadCountResponse{
		Max:          strconv.FormatInt(time.Now().Unix(), 10),
		UnreadCounts: entries,
	})
}
