package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"digesterr/internal/metrics"
)

// agentMailAPIBase is a constant, not a config value. The AgentMail API host is
// not something an operator should retarget, so tests point the client at an
// httptest server by assigning the unexported baseURL field — the same
// package-internal approach RedditFetcher.baseURL and TwitterFetcher.baseURL
// use. Do not turn this into a config option that exists only for tests.
const agentMailAPIBase = "https://api.agentmail.to/v0"

// agentMailTimeout bounds a single API request. The sync stage is best-effort
// and retried next cycle, so a slow request should fail rather than stall the
// poll loop and every other feed behind it.
const agentMailTimeout = 30 * time.Second

// agentMailPageLimit is the requested page size. The API caps returned rows
// independently (a limit of 100 returned 9), so callers must paginate rather
// than assume one request drains the mailbox.
const agentMailPageLimit = 100

// AgentMailMessageRef is one item of a message LIST response.
//
// The list endpoint is metadata-only: it does not return bodies at all. There
// is deliberately no Text/HTML field here, so code that wants a body is forced
// through GetMessage instead of silently reading an empty string.
type AgentMailMessageRef struct {
	MessageID string            `json:"message_id"`
	ThreadID  string            `json:"thread_id"`
	From      string            `json:"from"` // JSON key is "from"; the Python SDK renames it from_
	To        []string          `json:"to"`
	Labels    []string          `json:"labels"`
	Subject   string            `json:"subject"`
	Preview   string            `json:"preview"`
	Size      int               `json:"size"`
	Timestamp string            `json:"timestamp"`
	Headers   map[string]string `json:"headers"`
}

// AgentMailMessage is a single-message response, which does carry the body.
//
// The body fields are pointers because a message without an HTML part returns
// "html": null (verified live: a text-only message came back with html absent).
// Value types would turn that into an empty-string ambiguity indistinguishable
// from a genuinely empty body.
type AgentMailMessage struct {
	AgentMailMessageRef
	Text          *string `json:"text"`
	HTML          *string `json:"html"`
	ExtractedText *string `json:"extracted_text"`
}

// BodyText returns the plain-text body, preferring the API's reply-stripped
// extraction when present.
func (m *AgentMailMessage) BodyText() string {
	if m.ExtractedText != nil && *m.ExtractedText != "" {
		return *m.ExtractedText
	}
	if m.Text != nil {
		return *m.Text
	}
	return ""
}

// RawHTML returns the HTML body, or "" when the message has none.
func (m *AgentMailMessage) RawHTML() string {
	if m.HTML == nil {
		return ""
	}
	return *m.HTML
}

type agentMailListResponse struct {
	Count         int                   `json:"count"`
	NextPageToken string                `json:"next_page_token"`
	Messages      []AgentMailMessageRef `json:"messages"`
}

// AgentMailClient reads messages from one AgentMail inbox.
//
// It performs no retries: the sync stage runs every poll cycle, so a transient
// failure is retried by the next cycle with the same window (the watermark does
// not advance on failure). Retrying here would only extend the cycle.
type AgentMailClient struct {
	baseURL string
	apiKey  string
	inboxID string
	client  *http.Client
}

// NewAgentMailClient returns a client for the given inbox. An empty key or
// inbox returns nil: the caller treats that as "feed type not configured"
// rather than as an error to surface.
func NewAgentMailClient(apiKey, inboxID string) *AgentMailClient {
	if apiKey == "" || inboxID == "" {
		return nil
	}
	return &AgentMailClient{
		baseURL: agentMailAPIBase,
		apiKey:  apiKey,
		inboxID: inboxID,
		client:  &http.Client{Timeout: agentMailTimeout},
	}
}

// InboxID returns the mailbox this client reads.
func (c *AgentMailClient) InboxID() string { return c.inboxID }

// BaseURL returns the API base, used for the sync's metrics record.
func (c *AgentMailClient) BaseURL() string { return c.baseURL }

// Host returns the API host, so the sync's traffic is attributable in
// GET /api/metrics the same way a feed's is.
func (c *AgentMailClient) Host() string {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return ""
	}
	return u.Host
}

