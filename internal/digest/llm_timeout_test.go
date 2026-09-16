package digest

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A single client call can fan out into several sequential upstream attempts
// when the endpoint is a gateway that fails over. The client budget has to be
// wide enough to outlive that failover, so the default must be well above the
// 120s that used to lose production digests mid-sweep.
func TestNewLLMClientTimeout(t *testing.T) {
	tests := []struct {
		name    string
		timeout []time.Duration
		want    time.Duration
	}{
		{name: "no argument uses the default", want: LLMTimeoutDefault},
		{name: "zero uses the default", timeout: []time.Duration{0}, want: LLMTimeoutDefault},
		{name: "negative uses the default", timeout: []time.Duration{-time.Second}, want: LLMTimeoutDefault},
		{name: "explicit value wins", timeout: []time.Duration{300 * time.Second}, want: 300 * time.Second},
		{name: "short value honored", timeout: []time.Duration{45 * time.Second}, want: 45 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := NewLLMClient("http://llm.local/v1", "k", "m", tc.timeout...)
			if c.client.Timeout != tc.want {
				t.Errorf("client timeout = %v, want %v", c.client.Timeout, tc.want)
			}
		})
	}
}

// The default is 300s: the gateway this usually points at applies a 90s
// per-attempt budget and may sweep several providers in one call, and five
// minutes covers the deepest chain configured there with headroom.
func TestLLMTimeoutDefaultExceedsSingleAttemptBudget(t *testing.T) {
	if LLMTimeoutDefault != 300*time.Second {
		t.Errorf("LLMTimeoutDefault = %v, want 300s", LLMTimeoutDefault)
	}
	// Guard against a regression to the value that lost the digest: 120s was
	// consumed entirely by three sequential attempts (8s + 90s + 21s).
	if LLMTimeoutDefault <= 120*time.Second {
		t.Errorf("LLMTimeoutDefault = %v, must exceed the old 120s", LLMTimeoutDefault)
	}
}

// The endpoint is normalized once, so the timeout path and the URL path cannot
// drift apart.
func TestNewLLMClientEndpointNormalization(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"http://llm.local/v1", "http://llm.local/v1/chat/completions"},
		{"http://llm.local/v1/", "http://llm.local/v1/chat/completions"},
		{"http://llm.local/v1/chat/completions", "http://llm.local/v1/chat/completions"},
	}
	for _, tc := range tests {
		c := NewLLMClient(tc.in, "k", "m", 90*time.Second)
		if c.endpoint != tc.want {
			t.Errorf("endpoint for %q = %q, want %q", tc.in, c.endpoint, tc.want)
		}
		if c.client.Timeout != 90*time.Second {
			t.Errorf("explicit timeout ignored for %q: %v", tc.in, c.client.Timeout)
		}
	}
}

// The timeout must actually bound the request rather than only being stored: a
// server that never answers has to surface an error instead of hanging.
func TestLLMChatHonorsTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"late"}}]}`))
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	start := time.Now()
	_, err := NewLLMClient(srv.URL, "k", "m", 100*time.Millisecond).Chat(t.Context(), "sys", "user")
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	// The call must return because of the client timeout, not because the
	// handler eventually answered.
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Chat took %v; the 100ms timeout was not applied", elapsed)
	}
}
