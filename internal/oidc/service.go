// Package oidc implements Digesterr's native OpenID Connect sign-in: a
// server-side authorization-code flow with PKCE, single-use state bound to the
// initiating browser, nonce and ID token validation through a maintained
// library (go-oidc), and account resolution keyed on (issuer, subject).
//
// The feature is optional and additive. Local signup, password sign-in and
// Reader API keys keep working unchanged; enabling OIDC does not disable any of
// them.
package oidc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"digesterr/internal/store"
)

var (
	// ErrEmailNotVerified is returned when the ID token's email has not been
	// verified by the provider. Such an address proves nothing about who
	// controls it, so it can neither provision an account nor match one.
	ErrEmailNotVerified = errors.New("oidc: provider has not verified the email address")
	// ErrEmailMissing is returned when the ID token carries no email. The
	// account model is email-keyed, so a user without one cannot be shown in
	// the UI or identified in logs.
	ErrEmailMissing = errors.New("oidc: ID token carries no email address")
	// ErrEmailAmbiguous is returned when a first login's verified email matches
	// more than one local account under case-insensitive comparison. Choosing
	// one would be a guess that hands over an account, so the login fails and
	// the operator resolves it.
	ErrEmailAmbiguous = errors.New("oidc: multiple local accounts match the verified email")
	// ErrSubjectMissing is returned when the ID token carries no subject. A
	// subject is the only stable identity the provider gives us.
	ErrSubjectMissing = errors.New("oidc: ID token carries no subject")
)

// Identity is the subset of a validated ID token this package acts on. It is
// produced only after signature, issuer, audience, expiry and nonce checks have
// passed, so every field here is provider-asserted.
type Identity struct {
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
}

// APIKeyGenerator mints Reader API keys. Injected so a test can produce a
// deterministic key; production passes auth.GenerateAPIKey.
type APIKeyGenerator func() (string, error)

// Service owns account resolution. The HTTP layer owns the browser-facing
// protocol (state, PKCE, cookies) and calls in here once it holds a validated
// identity.
type Service struct {
	queries   *store.Queries
	db        *sql.DB
	issuer    string
	newAPIKey APIKeyGenerator
}

// NewService builds the resolution service for the given issuer. issuer must
// be the exact configured issuer string, because it is stored in
// user_identities and compared literally.
func NewService(db *sql.DB, queries *store.Queries, issuer string, newAPIKey APIKeyGenerator) *Service {
	return &Service{queries: queries, db: db, issuer: issuer, newAPIKey: newAPIKey}
}

// Resolve maps a validated identity to a local user, creating or linking one
// when necessary. It reports whether a user was provisioned so the caller can
// log first-login provisioning distinctly from a returning login.
//
// Order of resolution, and why it is this order:
//
//  1. (issuer, subject) already mapped -> that user. Always wins. Email is
//     mutable at the provider, so an address change must not move a login to
//     another account.
//  2. verified email matches exactly one local account -> link it. This is the
//     migration path for existing password accounts.
//  3. otherwise -> provision a new passwordless account with a fresh Reader
//     API key.
//
// An unverified email is refused outright, including for an already-mapped
// identity: the requirement is that email_verified is true on every login, not
// only on first login.
// resolveAttempts bounds how many times a transient write lock is retried. Each
// attempt is a fresh transaction, and the backoff below is short enough that a
// user never sees it next to a redirect.
const resolveAttempts = 5

// busyRetryBase is the first backoff; it doubles per attempt, so five attempts
// wait ~375ms in total. That is deliberately under the threshold where a user
// notices a pause on a redirect, and well inside SQLITE's own 5s busy_timeout:
// this covers the snapshot-class lock that busy_timeout does NOT cover.
const busyRetryBase = 25 * time.Millisecond

// Resolve maps a validated identity to a local user, retrying when the database
// reports a transient write lock.
//
// The transaction is read-then-write, so it needs the writer lock before it can
// commit. Another process sharing the SQLite file (a backup, sqlite3 on the
// volume, a second replica) can hold that lock for a moment, and the driver
// surfaces that as "database is locked" — sometimes the snapshot variant
// (SQLITE_BUSY_SNAPSHOT, code 517), which does NOT honour busy_timeout. Treating
// it as fatal turned a short-lived condition into a failed login the user saw as
// /login?error=oidc_error, so it is retried here instead.
//
// The retry is bounded and re-runs the whole resolution: an attempt that failed
// at commit wrote nothing, and re-reading the identity row means the second
// attempt takes the "already mapped" path if the first one actually landed.
// internal/oidc/busy_retry_test.go pins both the retry and the give-up.
func (s *Service) Resolve(ctx context.Context, id Identity) (*store.User, bool, error) {
	var lastErr error
	for attempt := 0; attempt < resolveAttempts; attempt++ {
		user, provisioned, err := s.resolveOnce(ctx, id)
		if err == nil {
			return user, provisioned, nil
		}
		if !isTransientLock(err) {
			return nil, false, err
		}
		lastErr = err
		if attempt == resolveAttempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-time.After(busyRetryBase << attempt):
		}
	}
	return nil, false, fmt.Errorf("oidc: database stayed locked after %d attempts: %w", resolveAttempts, lastErr)
}

