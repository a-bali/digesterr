package migrate

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// oldSchemaDDL is the pre-category-refactor schema: categories is its own
// table and feeds references it via feeds.category_id. Used to prove that a
// legacy database reaches the current shape through Run.
const oldSchemaDDL = `
CREATE TABLE users (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    email       TEXT    NOT NULL UNIQUE,
    password_hash TEXT  NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT (DATETIME('now')),
    updated_at  DATETIME NOT NULL DEFAULT (DATETIME('now'))
);
CREATE TABLE categories (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL,
    name        TEXT    NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT (DATETIME('now')),
    updated_at  DATETIME NOT NULL DEFAULT (DATETIME('now'))
);
CREATE TABLE feeds (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER NOT NULL,
    category_id     INTEGER,
    type            TEXT    NOT NULL,
    name            TEXT    NOT NULL,
    url             TEXT    NOT NULL DEFAULT '',
    config          TEXT    NOT NULL DEFAULT '{}',
    last_fetched_at DATETIME,
    status          TEXT    NOT NULL DEFAULT 'active',
    extract_content INTEGER NOT NULL DEFAULT 0,
    created_at      DATETIME NOT NULL DEFAULT (DATETIME('now')),
    updated_at      DATETIME NOT NULL DEFAULT (DATETIME('now'))
);
CREATE TABLE items (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    feed_id     INTEGER NOT NULL,
    title       TEXT    NOT NULL,
    url         TEXT    NOT NULL DEFAULT '',
    content     TEXT    NOT NULL DEFAULT '',
    published_at DATETIME,
    status      TEXT    NOT NULL DEFAULT 'unread',
    fetched_at  DATETIME NOT NULL DEFAULT (DATETIME('now')),
    created_at  DATETIME NOT NULL DEFAULT (DATETIME('now')),
    updated_at  DATETIME NOT NULL DEFAULT (DATETIME('now'))
);
CREATE TABLE digest_configs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL,
    schedule    TEXT    NOT NULL,
    config      TEXT    NOT NULL DEFAULT '{}',
    created_at  DATETIME NOT NULL DEFAULT (DATETIME('now')),
    updated_at  DATETIME NOT NULL DEFAULT (DATETIME('now'))
);
`

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db")+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_texttotime=true&_time_format=sqlite")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// appliedCount returns how many migrations are recorded.
func appliedCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&n); err != nil {
		t.Fatalf("count applied migrations: %v", err)
	}
	return n
}

