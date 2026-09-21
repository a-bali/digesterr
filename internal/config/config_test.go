package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "digesterr.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// clearEnv unsets every setting env var so tests start from a clean slate.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"SERVER_PORT", "PORT",
		"SERVER_JWT_SECRET", "JWT_SECRET",
		"SERVER_PUBLIC_BASE_URL", "PUBLIC_BASE_URL",
		"DB_PATH", "DATABASE_PATH",
		"LOGGING_LEVEL", "LOG_LEVEL", "DIGESTERR_LOG_LEVEL",
		"LOGGING_BUFFER_SIZE", "LOG_BUFFER_SIZE",
		"LLM_ENDPOINT", "LLM_API_KEY", "LLM_MODEL", "LLM_TIMEOUT",
		"FEED_SYNC_INTERVAL", "FEED_EXPIRE_ITEMS", "FEED_EXPIRE_DIGESTS",
		"FEED_EXTRACT_CONCURRENCY", "FEED_POLL_CONCURRENCY", "FEED_POLL_TIMEOUT",
		"REDDIT_CLIENT_ID", "REDDIT_CLIENT_SECRET",
	} {
		t.Setenv(k, "")
	}
}

func TestDefaults(t *testing.T) {
	clearEnv(t)
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("port = %d, want 8080", cfg.Server.Port)
	}
	if cfg.Server.JWTSecret != "" {
		t.Errorf("jwt_secret = %q, want empty", cfg.Server.JWTSecret)
	}
	if cfg.Server.PublicBaseURL != "" {
		t.Errorf("public_base_url = %q, want empty", cfg.Server.PublicBaseURL)
	}
	if cfg.DB.Path != "digesterr.db" {
		t.Errorf("db.path = %q, want digesterr.db", cfg.DB.Path)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("logging.level = %q, want info", cfg.Logging.Level)
	}
	if cfg.Logging.BufferSize != 1000 {
		t.Errorf("logging.buffer_size = %d, want 1000", cfg.Logging.BufferSize)
	}
	if cfg.LLM.Endpoint != "https://api.openai.com/v1" {
		t.Errorf("llm.endpoint = %q, want default", cfg.LLM.Endpoint)
	}
	if cfg.LLM.Model != "gpt-4o-mini" {
		t.Errorf("llm.model = %q, want gpt-4o-mini", cfg.LLM.Model)
	}
	if cfg.LLM.APIKey != "" {
		t.Errorf("llm.api_key = %q, want empty", cfg.LLM.APIKey)
	}
	if cfg.LLM.Timeout.Duration() != 5*time.Minute {
		t.Errorf("llm.timeout = %v, want 5m", cfg.LLM.Timeout.Duration())
	}
	if cfg.Feed.SyncInterval.Duration() != 15*time.Minute {
		t.Errorf("feed.sync_interval = %v, want 15m", cfg.Feed.SyncInterval.Duration())
	}
	if cfg.Feed.ExpireItems.Duration() != 365*24*time.Hour {
		t.Errorf("feed.expire_items = %v, want 8760h", cfg.Feed.ExpireItems.Duration())
	}
	if cfg.Feed.ExpireDigests.Duration() != 365*24*time.Hour {
		t.Errorf("feed.expire_digests = %v, want 8760h", cfg.Feed.ExpireDigests.Duration())
	}
	if cfg.Feed.PollConcurrency != 4 {
		t.Errorf("feed.poll_concurrency = %d, want 4", cfg.Feed.PollConcurrency)
	}
	if cfg.Feed.PollTimeout.Duration() != 5*time.Minute {
		t.Errorf("feed.poll_timeout = %v, want 5m", cfg.Feed.PollTimeout.Duration())
	}
	if cfg.Reddit.ClientID != "" || cfg.Reddit.ClientSecret != "" {
		t.Errorf("reddit credentials = %q/%q, want empty", cfg.Reddit.ClientID, cfg.Reddit.ClientSecret)
	}
}

