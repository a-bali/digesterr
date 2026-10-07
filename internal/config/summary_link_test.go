package config

import (
	"path/filepath"
	"testing"
)

func TestFeedSummaryLinkMinCharsConfig(t *testing.T) {
	clearEnv(t)
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Feed.SummaryLinkMinChars != 600 {
		t.Fatalf("default summary_link_min_chars = %d, want 600", cfg.Feed.SummaryLinkMinChars)
	}

	t.Setenv("FEED_SUMMARY_LINK_MIN_CHARS", "1234")
	cfg, err = Load(filepath.Join(t.TempDir(), "missing-env.yaml"))
	if err != nil {
		t.Fatalf("Load with env: %v", err)
	}
	if cfg.Feed.SummaryLinkMinChars != 1234 {
		t.Fatalf("env summary_link_min_chars = %d, want 1234", cfg.Feed.SummaryLinkMinChars)
	}
}
