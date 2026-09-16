package feed

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"digesterr/internal/metrics"
)

// listFixture is a real AgentMail list response (captured 2026-09-15), with the
// metadata-only shape preserved: note the absence of any text/html field, which
// is what the client must not expect.
const listFixture = `{
  "count": 2,
  "next_page_token": "eyJtZXRhZGF0YSI6e30sIm9mZnNldCI6Mn0=",
  "messages": [
    {
      "organization_id": "4b4791e7",
      "pod_id": "4b4791e7",
      "inbox_id": "hermes-snowchar@agentmail.to",
      "thread_id": "2b52986b-588b-4d08-86d0-db9ea7355099",
      "message_id": "<6aa99520.a47bfe04.370391.fce7@mx.google.com>",
      "labels": ["received", "unread"],
      "timestamp": "2026-09-15T18:57:36.000Z",
      "from": "andras.bali@gmail.com",
      "to": ["hermes-snowchar+newslettertest@agentmail.to"],
      "subject": "hermes inbound routing probe",
      "preview": "probe A: plus-addressing on agentmail.to",
      "headers": {"Authentication-Results": "amazonses.com; spf=pass"},
      "size": 5210,
      "created_at": "2026-09-15T18:57:38.528Z",
      "updated_at": "2026-09-15T18:57:38.528Z"
    },
    {
      "inbox_id": "hermes-snowchar@agentmail.to",
      "message_id": "<0100019fc77ebeb2-ec5f5256-15a8@agentmail.to>",
      "labels": ["received"],
      "timestamp": "2026-09-15T18:51:56.928Z",
      "from": "AgentMail <hermes-snowchar@agentmail.to>",
      "to": ["hermes-probe-does-this-bounce@xba.li"],
      "subject": "Inbound test for Hermes",
      "preview": "Testing",
      "size": 900,
      "headers": {}
    }
  ]
}`

// getFixture is a real single-message response WITH a body. The HTML here is
// the real payload, including the style attributes and branded footer link that
// the sanitizer has to deal with.
const getFixture = `{
  "inbox_id": "hermes-snowchar@agentmail.to",
  "message_id": "<0100019fc77ebeb2-ec5f5256-15a8@agentmail.to>",
  "thread_id": "thd_1",
  "labels": ["received"],
  "timestamp": "2026-09-15T18:51:56.928Z",
  "from": "AgentMail <hermes-snowchar@agentmail.to>",
  "to": ["hermes-probe-does-this-bounce@xba.li"],
  "subject": "Inbound test for Hermes",
  "preview": "Testing",
  "size": 900,
  "headers": {"Authentication-Results": "amazonses.com; spf=pass"},
  "text": "Testing whether an email can be received.",
  "extracted_text": "Testing whether an email can be received.",
  "html": "<div dir=\"ltr\">Testing whether an email can be received.</div><div style=\"margin-top:16px\">Sent via <a href=\"https://agentmail.to?utm_source=agentmail\" style=\"color:#999;\">AgentMail</a></div>"
}`

// textOnlyFixture is the shape a plain-text message takes: html is explicitly
// null. The client must not treat that as an error.
const textOnlyFixture = `{
  "message_id": "<text-only@x>",
  "timestamp": "2026-09-15T18:57:36.000Z",
  "from": "someone@example.com",
  "to": ["hermes-snowchar+sport@agentmail.to"],
  "subject": "text only",
  "text": "just text",
  "html": null
}`

func newTestClient(t *testing.T, handler http.HandlerFunc) *AgentMailClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewAgentMailClient("test-key", "hermes-snowchar@agentmail.to")
	if c == nil {
		t.Fatal("NewAgentMailClient returned nil for a configured key+inbox")
	}
	c.baseURL = srv.URL
	return c
}

func TestNewAgentMailClientUnconfigured(t *testing.T) {
	if c := NewAgentMailClient("", "x@y.z"); c != nil {
		t.Error("empty key should yield a nil client")
	}
	if c := NewAgentMailClient("key", ""); c != nil {
		t.Error("empty inbox should yield a nil client")
	}
	if c := NewAgentMailClient("key", "x@y.z"); c == nil {
		t.Error("key+inbox should yield a client")
	}
}

