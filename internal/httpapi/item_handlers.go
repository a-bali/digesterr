package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	readability "codeberg.org/readeck/go-readability/v2"
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

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		logger.Error("failed to create extract request", "error", err)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:134.0) Gecko/20100101 Firefox/134.0")

	resp, err := client.Do(req)
	if err != nil {
		logger.Error("failed to fetch URL for extraction", "error", err)
		s.failExtraction(itemID, fmt.Sprintf("[%s] fetch failed: %v", time.Now().Format("2006-01-02 15:04"), err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Error("extraction HTTP error", "status", resp.StatusCode)
		s.failExtraction(itemID, fmt.Sprintf("[%s] HTTP %d", time.Now().Format("2006-01-02 15:04"), resp.StatusCode))
		return
	}

	contentType := resp.Header.Get("Content-Type")
	contentType = strings.ToLower(contentType)

	switch {
	case strings.HasPrefix(contentType, "text/html") || strings.HasPrefix(contentType, "application/xhtml+xml"):
		s.extractItemHTML(ctx, itemID, resp, req, logger)

	case strings.HasPrefix(contentType, "image/"):
		s.extractItemDone(ctx, itemID, fmt.Sprintf(`<img src="%s" style="max-width:100%%" />`, htmlEscape(url)))

	case strings.HasPrefix(contentType, "video/"):
		s.extractItemDone(ctx, itemID, fmt.Sprintf(
			`<video src="%s" controls style="max-width:100%%"><p>Your browser does not support the video tag. <a href="%s" target="_blank">Watch video ↗</a></p></video>`,
			htmlEscape(url), htmlEscape(url)))

	case strings.HasPrefix(contentType, "audio/"):
		s.extractItemDone(ctx, itemID, fmt.Sprintf(
			`<audio src="%s" controls><p>Your browser does not support the audio tag. <a href="%s" target="_blank">Listen ↗</a></p></audio>`,
			htmlEscape(url), htmlEscape(url)))

	case strings.HasPrefix(contentType, "text/"):
		body, err := io.ReadAll(io.LimitReader(resp.Body, 100<<10))
		if err != nil {
			s.failExtraction(itemID, fmt.Sprintf("[%s] read body: %v", time.Now().Format("2006-01-02 15:04"), err))
			return
		}
		s.extractItemDone(ctx, itemID, fmt.Sprintf(`<pre style="white-space:pre-wrap">%s</pre>`, htmlEscape(string(body))))

	default:
		s.extractItemDone(ctx, itemID, fmt.Sprintf(`<p><a href="%s" target="_blank">Open in browser ↗</a></p>`, htmlEscape(url)))
	}
}

func (s *Server) extractItemHTML(ctx context.Context, itemID int64, resp *http.Response, req *http.Request, logger *slog.Logger) {
	article, err := readability.FromReader(resp.Body, req.URL)
	if err != nil {
		logger.Error("readability extraction failed", "error", err)
		s.failExtraction(itemID, fmt.Sprintf("[%s] extraction failed: %v", time.Now().Format("2006-01-02 15:04"), err))
		return
	}

	var buf bytes.Buffer
	if err := article.RenderHTML(&buf); err != nil {
		logger.Error("failed to render article HTML", "error", err)
		s.failExtraction(itemID, fmt.Sprintf("[%s] render failed: %v", time.Now().Format("2006-01-02 15:04"), err))
		return
	}

	_, err = s.queries.UpdateItemExtraction(ctx, store.UpdateItemExtractionParams{
		ID:             itemID,
		ExtractContent: buf.String(),
		ExtractStatus:  "done",
		ExtractRetries: 0,
	})
	if err != nil {
		logger.Error("failed to save extracted content", "error", err)
		return
	}
	logger.Info("content extracted successfully", "length", buf.Len())
}

func (s *Server) extractItemDone(ctx context.Context, itemID int64, content string) {
	_, err := s.queries.UpdateItemExtraction(ctx, store.UpdateItemExtractionParams{
		ID:             itemID,
		ExtractContent: content,
		ExtractStatus:  "done",
		ExtractRetries: 0,
	})
	if err != nil {
		slog.Error("failed to save extraction result", "item_id", itemID, "error", err)
	}
}

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
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