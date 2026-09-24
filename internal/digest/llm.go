package digest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"
)

// llmResponseLimit bounds how much of an LLM response is read into memory.
//
// Every other fetcher in this repo bounds its read (rss.go and
// twitter_fetcher.go at 10MB, email_client.go at 8MB, the Jina extractor at
// 500KB); these two call sites were the outlier. A digest response is a few KB
// of JSON, so 10MB is generous while still guaranteeing that a malformed or
// hostile response cannot exhaust memory.
const llmResponseLimit = 10 << 20

// readBoundedBody reads at most llmResponseLimit bytes and reports an error when
// the response exceeds it, rather than silently truncating.
//
// Truncation would be worse than an error here: a truncated JSON body fails to
// parse with a confusing syntax error, whereas an explicit over-limit error
// names the real problem.
func readBoundedBody(r io.Reader) ([]byte, error) {
	// One byte past the limit, so hitting the cap is distinguishable from a
	// response that exactly fills it.
	body, err := io.ReadAll(io.LimitReader(r, llmResponseLimit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > llmResponseLimit {
		return nil, fmt.Errorf("response exceeds the %d byte limit", llmResponseLimit)
	}
	return body, nil
}

// LLMRecordFunc records an outbound LLM API call.
type LLMRecordFunc func(timestamp time.Time, url string, statusCode int, durationMs int64, bytes int64, err string)

type LLMClient struct {
	endpoint string
	apiKey   string
	model    string
	client   *http.Client
	recordFn LLMRecordFunc
}

// Chatter is the subset of the LLM client used by digest generation,
// declared as an interface so generation can be tested with a fake.
//
// llmChatter is kept as an alias so existing references keep compiling;
// new code should name Chatter (exported so the scheduler's per-user
// resolver can be typed with it from outside this package).
type Chatter interface {
	Chat(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

type llmChatter = Chatter

// LLMTimeoutDefault bounds a single LLM request when the caller passes no
// timeout. It matches config.LLMTimeoutDefault.
//
// One client call is not necessarily one upstream attempt: when the endpoint is
// a gateway that fails over between providers, the gateway may run several
// sequential attempts before answering. The client budget therefore has to
// exceed the sum of the gateway's own per-attempt budgets, or the client gives
// up mid-failover and discards an answer that was still coming.
const LLMTimeoutDefault = 5 * time.Minute

// NewLLMClient builds a client for an OpenAI-compatible chat completions API.
// An optional timeout overrides LLMTimeoutDefault; zero or negative (including
// no argument at all) means the default.
func NewLLMClient(endpoint, apiKey, model string, timeout ...time.Duration) *LLMClient {
	endpoint = strings.TrimRight(endpoint, "/")
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}
	d := LLMTimeoutDefault
	if len(timeout) > 0 && timeout[0] > 0 {
		d = timeout[0]
	}
	return &LLMClient{
		endpoint: endpoint,
		apiKey:   apiKey,
		model:    model,
		client:   &http.Client{Timeout: d},
	}
}

// SetRecordFunc sets the callback for recording outbound LLM API calls.
func (c *LLMClient) SetRecordFunc(fn LLMRecordFunc) {
	c.recordFn = fn
}

// WithModel returns a copy of the client that sends a different model name.
// The HTTP client (including its timeout) and the metrics callback are
// shared: a per-user model override must not change transport behavior.
func (c *LLMClient) WithModel(model string) *LLMClient {
	cp := *c
	cp.model = model
	return &cp
}

// Models returns the model IDs the endpoint offers, for the settings dialog's
// picker. OpenAI-compatible servers expose this at GET /models.
//
// This is a convenience, never a gate: the settings dialog still accepts a typed
// model name, because a gateway may hide entries from the list or the list call
// may not be permitted at all. A failure here must therefore stay a soft error
// and never leave the dialog with no value.
func (c *LLMClient) Models(ctx context.Context) ([]string, error) {
	url := strings.TrimSuffix(c.endpoint, "/chat/completions") + "/models"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating models request: %w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	slog.Debug("listing LLM models", "url", url)
	start := time.Now()
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending models request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := readBoundedBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading models response: %w", err)
	}

	// Recorded like a chat call so a failing models endpoint is attributable in
	// GET /api/metrics rather than only in a handler log line.
	if c.recordFn != nil {
		errStr := ""
		if resp.StatusCode != http.StatusOK {
			errStr = fmt.Sprintf("status %d", resp.StatusCode)
		}
		c.recordFn(start, url, resp.StatusCode, time.Since(start).Milliseconds(), int64(len(respBody)), errStr)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model list returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &list); err != nil {
		return nil, fmt.Errorf("unmarshaling model list: %w", err)
	}

	names := make([]string, 0, len(list.Data))
	seen := make(map[string]bool, len(list.Data))
	for _, m := range list.Data {
		id := strings.TrimSpace(m.ID)
		// A duplicate would render as two identical menu rows.
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		names = append(names, id)
	}
	sort.Strings(names)
	return names, nil
}

// HasAPIKey reports whether an API key is configured. Digest generation logs
// an error when the LLM is needed but no key is set.
func (c *LLMClient) HasAPIKey() bool { return c.apiKey != "" }

// Model returns the model name this client sends.
func (c *LLMClient) Model() string { return c.model }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *LLMClient) Chat(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	messages := []chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	body := chatRequest{
		Model:    c.model,
		Messages: messages,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	slog.Debug("calling LLM", "endpoint", c.endpoint, "model", c.model, "input_bytes", len(systemPrompt)+len(userPrompt))
	start := time.Now()
	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := readBoundedBody(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	slog.Debug("LLM response received", "model", c.model, "output_bytes", len(respBody), "status", resp.StatusCode, "duration", time.Since(start))

	// Record LLM API call metrics.
	if c.recordFn != nil {
		errStr := ""
		if resp.StatusCode != http.StatusOK {
			errStr = fmt.Sprintf("status %d", resp.StatusCode)
		}
		c.recordFn(start, c.endpoint, resp.StatusCode, time.Since(start).Milliseconds(), int64(len(respBody)), errStr)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("LLM API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var cr chatResponse
	if err := json.Unmarshal(respBody, &cr); err != nil {
		return "", fmt.Errorf("unmarshaling response: %w", err)
	}

	if cr.Error != nil {
		return "", fmt.Errorf("LLM API error: %s", cr.Error.Message)
	}

	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("LLM returned no choices")
	}

	return cr.Choices[0].Message.Content, nil
}