func TestListMessagesParsesMetadata(t *testing.T) {
	var calls int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", got)
		}
		if !strings.HasSuffix(r.URL.Path, "/messages") {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		// The fixture advertises a next_page_token, so the real API's second
		// page is served here too rather than hanging the client.
		if r.URL.Query().Get("page_token") == "" {
			fmt.Fprint(w, listFixture)
			return
		}
		fmt.Fprint(w, `{"count":0,"next_page_token":"","messages":[]}`)
	})

	refs, err := c.ListMessages(context.Background(), "", 50)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if calls != 2 {
		t.Errorf("made %d requests, want 2 (the fixture advertises a second page)", calls)
	}
	if len(refs) != 2 {
		t.Fatalf("got %d refs, want 2", len(refs))
	}
	first := refs[0]
	if first.MessageID != "<6aa99520.a47bfe04.370391.fce7@mx.google.com>" {
		t.Errorf("message_id = %q", first.MessageID)
	}
	if first.From != "andras.bali@gmail.com" {
		t.Errorf("from = %q, want the JSON 'from' key parsed (not from_)", first.From)
	}
	if len(first.To) != 1 || first.To[0] != "hermes-snowchar+newslettertest@agentmail.to" {
		t.Errorf("to = %v", first.To)
	}
	if len(first.Labels) != 2 || first.Labels[0] != "received" {
		t.Errorf("labels = %v", first.Labels)
	}
	if first.Size != 5210 {
		t.Errorf("size = %d, want 5210", first.Size)
	}
	if first.Timestamp != "2026-09-15T18:57:36.000Z" {
		t.Errorf("timestamp = %q", first.Timestamp)
	}
	if first.Headers["Authentication-Results"] == "" {
		t.Error("headers not parsed")
	}
}

// The client must follow next_page_token; stopping at the first page would drop
// mail whenever the mailbox holds more than one page.
func TestListMessagesFollowsPagination(t *testing.T) {
	var calls int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page_token") {
		case "":
			fmt.Fprint(w, `{"count":1,"next_page_token":"tok2","messages":[{"message_id":"<p1@x>","timestamp":"2026-09-15T10:00:00.000Z","to":["a@b.c"]}]}`)
		case "tok2":
			fmt.Fprint(w, `{"count":1,"next_page_token":"","messages":[{"message_id":"<p2@x>","timestamp":"2026-09-15T11:00:00.000Z","to":["a@b.c"]}]}`)
		default:
			t.Errorf("unexpected page_token %q", r.URL.Query().Get("page_token"))
		}
	})

	refs, err := c.ListMessages(context.Background(), "", 50)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if calls != 2 {
		t.Errorf("made %d requests, want 2 (one per page)", calls)
	}
	if len(refs) != 2 || refs[1].MessageID != "<p2@x>" {
		t.Fatalf("pagination result = %v", refs)
	}
}

// A repeated next_page_token must terminate the loop rather than hang the poll
// cycle. The first version of this client spun here until its row-count guard
// fired, which this test now pins.
func TestListMessagesRepeatedTokenTerminates(t *testing.T) {
	var calls int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		// Always the same token, as a misbehaving server would.
		fmt.Fprint(w, `{"count":1,"next_page_token":"same-token","messages":[{"message_id":"<x@y>","timestamp":"2026-09-15T10:00:00.000Z"}]}`)
	})
	done := make(chan struct{})
	var refs []AgentMailMessageRef
	var err error
	go func() {
		refs, err = c.ListMessages(context.Background(), "", 50)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ListMessages did not terminate on a repeated page token")
	}
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if calls != 2 {
		t.Errorf("made %d requests, want 2 (second token is a repeat, so stop)", calls)
	}
	if len(refs) != 2 {
		t.Errorf("got %d refs, want the 2 rows served before the repeat", len(refs))
	}
}

