package migrate

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// legacyItemsDDL is the items table as an old database may have it: nullable
// points/comments_count and no unique index, i.e. exactly the shape that lets
// (feed_id, hash) duplicates exist.
const legacyItemsDDL = `CREATE TABLE items (
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
)`

// seedDuplicatesFixture produces a database holding `rows` items in one feed,
// all sharing the same non-empty hash, in the legacy shape, with migrations
// 34/35/42 cleared so Run replays them.
func seedDuplicatesFixture(t *testing.T, titles ...string) *sql.DB {
	t.Helper()

	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (email, password_hash) VALUES ('dup@example.com', 'h')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO feeds (user_id, type, name, url) VALUES (1, 'rss', 'F', 'https://example.com/dup')`); err != nil {
		t.Fatalf("seed feed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO digest_configs (user_id, name, schedule) VALUES (1, 'C', '0 8 * * *')`); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	// Two results so the multi-result cases have a second one to link.
	for range []int{0, 1} {
		if _, err := db.Exec(`INSERT INTO digest_results (user_id, config_id) VALUES (1, 1)`); err != nil {
			t.Fatalf("seed results: %v", err)
		}
	}
	if _, err := db.Exec(`DROP TABLE items`); err != nil {
		t.Fatalf("drop items: %v", err)
	}
	if _, err := db.Exec(legacyItemsDDL); err != nil {
		t.Fatalf("recreate legacy items: %v", err)
	}
	for _, title := range titles {
		if _, err := db.Exec(
			`INSERT INTO items (feed_id, hash, title, status, points, comments_count) VALUES (1, 'same', ?, 'unread', 1, 1)`,
			title,
		); err != nil {
			t.Fatalf("seed item %s: %v", title, err)
		}
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version IN (34, 35, 42)`); err != nil {
		t.Fatalf("clear versions: %v", err)
	}
	return db
}

// TestMigration34ResolvesProtectedDuplicateOnNewerRow is the regression for a
// permanently wedged startup.
//
// Migration 34's old keep-rule skipped any duplicate that was saved or linked to
// a digest result. When the protected row was NOT the oldest row of the group,
// the duplicate survived, and migration 35's UNIQUE index over (feed_id, hash)
// then failed on it. Because v34 was already recorded as applied, no later
// startup ever retried it: the database could never start again without hand
// editing it.
//
// The protected row here is the NEWER one — saved AND linked to a digest result
// (with a highlight) — which is precisely the old rule's blind spot.
func TestMigration34ResolvesProtectedDuplicateOnNewerRow(t *testing.T) {
	db := seedDuplicatesFixture(t, "older", "newer")

	var olderID int64
	if err := db.QueryRow(`SELECT id FROM items WHERE title = 'older'`).Scan(&olderID); err != nil {
		t.Fatalf("read older id: %v", err)
	}
	if _, err := db.Exec(
		`UPDATE items SET is_saved = 1, points = 3, comments_count = 7, status = 'read' WHERE title = 'newer'`,
	); err != nil {
		t.Fatalf("enrich newer row: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO digest_result_items (result_id, item_id, is_highlight)
		 SELECT 1, id, 1 FROM items WHERE title = 'newer'`,
	); err != nil {
		t.Fatalf("link newer row: %v", err)
	}

	// The whole point: startup must not abort.
	if err := Run(db); err != nil {
		t.Fatalf("Run must not fail on a protected duplicate: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM items WHERE feed_id = 1 AND hash = 'same'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 surviving duplicate, got %d", n)
	}

	// The index must now exist AND enforce.
	var ddl string
	if err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'idx_items_feed_hash'`).Scan(&ddl); err != nil {
		t.Fatalf("idx_items_feed_hash was not created: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO items (feed_id, hash, title) VALUES (1, 'same', 'third')`); err == nil {
		t.Error("duplicate hash accepted: the unique index is not enforcing")
	}

	// The older row survives (stable id, no reference churn)...
	var survivorID int64
	if err := db.QueryRow(`SELECT id FROM items WHERE feed_id = 1 AND hash = 'same'`).Scan(&survivorID); err != nil {
		t.Fatalf("read survivor: %v", err)
	}
	if survivorID != olderID {
		t.Errorf("expected the older row %d to survive, got %d", olderID, survivorID)
	}

	// ...carrying the state the protections existed for. Losing any of these
	// would mean the dedup silently discarded user-visible data.
	var saved, points, comments int
	var status string
	if err := db.QueryRow(
		`SELECT is_saved, points, comments_count, status FROM items WHERE id = ?`, survivorID,
	).Scan(&saved, &points, &comments, &status); err != nil {
		t.Fatalf("read survivor columns: %v", err)
	}
	if saved != 1 {
		t.Error("is_saved was lost: the saved article is no longer saved")
	}
	if status != "read" {
		t.Errorf("status = %q, want read: a read duplicate resurfaced as unread", status)
	}
	if points != 3 || comments != 7 {
		t.Errorf("enrichment lost: points=%d comments=%d, want 3 and 7", points, comments)
	}

	var linked, highlight int
	if err := db.QueryRow(
		`SELECT count(*), COALESCE(MAX(is_highlight), 0) FROM digest_result_items WHERE item_id = ?`, survivorID,
	).Scan(&linked, &highlight); err != nil {
		t.Fatalf("read digest link: %v", err)
	}
	if linked != 1 {
		t.Errorf("digest links on the survivor = %d, want 1 (link was not re-pointed)", linked)
	}
	if highlight != 1 {
		t.Error("is_highlight was lost: the highlighted digest entry lost its flag")
	}
}

