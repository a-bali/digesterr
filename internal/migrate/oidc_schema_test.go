package migrate

import (
	"testing"
)

// The OIDC feature adds two tables. A fresh database gets them from the
// embedded schema; an existing database has to be upgraded by a migration.
// Both paths are pinned here, because a table present only in one of them fails
// the same way at runtime -- with "no such table" on the first login attempt.
func TestFreshDatabaseHasOIDCTables(t *testing.T) {
	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, table := range []string{"oidc_auth_requests", "user_identities"} {
		ok, err := tableExists(db, table)
		if err != nil {
			t.Fatalf("tableExists(%q): %v", table, err)
		}
		if !ok {
			t.Errorf("fresh database is missing table %q: the embedded schema and the migrations disagree", table)
		}
	}
}

// An existing (pre-OIDC) database must gain the tables, not merely record the
// migration version.
func TestExistingDatabaseGainsOIDCTables(t *testing.T) {
	db := openTestDB(t)
	// Build a database at the current schema, then drop the OIDC tables to
	// reproduce one created before the feature existed.
	if err := Run(db); err != nil {
		t.Fatalf("initial Run: %v", err)
	}
	for _, stmt := range []string{
		`DROP TABLE oidc_auth_requests`,
		`DROP TABLE user_identities`,
		`DELETE FROM schema_migrations WHERE name LIKE '%oidc%'`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if err := Run(db); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	for _, table := range []string{"oidc_auth_requests", "user_identities"} {
		ok, err := tableExists(db, table)
		if err != nil {
			t.Fatalf("tableExists(%q): %v", table, err)
		}
		if !ok {
			t.Errorf("upgraded database is missing table %q", table)
		}
	}
}

// (issuer, subject) is the stable identity key, so two rows for the same pair
// must be rejected by the database rather than by application code that could
// be bypassed in a race between two concurrent first logins.
func TestUserIdentitiesAreUniquePerIssuerAndSubject(t *testing.T) {
	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (email, password_hash) VALUES ('a@example.com', '')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	ins := `INSERT INTO user_identities (user_id, issuer, subject, email) VALUES (1, 'https://id.example.com', 'sub-1', 'a@example.com')`
	if _, err := db.Exec(ins); err != nil {
		t.Fatalf("first identity insert: %v", err)
	}
	if _, err := db.Exec(ins); err == nil {
		t.Fatal("second insert of the same (issuer, subject) succeeded: the stable-identity key is not enforced")
	}
	// The same subject at a DIFFERENT issuer is a different principal.
	if _, err := db.Exec(`INSERT INTO user_identities (user_id, issuer, subject, email) VALUES (1, 'https://other.example.com', 'sub-1', 'a@example.com')`); err != nil {
		t.Fatalf("same subject at another issuer was rejected: %v", err)
	}
}

// The state store is keyed by a hash of the state value, so a replayed state
// cannot find two rows and the raw state is not at rest in the database.
func TestOIDCStateHashIsUnique(t *testing.T) {
	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}
	ins := `INSERT INTO oidc_auth_requests (state_hash, nonce, code_verifier, binding_hash, expires_at)
VALUES ('h1', 'n1', 'v1', 'b1', DATETIME('now', '+10 minutes'))`
	if _, err := db.Exec(ins); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := db.Exec(ins); err == nil {
		t.Fatal("duplicate state_hash accepted: single-use state cannot rely on a non-unique key")
	}
}
