package oidc

import (
	"context"
	"sync"
	"testing"

	"digesterr/internal/store"
	"digesterr/internal/testutil"
)

// Two logins arriving at the same moment for the same new identity must both
// succeed and land on ONE account — the shape a double-click on the SSO button,
// two open tabs, or a client retrying a slow callback produces.
//
// The pool is pinned to one connection exactly as production does
// (cmd/digesterr/main.go: db.SetMaxOpenConns(1), because SQLite allows a single
// writer). That setting is load-bearing for this path, and the first version of
// this test proved it by accident: with a multi-connection pool the two write
// transactions deadlock on the writer lock ("database table is locked: database
// is deadlocked (6)") and one login fails outright. So what is pinned here is
// the production configuration: concurrent first logins serialise, both get a
// user, and they converge on the same row.
func TestResolveConcurrentFirstLoginsConverge(t *testing.T) {
	db, err := testutil.OpenTestDB()
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	queries := store.New(db)
	svc := NewService(db, queries, "https://idp.example.com", func() (string, error) {
		return "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", nil
	})

	identity := Identity{
		Issuer:        "https://idp.example.com",
		Subject:       "same-subject",
		Email:         "racer@example.com",
		EmailVerified: true,
	}

	const n = 2
	users := make([]*store.User, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			users[i], _, errs[i] = svc.Resolve(context.Background(), identity)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("login %d failed: %v", i, err)
		}
	}
	if users[0].ID != users[1].ID {
		t.Fatalf("concurrent logins produced two accounts (%d, %d)", users[0].ID, users[1].ID)
	}

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM users WHERE email = 'racer@example.com'`).Scan(&count); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Errorf("users with the email = %d, want 1", count)
	}
	if err := db.QueryRow(`SELECT count(*) FROM user_identities WHERE subject = 'same-subject'`).Scan(&count); err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if count != 1 {
		t.Errorf("identity rows = %d, want 1", count)
	}
}

// The same race for an identity whose email already belongs to a local account:
// both logins must resolve to that account, not fail on the (issuer, subject)
// unique index or provision a duplicate.
func TestResolveConcurrentLinksConverge(t *testing.T) {
	db, err := testutil.OpenTestDB()
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1) // see the note in TestResolveConcurrentFirstLoginsConverge
	queries := store.New(db)
	existing, err := queries.CreateUser(context.Background(), store.CreateUserParams{
		Email:        "linked@example.com",
		PasswordHash: "$2a$12$abcdefghijklmnopqrstuv",
		ApiKey:       "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	})
	if err != nil {
		t.Fatalf("create local user: %v", err)
	}

	svc := NewService(db, queries, "https://idp.example.com", func() (string, error) {
		return "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", nil
	})

	identity := Identity{
		Issuer:        "https://idp.example.com",
		Subject:       "linked-subject",
		Email:         "linked@example.com",
		EmailVerified: true,
	}

	const n = 2
	users := make([]*store.User, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			users[i], _, errs[i] = svc.Resolve(context.Background(), identity)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("login %d failed: %v", i, err)
		}
		if users[i].ID != existing.ID {
			t.Errorf("login %d resolved to user %d, want the existing account %d", i, users[i].ID, existing.ID)
		}
	}

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM user_identities WHERE subject = 'linked-subject'`).Scan(&count); err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if count != 1 {
		t.Errorf("identity rows = %d, want 1", count)
	}
}
