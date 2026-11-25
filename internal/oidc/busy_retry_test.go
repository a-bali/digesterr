package oidc

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"digesterr/internal/schema"
	"digesterr/internal/store"

	_ "modernc.org/sqlite"
)

// openFileDB opens a real file-backed database with the production pragmas,
// which is what the busy-handling question is actually about: an in-memory
// shared-cache database serialises differently and hides the lock class.
func openFileDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_texttotime=true&_time_format=sqlite")
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema.DDL); err != nil {
		db.Close()
		t.Fatalf("apply schema: %v", err)
	}
	return db
}

// A login must survive a transient write lock held by another connection.
//
// With the production pool pinned to one connection, the only way a writer is
// busy is another *process* (a backup, sqlite3 on the volume, a second replica
// sharing the file). That window is short and SQLITE_BUSY honours busy_timeout —
// but the snapshot class (SQLITE_BUSY_SNAPSHOT, "database is locked (517)") does
// NOT, and it arrives as an ordinary error from the driver. Before this test the
// login path treated any such error as fatal and the user saw
// /login?error=oidc_error for what is a retryable condition.
func TestResolveRetriesWhenTheWriterIsBusy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "busy.db")

	db := openFileDB(t, path)
	t.Cleanup(func() { db.Close() })
	queries := store.New(db)

	// A second connection to the same file stands in for another writer.
	other, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(1)")
	if err != nil {
		t.Fatalf("open second handle: %v", err)
	}
	t.Cleanup(func() { other.Close() })
	other.SetMaxOpenConns(1)
	if _, err := other.Exec(`SELECT 1`); err != nil {
		t.Fatalf("second handle: %v", err)
	}

	svc := NewService(db, queries, "https://idp.example.com", func() (string, error) {
		return "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", nil
	})

	// Hold a write lock for a moment, starting it just before the login.
	tx, err := other.Begin()
	if err != nil {
		t.Fatalf("begin writer tx: %v", err)
	}
	if _, err := tx.Exec(`UPDATE users SET updated_at = DATETIME('now') WHERE 0`); err != nil {
		t.Fatalf("acquire write lock: %v", err)
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		tx.Rollback()
	}()

	user, provisioned, err := svc.Resolve(context.Background(), Identity{
		Issuer:        "https://idp.example.com",
		Subject:       "busy-subject",
		Email:         "busy@example.com",
		EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("a transient write lock failed the login: %v", err)
	}
	if user == nil || !provisioned {
		t.Fatalf("user=%v provisioned=%v, want a provisioned user", user, provisioned)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("db file: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM users WHERE email = 'busy@example.com'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("users = %d, want 1", n)
	}
}

// The retry must be bounded: a lock that never clears must surface as an error
// rather than hanging the request forever.
func TestResolveGivesUpOnAPersistentLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stuck.db")

	db := openFileDB(t, path)
	t.Cleanup(func() { db.Close() })
	queries := store.New(db)

	other, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(1)")
	if err != nil {
		t.Fatalf("open second handle: %v", err)
	}
	t.Cleanup(func() { other.Close() })
	other.SetMaxOpenConns(1)

	svc := NewService(db, queries, "https://idp.example.com", func() (string, error) { return "k", nil })

	tx, err := other.Begin()
	if err != nil {
		t.Fatalf("begin writer tx: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE users SET updated_at = DATETIME('now') WHERE 0`); err != nil {
		t.Fatalf("acquire write lock: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, _, err := svc.Resolve(context.Background(), Identity{
			Issuer:        "https://idp.example.com",
			Subject:       "stuck-subject",
			Email:         "stuck@example.com",
			EmailVerified: true,
		})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Resolve succeeded while the writer lock was held for the whole retry budget")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Resolve did not return while the lock never cleared: the retry budget is unbounded")
	}
	_ = fmt.Sprint()
}
