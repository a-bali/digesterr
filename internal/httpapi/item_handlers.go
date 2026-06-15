package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"digesterr/internal/store"
)

type updateItemStatusRequest struct {
	Status string `json:"status"`
}

func (s *Server) handleUpdateItemStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	validStatuses := map[string]bool{"unread": true, "read": true, "digested": true, "saved": true}
	if !validStatuses[req.Status] {
		writeError(w, http.StatusBadRequest, "invalid status; must be unread, read, digested, or saved")
		return
	}
	item, err := s.queries.UpdateItemStatus(r.Context(), store.UpdateItemStatusParams{
		ID:     id,
		Status: req.Status,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}
	writeJSON(w, http.StatusOK, item)
}