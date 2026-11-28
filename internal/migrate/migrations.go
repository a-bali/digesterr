package migrate

import (
	"database/sql"
	"fmt"
	"strings"
)

// migrations is the full ordered history of schema changes. New changes are
// appended here, never edited: old entries are what legacy databases get
// baselined or upgraded through, so changing them rewrites history.
//
// Each entry has a stable version number (1-based, sequential), an ordered
// statement list, and usually an applied-guard that detects whether a
// database predating schema_migrations already reflects the change. Table
// rebuilds carry the guard only (they never re-run): they are wholesale
// transformations, not incremental statements.
//
// The two feeds-table rebuilds (v14, v15) use explicit column lists in their
// INSERTs. The old runner used SELECT *, which broke if the column count had
// changed beforehand; the guards also make them baseline-only, so no live
// database re-executes them.
var migrations = []migration{
	{
		version: 1,
		name:    "add users.api_key",
		stmts:   []string{"ALTER TABLE users ADD COLUMN api_key TEXT NOT NULL DEFAULT ''"},
		applied: columnPresent("users", "api_key"),
	},
	{
		version: 2,
		name:    "add items.guid",
		stmts:   []string{"ALTER TABLE items ADD COLUMN guid TEXT NOT NULL DEFAULT ''"},
		applied: columnPresent("items", "guid"),
	},
	{
		version: 3,
		name:    "add items.author",
		stmts:   []string{"ALTER TABLE items ADD COLUMN author TEXT NOT NULL DEFAULT ''"},
		applied: columnPresent("items", "author"),
	},
	{
		version: 4,
		name:    "add items.published_at",
		stmts:   []string{"ALTER TABLE items ADD COLUMN published_at DATETIME"},
		applied: columnPresent("items", "published_at"),
	},
	{
		version: 5,
		name:    "add items.last_enriched_at",
		stmts:   []string{"ALTER TABLE items ADD COLUMN last_enriched_at DATETIME"},
		applied: columnPresent("items", "last_enriched_at"),
	},
	{
		version: 6,
		name:    "add items.feed_guid unique index",
		// Legacy: the index has since been replaced by idx_items_feed_hash
		// (v34). On current databases it is created only to be dropped again;
		// on databases that never had it, creation still dedupes cleanly
		// before the drop. Harmless either way, and mirrors history.
		stmts: []string{"CREATE UNIQUE INDEX IF NOT EXISTS idx_items_feed_guid ON items(feed_id, guid) WHERE guid != ''"},
	},
	{
		version: 7,
		name:    "migrate guid→hash column and index",
		stmts:   []string{"ALTER TABLE items ADD COLUMN hash TEXT NOT NULL DEFAULT ''"},
		applied: columnPresent("items", "hash"),
	},
	{
		version: 8,
		name:    "add feeds.extract_content",
		stmts:   []string{"ALTER TABLE feeds ADD COLUMN extract_content INTEGER NOT NULL DEFAULT 0"},
		applied: columnPresent("feeds", "extract_content"),
	},
	{
		version: 9,
		name:    "add items.extract_content",
		stmts:   []string{"ALTER TABLE items ADD COLUMN extract_content TEXT NOT NULL DEFAULT ''"},
		applied: columnPresent("items", "extract_content"),
	},
	{
		version: 10,
		name:    "add items.extract_status",
		stmts:   []string{"ALTER TABLE items ADD COLUMN extract_status TEXT NOT NULL DEFAULT ''"},
		applied: columnPresent("items", "extract_status"),
	},
	{
		version: 11,
		name:    "add items.extract_retries",
		stmts:   []string{"ALTER TABLE items ADD COLUMN extract_retries INTEGER NOT NULL DEFAULT 0"},
		applied: columnPresent("items", "extract_retries"),
	},
	{
		version: 12,
		name:    "add items.extract_date",
		stmts:   []string{"ALTER TABLE items ADD COLUMN extract_date DATETIME"},
		applied: columnPresent("items", "extract_date"),
	},
	{
		version: 13,
		name:    "add digest_configs.name",
		stmts:   []string{"ALTER TABLE digest_configs ADD COLUMN name TEXT NOT NULL DEFAULT ''"},
		applied: columnPresent("digest_configs", "name"),
	},
	{
		version:   14,
		name:      "make feeds.category_id nullable",
		disableFK: true,
		stmts: []string{
			// Recreate feeds with nullable category_id, preserving data. The
			// column list is explicit (not SELECT *), so the copy does not
			// depend on column order or count.
			`CREATE TABLE feeds_new (
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
    updated_at      DATETIME NOT NULL DEFAULT (DATETIME('now')),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE CASCADE
)`,
			`INSERT INTO feeds_new (id, user_id, category_id, type, name, url, config, last_fetched_at, status, extract_content, created_at, updated_at)
SELECT id, user_id, category_id, type, name, url, config, last_fetched_at, status, extract_content, created_at, updated_at FROM feeds`,
			`DROP TABLE feeds`,
			`ALTER TABLE feeds_new RENAME TO feeds`,
		},
		// Runs only while the legacy category_id layout is present; once
		// feeds carries the category text column it is baselined untouched.
		applied: columnAbsent("feeds", "category_id"),
	},
	{
		version:   15,
		name:      "convert feeds.category_id to feeds.category text",
		disableFK: true,
		stmts: []string{
			// Categories become an optional string attribute: recreate feeds
			// with a category TEXT column, carry over category names from the
			// categories table, drop the now-unused categories table.
			`CREATE TABLE feeds_new (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER NOT NULL,
    category        TEXT    NOT NULL DEFAULT '',
    type            TEXT    NOT NULL,
    name            TEXT    NOT NULL,
    url             TEXT    NOT NULL DEFAULT '',
    config          TEXT    NOT NULL DEFAULT '{}',
    last_fetched_at DATETIME,
    status          TEXT    NOT NULL DEFAULT 'active',
    extract_content INTEGER NOT NULL DEFAULT 0,
    created_at      DATETIME NOT NULL DEFAULT (DATETIME('now')),
    updated_at      DATETIME NOT NULL DEFAULT (DATETIME('now')),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
)`,
			`INSERT INTO feeds_new (id, user_id, category, type, name, url, config, last_fetched_at, status, extract_content, created_at, updated_at)
SELECT f.id, f.user_id, COALESCE(c.name, ''), f.type, f.name, f.url, f.config, f.last_fetched_at, f.status, f.extract_content, f.created_at, f.updated_at
FROM feeds f LEFT JOIN categories c ON c.id = f.category_id`,
			`DROP TABLE feeds`,
			`ALTER TABLE feeds_new RENAME TO feeds`,
			`DROP TABLE IF EXISTS categories`,
			`DROP INDEX IF EXISTS idx_feeds_category_id`,
			`CREATE INDEX IF NOT EXISTS idx_feeds_category ON feeds(category)`,
		},
		applied: columnAbsent("feeds", "category_id"),
	},
	{
		version: 16,
		name:    "add items.created_at index",
		stmts:   []string{"CREATE INDEX IF NOT EXISTS idx_items_created_at ON items(created_at)"},
	},
	{
		version: 17,
		name:    "add feeds.min_score",
		stmts:   []string{"ALTER TABLE feeds ADD COLUMN min_score INTEGER NOT NULL DEFAULT 0"},
		applied: columnPresent("feeds", "min_score"),
	},
	{
		version: 18,
		name:    "add feeds.last_error",
		stmts:   []string{"ALTER TABLE feeds ADD COLUMN last_error TEXT NOT NULL DEFAULT ''"},
		applied: columnPresent("feeds", "last_error"),
	},
	{
		version: 19,
		name:    "add feeds.last_error_at",
		stmts:   []string{"ALTER TABLE feeds ADD COLUMN last_error_at DATETIME"},
		applied: columnPresent("feeds", "last_error_at"),
	},
	{
		version: 20,
		name:    "add feeds.last_error_severity",
		stmts:   []string{"ALTER TABLE feeds ADD COLUMN last_error_severity TEXT NOT NULL DEFAULT ''"},
		applied: columnPresent("feeds", "last_error_severity"),
	},
	{
		version: 21,
		name:    "add digest_configs.last_error",
		stmts:   []string{"ALTER TABLE digest_configs ADD COLUMN last_error TEXT NOT NULL DEFAULT ''"},
		applied: columnPresent("digest_configs", "last_error"),
	},
	{
		version: 22,
		name:    "add digest_configs.last_error_at",
		stmts:   []string{"ALTER TABLE digest_configs ADD COLUMN last_error_at DATETIME"},
		applied: columnPresent("digest_configs", "last_error_at"),
	},
	{
		version: 23,
		name:    "add digest_results.created_at index",
		stmts:   []string{"CREATE INDEX IF NOT EXISTS idx_digest_results_created_at ON digest_results(created_at)"},
		// A database so old it predates digest_results has nothing to index;
		// treat the index as applied rather than erroring on a missing table.
		applied: tableAbsent("digest_results"),
	},
	{
		version: 24,
		name:    "add users.llm_model",
		stmts:   []string{"ALTER TABLE users ADD COLUMN llm_model TEXT NOT NULL DEFAULT ''"},
		applied: columnPresent("users", "llm_model"),
	},
	{
		version: 25,
		name:    "add users.email_inbox",
		stmts:   []string{"ALTER TABLE users ADD COLUMN email_inbox TEXT NOT NULL DEFAULT ''"},
		applied: columnPresent("users", "email_inbox"),
	},
	{
		version: 26,
		name:    "add users.email_api_key",
		stmts:   []string{"ALTER TABLE users ADD COLUMN email_api_key TEXT NOT NULL DEFAULT ''"},
		applied: columnPresent("users", "email_api_key"),
	},
	{
		version: 27,
		name:    "add email_messages staging table",
		stmts: []string{`CREATE TABLE IF NOT EXISTS email_messages (
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
)`},
	},
	{
		version: 28,
		name:    "add email_sync_state table",
		stmts: []string{`CREATE TABLE IF NOT EXISTS email_sync_state (
    inbox_id       TEXT    PRIMARY KEY,
    last_synced_at TEXT,
    last_error     TEXT    NOT NULL DEFAULT '',
    last_error_at  TEXT,
    updated_at     DATETIME NOT NULL DEFAULT (DATETIME('now'))
)`},
	},
	{
		version: 29,
		name:    "add email_messages inbox/received index",
		stmts:   []string{"CREATE INDEX IF NOT EXISTS idx_email_messages_inbox_received ON email_messages(inbox_id, received_at)"},
	},
	// Saved becomes a FLAG, orthogonal to read state, so a read toggle can no
	// longer destroy saved-ness. Three statements in one migration, in order:
	//
	//  1. add the column (every existing row defaults to not-saved)
	//  2. copy the current saved-ness across
	//  3. retire 'saved' as a status value
	//
	// Step 3 maps saved -> 'read' rather than 'unread' because that is what
	// the old code already reported: itemCategories() mapped a saved item to
	// [read, starred], so every reader-API client saw it as read and it was
	// absent from the unread counts. Preserving 'read' keeps existing clients
	// and every unread badge numerically unchanged.
	{
		version: 30,
		name:    "add items.is_saved flag and backfill from the saved status",
		stmts: []string{
			"ALTER TABLE items ADD COLUMN is_saved INTEGER NOT NULL DEFAULT 0",
			"UPDATE items SET is_saved = 1 WHERE status = 'saved'",
			"UPDATE items SET status = 'read' WHERE status = 'saved'",
		},
		// Baselines only when the migration cannot do anything: column
		// present AND no 'saved' rows left to convert.
		applied: func(db *sql.DB) (bool, error) {
			return allTrue(db,
				columnPresent("items", "is_saved"),
				noRows("SELECT count(*) FROM items WHERE status = 'saved'"),
			)
		},
	},
	{
		version: 31,
		name:    "add feeds.readability",
		// Readability becomes a per-feed ingest step: clean the body the feed
		// ALREADY delivered (items.content) with go-readability and store the
		// result in items.extract_content, without fetching anything. Default
		// 0 so every existing feed keeps its current behaviour.
		stmts:   []string{"ALTER TABLE feeds ADD COLUMN readability INTEGER NOT NULL DEFAULT 0"},
		applied: columnPresent("feeds", "readability"),
	},
	// The hash backfill used to run on every startup; it now runs once. Copies
	// legacy guid values into items.hash.
	{
		version: 32,
		name:    "backfill items.hash from guid",
		stmts:   []string{"UPDATE items SET hash = guid WHERE hash = '' AND guid != ''"},
		applied: noRows("SELECT count(*) FROM items WHERE hash = '' AND guid != ''"),
	},
	{
		version: 33,
		name:    "drop legacy items.feed_guid index",
		stmts:   []string{"DROP INDEX IF EXISTS idx_items_feed_guid"},
	},
	// Deduplicate items sharing the same (feed_id, hash): they arise when the
	// unique index was never created due to pre-existing duplicates.
	//
	// The previous version kept the oldest row but SKIPPED any duplicate that
	// was saved or linked to a digest result. That left a duplicate in place
	// whenever the protected row was not the oldest one, and migration 35's
	// UNIQUE index over (feed_id, hash) then failed on it — aborting startup on
	// every retry, forever, with no way out but hand-editing the database.
	//
	// The protections exist so that no *state* is lost, not so that a
	// particular row survives. So the oldest row is kept and everything the
	// discarded rows carried is folded onto it: is_saved, the read/digested
	// status, the richer points/comments_count, and every digest link. Items
	// with an empty hash are exempt — the unique index is partial (hash != '')
	// precisely because those legitimately collide.
	//
	// The digest link re-pointing needs the unique index on
	// digest_result_items(result_id, item_id) out of the way: two rows in the
	// same result that collapse onto one keeper would collide mid-update. It is
	// dropped and rebuilt, deduplicating the links in between.
	{
		version: 34,
		name:    "deduplicate items by feed_id and hash",
		build:   dedupeItemsByFeedHash,
		applied: noRows(`SELECT count(*) FROM (
    SELECT feed_id, hash FROM items WHERE hash != ''
    GROUP BY feed_id, hash HAVING count(*) > 1
)`),
	},
	{
		version: 35,
		name:    "create items.feed_hash unique index",
		// Runs the dedup first: a database that already recorded v34 under the
		// old rule still holds the duplicate that made this index fail, and a
		// recorded version is never re-run. Without this, such a database is
		// stuck permanently — v34 is marked done, so nothing ever retries it.
		build: func(db *sql.DB) ([]string, error) {
			stmts, err := dedupeItemsByFeedHash(db)
			if err != nil {
				return nil, err
			}
			return append(stmts,
				"CREATE UNIQUE INDEX IF NOT EXISTS idx_items_feed_hash ON items(feed_id, hash) WHERE hash != ''",
			), nil
		},
	},
	{
		version: 36,
		name:    "add feeds.is_archive marker",
		stmts:   []string{"ALTER TABLE feeds ADD COLUMN is_archive INTEGER NOT NULL DEFAULT 0"},
		applied: columnPresent("feeds", "is_archive"),
	},
	{
		version: 37,
		name:    "normalize email message recipients",
		stmts: []string{
			`CREATE TABLE IF NOT EXISTS email_message_recipients (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    inbox_id   TEXT NOT NULL,
    message_id TEXT NOT NULL,
    address    TEXT NOT NULL,
    UNIQUE (inbox_id, message_id, address),
    FOREIGN KEY (inbox_id, message_id) REFERENCES email_messages(inbox_id, message_id) ON DELETE CASCADE
)`,
			`CREATE INDEX IF NOT EXISTS idx_email_recipients_inbox_address ON email_message_recipients(inbox_id, address)`,
			// Backfill from the JSON array already stored in to_addrs. Only
			// valid JSON arrays are read (json_valid), so a malformed legacy
			// value cannot abort startup.
			`INSERT OR IGNORE INTO email_message_recipients (inbox_id, message_id, address)
SELECT m.inbox_id, m.message_id, je.value
FROM email_messages m, json_each(m.to_addrs) je
WHERE json_valid(m.to_addrs) AND je.value != ''`,
		},
		// Baseline when the table already exists (fresh schema, or a prior run).
		applied: tablePresent("email_message_recipients"),
	},
	{
		version: 38,
		name:    "composite indexes for hot access paths",
		stmts: []string{
			`CREATE INDEX IF NOT EXISTS idx_items_feed_status_published ON items(feed_id, status, published_at)`,
			`CREATE INDEX IF NOT EXISTS idx_items_feed_status_points ON items(feed_id, status, points)`,
			`CREATE INDEX IF NOT EXISTS idx_items_feed_status_enriched ON items(feed_id, status, last_enriched_at)`,
			`CREATE INDEX IF NOT EXISTS idx_items_saved_created ON items(is_saved, created_at)`,
			`CREATE INDEX IF NOT EXISTS idx_items_feed_extract ON items(feed_id, extract_status, extract_retries)`,
			`CREATE INDEX IF NOT EXISTS idx_digest_results_user_status_created ON digest_results(user_id, status, created_at)`,
			`CREATE INDEX IF NOT EXISTS idx_digest_results_config_created ON digest_results(config_id, created_at)`,
			`CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expires ON refresh_tokens(expires_at)`,
			`CREATE INDEX IF NOT EXISTS idx_feeds_user_category ON feeds(user_id, category)`,
		},
		// The legacy test fixture's items table predates several indexed
		// columns (points, ...): baseline when there is nothing to index
		// rather than erroring on missing columns. Real databases gained
		// those columns through earlier migrations in this same run.
		applied: columnAbsent("items", "points"),
	},
	{
		version: 39,
		name:    "deduplicate digest_result_items",
		// Existing databases may hold duplicate (result_id, item_id) pairs
		// from retried generation; keep the earliest link.
		stmts: []string{
			`DELETE FROM digest_result_items
WHERE id NOT IN (SELECT MIN(id) FROM digest_result_items GROUP BY result_id, item_id)`,
		},
		applied: func(db *sql.DB) (bool, error) {
			absent, err := tableAbsent("digest_result_items")(db)
			if err != nil || absent {
				return absent, err
			}
			return noRows(`SELECT count(*) FROM (
    SELECT result_id, item_id FROM digest_result_items
    GROUP BY result_id, item_id HAVING count(*) > 1
)`)(db)
		},
	},
	{
		version: 40,
		name:    "unique digest_result_items index",
		stmts:   []string{"CREATE UNIQUE INDEX IF NOT EXISTS idx_digest_result_items_unique ON digest_result_items(result_id, item_id)"},
		// Legacy databases may predate digest_results entirely: no table, no
		// index, nothing to do.
		applied: tableAbsent("digest_result_items"),
	},
	{
		version: 41,
		name:    "poll backoff columns on feeds",
		stmts: []string{
			"ALTER TABLE feeds ADD COLUMN fail_count INTEGER NOT NULL DEFAULT 0",
			"ALTER TABLE feeds ADD COLUMN next_poll_at DATETIME",
		},
		applied: func(db *sql.DB) (bool, error) {
			return allTrue(db,
				columnPresent("feeds", "fail_count"),
				columnPresent("feeds", "next_poll_at"),
			)
		},
	},
	{
		version:   42,
		name:      "enforce NOT NULL on items.points and items.comments_count",
		disableFK: true,
		build:     itemsPointsNotNullRebuild,
		// The predicate tests SCHEMA SHAPE, so it stays meaningful after the
		// version is recorded: if the columns are nullable again (restored from
		// a backup taken before the rebuild, or a rebuild interrupted after
		// being recorded), startup would otherwise never repair it and every
		// request scanning a NULL row would fail.
		recheck: true,
		// Baseline when BOTH rebuilt columns are already NOT NULL. Checking only
		// points would baseline a database whose rebuild was interrupted after
		// points was applied, leaving comments_count nullable and never
		// rebuilding it — the migration would be recorded as done while its
		// stated post-condition does not hold.
		applied: func(db *sql.DB) (bool, error) {
			return allTrue(db,
				columnNotNull("items", "points"),
				columnNotNull("items", "comments_count"),
			)
		},
	},
	{
		version: 43,
		name:    "track email staging completeness",
		stmts: []string{
			"ALTER TABLE email_messages ADD COLUMN complete_at DATETIME",
			// Backfill, not a blanket DATETIME('now'): a pre-existing row is
			// complete when it already has a body or at least one recipient
			// row, which is the same test the reader applies. Only rows that
			// fail it are left NULL, so those -- the ones a failed pass may
			// have half staged -- are re-staged on the next sync. A fresh
			// install backfills nothing.
			//
			// This deliberately does NOT try to tell a "text-only newsletter"
			// from a "body never fetched" for rows that have recipients: both
			// are complete. A row with neither a body nor recipients is the
			// shape a failed pass leaves behind.
			`UPDATE email_messages
SET complete_at = DATETIME('now')
WHERE (text != '' OR html != '')
   OR EXISTS (SELECT 1 FROM email_message_recipients r
              WHERE r.inbox_id = email_messages.inbox_id
                AND r.message_id = email_messages.message_id)`,
		},
		applied: func(db *sql.DB) (bool, error) {
			return allTrue(db,
				columnPresent("email_messages", "complete_at"),
			)
		},
	},
	{
		version: 44,
		name:    "add OIDC auth request and identity tables",
		// Kept in step with internal/schema/schema.sql. The applied predicate
		// tests table presence (not row data), so a database that already has
		// both tables is baselined without re-running, and one restored from a
		// backup taken between the two CREATE TABLEs is repaired.
		stmts: []string{
			`CREATE TABLE IF NOT EXISTS oidc_auth_requests (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    state_hash    TEXT    NOT NULL UNIQUE,
    nonce         TEXT    NOT NULL,
    code_verifier TEXT    NOT NULL,
    binding_hash  TEXT    NOT NULL,
    return_to     TEXT    NOT NULL DEFAULT '/',
    -- Unix seconds -- see internal/schema/schema.sql for why this is INTEGER
    -- rather than DATETIME text.
    expires_at    INTEGER NOT NULL,
    created_at    DATETIME NOT NULL DEFAULT (DATETIME('now'))
)`,
			`CREATE INDEX IF NOT EXISTS idx_oidc_auth_requests_expires ON oidc_auth_requests(expires_at)`,
			`CREATE TABLE IF NOT EXISTS user_identities (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL,
    issuer     TEXT    NOT NULL,
    subject    TEXT    NOT NULL,
    email      TEXT    NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT (DATETIME('now')),
    updated_at DATETIME NOT NULL DEFAULT (DATETIME('now')),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_user_identities_issuer_subject ON user_identities(issuer, subject)`,
			`CREATE INDEX IF NOT EXISTS idx_user_identities_user_id ON user_identities(user_id)`,
		},
		// recheck: the guard below tests SCHEMA SHAPE (tables and indexes
		// present), not row data, so consulting it after the version is
		// recorded is exactly the repair case this needs. Every statement
		// above is IF NOT EXISTS, so a re-run is a no-op when nothing is missing.
		recheck: true,
		// Every statement above is IF NOT EXISTS, so this is safe to re-run.
		//
		// The guard checks the INDEXES as well as the tables. Table presence
		// alone would baseline a database that has both tables but lost
		// idx_user_identities_issuer_subject (a partial restore, or tables
		// created by hand) -- and that index is what makes (issuer, subject)
		// single-valued, which the account-resolution code relies on to avoid
		// mapping one provider identity to two accounts.
		applied: func(db *sql.DB) (bool, error) {
			return allTrue(db,
				tablePresent("oidc_auth_requests"),
				tablePresent("user_identities"),
				indexPresent("idx_oidc_auth_requests_expires"),
				indexPresent("idx_user_identities_issuer_subject"),
				indexPresent("idx_user_identities_user_id"),
			)
		},
	},
	{
		version: 45,
		name:    "add feeds.site_url",
		// No backfill: the value comes from the feed DOCUMENT (the channel
		// <link>), which only a fetch can reveal, so an existing row learns it
		// on its next poll rather than from a guessed URL's origin. Until then
		// reader clients fall back to the origin, which is what they used before
		// the column existed.
		stmts:   []string{"ALTER TABLE feeds ADD COLUMN site_url TEXT NOT NULL DEFAULT ''"},
		applied: columnPresent("feeds", "site_url"),
	},
}

