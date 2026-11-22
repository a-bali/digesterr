package httpapi

import (
	"net/http"
	"strconv"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

type updateItemStatusRequest struct {
	Status string `json:"status"`
	// Saved is a flag, not a status: it is orthogonal to read state, so
	// `{"saved":true}` stars an item without dictating what read state it has,
	// and `{"status":"unread"}` un-reads it without unstarring it. A request may
	// carry one or the other, or both.
	Saved *bool `json:"saved"`
}

func (s *Server) handleUpdateItemStatus(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	_, err = s.queries.GetItemWithFeedOwner(r.Context(), store.GetItemWithFeedOwnerParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}

	var req updateItemStatusRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Status == "" && req.Saved == nil {
		writeError(w, http.StatusBadRequest, "status or saved is required")
		return
	}
	// 'saved' is deliberately NOT a valid status any more: it was a status
	// before is_saved existed, and accepting it here would let a client put the
	// column back into a state the app no longer understands.
	if req.Status != "" {
		validStatuses := map[string]bool{"unread": true, "read": true, "digested": true}
		if !validStatuses[req.Status] {
			writeError(w, http.StatusBadRequest, "invalid status; must be unread, read, or digested")
			return
		}
	}
	// Both fields in one request: apply the status first, then savedness, so the
	// saved handler's own read-state rule (starring an unread item marks it read)
	// sees the status the caller asked for.
	var item store.Item
	if req.Status != "" {
		item, err = s.queries.UpdateItemStatus(r.Context(), store.UpdateItemStatusParams{
			ID:     id,
			Status: req.Status,
		})
		if err != nil {
			writeError(w, http.StatusNotFound, "item not found")
			return
		}
	}
	if req.Saved != nil {
		savedArg := int64(0)
		if *req.Saved {
			savedArg = 1
		}
		item, err = s.queries.SetItemSaved(r.Context(), store.SetItemSavedParams{
			Saved:    savedArg,
			MarkRead: savedArg,
			ID:       id,
		})
		if err != nil {
			writeError(w, http.StatusNotFound, "item not found")
			return
		}
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleGetItem(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	item, err := s.queries.GetItemWithFeedOwner(r.Context(), store.GetItemWithFeedOwnerParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}
	// Repair a body stored before the image/filler rules existed, on the single
	// item the reader just opened. The Reader API path does the same for the
	// bodies it serves; both surfaces render the same stored markup, so both
	// have to apply the same upgrade. The stored row is not modified.
	item.Content = feed.UpgradeStoredBody(item.Content)
	item.ExtractContent = feed.UpgradeStoredBody(item.ExtractContent)
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleExtractItem(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	item, err := s.queries.GetItemWithFeedOwner(r.Context(), store.GetItemWithFeedOwnerParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}

	if item.Url == "" {
		writeError(w, http.StatusBadRequest, "item has no URL to extract content from")
		return
	}

	// Set pending immediately, preserve existing content for history
	item, err = s.queries.UpdateItemExtraction(r.Context(), store.UpdateItemExtractionParams{
		ID:             id,
		ExtractContent: item.ExtractContent,
		ExtractStatus:  "pending",
		ExtractRetries: item.ExtractRetries,
	})
	if err != nil {
		writeServerError(w, "failed to set extraction status", err)
		return
	}
	// Extract in background, bounded by the extraction semaphore: without it
	// one burst of requests fans out unbounded goroutines + HTTP clients. The
	// semaphore acquire is ctx-aware, so a client that disconnects before its
	// turn stops waiting instead of leaking a slot.
	select {
	case s.extractSem <- struct{}{}:
	case <-r.Context().Done():
		writeError(w, http.StatusServiceUnavailable, "server busy, try again")
		return
	}
	writeJSON(w, http.StatusAccepted, item)

	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		defer func() { <-s.extractSem }()
		s.doExtractItem(s.extractCtx(), id, item.Url)
	}()
}

// handleDeleteItemExtraction drops an item's extracted body so it falls back to
// the original content from the feed. The item is otherwise untouched (status,
// points, comments), and the extraction columns are reset rather than emptied so
// the next extraction pass can tell a deliberate deletion from "never extracted"
// — see ClearItemExtraction. Naturally idempotent: deleting twice is a no-op.
func (s *Server) handleDeleteItemExtraction(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	// Ownership check only; the item itself is what the update is keyed on.
	if _, err := s.queries.GetItemWithFeedOwner(r.Context(), store.GetItemWithFeedOwnerParams{
		ID:     id,
		UserID: userID,
	}); err != nil {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}

	item, err := s.queries.ClearItemExtraction(r.Context(), id)
	if err != nil {
		writeServerError(w, "failed to clear extracted content", err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
