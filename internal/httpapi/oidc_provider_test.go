package httpapi

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

// testOIDCProvider is a real OpenID Connect authorization server, served over
// HTTP on loopback by httptest: discovery document, JWKS, /authorize and
// /token. It is not a mock of our code — the server under test performs real
// discovery, a real code exchange, and real RS256 ID token verification against
// the JWKS published here.
//
// Loopback plain HTTP is deliberate and is exactly the exception
// config.OIDCConfig.Validate documents: a local provider cannot present a
// trusted certificate for a name the test controls, and the validation rule
// exists to protect public deployments, not test listeners.
type testOIDCProvider struct {
	srv      *httptest.Server
	key      *rsa.PrivateKey
	clientID string

	mu sync.Mutex
	// codes maps an issued authorization code to what /authorize recorded.
	codes map[string]*authCodeRecord
	// authorizeRequests records every /authorize query the client sent, so a
	// test can assert on scope, PKCE method, nonce and redirect_uri.
	authorizeRequests []url.Values
	tokenRequests     []url.Values

	// Behaviour knobs. Each one makes the provider misbehave in a specific,
	// realistic way so the server's validation can be shown to catch it.
	identity       func() (subject, email string, verified bool)
	mutateIDToken  func(claims map[string]any)
	mutateAuthzURL func(v url.Values)
	// skipPKCE accepts an authorization request without a code challenge, used
	// to prove the server always sends one.
	skipPKCE bool
	// signingKey, when set, is used to sign ID tokens while JWKS keeps
	// publishing key. That is the forgery case: well-formed token, correct
	// claims, signature by a key the provider never published.
	signingKey *rsa.PrivateKey
}

type authCodeRecord struct {
	clientID      string
	redirectURI   string
	codeChallenge string
	method        string
	nonce         string
	subject       string
	email         string
	verified      bool
	// redeemed is set by /token; a second exchange of the same code must fail.
	redeemed bool
}

func newTestOIDCProvider(t *testing.T, clientID string) *testOIDCProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	p := &testOIDCProvider{
		key:      key,
		clientID: clientID,
		codes:    map[string]*authCodeRecord{},
		identity: func() (string, string, bool) {
			return "subject-default", "person@example.com", true
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.handleDiscovery)
	mux.HandleFunc("GET /jwks", p.handleJWKS)
	mux.HandleFunc("GET /authorize", p.handleAuthorize)
	mux.HandleFunc("POST /token", p.handleToken)
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *testOIDCProvider) issuer() string { return p.srv.URL }

func (p *testOIDCProvider) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	writeJSONRaw(w, map[string]any{
		"issuer":                                p.srv.URL,
		"authorization_endpoint":                p.srv.URL + "/authorize",
		"token_endpoint":                        p.srv.URL + "/token",
		"jwks_uri":                              p.srv.URL + "/jwks",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      []string{"openid", "email", "profile"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		"code_challenge_methods_supported":      []string{"S256"},
	})
}

func (p *testOIDCProvider) handleJWKS(w http.ResponseWriter, r *http.Request) {
	pub := p.key.Public().(*rsa.PublicKey)
	writeJSONRaw(w, map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": "test-key-1",
			"n":   b64(pub.N.Bytes()),
			"e":   b64(bigEndianExponent(pub.E)),
		}},
	})
}

func (p *testOIDCProvider) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	p.mu.Lock()
	p.authorizeRequests = append(p.authorizeRequests, q)
	p.mu.Unlock()

	if p.mutateAuthzURL != nil {
		p.mutateAuthzURL(q)
	}

	redirectURI := q.Get("redirect_uri")
	fail := func(desc string) {
		// An authorization endpoint error travels back on the registered
		// redirect URI with an error parameter, which is what a real provider
		// does for a request it can parse.
		if redirectURI == "" {
			http.Error(w, desc, http.StatusBadRequest)
			return
		}
		u, _ := url.Parse(redirectURI)
		v := u.Query()
		v.Set("error", "invalid_request")
		v.Set("error_description", desc)
		if s := q.Get("state"); s != "" {
			v.Set("state", s)
		}
		u.RawQuery = v.Encode()
		w.Header().Set("Location", u.String())
		w.WriteHeader(http.StatusFound)
	}

	if q.Get("response_type") != "code" {
		fail("only response_type=code is supported")
		return
	}
	if q.Get("client_id") != p.clientID {
		fail("unknown client_id")
		return
	}
	if redirectURI == "" {
		fail("missing redirect_uri")
		return
	}
	if q.Get("state") == "" {
		fail("missing state")
		return
	}
	if q.Get("nonce") == "" {
		fail("missing nonce")
		return
	}
	if !p.skipPKCE {
		// The server must always send an S256 challenge. A missing one is a
		// silent downgrade to no PKCE at all.
		if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
			fail("PKCE S256 code_challenge is required")
			return
		}
	}

	subject, email, verified := p.identity()
	code := randHex(16)
	p.mu.Lock()
	p.codes[code] = &authCodeRecord{
		clientID:      q.Get("client_id"),
		redirectURI:   redirectURI,
		codeChallenge: q.Get("code_challenge"),
		method:        q.Get("code_challenge_method"),
		nonce:         q.Get("nonce"),
		subject:       subject,
		email:         email,
		verified:      verified,
	}
	p.mu.Unlock()

	u, _ := url.Parse(redirectURI)
	v := u.Query()
	v.Set("code", code)
	v.Set("state", q.Get("state"))
	u.RawQuery = v.Encode()
	w.Header().Set("Location", u.String())
	w.WriteHeader(http.StatusFound)
}