// isTransientLock reports whether err is SQLite telling us to try again later.
//
// Both the generic busy result and the snapshot variant are matched, by text:
// the driver's error type is internal to modernc.org/sqlite and matching on it
// would couple this package to that implementation.
func isTransientLock(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "database table is locked")
}

// resolveOnce is one attempt: it either resolves the identity or fails.
func (s *Service) resolveOnce(ctx context.Context, id Identity) (*store.User, bool, error) {
	if strings.TrimSpace(id.Subject) == "" {
		return nil, false, ErrSubjectMissing
	}
	if strings.TrimSpace(id.Email) == "" {
		return nil, false, ErrEmailMissing
	}
	if !id.EmailVerified {
		return nil, false, ErrEmailNotVerified
	}
	// The issuer is fixed by configuration, and go-oidc has already checked the
	// token's iss against it. Defend the invariant here too: identity rows are
	// keyed on the issuer string, so accepting a caller-supplied one would make
	// the key attacker-influenced.
	if id.Issuer != "" && id.Issuer != s.issuer {
		return nil, false, fmt.Errorf("oidc: issuer %q does not match configured issuer", id.Issuer)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	qtx := s.queries.WithTx(tx)

	if ident, err := qtx.GetUserIdentityByIssuerSubject(ctx, store.GetUserIdentityByIssuerSubjectParams{
		Issuer: s.issuer, Subject: id.Subject,
	}); err == nil {
		user, err := qtx.GetUserByID(ctx, ident.UserID)
		if err != nil {
			return nil, false, fmt.Errorf("oidc: mapped user %d is missing: %w", ident.UserID, err)
		}
		// Keep the recorded address current for audit, but never re-key on it.
		if ident.Email != id.Email {
			if err := qtx.UpdateUserIdentityEmail(ctx, store.UpdateUserIdentityEmailParams{
				Email: id.Email, ID: ident.ID,
			}); err != nil {
				return nil, false, err
			}
		}
		if err := tx.Commit(); err != nil {
			return nil, false, err
		}
		return &user, false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}

	match, err := s.matchUniqueEmail(ctx, qtx, id.Email)
	if err != nil {
		return nil, false, err
	}
	if match != nil {
		if _, err := qtx.CreateUserIdentity(ctx, store.CreateUserIdentityParams{
			UserID: match.ID, Issuer: s.issuer, Subject: id.Subject, Email: id.Email,
		}); err != nil {
			return nil, false, err
		}
		if err := tx.Commit(); err != nil {
			return nil, false, err
		}
		return match, false, nil
	}

	key, err := s.newAPIKey()
	if err != nil {
		return nil, false, fmt.Errorf("oidc: generating reader api key: %w", err)
	}
	user, err := qtx.CreateOIDCUserNoPassword(ctx, store.CreateOIDCUserNoPasswordParams{
		Email:  id.Email,
		ApiKey: key,
	})
	if err != nil {
		return nil, false, fmt.Errorf("oidc: provisioning user: %w", err)
	}
	if _, err := qtx.CreateUserIdentity(ctx, store.CreateUserIdentityParams{
		UserID: user.ID, Issuer: s.issuer, Subject: id.Subject, Email: id.Email,
	}); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return &user, true, nil
}

// matchUniqueEmail finds the single account whose email equals want after
// trimming surrounding whitespace and lowercasing, and returns (nil, nil) when
// no account matches and ErrEmailAmbiguous when more than one does.
//
// The comparison happens in Go rather than SQL because SQLite's LOWER() folds
// ASCII only: "İ" (U+0130) lowercases to "i" in Go and is left untouched by
// LOWER(), so a query-side match would consider a different set of candidates
// than the application does.
//
// What this is NOT: full Unicode case folding. strings.ToLower is simple
// lowercasing, so "ß" does not match "ss", "ς" does not match "σ" and the
// fullwidth "Ａ" does not match "a". That difference only ever produces a
// MISSED link — a provider identity whose address differs from a local account
// by one of those folds provisions a new account instead of linking the
// existing one — never a wrong link, because two addresses that compare equal
// here are genuinely the same string case-insensitively. If stronger folding is
// ever wanted, golang.org/x/text/cases with fold would be the tool, and the
// ambiguity branch would need re-examining.
func (s *Service) matchUniqueEmail(ctx context.Context, q *store.Queries, want string) (*store.User, error) {
	rows, err := q.ListAllUsersEmails(ctx)
	if err != nil {
		return nil, err
	}
	folded := foldEmail(want)
	var ids []int64
	for _, r := range rows {
		if foldEmail(r.Email) == folded {
			ids = append(ids, r.ID)
		}
	}
	switch len(ids) {
	case 0:
		return nil, nil
	case 1:
		user, err := q.GetUserByID(ctx, ids[0])
		if err != nil {
			return nil, err
		}
		return &user, nil
	default:
		// Note the count, not the addresses: this message can reach a browser,
		// and an attacker supplying an address should not learn how many
		// accounts exist beyond the one they are probing.
		return nil, fmt.Errorf("%w (%d accounts)", ErrEmailAmbiguous, len(ids))
	}
}

// foldEmail normalises an address for comparison: trimmed, then lowercased.
// strings.ToLower is Unicode-aware (unlike SQLite's LOWER, which is ASCII-only)
// but it is simple lowercasing, not case folding — see matchUniqueEmail.
func foldEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// Issuer returns the configured issuer string.
func (s *Service) Issuer() string { return s.issuer }
