package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// oidcBase returns a valid enabled OIDC configuration that individual tests
// then break one field at a time. Deriving every case from one valid value
// keeps the tests about the rule under test rather than about assembling
// config.
func oidcBase() OIDCConfig {
	return OIDCConfig{
		Enabled:      true,
		Issuer:       "https://id.example.com",
		ClientID:     "digesterr",
		ClientSecret: "s3cret",
		RedirectURL:  "https://reader.example.com/api/auth/oidc/callback",
	}
}

func TestOIDCValidateAcceptsCompleteConfig(t *testing.T) {
	cfg := oidcBase()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil for a complete config", err)
	}
}

func TestOIDCValidateIsNoOpWhenDisabled(t *testing.T) {
	// Every field is wrong, but the feature is off: startup must not fail on
	// settings that are not in use, or an operator cannot stage a config
	// before the provider is ready.
	cfg := OIDCConfig{Enabled: false, Issuer: "not a url", RedirectURL: "nope"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil when oidc.enabled is false", err)
	}
}

func TestOIDCValidateRejectsIncompleteConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*OIDCConfig)
		want   string
	}{
		{"missing issuer", func(c *OIDCConfig) { c.Issuer = "" }, "oidc.issuer is required"},
		{"relative issuer", func(c *OIDCConfig) { c.Issuer = "id.example.com" }, "oidc.issuer must be an absolute URL"},
		{"plain http issuer", func(c *OIDCConfig) { c.Issuer = "http://id.example.com" }, "oidc.issuer must use https"},
		{"issuer with query", func(c *OIDCConfig) { c.Issuer = "https://id.example.com/?x=1" }, "oidc.issuer must not carry a query or fragment"},
		{"missing client id", func(c *OIDCConfig) { c.ClientID = "" }, "oidc.client_id is required"},
		// A confidential client is required: the authorization-code flow here
		// is server-side, and PKCE alone would make the client public.
		{"missing client secret", func(c *OIDCConfig) { c.ClientSecret = "" }, "oidc.client_secret is required"},
		{"missing redirect", func(c *OIDCConfig) { c.RedirectURL = "" }, "oidc.redirect_url is required"},
		{"relative redirect", func(c *OIDCConfig) { c.RedirectURL = "/api/auth/oidc/callback" }, "oidc.redirect_url must be an absolute URL"},
		{"plain http redirect", func(c *OIDCConfig) { c.RedirectURL = "http://reader.example.com/api/auth/oidc/callback" }, "oidc.redirect_url must use https"},
		// The callback path is a fixed route. A URL pointing anywhere else can
		// never complete: the provider would redirect to a page that does not
		// exist, and the code would be dropped.
		{"wrong callback path", func(c *OIDCConfig) { c.RedirectURL = "https://reader.example.com/oauth/cb" }, "oidc.redirect_url must end in /api/auth/oidc/callback"},
		{"redirect with query", func(c *OIDCConfig) { c.RedirectURL = "https://reader.example.com/api/auth/oidc/callback?x=1" }, "oidc.redirect_url must not carry a query or fragment"},
		{"scopes without openid", func(c *OIDCConfig) { c.Scopes = []string{"email", "profile"} }, "oidc.scopes must include openid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := oidcBase()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want an error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

// Loopback is the one place plain HTTP is allowed: a test or a locally run
// provider cannot present a trusted certificate, and refusing http://127.0.0.1
// would make the integration tests impossible to write without a CA.
func TestOIDCValidateAllowsLoopbackHTTP(t *testing.T) {
	cfg := OIDCConfig{
		Enabled:      true,
		Issuer:       "http://127.0.0.1:9000",
		ClientID:     "digesterr",
		ClientSecret: "s3cret",
		RedirectURL:  "http://localhost:8080/api/auth/oidc/callback",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil for loopback http", err)
	}
}

func TestOIDCScopesDefaultToOpenIDEmailProfile(t *testing.T) {
	cfg := oidcBase()
	got := cfg.EffectiveScopes()
	want := []string{"openid", "email", "profile"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("EffectiveScopes() = %v, want %v", got, want)
	}
}

func TestOIDCEnvOverridesConfig(t *testing.T) {
	t.Setenv("OIDC_ENABLED", "true")
	t.Setenv("OIDC_ISSUER", "https://env.example.com")
	t.Setenv("OIDC_CLIENT_ID", "env-client")
	t.Setenv("OIDC_CLIENT_SECRET", "env-secret")
	t.Setenv("OIDC_REDIRECT_URL", "https://env.example.com/api/auth/oidc/callback")
	t.Setenv("OIDC_PROVIDER_NAME", "Env SSO")
	t.Setenv("OIDC_SCOPES", "openid, groups")

	cfg, err := Load(writeConfig(t, "server:\n  jwt_secret: x\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.OIDC.Enabled {
		t.Error("OIDC.Enabled = false, want true from OIDC_ENABLED")
	}
	if cfg.OIDC.Issuer != "https://env.example.com" {
		t.Errorf("OIDC.Issuer = %q", cfg.OIDC.Issuer)
	}
	if cfg.OIDC.ClientID != "env-client" || cfg.OIDC.ClientSecret != "env-secret" {
		t.Errorf("client credentials not applied: %q / %q", cfg.OIDC.ClientID, cfg.OIDC.ClientSecret)
	}
	if cfg.OIDC.RedirectURL != "https://env.example.com/api/auth/oidc/callback" {
		t.Errorf("OIDC.RedirectURL = %q", cfg.OIDC.RedirectURL)
	}
	if cfg.OIDC.ProviderName != "Env SSO" {
		t.Errorf("OIDC.ProviderName = %q", cfg.OIDC.ProviderName)
	}
	if strings.Join(cfg.OIDC.Scopes, " ") != "openid groups" {
		t.Errorf("OIDC.Scopes = %v, want [openid groups]", cfg.OIDC.Scopes)
	}
}

// An unparseable bool must not silently enable or disable the feature: the
// configured value stays, and the operator sees a warning (same policy as the
// other typed env settings).
func TestOIDCInvalidEnabledEnvKeepsConfiguredValue(t *testing.T) {
	t.Setenv("OIDC_ENABLED", "yes-please")
	cfg, err := Load(writeConfig(t, "oidc:\n  enabled: false\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OIDC.Enabled {
		t.Fatal("OIDC.Enabled = true, want the configured false to survive an invalid env value")
	}
}

// The shipped example config is a document operators copy. It has to parse and
// has to describe the feature as OFF by default: an example that silently
// enables a login path nobody configured would be worse than no example.
func TestExampleConfigParsesWithOIDCDisabled(t *testing.T) {
	path := filepath.Join("..", "..", "digesterr.example.yaml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("the shipped example config does not load: %v", err)
	}
	if cfg.OIDC.Enabled {
		t.Error("digesterr.example.yaml enables oidc: a copied example must not turn on an unconfigured provider")
	}
	if err := cfg.OIDC.Validate(); err != nil {
		t.Errorf("disabled example section fails validation: %v", err)
	}
}
