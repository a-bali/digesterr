package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "modernc.org/sqlite"

	"digesterr/internal/auth"
	"digesterr/internal/config"
	"digesterr/internal/httpapi"
	"digesterr/internal/logbuf"
	"digesterr/internal/logging"
	"digesterr/internal/schema"
	"digesterr/internal/store"
)

//go:embed static/*
var staticFiles embed.FS

func staticFileSystem() http.FileSystem {
	if _, err := os.Stat("web/dist"); err == nil {
		return http.Dir("web/dist")
	}
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil
	}
	// Check if the embedded files have real content (not just placeholder).
	if f, err := sub.Open("index.html"); err != nil {
		return nil
	} else {
		f.Close()
	}
	return http.FS(sub)
}

func main() {
	configPath := flag.String("config", "", "path to YAML config file (default ~/.digesterr.yaml)")
	flag.StringVar(configPath, "c", "", "path to YAML config file (shorthand for -config)")
	dbFlag := flag.String("db", "", "SQLite database path (overrides config file and env)")
	flag.StringVar(dbFlag, "d", "", "SQLite database path (shorthand for -db)")
	portFlag := flag.String("port", "", "HTTP listen port (overrides config file and env)")
	flag.StringVar(portFlag, "p", "", "HTTP listen port (shorthand for -port)")
	logLevelFlag := flag.String("log-level", "", "log level: debug, info, warn, error (default info; overrides config file and env)")
	flag.StringVar(logLevelFlag, "l", "", "log level (shorthand for -log-level)")
	flag.Parse()

	path := *configPath
	if path == "" {
		path = config.DefaultPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		slog.Error("failed to load configuration", "path", path, "error", err)
		os.Exit(1)
	}
	// CLI flags override both the config file and environment variables.
	if *dbFlag != "" {
		cfg.DB.Path = *dbFlag
	}
	if *portFlag != "" {
		p, err := strconv.Atoi(*portFlag)
		if err != nil {
			slog.Error("invalid -port value", "value", *portFlag)
			os.Exit(1)
		}
		cfg.Server.Port = p
	}
	if *logLevelFlag != "" {
		cfg.Logging.Level = *logLevelFlag
	}

	// Set up logging first so even early fatal errors carry the configured
	// level, source, and category attributes.
	logBuf := setupLogger(resolveLogLevel(cfg.Logging.Level), cfg.Logging.BufferSize, os.Stderr)

	if cfg.Server.JWTSecret == "" {
		slog.Error("JWT_SECRET (server.jwt_secret) is required")
		os.Exit(1)
	}

	db, err := sql.Open("sqlite", cfg.DB.Path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_texttotime=true&_time_format=sqlite")
	if err != nil {
		slog.Error("failed to open database", "path", cfg.DB.Path, "error", err)
		os.Exit(1)
	}
	// SQLite allows only one writer at a time. The poller, scheduler, and
	// HTTP handlers all write concurrently; a single connection serializes
	// them and avoids SQLITE_BUSY storms that busy_timeout alone cannot
	// fully prevent.
	db.SetMaxOpenConns(1)
	defer db.Close()

	if err := db.Ping(); err != nil {
		slog.Error("failed to ping database", "error", err)
		os.Exit(1)
	}

	if err := initSchema(db); err != nil {
		slog.Error("failed to initialize database schema", "error", err)
		os.Exit(1)
	}

	queries := store.New(db)
	authSvc := auth.NewService(cfg.Server.JWTSecret, queries, db)
	server := httpapi.NewServer(db, queries, authSvc, logBuf, cfg)

	// Cancelled on SIGINT/SIGTERM: stops the poller and scheduler on their next
	// tick and is the lifetime context for detached extraction work.
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server.SetBaseContext(rootCtx)
	server.StartPoller(rootCtx, cfg.Feed.SyncInterval.Duration())
	server.StartScheduler(rootCtx)

	handler := spaHandler(server, staticFileSystem())

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: handler,
		// Bounds a slow or stalled client without touching response time:
		// WriteTimeout is deliberately 0 because summarize/generate call the
		// LLM with a multi-minute budget, and any finite response deadline
		// would sever those replies. Handler-level context timeouts cover them.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Log the effective LLM model. Environment variables override the config
	// file (applyEnv runs last), so the value the binary actually uses is not
	// necessarily the one written in the YAML — without this line there is no
	// way to tell which model is in effect. The API key is never logged.
	slog.Info("starting digesterr", "port", cfg.Server.Port, "log_level", resolveLogLevel(cfg.Logging.Level).String(), "config", path,
		"llm_model", cfg.LLM.Model, "llm_configured", cfg.LLM.APIKey != "")

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		slog.Error("server failed", "error", err)
		os.Exit(1)
	case <-rootCtx.Done():
	}

	// Shutdown: stop accepting connections, let in-flight requests finish, then
	// let background writers drain before the database is closed. Without this,
	// a signal kills the process mid-transaction and can leave WAL work torn.
	stop() // restore default signal handling so a second signal force-quits
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("http shutdown did not complete cleanly", "error", err)
	}
	server.WaitBackground(shutdownCtx)
	if err := db.Close(); err != nil {
		slog.Warn("closing database", "error", err)
	}
	slog.Info("shutdown complete")
}

