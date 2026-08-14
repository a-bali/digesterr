// Package config loads Digesterr configuration from a YAML file, environment
// variables, and (in cmd/digesterr) CLI flags.
//
// Precedence (lowest to highest):
//
//  1. Built-in defaults
//  2. Values from the YAML config file (~/.digesterr.yaml by default)
//  3. Environment variables (canonical SECTION_KEY names, see below)
//  4. CLI flags (applied by the caller in cmd/digesterr/main.go)
//
// Environment variable names are derived from the YAML paths by uppercasing
// and replacing '.' with '_' (e.g. feed.sync_interval -> FEED_SYNC_INTERVAL).
// For backwards compatibility the legacy flat names (PORT, DATABASE_PATH,
// JWT_SECRET, LOG_LEVEL, LOG_BUFFER_SIZE, PUBLIC_BASE_URL) are still
// accepted as fallbacks, with the canonical name winning when both are set.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that also accepts "y" (year, 365 days) and
// "d" (day, 24 hours) suffixes in YAML and env values, e.g. "1y", "30d",
// "1h30m". Plain time.ParseDuration syntax works unchanged.
type Duration time.Duration

// Duration returns the underlying time.Duration.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// ParseDuration parses a duration string supporting the y/d suffixes.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if strings.HasSuffix(s, "y") {
		years, err := strconv.ParseFloat(strings.TrimSuffix(s, "y"), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid year duration %q: %w", s, err)
		}
		return time.Duration(years * float64(365*24*time.Hour)), nil
	}
	if strings.HasSuffix(s, "mo") {
		months, err := strconv.ParseFloat(strings.TrimSuffix(s, "mo"), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid month duration %q: %w", s, err)
		}
		return time.Duration(months * float64(30*24*time.Hour)), nil
	}
	if strings.HasSuffix(s, "d") {
		days, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid day duration %q: %w", s, err)
		}
		return time.Duration(days * float64(24*time.Hour)), nil
	}
	return time.ParseDuration(s)
}

// UnmarshalYAML lets yaml.v3 decode duration strings into Duration values.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var raw string
	if err := value.Decode(&raw); err != nil {
		return err
	}
	dur, err := ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("parsing duration %q: %w", raw, err)
	}
	*d = Duration(dur)
	return nil
}

// Config is the complete application configuration.
type Config struct {
	Server  ServerConfig  `yaml:"server"`
	DB      DBConfig      `yaml:"db"`
	Logging LoggingConfig `yaml:"logging"`
	LLM     LLMConfig     `yaml:"llm"`
	Feed    FeedConfig    `yaml:"feed"`
	Reddit  RedditConfig  `yaml:"reddit"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port          int    `yaml:"port"`
	JWTSecret     string `yaml:"jwt_secret"`
	PublicBaseURL string `yaml:"public_base_url"`
}

// DBConfig holds database settings.
type DBConfig struct {
	Path string `yaml:"path"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level      string `yaml:"level"`
	BufferSize int    `yaml:"buffer_size"`
}

// LLMConfig holds OpenAI-compatible API settings used for digest generation.
type LLMConfig struct {
	Endpoint string `yaml:"endpoint"`
	APIKey   string `yaml:"api_key"`
	Model    string `yaml:"model"`
}

// FeedConfig holds feed polling and data retention settings.
type FeedConfig struct {
	// SyncInterval is how often feeds are polled.
	SyncInterval Duration `yaml:"sync_interval"`
	// ExpireItems deletes items older than this duration during feed refresh
	// and digest generation. Saved items are never deleted.
	ExpireItems Duration `yaml:"expire_items"`
	// ExpireDigests deletes digest results older than this duration.
	ExpireDigests Duration `yaml:"expire_digests"`
}

// RedditConfig holds Reddit API credentials used for reddit feed enrichment.
type RedditConfig struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
}

// Default returns the built-in configuration, used when neither the config
// file nor environment variables set a value.
func Default() Config {
	return Config{
		Server: ServerConfig{
			Port:          8080,
			JWTSecret:     "",
			PublicBaseURL: "",
		},
		DB: DBConfig{
			Path: "digesterr.db",
		},
		Logging: LoggingConfig{
			Level:      "info",
			BufferSize: 1000,
		},
		LLM: LLMConfig{
			Endpoint: "https://api.openai.com/v1",
			APIKey:   "",
			Model:    "gpt-4o-mini",
		},
		Feed: FeedConfig{
			SyncInterval:  Duration(time.Hour),
			ExpireItems:   Duration(365 * 24 * time.Hour),
			ExpireDigests: Duration(365 * 24 * time.Hour),
		},
		Reddit: RedditConfig{},
	}
}

