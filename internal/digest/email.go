package digest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// emailSender sends an email with plain-text and HTML bodies. It is the
// seam the Generator uses so delivery can be faked in tests.
type emailSender interface {
	Send(ctx context.Context, to, subject, textBody, htmlBody string) error
}

// DefaultAgentMailBaseURL is the production AgentMail API endpoint.
const DefaultAgentMailBaseURL = "https://api.agentmail.to"

// AgentMailSender sends email through the agentmail.to HTTP API without any
// SDK dependency. Create one per process with NewAgentMailSender; it is safe
// for concurrent use.
type AgentMailSender struct {
	// BaseURL is the API root. Empty means DefaultAgentMailBaseURL.
	BaseURL string
	// Token is the AgentMail API key used as the Bearer credential.
	Token string
	// InboxID is the inbox emails are sent from (e.g. user@agentmail.to).
	InboxID string
	// HTTPClient is optional; nil uses a client with a 30s timeout.
	HTTPClient *http.Client
}

// NewAgentMailSender returns a sender for the given API credentials using the
// default production endpoint.
func NewAgentMailSender(token, inboxID string) *AgentMailSender {
	return &AgentMailSender{Token: token, InboxID: inboxID}
}

type agentMailSendRequest struct {
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
	HTML    string   `json:"html"`
}

// Send posts the message to POST {BaseURL}/v0/inboxes/{inbox_id}/messages/send.
// A non-2xx response yields an error that includes a snippet of the response
// body for diagnostics.
func (s *AgentMailSender) Send(ctx context.Context, to, subject, textBody, htmlBody string) error {
	if s.Token == "" {
		return fmt.Errorf("agentmail: missing API token")
	}
	if s.InboxID == "" {
		return fmt.Errorf("agentmail: missing inbox id")
	}
	if to == "" {
		return fmt.Errorf("agentmail: missing recipient")
	}

	base := s.BaseURL
	if base == "" {
		base = DefaultAgentMailBaseURL
	}
	endpoint := strings.TrimRight(base, "/") + "/v0/inboxes/" + url.PathEscape(s.InboxID) + "/messages/send"

	payload, err := json.Marshal(agentMailSendRequest{To: []string{to}, Subject: subject, Text: textBody, HTML: htmlBody})
	if err != nil {
		return fmt.Errorf("agentmail: encoding request: %w", err)
	}

	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("agentmail: building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("agentmail: sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet := readSnippet(resp.Body)
		return fmt.Errorf("agentmail: unexpected status %d: %s", resp.StatusCode, snippet)
	}
	// Drain the body so the connection can be reused.
	io.Copy(io.Discard, resp.Body)
	return nil
}

// readSnippet reads up to 200 bytes of the body for inclusion in an error.
func readSnippet(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 200))
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "(empty response body)"
	}
	return s
}
