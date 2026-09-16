package digest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// LLMRecordFunc records an outbound LLM API call.
type LLMRecordFunc func(timestamp time.Time, url string, statusCode int, durationMs int64, bytes int64, err string)

type LLMClient struct {
	endpoint string
	apiKey   string
	model    string
	client   *http.Client
	recordFn LLMRecordFunc
}

// llmChatter is the subset of the LLM client used by digest generation,
// declared as an interface so generation can be tested with a fake.
type llmChatter interface {
	Chat(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

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

// HasAPIKey reports whether an API key is configured. Digest generation logs
// an error when the LLM is needed but no key is set.
func (c *LLMClient) HasAPIKey() bool { return c.apiKey != "" }

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

	respBody, err := io.ReadAll(resp.Body)
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
