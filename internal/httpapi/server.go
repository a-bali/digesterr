package httpapi

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"digesterr/internal/auth"
	"digesterr/internal/config"
	"digesterr/internal/digest"
	"digesterr/internal/feed"
	"digesterr/internal/logbuf"
	"digesterr/internal/metrics"
	"digesterr/internal/store"
)

type Server struct {
	mux       *http.ServeMux
	db        *sql.DB
	queries   *store.Queries
	auth      *auth.Service
	poller    *feed.Poller
	generator *digest.Generator
	llmClient *digest.LLMClient
	logs      *logbuf.Buffer
	metrics   *metrics.Recorder
	// publicBaseURL is the absolute base URL of the digesterr web UI, used to
	// build in-app links surfaced to Reader clients (feed home pages, digest
	// article links). Empty means "derive from the request host".
	publicBaseURL string
	// topComments is the experimental per-item comment fetch limit.
	// Zero disables on-demand comment fetching.
	topComments int
	// commentCache caches fetched comments in memory with a configurable TTL.
	commentCache *commentCache
	// schedulerTimezone is the IANA timezone name for digest cron schedules.
	schedulerTimezone string
	// summaryPrompt is the configured system prompt for per-item summaries.
	// Empty means the built-in default.
	summaryPrompt string
	// llmDefaultModel is the env/file-configured model name. A user with a
	// users.llm_model override uses that instead; the default stays untouched
	// so a reset can fall back to it. Stored separately (not read off the
	// shared client) because the client carries no getter.
	llmDefaultModel string
	// emailVerify checks a candidate inbox + token against AgentMail when the
	// settings dialog saves them. Nil means the real API. Tests substitute a
	// function rather than a client: that keeps the seam inside this package,
	// needing no exported base-URL setter on feed.AgentMailClient (which exists
	// precisely so the host cannot become a configuration knob).
	emailVerify func(ctx context.Context, inbox, apiKey string) error
	// llmModels lists the endpoint's models for the settings picker. Nil means
	// the real endpoint; tests substitute a function. The list is advisory, so a
	// failure is swallowed rather than surfaced.
	llmModels func(ctx context.Context) ([]string, error)
	// bg tracks background work the server owns: the feed poller, the digest
	// scheduler, and detached per-item extractions. Shutdown waits on it so
	// in-flight DB writes finish instead of being killed mid-transaction.
	bg sync.WaitGroup
	// extractSem bounds concurrent detached content extractions. Without it,
	// one burst of extract requests fans out one goroutine + one 30s HTTP
	// client each, with no upper bound.
	extractSem chan struct{}
	// baseCtx is the process lifetime context: detached work uses it so a
	// client disconnect does not abort an extraction in progress, while
	// shutdown (which cancels it) does.
	baseCtx context.Context
}

// SetBaseContext installs the process lifetime context used by detached work.
// Main calls it with the signal-aware root ctx before starting the poller.
func (s *Server) SetBaseContext(ctx context.Context) {
	s.baseCtx = ctx
}

// extractCtx returns the base context for detached extraction.
func (s *Server) extractCtx() context.Context {
	if s.baseCtx != nil {
		return s.baseCtx
	}
	return context.Background()
}

