package httpapi

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"digesterr/internal/config"
	"digesterr/internal/logbuf"
	"digesterr/internal/auth"
)

// cookieSecureFor builds a server with the given cookie_secure setting and
// reports the Secure flag on the refresh cookie for a request that is (or is
// not) on TLS. Setting nil means the setting is unset.
func cookieSecureFor(t *testing.T, setting *bool, onTLS bool) bool {
	t.Helper()
	db, queries := setupTestDB(t)
	authSvc := auth.NewService("test-jwt-secret-for-testing", queries, db)
	cfg := config.Default()
	cfg.Server.CookieSecure = setting
	server := NewServer(db, queries, authSvc, logbuf.New(100), cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/signin", nil)
	if onTLS {
		req.TLS = &tls.ConnectionState{}
	}
	return server.refreshCookie(req, "token", 3600).Secure
}

func boolPtr(v bool) *bool { return &v }

// TestRefreshCookieSecureDefaultsToRequestTLS pins back-compat: with no
// cookie_secure setting the flag keeps following r.TLS, which is exactly the old
// behaviour. This is what makes the setting safe to add — an existing deployment
// that sets nothing is unaffected, and a plain-HTTP install keeps a working
// refresh cookie.
func TestRefreshCookieSecureDefaultsToRequestTLS(t *testing.T) {
	if got := cookieSecureFor(t, nil, true); !got {
		t.Error("unset setting on TLS: Secure = false, want true")
	}
	if got := cookieSecureFor(t, nil, false); got {
		t.Error("unset setting on plain HTTP: Secure = true, want false — this would break non-HTTPS installs")
	}
}

// TestRefreshCookieSecureExplicitOverridesRequest covers the deployment the
// setting exists for: TLS terminates at a proxy, so r.TLS is nil on the request
// the app sees and the cookie would otherwise be issued without Secure.
func TestRefreshCookieSecureExplicitOverridesRequest(t *testing.T) {
	if got := cookieSecureFor(t, boolPtr(true), false); !got {
		t.Error("cookie_secure: true behind a TLS proxy: Secure = false, want true")
	}
	// The inverse must work too: an operator who knows the deployment is plain
	// HTTP can force the flag off even if something upstream sets r.TLS.
	if got := cookieSecureFor(t, boolPtr(false), true); got {
		t.Error("cookie_secure: false: Secure = true, want false")
	}
}

// TestRefreshCookieKeepsProtectiveAttributes guards the rest of the cookie while
// the Secure flag is being made configurable.
func TestRefreshCookieKeepsProtectiveAttributes(t *testing.T) {
	db, queries := setupTestDB(t)
	authSvc := auth.NewService("test-jwt-secret-for-testing", queries, db)
	server := NewServer(db, queries, authSvc, logbuf.New(100), config.Default())

	c := server.refreshCookie(httptest.NewRequest(http.MethodPost, "/api/auth/signin", nil), "tok", 3600)
	if !c.HttpOnly {
		t.Error("refresh cookie must stay HttpOnly: it is the one credential the SPA cannot read")
	}
	if c.Path != "/api/auth/refresh" {
		t.Errorf("Path = %q, want /api/auth/refresh", c.Path)
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
}
