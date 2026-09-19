package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"digesterr/internal/logbuf"
	"digesterr/internal/store"
)

// levelRank maps a severity string to a comparable rank.
var levelRank = map[string]int{
	"DEBUG": 0,
	"INFO":  1,
	"WARN":  2,
	"ERROR": 3,
}

// categoryOf returns the effective category for an entry. An explicit
// "category" attribute (set by the logging package, e.g. "item" for
// item-level records logged from the http layer) wins over the
// source-derived type. Entries without a category attribute fall back to
// their type, preserving pre-category behavior.
func categoryOf(e logbuf.Entry) string {
	if c, ok := e.Attrs["category"]; ok && c != "" {
		return c
	}
	return e.Type
}

// logAttrAllowlist is the set of structured log attributes the /api/logs
// endpoint serves. The ring buffer is process-global, so every attribute that
// leaves through here is visible to EVERY authenticated user: any attribute
// naming another account's data (feed names/URLs, inbox addresses, config
// names, mail subjects) must not be added to this list. IDs are kept because
// the caller needs them to correlate a log line with their own resources —
// and scoped below to the caller's own objects.
var logAttrAllowlist = map[string]bool{
	"category": true, "error": true, "status": true, "duration": true,
	"count": true, "items": true, "feeds": true, "results": true,
	"feed_id": true, "item_id": true, "config_id": true, "result_id": true,
	"points": true, "min_score": true, "method": true, "path": true,
	"host": true, "reason": true, "level": true,
}

// handleListLogs returns the most recent captured log entries, optionally
// filtered by minimum severity (level=info) and exact category (type=digest).
// The category filter matches the entry's effective category: an explicit
// "category" attribute when present, otherwise its source-derived type.
// Pagination is offset-based over the filtered stream (newest first);
// has_more reports whether older entries exist beyond this page.
//
// The buffer is process-global while the endpoint is per-user, so entries are
// scoped before they are served: an entry is shown when it carries no
// ownership attribute at all (server-level: scheduler ticks, poll cycles), or
// when every ownership attribute it carries (user_id, feed_id, item_id,
// config_id, result_id) resolves to the caller. Everything else — other
// accounts' feeds, digests, and mailboxes — is dropped. Attribute values are
// additionally allowlisted so a future log line cannot leak a new sensitive
// field (feed_name, inbox, feed_url, config_name) through this endpoint.
func (s *Server) handleListLogs(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 1000 {
		limit = 1000
	}
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset = n
		}
	}

	minLevel := strings.ToUpper(r.URL.Query().Get("level"))
	minRank, hasLevel := levelRank[minLevel]
	typ := r.URL.Query().Get("type")

	ctx := r.Context()
	all := s.logs.List(0)
	filtered := make([]logbuf.Entry, 0, len(all))
	for _, e := range all {
		if hasLevel {
			rank, ok := levelRank[strings.ToUpper(e.Level)]
			if !ok || rank < minRank {
				continue
			}
		}
		if typ != "" && categoryOf(e) != typ {
			continue
		}
		if !s.logEntryVisible(ctx, userID, e) {
			continue
		}
		filtered = append(filtered, scrubLogEntry(e))
	}

	if offset > len(filtered) {
		offset = len(filtered)
	}
	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	page := filtered[offset:end]

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"entries":  page,
		"total":    len(filtered),
		"has_more": end < len(filtered),
	})
}