func NewServer(db *sql.DB, queries *store.Queries, authSvc *auth.Service, logs *logbuf.Buffer, cfg config.Config) *Server {
	s := &Server{
		mux:               http.NewServeMux(),
		db:                db,
		queries:           queries,
		auth:              authSvc,
		poller:            feed.NewPoller(queries),
		logs:              logs,
		metrics:           metrics.New(10000),
		publicBaseURL:     strings.TrimRight(cfg.Server.PublicBaseURL, "/"),
		topComments:       cfg.Feed.TopComments,
		commentCache:      newCommentCache(cfg.Feed.CommentCacheTTL.Duration()),
		schedulerTimezone: cfg.Digest.SchedulerTimezone,
		summaryPrompt:     cfg.LLM.SummaryPrompt,
		llmDefaultModel:   cfg.LLM.Model,
		extractSem:        make(chan struct{}, extractConcurrency(cfg)),
	}

	llmClient := digest.NewLLMClient(cfg.LLM.Endpoint, cfg.LLM.APIKey, cfg.LLM.Model, cfg.LLM.Timeout.Duration())
	llmClient.SetRecordFunc(func(timestamp time.Time, url string, statusCode int, durationMs int64, bytes int64, errMsg string) {
		s.metrics.Record(metrics.Record{
			Timestamp:  timestamp,
			URL:        url,
			StatusCode: statusCode,
			Bytes:      bytes,
			DurationMs: durationMs,
			Reason:     "llm",
			Error:      errMsg,
		})
	})
	s.llmClient = llmClient
	s.generator = digest.NewGenerator(db, queries, llmClient, s.poller)
	s.generator.SetRecordFunc(func(rec metrics.Record) {
		s.metrics.Record(rec)
	})

	// Data retention and provider credentials come from the merged config
	// (defaults < config file < env < CLI flags). The expiry age is shared by
	// feed refresh (Poller) and digest generation (Generator calls the same
	// Poller expiry path).
	s.poller.SetExpiry(cfg.Feed.ExpireItems.Duration(), cfg.Feed.ExpireDigests.Duration())
	s.poller.SetEnrichmentWindow(cfg.Feed.EnrichmentWindow.Duration())
	s.poller.SetRecordFunc(func(rec feed.Record) {
		s.metrics.Record(metrics.Record{
			Timestamp:  rec.Timestamp,
			URL:        rec.URL,
			Host:       rec.Host,
			StatusCode: rec.StatusCode,
			Bytes:      rec.Bytes,
			Requests:   rec.Requests,
			DurationMs: rec.DurationMs,
			Reason:     rec.Reason,
			FeedID:     rec.FeedID,
			FeedName:   rec.FeedName,
			FeedType:   rec.FeedType,
			Error:      rec.Error,
		})
	})
	s.poller.SetTransport(s.metrics.Transport())
	s.poller.SetRedditCredentials(cfg.Reddit.ClientID, cfg.Reddit.ClientSecret)
	s.poller.SetTwitterCredentials(cfg.Twitter.Ct0, cfg.Twitter.AuthToken)
	// The email feed type has no wiring here any more: its AgentMail credentials
	// are per user, so the poller resolves them from each feed owner's row at
	// poll time (see Poller.emailAccountFor) and creates the sync stage on first
	// use. An account that never configures the feature is simply skipped.
	s.poller.SetUserAgent(cfg.Feed.UserAgent)
	s.poller.SetTwitterCount(cfg.Feed.TwitterCount)
	s.poller.SetRedditCount(cfg.Feed.RedditCount)
	s.poller.SetHNEnrichCount(cfg.Feed.HNEnrichCount)
	s.poller.SetLobstersEnrichCount(cfg.Feed.LobstersEnrichCount)

	mux := s.mux

	// Health endpoints are unauthenticated so cluster probes work.
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /api/version", s.handleVersion)
	mux.HandleFunc("GET /api/health", s.handleHealth)

	mux.HandleFunc("POST /api/auth/signup", s.handleSignup)
	mux.HandleFunc("POST /api/auth/signin", s.handleSignin)
	mux.HandleFunc("POST /api/auth/refresh", s.handleRefresh)

	mux.HandleFunc("GET /api/auth/me", s.requireAuth(s.handleGetCurrentUser))
	mux.HandleFunc("POST /api/auth/change-password", s.requireAuth(s.handleChangePassword))
	mux.HandleFunc("POST /api/auth/signout", s.requireAuth(s.handleSignout))
	mux.HandleFunc("GET /api/auth/api-key", s.requireAuth(s.handleGetAPIKey))
	mux.HandleFunc("POST /api/auth/api-key/rotate", s.requireAuth(s.handleRotateAPIKey))

	mux.HandleFunc("GET /api/feeds", s.requireAuth(s.handleListFeeds))
	mux.HandleFunc("POST /api/feeds", s.requireAuth(s.handleCreateFeed))
	mux.HandleFunc("POST /api/feeds/discover", s.requireAuth(s.handleDiscoverFeed))
	mux.HandleFunc("GET /api/feeds/{id}", s.requireAuth(s.handleGetFeed))
	mux.HandleFunc("PATCH /api/feeds/{id}", s.requireAuth(s.handleUpdateFeed))
	mux.HandleFunc("DELETE /api/feeds/{id}", s.requireAuth(s.handleDeleteFeed))
	mux.HandleFunc("GET /api/feeds/{id}/items", s.requireAuth(s.handleListFeedItems))
	mux.HandleFunc("POST /api/feeds/{id}/fetch", s.requireAuth(s.handleFetchFeed))
	mux.HandleFunc("POST /api/feeds/{id}/mark-read", s.requireAuth(s.handleMarkFeedRead))
	mux.HandleFunc("POST /api/mark-read", s.requireAuth(s.handleMarkRead))

	mux.HandleFunc("GET /api/unread-counts", s.requireAuth(s.handleUnreadCounts))
	mux.HandleFunc("GET /api/digest-unread-counts", s.requireAuth(s.handleDigestUnreadCounts))
	// Total saved (starred) items for the sidebar badge. Deliberately distinct
	// from the unread counts: "saved" is a status, not an unread tally, and the
	// saved view is assembled client-side across feeds, so it has no length.
	mux.HandleFunc("GET /api/saved-count", s.requireAuth(s.handleSavedCount))

	mux.HandleFunc("PATCH /api/items/{id}", s.requireAuth(s.handleUpdateItemStatus))
	mux.HandleFunc("GET /api/items/{id}", s.requireAuth(s.handleGetItem))
	mux.HandleFunc("GET /api/items/{id}/comments", s.requireAuth(s.handleGetItemComments))
	mux.HandleFunc("POST /api/items/{id}/extract", s.requireAuth(s.handleExtractItem))
	mux.HandleFunc("DELETE /api/items/{id}/extract", s.requireAuth(s.handleDeleteItemExtraction))
	mux.HandleFunc("POST /api/items/{id}/mark-read-above", s.requireAuth(s.handleMarkItemReadAbove))
	mux.HandleFunc("POST /api/items/{id}/mark-read-below", s.requireAuth(s.handleMarkItemReadBelow))
	mux.HandleFunc("POST /api/items/{id}/summarize", s.requireAuth(s.handleSummarizeItem))
	mux.HandleFunc("GET /api/items/multi", s.requireAuth(s.handleListMultiFeedItems))

	mux.HandleFunc("GET /api/digest-configs", s.requireAuth(s.handleListDigestConfigs))
	mux.HandleFunc("POST /api/digest-configs", s.requireAuth(s.handleCreateDigestConfig))
	mux.HandleFunc("GET /api/digest-configs/{id}", s.requireAuth(s.handleGetDigestConfig))
	mux.HandleFunc("PATCH /api/digest-configs/{id}", s.requireAuth(s.handleUpdateDigestConfig))
	mux.HandleFunc("DELETE /api/digest-configs/{id}", s.requireAuth(s.handleDeleteDigestConfig))
	mux.HandleFunc("GET /api/digest-configs/{id}/results", s.requireAuth(s.handleListDigestResults))
	mux.HandleFunc("GET /api/digest-results", s.requireAuth(s.handleListAllDigestResults))

	mux.HandleFunc("POST /api/digest-configs/{id}/generate", s.requireAuth(s.handleGenerateDigest))

	mux.HandleFunc("GET /api/digest-results/{id}", s.requireAuth(s.handleGetDigestResult))
	mux.HandleFunc("PATCH /api/digest-results/{id}", s.requireAuth(s.handleUpdateDigestResultStatus))
	mux.HandleFunc("DELETE /api/digest-results/{id}", s.requireAuth(s.handleDeleteDigestResult))
	mux.HandleFunc("GET /api/digest-results/{id}/items", s.requireAuth(s.handleListDigestResultItemsWithData))
	mux.HandleFunc("POST /api/digest-results/mark-read", s.requireAuth(s.handleMarkDigestResultsRead))
	mux.HandleFunc("POST /api/digest-results/{id}/mark-read-above", s.requireAuth(s.handleMarkDigestReadAbove))
	mux.HandleFunc("POST /api/digest-results/{id}/mark-read-below", s.requireAuth(s.handleMarkDigestReadBelow))

	mux.HandleFunc("GET /api/logs", s.requireAuth(s.handleListLogs))
	mux.HandleFunc("GET /api/metrics", s.requireAuth(s.handleGetMetrics))
	mux.HandleFunc("POST /api/metrics/reset", s.requireAuth(s.handleResetMetrics))

	mux.HandleFunc("GET /api/settings/llm", s.requireAuth(s.handleGetLLMSettings))
	mux.HandleFunc("PUT /api/settings/llm", s.requireAuth(s.handleUpdateLLMSettings))
	mux.HandleFunc("DELETE /api/settings/llm", s.requireAuth(s.handleResetLLMSettings))

	mux.HandleFunc("GET /api/settings/email", s.requireAuth(s.handleGetEmailSettings))
	mux.HandleFunc("PUT /api/settings/email", s.requireAuth(s.handleUpdateEmailSettings))
	mux.HandleFunc("DELETE /api/settings/email", s.requireAuth(s.handleResetEmailSettings))

	mux.HandleFunc("GET /reader/api/0/token", s.readerAuth(s.handleReaderToken))
	mux.HandleFunc("GET /reader/api/0/user-info", s.readerAuth(s.handleReaderUserInfo))
	mux.HandleFunc("POST /reader/api/0/accounts/ClientLogin", s.handleReaderClientLogin)
	mux.HandleFunc("POST /accounts/ClientLogin", s.handleReaderClientLogin)
	mux.HandleFunc("GET /reader/api/0/subscription/list", s.readerAuth(s.handleReaderSubscriptionList))
	mux.HandleFunc("GET /reader/api/0/tag/list", s.readerAuth(s.handleReaderTagList))
	mux.HandleFunc("GET /reader/api/0/subscription/export", s.readerAuth(s.handleReaderSubscriptionExport))
	mux.HandleFunc("GET /reader/api/0/unread-count", s.readerAuth(s.handleReaderUnreadCount))
	mux.HandleFunc("GET /reader/api/0/stream/items/ids", s.readerAuth(s.handleReaderStreamItemsIDs))
	mux.HandleFunc("GET /reader/api/0/stream/items/contents", s.readerAuth(s.handleReaderStreamItemsContents))
	mux.HandleFunc("POST /reader/api/0/stream/items/contents", s.readerAuth(s.handleReaderStreamItemsContents))
	mux.HandleFunc("GET /reader/api/0/stream/contents/{streamID...}", s.readerAuth(s.handleReaderStreamContents))
	mux.HandleFunc("POST /reader/api/0/edit-tag", s.readerAuth(s.handleReaderEditTag))
	mux.HandleFunc("POST /reader/api/0/mark-all-as-read", s.readerAuth(s.handleReaderMarkAllAsRead))
	mux.HandleFunc("POST /reader/api/0/subscription/quickadd", s.readerAuth(s.handleReaderNoOp))
	mux.HandleFunc("POST /reader/api/0/subscription/edit", s.readerAuth(s.handleReaderNoOp))
	mux.HandleFunc("POST /reader/api/0/rename-tag", s.readerAuth(s.handleReaderNoOp))
	mux.HandleFunc("POST /reader/api/0/disable-tag", s.readerAuth(s.handleReaderNoOp))

	return s
}

