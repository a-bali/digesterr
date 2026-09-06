package httpapi

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strings"
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
	}

	llmClient := digest.NewLLMClient(cfg.LLM.Endpoint, cfg.LLM.APIKey, cfg.LLM.Model)
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

	mux.HandleFunc("POST /api/auth/change-password", s.requireAuth(s.handleChangePassword))
	mux.HandleFunc("POST /api/auth/signout", s.requireAuth(s.handleSignout))
	mux.HandleFunc("GET /api/auth/api-key", s.requireAuth(s.handleGetAPIKey))
	mux.HandleFunc("POST /api/auth/api-key/rotate", s.requireAuth(s.handleRotateAPIKey))

	mux.HandleFunc("GET /api/feeds", s.requireAuth(s.handleListFeeds))
	mux.HandleFunc("POST /api/feeds", s.requireAuth(s.handleCreateFeed))
	mux.HandleFunc("GET /api/feeds/{id}", s.requireAuth(s.handleGetFeed))
	mux.HandleFunc("PATCH /api/feeds/{id}", s.requireAuth(s.handleUpdateFeed))
	mux.HandleFunc("DELETE /api/feeds/{id}", s.requireAuth(s.handleDeleteFeed))
	mux.HandleFunc("GET /api/feeds/{id}/items", s.requireAuth(s.handleListFeedItems))
	mux.HandleFunc("POST /api/feeds/{id}/fetch", s.requireAuth(s.handleFetchFeed))
	mux.HandleFunc("POST /api/feeds/{id}/mark-read", s.requireAuth(s.handleMarkFeedRead))

	mux.HandleFunc("GET /api/unread-counts", s.requireAuth(s.handleUnreadCounts))
	mux.HandleFunc("GET /api/digest-unread-counts", s.requireAuth(s.handleDigestUnreadCounts))

	mux.HandleFunc("PATCH /api/items/{id}", s.requireAuth(s.handleUpdateItemStatus))
	mux.HandleFunc("GET /api/items/{id}", s.requireAuth(s.handleGetItem))
	mux.HandleFunc("GET /api/items/{id}/comments", s.requireAuth(s.handleGetItemComments))
	mux.HandleFunc("POST /api/items/{id}/extract", s.requireAuth(s.handleExtractItem))
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

	mux.HandleFunc("GET /api/logs", s.requireAuth(s.handleListLogs))
	mux.HandleFunc("GET /api/metrics", s.requireAuth(s.handleGetMetrics))
	mux.HandleFunc("POST /api/metrics/reset", s.requireAuth(s.handleResetMetrics))

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

func (s *Server) StartPoller(ctx context.Context, interval time.Duration) {
	go s.poller.Start(ctx, interval)
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
	go scheduler.Start(ctx)
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