// itemsPointsNotNullRebuild returns the statements for migration 42.
//
// schema.sql has always declared items.points and items.comments_count as
// NOT NULL DEFAULT 0, but a database created from an older table definition can
// still hold them as nullable. The Go side scans them into a plain int64, so ONE
// NULL row aborts the whole request — "sql: Scan error on column index 8, name
// \"points\": converting NULL to int64 is unsupported" — and every endpoint whose
// result set reaches that row 500s. SQLite cannot add NOT NULL to an existing
// column, so the table is rebuilt with the constraints the schema specifies,
// COALESCE-ing legacy NULLs to 0.
//
// The copy is generated from the columns actually present: a legacy database may
// predate comment_url, is_saved, extract_* and others, and a static column list
// would fail with "no such column". Columns the destination has but the source
// lacks are simply omitted, taking the destination's DEFAULT.
func itemsPointsNotNullRebuild(db *sql.DB) ([]string, error) {
	present, err := columnsOf(db, "items")
	if err != nil {
		return nil, err
	}
	var cols []string
	for _, c := range itemsColumns {
		if present[c] {
			cols = append(cols, c)
		}
	}
	// Without these there is nothing to key the rows on; rebuilding a table
	// that lacks them would be meaningless.
	for _, required := range []string{"id", "feed_id", "title"} {
		if !present[required] {
			return nil, fmt.Errorf("items table has no %s column; refusing to rebuild", required)
		}
	}
	// COALESCE only the columns this migration is about; they are the ones that
	// can hold NULL while the Go side expects an int64. Everything else is
	// copied as-is so the rebuild does not silently rewrite unrelated data.
	exprs := make([]string, 0, len(cols))
	for _, c := range cols {
		switch c {
		case "points", "comments_count":
			exprs = append(exprs, fmt.Sprintf("COALESCE(%s, 0)", c))
		default:
			exprs = append(exprs, c)
		}
	}
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = quoteIdent(c)
	}
	quotedExprs := make([]string, len(exprs))
	for i, e := range exprs {
		quotedExprs[i] = e
	}

	stmts := []string{
		// A rebuild left half-done by an interrupted run (a kill between the
		// CREATE and the RENAME) strands items_new. Drop it first so the
		// migration is restartable rather than failing forever on
		// "table items_new already exists".
		`DROP TABLE IF EXISTS items_new`,
		makeItemsTableDDL("items_new"),
		fmt.Sprintf("INSERT INTO items_new (%s) SELECT %s FROM items",
			strings.Join(quoted, ", "), strings.Join(quotedExprs, ", ")),
		`DROP TABLE items`,
		`ALTER TABLE items_new RENAME TO items`,
	}
	// Indexes live on the table, so the rebuild took them with it. Recreate the
	// current set plus the unique hash index; IF NOT EXISTS keeps this a no-op
	// for any that somehow survived.
	stmts = append(stmts, itemsIndexDDL...)
	return stmts, nil
}

