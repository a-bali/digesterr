package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"digesterr/internal/auth"
	"digesterr/internal/config"
	"digesterr/internal/logbuf"
	"digesterr/internal/store"
)

// emailTestServer returns a server with the email feed type configured, since
// config.Default() leaves it off (which is itself a case worth testing).
// It returns the server's OWN queries: calling setupTestDB separately in a test
// yields a different in-memory database, so users created there would not exist
// for the server and feed creation would fail on the user_id foreign key.
func emailTestServer(t *testing.T, inbox string) (*Server, *store.Queries) {
	t.Helper()
	db, queries := setupTestDB(t)
	cfg := config.Default()
	cfg.Email.APIKey = "test-agentmail-key"
	cfg.Email.InboxID = inbox
	authSvc := auth.NewService("test-jwt-secret-for-testing", queries, db)
	return NewServer(db, queries, authSvc, logbuf.New(100), cfg), queries
}

// The unauthenticated health payload must stay byte-identical to the previous
// shape so probes are unaffected, and the email keys must be ABSENT (not false)
// so the frontend can tell "not configured" from "not logged in".
func TestHealthEmailKeysOnlyWhenAuthenticated(t *testing.T) {
	s, queries := emailTestServer(t, "name@agentmail.to")

	// Unauthenticated: no email keys at all.
	w := makeRequest(s, http.MethodGet, "/api/health", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("health status = %d", w.Code)
	}
	var anon map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &anon); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(anon) != 1 || anon["status"] != "ok" {
		t.Fatalf("unauthenticated health payload = %v, want exactly {status: ok}", anon)
	}
	for _, k := range []string{"email_enabled", "email_inbox", "email_prefix", "email_domain"} {
		if _, ok := anon[k]; ok {
			t.Errorf("key %q present in unauthenticated payload", k)
		}
	}

	// Authenticated: the feature keys appear with the address shape the UI needs.
	user := createTestUser(t, queries, "email-health@example.com", "pw12345678")
	token := getTestToken(t, s.auth, &user)
	w = makeRequest(s, http.MethodGet, "/api/health", nil, token)
	var authed map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &authed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if authed["email_enabled"] != true {
		t.Errorf("email_enabled = %v, want true", authed["email_enabled"])
	}
	if authed["email_inbox"] != "name@agentmail.to" {
		t.Errorf("email_inbox = %v", authed["email_inbox"])
	}
	if authed["email_prefix"] != "name+" {
		t.Errorf("email_prefix = %v, want the fixed prefix the UI renders", authed["email_prefix"])
	}
	if authed["email_domain"] != "@agentmail.to" {
		t.Errorf("email_domain = %v", authed["email_domain"])
	}
}

// An instance without AgentMail credentials must report the feature off and
// expose no address shape, so the UI can disable the option honestly.
func TestHealthEmailUnconfigured(t *testing.T) {
	s, queries := setupTestServer(t) // config.Default(): no email credentials
	user := createTestUser(t, queries, "no-email@example.com", "pw12345678")
	token := getTestToken(t, s.auth, &user)

	w := makeRequest(s, http.MethodGet, "/api/health", nil, token)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["email_enabled"] != false {
		t.Errorf("email_enabled = %v, want false", body["email_enabled"])
	}
	for _, k := range []string{"email_inbox", "email_prefix", "email_domain"} {
		if _, ok := body[k]; ok {
			t.Errorf("key %q should be omitted when unconfigured, got %v", k, body[k])
		}
	}
}

