package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"digesterr/internal/auth"
	"digesterr/internal/config"
	"digesterr/internal/logbuf"
	"digesterr/internal/store"
)

// emailSettingsTestServer returns a server with the verify stub installed and an
// account that has NO email configuration yet — the state the settings dialog
// opens in for a fresh account.
func emailSettingsTestServer(t *testing.T) (*Server, *store.Queries, store.User, string) {
	t.Helper()
	db, queries := setupTestDB(t)
	authSvc := auth.NewService("test-jwt-secret-for-testing", queries, db)
	s := NewServer(db, queries, authSvc, logbuf.New(100), config.Default())
	s.SetEmailVerifyFunc(acceptInbox("fresh@agentmail.to"))
	user := createTestUser(t, queries, "settings@example.com", "pw12345678")
	return s, queries, user, getTestToken(t, s.auth, &user)
}

func decodeEmailSettings(t *testing.T, body []byte) emailSettingsResponse {
	t.Helper()
	var resp emailSettingsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode settings: %v (body %s)", err, body)
	}
	return resp
}

// A fresh account reports unconfigured, and the response carries no credential:
// token_set says one exists, the token itself is never sent.
func TestEmailSettingsDefaultsToUnconfigured(t *testing.T) {
	s, _, _, token := emailSettingsTestServer(t)

	w := makeRequest(s, http.MethodGet, "/api/settings/email", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d, body: %s", w.Code, w.Body.String())
	}
	resp := decodeEmailSettings(t, w.Body.Bytes())
	if resp.Configured || resp.TokenSet || resp.Inbox != "" {
		t.Errorf("fresh account settings = %+v, want unconfigured with no inbox", resp)
	}
}

