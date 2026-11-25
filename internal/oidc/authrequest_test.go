package oidc

import (
	"context"
	"testing"
	"time"

	"digesterr/internal/store"
	"digesterr/internal/testutil"
)

// A row that is still inside its lifetime must be consumable regardless of the
// timezone the process writes it in.
//
// The expiry predicates in sql/queries/oidc.sql compared the stored DATETIME
// against DATETIME('now'). SQLite compares two TEXT values lexicographically,
// and DATETIME('now') has no offset ("2026-11-25 11:08:32"), so a value the
// driver wrote in a negative-offset zone ("2026-11-25 06:18:32-05:00") sorts
// BEFORE it — a request created ten minutes in the future reads as already
// expired, and every OIDC login fails on a host west of UTC. Nothing in the
// test suite changed TZ, so this was invisible.
func TestConsumeAuthRequestIsTimezoneIndependent(t *testing.T) {
	// A fixed zone west of UTC is what makes the text comparison fail.
	west := time.FixedZone("UTC-5", -5*3600)

	for _, tc := range []struct {
		name string
		loc  *time.Location
	}{
		{"utc", time.UTC},
		{"west_of_utc", west},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := testutil.OpenTestDB()
			if err != nil {
				t.Fatalf("open test db: %v", err)
			}
			t.Cleanup(func() { db.Close() })
			q := store.New(db)
			ctx := context.Background()

			if err := q.CreateOIDCAuthRequest(ctx, store.CreateOIDCAuthRequestParams{
				StateHash:    "hash-live",
				Nonce:        "nonce",
				CodeVerifier: "verifier",
				BindingHash:  "binding",
				ReturnTo:     "/",
				// Ten minutes in the FUTURE, expressed in the process's zone.
				ExpiresAt: time.Now().In(tc.loc).Add(10 * time.Minute),
			}); err != nil {
				t.Fatalf("create auth request: %v", err)
			}

			req, err := q.ConsumeOIDCAuthRequest(ctx, "hash-live")
			if err != nil {
				t.Fatalf("a request that has not expired was not consumable: %v", err)
			}
			if req.StateHash != "hash-live" {
				t.Fatalf("consumed state %q, want hash-live", req.StateHash)
			}

			// The sweep must leave a live row alone for the same reason.
			if err := q.DeleteExpiredOIDCAuthRequests(ctx); err != nil {
				t.Fatalf("sweep expired: %v", err)
			}
			if err := q.CreateOIDCAuthRequest(ctx, store.CreateOIDCAuthRequestParams{
				StateHash:    "hash-live-2",
				Nonce:        "nonce",
				CodeVerifier: "verifier",
				BindingHash:  "binding",
				ReturnTo:     "/",
				ExpiresAt:    time.Now().In(tc.loc).Add(10 * time.Minute),
			}); err != nil {
				t.Fatalf("create second auth request: %v", err)
			}
			if err := q.DeleteExpiredOIDCAuthRequests(ctx); err != nil {
				t.Fatalf("sweep expired: %v", err)
			}
			if _, err := q.ConsumeOIDCAuthRequest(ctx, "hash-live-2"); err != nil {
				t.Fatalf("the sweep deleted a request that has not expired: %v", err)
			}
		})
	}
}

// An actually-expired request must still be refused in both directions: the fix
// for the comparison above must not turn the check into a no-op.
func TestConsumeAuthRequestRejectsExpiredInAnyZone(t *testing.T) {
	for _, loc := range []*time.Location{time.UTC, time.FixedZone("UTC-5", -5*3600), time.FixedZone("UTC+9", 9*3600)} {
		db, err := testutil.OpenTestDB()
		if err != nil {
			t.Fatalf("open test db: %v", err)
		}
		q := store.New(db)
		ctx := context.Background()

		if err := q.CreateOIDCAuthRequest(ctx, store.CreateOIDCAuthRequestParams{
			StateHash:    "hash-expired",
			Nonce:        "nonce",
			CodeVerifier: "verifier",
			BindingHash:  "binding",
			ReturnTo:     "/",
			ExpiresAt:    time.Now().In(loc).Add(-time.Minute),
		}); err != nil {
			t.Fatalf("create auth request: %v", err)
		}
		if _, err := q.ConsumeOIDCAuthRequest(ctx, "hash-expired"); err == nil {
			t.Errorf("zone %s: an expired request was consumable", loc)
		}
		db.Close()
	}
}
