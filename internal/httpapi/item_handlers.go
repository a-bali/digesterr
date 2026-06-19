package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"digesterr/internal/feed"
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

func (s *Server) handleGetItem(w http.ResponseWriter, r *http.Request) {
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
	item, err := s.queries.GetItemWithFeedOwner(r.Context(), store.GetItemWithFeedOwnerParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleExtractItem(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, http.StatusAccepted, item)

	// Extract in background
	go s.doExtractItem(id, item.Url)
}

func (s *Server) doExtractItem(itemID int64, url string) {
	ctx := context.Background()
	logger := slog.With("item_id", itemID, "url", url)

	content, err := feed.ExtractContent(ctx, url, &http.Client{Timeout: 30 * time.Second}, logger)
	if err != nil {
		logger.Error("extraction failed", "error", err)
		s.failExtraction(itemID, fmt.Sprintf("[%s] %v", time.Now().Format("2006-01-02 15:04"), err))
		return
	}

	_, err = s.queries.UpdateItemExtraction(ctx, store.UpdateItemExtractionParams{
		ID:             itemID,
		ExtractContent: content,
		ExtractStatus:  "done",
		ExtractRetries: 0,
	})
	if err != nil {
		logger.Error("failed to save extracted content", "error", err)
		return
	}
}

func (s *Server) failExtraction(itemID int64, errorLine string) {
	ctx := context.Background()
	item, err := s.queries.GetItem(ctx, itemID)
	if err != nil {
		slog.Error("failed to fetch item for extraction failure", "item_id", itemID, "error", err)
		return
	}
	newContent := strings.TrimSpace(item.ExtractContent)
	if newContent != "" {
		newContent += "\n"
	}
	newContent += errorLine

	_, err = s.queries.UpdateItemExtraction(ctx, store.UpdateItemExtractionParams{
		ID:             itemID,
		ExtractContent: newContent,
		ExtractStatus:  "failed",
		ExtractRetries: item.ExtractRetries + 1,
	})
	if err != nil {
		slog.Error("failed to update extraction failure", "item_id", itemID, "error", err)
	}
}