func (p *testOIDCProvider) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	p.tokenRequests = append(p.tokenRequests, r.PostForm)
	p.mu.Unlock()

	clientID, clientSecret, hasBasic := r.BasicAuth()
	if !hasBasic {
		clientID, clientSecret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if clientID != p.clientID || clientSecret == "" {
		writeJSONRaw(w, map[string]any{"error": "invalid_client"})
		return
	}
	if r.PostForm.Get("grant_type") != "authorization_code" {
		writeJSONRaw(w, map[string]any{"error": "unsupported_grant_type"})
		return
	}

	code := r.PostForm.Get("code")
	p.mu.Lock()
	rec, ok := p.codes[code]
	if ok {
		if rec.redeemed {
			p.mu.Unlock()
			writeJSONRaw(w, map[string]any{"error": "invalid_grant", "error_description": "code already redeemed"})
			return
		}
		rec.redeemed = true
	}
	p.mu.Unlock()
	if !ok {
		writeJSONRaw(w, map[string]any{"error": "invalid_grant"})
		return
	}

	if rec.redirectURI != r.PostForm.Get("redirect_uri") {
		writeJSONRaw(w, map[string]any{"error": "invalid_grant", "error_description": "redirect_uri mismatch"})
		return
	}
	// PKCE verification, S256 only.
	if rec.codeChallenge != "" {
		verifier := r.PostForm.Get("code_verifier")
		if verifier == "" {
			writeJSONRaw(w, map[string]any{"error": "invalid_grant", "error_description": "missing code_verifier"})
			return
		}
		sum := sha256.Sum256([]byte(verifier))
		if b64(sum[:]) != rec.codeChallenge {
			writeJSONRaw(w, map[string]any{"error": "invalid_grant", "error_description": "code_verifier does not match challenge"})
			return
		}
	} else if !p.skipPKCE {
		writeJSONRaw(w, map[string]any{"error": "invalid_grant", "error_description": "no PKCE challenge was recorded"})
		return
	}

	now := time.Now()
	claims := map[string]any{
		"iss":            p.srv.URL,
		"sub":            rec.subject,
		"aud":            p.clientID,
		"exp":            now.Add(5 * time.Minute).Unix(),
		"iat":            now.Unix(),
		"nonce":          rec.nonce,
		"email":          rec.email,
		"email_verified": rec.verified,
	}
	if p.mutateIDToken != nil {
		p.mutateIDToken(claims)
	}
	idToken := p.sign(claims)

	writeJSONRaw(w, map[string]any{
		"access_token": "provider-access-token-not-for-storage",
		"token_type":   "Bearer",
		"expires_in":   300,
		"id_token":     idToken,
	})
}

// sign produces an RS256 JWT with the provider's (or the forged) key.
func (p *testOIDCProvider) sign(claims map[string]any) string {
	header := map[string]any{"alg": "RS256", "typ": "JWT", "kid": "test-key-1"}
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	signingInput := b64(h) + "." + b64(c)
	sum := sha256.Sum256([]byte(signingInput))
	p.mu.Lock()
	key := p.key
	if p.signingKey != nil {
		key = p.signingKey
	}
	p.mu.Unlock()
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		panic(err)
	}
	return signingInput + "." + b64(sig)
}

// lastAuthorizeQuery returns the most recent /authorize query.
func (p *testOIDCProvider) lastAuthorizeQuery(t *testing.T) url.Values {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.authorizeRequests) == 0 {
		t.Fatal("the server never called /authorize: OIDC sign-in did not start a flow")
	}
	return p.authorizeRequests[len(p.authorizeRequests)-1]
}

func (p *testOIDCProvider) lastTokenRequest(t *testing.T) url.Values {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.tokenRequests) == 0 {
		t.Fatal("the server never called /token: the authorization code was never exchanged")
	}
	return p.tokenRequests[len(p.tokenRequests)-1]
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func bigEndianExponent(e int) []byte {
	out := []byte{}
	for e > 0 {
		out = append([]byte{byte(e & 0xff)}, out...)
		e >>= 8
	}
	return out
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("rand: %v", err))
	}
	return fmt.Sprintf("%x", b)
}

func writeJSONRaw(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
