package digest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewAgentMailSenderDisabled(t *testing.T) {
	if NewAgentMailSender("", "inbox") != nil {
		t.Error("expected nil sender without token")
	}
	if NewAgentMailSender("token", "") != nil {
		t.Error("expected nil sender without inbox ID")
	}
	if NewAgentMailSender("token", "inbox") == nil {
		t.Error("expected non-nil sender with both credentials")
	}
}

func TestAgentMailSenderSend(t *testing.T) {
	var gotPath, gotAuth, gotTo, gotSubject, gotText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var req agentMailSendRequest
		json.NewDecoder(r.Body).Decode(&req)
		gotTo = strings.Join(req.To, ",")
		gotSubject = req.Subject
		gotText = req.Text
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"message_id":"m1","thread_id":"t1"}`))
	}))
	defer srv.Close()

	s := &AgentMailSender{Token: "tok", InboxID: "inb", BaseURL: srv.URL, Client: srv.Client()}
	if err := s.Send(context.Background(), "a@b.c", "Sub", "body text", "<p>body</p>"); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if gotPath != "/v0/inboxes/inb/messages/send" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotTo != "a@b.c" || gotSubject != "Sub" || gotText != "body text" {
		t.Errorf("to=%q subject=%q text=%q", gotTo, gotSubject, gotText)
	}
}

func TestAgentMailSenderHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bad token"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	s := &AgentMailSender{Token: "tok", InboxID: "inb", BaseURL: srv.URL, Client: srv.Client()}
	err := s.Send(context.Background(), "a@b.c", "s", "t", "")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 error, got %v", err)
	}
}

type fakeSender struct {
	calls int
	err   error
	last  struct{ to, subject, text, html string }
}

func (f *fakeSender) Send(_ context.Context, to, subject, textBody, htmlBody string) error {
	f.calls++
	f.last.to, f.last.subject, f.last.text, f.last.html = to, subject, textBody, htmlBody
	return f.err
}

func TestValidateConfigEmailFields(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"no email fields", `{"min_score":0}`, false},
		{"valid email", `{"send_email":true,"email_to":"a@b.c"}`, false},
		{"missing address", `{"send_email":true}`, true},
		{"invalid address", `{"send_email":true,"email_to":"not-an-email"}`, true},
		{"two ats", `{"send_email":true,"email_to":"a@@b.c"}`, true},
		{"email without send flag ignored", `{"email_to":"garbage"}`, false},
	}
	for _, tc := range cases {
		err := ValidateConfig(tc.raw)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestRenderDigestEmail(t *testing.T) {
	content := &digestResultContent{
		Title:   "Weekly tech",
		Summary: "A summary",
		Highlights: []digestItemRef{
			{Title: "H1 <script>", URL: "https://x/1", LLMSummary: "sum1"},
		},
		Items: []digestItemRef{
			{Title: "I1", URL: "https://x/2"},
		},
	}
	subject, text, htmlBody := renderDigestEmail(content, "https://digesterr.example.com")
	if subject != "Weekly tech" {
		t.Errorf("subject = %q", subject)
	}
	if !strings.Contains(text, "H1") || !strings.Contains(text, "https://x/1") || !strings.Contains(text, "I1") {
		t.Errorf("text body missing pieces: %q", text)
	}
	if strings.Contains(htmlBody, "<script>") {
		t.Errorf("HTML not escaped: %q", htmlBody)
	}
	if !strings.Contains(htmlBody, "&lt;script&gt;") {
		t.Errorf("escaped title missing: %q", htmlBody)
	}
	// The email HTML must reuse the shared web/Reader renderer (title/summary
	// markup, highlight cards, "All items" list).
	if !strings.Contains(htmlBody, `class="digest-title"`) || !strings.Contains(htmlBody, `class="digest-summary"`) || !strings.Contains(htmlBody, "<h4>All items</h4>") {
		t.Errorf("expected shared renderer markup, got %q", htmlBody)
	}
}