// A terminal page advertising an empty next_page_token must stop the loop.
func TestListMessagesStopsOnEmptyToken(t *testing.T) {
	var calls int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"count":0,"next_page_token":"","messages":[]}`)
	})
	if _, err := c.ListMessages(context.Background(), "", 50); err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if calls != 1 {
		t.Errorf("made %d requests, want 1", calls)
	}
}

// The after parameter must be forwarded as sent — the API takes an RFC3339
// string, and reformatting it here would break the window.
func TestListMessagesSendsAfterParam(t *testing.T) {
	var gotAfter string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAfter = r.URL.Query().Get("after")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"count":0,"messages":[]}`)
	})
	if _, err := c.ListMessages(context.Background(), "2026-09-15T12:00:00.000Z", 50); err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if gotAfter != "2026-09-15T12:00:00.000Z" {
		t.Errorf("after = %q, want it forwarded verbatim", gotAfter)
	}
}

func TestGetMessageReadsBody(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, getFixture)
	})

	msg, err := c.GetMessage(context.Background(), "<0100019fc77ebeb2-ec5f5256-15a8@agentmail.to>")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if msg.BodyText() != "Testing whether an email can be received." {
		t.Errorf("body text = %q", msg.BodyText())
	}
	html := msg.RawHTML()
	if !strings.Contains(html, "Testing whether an email can be received.") {
		t.Errorf("html missing content: %q", html)
	}
	if !strings.Contains(html, "agentmail.to") {
		t.Error("html footer link not parsed")
	}
	if msg.Subject != "Inbound test for Hermes" {
		t.Errorf("subject = %q", msg.Subject)
	}
	if len(msg.To) != 1 || msg.To[0] != "hermes-probe-does-this-bounce@xba.li" {
		t.Errorf("to = %v", msg.To)
	}
}

// A null html field must read as "no HTML", not crash and not be confused with
// an empty-but-present part.
func TestGetMessageNullHTML(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, textOnlyFixture)
	})
	msg, err := c.GetMessage(context.Background(), "<text-only@x>")
	if err != nil {
		t.Fatalf("GetMessage with null html: %v", err)
	}
	if got := msg.RawHTML(); got != "" {
		t.Errorf("RawHTML() = %q, want empty for a null html part", got)
	}
	if msg.BodyText() != "just text" {
		t.Errorf("BodyText() = %q, want the plain text body", msg.BodyText())
	}
}

// A 404 must carry its status so metrics record 4xx, not 500: hardcoding the
// status is what hid the Twitter 429s from the metrics dialog.
func TestGetMessageStatusError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"name":"NotFoundError","message":"Inbox not found"}`)
	})
	_, err := c.GetMessage(context.Background(), "<missing@x>")
	if err == nil {
		t.Fatal("expected an error for 404")
	}
	if got := metrics.StatusFor(err); got != http.StatusNotFound {
		t.Errorf("StatusFor(err) = %d, want 404 (not 500)", got)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error text should mention the status: %v", err)
	}
}

func TestListMessagesRateLimitStatus(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"message":"rate limited"}`)
	})
	_, err := c.ListMessages(context.Background(), "", 50)
	if err == nil {
		t.Fatal("expected an error for 429")
	}
	if got := metrics.StatusFor(err); got != http.StatusTooManyRequests {
		t.Errorf("StatusFor(err) = %d, want 429", got)
	}
}

// The inbox must be path-escaped: it is an address, and '@' plus a '+' must not
// be reinterpreted by the transport.
func TestClientEscapesInboxInPath(t *testing.T) {
	var gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"count":0,"messages":[]}`)
	})
	if _, err := c.ListMessages(context.Background(), "", 5); err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if !strings.Contains(gotPath, "/inboxes/") {
		t.Fatalf("path = %q", gotPath)
	}
	if strings.Contains(gotPath, " ") {
		t.Errorf("path contains an unescaped space: %q", gotPath)
	}
}

// setTransport is what Poller.SetTransport type-asserts; without it the client's
// requests never reach the metrics transport.
func TestClientImplementsTransportSetter(t *testing.T) {
	c := NewAgentMailClient("k", "i@j.k")
	if _, ok := any(c).(interface{ setTransport(http.RoundTripper) }); !ok {
		t.Fatal("AgentMailClient must implement setTransport for metrics wiring")
	}
}
