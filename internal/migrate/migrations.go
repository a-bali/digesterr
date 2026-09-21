package migrate

import "database/sql"

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
	// unique index was never created due to pre-existing duplicates. Keep the
	// oldest row; protect saved items and items linked to digest results.
	{
		version: 34,
		name:    "deduplicate items by feed_id and hash",
		stmts: []string{`DELETE FROM items
WHERE hash != ''
  AND id NOT IN (
    SELECT MIN(id) FROM items WHERE hash != '' GROUP BY feed_id, hash
  )
  AND is_saved = 0
  AND NOT EXISTS (SELECT 1 FROM digest_result_items dri WHERE dri.item_id = items.id)`},
		applied: noRows(`SELECT count(*) FROM (
    SELECT feed_id, hash FROM items WHERE hash != ''
    GROUP BY feed_id, hash HAVING count(*) > 1
)`),
	},
	{
		version: 35,
		name:    "create items.feed_hash unique index",
		stmts:   []string{"CREATE UNIQUE INDEX IF NOT EXISTS idx_items_feed_hash ON items(feed_id, hash) WHERE hash != ''"},
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
		applied: tableAbsent("digest_result_items"),
	},
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
