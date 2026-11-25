package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// OIDCConfig holds native OpenID Connect settings. The feature is opt-in:
// everything below is ignored unless Enabled is true, and a disabled section is
// never validated, so an operator can commit a half-filled section before the
// provider is ready.
//
// The flow implemented here is the authorization-code flow with PKCE, executed
// server-side by the digesterr binary against the configured issuer. Local
// email/password accounts, local signup, and Reader API keys are unaffected and
// remain available whether or not OIDC is enabled.
type OIDCConfig struct {
	Enabled bool `yaml:"enabled"`
	// Issuer is the provider's issuer identifier. It must match the `iss`
	// claim and the issuer in the discovery document exactly, including any
	// path and trailing slash. Discovery is read from
	// <issuer>/.well-known/openid-configuration at startup.
	Issuer string `yaml:"issuer"`
	// ClientID and ClientSecret identify the registered confidential client.
	// A secret is mandatory: the exchange happens on the server, and treating
	// the client as public would let anyone who learns the client_id redeem an
	// intercepted code.
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	// RedirectURL is the exact callback registered with the provider:
	// https://<host>/api/auth/oidc/callback. It is never derived from the
	// request Host header -- a derived redirect is attacker-controllable and
	// turns the callback into an open redirect plus a code leak.
	RedirectURL string `yaml:"redirect_url"`
	// ProviderName is the human label on the sign-in button ("Continue with
	// <name>"). Empty means "single sign-on".
	ProviderName string `yaml:"provider_name"`
	// Scopes overrides the default scopes. "openid" is mandatory.
	Scopes []string `yaml:"scopes"`
}

// CallbackPath is the fixed path component of the redirect URI. Config
// validation requires RedirectURL to end with it, so the registered URI and the
// served route cannot drift apart.
const CallbackPath = "/api/auth/oidc/callback"

// EffectiveScopes returns the scopes to request: the configured list, or the
// default when none is set. The result always starts with "openid".
func (c OIDCConfig) EffectiveScopes() []string {
	if len(c.Scopes) == 0 {
		return []string{"openid", "email", "profile"}
	}
	out := make([]string, 0, len(c.Scopes)+1)
	seenOpenID := false
	for _, s := range c.Scopes {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if s == "openid" {
			seenOpenID = true
		}
		out = append(out, s)
	}
	if !seenOpenID {
		out = append([]string{"openid"}, out...)
	}
	return out
}

// Validate checks the OIDC section and returns an error describing the first
// problem. It is a no-op when the feature is disabled.
func (c OIDCConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.Issuer) == "" {
		return fmt.Errorf("oidc.issuer is required")
	}
	issuer, err := requireAbsoluteHTTPS("oidc.issuer", c.Issuer)
	if err != nil {
		return err
	}
	if issuer.RawQuery != "" || issuer.Fragment != "" {
		return fmt.Errorf("oidc.issuer must not carry a query or fragment")
	}
	if strings.TrimSpace(c.ClientID) == "" {
		return fmt.Errorf("oidc.client_id is required")
	}
	if strings.TrimSpace(c.ClientSecret) == "" {
		return fmt.Errorf("oidc.client_secret is required")
	}
	if strings.TrimSpace(c.RedirectURL) == "" {
		return fmt.Errorf("oidc.redirect_url is required")
	}
	redirect, err := requireAbsoluteHTTPS("oidc.redirect_url", c.RedirectURL)
	if err != nil {
		return err
	}
	if redirect.RawQuery != "" || redirect.Fragment != "" {
		return fmt.Errorf("oidc.redirect_url must not carry a query or fragment")
	}
	if !strings.HasSuffix(strings.TrimRight(redirect.Path, "/"), CallbackPath) {
		return fmt.Errorf("oidc.redirect_url must end in %s", CallbackPath)
	}
	for _, s := range c.Scopes {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("oidc.scopes must not contain an empty scope")
		}
	}
	if len(c.Scopes) > 0 && !containsScope(c.Scopes, "openid") {
		return fmt.Errorf("oidc.scopes must include openid")
	}
	return nil
}

func containsScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if strings.TrimSpace(s) == want {
			return true
		}
	}
	return false
}

// requireAbsoluteHTTPS parses raw as an absolute URL and rejects any scheme
// other than https, with loopback hosts exempted so a local test provider works
// without a trusted certificate.
func requireAbsoluteHTTPS(field, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("%s must be an absolute URL", field)
	}
	switch u.Scheme {
	case "https":
		return u, nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return u, nil
		}
		return nil, fmt.Errorf("%s must use https (plain http is accepted only for loopback)", field)
	default:
		return nil, fmt.Errorf("%s must use https", field)
	}
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// parseScopeList splits a comma- or space-separated scope list from the
// environment.
func parseScopeList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' '
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// setBool sets a plain bool from an environment variable, leaving the
// configured value in place when the variable is unparseable.
func setBool(dst *bool, canonical string) {
	v, ok := lookupEnv(canonical)
	if !ok {
		return
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		slog.Warn("invalid value for environment variable, keeping configured value", "env", canonical, "value", v, "error", err)
		return
	}
	*dst = b
}