// appliedAt returns the applied_at values keyed by version.
func appliedAt(t *testing.T, db *sql.DB) map[int]string {
	t.Helper()
	rows, err := db.Query("SELECT version, applied_at FROM schema_migrations")
	if err != nil {
		t.Fatalf("read applied migrations: %v", err)
	}
	defer rows.Close()
	out := map[int]string{}
	for rows.Next() {
		var v int
		var at string
		if err := rows.Scan(&v, &at); err != nil {
			t.Fatalf("scan applied migration: %v", err)
		}
		out[v] = at
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func TestRunOnFreshDatabase(t *testing.T) {
	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Every migration is recorded without running: a fresh schema is already
	// the end state of all of them.
	if n := appliedCount(t, db); n != len(migrations) {
		t.Errorf("applied %d migrations, want %d (all)", n, len(migrations))
	}
	// Tables and key indexes exist from the embedded schema.
	for _, table := range []string{"users", "feeds", "items", "digest_configs", "digest_results", "digest_result_items", "refresh_tokens", "email_messages", "email_sync_state"} {
		var n int
		if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&n); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if n != 1 {
			t.Errorf("table %s missing after fresh Run", table)
		}
	}
}

func TestRunIsNoOpOnRestart(t *testing.T) {
	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	before := appliedAt(t, db)
	var usersBefore, feedsBefore int
	if err := db.QueryRow("SELECT count(*) FROM users").Scan(&usersBefore); err != nil {
		t.Fatalf("count users: %v", err)
	}

	if err := Run(db); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	after := appliedAt(t, db)
	if len(before) != len(after) {
		t.Errorf("migration count changed on restart: %d -> %d", len(before), len(after))
	}
	for v, at := range before {
		if after[v] != at {
			t.Errorf("migration v%d re-applied on restart (applied_at %q -> %q)", v, at, after[v])
		}
	}
	var usersAfter int
	if err := db.QueryRow("SELECT count(*) FROM users").Scan(&usersAfter); err != nil {
		t.Fatalf("count users after restart: %v", err)
	}
	if usersBefore != usersAfter || feedsBefore != 0 {
		t.Errorf("rows changed on restart: users %d -> %d", usersBefore, usersAfter)
	}
}

// TestRunUpgradesLegacyDatabase covers the category refactor end to end: a
// legacy database (categories table + feeds.category_id) reaches the current
// shape through Run, and a second Run is a safe no-op.
func TestRunUpgradesLegacyDatabase(t *testing.T) {
	db := openTestDB(t)

	if _, err := db.Exec(oldSchemaDDL); err != nil {
		t.Fatalf("apply legacy schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (email, password_hash) VALUES ('mig@example.com', 'hash')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	var userID int64
	if err := db.QueryRow(`SELECT id FROM users WHERE email = 'mig@example.com'`).Scan(&userID); err != nil {
		t.Fatalf("read user id: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO categories (user_id, name) VALUES (?, 'Tech')`, userID); err != nil {
		t.Fatalf("seed category: %v", err)
	}
	var catID int64
	if err := db.QueryRow(`SELECT id FROM categories WHERE name = 'Tech'`).Scan(&catID); err != nil {
		t.Fatalf("read category id: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO feeds (user_id, category_id, type, name, url) VALUES (?, ?, 'rss', 'Migrated Feed', 'https://example.com/feed')`, userID, catID); err != nil {
		t.Fatalf("seed feed: %v", err)
	}
	// An uncategorized feed must convert with an empty category.
	if _, err := db.Exec(`INSERT INTO feeds (user_id, type, name, url) VALUES (?, 'rss', 'Bare Feed', 'https://example.com/bare')`, userID); err != nil {
		t.Fatalf("seed bare feed: %v", err)
	}

	if err := Run(db); err != nil {
		t.Fatalf("Run on legacy database: %v", err)
	}

	// Conversion: category text carries the category name, categories table
	// is gone, legacy column and index are gone.
	hasCatID, err := HasColumn(db, "feeds", "category_id")
	if err != nil {
		t.Fatalf("HasColumn(category_id): %v", err)
	}
	if hasCatID {
		t.Error("feeds.category_id still present after conversion")
	}
	hasCat, err := HasColumn(db, "feeds", "category")
	if err != nil {
		t.Fatalf("HasColumn(category): %v", err)
	}
	if !hasCat {
		t.Error("feeds.category missing after conversion")
	}
	var catTable int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'categories'`).Scan(&catTable); err != nil {
		t.Fatalf("check categories table: %v", err)
	}
	if catTable != 0 {
		t.Error("categories table still exists after conversion")
	}
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM feeds`).Scan(&rows); err != nil {
		t.Fatalf("count feeds: %v", err)
	}
	if rows != 2 {
		t.Errorf("expected 2 feeds after conversion, got %d", rows)
	}
	var cat string
	if err := db.QueryRow(`SELECT category FROM feeds WHERE name = 'Migrated Feed'`).Scan(&cat); err != nil {
		t.Fatalf("read migrated category: %v", err)
	}
	if cat != "Tech" {
		t.Errorf("migrated feed category = %q, want Tech", cat)
	}
	if err := db.QueryRow(`SELECT category FROM feeds WHERE name = 'Bare Feed'`).Scan(&cat); err != nil {
		t.Fatalf("read bare feed category: %v", err)
	}
	if cat != "" {
		t.Errorf("bare feed category = %q, want empty", cat)
	}

	// Restart: strict no-op, keeping the converted shape and all rows.
	before := appliedAt(t, db)
	if err := Run(db); err != nil {
		t.Fatalf("Run on restart: %v", err)
	}
	after := appliedAt(t, db)
	if len(before) != len(after) {
		t.Errorf("migration count changed on restart: %d -> %d", len(before), len(after))
	}
	for v, at := range before {
		if after[v] != at {
			t.Errorf("migration v%d re-applied on restart", v)
		}
	}
	hasCatID, err = HasColumn(db, "feeds", "category_id")
	if err != nil {
		t.Fatalf("HasColumn(category_id) after restart: %v", err)
	}
	if hasCatID {
		t.Error("feeds.category_id resurrected on restart")
	}
	if err := db.QueryRow(`SELECT count(*) FROM feeds`).Scan(&rows); err != nil {
		t.Fatalf("count feeds after restart: %v", err)
	}
	if rows != 2 {
		t.Errorf("expected 2 feeds after restart, got %d", rows)
	}
	var orphan int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'feeds_new'`).Scan(&orphan); err != nil {
		t.Fatalf("check orphan table: %v", err)
	}
	if orphan != 0 {
		t.Error("orphan feeds_new table left behind")
	}
}

// TestRunReappliesSingleMissingMigration deletes one version row and proves
// only that migration is re-recorded, leaving every other applied_at intact.
func TestRunReappliesSingleMissingMigration(t *testing.T) {
	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}
	before := appliedAt(t, db)
	// v16 is an idempotent CREATE INDEX IF NOT EXISTS: safe to re-run, and
	// its absence from the version table is what must trigger that re-run.
	if _, err := db.Exec("DELETE FROM schema_migrations WHERE version = 16"); err != nil {
		t.Fatalf("delete version row: %v", err)
	}
	if err := Run(db); err != nil {
		t.Fatalf("Run after version-row delete: %v", err)
	}
	after := appliedAt(t, db)
	if _, ok := after[16]; !ok {
		t.Error("v16 not restored after being deleted")
	}
	for v, at := range before {
		if v == 16 {
			continue
		}
		if after[v] != at {
			t.Errorf("migration v%d re-applied unexpectedly", v)
		}
	}
}

// TestRunFailsClosedOnBadMigration proves a failing statement aborts startup
// and records nothing, instead of the old warn-and-continue.
func TestRunFailsClosedOnBadMigration(t *testing.T) {
	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}
	before := appliedCount(t, db)

	bad := migration{version: 9999, name: "deliberately broken", stmts: []string{"ALTER TABLE nope ADD COLUMN x TEXT"}}
	if err := runMigration(context.Background(), db, bad); err == nil {
		t.Fatal("expected error for broken migration, got nil")
	}
	if n := appliedCount(t, db); n != before {
		t.Errorf("broken migration recorded a version: %d -> %d", before, n)
	}
}

// TestItemsPointsNotNullNormalisesLegacyNulls covers migration 42. schema.sql has
// always declared items.points/comments_count NOT NULL DEFAULT 0, but a database
// built from an older definition can hold a nullable column, and the Go side scans
// them into a plain int64 — so ONE NULL row aborts the whole list request
// ("converting NULL to int64 is unsupported"), 500ing every endpoint that reaches
// it. The migration rebuilds the table with the constraints the schema specifies
// and COALESCEs the legacy NULLs to 0.
func TestItemsPointsNotNullNormalisesLegacyNulls(t *testing.T) {
	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Simulate the legacy shape: rebuild items with nullable points, then put a
	// NULL in it. Doing it by hand is the only way to reach a state the current
	// code cannot produce (every Go param is a plain int64).
	if _, err := db.Exec(`INSERT INTO users (email, password_hash) VALUES ('nn@example.com', 'h')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO feeds (user_id, type, name, url) VALUES (1, 'rss', 'F', 'https://example.com/f')`); err != nil {
		t.Fatalf("seed feed: %v", err)
	}
	// Force the nullable shape and a NULL value. (The table is dropped, so any
	// row seeded before this point would not survive — seed after the rebuild.)
	if _, err := db.Exec(`DROP TABLE items`); err != nil {
		t.Fatalf("drop items: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    feed_id INTEGER NOT NULL,
    hash TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL,
    url TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    author TEXT NOT NULL DEFAULT '',
    comment_url TEXT NOT NULL DEFAULT '',
    points INTEGER,
    comments_count INTEGER,
    published_at DATETIME,
    status TEXT NOT NULL DEFAULT 'unread',
    fetched_at DATETIME NOT NULL DEFAULT (DATETIME('now')),
    created_at DATETIME NOT NULL DEFAULT (DATETIME('now')),
    updated_at DATETIME NOT NULL DEFAULT (DATETIME('now')),
    last_enriched_at DATETIME,
    extract_content TEXT NOT NULL DEFAULT '',
    extract_status TEXT NOT NULL DEFAULT '',
    extract_retries INTEGER NOT NULL DEFAULT 0,
    extract_date DATETIME,
    is_saved INTEGER NOT NULL DEFAULT 0
)`); err != nil {
		t.Fatalf("recreate nullable items: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO items (feed_id, hash, title, points, comments_count) VALUES (1, 'h2', 'nulls', NULL, NULL)`); err != nil {
		t.Fatalf("seed null-point item: %v", err)
	}
	// Re-open v42 so it runs again against the legacy shape.
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = 42`); err != nil {
		t.Fatalf("clear v42: %v", err)
	}

	if err := Run(db); err != nil {
		t.Fatalf("Run after reintroducing the legacy shape: %v", err)
	}

	// The NULLs are gone and the constraint is enforced.
	var nulls int
	if err := db.QueryRow(`SELECT count(*) FROM items WHERE points IS NULL OR comments_count IS NULL`).Scan(&nulls); err != nil {
		t.Fatalf("count nulls: %v", err)
	}
	if nulls != 0 {
		t.Errorf("%d rows still hold NULL points/comments_count", nulls)
	}
	for _, col := range []string{"points", "comments_count"} {
		guard := columnNotNull("items", col)
		ok, err := guard(db)
		if err != nil {
			t.Fatalf("columnNotNull(%s): %v", col, err)
		}
		if !ok {
			t.Errorf("items.%s is not NOT NULL after migration 42", col)
		}
	}
	// The row survives, with its NULLs read as 0 rather than the row dropped.
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM items`).Scan(&n); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if n != 1 {
		t.Errorf("expected the row preserved, got %d", n)
	}
	var pts int
	if err := db.QueryRow(`SELECT points FROM items WHERE hash = 'h2'`).Scan(&pts); err != nil {
		t.Fatalf("read normalised points: %v", err)
	}
	if pts != 0 {
		t.Errorf("NULL points normalised to %d, want 0", pts)
	}
	// The build helper must report NOT NULL (=1) rather than 0, or the migration
	// would re-run on every startup.
	if ok, err := columnNotNull("items", "status")(db); err != nil || !ok {
		t.Errorf("columnNotNull on an existing NOT NULL column = %v, %v; want true", ok, err)
	}
}

// TestMigration42ToleratesDuplicateEmptyHashes is the regression test for the
// rebuild's index recreation.
//
// The hash column is NOT NULL with an empty-string default, so every item
// predating the hash backfill carries the same empty value. Rebuilding the table
// recreates idx_items_feed_hash — and if that index is created WITHOUT the
// partial predicate (hash != the empty string) that schema.sql and migration 35
// both use, the second empty-hash row in one feed makes CREATE UNIQUE INDEX
// fail. That aborts migration 42, and because migrations run at startup, the
// server never comes up on exactly the
// legacy databases the rebuild exists to fix.
//
// Two empty-hash rows in one feed is the minimum reproduction: one row would not
// collide, and rows in different feeds only collide on a full index if their
// feed_id matches.
func TestMigration42ToleratesDuplicateEmptyHashes(t *testing.T) {
	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO users (email, password_hash) VALUES ('eh@example.com', 'h')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO feeds (user_id, type, name, url) VALUES (1, 'rss', 'F', 'https://example.com/eh')`); err != nil {
		t.Fatalf("seed feed: %v", err)
	}
	// Legacy shape: nullable points/comments_count (so the rebuild has real work
	// to do) plus the pre-backfill empty hashes.
	if _, err := db.Exec(`DROP TABLE items`); err != nil {
		t.Fatalf("drop items: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    feed_id INTEGER NOT NULL,
    hash TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL,
    url TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    author TEXT NOT NULL DEFAULT '',
    comment_url TEXT NOT NULL DEFAULT '',
    points INTEGER,
    comments_count INTEGER,
    published_at DATETIME,
    status TEXT NOT NULL DEFAULT 'unread',
    fetched_at DATETIME NOT NULL DEFAULT (DATETIME('now')),
    created_at DATETIME NOT NULL DEFAULT (DATETIME('now')),
    updated_at DATETIME NOT NULL DEFAULT (DATETIME('now')),
    last_enriched_at DATETIME,
    extract_content TEXT NOT NULL DEFAULT '',
    extract_status TEXT NOT NULL DEFAULT '',
    extract_retries INTEGER NOT NULL DEFAULT 0,
    extract_date DATETIME,
    is_saved INTEGER NOT NULL DEFAULT 0
)`); err != nil {
		t.Fatalf("recreate legacy items: %v", err)
	}
	// TWO rows in the SAME feed, both with hash = '' — the collision case.
	for _, title := range []string{"dup-a", "dup-b"} {
		if _, err := db.Exec(
			`INSERT INTO items (feed_id, hash, title, points, comments_count) VALUES (1, '', ?, 1, 1)`,
			title,
		); err != nil {
			t.Fatalf("seed empty-hash item %s: %v", title, err)
		}
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = 42`); err != nil {
		t.Fatalf("clear v42: %v", err)
	}

	if err := Run(db); err != nil {
		t.Fatalf("Run with duplicate empty hashes must succeed, got: %v", err)
	}

	// Both rows survive the rebuild...
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM items`).Scan(&n); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if n != 2 {
		t.Errorf("expected both empty-hash rows preserved, got %d", n)
	}
	// ...and the index exists AND is still partial, so it keeps allowing them.
	var ddl string
	if err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'idx_items_feed_hash'`,
	).Scan(&ddl); err != nil {
		t.Fatalf("read index DDL: %v", err)
	}
	if !strings.Contains(ddl, "hash != ''") {
		t.Errorf("idx_items_feed_hash lost its partial predicate: %s", ddl)
	}
	// A NON-empty duplicate must still be rejected — the index is not simply gone.
	if _, err := db.Exec(`INSERT INTO items (feed_id, hash, title) VALUES (1, 'same', 'x')`); err != nil {
		t.Fatalf("seed non-empty hash: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO items (feed_id, hash, title) VALUES (1, 'same', 'y')`); err == nil {
		t.Error("duplicate non-empty hash was accepted; the unique index is not enforcing")
	}
}

// TestMigration42IsRestartable proves a rebuild interrupted between CREATE and
// RENAME (a kill mid-migration) does not wedge the database permanently on
// "table items_new already exists".
//
// The interrupted state is specifically: the column is STILL nullable AND a stray
// items_new exists. Once the column carries NOT NULL the applied-guard baselines
// the migration and it never runs again, so planting the table after a clean Run
// would test nothing.
func TestMigration42IsRestartable(t *testing.T) {
	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (email, password_hash) VALUES ('re@example.com', 'h')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO feeds (user_id, type, name, url) VALUES (1, 'rss', 'F', 'https://example.com/f')`); err != nil {
		t.Fatalf("seed feed: %v", err)
	}
	// Recreate the interrupted state: nullable column + stray items_new. The seed
	// item is inserted AFTER the rebuild, since DROP TABLE discards anything that
	// existed before it.
	if _, err := db.Exec(`DROP TABLE items`); err != nil {
		t.Fatalf("drop items: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    feed_id INTEGER NOT NULL,
    hash TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL,
    url TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    author TEXT NOT NULL DEFAULT '',
    comment_url TEXT NOT NULL DEFAULT '',
    points INTEGER,
    comments_count INTEGER,
    published_at DATETIME,
    status TEXT NOT NULL DEFAULT 'unread',
    fetched_at DATETIME NOT NULL DEFAULT (DATETIME('now')),
    created_at DATETIME NOT NULL DEFAULT (DATETIME('now')),
    updated_at DATETIME NOT NULL DEFAULT (DATETIME('now')),
    last_enriched_at DATETIME,
    extract_content TEXT NOT NULL DEFAULT '',
    extract_status TEXT NOT NULL DEFAULT '',
    extract_retries INTEGER NOT NULL DEFAULT 0,
    extract_date DATETIME,
    is_saved INTEGER NOT NULL DEFAULT 0
)`); err != nil {
		t.Fatalf("recreate nullable items: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO items (feed_id, hash, title) VALUES (1, 'h2', 't2')`); err != nil {
		t.Fatalf("seed item 2: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = 42`); err != nil {
		t.Fatalf("clear v42: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE items_new (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("plant stray items_new: %v", err)
	}

	if err := Run(db); err != nil {
		t.Fatalf("Run with a stray items_new present: %v", err)
	}

	var stray int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'items_new'`).Scan(&stray); err != nil {
		t.Fatalf("check items_new: %v", err)
	}
	if stray != 0 {
		t.Error("items_new still present after the migration")
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM items`).Scan(&n); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if n != 1 {
		t.Errorf("expected the row preserved through the retry, got %d", n)
	}
	if ok, err := columnNotNull("items", "points")(db); err != nil || !ok {
		t.Errorf("retry did not apply NOT NULL: %v, %v", ok, err)
	}
}