// extractConcurrency bounds detached per-item extractions. Zero or negative
// config falls back to the default: extraction is I/O-bound (30s upstream
// timeout), so a small multiple of the single SQLite writer keeps throughput
// without queueing unbounded goroutines.
func extractConcurrency(cfg config.Config) int {
	if n := cfg.Feed.ExtractConcurrency; n > 0 {
		return n
	}
	return 4
}

func (s *Server) StartPoller(ctx context.Context, interval time.Duration) {
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		s.poller.Start(ctx, interval)
	}()
}

func (s *Server) StartScheduler(ctx context.Context) {
	var loc *time.Location
	if tzName := s.schedulerTimezone; tzName != "" {
		parsed, err := time.LoadLocation(tzName)
		if err != nil {
			slog.Error("invalid scheduler timezone, using server-local time", "timezone", tzName, "error", err)
		} else {
			loc = parsed
		}
	}
	scheduler := digest.NewScheduler(s.queries, s.generator, loc)
	// Scheduled runs must honor the same per-user model override as manual
	// generations: resolveLLMClient returns the owner's override when set,
	// otherwise the shared default — and *LLMClient satisfies digest.Chatter.
	scheduler.SetLLMResolver(func(ctx context.Context, userID int64) digest.Chatter {
		return s.resolveLLMClient(ctx, userID)
	})
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		scheduler.Start(ctx)
	}()
}

