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
	"digesterr/internal/llmjson"
	"digesterr/internal/logging"
	"digesterr/internal/store"
)

type summarizeRequest struct {
	SentenceCount int    `json:"sentence_count"`
	Prompt        string `json:"prompt"`
	// Optional length window. The UI offers presets described as a range
	// ("10-15 sentences"), and a bare sentence count alone made the model
	// undershoot, so the range is passed through when it is known.
	MinSentences int `json:"min_sentences"`
	MaxSentences int `json:"max_sentences"`
	// Detail names the character of the summary — "gist", "balanced" or
	// "detailed". Length alone was not enough to tell the presets apart: they
	// differed only in sentence count, so all three read the same way. Optional;
	// when absent it is derived from the length window.
	Detail string `json:"detail"`
}

// defaultSummaryPrompt is the built-in system prompt for per-item summaries.
// Override it with llm.summary_prompt in the config file.
const defaultSummaryPrompt = "You are a helpful assistant that summarizes articles and their comments. " +
	"Summarize the content in {content_sentences} sentences, then separately summarize the comments in {comment_sentences} sentences. " +
	"Aim for {style}. " +
	"Format your response as JSON with two fields: \"content_summary\" and \"comments_summary\". " +
	"Do not include any text outside the JSON."

// Summary detail levels. The presets are defined by length, but length alone
// made them read the same way — just longer — so each level also carries a
// character the prompt asks for.
const (
	detailGist     = "gist"
	detailBalanced = "balanced"
	detailDetailed = "detailed"
)

// normalizeDetail resolves the requested detail level, deriving it from the
// length window when the caller did not name one. The thresholds line up with
// the UI presets (1-2 / 5-7 / 12-18 sentences).
func normalizeDetail(requested string, maxSentences int) string {
	switch d := strings.ToLower(strings.TrimSpace(requested)); d {
	case detailGist, detailBalanced, detailDetailed:
		return d
	}
	switch {
	case maxSentences <= 3:
		return detailGist
	case maxSentences <= 9:
		return detailBalanced
	default:
		return detailDetailed
	}
}

// detailStyle renders a detail level's character for the prompt.
func detailStyle(detail string) string {
	switch detail {
	case detailGist:
		return "a one-line gist: only the single most important takeaway, with no supporting detail"
	case detailDetailed:
		return "a detailed summary: the full argument with its supporting context, figures and nuance"
	default:
		return "a balanced summary: the key points and enough context to see why they matter, without minor detail"
	}
}

// renderSummaryPrompt substitutes the prompt placeholders. Lengths and the
// style text arrive pre-rendered, so a range ("2-3") and a single count read
// the same way, and a caller with a custom prompt can use {style} too.
func renderSummaryPrompt(tmpl, style, contentSentences, commentSentences string) string {
	return strings.NewReplacer(
		"{style}", style,
		"{content_sentences}", contentSentences,
		"{comment_sentences}", commentSentences,
	).Replace(tmpl)
}

// lengthLabel renders a sentence-count window for the prompt. A window wider
// than one sentence reads as a range, which is how the UI describes the presets.
func lengthLabel(minSentences, maxSentences int) string {
	if minSentences > 0 && maxSentences > minSentences {
		return strconv.Itoa(minSentences) + "-" + strconv.Itoa(maxSentences)
	}
	if maxSentences > 0 {
		return strconv.Itoa(maxSentences)
	}
	return strconv.Itoa(minSentences)
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

// parseSummaryResponse turns the model's reply into the two summary fields.
//
// The prompt asks for bare JSON, but models routinely wrap it in a markdown
// code fence (```json ... ```), surround it with prose, or double-encode it as
// a JSON string. Each shape is unwrapped in turn. Anything left that still
// looks like JSON is dropped rather than rendered — passing it through is how
// raw JSON ended up in the pane — while prose, which is a legitimate answer, is
// kept verbatim.
func parseSummaryResponse(raw string) summarizeResponse {
	text := strings.TrimSpace(raw)
	if text == "" {
		return summarizeResponse{}
	}

	if parsed, ok := decodeSummary(text); ok {
		return parsed
	}
	// A fence or surrounding prose: take the outermost object in the reply.
	if obj, ok := llmjson.FirstObject(text); ok {
		if parsed, ok := decodeSummary(obj); ok {
			return parsed
		}
	}
	// A double-encoded reply: the whole thing is a JSON string, so unwrap it
	// first and then look for the object inside.
	if inner, ok := llmjson.UnwrapString(text); ok {
		if obj, ok := llmjson.FirstObject(inner); ok {
			if parsed, ok := decodeSummary(obj); ok {
				return parsed
			}
		}
	}
	// Everything above failed, so the reply is not decodable JSON. Handing it
	// back verbatim is how the pane ended up rendering raw JSON, so JSON-looking
	// text is dropped instead. Plain prose is still a legitimate answer and is
	// kept — losing it would be worse than showing it.
	if looksLikeJSON(text) {
		return summarizeResponse{}
	}
	return summarizeResponse{ContentSummary: text}
}

// looksLikeJSON reports whether a reply is JSON that we failed to decode, as
// opposed to prose that merely happens to be the model's answer.
func looksLikeJSON(text string) bool {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
		return true
	}
	return strings.Contains(t, `"content_summary"`) || strings.Contains(t, `"comments_summary"`)
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
	if !decodeBody(w, r, &req) {
		return
	}
	if req.SentenceCount <= 0 {
		req.SentenceCount = 5
	}
	if req.SentenceCount > 50 {
		req.SentenceCount = 50
	}

	// Resolve the length window. A caller that only knows a single count keeps
	// working: the window collapses to that count.
	minSentences, maxSentences := req.MinSentences, req.MaxSentences
	if maxSentences <= 0 || maxSentences > 50 {
		maxSentences = req.SentenceCount
	}
	if minSentences <= 0 || minSentences > maxSentences {
		minSentences = maxSentences
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
	systemPrompt = renderSummaryPrompt(systemPrompt,
		detailStyle(normalizeDetail(req.Detail, maxSentences)),
		lengthLabel(minSentences, maxSentences),
		lengthLabel(max(minSentences/3, 1), max(maxSentences/3, 1)))

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

	result, err := s.resolveLLMClient(r.Context(), userID).Chat(r.Context(), systemPrompt, userPrompt)
	if err != nil {
		writeServerError(w, "LLM request failed", err)
		return
	}

	writeJSON(w, http.StatusOK, parseSummaryResponse(result))
}

func (s *Server) doExtractItem(ctx context.Context, itemID int64, url string) {
	// Delegate to the poller's extraction implementation — same meta-line
	// format, retry accounting, and persistence as feed-level extraction.
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
