package config

import (
	"path/filepath"
	"testing"
)

// The email feed type needs both values, and the canonical env names must win
// over the legacy AGENTMAIL_* aliases the reverted digest-delivery feature used.
func TestEmailConfigEnvOverlay(t *testing.T) {
	clearEnv(t)

	// Nothing set: the type is unconfigured, not half-configured.
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Email.APIKey != "" || cfg.Email.InboxID != "" {
		t.Fatalf("email config = %q/%q, want empty by default", cfg.Email.APIKey, cfg.Email.InboxID)
	}
	if cfg.Email.EmailConfigured() {
		t.Fatal("EmailConfigured() = true with no credentials set")
	}

	// Canonical names.
	t.Setenv("EMAIL_API_KEY", "canonical-key")
	t.Setenv("EMAIL_INBOX_ID", "canonical@agentmail.to")
	cfg, err = Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Email.APIKey != "canonical-key" {
		t.Errorf("email.api_key = %q, want canonical-key", cfg.Email.APIKey)
	}
	if cfg.Email.InboxID != "canonical@agentmail.to" {
		t.Errorf("email.inbox_id = %q, want canonical@agentmail.to", cfg.Email.InboxID)
	}
	if !cfg.Email.EmailConfigured() {
		t.Error("EmailConfigured() = false with both values set")
	}

	// Canonical wins when the legacy alias is also present: the reverted
	// feature used AGENTMAIL_TOKEN/AGENTMAIL_INBOX_ID, and a stale value left
	// in a deployment must not shadow the operator's explicit setting.
	t.Setenv("AGENTMAIL_API_KEY", "legacy-key")
	t.Setenv("AGENTMAIL_INBOX_ID", "legacy@agentmail.to")
	cfg, err = Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Email.APIKey != "canonical-key" {
		t.Errorf("email.api_key = %q, want canonical-key to win over the legacy alias", cfg.Email.APIKey)
	}
	if cfg.Email.InboxID != "canonical@agentmail.to" {
		t.Errorf("email.inbox_id = %q, want canonical to win over the legacy alias", cfg.Email.InboxID)
	}
}

// A deployment that predates this feature carries AGENTMAIL_API_KEY (it is set
// in this container's environment), so the alias alone has to work.
func TestEmailConfigLegacyAliasOnly(t *testing.T) {
	clearEnv(t)
	t.Setenv("AGENTMAIL_API_KEY", "legacy-only-key")
	t.Setenv("AGENTMAIL_INBOX_ID", "legacy-only@agentmail.to")

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Email.APIKey != "legacy-only-key" {
		t.Errorf("email.api_key = %q, want the legacy alias to apply", cfg.Email.APIKey)
	}
	if cfg.Email.InboxID != "legacy-only@agentmail.to" {
		t.Errorf("email.inbox_id = %q, want the legacy alias to apply", cfg.Email.InboxID)
	}
}

// The YAML file is a valid source, and env still overrides it.
func TestEmailConfigYAMLAndEnvPrecedence(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `
email:
  api_key: file-key
  inbox_id: file@agentmail.to
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Email.APIKey != "file-key" || cfg.Email.InboxID != "file@agentmail.to" {
		t.Fatalf("email from file = %q/%q, want file values", cfg.Email.APIKey, cfg.Email.InboxID)
	}

	t.Setenv("EMAIL_API_KEY", "env-key")
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Email.APIKey != "env-key" {
		t.Errorf("email.api_key = %q, want env to override the file", cfg.Email.APIKey)
	}
	// Unset-by-env keeps the file value.
	if cfg.Email.InboxID != "file@agentmail.to" {
		t.Errorf("email.inbox_id = %q, want the file value (env did not set it)", cfg.Email.InboxID)
	}
}

// Half-configuration must not report as usable: a key with no mailbox cannot
// be polled, and the UI relies on this to avoid offering a type that cannot work.
func TestEmailConfiguredRequiresBoth(t *testing.T) {
	cases := []struct {
		name       string
		cfg        EmailConfig
		configured bool
	}{
		{"both", EmailConfig{APIKey: "k", InboxID: "a@b.c"}, true},
		{"key only", EmailConfig{APIKey: "k"}, false},
		{"inbox only", EmailConfig{InboxID: "a@b.c"}, false},
		{"neither", EmailConfig{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.EmailConfigured(); got != tc.configured {
				t.Errorf("EmailConfigured() = %v, want %v", got, tc.configured)
			}
		})
	}
}