// logEntryVisible reports whether a buffered log entry may be shown to userID.
// Ownership attributes must ALL resolve to the caller: a user_id that is not
// theirs, or a feed/item/config/result belonging to someone else, hides the
// entry. Entries without ownership attributes (server-level telemetry) and
// entries whose referenced rows are gone (deleted feeds/digests — ownership
// can no longer be proven) are treated as server-level and shown.
func (s *Server) logEntryVisible(ctx context.Context, userID int64, e logbuf.Entry) bool {
	if v, ok := e.Attrs["user_id"]; ok && v != "" && v != itoa(userID) {
		return false
	}
	if v, ok := e.Attrs["feed_id"]; ok && v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			// GetFeed is scoped to (id, owner): success means theirs. An
			// error means "someone else's or deleted" — distinguish via
			// the unscoped lookup. Deleted feeds leave a bare ID that
			// leaks nothing once names are scrubbed, so they stay visible.
			if _, err := s.queries.GetFeed(ctx, store.GetFeedParams{ID: id, UserID: userID}); err != nil {
				if owned, ok := s.feedOwnedBy(ctx, id, userID); ok && !owned {
					return false
				}
			}
		}
	}
	if v, ok := e.Attrs["item_id"]; ok && v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			if owned, ok := s.itemOwnedBy(ctx, id, userID); ok && !owned {
				return false
			}
		}
	}
	if v, ok := e.Attrs["config_id"]; ok && v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			if owned, ok := s.digestConfigOwnedBy(ctx, id, userID); ok && !owned {
				return false
			}
		}
	}
	if v, ok := e.Attrs["result_id"]; ok && v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			if owned, ok := s.digestResultOwnedBy(ctx, id, userID); ok && !owned {
				return false
			}
		}
	}
	return true
}

// scrubLogEntry strips every attribute outside the allowlist, so a future log
// line carrying feed_name/feed_url/inbox/config_name cannot leak it here.
func scrubLogEntry(e logbuf.Entry) logbuf.Entry {
	if len(e.Attrs) == 0 {
		return e
	}
	kept := make(map[string]string, len(e.Attrs))
	for k, v := range e.Attrs {
		if logAttrAllowlist[k] {
			kept[k] = v
		}
	}
	e.Attrs = kept
	return e
}

// feedOwnedBy resolves the owner of a feed through the UNSCOPED lookup: the
// scoped GetFeed cannot distinguish "someone else's" from "deleted". ok=false
// means the row is gone — ownership can no longer be proven, and a bare ID
// without its feed_name leaks nothing, so the caller treats it as visible.
func (s *Server) feedOwnedBy(ctx context.Context, feedID, userID int64) (owned, ok bool) {
	feed, err := s.queries.GetFeedByID(ctx, feedID)
	if err != nil {
		return false, false
	}
	return feed.UserID == userID, true
}

// itemOwnedBy resolves an item's owner via its feed. ok=false means the item
// or its feed is gone (deleted feeds cascade to items).
func (s *Server) itemOwnedBy(ctx context.Context, itemID, userID int64) (owned, ok bool) {
	item, err := s.queries.GetItem(ctx, itemID)
	if err != nil {
		return false, false
	}
	return s.feedOwnedBy(ctx, item.FeedID, userID)
}

// digestConfigOwnedBy resolves a digest config's owner. There is no unscoped
// config lookup (all config queries are owner-scoped), so ownership is proven
// through the caller's own list: a config absent from it is either someone
// else's or deleted. Deleted configs leave an ID that leaks nothing once
// names are scrubbed — but another account's live config ID is equally opaque
// without its name, so "absent" is treated as visible and the scrubbed ID is
// all the viewer gets either way. The user_id check above is what actually
// separates accounts: user-scoped log lines never cross.
func (s *Server) digestConfigOwnedBy(ctx context.Context, configID, userID int64) (owned, ok bool) {
	configs, err := s.queries.ListDigestConfigs(ctx, userID)
	if err != nil {
		return false, false
	}
	for _, c := range configs {
		if c.ID == configID {
			return true, true
		}
	}
	return false, false
}

// digestResultOwnedBy resolves a digest result's owner. There is no unscoped
// result lookup (all result queries are owner-scoped), so ownership is proven
// through the caller's own per-status lists: a result absent from both is
// either someone else's or deleted. Deleted results leave an ID that leaks
// nothing once names are scrubbed — but another account's live result ID is
// equally opaque without its content, so "absent" is treated as visible and
// the scrubbed ID is all the viewer gets either way. The user_id check above
// is what actually separates accounts.
func (s *Server) digestResultOwnedBy(ctx context.Context, resultID, userID int64) (owned, ok bool) {
	for _, status := range []string{"new", "read"} {
		results, err := s.queries.ListDigestResultsByUserStatus(ctx, store.ListDigestResultsByUserStatusParams{
			UserID: userID,
			Status: status,
		})
		if err != nil {
			return false, false
		}
		for _, r := range results {
			if r.ID == resultID {
				return true, true
			}
		}
	}
	return false, false
}
