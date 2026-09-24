package migrate

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// preV43EmailDB builds a database that is current EXCEPT for migration 43, by
// running the migrations and then rebuilding email_messages the way it looked
// before v43 added its columns.
//
// Simulating the old shape this way (rather than hand-writing a whole legacy
// schema) keeps the fixture honest: the rest of the database is real, so the
// migration under test runs against the same surroundings it will meet in
// production.
func preV43EmailDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("initial Run: %v", err)
	}

	if _, err := db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatalf("fk off: %v", err)
	}
	// Drop the index and rebuild the table without the v43 columns. The column
	// list is explicit, so a future column added by a LATER migration is not
	// silently carried into this fixture.
	stmts := []string{
		"DROP INDEX IF EXISTS idx_email_messages_incomplete",
		`CREATE TABLE email_messages_pre43 (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    inbox_id    TEXT    NOT NULL,
    message_id  TEXT    NOT NULL,
    thread_id   TEXT    NOT NULL DEFAULT '',
    from_addr   TEXT    NOT NULL DEFAULT '',
    to_addrs    TEXT    NOT NULL DEFAULT '',
    subject     TEXT    NOT NULL DEFAULT '',
    text        TEXT    NOT NULL DEFAULT '',
    html        TEXT    NOT NULL DEFAULT '',
    raw_headers TEXT    NOT NULL DEFAULT '{}',
    received_at TEXT,
    created_at  DATETIME NOT NULL DEFAULT (DATETIME('now')),
    UNIQUE (inbox_id, message_id)
)`,
		`INSERT INTO email_messages_pre43
   (id, inbox_id, message_id, thread_id, from_addr, to_addrs, subject, text, html, raw_headers, received_at, created_at)
 SELECT id, inbox_id, message_id, thread_id, from_addr, to_addrs, subject, text, html, raw_headers, received_at, created_at
   FROM email_messages`,
		"DROP TABLE email_messages",
		"ALTER TABLE email_messages_pre43 RENAME TO email_messages",
		"DELETE FROM schema_migrations WHERE version = 43",
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("pre-43 rebuild (%s): %v", s[:min(40, len(s))], err)
		}
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatalf("fk on: %v", err)
	}

	// Guard the fixture: if the columns are still there, the test would pass
	// without exercising the migration at all.
	for _, col := range []string{"complete_at", "staged_attempts"} {
		ok, err := HasColumn(db, "email_messages", col)
		if err != nil {
			t.Fatalf("HasColumn: %v", err)
		}
		if ok {
			t.Fatalf("fixture is wrong: %s still present, so v43 would be baselined", col)
		}
	}
	return db
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Migration 43 adds the staging-completeness marker and backfills it. The
// backfill is the load-bearing half: a blanket DATETIME('now') would declare
// every legacy row whole, including the half-staged ones this fix exists to
// repair, and the repair would never run for exactly the mail that needs it.
func TestMigration43BackfillsCompleteness(t *testing.T) {
	db := preV43EmailDB(t)

	if _, err := db.Exec(`INSERT INTO email_messages (inbox_id, message_id, text) VALUES
    ('name@agentmail.to', '<withbody@x>', 'a body'),
    ('name@agentmail.to', '<withrecip@x>', ''),
    ('name@agentmail.to', '<halfstaged@x>', '')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO email_message_recipients (inbox_id, message_id, address)
VALUES ('name@agentmail.to', '<withrecip@x>', 'name+sport@agentmail.to')`); err != nil {
		t.Fatalf("seed recipient: %v", err)
	}

	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	ok, err := HasColumn(db, "email_messages", "complete_at")
	if err != nil {
		t.Fatalf("HasColumn(complete_at): %v", err)
	}
	if !ok {
		t.Fatal("email_messages.complete_at missing after Run")
	}

	cases := []struct {
		msgID        string
		wantComplete bool
		why          string
	}{
		{"<withbody@x>", true, "a body means the message was ingested"},
		{"<withrecip@x>", true, "a recipient row means routing landed; the body may be a text-only newsletter"},
		{"<halfstaged@x>", false, "no body and no recipient row is the shape a failed pass leaves"},
	}
	for _, tc := range cases {
		var complete sql.NullString
		if err := db.QueryRow(`SELECT complete_at FROM email_messages WHERE message_id = ?`, tc.msgID).Scan(&complete); err != nil {
			t.Fatalf("read %s: %v", tc.msgID, err)
		}
		if tc.wantComplete && !complete.Valid {
			t.Errorf("%s: complete_at is NULL, want set: %s", tc.msgID, tc.why)
		}
		if !tc.wantComplete && complete.Valid {
			t.Errorf("%s: complete_at is set, want NULL: %s", tc.msgID, tc.why)
		}
	}

	// A second Run must not re-backfill: the half-staged row has to stay
	// repairable, or the fix silently does nothing after a restart.
	if err := Run(db); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	var stillNull int
	if err := db.QueryRow(`SELECT count(*) FROM email_messages WHERE message_id = '<halfstaged@x>' AND complete_at IS NULL`).Scan(&stillNull); err != nil {
		t.Fatalf("recheck: %v", err)
	}
	if stillNull != 1 {
		t.Error("the half-staged row was completed by a second Run: the repair would never see it")
	}
}

// The migration must be safe to apply to a database whose email tables are
// empty, which is the common upgrade case.
func TestMigration43OnEmptyEmailTables(t *testing.T) {
	db := preV43EmailDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}
	ok, err := HasColumn(db, "email_messages", "complete_at")
	if err != nil {
		t.Fatalf("HasColumn: %v", err)
	}
	if !ok {
		t.Fatal("complete_at missing")
	}
}

// A fresh install must get the columns from the schema itself, with no backfill
// needed and nothing marked incomplete.
func TestMigration43OnFreshDatabase(t *testing.T) {
	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}
	ok, err := HasColumn(db, "email_messages", "complete_at")
	if err != nil {
		t.Fatalf("HasColumn: %v", err)
	}
	if !ok {
		t.Fatal("complete_at missing on a fresh database")
	}
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM email_messages WHERE complete_at IS NULL`).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 0 {
		t.Errorf("a fresh database has %d incomplete rows", rows)
	}
}