// itemsColumns is the canonical column order for a rebuilt items table.
var itemsColumns = []string{
	"id", "feed_id", "hash", "title", "url", "content", "author", "comment_url",
	"points", "comments_count", "published_at", "status", "fetched_at",
	"created_at", "updated_at", "last_enriched_at", "extract_content",
	"extract_status", "extract_retries", "extract_date", "is_saved",
}

// itemsIndexDDL recreates every index the items table carries, so a rebuild does
// not silently drop query performance.
//
// The hash index MUST keep its partial predicate (hash != the empty string).
// schema.sql and the migration that first created it (v35) are both partial, and
// the predicate is load-bearing: the hash column is NOT NULL with an empty-string
// default, so every pre-hash-dedup item carries the same empty value and a FULL
// unique index on (feed_id, hash) rejects the second one. Two such rows in one
// feed make the CREATE fail and abort migration 42 — and with it startup — on
// exactly the legacy databases the rebuild exists to fix.
var itemsIndexDDL = []string{
	`CREATE INDEX IF NOT EXISTS idx_items_created_at ON items(created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_items_feed_status_published ON items(feed_id, status, published_at)`,
	`CREATE INDEX IF NOT EXISTS idx_items_feed_status_points ON items(feed_id, status, points)`,
	`CREATE INDEX IF NOT EXISTS idx_items_feed_status_enriched ON items(feed_id, status, last_enriched_at)`,
	`CREATE INDEX IF NOT EXISTS idx_items_saved_created ON items(is_saved, created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_items_feed_extract ON items(feed_id, extract_status, extract_retries)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_items_feed_hash ON items(feed_id, hash) WHERE hash != ''`,
}

