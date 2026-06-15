package main

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	_ "modernc.org/sqlite"

	"digesterr/internal/auth"
	"digesterr/internal/httpapi"
	"digesterr/internal/schema"
	"digesterr/internal/store"
)

func main() {
	dbPath := envOr("DATABASE_PATH", "digesterr.db")
	port := envOr("PORT", "8080")
	jwtSecret := envOr("JWT_SECRET", "")
	if jwtSecret == "" {
		slog.Error("JWT_SECRET environment variable is required")
		os.Exit(1)
	}

	db, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)")
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

	slog.Info("starting digesterr", "port", port)
	if err := http.ListenAndServe(fmt.Sprintf(":%s", port), server); err != nil {
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
	if count > 0 {
		return nil
	}
	if _, err := db.Exec(schema.DDL); err != nil {
		return fmt.Errorf("applying schema: %w", err)
	}
	slog.Info("database schema initialized")
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}