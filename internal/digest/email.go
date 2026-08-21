package digest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// emailSender is the subset of the agentmail client used by digest
// generation. It is an interface so tests can inject a fake.
type emailSender interface {
	Send(ctx context.Context, to, subject, textBody, htmlBody string) error
}

// AgentMailSender sends emails via the agentmail.to HTTP API using raw
// net/http requests (no SDK dependency).
type AgentMailSender struct {
	Token   string
	InboxID string
	// BaseURL defaults to https://api.agentmail.to; overridable in tests.
	BaseURL string
	Client  *http.Client
}

// NewAgentMailSender returns a sender, or nil when token or inbox ID are
// empty (feature disabled).
func NewAgentMailSender(token, inboxID string) *AgentMailSender {
	if token == "" || inboxID == "" {
		return nil
	}
	return &AgentMailSender{
		Token:   token,
		InboxID: inboxID,
		BaseURL: "https://api.agentmail.to",
		Client:  &http.Client{Timeout: 30 * time.Second},
	}
}

type agentMailSendRequest struct {
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text,omitempty"`
	HTML    string   `json:"html,omitempty"`
}

type agentMailSendResponse struct {
	MessageID string `json:"message_id"`
	ThreadID  string `json:"thread_id"`
}

// Send delivers a message to the given address through agentmail.
func (s *AgentMailSender) Send(ctx context.Context, to, subject, textBody, htmlBody string) error {
	body, err := json.Marshal(agentMailSendRequest{
		To:      []string{to},
		Subject: subject,
		Text:    textBody,
		HTML:    htmlBody,
	})
	if err != nil {
		return fmt.Errorf("encoding agentmail request: %w", err)
	}

	url := strings.TrimRight(s.BaseURL, "/") + "/v0/inboxes/" + s.InboxID + "/messages/send"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building agentmail request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.Client.Do(req)
	if err != nil {
		return fmt.Errorf("agentmail request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("agentmail send failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	var out agentMailSendResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&out); err != nil {
		// A 2xx without parseable JSON is unusual but not fatal to delivery.
		return nil
	}
	if out.MessageID == "" {
		return fmt.Errorf("agentmail response missing message_id")
	}
	return nil
}
