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

// defaultSummaryPrompt is the built-in system prompt for per-item summaries.
// Override it with llm.summary_prompt in the config file.
const defaultSummaryPrompt = "You are a helpful assistant that summarizes articles and their comments. " +
	"Summarize the content in about {content_sentences} sentences, then separately summarize the comments in about {comment_sentences} sentences. " +
	"Format your response as JSON with two fields: \"content_summary\" and \"comments_summary\". " +
	"Do not include any text outside the JSON."

// renderSummaryPrompt substitutes the sentence-count placeholders.
func renderSummaryPrompt(tmpl string, contentSentences, commentSentences int) string {
	return strings.NewReplacer(
		"{content_sentences}", strconv.Itoa(contentSentences),
		"{comment_sentences}", strconv.Itoa(commentSentences),
	).Replace(tmpl)
}

type summarizeResponse struct {
	ContentSummary  string `json:"content_summary"`
	CommentsSummary string `json:"comments_summary"`
}

// decodeSummary reads the two expected fields out of a JSON object. Values are
// accepted as strings; a model that nests the object one level deeper is
// unwrapped rather than thrown away.
func decodeSummary(raw string) (summarizeResponse, bool) {
	var parsed summarizeResponse
	if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
		if parsed.ContentSummary != "" || parsed.CommentsSummary != "" {
			return parsed, true
		}
		return summarizeResponse{}, false
	}

	// The values were not plain strings — coerce whatever came back, so a
	// nested or non-string field does not push the whole reply to raw text.
	var generic map[string]any
	if err := json.Unmarshal([]byte(raw), &generic); err != nil {
		return summarizeResponse{}, false
	}
	var out summarizeResponse
	if v, ok := stringField(generic["content_summary"]); ok {
		out.ContentSummary = v
	}
	if v, ok := stringField(generic["comments_summary"]); ok {
		out.CommentsSummary = v
	}
	if out.ContentSummary == "" && out.CommentsSummary == "" {
		return summarizeResponse{}, false
	}
	return out, true
}

// stringField coerces a decoded JSON value into display text.
func stringField(v any) (string, bool) {
	switch val := v.(type) {
	case string:
		if strings.TrimSpace(val) == "" {
			return "", false
		}
		return val, true
	case []any:
		// A list of sentences/paragraphs reads fine joined by blank lines.
		var parts []string
		for _, e := range val {
			if s, ok := stringField(e); ok {
				parts = append(parts, s)
			}
		}
		if len(parts) == 0 {
			return "", false
		}
		return strings.Join(parts, "\n\n"), true
	case map[string]any:
		// Try the obvious keys, then fall back to the first usable string.
		for _, key := range []string{"text", "summary", "content", "value"} {
			if s, ok := stringField(val[key]); ok {
				return s, true
			}
		}
		return "", false
	default:
		return "", false
	}
}

// firstJSONObject returns the outermost {...} span in s, ignoring braces inside
// quoted strings so a brace in a summary does not end the scan early.
func firstJSONObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

// parseSummaryResponse turns the model's reply into the two summary fields.
//
// The prompt asks for bare JSON, but models routinely wrap it in a markdown
// code fence (```json ... ```) or add a sentence before or after it. Sending
// that straight back made the UI render raw JSON where the summary should be,
// so the JSON is located and unwrapped first. A reply with no usable object is
// kept verbatim as the content summary rather than dropped — plain prose is a
// legitimate answer and losing it would be worse than showing it.
func parseSummaryResponse(raw string) summarizeResponse {
	text := strings.TrimSpace(raw)
	if text == "" {
		return summarizeResponse{}
	}

	if parsed, ok := decodeSummary(text); ok {
		return parsed
	}
	if obj, ok := firstJSONObject(text); ok {
		if parsed, ok := decodeSummary(obj); ok {
			return parsed
		}
	}
	return summarizeResponse{ContentSummary: text}
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

	// Prompt precedence: explicit request override > llm.summary_prompt from
	// the config file > built-in default. Placeholders are substituted for all
	// of them, so a custom prompt can still react to the sentence slider.
	systemPrompt := req.Prompt
	if systemPrompt == "" {
		systemPrompt = s.summaryPrompt
	}
	if systemPrompt == "" {
		systemPrompt = defaultSummaryPrompt
	}
	systemPrompt = renderSummaryPrompt(systemPrompt, req.SentenceCount, max(req.SentenceCount/3, 1))

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

	writeJSON(w, http.StatusOK, parseSummaryResponse(result))
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
