package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

// maxEmailInboxLength bounds a stored inbox address. Addresses are short; the
// value is echoed back by the settings dialog and carries the polled mailbox, so
// a paste accident stays bounded rather than reaching the API.
const maxEmailInboxLength = 320

// emailVerifyTimeout bounds the live inbox check on save. The user is waiting on
// this request, so it must fail rather than hang behind a slow API.
const emailVerifyTimeout = 15 * time.Second

// errEmailNoDB is returned when a multi-statement email-settings transaction is
// requested on a Queries not backed by *sql.DB (tests always use *sql.DB).
var errEmailNoDB = fmt.Errorf("email settings change requires a *sql.DB-backed Queries")

// emailSettingsResponse is the JSON shape of GET /api/settings/email.
//
// The API key is deliberately ABSENT — not empty-string, absent. A stored
// credential is never sent back to the browser; the dialog renders an empty
// password field and token_set tells it whether one exists, so "leave the token
// alone" and "there is no token" stay distinguishable without ever echoing the
// secret.
type emailSettingsResponse struct {
	Configured bool   `json:"configured"`
	Inbox      string `json:"inbox"`
	TokenSet   bool   `json:"token_set"`
}

// emailConfigFor reads a user's AgentMail configuration.
func (s *Server) emailConfigFor(ctx context.Context, userID int64) (inbox, key string, err error) {
	user, err := s.queries.GetUserByID(ctx, userID)
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(user.EmailInbox), strings.TrimSpace(user.EmailApiKey), nil
}

// callerEmailInbox returns the authenticated user's AgentMail inbox, or "" when
// the account has not configured the email feed type.
//
// Resolved per request rather than cached on the Server (which is what the
// previous instance-wide field did): this is per-user state that the settings
// dialog changes at runtime, so a cached copy would go stale mid-session.
func (s *Server) callerEmailInbox(ctx context.Context, userID int64) string {
	inbox, _, err := s.emailConfigFor(ctx, userID)
	if err != nil {
		return ""
	}
	return inbox
}

// SetEmailVerifyFunc substitutes the AgentMail credential check. Tests use it;
// nil restores the real API.
func (s *Server) SetEmailVerifyFunc(f func(ctx context.Context, inbox, apiKey string) error) {
	s.emailVerify = f
}

// verifyEmailCredentials checks a candidate inbox + token against AgentMail.
//
// A wrong inbox or token is otherwise invisible: the save succeeds, every poll
// returns nothing, and the only symptom is a permanently empty feed. AgentMail
// has no catch-all, so a typo receives no mail at all.
func (s *Server) verifyEmailCredentials(ctx context.Context, inbox, apiKey string) error {
	if s.emailVerify != nil {
		return s.emailVerify(ctx, inbox, apiKey)
	}
	client := feed.NewAgentMailClient(apiKey, inbox)
	if client == nil {
		return fmt.Errorf("an inbox and API key are both required")
	}
	return client.VerifyInbox(ctx)
}

// handleGetEmailSettings reports the caller's AgentMail configuration.
func (s *Server) handleGetEmailSettings(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)
	inbox, key, err := s.emailConfigFor(r.Context(), userID)
	if err != nil {
		writeServerError(w, "failed to load email settings", err)
		return
	}
	writeJSON(w, http.StatusOK, emailSettingsResponse{
		Configured: inbox != "" && key != "",
		Inbox:      inbox,
		TokenSet:   key != "",
	})
}

