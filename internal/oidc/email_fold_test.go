package oidc

import (
	"context"
	"testing"

	"digesterr/internal/store"
	"digesterr/internal/testutil"
)

// The email comparison has to be Unicode-aware. SQLite's LOWER folds ASCII only,
// so doing this in SQL would treat "İ" (U+0130) as a different character from
// "i" and miss a link that should have happened.
//
// This is pinned because the previous suite had NO non-ASCII address, so
// replacing the fold with an ASCII-only one left every test green (found by
// mutation).
func TestResolveMatchesNonASCIIEmailCaseInsensitively(t *testing.T) {
	cases := []struct {
		name  string
		local string
		idp   string
	}{
		{"dotted capital I", "İlknur@example.com", "ilknur@example.com"},
		{"mixed non-ASCII", "JOSÉ@exämple.com", "josé@EXÄMPLE.COM"},
		{"turkish lowercase i", "istanbul@example.com", "ISTANBUL@example.com"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := testutil.OpenTestDB()
			if err != nil {
				t.Fatalf("open test db: %v", err)
			}
			t.Cleanup(func() { db.Close() })
			queries := store.New(db)

			existing, err := queries.CreateUser(context.Background(), store.CreateUserParams{
				Email:        tc.local,
				PasswordHash: "$2a$12$abcdefghijklmnopqrstuv",
				ApiKey:       "abcd0123456789abcdef0123456789abcdef0123456789abcdef0123456789a",
			})
			if err != nil {
				t.Fatalf("create local user: %v", err)
			}

			svc := NewService(db, queries, "https://idp.example.com", func() (string, error) {
				return "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", nil
			})
			user, provisioned, err := svc.Resolve(context.Background(), Identity{
				Issuer:        "https://idp.example.com",
				Subject:       "nonascii-subject",
				Email:         tc.idp,
				EmailVerified: true,
			})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if provisioned {
				t.Fatalf("provisioned a second account instead of linking the existing one (local %q vs idp %q)", tc.local, tc.idp)
			}
			if user.ID != existing.ID {
				t.Fatalf("resolved to user %d, want the existing %d", user.ID, existing.ID)
			}
		})
	}
}

// Case folding is NOT what this implements, and the difference must stay one
// that only ever costs a link, never produces a wrong one. "ß" does not
// lowercase to "ss", so the accounts stay separate — a duplicate, not a
// takeover. Pinned so a future change to full folding is a deliberate decision
// rather than a silent behaviour shift.
func TestResolveDoesNotFoldSharpS(t *testing.T) {
	db, err := testutil.OpenTestDB()
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	queries := store.New(db)

	if _, err := queries.CreateUser(context.Background(), store.CreateUserParams{
		Email:        "strasse@example.com",
		PasswordHash: "$2a$12$abcdefghijklmnopqrstuv",
		ApiKey:       "abcd0123456789abcdef0123456789abcdef0123456789abcdef0123456789a",
	}); err != nil {
		t.Fatalf("create local user: %v", err)
	}

	svc := NewService(db, queries, "https://idp.example.com", func() (string, error) {
		return "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", nil
	})
	user, provisioned, err := svc.Resolve(context.Background(), Identity{
		Issuer:        "https://idp.example.com",
		Subject:       "sharp-s-subject",
		Email:         "straße@example.com",
		EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !provisioned {
		t.Fatalf("linked \"straße\" to \"strasse\" (user %d): simple lowercasing does not equate those, and treating them as equal would be full case folding", user.ID)
	}
	if user.Email != "straße@example.com" {
		t.Errorf("provisioned user email = %q, want the provider's address verbatim", user.Email)
	}
}
