package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"digesterr/internal/logging"
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

// editTagBatch accumulates one edit-tag request's ID sets by transition, so
// the whole request applies in a handful of batch statements instead of
// 2-3 round trips per ID.
type editTagBatch struct {
	itemRead   []int64
	itemUnread []int64
	itemSave   []int64
	itemUnsave []int64
	digestRead []int64
	digestNew  []int64
}

func (b *editTagBatch) empty() bool {
	return len(b.itemRead) == 0 && len(b.itemUnread) == 0 &&
		len(b.itemSave) == 0 && len(b.itemUnsave) == 0 &&
		len(b.digestRead) == 0 && len(b.digestNew) == 0
}

// addEditTag accumulates a single add/remove tag transition for an item or
// digest result. The status mapping is:
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
func (b *editTagBatch) addEditTag(kind readerEditKind, id int64, add bool, tag string) {
	switch tag {
	case readerTagRead:
		switch kind {
		case readerEditItem:
			if add {
				b.itemRead = append(b.itemRead, id)
			} else {
				b.itemUnread = append(b.itemUnread, id)
			}
		case readerEditDigest:
			if add {
				b.digestRead = append(b.digestRead, id)
			} else {
				b.digestNew = append(b.digestNew, id)
			}
		}
	case readerTagStarred:
		if kind == readerEditItem {
			if add {
				b.itemSave = append(b.itemSave, id)
			} else {
				b.itemUnsave = append(b.itemUnsave, id)
			}
		}
		// Digest results have no starred state: no-op.
	}
}

// idsJSON renders an ID set as a JSON array for the json_each batch queries.
func idsJSON(ids []int64) string {
	raw, _ := json.Marshal(ids)
	return string(raw)
}

