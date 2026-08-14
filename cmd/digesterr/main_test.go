package main

import (
	"bytes"
	"database/sql"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"digesterr/internal/config"
)

func TestResolveLogLevel(t *testing.T) {
	cases := []struct {
		name  string
		level string
		want  slog.Level
	}{
		{"default is info", "", slog.LevelInfo},
		{"debug", "debug", slog.LevelDebug},
		{"info", "info", slog.LevelInfo},
		{"warn", "warn", slog.LevelWarn},
		{"error", "error", slog.LevelError},
		{"invalid falls back", "verbose", slog.LevelInfo},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveLogLevel(c.level); got != c.want {
				t.Errorf("resolveLogLevel(%q) = %v, want %v", c.level, got, c.want)
			}
		})
	}
}

// oldSchemaDDL is the pre-category-refactor schema: categories is its own
// table and feeds references it via feeds.category_id. Used to prove the
// category migrations convert legacy databases.
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

// TestMigrateSchemaCategoryConversion covers the category refactor end to
// end: a legacy database (categories table + feeds.category_id) converts to
// the category text attribute, and a second migrateSchema run — which happens
// on every startup — is a safe no-op that leaves the data intact.
func TestMigrateSchemaCategoryConversion(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

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

	if err := migrateSchema(db); err != nil {
		t.Fatalf("migrateSchema: %v", err)
	}

	// Conversion: category text carries the category name, categories table
	// is gone, legacy column and index are gone.
	hasCatID, err := hasColumn(db, "feeds", "category_id")
	if err != nil {
		t.Fatalf("hasColumn(category_id): %v", err)
	}
	if hasCatID {
		t.Error("feeds.category_id still present after conversion")
	}
	hasCat, err := hasColumn(db, "feeds", "category")
	if err != nil {
		t.Fatalf("hasColumn(category): %v", err)
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

	// Restart: migrateSchema must be a no-op, keeping the converted shape and
	// all rows.
	if err := migrateSchema(db); err != nil {
		t.Fatalf("migrateSchema on restart: %v", err)
	}
	hasCatID, err = hasColumn(db, "feeds", "category_id")
	if err != nil {
		t.Fatalf("hasColumn(category_id) after restart: %v", err)
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
	if err := db.QueryRow(`SELECT category FROM feeds WHERE name = 'Migrated Feed'`).Scan(&cat); err != nil {
		t.Fatalf("read category after restart: %v", err)
	}
	if cat != "Tech" {
		t.Errorf("category after restart = %q, want Tech", cat)
	}
	var orphan int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'feeds_new'`).Scan(&orphan); err != nil {
		t.Fatalf("check orphan table: %v", err)
	}
	if orphan != 0 {
		t.Error("orphan feeds_new table left behind")
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_texttotime=true&_time_format=sqlite")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping db: %v", err)
	}
	return db
}

// TestSetupLoggerDefaultHidesDebug verifies the default level is info:
// info lines appear, debug lines are dropped, and every emitted line carries
// a category key.
func TestSetupLoggerDefaultHidesDebug(t *testing.T) {
	var out bytes.Buffer
	buf := setupLogger(slog.LevelInfo, 10, &out)

	slog.Info("visible info")
	slog.Debug("hidden debug")
	slog.Warn("visible warn")

	text := out.String()
	for _, want := range []string{"visible info", "visible warn"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in output, got %q", want, text)
		}
	}
	if strings.Contains(text, "hidden debug") {
		t.Errorf("debug line emitted at default level, got %q", text)
	}
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if !strings.Contains(line, "category=") {
			t.Errorf("record without category key: %q", line)
		}
	}
	if got := buf.Len(); got != 2 {
		t.Errorf("buffer captured %d entries, want 2", got)
	}
}

// TestSetupLoggerDebugShowsDebug verifies LOG_LEVEL=debug enables debug lines.
func TestSetupLoggerDebugShowsDebug(t *testing.T) {
	var out bytes.Buffer
	setupLogger(slog.LevelDebug, 10, &out)

	slog.Debug("debug line visible")
	slog.Info("info line visible")

	text := out.String()
	if !strings.Contains(text, "debug line visible") {
		t.Errorf("debug line not emitted at debug level, got %q", text)
	}
	if !strings.Contains(text, "info line visible") {
		t.Errorf("info line not emitted at debug level, got %q", text)
	}
}

// TestSetupLoggerWarnOnly verifies -log-level=warn keeps only warn and above.
func TestSetupLoggerWarnOnly(t *testing.T) {
	var out bytes.Buffer
	setupLogger(slog.LevelWarn, 10, &out)

	slog.Info("hidden info")
	slog.Warn("visible warn")
	slog.Error("visible error")

	text := out.String()
	if strings.Contains(text, "hidden info") {
		t.Errorf("info line emitted at warn threshold, got %q", text)
	}
	for _, want := range []string{"visible warn", "visible error"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in output, got %q", want, text)
		}
	}
}

// TestSetupLoggerCategoryFromSource verifies that a record logged from this
// package (cmd/digesterr) gets category=app.
func TestSetupLoggerCategoryFromSource(t *testing.T) {
	var out bytes.Buffer
	setupLogger(slog.LevelInfo, 10, &out)

	slog.Info("startup")

	text := out.String()
	if !strings.Contains(text, "category=app") {
		t.Errorf("expected category=app for cmd/digesterr source, got %q", text)
	}
}

// TestSetupLoggerExplicitCategory verifies explicit categories pass through
// the full chain (category=item example).
func TestSetupLoggerExplicitCategory(t *testing.T) {
	var out bytes.Buffer
	setupLogger(slog.LevelInfo, 10, &out)

	slog.With("category", "item").Info("item op")

	text := out.String()
	if !strings.Contains(text, "category=item") {
		t.Errorf("expected category=item, got %q", text)
	}
}

// TestSetupLoggerEnvTogglesDebug verifies the LOG_LEVEL env var reaches the
// logger chain: with LOG_LEVEL=debug, debug lines are emitted; with
// LOG_LEVEL=warn they are dropped again. This is the integration-level check
// for the "LOG_LEVEL override works" rule — it fails if setupLogger stops
// honoring resolveLogLevel's result.
func TestSetupLoggerEnvTogglesDebug(t *testing.T) {
	loadCfg := func(t *testing.T) config.Config {
		t.Helper()
		cfg, err := config.Load(filepath.Join(t.TempDir(), "missing.yaml"))
		if err != nil {
			t.Fatalf("config.Load: %v", err)
		}
		return cfg
	}

	t.Run("LOG_LEVEL=debug shows debug", func(t *testing.T) {
		t.Setenv("LOG_LEVEL", "debug")
		t.Setenv("DIGESTERR_LOG_LEVEL", "")

		var out bytes.Buffer
		setupLogger(resolveLogLevel(loadCfg(t).Logging.Level), 10, &out)

		slog.Debug("debug visible via env")
		text := out.String()
		if !strings.Contains(text, "debug visible via env") {
			t.Errorf("debug line not emitted with LOG_LEVEL=debug, got %q", text)
		}
	})

	t.Run("flag overrides LOG_LEVEL=debug", func(t *testing.T) {
		t.Setenv("LOG_LEVEL", "debug")
		t.Setenv("DIGESTERR_LOG_LEVEL", "")

		cfg := loadCfg(t)
		cfg.Logging.Level = "warn" // what -l/--log-level does in main()

		var out bytes.Buffer
		setupLogger(resolveLogLevel(cfg.Logging.Level), 10, &out)

		slog.Debug("debug hidden by flag")
		slog.Warn("warn visible via flag")
		text := out.String()
		if strings.Contains(text, "debug hidden by flag") {
			t.Errorf("debug line emitted with -log-level=warn, got %q", text)
		}
		if !strings.Contains(text, "warn visible via flag") {
			t.Errorf("warn line missing with -log-level=warn, got %q", text)
		}
	})
}