// consumeCounters reads and resets the shared metrics transport counters. The
// sync uses this so its traffic is reported as its own instead of being left
// for the next feed's ConsumeCounters to claim.
func (c *AgentMailClient) consumeCounters() (int64, int) {
	if t, ok := c.client.Transport.(*metrics.Transport); ok {
		b, r := t.Consume()
		return b, int(r)
	}
	return 0, 0
}

// setTransport implements the transportSetter interface Poller.SetTransport
// type-asserts, so the shared metrics transport reaches this client. Without it
// the email feed's requests are invisible in GET /api/metrics — the same blind
// spot that hid the Twitter 429s until the transport counter landed.
func (c *AgentMailClient) setTransport(t http.RoundTripper) { c.client.Transport = t }

// ListMessages returns messages received after the given RFC3339 timestamp,
// newest first. An empty `after` means no lower bound.
//
// It follows next_page_token to completion, because one page can be smaller
// than the mailbox and stopping early would silently drop mail.
func (c *AgentMailClient) ListMessages(ctx context.Context, after string, limit int) ([]AgentMailMessageRef, error) {
	if limit <= 0 {
		limit = agentMailPageLimit
	}
	var (
		out        []AgentMailMessageRef
		pageToken  string
		seenTokens = map[string]bool{}
	)
	for {
		q := url.Values{}
		q.Set("limit", strconv.Itoa(limit))
		if after != "" {
			q.Set("after", after)
		}
		if pageToken != "" {
			q.Set("page_token", pageToken)
		}

		var page agentMailListResponse
		if err := c.get(ctx, "/inboxes/"+url.PathEscape(c.inboxID)+"/messages?"+q.Encode(), &page); err != nil {
			return nil, err
		}
		out = append(out, page.Messages...)

		if page.NextPageToken == "" || len(page.Messages) == 0 {
			return out, nil
		}
		// A token that repeats would loop forever inside the poll cycle, so
		// track what has been seen rather than trusting the server to
		// terminate. This is not hypothetical: a server that echoes the
		// request's token back is exactly how the first version of this loop
		// hung.
		if seenTokens[page.NextPageToken] {
			return out, nil
		}
		seenTokens[page.NextPageToken] = true
		pageToken = page.NextPageToken
	}
}

// GetMessage fetches one message including its body.
//
// Bodies only ever come from here. The list endpoint omits them, and
// batch-get — which looks like the efficient choice — returns 200 with text and
// html ABSENT for the same messages this returns fully (verified over raw HTTP,
// twice, so it is server-side and not an SDK artifact). Do not "optimise" this
// into batch-get; it silently produces empty newsletters.
func (c *AgentMailClient) GetMessage(ctx context.Context, messageID string) (*AgentMailMessage, error) {
	var msg AgentMailMessage
	path := "/inboxes/" + url.PathEscape(c.inboxID) + "/messages/" + url.PathEscape(messageID)
	if err := c.get(ctx, path, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// VerifyInbox checks that this key can actually see the configured mailbox.
//
// It exists because a wrong inbox or a wrong token is otherwise INVISIBLE: the
// settings save succeeds, every poll returns nothing, and the only symptom is a
// permanently empty feed. One request at save time converts that into a 400 with
// the API's own message. AgentMail has no catch-all, so a typo is not a partial
// failure — it receives no mail at all.
//
// The returned error is a metrics.StatusError carrying the real status code, so
// a rejected credential is not recorded as a 500 by callers that log it.
func (c *AgentMailClient) VerifyInbox(ctx context.Context) error {
	var out struct {
		InboxID string `json:"inbox_id"`
	}
	return c.get(ctx, "/inboxes/"+url.PathEscape(c.inboxID), &out)
}

// get performs a GET and decodes the JSON body, mapping failures to a
// metrics.StatusError so the poller records the real status code. Hardcoding
// 500 here is what made the Twitter 429s invisible in metrics for months.
func (c *AgentMailClient) get(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("building agentmail request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("agentmail request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read a bounded amount of the body: the API returns a JSON error
		// object and its text is the most useful thing to log.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return metrics.StatusErrorf(resp.StatusCode, "agentmail %s returned status %d: %s",
			path, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(dst); err != nil {
		return fmt.Errorf("decoding agentmail response: %w", err)
	}
	return nil
}