// DefaultPath returns the default config file path, ~/.digesterr.yaml.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".digesterr.yaml"
	}
	return filepath.Join(home, ".digesterr.yaml")
}

// Load reads the YAML config file at path (if it exists) and overlays
// environment variables, which take precedence. An empty path falls back to
// DefaultPath(). A missing file is not an error: built-in defaults apply.
func Load(path string) (Config, error) {
	if path == "" {
		path = DefaultPath()
	}

	cfg := Default()

	data, err := os.ReadFile(path)
	levelSetByFile := false
	switch {
	case err == nil:
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parsing config file %s: %w", path, err)
		}
		// Detect whether the file explicitly set logging.level so the legacy
		// DIGESTERR_LOG_LEVEL alias (lowest precedence) does not override it.
		var raw map[string]map[string]any
		if err := yaml.Unmarshal(data, &raw); err == nil {
			if sec, ok := raw["logging"]; ok {
				_, levelSetByFile = sec["level"]
			}
		}
	case os.IsNotExist(err):
		slog.Debug("config file not found, using defaults", "path", path)
	default:
		return Config{}, fmt.Errorf("reading config file %s: %w", path, err)
	}

	applyEnv(&cfg, levelSetByFile)
	return cfg, nil
}

// envVars returns the env var names to check for a setting: the canonical
// SECTION_KEY name first, then the optional legacy flat name.
func envVars(canonical, legacy string) []string {
	if legacy != "" {
		return []string{canonical, legacy}
	}
	return []string{canonical}
}

func lookupEnv(names ...string) (string, bool) {
	for _, n := range names {
		if v, ok := os.LookupEnv(n); ok && v != "" {
			return v, true
		}
	}
	return "", false
}

func setString(dst *string, canonical, legacy string) {
	if v, ok := lookupEnv(envVars(canonical, legacy)...); ok {
		*dst = v
	}
}

func setInt(dst *int, canonical, legacy string) {
	v, ok := lookupEnv(envVars(canonical, legacy)...)
	if !ok {
		return
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("invalid value for environment variable, keeping configured value", "env", canonical, "value", v, "error", err)
		return
	}
	*dst = n
}

func setDuration(dst *Duration, canonical string) {
	v, ok := lookupEnv(canonical)
	if !ok {
		return
	}
	d, err := ParseDuration(v)
	if err != nil {
		slog.Warn("invalid value for environment variable, keeping configured value", "env", canonical, "value", v, "error", err)
		return
	}
	*dst = Duration(d)
}

// applyEnv overlays environment variables onto cfg. Each setting maps to the
// canonical SECTION_KEY name (uppercased path with '.' -> '_') and, where a
// legacy flat env var predates the config file, that legacy name as fallback.
// levelSetByFile reports whether the YAML file explicitly set logging.level,
// which suppresses the DIGESTERR_LOG_LEVEL legacy alias (lowest precedence).
func applyEnv(cfg *Config, levelSetByFile bool) {
	setString(&cfg.Server.JWTSecret, "SERVER_JWT_SECRET", "JWT_SECRET")
	setString(&cfg.Server.PublicBaseURL, "SERVER_PUBLIC_BASE_URL", "PUBLIC_BASE_URL")
	setInt(&cfg.Server.Port, "SERVER_PORT", "PORT")

	setString(&cfg.DB.Path, "DB_PATH", "DATABASE_PATH")

	setString(&cfg.Logging.Level, "LOGGING_LEVEL", "LOG_LEVEL")
	setInt(&cfg.Logging.BufferSize, "LOGGING_BUFFER_SIZE", "LOG_BUFFER_SIZE")
	// Legacy alias: DIGESTERR_LOG_LEVEL=debug enabled debug logging before
	// LOG_LEVEL existed. Only the debug value was ever meaningful.
	if !levelSetByFile {
		if _, ok := lookupEnv("LOGGING_LEVEL", "LOG_LEVEL"); !ok && os.Getenv("DIGESTERR_LOG_LEVEL") == "debug" {
			cfg.Logging.Level = "debug"
		}
	}

	setString(&cfg.LLM.Endpoint, "LLM_ENDPOINT", "")
	setString(&cfg.LLM.APIKey, "LLM_API_KEY", "")
	setString(&cfg.LLM.Model, "LLM_MODEL", "")

	setDuration(&cfg.Feed.SyncInterval, "FEED_SYNC_INTERVAL")
	setDuration(&cfg.Feed.ExpireItems, "FEED_EXPIRE_ITEMS")
	setDuration(&cfg.Feed.ExpireDigests, "FEED_EXPIRE_DIGESTS")

	setString(&cfg.Reddit.ClientID, "REDDIT_CLIENT_ID", "")
	setString(&cfg.Reddit.ClientSecret, "REDDIT_CLIENT_SECRET", "")
}
