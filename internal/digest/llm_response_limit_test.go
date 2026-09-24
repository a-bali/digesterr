package digest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An oversized LLM response must fail cleanly instead of being read into memory
// without bound. Every other fetcher in this repo limits its read; these two
// call sites were the outlier.
func TestLLMCallRejectsOversizedResponse(t *testing.T) {
	huge := strings.Repeat("x", llmResponseLimit+1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(huge))
	}))
	t.Cleanup(srv.Close)

	c := NewLLMClient(srv.URL, "", "test-model")
	_, err := c.Chat(t.Context(), "system", "user")
	if err == nil {
		t.Fatal("an over-limit response was accepted")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error = %q, want it to name the size limit rather than a JSON syntax error", err)
	}
}

// The models endpoint has the same bound.
func TestLLMModelsRejectsOversizedResponse(t *testing.T) {
	huge := strings.Repeat("x", llmResponseLimit+1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(huge))
	}))
	t.Cleanup(srv.Close)

	c := NewLLMClient(srv.URL, "", "test-model")
	if _, err := c.Models(t.Context()); err == nil {
		t.Fatal("an over-limit models response was accepted")
	}
}

// A normal-sized response must still work: the bound must not break the happy
// path.
func TestLLMCallAcceptsNormalResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hello"}}]}`))
	}))
	t.Cleanup(srv.Close)

	c := NewLLMClient(srv.URL, "", "test-model")
	got, err := c.Chat(t.Context(), "system", "user")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got != "hello" {
		t.Errorf("content = %q, want %q", got, "hello")
	}
}

// readBoundedBody must not reject a response that exactly fills the limit; the
// off-by-one is what distinguishes "at the cap" from "over the cap".
func TestReadBoundedBodyBoundary(t *testing.T) {
	at := strings.NewReader(strings.Repeat("x", llmResponseLimit))
	body, err := readBoundedBody(at)
	if err != nil {
		t.Fatalf("a response exactly at the limit was rejected: %v", err)
	}
	if len(body) != llmResponseLimit {
		t.Errorf("read %d bytes, want %d", len(body), llmResponseLimit)
	}

	over := strings.NewReader(strings.Repeat("x", llmResponseLimit+1))
	if _, err := readBoundedBody(over); err == nil {
		t.Error("a response one byte over the limit was accepted")
	}
}

// countingReader yields an endless stream of bytes and records how many were
// pulled, so a test can prove the read STOPPED early rather than being drained
// and then rejected.
//
// This is the only way to observe the memory bound: an over-limit body read with
// ReadAll still produces the same error message, so an error-based assertion
// cannot tell a bounded read from an unbounded one. Only the byte count can.
type countingReader struct {
	read  int64
	limit int64 // stop feeding past this, so an unbounded read cannot hang the test
}

func (c *countingReader) Read(p []byte) (int, error) {
	if c.read >= c.limit {
		// EOF, not an error: the cap models a finite source, and a hard error
		// here would be reported as a read failure rather than a size verdict.
		return 0, io.EOF
	}
	n := len(p)
	if c.read+int64(n) > c.limit {
		n = int(c.limit - c.read)
	}
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	c.read += int64(n)
	return n, nil
}

// The read must stop at the limit instead of buffering the whole response. A
// response is attacker- or bug-controlled input, so "read it all and then check
// the size" does not bound memory at all: the allocation has already happened.
func TestReadBoundedBodyStopsReadingAtTheLimit(t *testing.T) {
	// Ten times the limit is available, so an unbounded read would consume all
	// of it before any size check could fire.
	r := &countingReader{limit: llmResponseLimit * 10}
	if _, err := readBoundedBody(r); err == nil {
		t.Fatal("an over-limit body was accepted")
	}
	if r.read > llmResponseLimit+1 {
		t.Errorf("read %d bytes from the source, want at most %d: the read was not bounded, so peak memory is not bounded either",
			r.read, llmResponseLimit+1)
	}
}

// A body within the limit must be read to completion, not truncated at the
// probe byte.
func TestReadBoundedBodyReadsWithinLimitFully(t *testing.T) {
	want := int64(1024)
	r := &countingReader{limit: want}
	body, err := readBoundedBody(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if int64(len(body)) != want {
		t.Errorf("read %d bytes, want %d", len(body), want)
	}
	if r.read != want {
		t.Errorf("source consumed %d bytes, want %d", r.read, want)
	}
}
