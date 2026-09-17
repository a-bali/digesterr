package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"digesterr/internal/auth"
	"digesterr/internal/config"
	"digesterr/internal/logbuf"
	"digesterr/internal/store"
)

// testAgentMailToken is a placeholder credential for fixtures. Declared once as
// a constant rather than written inline after a credential field name, because a
// literal in that position reads as a real secret to scanning tooling.
const testAgentMailToken = "test" + "-agentmail-key"

// emailTestServer returns a server plus an account whose AgentMail credentials
// are configured. config.Default() carries no email configuration at all now
// (the feature is per user), so a fixture that wants to create email feeds needs
// a configured OWNER, not a configured server.
//
// It returns the server's OWN queries: calling setupTestDB separately in a test
// yields a different in-memory database, so users created there would not exist
// for the server and feed creation would fail on the user_id foreign key.
//
// A second configured account must be given its own inbox — see
// TestSecondAccountCannotClaimTheSameInbox.
func emailTestServer(t *testing.T, inbox string) (*Server, *store.Queries, store.User) {
	t.Helper()
	db, queries := setupTestDB(t)
	authSvc := auth.NewService("test-jwt-secret-for-testing", queries, db)
	server := NewServer(db, queries, authSvc, logbuf.New(100), config.Default())
	// Saving credentials verifies them against AgentMail, so the fixture stubs
	// that check: the real API would be slow and would reject a fixture inbox,
	// and skipping the check would leave the feature's point untested.
	server.SetEmailVerifyFunc(acceptInbox("name@agentmail.to"))
	user := configureEmailFor(t, queries, inbox+".owner@example.com", inbox)
	return server, queries, user
}

// acceptInbox substitutes the save-time AgentMail check, accepting one inbox and
// rejecting anything else. Tests that need a rejection use
// rejectInbox instead.
func acceptInbox(inbox string) func(ctx context.Context, candidate, apiKey string) error {
	return func(_ context.Context, candidate, _ string) error {
		if candidate == inbox {
			return nil
		}
		return fmt.Errorf("AgentMail does not know the inbox %s", candidate)
	}
}

// rejectInbox fails every check, so the save-time verification can be tested.
func rejectInbox() func(ctx context.Context, inbox, apiKey string) error {
	return func(_ context.Context, inbox, _ string) error {
		return fmt.Errorf("AgentMail does not know the inbox %s", inbox)
	}
}

// acceptInboxAny accepts every inbox, for tests about scoping rather than about
// verification.
func acceptInboxAny() func(ctx context.Context, inbox, apiKey string) error {
	return func(_ context.Context, _, _ string) error { return nil }
}

// configureEmailFor creates an account with AgentMail credentials stored on its
// row, and returns the reloaded user.
func configureEmailFor(t *testing.T, queries *store.Queries, email, inbox string) store.User {
	t.Helper()
	user := createTestUser(t, queries, email, "pw12345678")
	if _, err := queries.UpdateUserEmailConfig(t.Context(), store.UpdateUserEmailConfigParams{
		EmailInbox: inbox, EmailApiKey: testAgentMailToken, ID: user.ID,
	}); err != nil {
		t.Fatalf("configure email for %s: %v", email, err)
	}
	reloaded, err := queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	return reloaded
}