// applyEditTagBatch applies an accumulated batch in one transaction: item
// status flips, saved-flag flips, digest status flips, then the digest read
// cascade (result read -> linked items digested -> read). Ownership is
// enforced inside each statement, so unknown or foreign IDs match nothing and
// the request stays best-effort with its 200-OK contract. A failure rolls
// everything back instead of leaving a digest read while its items stay
// digested.
func (s *Server) applyEditTagBatch(ctx context.Context, userID int64, b *editTagBatch) error {
	db := s.queries.DB()
	if db == nil {
		return fmt.Errorf("edit-tag requires a *sql.DB-backed Queries")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning edit-tag batch: %w", err)
	}
	defer tx.Rollback()
	qtx := s.queries.WithTx(tx)

	apply := func(ids []int64, op string, fn func(context.Context, *store.Queries, string) error) error {
		if len(ids) == 0 {
			return nil
		}
		if err := fn(ctx, qtx, idsJSON(ids)); err != nil {
			return fmt.Errorf("edit-tag %s: %w", op, err)
		}
		return nil
	}
	if err := apply(b.itemRead, "item-read", func(ctx context.Context, q *store.Queries, ids string) error {
		_, err := q.UpdateItemStatusByIDs(ctx, store.UpdateItemStatusByIDsParams{Status: "read", Ids: ids, UserID: userID})
		return err
	}); err != nil {
		return err
	}
	if err := apply(b.itemUnread, "item-unread", func(ctx context.Context, q *store.Queries, ids string) error {
		_, err := q.UpdateItemStatusByIDs(ctx, store.UpdateItemStatusByIDsParams{Status: "unread", Ids: ids, UserID: userID})
		return err
	}); err != nil {
		return err
	}
	if err := apply(b.itemSave, "item-save", func(ctx context.Context, q *store.Queries, ids string) error {
		_, err := q.SetItemSavedByIDs(ctx, store.SetItemSavedByIDsParams{Saved: 1, Ids: ids, UserID: userID})
		return err
	}); err != nil {
		return err
	}
	if err := apply(b.itemUnsave, "item-unsave", func(ctx context.Context, q *store.Queries, ids string) error {
		_, err := q.SetItemSavedByIDs(ctx, store.SetItemSavedByIDsParams{Saved: 0, Ids: ids, UserID: userID})
		return err
	}); err != nil {
		return err
	}
	if err := apply(b.digestRead, "digest-read", func(ctx context.Context, q *store.Queries, ids string) error {
		// Cascade only the results that ACTUALLY transitioned. Driving it from
		// the submitted id set made a re-add of the read tag to an already-read
		// result consume an item that a LATER digest generation had re-linked
		// and set back to 'digested' -- the same state-test-vs-transition defect
		// fixed for the bulk, per-config and relative paths in 39baa90, which
		// the Reader path was left out of.
		//
		// TransitionDigestResultStatusByIDs returns exactly the ids it changed,
		// so the cascade is driven by the transition itself. An id set that
		// matched nothing issues no cascade statement at all.
		changed, err := q.TransitionDigestResultStatusByIDs(ctx, store.TransitionDigestResultStatusByIDsParams{
			Status: "read",
			Ids:    ids,
			UserID: userID,
		})
		if err != nil {
			return err
		}
		return cascadeItemsForReadResults(ctx, q, userID, changed)
	}); err != nil {
		return err
	}
	if err := apply(b.digestNew, "digest-new", func(ctx context.Context, q *store.Queries, ids string) error {
		_, err := q.UpdateDigestResultStatusByIDs(ctx, store.UpdateDigestResultStatusByIDsParams{Status: "new", Ids: ids, UserID: userID})
		return err
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing edit-tag batch: %w", err)
	}
	return nil
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

	var batch editTagBatch
	for _, tag := range r.Form["a"] {
		for _, idStr := range ids {
			kind, id := resolveEditTarget(idStr)
			if kind == readerEditSkip {
				continue
			}
			batch.addEditTag(kind, id, true, tag)
		}
	}
	for _, tag := range r.Form["r"] {
		for _, idStr := range ids {
			kind, id := resolveEditTarget(idStr)
			if kind == readerEditSkip {
				continue
			}
			batch.addEditTag(kind, id, false, tag)
		}
	}
	if !batch.empty() {
		// Best-effort stays: a DB failure is logged but the handler still
		// answers 200 OK for client compatibility.
		if err := s.applyEditTagBatch(ctx, userID, &batch); err != nil {
			slog.Warn("reader edit-tag batch failed", "user_id", userID, "error", err)
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
		if err := s.markDigestScopeRead(ctx, userID, &id); err != nil {
			writeServerError(w, "failed to mark digest read", err)
			return
		}

	case strings.HasPrefix(stream, readerLabelPrefix):
		// A feed category, advertised as a label stream (see readerLabelID).
		// This must be handled explicitly: categories are per-user, so the
		// category name is only meaningful combined with userID.
		category := strings.TrimPrefix(stream, readerLabelPrefix)
		if err := s.queries.MarkCategoryItemsRead(ctx, store.MarkCategoryItemsReadParams{
			UserID:   userID,
			Category: category,
		}); err != nil {
			writeServerError(w, "failed to mark category read", err)
			return
		}

	case stream == "" || stream == readerStreamReadingList || stream == readerStreamFresh:
		// The reading-list tag IS the whole account, so this scope is global by
		// definition. An empty/missing `s` is the same request.
		//
		// `fresh` belongs here because it is a LISTING alias of the reading
		// list (see streamEntries: `fresh` resolves to exactly the unread items
		// plus new results that the reading list shows). Leaving it to the
		// default branch made mark-all a silent no-op on the one stream a reader
		// client is most likely to issue it from, while listing returned real
		// data -- the client saw unread items, asked to clear them, got 200, and
		// nothing changed.
		if err := s.markDigestScopeRead(ctx, userID, nil); err != nil {
			writeServerError(w, "failed to mark all digest results read", err)
			return
		}
		if err := s.queries.MarkAllUserItemsRead(ctx, userID); err != nil {
			writeServerError(w, "failed to mark all items read", err)
			return
		}

	default:
		// An unknown stream id is NOT a request to mark everything read. It is
		// answered OK, because Google Reader clients treat a non-200 as a
		// retryable failure and would hammer the endpoint, but nothing is
		// modified: silently widening an unrecognized scope to the whole
		// account turns a client typo or an unsupported stream into data loss.
		slog.Info("mark-all-as-read: ignoring unrecognized stream",
			logging.CategoryAttr(logging.CategoryHTTP), "stream", stream, "user_id", userID)
	}

	writeText(w, http.StatusOK, "OK")
}

// markDigestScopeRead marks a digest config's results (or every result when
// configID is nil) read together with their linked items' digested->read
// cascade, in one transaction. A partial chain (results read, items still
// digested) is the failure the old three-statement sequence could leave.
//
// The work itself lives in markDigestResultsRead, shared with the REST
// handlers: the Reader and REST paths must not drift on whether the cascade is
// transactional, which is exactly the drift this function used to represent.
func (s *Server) markDigestScopeRead(ctx context.Context, userID int64, configID *int64) error {
	if configID != nil {
		if _, err := s.queries.GetDigestConfig(ctx, store.GetDigestConfigParams{ID: *configID, UserID: userID}); err != nil {
			return nil // missing scope: best-effort OK, same as before
		}
	}
	return s.markDigestResultsRead(ctx, userID, configID)
}

// handleReaderNoOp answers 200 "OK" for Google Reader management endpoints
// that have no meaningful local equivalent (subscription quickadd/edit,
// rename-tag, disable-tag). These exist so clients that call them get a
// successful response instead of a 404.
func (s *Server) handleReaderNoOp(w http.ResponseWriter, r *http.Request) {
	writeText(w, http.StatusOK, "OK")
}
