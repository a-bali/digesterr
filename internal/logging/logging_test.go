package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
		ok   bool
	}{
		{"", slog.LevelInfo, true},
		{"info", slog.LevelInfo, true},
		{"INFO", slog.LevelInfo, true},
		{"  info  ", slog.LevelInfo, true},
		{"debug", slog.LevelDebug, true},
		{"DEBUG", slog.LevelDebug, true},
		{"warn", slog.LevelWarn, true},
		{"warning", slog.LevelWarn, true},
		{"error", slog.LevelError, true},
		{"trace", slog.LevelInfo, false},
		{"verbose", slog.LevelInfo, false},
	}
	for _, c := range cases {
		got, ok := ParseLevel(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("ParseLevel(%q) = (%v, %v), want (%v, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestClassifyPath(t *testing.T) {
	cases := []struct {
		file string
		want string
	}{
		{"/opt/data/workspace/digesterr/internal/httpapi/server.go", CategoryHTTP},
		{"/opt/data/workspace/digesterr/internal/digest/generator.go", CategoryDigest},
		{"/opt/data/workspace/digesterr/internal/feed/poller.go", CategoryFeed},
		{"/opt/data/workspace/digesterr/internal/auth/service.go", CategoryAuth},
		{"/opt/data/workspace/digesterr/cmd/digesterr/main.go", CategoryApp},
		{"/opt/data/workspace/digesterr/internal/logbuf/buffer.go", "logbuf"},
		{"/opt/data/workspace/other/thing.go", "other"},
	}
	for _, c := range cases {
		if got := ClassifyPath(c.file); got != c.want {
			t.Errorf("ClassifyPath(%q) = %q, want %q", c.file, got, c.want)
		}
	}
}

func TestCategoryForWithoutSource(t *testing.T) {
	if got := CategoryFor(0, nil); got != CategoryApp {
		t.Errorf("CategoryFor(0, nil) = %q, want %q", got, CategoryApp)
	}
	if got := CategoryFor(123, &slog.HandlerOptions{}); got != CategoryApp {
		t.Errorf("CategoryFor with AddSource off = %q, want %q", got, CategoryApp)
	}
}

// TestHandlerAddsCategory verifies that a record without an explicit category
// gets one derived from the caller's source path.
func TestHandlerAddsCategory(t *testing.T) {
	var out bytes.Buffer
	opts := &slog.HandlerOptions{AddSource: true}
	h := NewHandler(slog.NewTextHandler(&out, opts), opts)
	slog.New(h).Info("hello")

	line := out.String()
	if !strings.Contains(line, "category=logging") {
		t.Errorf("record missing derived category, got %q", line)
	}
	if !strings.Contains(line, "msg=hello") {
		t.Errorf("record not forwarded, got %q", line)
	}
}

// TestHandlerKeepsExplicitCategory verifies that an explicit category attr
// (e.g. category=item) wins over the source-derived default.
func TestHandlerKeepsExplicitCategory(t *testing.T) {
	var out bytes.Buffer
	opts := &slog.HandlerOptions{AddSource: true}
	h := NewHandler(slog.NewTextHandler(&out, opts), opts)

	logger := slog.New(h).With(CategoryAttr(CategoryItem))
	logger.Info("item saved")

	line := out.String()
	if !strings.Contains(line, "category=item") {
		t.Errorf("explicit category lost, got %q", line)
	}
	if strings.Contains(line, "category=logging") {
		t.Errorf("explicit category overridden by source-derived one, got %q", line)
	}
}

// TestHandlerLevelFiltering verifies that level thresholds are enforced
// through the category handler (debug/info/warn semantics).
func TestHandlerLevelFiltering(t *testing.T) {
	var out bytes.Buffer
	opts := &slog.HandlerOptions{Level: slog.LevelWarn, AddSource: true}
	logger := slog.New(NewHandler(slog.NewTextHandler(&out, opts), opts))

	logger.Info("should be hidden")
	logger.Warn("should be shown")

	line := out.String()
	if strings.Contains(line, "should be hidden") {
		t.Errorf("info record passed warn threshold, got %q", line)
	}
	if !strings.Contains(line, "should be shown") {
		t.Errorf("warn record missing, got %q", line)
	}
}
