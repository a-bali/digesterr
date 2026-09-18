package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"digesterr/internal/store"
)

// Tag constants understood by edit-tag. These are the Google Reader
// state tags; any other tag is ignored (no-op).
const (
	readerTagRead    = "user/-/state/com.google/read"
	readerTagStarred = "user/-/state/com.google/starred"
)

// readerEditKind identifies what an edit-tag `i` value refers to.
type readerEditKind int

const (
	readerEditSkip   readerEditKind = iota // unknown or malformed id
	readerEditItem                         // "item/<id>"
	readerEditDigest                       // "digest-item/<id>"
)

// resolveEditTarget parses an edit-tag item id (any of the forms accepted by
// parseReaderItemRef) into a kind and numeric id.
func resolveEditTarget(id string) (readerEditKind, int64) {
	ref, ok := parseReaderItemRef(id)
	if !ok {
		return readerEditSkip, 0
	}
	kind, n := splitReaderItemRef(ref)
	return kind, n
}

// setItemStatus updates an item's status after verifying the item belongs to
// the user. Unknown or foreign items are silently skipped.
func (s *Server) setItemStatus(ctx context.Context, id, userID int64, status string) {
	if _, err := s.queries.GetItemWithFeedOwner(ctx, store.GetItemWithFeedOwnerParams{ID: id, UserID: userID}); err != nil {
		return
	}
	_, _ = s.queries.UpdateItemStatus(ctx, store.UpdateItemStatusParams{ID: id, Status: status})
}

// setItemSaved flips an item's saved flag after verifying the item belongs to
// the user. Unknown or foreign items are silently skipped. Read state is left
// to SetItemSaved's own rule (saving an unread item reads it); the point of the
// flag is that the two are independent, so this never writes `status` directly.
func (s *Server) setItemSaved(ctx context.Context, id, userID, saved int64) {
	if _, err := s.queries.GetItemWithFeedOwner(ctx, store.GetItemWithFeedOwnerParams{ID: id, UserID: userID}); err != nil {
		return
	}
	_, _ = s.queries.SetItemSaved(ctx, store.SetItemSavedParams{ID: id, Saved: saved, MarkRead: saved})
}

// setDigestStatus updates a digest result's status after verifying it belongs
// to the user. Unknown or foreign results are silently skipped.
func (s *Server) setDigestStatus(ctx context.Context, id, userID int64, status string) {
	if _, err := s.queries.GetDigestResult(ctx, store.GetDigestResultParams{ID: id, UserID: userID}); err != nil {
		return
	}
	_, _ = s.queries.UpdateDigestResultStatus(ctx, store.UpdateDigestResultStatusParams{ID: id, UserID: userID, Status: status})
	if status == "read" {
		// Spec: result read -> linked items digested -> read.
		_ = s.queries.MarkDigestResultItemsRead(ctx, id)
	}
}

// applyEditTag applies a single add/remove tag transition to an item or digest
// result. The status mapping is:
//
//	                    | add              | remove
//	read (item)         | read             | unread
//	read (digest)       | read             | new
//	starred (item)      | saved flag on    | saved flag off
//	starred (digest)    | no-op            | no-op
//	any other tag       | no-op            | no-op
//
// Starred is a FLAG, so it is independent of the read transition: removing the
// read tag from a starred item leaves it starred. Before is_saved existed,
// starred WAS the status, so a client that starred an item and then marked it
// unread silently unstarred it.
func (s *Server) applyEditTag(ctx context.Context, userID int64, kind readerEditKind, id int64, add bool, tag string) {
	switch tag {
	case readerTagRead:
		switch kind {
		case readerEditItem:
			if add {
				s.setItemStatus(ctx, id, userID, "read")
			} else {
				s.setItemStatus(ctx, id, userID, "unread")
			}
		case readerEditDigest:
			if add {
				s.setDigestStatus(ctx, id, userID, "read")
			} else {
				s.setDigestStatus(ctx, id, userID, "new")
			}
		}
	case readerTagStarred:
		if kind == readerEditItem {
			saved := int64(0)
			if add {
				saved = 1
			}
			s.setItemSaved(ctx, id, userID, saved)
		}
		// Digest results have no starred state: no-op.
	}
}