// TestMigration35UnwedgesAlreadyAppliedV34 covers the databases the fix has to
// rescue in the field: v34 is ALREADY recorded as applied under the old rule, so
// it will never run again, and v35 is the migration that failed. Only v35
// repeating the dedup can recover them.
func TestMigration35UnwedgesAlreadyAppliedV34(t *testing.T) {
	db := seedDuplicatesFixture(t, "older", "newer")

	// Recreate the field state: v34 recorded as applied, v35 absent, duplicate
	// still present (exactly what the old rule left behind).
	if _, err := db.Exec(
		`INSERT OR IGNORE INTO schema_migrations (version, name) VALUES (34, 'deduplicate items by feed_id and hash')`,
	); err != nil {
		t.Fatalf("record v34: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM items WHERE hash = 'same'`).Scan(&n); err != nil {
		t.Fatalf("count before: %v", err)
	}
	if n != 2 {
		t.Fatalf("fixture is wrong: expected the duplicate to still exist before Run, got %d rows", n)
	}

	if err := Run(db); err != nil {
		t.Fatalf("Run must unwedge a database whose v34 was already applied: %v", err)
	}

	if err := db.QueryRow(`SELECT count(*) FROM items WHERE hash = 'same'`).Scan(&n); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if n != 1 {
		t.Errorf("duplicate survived: %d rows, want 1", n)
	}
	var ddl string
	if err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'idx_items_feed_hash'`).Scan(&ddl); err != nil {
		t.Errorf("idx_items_feed_hash still absent after Run: %v", err)
	}
}

// TestDuplicateHashDedupPreservesGroupState checks the merge across a three-way
// duplicate group where the state is spread over different rows: no single
// discarded row holds all of it, so a naive "keep the richest row" or
// "delete the unprotected rows" rule loses some.
func TestDuplicateHashDedupPreservesGroupState(t *testing.T) {
	db := seedDuplicatesFixture(t, "a", "b", "c")

	// a: plain. b: saved, enriched, highlighted in result 1. c: read, linked to
	// result 2 with no highlight.
	if _, err := db.Exec(`UPDATE items SET is_saved = 1, points = 9 WHERE title = 'b'`); err != nil {
		t.Fatalf("enrich b: %v", err)
	}
	if _, err := db.Exec(`UPDATE items SET status = 'read', comments_count = 4 WHERE title = 'c'`); err != nil {
		t.Fatalf("set c read: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO digest_result_items (result_id, item_id, is_highlight)
		 SELECT 1, id, 1 FROM items WHERE title = 'b'`); err != nil {
		t.Fatalf("link b: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO digest_result_items (result_id, item_id, is_highlight)
		 SELECT 2, id, 0 FROM items WHERE title = 'c'`); err != nil {
		t.Fatalf("link c: %v", err)
	}

	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var id, saved, points, comments int
	var status string
	if err := db.QueryRow(`SELECT id, is_saved, points, comments_count, status FROM items WHERE hash = 'same'`).
		Scan(&id, &saved, &points, &comments, &status); err != nil {
		t.Fatalf("read survivor: %v", err)
	}
	if saved != 1 {
		t.Error("is_saved from row b was lost")
	}
	if status != "read" {
		t.Errorf("status = %q, want read: the read state from row c was lost", status)
	}
	if points != 9 || comments != 4 {
		t.Errorf("enrichment lost: points=%d comments=%d, want 9 and 4", points, comments)
	}

	// Both digest results must still reference the surviving item.
	var r1, r2, hl1 int
	if err := db.QueryRow(`SELECT count(*) FROM digest_result_items WHERE result_id = 1 AND item_id = ?`, id).
		Scan(&r1); err != nil {
		t.Fatalf("count result 1 links: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*), COALESCE(MAX(is_highlight), 0) FROM digest_result_items WHERE result_id = 2 AND item_id = ?`, id).
		Scan(&r2, &hl1); err != nil {
		t.Fatalf("count result 2 links: %v", err)
	}
	if r1 != 1 || r2 != 1 {
		t.Errorf("digest links: result 1 = %d, result 2 = %d, want 1 each", r1, r2)
	}
	var hl int
	if err := db.QueryRow(`SELECT is_highlight FROM digest_result_items WHERE result_id = 1 AND item_id = ?`, id).
		Scan(&hl); err != nil {
		t.Fatalf("read highlight: %v", err)
	}
	if hl != 1 {
		t.Error("the highlight from row b was lost")
	}
	if hl1 != 0 {
		t.Error("the highlight leaked into result 2, which never had one")
	}
}

// TestDuplicateHashDedupLeavesEmptyHashesAlone guards the partial index: rows
// with hash = '' are legitimately permitted to collide (the index is partial on
// hash != ''), so the dedup must not touch them.
func TestDuplicateHashDedupLeavesEmptyHashesAlone(t *testing.T) {
	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (email, password_hash) VALUES ('e@example.com', 'h')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO feeds (user_id, type, name, url) VALUES (1, 'rss', 'F', 'https://example.com/e')`); err != nil {
		t.Fatalf("seed feed: %v", err)
	}
	for _, title := range []string{"empty-a", "empty-b"} {
		if _, err := db.Exec(
			`INSERT INTO items (feed_id, hash, title) VALUES (1, '', ?)`, title); err != nil {
			t.Fatalf("seed %s: %v", title, err)
		}
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version IN (34, 35)`); err != nil {
		t.Fatalf("clear versions: %v", err)
	}

	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM items WHERE hash = ''`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("empty-hash rows = %d, want both preserved", n)
	}
}

// TestDuplicateHashDedupIsIdempotent proves the sequence can run twice — which
// it does whenever a wedged database replays v35 after a successful v34 — and
// that a second pass over an already-clean database is a no-op.
func TestDuplicateHashDedupIsIdempotent(t *testing.T) {
	db := seedDuplicatesFixture(t, "older", "newer")
	if _, err := db.Exec(`UPDATE items SET is_saved = 1 WHERE title = 'newer'`); err != nil {
		t.Fatalf("mark saved: %v", err)
	}
	if err := Run(db); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	before := snapshotItems(t, db)

	// Force both migrations to re-run over the now-clean database.
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version IN (34, 35)`); err != nil {
		t.Fatalf("clear versions: %v", err)
	}
	if err := Run(db); err != nil {
		t.Fatalf("second Run must be a clean no-op: %v", err)
	}
	after := snapshotItems(t, db)
	if before != after {
		t.Errorf("second pass changed data:\nbefore %v\nafter  %v", before, after)
	}
}