// WaitBackground blocks until the poller, scheduler, and detached extractions
// finish, or ctx expires. Main calls it during shutdown after the HTTP server
// stops accepting connections, so in-flight work drains instead of dying
// mid-write.
func (s *Server) WaitBackground(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.bg.Wait()
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &responseRecorder{ResponseWriter: w}
	defer func() {
		if err := recover(); err != nil {
			slog.Error("panic recovered", "error", err)
			rec.WriteHeader(http.StatusInternalServerError)
			writeJSON(rec, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		}
	}()
	s.mux.ServeHTTP(rec, r)
	status := rec.status
	dur := time.Since(start)
	// All HTTP traffic is debug-level; failed actions that would otherwise
	// be info are promoted to warn (see acceptance criteria).
	slog.Debug("request", "method", r.Method, "host", r.Host, "path", r.URL.Path, "status", status, "duration", dur)
	if status >= 500 && status < 600 {
		slog.Warn("request failed with server error", "method", r.Method, "host", r.Host, "path", r.URL.Path, "status", status, "duration", dur)
	}
}

type responseRecorder struct {
	http.ResponseWriter
	status       int
	wroteHeader  bool
	bytesWritten int64
}

func (rr *responseRecorder) WriteHeader(code int) {
	if !rr.wroteHeader {
		rr.status = code
		rr.wroteHeader = true
	}
	rr.ResponseWriter.WriteHeader(code)
}

func (rr *responseRecorder) Write(b []byte) (int, error) {
	if !rr.wroteHeader {
		rr.WriteHeader(http.StatusOK)
	}
	n, err := rr.ResponseWriter.Write(b)
	rr.bytesWritten += int64(n)
	return n, err
}
