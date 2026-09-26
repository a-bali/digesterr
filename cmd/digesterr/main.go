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
	"digesterr/internal/migrate"
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
	// `digesterr healthcheck` is a probe subcommand, dispatched before flag
	// parsing so its own flags are not consumed by the server's flag set. The
	// container HEALTHCHECK calls it, and it has to resolve the effective port
	// itself -- see healthcheck.go.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck(os.Args[2:]))
	}

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

	if err := migrate.Run(db); err != nil {
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

	// Record the port we actually bound, so the health probe does not have to
	// re-derive it from a subset of the inputs. Written after the listener is
	// live: the probe must not read a port that is not yet serving.
	//
	// It is recorded at EVERY location the probe searches (see
	// portFileWriteLocations). Writing to only one was the bug: the probe is a
	// separate process with a static command line, so a record beside an absolute
	// -c config was invisible to a no-argument container probe, which then
	// derived a different port and could report healthy while probing some other
	// process. A failure is not fatal -- it degrades the probe's detection to
	// config resolution -- but each failed location is logged.
	var portFiles []string
	for _, p := range portFileWriteLocations(*configPath, cfg.DB.Path) {
		if err := writePortFile(p, cfg.Server.Port); err != nil {
			slog.Warn("could not record bound port for the health probe",
				"error", err, "path", p)
			continue
		}
		portFiles = append(portFiles, p)
	}
	defer func() {
		for _, p := range portFiles {
			_ = os.Remove(p)
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