// handleUpdateEmailSettings stores the caller's AgentMail credentials.
//
// api_key is a POINTER so an omitted field keeps the stored token, which is what
// the dialog sends when the user edited only the inbox. An empty string CLEARS
// the stored token: the dialog shows a stored key as a fixed mask, so blanking
// the field is the deliberate way to remove it. Clearing the token is not the
// same as clearing the account — the inbox stays, and any feeds that then have
// no credentials go to status=error on the next poll. DELETE removes both.
func (s *Server) handleUpdateEmailSettings(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)

	var req struct {
		Inbox  string  `json:"inbox"`
		APIKey *string `json:"api_key"`
	}
	if !decodeBody(w, r, &req) {
		return
	}

	// existingKey is read to preserve a stored token when the request omits one;
	// the stored inbox is not needed (the request always carries the inbox it
	// wants, and an empty one is rejected below).
	_, existingKey, err := s.emailConfigFor(r.Context(), userID)
	if err != nil {
		writeServerError(w, "failed to load email settings", err)
		return
	}

	// The inbox is a full address and nothing more: the '+' suffix belongs to a
	// FEED, not to the mailbox. Accepting one here would silently make every
	// address composed from it wrong.
	newInbox := strings.TrimSpace(req.Inbox)
	if msg := validateEmailInbox(newInbox); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	// key is what gets stored. An omitted api_key keeps the existing token; an
	// explicit empty string clears it (the deliberate way to remove a stored key,
	// now that the field renders as a mask rather than an empty box).
	key := existingKey
	clearingKey := false
	if req.APIKey != nil {
		if provided := strings.TrimSpace(*req.APIKey); provided != "" {
			key = provided
		} else {
			key = ""
			clearingKey = true
		}
	}
	if key == "" && !clearingKey {
		writeError(w, http.StatusBadRequest, "an API key is required")
		return
	}

	// One mailbox, one account. email_messages is staged per inbox and a feed
	// reads the rows addressed to its own plus-address, so two accounts on one
	// mailbox would each read the other's subscriptions.
	if owner, oerr := s.queries.GetUserByEmailInbox(r.Context(), newInbox); oerr == nil && owner.ID != userID {
		writeError(w, http.StatusConflict, "this inbox is already configured by another account")
		return
	}

	// Verify before storing: a wrong inbox or token is otherwise invisible until
	// someone notices a feed that never receives mail. Skipped when the key is
	// being cleared — there is nothing to verify, and the request is explicitly
	// giving up the credentials.
	if !clearingKey {
		ctx, cancel := context.WithTimeout(r.Context(), emailVerifyTimeout)
		defer cancel()
		if err := s.verifyEmailCredentials(ctx, newInbox, key); err != nil {
			writeError(w, http.StatusBadRequest, "AgentMail rejected these credentials: "+err.Error())
			return
		}
	}

	if _, err := s.queries.UpdateUserEmailConfig(r.Context(), store.UpdateUserEmailConfigParams{
		EmailInbox:  newInbox,
		EmailApiKey: key,
		ID:          userID,
	}); err != nil {
		writeServerError(w, "failed to save email settings", err)
		return
	}

	writeJSON(w, http.StatusOK, emailSettingsResponse{
		Configured: key != "",
		Inbox:      newInbox,
		TokenSet:   key != "",
	})
}

// handleResetEmailSettings clears the caller's AgentMail configuration.
//
// Email feeds owned by this account are deliberately LEFT IN PLACE. They become
// `status = 'error'` with a user-facing last_error on the next poll, which is
// where the sidebar shows the failure — the user deletes them explicitly rather
// than having feeds silently disappear with their credentials. The alternative
// (refusing to clear while feeds exist) would trap the user behind a delete
// dialog they may not want.
func (s *Server) handleResetEmailSettings(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)

	// Resolve the inbox first: clearing the config orphans the staged mail
	// and sync watermark, so they must be deleted in the same transaction.
	inbox, _, err := s.emailConfigFor(r.Context(), userID)
	if err != nil {
		writeServerError(w, "failed to load email settings", err)
		return
	}
	db := s.queries.DB()
	if db == nil {
		writeServerError(w, "failed to clear email settings", errEmailNoDB)
		return
	}
	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		writeServerError(w, "failed to clear email settings", err)
		return
	}
	defer tx.Rollback()
	qtx := s.queries.WithTx(tx)
	if err := qtx.ClearUserEmailConfig(r.Context(), userID); err != nil {
		writeServerError(w, "failed to clear email settings", err)
		return
	}
	if inbox != "" {
		if _, err := qtx.DeleteEmailMessagesForInbox(r.Context(), inbox); err != nil {
			writeServerError(w, "failed to clear staged mail", err)
			return
		}
		if _, err := qtx.DeleteEmailSyncStateForInbox(r.Context(), inbox); err != nil {
			writeServerError(w, "failed to clear sync state", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeServerError(w, "failed to clear email settings", err)
		return
	}
	writeJSON(w, http.StatusOK, emailSettingsResponse{Configured: false, Inbox: "", TokenSet: false})
}

// validateEmailInbox returns a user-facing message when inbox is not a bare
// mailbox address, or "" when it is acceptable.
func validateEmailInbox(inbox string) string {
	if inbox == "" {
		return "an inbox address is required"
	}
	if len(inbox) > maxEmailInboxLength {
		return "the inbox address is too long"
	}
	local, domain, found := strings.Cut(inbox, "@")
	if !found || local == "" || domain == "" {
		return "the inbox must be an email address like name@agentmail.to"
	}
	if strings.ContainsAny(inbox, " \t") {
		return "the inbox must not contain spaces"
	}
	if strings.Contains(inbox, "+") {
		return "the inbox is the mailbox itself, without a '+suffix': the suffix belongs to each feed"
	}
	if strings.ContainsAny(local, "@") || strings.ContainsAny(domain, "@") {
		return "the inbox must be an email address like name@agentmail.to"
	}
	return ""
}