// An invalid address must be rejected with 400 BEFORE it is stored: a wrong
// suffix is not a harmless typo (there is no catch-all on AgentMail), it is a
// feed that silently never receives anything.
func TestCreateEmailFeedValidatesAddress(t *testing.T) {
	s, queries := emailTestServer(t, "name@agentmail.to")
	user := createTestUser(t, queries, "email-create@example.com", "pw12345678")
	token := getTestToken(t, s.auth, &user)

	cases := []struct {
		name string
		url  string
		want int
	}{
		{"valid synthetic address", "name+sport@agentmail.to", http.StatusCreated},
		{"wrong domain", "name+sport@example.com", http.StatusBadRequest},
		{"wrong mailbox", "other+sport@agentmail.to", http.StatusBadRequest},
		{"no plus", "name@agentmail.to", http.StatusBadRequest},
		{"empty suffix", "name+@agentmail.to", http.StatusBadRequest},
		{"invalid suffix characters", "name+sp ort@agentmail.to", http.StatusBadRequest},
		{"slash in suffix", "name+a/b@agentmail.to", http.StatusBadRequest},
		{"empty url", "", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := makeRequest(s, http.MethodPost, "/api/feeds", map[string]any{
				"type": "email", "name": tc.name, "url": tc.url,
			}, token)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

// The stored URL must be the canonical form, lowercased, so the same address
// typed with different casing cannot become two feeds.
func TestCreateEmailFeedNormalizesAddress(t *testing.T) {
	s, queries := emailTestServer(t, "name@agentmail.to")
	user := createTestUser(t, queries, "email-norm@example.com", "pw12345678")
	token := getTestToken(t, s.auth, &user)

	w := makeRequest(s, http.MethodPost, "/api/feeds", map[string]any{
		"type": "email", "name": "Sport", "url": "NAME+Sport@AGENTMAIL.TO",
	}, token)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created["url"] != "name+sport@agentmail.to" {
		t.Fatalf("stored url = %v, want the canonical lowercase form", created["url"])
	}
}

// THE isolation case: the address namespace is instance-wide, so a second user
// must not be able to claim an address already in use — otherwise both feeds
// read the same mail, and one user reads the other's subscription.
func TestEmailAddressIsUniqueAcrossUsers(t *testing.T) {
	s, queries := emailTestServer(t, "name@agentmail.to")

	alice := createTestUser(t, queries, "alice@example.com", "pw12345678")
	bob := createTestUser(t, queries, "bob@example.com", "pw12345678")
	aliceToken := getTestToken(t, s.auth, &alice)
	bobToken := getTestToken(t, s.auth, &bob)

	w := makeRequest(s, http.MethodPost, "/api/feeds", map[string]any{
		"type": "email", "name": "Alice sport", "url": "name+sport@agentmail.to",
	}, aliceToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("alice's create = %d, body: %s", w.Code, w.Body.String())
	}

	// Bob claims the same address.
	w = makeRequest(s, http.MethodPost, "/api/feeds", map[string]any{
		"type": "email", "name": "Bob sport", "url": "name+sport@agentmail.to",
	}, bobToken)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bob's create = %d, want 400 (the namespace is instance-wide)", w.Code)
	}
	// The rejection must not name the other user's feed.
	if body := w.Body.String(); strings.Contains(body, "Alice") {
		t.Errorf("rejection leaked the other user's feed name: %s", body)
	}

	// A different suffix is still free for Bob.
	w = makeRequest(s, http.MethodPost, "/api/feeds", map[string]any{
		"type": "email", "name": "Bob news", "url": "name+news@agentmail.to",
	}, bobToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("bob's second create = %d, want 201 for a free address; body: %s", w.Code, w.Body.String())
	}
}

// Updating a feed without changing its address must not conflict with itself.
func TestUpdateEmailFeedWithoutAddressChange(t *testing.T) {
	s, queries := emailTestServer(t, "name@agentmail.to")
	user := createTestUser(t, queries, "email-update@example.com", "pw12345678")
	token := getTestToken(t, s.auth, &user)

	w := makeRequest(s, http.MethodPost, "/api/feeds", map[string]any{
		"type": "email", "name": "Sport", "url": "name+sport@agentmail.to",
	}, token)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d, body: %s", w.Code, w.Body.String())
	}
	var created map[string]any
	json.Unmarshal(w.Body.Bytes(), &created)
	id := int64(created["id"].(float64))

	// Rename only: the unchanged address must not be seen as a conflict.
	w = makeRequest(s, http.MethodPatch, "/api/feeds/"+itoa(id), map[string]any{
		"name": "Sport News",
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("rename = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	// Changing the address to an invalid one must be rejected.
	w = makeRequest(s, http.MethodPatch, "/api/feeds/"+itoa(id), map[string]any{
		"url": "name+bad suffix@agentmail.to",
	}, token)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid address update = %d, want 400", w.Code)
	}
}

// Discover must name an email feed from its suffix with no network call.
func TestDiscoverEmailFeedName(t *testing.T) {
	s, queries := emailTestServer(t, "name@agentmail.to")
	user := createTestUser(t, queries, "email-discover@example.com", "pw12345678")
	token := getTestToken(t, s.auth, &user)

	w := makeRequest(s, http.MethodPost, "/api/feeds/discover", map[string]any{
		"type": "email", "url": "name+ai-weekly@agentmail.to",
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("discover = %d, body: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["name"] != "Ai Weekly" {
		t.Fatalf("name = %q, want %q", body["name"], "Ai Weekly")
	}

	// An unusable address yields an empty name rather than an error, matching
	// the contract that discovery failure just means "type it yourself".
	w = makeRequest(s, http.MethodPost, "/api/feeds/discover", map[string]any{
		"type": "email", "url": "not-an-address",
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("discover with a bad address = %d, want 200 with an empty name", w.Code)
	}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["name"] != "" {
		t.Fatalf("name = %q, want empty", body["name"])
	}
}

// When the email type is not configured the API must refuse email feeds rather
// than storing one that can never be polled.
func TestCreateEmailFeedWhenUnconfigured(t *testing.T) {
	s, queries := setupTestServer(t) // no email credentials
	user := createTestUser(t, queries, "unconfigured@example.com", "pw12345678")
	token := getTestToken(t, s.auth, &user)

	w := makeRequest(s, http.MethodPost, "/api/feeds", map[string]any{
		"type": "email", "name": "Nope", "url": "name+sport@agentmail.to",
	}, token)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 when email is unconfigured; body: %s", w.Code, w.Body.String())
	}
}
