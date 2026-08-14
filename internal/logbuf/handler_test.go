package logbuf

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestHandlerCapturesAndTypes(t *testing.T) {
	buf := New(10)
	var out bytes.Buffer
	inner := slog.NewTextHandler(&out, nil)
	opts := &slog.HandlerOptions{AddSource: true}
	h := NewHandler(inner, buf, opts)
	logger := slog.New(h)

	logger.Info("hello", "feed_id", 7)

	entries := buf.List(0)
	if len(entries) != 1 {
		t.Fatalf("expected 1 captured entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Message != "hello" {
		t.Fatalf("message = %q, want hello", e.Message)
	}
	if e.Level != "INFO" {
		t.Fatalf("level = %q, want INFO", e.Level)
	}
	if e.Type == "" {
		t.Fatal("expected a non-empty type")
	}
	if e.Attrs["feed_id"] != "7" {
		t.Fatalf("attrs = %v, want feed_id=7", e.Attrs)
	}
	// The underlying handler still got the record.
	if !strings.Contains(out.String(), "hello") {
		t.Fatalf("inner handler did not receive record, got %q", out.String())
	}
}

func TestHandlerCapturesWithAttrs(t *testing.T) {
	buf := New(10)
	var out bytes.Buffer
	inner := slog.NewTextHandler(&out, nil)
	opts := &slog.HandlerOptions{AddSource: true}
	h := NewHandler(inner, buf, opts)
	logger := slog.New(h).With("feed_id", int64(7), "feed_name", "Example")

	// Handler-level attrs must be captured; a record-level attr with the same
	// key overrides it, mirroring slog output.
	logger.Info("polled feeds", "feed_name", "Renamed")

	entries := buf.List(0)
	if len(entries) != 1 {
		t.Fatalf("expected 1 captured entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Attrs["feed_id"] != "7" {
		t.Fatalf("handler attr feed_id = %q, want 7", e.Attrs["feed_id"])
	}
	if e.Attrs["feed_name"] != "Renamed" {
		t.Fatalf("feed_name = %q, want Renamed (record attr wins)", e.Attrs["feed_name"])
	}
}

func TestClassifyPath(t *testing.T) {
	cases := []struct {
		file string
		want string
	}{
		{"/opt/data/workspace/digesterr/internal/httpapi/server.go", "http"},
		{"/opt/data/workspace/digesterr/internal/digest/generator.go", "digest"},
		{"/opt/data/workspace/digesterr/internal/feed/poller.go", "feed"},
		{"/opt/data/workspace/digesterr/internal/auth/service.go", "auth"},
		{"/opt/data/workspace/digesterr/cmd/digesterr/main.go", "app"},
		{"/opt/data/workspace/digesterr/internal/logbuf/buffer.go", "logbuf"},
		{"/opt/data/workspace/other/thing.go", "other"},
	}
	for _, c := range cases {
		if got := ClassifyPath(c.file); got != c.want {
			t.Errorf("ClassifyPath(%q) = %q, want %q", c.file, got, c.want)
		}
	}
}
