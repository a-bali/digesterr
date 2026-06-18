package main

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"time"

	_ "modernc.org/sqlite"

	"digesterr/internal/auth"
	"digesterr/internal/httpapi"
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
	dbPath := envOr("DATABASE_PATH", "digesterr.db")
	port := envOr("PORT", "8080")
	jwtSecret := envOr("JWT_SECRET", "")
	if jwtSecret == "" {
		slog.Error("JWT_SECRET environment variable is required")
		os.Exit(1)
	}

	logLevel := slog.LevelInfo
	if os.Getenv("DIGESTERR_LOG_LEVEL") == "debug" {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	db, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_texttotime=true&_time_format=sqlite")
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
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
	authSvc := auth.NewService(jwtSecret, queries)
	server := httpapi.NewServer(queries, authSvc)
	server.StartPoller(context.Background(), 1*time.Hour)
	server.StartScheduler(context.Background())

	handler := spaHandler(server, staticFileSystem())

	slog.Info("starting digesterr", "port", port)
	if err := http.ListenAndServe(fmt.Sprintf(":%s", port), handler); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
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
	}
	for _, m := range migrations {
		if _, err := db.Exec(m.sql); err != nil {
			slog.Debug("schema migration (may already exist)", "name", m.name, "error", err)
		} else {
			slog.Info("schema migration applied", "name", m.name)
		}
	}
	// Copy existing guid data into hash and create new unique index.
	db.Exec("UPDATE items SET hash = guid WHERE hash = '' AND guid != ''")
	db.Exec("DROP INDEX IF EXISTS idx_items_feed_guid")
	db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_items_feed_hash ON items(feed_id, hash) WHERE hash != ''")
	slog.Info("hash migration complete")
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// spaHandler wraps an API handler to also serve static SPA files.
// If no static filesystem is available (dev mode), just serves API.
func spaHandler(api http.Handler, fs http.FileSystem) http.Handler {
	if fs == nil {
		return api
	}
	fileServer := http.FileServer(fs)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Path) >= 4 && r.URL.Path[:4] == "/api" {
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
		r.URL.Path = "/index.html"
		fileServer.ServeHTTP(w, r)
	})
}