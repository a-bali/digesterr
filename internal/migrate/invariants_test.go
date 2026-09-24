package migrate

import (
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// A known duplicate must WARN and let startup continue, not abort it.
//
// This is the deliberate half of the invariant contract: duplicates can only
// arrive by hand-editing the database, both sources are rejected by the API, and
// failing startup would leave the operator with no way to resolve the collision.
// What matters is that the index is reported as absent, which the log does.
func TestDuplicateEmailFeedAddressDoesNotAbortStartup(t *testing.T) {
	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("initial Run: %v", err)
	}
	// Create the collision the invariant exists to catch. The unique index from
	// a previous Run must go first, or the insert is rejected.
	if _, err := db.Exec("DROP INDEX IF EXISTS idx_feeds_email_url"); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, email, password_hash) VALUES (1, 'a@x', 'h'), (2, 'b@x', 'h')`); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO feeds (user_id, type, name, url) VALUES
    (1, 'email', 'one', 'shared+tag@agentmail.to'),
    (2, 'email', 'two', 'shared+tag@agentmail.to')`); err != nil {
		t.Fatalf("seed feeds: %v", err)
	}

	// The whole point: Run must not return an error.
	if err := Run(db); err != nil {
		t.Fatalf("a known duplicate aborted startup: %v", err)
	}

	// And the index must genuinely be absent, so the warning is not a lie.
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_feeds_email_url'`).Scan(&n); err != nil {
		t.Fatalf("index check: %v", err)
	}
	if n != 0 {
		t.Error("the unique index exists despite the duplicate: the guard did not report accurately")
	}
}

// Once the duplicate is resolved, the index must be created on the next Run.
// Without this the instance would stay unprotected forever after one collision.
func TestEmailFeedURLIndexCreatedOnceDuplicatesAreResolved(t *testing.T) {
	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("initial Run: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, email, password_hash) VALUES (1, 'a@x', 'h'), (2, 'b@x', 'h')`); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO feeds (user_id, type, name, url) VALUES
    (1, 'email', 'one', 'solo+tag@agentmail.to')`); err != nil {
		t.Fatalf("seed feeds: %v", err)
	}
	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_feeds_email_url'`).Scan(&n); err != nil {
		t.Fatalf("index check: %v", err)
	}
	if n != 1 {
		t.Error("the unique index was not created for a clean database")
	}
	// It must actually enforce: a second feed on the same address is rejected.
	if _, err := db.Exec(`INSERT INTO feeds (user_id, type, name, url) VALUES (2, 'email', 'dup', 'solo+tag@agentmail.to')`); err == nil {
		t.Error("the unique index does not reject a duplicate address")
	}
}

// A known duplicate inbox must warn and continue, matching the feed-address
// behaviour.
func TestDuplicateUserInboxDoesNotAbortStartup(t *testing.T) {
	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("initial Run: %v", err)
	}
	if _, err := db.Exec("DROP INDEX IF EXISTS idx_users_email_inbox"); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (email, password_hash, email_inbox) VALUES
    ('a@x', 'h', 'shared@agentmail.to'),
    ('b@x', 'h', 'shared@agentmail.to')`); err != nil {
		t.Fatalf("seed users: %v", err)
	}

	if err := Run(db); err != nil {
		t.Fatalf("a known duplicate inbox aborted startup: %v", err)
	}
}

// A genuine index-creation failure must abort startup: that is the case with no
// known duplicate to explain it, and continuing would leave the load-bearing
// index silently missing.
func TestIndexCreationFailureAbortsStartup(t *testing.T) {
	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("initial Run: %v", err)
	}

	// Make CREATE INDEX fail for a reason that is NOT a duplicate: a table is
	// squatting on the index name. Verified against SQLite:
	//   "there is already a table named idx_feeds_email_url"
	if _, err := db.Exec("DROP INDEX IF EXISTS idx_feeds_email_url"); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE idx_feeds_email_url (x INTEGER)"); err != nil {
		t.Fatalf("squat on the index name: %v", err)
	}

	err := Run(db)
	if err == nil {
		// If SQLite tolerated it, the index must at least exist: silent success
		// with no index is the outcome this test exists to forbid.
		var n int
		if qerr := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_feeds_email_url'`).Scan(&n); qerr != nil {
			t.Fatalf("index check: %v", qerr)
		}
		if n == 0 {
			t.Error("Run reported success while the unique index is absent: the failure was swallowed")
		}
		return
	}
	// Failing is correct here. Check it names the index, so the operator can act.
	if !strings.Contains(err.Error(), "idx_feeds_email_url") && !strings.Contains(err.Error(), "email feed addresses") {
		t.Errorf("error = %q, want it to identify the index that could not be created", err)
	}
}