// makeItemsTableDDL returns the CREATE TABLE statement for the items table under
// the given name, matching schema.sql.
func makeItemsTableDDL(name string) string {
	return fmt.Sprintf(`CREATE TABLE %s (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    feed_id         INTEGER NOT NULL,
    hash            TEXT    NOT NULL DEFAULT '',
    title           TEXT    NOT NULL,
    url             TEXT    NOT NULL DEFAULT '',
    content         TEXT    NOT NULL DEFAULT '',
    author          TEXT    NOT NULL DEFAULT '',
    comment_url     TEXT    NOT NULL DEFAULT '',
    points          INTEGER NOT NULL DEFAULT 0,
    comments_count  INTEGER NOT NULL DEFAULT 0,
    published_at    DATETIME,
    status          TEXT    NOT NULL DEFAULT 'unread',
    fetched_at      DATETIME NOT NULL DEFAULT (DATETIME('now')),
    created_at      DATETIME NOT NULL DEFAULT (DATETIME('now')),
    updated_at      DATETIME NOT NULL DEFAULT (DATETIME('now')),
    last_enriched_at DATETIME,
    extract_content  TEXT    NOT NULL DEFAULT '',
    extract_status   TEXT    NOT NULL DEFAULT '',
    extract_retries  INTEGER NOT NULL DEFAULT 0,
    extract_date     DATETIME,
    is_saved         INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (feed_id) REFERENCES feeds(id) ON DELETE CASCADE
)`, quoteIdent(name))
}