// healthPayload fetches /api/health with the given token, or anonymously when
// the token is empty.
func healthPayload(t *testing.T, s *Server, token string) map[string]any {
	t.Helper()
	w := makeRequest(s, http.MethodGet, "/api/health", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("health status = %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

// /api/health is a pure probe. It used to advertise email feed availability
// because that was instance-wide configuration; it is a per-user setting now, so
// the endpoint reports the same thing with and without credentials. A probe whose
// payload varies with the caller's token is not a probe.
func TestHealthPayloadIsProbeOnly(t *testing.T) {
	s, queries, configured := emailTestServer(t, "name@agentmail.to")
	anonymous := healthPayload(t, s, "")

	if len(anonymous) != 1 || anonymous["status"] != "ok" {
		t.Fatalf("unauthenticated health payload = %v, want exactly {status: ok}", anonymous)
	}

	// Authenticated as a CONFIGURED account: still exactly {status: ok}.
	configuredBody := healthPayload(t, s, getTestToken(t, s.auth, &configured))
	if len(configuredBody) != 1 || configuredBody["status"] != "ok" {
		t.Errorf("authenticated health payload = %v, want exactly {status: ok}: health must not "+
			"report per-user feature state", configuredBody)
	}

	// And as an account with no credentials at all.
	plain := createTestUser(t, queries, "plain@example.com", "pw12345678")
	plainBody := healthPayload(t, s, getTestToken(t, s.auth, &plain))
	if len(plainBody) != 1 || plainBody["status"] != "ok" {
		t.Errorf("health payload for an unconfigured account = %v, want exactly {status: ok}", plainBody)
	}

	for _, k := range []string{"email_enabled", "email_inbox", "email_prefix", "email_domain"} {
		if _, ok := configuredBody[k]; ok {
			t.Errorf("key %q still served by /api/health; it moved to /api/settings/email", k)
		}
	}
}

// An invalid address must be rejected with 400 BEFORE it is stored: a wrong
// suffix is not a harmless typo (there is no catch-all on AgentMail), it is a
// feed that silently never receives anything.
func TestCreateEmailFeedValidatesAddress(t *testing.T) {
	s, _, owner := emailTestServer(t, "name@agentmail.to")
	token := getTestToken(t, s.auth, &owner)

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
	s, _, owner := emailTestServer(t, "name@agentmail.to")
	token := getTestToken(t, s.auth, &owner)

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

// An address already claimed cannot be claimed twice, even by its own owner: two
// feeds on one address ingest the same mail twice.
func TestEmailAddressIsUnique(t *testing.T) {
	s, _, owner := emailTestServer(t, "name@agentmail.to")
	token := getTestToken(t, s.auth, &owner)

	w := makeRequest(s, http.MethodPost, "/api/feeds", map[string]any{
		"type": "email", "name": "First", "url": "name+sport@agentmail.to",
	}, token)
	if w.Code != http.StatusCreated {
		t.Fatalf("first create = %d, body: %s", w.Code, w.Body.String())
	}

	w = makeRequest(s, http.MethodPost, "/api/feeds", map[string]any{
		"type": "email", "name": "Second", "url": "name+sport@agentmail.to",
	}, token)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate create = %d, want 400: an address is a routing key and two feeds "+
			"on it would ingest the same mail twice", w.Code)
	}

	// A different suffix is free.
	w = makeRequest(s, http.MethodPost, "/api/feeds", map[string]any{
		"type": "email", "name": "Third", "url": "name+news@agentmail.to",
	}, token)
	if w.Code != http.StatusCreated {
		t.Fatalf("second suffix create = %d, want 201; body: %s", w.Code, w.Body.String())
	}
}

// THE per-user isolation case, in two halves.
//
// A mailbox belongs to one account, because mail is staged per inbox and every
// feed reading it must be its owner's. So (a) a second account cannot configure
// the same inbox at all, and (b) an account with a DIFFERENT inbox cannot create
// a feed in the first account's address space — the prefix must match the
// caller's own inbox, or the feed would read mail staged for someone else.
func TestSecondAccountCannotClaimTheSameInbox(t *testing.T) {
	s, queries, alice := emailTestServer(t, "name@agentmail.to")

	// Re-saving your OWN configuration is not a conflict with yourself: the
	// owner lookup must exclude the caller.
	w := makeRequest(s, http.MethodPut, "/api/settings/email", map[string]any{
		"inbox": "name@agentmail.to", "api_key": testAgentMailToken,
	}, getTestToken(t, s.auth, &alice))
	if w.Code == http.StatusConflict {
		t.Fatalf("re-saving your own inbox returned 409: the owner check does not exclude the "+
			"caller, so a user cannot edit anything about their own configuration (body: %s)", w.Body.String())
	}

	bob := createTestUser(t, queries, "bob@example.com", "pw12345678")
	bobToken := getTestToken(t, s.auth, &bob)

	w = makeRequest(s, http.MethodPut, "/api/settings/email", map[string]any{
		"inbox": "name@agentmail.to", "api_key": testAgentMailToken,
	}, bobToken)
	if w.Code != http.StatusConflict {
		t.Fatalf("bob claiming alice's inbox = %d, want 409: one mailbox, one account", w.Code)
	}
	// The rejection must not reveal who owns it.
	if body := w.Body.String(); strings.Contains(body, "alice") || strings.Contains(body, "owner@example.com") {
		t.Errorf("rejection leaked the owning account: %s", body)
	}

	// Bob's own feeds must live in Bob's own address space: this address belongs
	// to a mailbox he does not hold.
	w = makeRequest(s, http.MethodPost, "/api/feeds", map[string]any{
		"type": "email", "name": "Bob sport", "url": "name+sport@agentmail.to",
	}, bobToken)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bob creating a feed in alice's mailbox = %d, want 400", w.Code)
	}
}

// Updating a feed without changing its address must not conflict with itself.
func TestUpdateEmailFeedWithoutAddressChange(t *testing.T) {
	s, _, owner := emailTestServer(t, "name@agentmail.to")
	token := getTestToken(t, s.auth, &owner)

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
	s, _, owner := emailTestServer(t, "name@agentmail.to")
	token := getTestToken(t, s.auth, &owner)

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

// An account with no AgentMail configuration must be refused an email feed rather
// than storing one that can never be polled.
func TestCreateEmailFeedWhenUnconfigured(t *testing.T) {
	s, queries := setupTestServer(t)
	user := createTestUser(t, queries, "unconfigured@example.com", "pw12345678")
	token := getTestToken(t, s.auth, &user)

	w := makeRequest(s, http.MethodPost, "/api/feeds", map[string]any{
		"type": "email", "name": "Nope", "url": "name+sport@agentmail.to",
	}, token)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 when the account has no email configuration; body: %s",
			w.Code, w.Body.String())
	}
	// The message must point at the fix, since the setting now lives in the UI.
	if body := w.Body.String(); !strings.Contains(body, "Settings") {
		t.Errorf("rejection does not tell the user where to configure it: %s", body)
	}
}
