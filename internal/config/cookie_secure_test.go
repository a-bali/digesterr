package config

import (
	"path/filepath"
	"testing"
)

// TestCookieSecureConfig covers the tri-state server.cookie_secure setting: unset
// must stay distinguishable from an explicit false, because "follow the request"
// and "never set Secure" are different deployment intents.
func TestCookieSecureConfig(t *testing.T) {
	t.Run("unset is nil", func(t *testing.T) {
		clearEnv(t)
		cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Server.CookieSecure != nil {
			t.Errorf("CookieSecure = %v, want nil (unset follows the request TLS state)", *cfg.Server.CookieSecure)
		}
	})

	t.Run("file value parses", func(t *testing.T) {
		clearEnv(t)
		path := writeConfig(t, "server:\n  cookie_secure: true\n")
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Server.CookieSecure == nil || !*cfg.Server.CookieSecure {
			t.Errorf("CookieSecure = %v, want true", cfg.Server.CookieSecure)
		}
	})

	t.Run("env overrides file", func(t *testing.T) {
		clearEnv(t)
		path := writeConfig(t, "server:\n  cookie_secure: false\n")
		t.Setenv("SERVER_COOKIE_SECURE", "true")
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Server.CookieSecure == nil || !*cfg.Server.CookieSecure {
			t.Errorf("CookieSecure = %v, want true from the env var", cfg.Server.CookieSecure)
		}
	})

	t.Run("invalid env value keeps the configured setting", func(t *testing.T) {
		clearEnv(t)
		path := writeConfig(t, "server:\n  cookie_secure: true\n")
		t.Setenv("SERVER_COOKIE_SECURE", "not-a-bool")
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Server.CookieSecure == nil || !*cfg.Server.CookieSecure {
			t.Errorf("CookieSecure = %v, want the file's true to survive an unparseable env value", cfg.Server.CookieSecure)
		}
	})
}
