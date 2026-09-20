package main

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"digesterr/internal/config"
)

func TestResolveLogLevel(t *testing.T) {
	cases := []struct {
		name  string
		level string
		want  slog.Level
	}{
		{"default is info", "", slog.LevelInfo},
		{"debug", "debug", slog.LevelDebug},
		{"info", "info", slog.LevelInfo},
		{"warn", "warn", slog.LevelWarn},
		{"error", "error", slog.LevelError},
		{"invalid falls back", "verbose", slog.LevelInfo},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveLogLevel(c.level); got != c.want {
				t.Errorf("resolveLogLevel(%q) = %v, want %v", c.level, got, c.want)
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

// TestSetupLoggerEnvTogglesDebug verifies the LOG_LEVEL env var reaches the
// logger chain: with LOG_LEVEL=debug, debug lines are emitted; with
// LOG_LEVEL=warn they are dropped again. This is the integration-level check
// for the "LOG_LEVEL override works" rule — it fails if setupLogger stops
// honoring resolveLogLevel's result.
func TestSetupLoggerEnvTogglesDebug(t *testing.T) {
	loadCfg := func(t *testing.T) config.Config {
		t.Helper()
		cfg, err := config.Load(filepath.Join(t.TempDir(), "missing.yaml"))
		if err != nil {
			t.Fatalf("config.Load: %v", err)
		}
		return cfg
	}

	t.Run("LOG_LEVEL=debug shows debug", func(t *testing.T) {
		t.Setenv("LOG_LEVEL", "debug")
		t.Setenv("DIGESTERR_LOG_LEVEL", "")

		var out bytes.Buffer
		setupLogger(resolveLogLevel(loadCfg(t).Logging.Level), 10, &out)

		slog.Debug("debug visible via env")
		text := out.String()
		if !strings.Contains(text, "debug visible via env") {
			t.Errorf("debug line not emitted with LOG_LEVEL=debug, got %q", text)
		}
	})

	t.Run("flag overrides LOG_LEVEL=debug", func(t *testing.T) {
		t.Setenv("LOG_LEVEL", "debug")
		t.Setenv("DIGESTERR_LOG_LEVEL", "")

		cfg := loadCfg(t)
		cfg.Logging.Level = "warn" // what -l/--log-level does in main()

		var out bytes.Buffer
		setupLogger(resolveLogLevel(cfg.Logging.Level), 10, &out)

		slog.Debug("debug hidden by flag")
		slog.Warn("warn visible via flag")
		text := out.String()
		if strings.Contains(text, "debug hidden by flag") {
			t.Errorf("debug line emitted with -log-level=warn, got %q", text)
		}
		if !strings.Contains(text, "warn visible via flag") {
			t.Errorf("warn line missing with -log-level=warn, got %q", text)
		}
	})
}