// columnsOf returns the set of column names on a table.
func columnsOf(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(fmt.Sprintf("SELECT name FROM pragma_table_info(%s)", quoteIdent(table)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

// allTrue ANDs guard results: the migration is baselined only when every
// condition holds.
func allTrue(db *sql.DB, guards ...func(*sql.DB) (bool, error)) (bool, error) {
	for _, g := range guards {
		ok, err := g(db)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

// dedupeItemsByFeedHash returns the statements that collapse items sharing a
// non-empty (feed_id, hash) onto the oldest such row.
//
// Merging rather than deleting the protected rows is what makes the unique
// index creatable: the old rule kept any saved or digest-linked duplicate, so a
// duplicate whose oldest row was unprotected survived and made the index
// creation fail — permanently, since the migration recording the delete was
// already marked applied. Every piece of state a discarded row carried is
// therefore folded onto the keeper:
//
//   - is_saved and the read/digested status, so an article does not un-save or
//     re-appear as unread merely because its row lost a dedup race;
//   - points and comments_count by MAX, so a re-poll that enriched the newer
//     row does not lose its score to the older one (the values are absolute
//     counters, not deltas, so the larger is the more recent observation);
//   - every digest_result_items link, re-pointed at the keeper.
//
// Digest links need care in two ways. A highlight flag must not be lost when
// the highlighted link is the one that gets deduplicated away, so highlights are
// propagated to every link in the same result that refers to the same keeper
// BEFORE any link is re-pointed. And the unique index on
// digest_result_items(result_id, item_id) has to be dropped for the duration:
// re-pointing can collapse two links in one result onto the same keeper, which
// the index would reject mid-update. It is recreated afterwards.
//
// Both migrations 34 and 35 build from this. 35 repeats the work because a
// database that already recorded 34 under the old rule still holds the
// duplicate, and a recorded version is never re-run — so 34 alone cannot
// unwedge it. Running the (idempotent) sequence twice is harmless.
//
// Legacy databases may predate digest_result_items entirely, or hold a nullable
// points column; both are handled by inspecting the actual schema.
func dedupeItemsByFeedHash(db *sql.DB) ([]string, error) {
	cols, err := columnsOf(db, "items")
	if err != nil {
		return nil, err
	}
	for _, required := range []string{"id", "feed_id", "hash"} {
		if !cols[required] {
			// Nothing to deduplicate on: a table this old predates the whole
			// hash concept, and the caller's index is partial on hash != ''
			// anyway.
			return nil, nil
		}
	}
	hasLinks, err := tableExists(db, "digest_result_items")
	if err != nil {
		return nil, err
	}

	stmts := []string{
		// A stale temp table can survive on a connection that was reused after
		// an interrupted run, and CREATE TABLE would then fail on the name.
		`DROP TABLE IF EXISTS _dup_item_survivors`,
		// doomed_id -> keep_id for every row that is about to be discarded.
		// The keeper is the oldest row of the group, matching the previous
		// behaviour so ids stay stable for the common case.
		`CREATE TEMP TABLE _dup_item_survivors AS
SELECT i.id AS doomed_id,
       (SELECT MIN(s.id) FROM items s WHERE s.feed_id = i.feed_id AND s.hash = i.hash) AS keep_id
FROM items i
WHERE i.hash != ''
  AND i.id NOT IN (SELECT MIN(id) FROM items WHERE hash != '' GROUP BY feed_id, hash)`,
	}

	if hasLinks {
		// group(expr) maps an item id to the id that will represent its group.
		group := func(v string) string {
			return fmt.Sprintf(
				"COALESCE((SELECT m.keep_id FROM _dup_item_survivors m WHERE m.doomed_id = %s), %s)", v, v)
		}
		stmts = append(stmts,
			// Propagate highlights first, while the doomed links still exist:
			// if any link in a result points at any row of this group and is
			// highlighted, every link to the group becomes highlighted, so the
			// survivor inherits the flag no matter which link is dropped.
			fmt.Sprintf(`UPDATE digest_result_items SET is_highlight = 1
WHERE is_highlight = 0 AND EXISTS (
  SELECT 1 FROM digest_result_items x
  WHERE x.result_id = digest_result_items.result_id
    AND x.is_highlight = 1
    AND %s = %s)`, group("x.item_id"), group("digest_result_items.item_id")),
			`DROP INDEX IF EXISTS idx_digest_result_items_unique`,
			fmt.Sprintf(`UPDATE digest_result_items
SET item_id = %s
WHERE EXISTS (SELECT 1 FROM _dup_item_survivors m WHERE m.doomed_id = digest_result_items.item_id)`,
				group("digest_result_items.item_id")),
			// Links that collapsed onto the same (result, item) are now
			// duplicates; keep the earliest.
			`DELETE FROM digest_result_items
WHERE id NOT IN (SELECT MIN(id) FROM digest_result_items GROUP BY result_id, item_id)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_digest_result_items_unique ON digest_result_items(result_id, item_id)`,
		)
	}

	// Fold the discarded rows' state onto the keeper. Each subquery is guarded
	// by the survivors table, so a column absent from a very old database never
	// appears in the statement at all.
	merges := make([]string, 0, 4)
	numeric := func(col string) string {
		return fmt.Sprintf(`%s = MAX(COALESCE(%s, 0), COALESCE((SELECT MAX(COALESCE(x.%s, 0)) FROM items x
    JOIN _dup_item_survivors m ON m.doomed_id = x.id WHERE m.keep_id = items.id), 0))`, col, col, col)
	}
	if cols["is_saved"] {
		merges = append(merges, numeric("is_saved"))
	}
	if cols["points"] {
		merges = append(merges, numeric("points"))
	}
	if cols["comments_count"] {
		merges = append(merges, numeric("comments_count"))
	}
	if cols["status"] {
		// A row is settled if ANY row in its group was: read beats digested
		// beats unread. Otherwise deleting a read duplicate would resurface
		// the article as unread.
		merges = append(merges, `status = CASE
    WHEN status = 'read' OR EXISTS (SELECT 1 FROM items d JOIN _dup_item_survivors m ON m.doomed_id = d.id
        WHERE m.keep_id = items.id AND d.status = 'read') THEN 'read'
    WHEN status = 'digested' OR EXISTS (SELECT 1 FROM items d JOIN _dup_item_survivors m ON m.doomed_id = d.id
        WHERE m.keep_id = items.id AND d.status = 'digested') THEN 'digested'
    ELSE status END`)
	}
	if len(merges) > 0 {
		stmts = append(stmts, fmt.Sprintf(
			"UPDATE items SET\n    %s\nWHERE id IN (SELECT keep_id FROM _dup_item_survivors)",
			strings.Join(merges, ",\n    ")))
	}

	return append(stmts,
		`DELETE FROM items WHERE id IN (SELECT doomed_id FROM _dup_item_survivors)`,
		`DROP TABLE IF EXISTS _dup_item_survivors`,
	), nil
}

// tableExists reports whether the named table is present. table is always a
// constant from this package.
func tableExists(db *sql.DB, table string) (bool, error) {
	var n int
	err := db.QueryRow(
		"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&n)
	return n > 0, err
}