// Saving stores both values, and the API key is NEVER echoed back — not as an
// empty string, not at all. A credential that round-trips to the browser is a
// credential in the browser's memory and in every proxy log along the way.
func TestEmailSettingsSaveDoesNotEchoTheToken(t *testing.T) {
	s, queries, user, token := emailSettingsTestServer(t)

	w := makeRequest(s, http.MethodPut, "/api/settings/email", map[string]any{
		"inbox": "fresh@agentmail.to", "api_key": testAgentMailToken,
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d, body: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); strings.Contains(body, testAgentMailToken) {
		t.Fatalf("the response body contains the API key: %s", body)
	}
	resp := decodeEmailSettings(t, w.Body.Bytes())
	if !resp.Configured || !resp.TokenSet || resp.Inbox != "fresh@agentmail.to" {
		t.Errorf("after save = %+v, want configured with the inbox", resp)
	}

	// Stored on the user's row, and a subsequent GET still withholds the key.
	stored, err := queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if stored.EmailInbox != "fresh@agentmail.to" || stored.EmailApiKey != testAgentMailToken {
		t.Errorf("stored config = %q/%q, want the saved values", stored.EmailInbox, stored.EmailApiKey)
	}

	w = makeRequest(s, http.MethodGet, "/api/settings/email", nil, token)
	if body := w.Body.String(); strings.Contains(body, testAgentMailToken) {
		t.Fatalf("GET leaked the API key: %s", body)
	}
}

// Omitting api_key means "keep the stored one". This is the case a naive
// "empty means clear" implementation gets wrong: the dialog sends only the inbox
// when the user changed the address and left the password field alone, and
// clearing the token would silently disable the feed type.
func TestEmailSettingsOmittedTokenIsPreserved(t *testing.T) {
	s, queries, user, token := emailSettingsTestServer(t)

	w := makeRequest(s, http.MethodPut, "/api/settings/email", map[string]any{
		"inbox": "fresh@agentmail.to", "api_key": testAgentMailToken,
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("first PUT = %d, body: %s", w.Code, w.Body.String())
	}

	// Second save: inbox only, no api_key field at all.
	w = makeRequest(s, http.MethodPut, "/api/settings/email", map[string]any{
		"inbox": "fresh@agentmail.to",
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("inbox-only PUT = %d, body: %s", w.Code, w.Body.String())
	}

	stored, err := queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if stored.EmailApiKey != testAgentMailToken {
		t.Errorf("stored token = %q, want it preserved when the request omits api_key "+
			"(a save that only changes the inbox must not clear the credential)", stored.EmailApiKey)
	}
}

// An explicitly empty api_key CLEARS the stored token, keeping the inbox. The
// dialog shows a stored key as a mask, so blanking the field is the deliberate
// way to remove it. A wrong key must not be silently stored either, so the
// response reports the credential as gone.
//
// The verify stub fails the test if it is called: there is nothing to verify when
// the key is being cleared, and calling the live check would reject the request.
func TestEmailSettingsClearsTokenOnEmptyKey(t *testing.T) {
	s, queries, user, token := emailSettingsTestServer(t)
	s.SetEmailVerifyFunc(acceptInboxAny())

	w := makeRequest(s, http.MethodPut, "/api/settings/email", map[string]any{
		"inbox": "fresh@agentmail.to", "api_key": testAgentMailToken,
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("seed PUT = %d", w.Code)
	}

	// Installed only now: the seed request is legitimately verified, the clearing
	// request must not be.
	s.SetEmailVerifyFunc(func(context.Context, string, string) error {
		t.Error("verify called while clearing the key; there is nothing to verify")
		return nil
	})

	w = makeRequest(s, http.MethodPut, "/api/settings/email", map[string]any{
		"inbox": "fresh@agentmail.to", "api_key": "",
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("empty api_key = %d, want 200 (an empty field clears the stored key)", w.Code)
	}

	var res emailSettingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.TokenSet || res.Configured {
		t.Errorf("response = %+v, want token_set and configured false after clearing", res)
	}

	stored, err := queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if stored.EmailApiKey != "" {
		t.Errorf("stored token = %q, want it cleared", stored.EmailApiKey)
	}
	if stored.EmailInbox != "fresh@agentmail.to" {
		t.Errorf("stored inbox = %q, want it KEPT when only the key is cleared", stored.EmailInbox)
	}
}

// The inbox is the mailbox, not a feed address. A '+' suffix belongs to a FEED,
// and accepting one here would make every address composed from it wrong.
//
// The verify stub accepts ANY inbox here on purpose: with the stub's normal
// behaviour the malformed values would be rejected by the live check instead, and
// the test would pass without validateEmailInbox doing anything — it would be
// asserting the stub, not the validation.
func TestEmailSettingsRejectsInboxWithSuffix(t *testing.T) {
	s, _, _, token := emailSettingsTestServer(t)
	s.SetEmailVerifyFunc(acceptInboxAny())

	cases := []struct{ name, inbox string }{
		{"plus suffix", "fresh+news@agentmail.to"},
		{"no at sign", "fresh.agentmail.to"},
		{"empty local part", "@agentmail.to"},
		{"empty domain", "fresh@"},
		{"internal space", "fre sh@agentmail.to"},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := makeRequest(s, http.MethodPut, "/api/settings/email", map[string]any{
				"inbox": tc.inbox, "api_key": testAgentMailToken,
			}, token)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("inbox %q = %d, want 400 (body: %s)", tc.inbox, w.Code, w.Body.String())
			}
		})
	}
}

// A token AgentMail rejects must not be stored: saving it would look successful
// and produce a feed that never receives anything.
func TestEmailSettingsRejectsCredentialsAgentMailRefuses(t *testing.T) {
	s, queries, user, token := emailSettingsTestServer(t)
	s.SetEmailVerifyFunc(rejectInbox())

	w := makeRequest(s, http.MethodPut, "/api/settings/email", map[string]any{
		"inbox": "fresh@agentmail.to", "api_key": testAgentMailToken,
	}, token)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PUT with rejected credentials = %d, want 400 (body: %s)", w.Code, w.Body.String())
	}

	stored, err := queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if stored.EmailInbox != "" || stored.EmailApiKey != "" {
		t.Errorf("stored config = %q/%q, want nothing stored when verification fails",
			stored.EmailInbox, stored.EmailApiKey)
	}
}

// Clearing removes the credentials and leaves the feeds alone. The feeds become
// status=error on the next poll, which is where the sidebar shows the failure —
// the alternative (refusing to clear) traps the user behind a delete dialog.
func TestEmailSettingsResetClearsAndLeavesFeeds(t *testing.T) {
	s, queries, user, token := emailSettingsTestServer(t)

	w := makeRequest(s, http.MethodPut, "/api/settings/email", map[string]any{
		"inbox": "fresh@agentmail.to", "api_key": testAgentMailToken,
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("seed PUT = %d", w.Code)
	}
	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "email", Name: "News", Url: "fresh+news@agentmail.to",
		Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	w = makeRequest(s, http.MethodDelete, "/api/settings/email", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE = %d, want 200 even with email feeds present (body: %s)", w.Code, w.Body.String())
	}
	resp := decodeEmailSettings(t, w.Body.Bytes())
	if resp.Configured || resp.TokenSet || resp.Inbox != "" {
		t.Errorf("after DELETE = %+v, want unconfigured", resp)
	}

	stored, err := queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if stored.EmailInbox != "" || stored.EmailApiKey != "" {
		t.Errorf("stored config = %q/%q, want cleared", stored.EmailInbox, stored.EmailApiKey)
	}

	// The feed must survive: its owner's missing credentials are a runtime
	// failure, not a reason to delete the user's subscription.
	if _, err := queries.GetFeed(t.Context(), store.GetFeedParams{ID: feed.ID, UserID: user.ID}); err != nil {
		t.Errorf("the email feed was removed by DELETE: %v", err)
	}
}

// Two accounts keep separate configurations, and one cannot reach the other's.
func TestEmailSettingsAreScopedToTheCaller(t *testing.T) {
	s, queries, _, aliceToken := emailSettingsTestServer(t)

	w := makeRequest(s, http.MethodPut, "/api/settings/email", map[string]any{
		"inbox": "fresh@agentmail.to", "api_key": testAgentMailToken,
	}, aliceToken)
	if w.Code != http.StatusOK {
		t.Fatalf("alice PUT = %d, body: %s", w.Code, w.Body.String())
	}

	bob := createTestUser(t, queries, "bob@example.com", "pw12345678")
	bobToken := getTestToken(t, s.auth, &bob)

	// Bob's own view is untouched by alice's save.
	w = makeRequest(s, http.MethodGet, "/api/settings/email", nil, bobToken)
	bobResp := decodeEmailSettings(t, w.Body.Bytes())
	if bobResp.Configured || bobResp.Inbox != "" {
		t.Errorf("bob sees %+v, want his own empty configuration", bobResp)
	}

	// Bob can use a different inbox.
	s.SetEmailVerifyFunc(acceptInboxAny())
	w = makeRequest(s, http.MethodPut, "/api/settings/email", map[string]any{
		"inbox": "bob-inbox@agentmail.to", "api_key": testAgentMailToken,
	}, bobToken)
	if w.Code != http.StatusOK {
		t.Fatalf("bob PUT = %d, body: %s", w.Code, w.Body.String())
	}

	// Alice's row is untouched.
	w = makeRequest(s, http.MethodGet, "/api/settings/email", nil, aliceToken)
	aliceResp := decodeEmailSettings(t, w.Body.Bytes())
	if aliceResp.Inbox != "fresh@agentmail.to" {
		t.Errorf("alice's inbox = %q, want her own value after bob's save", aliceResp.Inbox)
	}
}

// Every endpoint is authenticated: a configuration holding an API key must not be
// readable or writable anonymously.
func TestEmailSettingsRequireAuth(t *testing.T) {
	s, _, _, _ := emailSettingsTestServer(t)

	for _, tc := range []struct {
		method string
		body   map[string]any
	}{
		{http.MethodGet, nil},
		{http.MethodPut, map[string]any{"inbox": "x@agentmail.to", "api_key": testAgentMailToken}},
		{http.MethodDelete, nil},
	} {
		w := makeRequest(s, tc.method, "/api/settings/email", tc.body, "")
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s /api/settings/email without a token = %d, want 401", tc.method, w.Code)
		}
	}
}
