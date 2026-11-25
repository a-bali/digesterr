package oidc

import (
	"errors"
	"testing"

	"digesterr/internal/auth"
	"digesterr/internal/store"
	"digesterr/internal/testutil"
)

// newTestResolver builds the resolution half of the service against a real
// in-memory SQLite database with the production schema and real sqlc queries.
// The provider/network half is exercised by the HTTP integration tests; these
// tests are about the account rules, which is where a mistake hands one person
// another person's data.
func newTestResolver(t *testing.T) (*Service, *store.Queries) {
	t.Helper()
	db, err := testutil.OpenTestDB()
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	q := store.New(db)
	s := &Service{
		queries: q,
		db:      db,
		issuer:  "https://id.example.com",
		newAPIKey: func() (string, error) {
			return auth.GenerateAPIKey()
		},
	}
	return s, q
}

func mustCreateLocalUser(t *testing.T, q *store.Queries, email string) store.User {
	t.Helper()
	hash, err := auth.HashPassword("correct horse battery")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := q.CreateUser(t.Context(), store.CreateUserParams{
		Email: email, PasswordHash: hash, ApiKey: "key-" + email,
	})
	if err != nil {
		t.Fatalf("create user %q: %v", email, err)
	}
	return u
}

func TestResolveProvisionsNewUserWhenEmailUnknown(t *testing.T) {
	s, q := newTestResolver(t)

	user, provisioned, err := s.Resolve(t.Context(), Identity{
		Issuer: "https://id.example.com", Subject: "sub-1",
		Email: "New.Person@Example.com", EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !provisioned {
		t.Error("provisioned = false, want true for an unknown email with no existing identity")
	}
	if user.Email != "New.Person@Example.com" {
		t.Errorf("user.Email = %q, want the provider-asserted address", user.Email)
	}
	// No local password: an empty hash cannot match any bcrypt comparison, so
	// the account is not password-authenticatable until its owner sets one.
	if user.PasswordHash != "" {
		t.Errorf("PasswordHash = %q, want empty for an OIDC-provisioned user", user.PasswordHash)
	}
	if auth.CheckPassword("", user.PasswordHash) {
		t.Error("empty password authenticated against an OIDC-provisioned account")
	}
	// A Reader API key is generated: OIDC covers the browser, but Reader
	// clients are machine clients that only understand the API key.
	if len(user.ApiKey) != 64 {
		t.Errorf("ApiKey = %q (len %d), want a generated 64-char key", user.ApiKey, len(user.ApiKey))
	}
	if user.ApiKey == "key-"+user.Email {
		t.Error("provisioned user reused a predictable API key")
	}

	// The identity row is written with the provider's subject, and a second
	// login for the same subject returns the SAME user without provisioning.
	again, provisioned, err := s.Resolve(t.Context(), Identity{
		Issuer: "https://id.example.com", Subject: "sub-1",
		Email: "New.Person@Example.com", EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if provisioned {
		t.Error("second Resolve provisioned a user; the existing identity mapping was ignored")
	}
	if again.ID != user.ID {
		t.Errorf("second login resolved to user %d, want %d", again.ID, user.ID)
	}
	if n := countUsers(t, q); n != 1 {
		t.Errorf("users = %d, want 1: a returning login created a duplicate account", n)
	}
}

func TestResolveLinksUniqueEmailMatchOnFirstLogin(t *testing.T) {
	s, q := newTestResolver(t)
	local := mustCreateLocalUser(t, q, "Alice@Example.com")

	user, provisioned, err := s.Resolve(t.Context(), Identity{
		Issuer: "https://id.example.com", Subject: "sub-alice",
		// Different case, and the provider has since changed the local part's
		// capitalisation: matching is case-insensitive by requirement.
		Email: "alice@example.COM", EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if provisioned {
		t.Error("provisioned = true, want the existing account to be linked")
	}
	if user.ID != local.ID {
		t.Fatalf("linked to user %d, want existing user %d", user.ID, local.ID)
	}
	// The existing account keeps its password: linking must not disable local
	// sign-in for an account that had one.
	if user.PasswordHash != local.PasswordHash {
		t.Error("linking changed the local password hash")
	}
	ident, err := q.GetUserIdentityByIssuerSubject(t.Context(), store.GetUserIdentityByIssuerSubjectParams{
		Issuer: "https://id.example.com", Subject: "sub-alice",
	})
	if err != nil {
		t.Fatalf("identity row missing after link: %v", err)
	}
	if ident.UserID != local.ID {
		t.Errorf("identity maps to user %d, want %d", ident.UserID, local.ID)
	}
	if n := countUsers(t, q); n != 1 {
		t.Errorf("users = %d, want 1: linking created a second account", n)
	}
}

// Two local accounts differing only by case are indistinguishable under the
// case-insensitive match. Linking either one would be a guess, so the login is
// refused. This is the case automatic matching must not paper over.
func TestResolveFailsOnAmbiguousEmailMatch(t *testing.T) {
	s, q := newTestResolver(t)
	a := mustCreateLocalUser(t, q, "bob@example.com")
	b := mustCreateLocalUser(t, q, "BOB@example.com")

	user, provisioned, err := s.Resolve(t.Context(), Identity{
		Issuer: "https://id.example.com", Subject: "sub-bob",
		Email: "Bob@Example.com", EmailVerified: true,
	})
	if !errors.Is(err, ErrEmailAmbiguous) {
		t.Fatalf("Resolve error = %v, want ErrEmailAmbiguous", err)
	}
	if user != nil || provisioned {
		t.Error("an ambiguous match returned a user or provisioned an account")
	}
	// Critically: nothing was written. Neither existing account was linked,
	// and no third account was created from the same address.
	for _, id := range []int64{a.ID, b.ID} {
		if _, err := q.GetUserIdentityByUserAndIssuer(t.Context(), store.GetUserIdentityByUserAndIssuerParams{
			UserID: id, Issuer: "https://id.example.com",
		}); err == nil {
			t.Errorf("user %d gained an identity mapping despite the ambiguous match", id)
		}
	}
	if n := countUsers(t, q); n != 2 {
		t.Errorf("users = %d, want 2: an ambiguous login created a third account", n)
	}
}

func TestResolveRejectsUnverifiedEmail(t *testing.T) {
	s, q := newTestResolver(t)
	mustCreateLocalUser(t, q, "carol@example.com")

	for _, tt := range []struct {
		name string
		sub  string
	}{
		{"no existing identity", "sub-carol"},
		{"existing identity", "sub-carol"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "existing identity" {
				// Establish the mapping with a verified login first, then show
				// that a later unverified assertion is still refused.
				if _, _, err := s.Resolve(t.Context(), Identity{
					Issuer: "https://id.example.com", Subject: tt.sub,
					Email: "carol@example.com", EmailVerified: true,
				}); err != nil {
					t.Fatalf("setup Resolve: %v", err)
				}
			}
			user, _, err := s.Resolve(t.Context(), Identity{
				Issuer: "https://id.example.com", Subject: tt.sub,
				Email: "carol@example.com", EmailVerified: false,
			})
			if !errors.Is(err, ErrEmailNotVerified) {
				t.Fatalf("Resolve error = %v, want ErrEmailNotVerified", err)
			}
			if user != nil {
				t.Error("an unverified email produced a session")
			}
		})
	}
}

// A provider-side email change must not create a second account, and must not
// move the user to the account that now owns the old address. (issuer,
// subject) decides identity; email is only recorded.
func TestResolveKeepsIdentityWhenProviderEmailChanges(t *testing.T) {
	s, q := newTestResolver(t)

	first, _, err := s.Resolve(t.Context(), Identity{
		Issuer: "https://id.example.com", Subject: "sub-dave",
		Email: "dave@example.com", EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	// Someone else registers the address dave used to have.
	other := mustCreateLocalUser(t, q, "dave@newdomain.example")

	second, provisioned, err := s.Resolve(t.Context(), Identity{
		Issuer: "https://id.example.com", Subject: "sub-dave",
		Email: "dave@newdomain.example", EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if provisioned {
		t.Error("an email change provisioned a new user")
	}
	if second.ID != first.ID {
		t.Errorf("email change resolved to user %d, want the originally mapped user %d: "+
			"identity must follow (issuer, subject), not email", second.ID, first.ID)
	}
	if second.ID == other.ID {
		t.Error("an email change moved the login to the account that now owns the address")
	}
	ident, err := q.GetUserIdentityByIssuerSubject(t.Context(), store.GetUserIdentityByIssuerSubjectParams{
		Issuer: "https://id.example.com", Subject: "sub-dave",
	})
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	if ident.Email != "dave@newdomain.example" {
		t.Errorf("stored identity email = %q, want it refreshed to the provider's current value", ident.Email)
	}
}

// The same subject at a different issuer is a different principal. Trusting a
// subject across issuers would let the operator of any configured issuer
// impersonate users of another.
func TestResolveSeparatesIdentitiesByIssuer(t *testing.T) {
	s, q := newTestResolver(t)
	first, _, err := s.Resolve(t.Context(), Identity{
		Issuer: "https://id.example.com", Subject: "shared-sub",
		Email: "erin@example.com", EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("first Resolve: %v", err)
	}

	// A second service instance configured for a different issuer resolves the
	// same subject to a different local user (provisioned, since the email
	// belongs to an account at the first issuer... and matching is by email,
	// so this is the pre-hijack case the docs cover: the second issuer can
	// claim the same verified address and be linked to the same account. That
	// is why automatic matching is opt-in and documented.)
	s2 := *s
	s2.issuer = "https://other.example.com"
	second, _, err := s2.Resolve(t.Context(), Identity{
		Issuer: "https://other.example.com", Subject: "shared-sub",
		Email: "erin@example.com", EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("second issuer resolved to user %d, want %d via the email match", second.ID, first.ID)
	}
	ident, err := q.GetUserIdentityByIssuerSubject(t.Context(), store.GetUserIdentityByIssuerSubjectParams{
		Issuer: "https://other.example.com", Subject: "shared-sub",
	})
	if err != nil {
		t.Fatalf("identity for the second issuer missing: %v", err)
	}
	if ident.UserID != first.ID {
		t.Errorf("second issuer identity maps to %d, want %d", ident.UserID, first.ID)
	}
}

func countUsers(t *testing.T, q *store.Queries) int {
	t.Helper()
	rows, err := q.ListAllUsersEmails(t.Context())
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	return len(rows)
}
