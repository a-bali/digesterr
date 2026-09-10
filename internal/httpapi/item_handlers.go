package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"digesterr/internal/digest"
	"digesterr/internal/logging"
	"digesterr/internal/store"
)

type updateItemStatusRequest struct {
	Status string `json:"status"`
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
	writeJSON(w, http.StatusAccepted, item)

	// Extract in background
	go s.doExtractItem(id, item.Url)
}

type summarizeRequest struct {
	SentenceCount int    `json:"sentence_count"`
	Prompt        string `json:"prompt"`
}

type summarizeResponse struct {
	ContentSummary  string `json:"content_summary"`
	CommentsSummary string `json:"comments_summary"`
}

func (s *Server) handleSummarizeItem(w http.ResponseWriter, r *http.Request) {
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

	if !s.llmClient.HasAPIKey() {
		writeError(w, http.StatusBadRequest, "LLM is not configured")
		return
	}

	var req summarizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.SentenceCount <= 0 {
		req.SentenceCount = 5
	}
	if req.SentenceCount > 50 {
		req.SentenceCount = 50
	}

	// Build content: prefer extracted content, fall back to original.
	content := item.ExtractContent
	if content == "" || item.ExtractStatus != "done" {
		content = item.Content
	}
	if content == "" {
		writeError(w, http.StatusBadRequest, "no content available to summarize")
		return
	}
	content = digest.CleanContentForLLM(content)

	// Fetch top comments for inclusion in summary.
	comments, _ := s.fetchTopComments(r, item, 5, false)

	// Build prompt with configurable sentence count and prompt text.
	systemPrompt := req.Prompt
	if systemPrompt == "" {
		systemPrompt = fmt.Sprintf(
			"You are a helpful assistant that summarizes articles and their comments. "+
				"Summarize the content in about %d sentences, then separately summarize the comments in about %d sentences. "+
				"Format your response as JSON with two fields: \"content_summary\" and \"comments_summary\". "+
				"Do not include any text outside the JSON.",
			req.SentenceCount,
			max(req.SentenceCount/3, 1),
		)
	}

	userPrompt := "Content:\n\n" + content
	if len(comments) > 0 {
		userPrompt += "\n\nTop comments:\n\n"
		for i, c := range comments {
			text := c.Text
			if len(text) > 500 {
				text = text[:500] + "…"
			}
			prefix := ""
			if c.Author != "" {
				prefix = c.Author + ": "
			}
			userPrompt += fmt.Sprintf("%d. %s%s\n", i+1, prefix, text)
		}
	}

	result, err := s.llmClient.Chat(r.Context(), systemPrompt, userPrompt)
	if err != nil {
		writeServerError(w, "LLM request failed", err)
		return
	}

	// Parse JSON response from LLM, fall back to plain text if parsing fails.
	var parsed summarizeResponse
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		parsed = summarizeResponse{ContentSummary: result}
	}

	writeJSON(w, http.StatusOK, parsed)
}

func (s *Server) doExtractItem(itemID int64, url string) {
	// Delegate to the poller's extraction implementation — same meta-line
	// format, retry accounting, and persistence as feed-level extraction.
	ctx := context.Background()
	item, err := s.queries.GetItem(ctx, itemID)
	if err != nil {
		slog.Error("failed to fetch item for extraction", logging.CategoryAttr(logging.CategoryItem), "item_id", itemID, "error", err)
		return
	}
	logger := slog.With(logging.CategoryAttr(logging.CategoryItem), "item_id", itemID, "url", url)
	feed, _ := s.queries.GetFeedByID(ctx, item.FeedID)
	client := &http.Client{Timeout: 30 * time.Second, Transport: s.poller.MetricsTransport()}
	s.poller.ExtractSingleItem(ctx, client, item, feed, logger)
}