func initSchema(db *sql.DB) error {
	var count int
	err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='users'").Scan(&count)
	if err != nil {
		return fmt.Errorf("checking schema: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec(schema.DDL); err != nil {
			return fmt.Errorf("applying schema: %w", err)
		}
		slog.Info("database schema initialized")
		return nil
	}
	// Schema exists — apply any missing columns / indexes.
	return migrateSchema(db)
}

func migrateSchema(db *sql.DB) error {
	migrations := []struct {
		name string
		sql  string
	}{
		{
			"add users.api_key",
			"ALTER TABLE users ADD COLUMN api_key TEXT NOT NULL DEFAULT ''",
		},
		{
			"add items.guid",
			"ALTER TABLE items ADD COLUMN guid TEXT NOT NULL DEFAULT ''",
		},
		{
			"add items.author",
			"ALTER TABLE items ADD COLUMN author TEXT NOT NULL DEFAULT ''",
		},
		{
			"add items.published_at",
			"ALTER TABLE items ADD COLUMN published_at DATETIME",
		},
		{
			"add items.last_enriched_at",
			"ALTER TABLE items ADD COLUMN last_enriched_at DATETIME",
		},
		{
			"add items.feed_guid unique index",
			"CREATE UNIQUE INDEX IF NOT EXISTS idx_items_feed_guid ON items(feed_id, guid) WHERE guid != ''",
		},
		{
			"migrate guid→hash column and index",
			"ALTER TABLE items ADD COLUMN hash TEXT NOT NULL DEFAULT ''",
		},
		{
			"add feeds.extract_content",
			"ALTER TABLE feeds ADD COLUMN extract_content INTEGER NOT NULL DEFAULT 0",
		},
		{
			"add items.extract_content",
			"ALTER TABLE items ADD COLUMN extract_content TEXT NOT NULL DEFAULT ''",
		},
		{
			"add items.extract_status",
			"ALTER TABLE items ADD COLUMN extract_status TEXT NOT NULL DEFAULT ''",
		},
		{
			"add items.extract_retries",
			"ALTER TABLE items ADD COLUMN extract_retries INTEGER NOT NULL DEFAULT 0",
		},
		{
			"add items.extract_date",
			"ALTER TABLE items ADD COLUMN extract_date DATETIME",
		},
		{
			"add digest_configs.name",
			"ALTER TABLE digest_configs ADD COLUMN name TEXT NOT NULL DEFAULT ''",
		},
		{
			"make feeds.category_id nullable",
			// Recreate feeds table with nullable category_id, preserving data with explicit column order.
			`
PRAGMA foreign_keys=OFF;
CREATE TABLE feeds_new (
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
);
INSERT INTO feeds_new SELECT * FROM feeds;
DROP TABLE feeds;
ALTER TABLE feeds_new RENAME TO feeds;
PRAGMA foreign_keys=ON;
`,
		},
		{
			"convert feeds.category_id to feeds.category text",
			// Categories become an optional string attribute on the feed. Recreate
			// feeds with a category TEXT column, carry over category names from the
			// categories table, then drop the now-unused categories table.
			`
PRAGMA foreign_keys=OFF;
CREATE TABLE feeds_new (
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
);
INSERT INTO feeds_new (id, user_id, category, type, name, url, config, last_fetched_at, status, extract_content, created_at, updated_at)
SELECT f.id, f.user_id, COALESCE(c.name, ''), f.type, f.name, f.url, f.config, f.last_fetched_at, f.status, f.extract_content, f.created_at, f.updated_at
FROM feeds f LEFT JOIN categories c ON c.id = f.category_id;
DROP TABLE feeds;
ALTER TABLE feeds_new RENAME TO feeds;
DROP TABLE IF EXISTS categories;
DROP INDEX IF EXISTS idx_feeds_category_id;
CREATE INDEX IF NOT EXISTS idx_feeds_category ON feeds(category);
PRAGMA foreign_keys=ON;
`,
		},
		{
			"add items.created_at index",
			"CREATE INDEX IF NOT EXISTS idx_items_created_at ON items(created_at)",
		},
		{
			"add feeds.min_score",
			// Must run after the feeds table-recreate migrations: those use
			// `INSERT INTO feeds_new SELECT *`, which breaks if the column
			// count changed beforehand.
			"ALTER TABLE feeds ADD COLUMN min_score INTEGER NOT NULL DEFAULT 0",
		},
		{
			"add feeds.last_error",
			"ALTER TABLE feeds ADD COLUMN last_error TEXT NOT NULL DEFAULT ''",
		},
		{
			"add feeds.last_error_at",
			"ALTER TABLE feeds ADD COLUMN last_error_at DATETIME",
		},
		{
			"add feeds.last_error_severity",
			"ALTER TABLE feeds ADD COLUMN last_error_severity TEXT NOT NULL DEFAULT ''",
		},
		{
			"add digest_configs.last_error",
			"ALTER TABLE digest_configs ADD COLUMN last_error TEXT NOT NULL DEFAULT ''",
		},
		{
			"add digest_configs.last_error_at",
			"ALTER TABLE digest_configs ADD COLUMN last_error_at DATETIME",
		},
		{
			"add digest_results.created_at index",
			"CREATE INDEX IF NOT EXISTS idx_digest_results_created_at ON digest_results(created_at)",
		},
		{
			"add users.llm_model",
			"ALTER TABLE users ADD COLUMN llm_model TEXT NOT NULL DEFAULT ''",
		},
		{
			"add users.email_inbox",
			"ALTER TABLE users ADD COLUMN email_inbox TEXT NOT NULL DEFAULT ''",
		},
		{
			"add users.email_api_key",
			"ALTER TABLE users ADD COLUMN email_api_key TEXT NOT NULL DEFAULT ''",
		},
		{
			"add email_messages staging table",
			`CREATE TABLE IF NOT EXISTS email_messages (
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
		},
		{
			"add email_sync_state table",
			`CREATE TABLE IF NOT EXISTS email_sync_state (
    inbox_id       TEXT    PRIMARY KEY,
    last_synced_at TEXT,
    last_error     TEXT    NOT NULL DEFAULT '',
    last_error_at  TEXT,
    updated_at     DATETIME NOT NULL DEFAULT (DATETIME('now'))
)`,
		},
		{
			"add email_messages inbox/received index",
			"CREATE INDEX IF NOT EXISTS idx_email_messages_inbox_received ON email_messages(inbox_id, received_at)",
		},
		{
			"add items.is_saved flag and backfill from the saved status",
			// Saved becomes a FLAG, orthogonal to read state, so a read toggle can
			// no longer destroy saved-ness. Three statements in one migration, in
			// this order:
			//
			//   1. add the column (every existing row defaults to not-saved)
			//   2. copy the current saved-ness across
			//   3. retire 'saved' as a status value
			//
			// Step 3 maps saved -> 'read' rather than 'unread' because that is
			// what the old code already reported: itemCategories() mapped a saved
			// item to [read, starred], so every reader-API client saw it as read
			// and it was absent from the unread counts. Preserving 'read' keeps
			// existing clients and every unread badge numerically unchanged.
			//
			// Idempotent: re-running copies nothing (step 2 finds no 'saved' rows)
			// and step 3 is a no-op. migrationSkipped() guards it on new DBs,
			// where the column comes from schema.sql instead.
			`ALTER TABLE items ADD COLUMN is_saved INTEGER NOT NULL DEFAULT 0;
UPDATE items SET is_saved = 1 WHERE status = 'saved';
UPDATE items SET status = 'read' WHERE status = 'saved';`,
		},
		{
			"add feeds.readability",
			// Readability becomes a per-feed ingest step: clean the body the feed
			// ALREADY delivered (items.content) with go-readability and store the
			// result in items.extract_content, without fetching anything. It exists
			// for feeds whose body is the full article (full-text RSS, newsletters),
			// where extraction — which fetches the linked page — would discard the
			// good stored body and fetch a worse copy.
			//
			// Must stay at the END of the list: two earlier migrations rebuild the
			// feeds table with `INSERT INTO feeds_new SELECT *`, which breaks if the
			// column count changed before they run. See the note on min_score above.
			//
			// Default 0 so every existing feed keeps its current behaviour.
			"ALTER TABLE feeds ADD COLUMN readability INTEGER NOT NULL DEFAULT 0",
		},
	}
	for _, m := range migrations {
		if skip, err := migrationSkipped(db, m.name); err != nil {
			slog.Debug("schema migration guard failed", "name", m.name, "error", err)
		} else if skip {
			slog.Debug("schema migration skipped (already migrated)", "name", m.name)
			continue
		}
		if _, err := db.Exec(m.sql); err != nil {
			// Most "failures" here are duplicate-column/index errors on
			// databases that already have the change, which are expected and
			// safe to ignore. Anything else (disk error, syntax) must be
			// visible: log at warn so operators see real failures at the
			// default info level.
			slog.Warn("schema migration not applied (may already exist)", "name", m.name, "error", err)
		} else {
			slog.Info("schema migration applied", "name", m.name)
		}
	}
	// Migrations toggle PRAGMA foreign_keys; make sure enforcement is on even
	// if a migration failed partway.
	db.Exec("PRAGMA foreign_keys=ON")
	// Enforce the instance-wide email address namespace. Done outside the
	// migration loop because a unique index cannot be created while colliding
	// rows exist, and that loop only logs failures at warn — an invariant this
	// load-bearing must not be able to no-op without anyone noticing.
	ensureEmailFeedURLUnique(db)
	// The per-user email configuration's load-bearing invariant: one AgentMail
	// inbox, one account. See ensureUserEmailInboxUnique.
	ensureUserEmailInboxUnique(db)
	// Copy existing guid data into hash and create new unique index. Only
	// report when there was actual work to do.
	res, err := db.Exec("UPDATE items SET hash = guid WHERE hash = '' AND guid != ''")
	hashMigrated := false
	if err == nil {
		if n, rerr := res.RowsAffected(); rerr == nil && n > 0 {
			hashMigrated = true
		}
	}
	db.Exec("DROP INDEX IF EXISTS idx_items_feed_guid")
	// Deduplicate items that share the same (feed_id, hash) — this can
	// happen when the unique index was never created (e.g. the migration
	// failed silently due to pre-existing duplicates). Keep the oldest
	// row; protect saved items and items linked to digest results.
	res, err = db.Exec(`
		DELETE FROM items
		WHERE hash != ''
		  AND id NOT IN (
			SELECT MIN(id) FROM items WHERE hash != '' GROUP BY feed_id, hash
		  )
		  AND is_saved = 0
		  AND NOT EXISTS (SELECT 1 FROM digest_result_items dri WHERE dri.item_id = items.id)
	`)
	if err == nil {
		if n, rerr := res.RowsAffected(); rerr == nil && n > 0 {
			slog.Info("deduplicated items before index creation", "deleted", n)
		}
	} else {
		slog.Warn("item deduplication failed", "error", err)
	}
	if _, err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_items_feed_hash ON items(feed_id, hash) WHERE hash != ''"); err != nil {
		slog.Warn("failed to create idx_items_feed_hash (may already exist)", "error", err)
	}
	if hashMigrated {
		slog.Info("hash migration complete: copied legacy guid values into items.hash")
	}
	return nil
}

func hasColumn(db *sql.DB, table, column string) (bool, error) {
	var n int
	// pragma_table_info's table argument is a constant here; the column name
	// is the only user-adjacent value and is bound.
	err := db.QueryRow("SELECT count(*) FROM pragma_table_info('"+table+"') WHERE name = ?", column).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ensureEmailFeedURLUnique creates the instance-wide unique index on email
// feed addresses, reporting collisions instead of failing quietly.
//
// An email address is a routing key: two feeds on the same plus-address both
// read the same mail, which double-ingests every newsletter. Email feeds are
// namespaced per account (only one account can hold a given inbox — see the
// users.email_inbox index), so a global index on url is consistent with the
// per-user setting and costs no migration. The cost of the index silently not
// existing is high enough to warrant its own path rather than a migration-loop
// entry, which only logs failures at warn.
//
// It runs OUTSIDE the migration loop for the same reason, and because indexes do
// not exist in the embedded schema at all: schema.sql only creates tables, so
// every index on an existing database comes from here or from a migration entry.
//
// Duplicate rows can only arrive by hand-editing the DB, so this reports them
// and leaves them alone rather than deleting a feed: the operator decides.
func ensureEmailFeedURLUnique(db *sql.DB) {
	var dupes int
	err := db.QueryRow(`
SELECT count(*) FROM (
    SELECT url FROM feeds WHERE type = 'email' AND url != ''
    GROUP BY url HAVING count(*) > 1
)`).Scan(&dupes)
	if err != nil {
		slog.Warn("email feed url uniqueness check failed", "error", err)
		return
	}
	if dupes > 0 {
		// Name the addresses but not the owners: this is a log, and the rows
		// may belong to more than one account.
		rows, qerr := db.Query(`
SELECT url, count(*) FROM feeds WHERE type = 'email' AND url != ''
GROUP BY url HAVING count(*) > 1 ORDER BY url`)
		if qerr == nil {
			defer rows.Close()
			for rows.Next() {
				var url string
				var n int
				if rows.Scan(&url, &n) == nil {
					slog.Warn("duplicate email feed address", "url", url, "feeds", n)
				}
			}
		}
		slog.Warn("unique index on email feed addresses NOT created: resolve the duplicate addresses and restart", "duplicate_addresses", dupes)
		return
	}
	if _, err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_feeds_email_url ON feeds(url) WHERE type = 'email'"); err != nil {
		slog.Warn("failed to create unique index on email feed addresses", "error", err)
	}
}

// ensureUserEmailInboxUnique creates the unique index that makes an AgentMail
// inbox belong to exactly one account, reporting collisions instead of failing
// quietly.
//
// It is the load-bearing invariant of the per-user email configuration: mail is
// staged in email_messages per inbox, and a feed reads the rows addressed to its
// own plus-address, so two accounts sharing a mailbox would each read the other's
// subscriptions. Without the index nothing rejects that — the second account's
// save simply succeeds and the two silently share one mailbox.
//
// It runs OUTSIDE the migration loop for the same reason as
// ensureEmailFeedURLUnique: indexes do not exist in the embedded schema (which
// creates tables only), a pre-existing duplicate would make an in-loop CREATE
// INDEX fail quietly at warn, and this invariant must not be able to no-op
// without anyone noticing.
func ensureUserEmailInboxUnique(db *sql.DB) {
	var dupes int
	err := db.QueryRow(`
SELECT count(*) FROM (
    SELECT email_inbox FROM users WHERE email_inbox != ''
    GROUP BY email_inbox HAVING count(*) > 1
)`).Scan(&dupes)
	if err != nil {
		slog.Warn("user email inbox uniqueness check failed", "error", err)
		return
	}
	if dupes > 0 {
		rows, qerr := db.Query(`
SELECT email_inbox, count(*) FROM users WHERE email_inbox != ''
GROUP BY email_inbox HAVING count(*) > 1 ORDER BY email_inbox`)
		if qerr == nil {
			defer rows.Close()
			for rows.Next() {
				var inbox string
				var n int
				if rows.Scan(&inbox, &n) == nil {
					// The inbox is the operator's own configuration, not
					// another account's private data, so naming it is useful
					// here; no user identity is logged.
					slog.Warn("duplicate email inbox across accounts", "inbox", inbox, "accounts", n)
				}
			}
		}
		slog.Warn("unique index on users.email_inbox NOT created: give each account its own mailbox and restart", "duplicate_inboxes", dupes)
		return
	}
	if _, err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_inbox ON users(email_inbox) WHERE email_inbox != ''"); err != nil {
		slog.Warn("failed to create unique index on users.email_inbox", "error", err)
	}
}

// migrationSkipped reports whether a known table-recreating migration must
// not run because the database already reflects its end state. Migrations
// re-run on every startup, so without these guards a second startup would
// recreate the legacy table shape and corrupt already-converted data.
// Unknown migrations never skip.
func migrationSkipped(db *sql.DB, name string) (bool, error) {
	switch name {
	case "make feeds.category_id nullable", "convert feeds.category_id to feeds.category text":
		// Both migrations only apply to the legacy category_id layout. Once
		// feeds carries the category text column, they are no-ops and must
		// not recreate the table.
		hasID, err := hasColumn(db, "feeds", "category_id")
		if err != nil {
			return false, err
		}
		return !hasID, nil
	}
	return false, nil
}

// resolveLogLevel picks the effective slog level for a configured level
// string (from config file, env, or -log-level flag). The default is info;
// invalid values fall back to info with a warning.
func resolveLogLevel(level string) slog.Level {
	if level == "" {
		level = "info"
	}
	if lv, ok := logging.ParseLevel(level); ok {
		return lv
	}
	slog.Warn("invalid log level value, falling back to info", "value", level)
	return slog.LevelInfo
}

// setupLogger configures the default slog logger: a text handler on w with
// level filtering, source attribution, and category stamping. Every record is
// also captured into a ring buffer (returned for the /api/logs endpoint).
func setupLogger(level slog.Level, capacity int, w io.Writer) *logbuf.Buffer {
	opts := &slog.HandlerOptions{Level: level, AddSource: true}
	text := slog.NewTextHandler(w, opts)
	buf := logbuf.New(capacity)
	slog.SetDefault(slog.New(logbuf.NewHandler(logging.NewHandler(text, opts), buf, opts)))
	return buf
}

// spaHandler wraps an API handler to also serve static SPA files.
// If no static filesystem is available (dev mode), just serves API.
func spaHandler(api http.Handler, fs http.FileSystem) http.Handler {
	if fs == nil {
		return api
	}
	fileServer := http.FileServer(fs)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Defense in depth behind the sanitizers: the SPA serves no inline
		// scripts/styles (Vite emits hashed external assets only), so block
		// inline execution, plugins, and framing outright. Feed images still
		// load from anywhere; the connect-src 'self' keeps fetch/XHR on-origin.
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' https: data:; font-src 'self' data:; "+
				"connect-src 'self'; frame-src 'none'; object-src 'none'; base-uri 'self'")
		if len(r.URL.Path) >= 4 && r.URL.Path[:4] == "/api" {
			api.ServeHTTP(w, r)
			return
		}
		// The Google Reader API is mounted under /reader/ and its ClientLogin
		// endpoint also at /accounts/ — both must bypass the SPA file server
		// (same as /api).
		if len(r.URL.Path) >= 7 && r.URL.Path[:7] == "/reader" {
			api.ServeHTTP(w, r)
			return
		}
		if len(r.URL.Path) >= 9 && r.URL.Path[:9] == "/accounts" {
			api.ServeHTTP(w, r)
			return
		}
		// /healthz must bypass the SPA file server (like /api) so liveness
		// probes get the real handler, not the index.html fallback.
		if len(r.URL.Path) >= 8 && r.URL.Path[:8] == "/healthz" {
			api.ServeHTTP(w, r)
			return
		}
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}
		if f, err := fs.Open(path); err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		slog.Debug("spa fallback", "path", r.URL.Path)
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
