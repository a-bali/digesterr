package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestResolveLogLevel(t *testing.T) {
	cases := []struct {
		name    string
		flagVal string
		env     map[string]string
		want    slog.Level
	}{
		{"default is info", "", nil, slog.LevelInfo},
		{"flag debug", "debug", nil, slog.LevelDebug},
		{"flag warn", "warn", nil, slog.LevelWarn},
		{"flag error", "error", nil, slog.LevelError},
		{"flag overrides env", "warn", map[string]string{"LOG_LEVEL": "debug"}, slog.LevelWarn},
		{"invalid flag falls back", "trace", nil, slog.LevelInfo},
		{"env debug", "", map[string]string{"LOG_LEVEL": "debug"}, slog.LevelDebug},
		{"env warn", "", map[string]string{"LOG_LEVEL": "warn"}, slog.LevelWarn},
		{"invalid env falls back", "", map[string]string{"LOG_LEVEL": "verbose"}, slog.LevelInfo},
		{"legacy alias debug", "", map[string]string{"DIGESTERR_LOG_LEVEL": "debug"}, slog.LevelDebug},
		{"env wins over legacy alias", "", map[string]string{"LOG_LEVEL": "warn", "DIGESTERR_LOG_LEVEL": "debug"}, slog.LevelWarn},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			// Ensure no stray env leaks between subtests.
			for _, k := range []string{"LOG_LEVEL", "DIGESTERR_LOG_LEVEL"} {
				if _, ok := c.env[k]; !ok {
					t.Setenv(k, "")
				}
			}
			if got := resolveLogLevel(c.flagVal); got != c.want {
				t.Errorf("resolveLogLevel(%q, %v) = %v, want %v", c.flagVal, c.env, got, c.want)
			}
		})
	}
}

// TestSetupLoggerDefaultHidesDebug verifies the default level is info:
// info lines appear, debug lines are dropped, and every emitted line carries
// a category key.
func TestSetupLoggerDefaultHidesDebug(t *testing.T) {
	var out bytes.Buffer
	buf := setupLogger(slog.LevelInfo, 10, &out)

	slog.Info("visible info")
	slog.Debug("hidden debug")
	slog.Warn("visible warn")

	text := out.String()
	for _, want := range []string{"visible info", "visible warn"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in output, got %q", want, text)
		}
	}
	if strings.Contains(text, "hidden debug") {
		t.Errorf("debug line emitted at default level, got %q", text)
	}
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if !strings.Contains(line, "category=") {
			t.Errorf("record without category key: %q", line)
		}
	}
	if got := buf.Len(); got != 2 {
		t.Errorf("buffer captured %d entries, want 2", got)
	}
}

// TestSetupLoggerDebugShowsDebug verifies LOG_LEVEL=debug enables debug lines.
func TestSetupLoggerDebugShowsDebug(t *testing.T) {
	var out bytes.Buffer
	setupLogger(slog.LevelDebug, 10, &out)

	slog.Debug("debug line visible")
	slog.Info("info line visible")

	text := out.String()
	if !strings.Contains(text, "debug line visible") {
		t.Errorf("debug line not emitted at debug level, got %q", text)
	}
	if !strings.Contains(text, "info line visible") {
		t.Errorf("info line not emitted at debug level, got %q", text)
	}
}

// TestSetupLoggerWarnOnly verifies -log-level=warn keeps only warn and above.
func TestSetupLoggerWarnOnly(t *testing.T) {
	var out bytes.Buffer
	setupLogger(slog.LevelWarn, 10, &out)

	slog.Info("hidden info")
	slog.Warn("visible warn")
	slog.Error("visible error")

	text := out.String()
	if strings.Contains(text, "hidden info") {
		t.Errorf("info line emitted at warn threshold, got %q", text)
	}
	for _, want := range []string{"visible warn", "visible error"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in output, got %q", want, text)
		}
	}
}

// TestSetupLoggerCategoryFromSource verifies that a record logged from this
// package (cmd/digesterr) gets category=app.
func TestSetupLoggerCategoryFromSource(t *testing.T) {
	var out bytes.Buffer
	setupLogger(slog.LevelInfo, 10, &out)

	slog.Info("startup")

	text := out.String()
	if !strings.Contains(text, "category=app") {
		t.Errorf("expected category=app for cmd/digesterr source, got %q", text)
	}
}

// TestSetupLoggerExplicitCategory verifies explicit categories pass through
// the full chain (category=item example).
func TestSetupLoggerExplicitCategory(t *testing.T) {
	var out bytes.Buffer
	setupLogger(slog.LevelInfo, 10, &out)

	slog.With("category", "item").Info("item op")

	text := out.String()
	if !strings.Contains(text, "category=item") {
		t.Errorf("expected category=item, got %q", text)
	}
}