func TestLoadYAMLFile(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `
server:
  port: 9000
  jwt_secret: file-secret
  public_base_url: https://reader.example.com
db:
  path: /var/lib/digesterr/db.sqlite
logging:
  level: warn
  buffer_size: 250
llm:
  endpoint: http://llm.local/v1
  api_key: file-llm-key
  model: llama-3
  timeout: 10m
feed:
  sync_interval: 30m
  expire_items: 6mo
  expire_digests: 90d
reddit:
  client_id: file-rid
  client_secret: file-rsecret
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 9000 {
		t.Errorf("port = %d, want 9000", cfg.Server.Port)
	}
	if cfg.Server.JWTSecret != "file-secret" {
		t.Errorf("jwt_secret = %q", cfg.Server.JWTSecret)
	}
	if cfg.Server.PublicBaseURL != "https://reader.example.com" {
		t.Errorf("public_base_url = %q", cfg.Server.PublicBaseURL)
	}
	if cfg.DB.Path != "/var/lib/digesterr/db.sqlite" {
		t.Errorf("db.path = %q", cfg.DB.Path)
	}
	if cfg.Logging.Level != "warn" {
		t.Errorf("logging.level = %q", cfg.Logging.Level)
	}
	if cfg.Logging.BufferSize != 250 {
		t.Errorf("logging.buffer_size = %d", cfg.Logging.BufferSize)
	}
	if cfg.LLM.Endpoint != "http://llm.local/v1" {
		t.Errorf("llm.endpoint = %q", cfg.LLM.Endpoint)
	}
	if cfg.LLM.APIKey != "file-llm-key" {
		t.Errorf("llm.api_key = %q", cfg.LLM.APIKey)
	}
	if cfg.LLM.Model != "llama-3" {
		t.Errorf("llm.model = %q", cfg.LLM.Model)
	}
	if cfg.LLM.Timeout.Duration() != 10*time.Minute {
		t.Errorf("llm.timeout = %v, want file 10m", cfg.LLM.Timeout.Duration())
	}
	if cfg.Feed.SyncInterval.Duration() != 30*time.Minute {
		t.Errorf("feed.sync_interval = %v, want 30m", cfg.Feed.SyncInterval.Duration())
	}
	if cfg.Feed.ExpireItems.Duration() != 6*30*24*time.Hour {
		t.Errorf("feed.expire_items = %v, want ~6mo", cfg.Feed.ExpireItems.Duration())
	}
	if cfg.Feed.ExpireDigests.Duration() != 90*24*time.Hour {
		t.Errorf("feed.expire_digests = %v, want 2160h", cfg.Feed.ExpireDigests.Duration())
	}
	if cfg.Reddit.ClientID != "file-rid" || cfg.Reddit.ClientSecret != "file-rsecret" {
		t.Errorf("reddit = %q/%q", cfg.Reddit.ClientID, cfg.Reddit.ClientSecret)
	}
}

func TestEnvOverridesYAML(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `
server:
  port: 9000
  jwt_secret: file-secret
db:
  path: file.db
logging:
  level: warn
llm:
  endpoint: http://file.local/v1
  api_key: file-key
  timeout: 3m
feed:
  sync_interval: 1h
  expire_items: 1y
reddit:
  client_id: file-rid
`)
	t.Setenv("SERVER_PORT", "7000")
	t.Setenv("JWT_SECRET", "env-secret")
	t.Setenv("DATABASE_PATH", "env.db")
	t.Setenv("LOGGING_LEVEL", "debug")
	t.Setenv("LLM_ENDPOINT", "http://env.local/v1")
	t.Setenv("LLM_API_KEY", "env-key")
	t.Setenv("LLM_TIMEOUT", "45s")
	t.Setenv("FEED_SYNC_INTERVAL", "15m")
	t.Setenv("FEED_EXPIRE_ITEMS", "30d")
	t.Setenv("REDDIT_CLIENT_ID", "env-rid")
	t.Setenv("REDDIT_CLIENT_SECRET", "env-rsecret")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 7000 {
		t.Errorf("port = %d, want env 7000", cfg.Server.Port)
	}
	if cfg.Server.JWTSecret != "env-secret" {
		t.Errorf("jwt_secret = %q, want env", cfg.Server.JWTSecret)
	}
	if cfg.DB.Path != "env.db" {
		t.Errorf("db.path = %q, want env", cfg.DB.Path)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("logging.level = %q, want env", cfg.Logging.Level)
	}
	if cfg.LLM.Endpoint != "http://env.local/v1" {
		t.Errorf("llm.endpoint = %q, want env", cfg.LLM.Endpoint)
	}
	if cfg.LLM.APIKey != "env-key" {
		t.Errorf("llm.api_key = %q, want env", cfg.LLM.APIKey)
	}
	if cfg.LLM.Timeout.Duration() != 45*time.Second {
		t.Errorf("llm.timeout = %v, want env 45s overriding file 3m", cfg.LLM.Timeout.Duration())
	}
	if cfg.Feed.SyncInterval.Duration() != 15*time.Minute {
		t.Errorf("feed.sync_interval = %v, want 15m", cfg.Feed.SyncInterval.Duration())
	}
	if cfg.Feed.ExpireItems.Duration() != 30*24*time.Hour {
		t.Errorf("feed.expire_items = %v, want 720h", cfg.Feed.ExpireItems.Duration())
	}
	if cfg.Reddit.ClientID != "env-rid" || cfg.Reddit.ClientSecret != "env-rsecret" {
		t.Errorf("reddit = %q/%q, want env", cfg.Reddit.ClientID, cfg.Reddit.ClientSecret)
	}
	// Unset-by-env values keep the file value.
	if cfg.Logging.BufferSize != 1000 {
		t.Errorf("logging.buffer_size = %d, want default (file did not set it)", cfg.Logging.BufferSize)
	}
}

func TestCanonicalEnvNames(t *testing.T) {
	clearEnv(t)
	// Every canonical SECTION_KEY name must be honored with no config file.
	t.Setenv("SERVER_PORT", "8081")
	t.Setenv("SERVER_JWT_SECRET", "canonical-secret")
	t.Setenv("SERVER_PUBLIC_BASE_URL", "https://canonical.example")
	t.Setenv("DB_PATH", "canonical.db")
	t.Setenv("LOGGING_LEVEL", "error")
	t.Setenv("LOGGING_BUFFER_SIZE", "42")
	t.Setenv("LLM_ENDPOINT", "http://canonical-llm/v1")
	t.Setenv("LLM_API_KEY", "canonical-key")
	t.Setenv("LLM_MODEL", "canonical-model")
	t.Setenv("LLM_TIMEOUT", "45s")
	t.Setenv("FEED_SYNC_INTERVAL", "45s")
	t.Setenv("FEED_EXPIRE_ITEMS", "2y")
	t.Setenv("FEED_EXPIRE_DIGESTS", "1d")
	t.Setenv("FEED_EXTRACT_CONCURRENCY", "6")
	t.Setenv("FEED_POLL_CONCURRENCY", "7")
	t.Setenv("FEED_POLL_TIMEOUT", "90s")
	t.Setenv("REDDIT_CLIENT_ID", "canonical-rid")
	t.Setenv("REDDIT_CLIENT_SECRET", "canonical-rsecret")

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 8081 || cfg.Server.JWTSecret != "canonical-secret" || cfg.Server.PublicBaseURL != "https://canonical.example" {
		t.Errorf("server = %+v", cfg.Server)
	}
	if cfg.DB.Path != "canonical.db" {
		t.Errorf("db.path = %q", cfg.DB.Path)
	}
	if cfg.Logging.Level != "error" || cfg.Logging.BufferSize != 42 {
		t.Errorf("logging = %+v", cfg.Logging)
	}
	if cfg.LLM.Endpoint != "http://canonical-llm/v1" || cfg.LLM.APIKey != "canonical-key" || cfg.LLM.Model != "canonical-model" {
		t.Errorf("llm = %+v", cfg.LLM)
	}
	if cfg.LLM.Timeout.Duration() != 45*time.Second {
		t.Errorf("llm.timeout = %v, want env 45s", cfg.LLM.Timeout.Duration())
	}
	if cfg.Feed.SyncInterval.Duration() != 45*time.Second {
		t.Errorf("feed.sync_interval = %v", cfg.Feed.SyncInterval.Duration())
	}
	if cfg.Feed.ExtractConcurrency != 6 {
		t.Errorf("feed.extract_concurrency = %d, want env 6", cfg.Feed.ExtractConcurrency)
	}
	if cfg.Feed.PollConcurrency != 7 {
		t.Errorf("feed.poll_concurrency = %d, want env 7", cfg.Feed.PollConcurrency)
	}
	if cfg.Feed.PollTimeout.Duration() != 90*time.Second {
		t.Errorf("feed.poll_timeout = %v, want env 90s", cfg.Feed.PollTimeout.Duration())
	}
	if cfg.Feed.ExpireItems.Duration() != 2*365*24*time.Hour {
		t.Errorf("feed.expire_items = %v, want 2y", cfg.Feed.ExpireItems.Duration())
	}
	if cfg.Feed.ExpireDigests.Duration() != 24*time.Hour {
		t.Errorf("feed.expire_digests = %v, want 1d", cfg.Feed.ExpireDigests.Duration())
	}
	if cfg.Reddit.ClientID != "canonical-rid" || cfg.Reddit.ClientSecret != "canonical-rsecret" {
		t.Errorf("reddit = %+v", cfg.Reddit)
	}
}

func TestLegacyEnvAliases(t *testing.T) {
	clearEnv(t)
	t.Setenv("PORT", "9090")
	t.Setenv("DATABASE_PATH", "legacy.db")
	t.Setenv("JWT_SECRET", "legacy-secret")
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("LOG_BUFFER_SIZE", "77")
	t.Setenv("PUBLIC_BASE_URL", "https://legacy.example")

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("port = %d, want 9090", cfg.Server.Port)
	}
	if cfg.DB.Path != "legacy.db" {
		t.Errorf("db.path = %q", cfg.DB.Path)
	}
	if cfg.Server.JWTSecret != "legacy-secret" {
		t.Errorf("jwt_secret = %q", cfg.Server.JWTSecret)
	}
	if cfg.Logging.Level != "warn" {
		t.Errorf("logging.level = %q", cfg.Logging.Level)
	}
	if cfg.Logging.BufferSize != 77 {
		t.Errorf("logging.buffer_size = %d", cfg.Logging.BufferSize)
	}
	if cfg.Server.PublicBaseURL != "https://legacy.example" {
		t.Errorf("public_base_url = %q", cfg.Server.PublicBaseURL)
	}
}

func TestCanonicalWinsOverLegacy(t *testing.T) {
	clearEnv(t)
	t.Setenv("SERVER_PORT", "7001")
	t.Setenv("PORT", "7002")
	t.Setenv("DB_PATH", "canonical.db")
	t.Setenv("DATABASE_PATH", "legacy.db")

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 7001 {
		t.Errorf("port = %d, want canonical 7001", cfg.Server.Port)
	}
	if cfg.DB.Path != "canonical.db" {
		t.Errorf("db.path = %q, want canonical", cfg.DB.Path)
	}
}

func TestLegacyLogLevelAlias(t *testing.T) {
	clearEnv(t)
	t.Setenv("DIGESTERR_LOG_LEVEL", "debug")

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("logging.level = %q, want debug via legacy alias", cfg.Logging.Level)
	}
}

func TestDefaultPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	want := filepath.Join(home, ".digesterr.yaml")
	if got := DefaultPath(); got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestInvalidYAMLReturnsError(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, "server: [unclosed")
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		err  bool
	}{
		{"1y", 365 * 24 * time.Hour, false},
		{"2y", 2 * 365 * 24 * time.Hour, false},
		{"0.5y", 0.5 * 365 * 24 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"30d", 30 * 24 * time.Hour, false},
		{"1h", time.Hour, false},
		{"90m", 90 * time.Minute, false},
		{"1h30m", 90 * time.Minute, false},
		{"", 0, true},
		{"abc", 0, true},
	}
	for _, c := range cases {
		got, err := ParseDuration(c.in)
		if c.err {
			if err == nil {
				t.Errorf("ParseDuration(%q) = %v, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseDuration(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseDuration(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