// TestDuplicateHashDedupWithoutDigestTables covers a legacy database old enough
// to predate digest_result_items, where the link statements must be skipped
// rather than failing on a missing table.
func TestDuplicateHashDedupWithoutDigestTables(t *testing.T) {
	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// A minimal database: no digest tables at all.
	if _, err := db.Exec(`DROP TABLE digest_result_items`); err != nil {
		t.Fatalf("drop digest_result_items: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE digest_results`); err != nil {
		t.Fatalf("drop digest_results: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE digest_configs`); err != nil {
		t.Fatalf("drop digest_configs: %v", err)
	}
	// A legacy database cannot hold the duplicate at all while the index
	// exists, so drop it: this is exactly the pre-index shape.
	if _, err := db.Exec(`DROP INDEX IF EXISTS idx_items_feed_hash`); err != nil {
		t.Fatalf("drop hash index: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version IN (34, 35)`); err != nil {
		t.Fatalf("clear versions: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (email, password_hash) VALUES ('n@example.com', 'h')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO feeds (user_id, type, name, url) VALUES (1, 'rss', 'F', 'https://example.com/n')`); err != nil {
		t.Fatalf("seed feed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO items (feed_id, hash, title) VALUES (1, 'same', 'a')`); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO items (feed_id, hash, title) VALUES (1, 'same', 'b')`); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version IN (34, 35)`); err != nil {
		t.Fatalf("clear versions: %v", err)
	}

	if err := Run(db); err != nil {
		t.Fatalf("Run without digest tables: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM items WHERE hash = 'same'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("duplicate = %d rows, want 1", n)
	}
}

// TestDuplicateHashDedupHandlesNullPoints covers the legacy nullable columns:
// MAX() in SQLite propagates NULL rather than ignoring it, so a keeper with NULL
// points would silently swallow the discarded row's real value without the
// COALESCE guards.
func TestDuplicateHashDedupHandlesNullPoints(t *testing.T) {
	db := seedDuplicatesFixture(t, "keeper", "doomed")
	if _, err := db.Exec(`UPDATE items SET points = NULL, comments_count = NULL WHERE title = 'keeper'`); err != nil {
		t.Fatalf("null the keeper: %v", err)
	}
	if _, err := db.Exec(`UPDATE items SET points = 5, comments_count = 2 WHERE title = 'doomed'`); err != nil {
		t.Fatalf("enrich doomed: %v", err)
	}

	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var points, comments int
	if err := db.QueryRow(`SELECT points, comments_count FROM items WHERE hash = 'same'`).
		Scan(&points, &comments); err != nil {
		t.Fatalf("read: %v", err)
	}
	if points != 5 || comments != 2 {
		t.Errorf("points=%d comments=%d, want 5 and 2: the NULL keeper swallowed the values", points, comments)
	}
}

// snapshotItems renders the items table compactly so two runs can be compared.
func snapshotItems(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT id, feed_id, hash, title, status, is_saved,
        COALESCE(points, -1), COALESCE(comments_count, -1) FROM items ORDER BY id`)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var id, feedID, saved, points, comments int
		var hash, title, status string
		if err := rows.Scan(&id, &feedID, &hash, &title, &status, &saved, &points, &comments); err != nil {
			t.Fatalf("scan: %v", err)
		}
		fmt.Fprintf(&b, "%d|%d|%s|%s|%s|%d|%d|%d\n", id, feedID, hash, title, status, saved, points, comments)
	}
	return b.String()
}