// handleReaderEditTag implements POST /reader/api/0/edit-tag. The body is
// form-encoded: `i` is the item id (repeatable), `a` a tag to add, `r` a tag
// to remove. Adds are applied before removes. Every mutation is idempotent and
// best-effort; missing/foreign/unknown ids and tags are silently ignored and
// the handler always answers 200 "OK" for client compatibility.
func (s *Server) handleReaderEditTag(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeText(w, http.StatusOK, "OK")
		return
	}

	ctx := r.Context()
	ids := r.Form["i"]

	for _, tag := range r.Form["a"] {
		for _, idStr := range ids {
			kind, id := resolveEditTarget(idStr)
			if kind == readerEditSkip {
				continue
			}
			s.applyEditTag(ctx, userID, kind, id, true, tag)
		}
	}
	for _, tag := range r.Form["r"] {
		for _, idStr := range ids {
			kind, id := resolveEditTarget(idStr)
			if kind == readerEditSkip {
				continue
			}
			s.applyEditTag(ctx, userID, kind, id, false, tag)
		}
	}

	writeText(w, http.StatusOK, "OK")
}

// handleReaderMarkAllAsRead implements POST /reader/api/0/mark-all-as-read.
// `s` selects the scope: feed/<id>, digest/<id>, the reading-list/all state
// streams, or anything else (treated as a global mark-all-read). `ts` is
// ignored. Feed and digest scopes verify ownership and answer 200 "OK" even
// when the target is missing.
func (s *Server) handleReaderMarkAllAsRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeText(w, http.StatusOK, "OK")
		return
	}

	ctx := r.Context()
	stream := r.Form.Get("s")

	switch {
	case strings.HasPrefix(stream, "feed/"):
		id, err := strconv.ParseInt(strings.TrimPrefix(stream, "feed/"), 10, 64)
		if err != nil {
			writeText(w, http.StatusOK, "OK")
			return
		}
		if _, err := s.queries.GetFeed(ctx, store.GetFeedParams{ID: id, UserID: userID}); err != nil {
			writeText(w, http.StatusOK, "OK")
			return
		}
		if err := s.queries.MarkFeedRead(ctx, id); err != nil {
			writeServerError(w, "failed to mark feed read", err)
			return
		}

	case strings.HasPrefix(stream, "digest/"):
		id, err := strconv.ParseInt(strings.TrimPrefix(stream, "digest/"), 10, 64)
		if err != nil {
			writeText(w, http.StatusOK, "OK")
			return
		}
		if _, err := s.queries.GetDigestConfig(ctx, store.GetDigestConfigParams{ID: id, UserID: userID}); err != nil {
			writeText(w, http.StatusOK, "OK")
			return
		}
		if err := s.queries.MarkDigestConfigRead(ctx, store.MarkDigestConfigReadParams{ConfigID: id, UserID: userID}); err != nil {
			writeServerError(w, "failed to mark digest read", err)
			return
		}
		if _, err := s.queries.MarkDigestConfigResultItemsRead(ctx, store.MarkDigestConfigResultItemsReadParams{UserID: userID, Column2: nil}); err != nil {
			writeServerError(w, "failed to update digest items", err)
			return
		}

	default:
		// reading-list, all, empty/missing s, or any unrecognized stream
		// (e.g. a label): mark everything read.
		if err := s.queries.MarkAllUserItemsRead(ctx, userID); err != nil {
			writeServerError(w, "failed to mark all items read", err)
			return
		}
		if err := s.queries.MarkAllUserDigestResultsRead(ctx, userID); err != nil {
			writeServerError(w, "failed to mark all digest results read", err)
			return
		}
		if _, err := s.queries.MarkDigestConfigResultItemsRead(ctx, store.MarkDigestConfigResultItemsReadParams{UserID: userID, Column2: nil}); err != nil {
			writeServerError(w, "failed to update digest items", err)
			return
		}
	}

	writeText(w, http.StatusOK, "OK")
}

// handleReaderNoOp answers 200 "OK" for Google Reader management endpoints
// that have no meaningful local equivalent (subscription quickadd/edit,
// rename-tag, disable-tag). These exist so clients that call them get a
// successful response instead of a 404.
func (s *Server) handleReaderNoOp(w http.ResponseWriter, r *http.Request) {
	writeText(w, http.StatusOK, "OK")
}
