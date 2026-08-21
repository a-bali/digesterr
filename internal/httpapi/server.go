package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"digesterr/internal/auth"
	"digesterr/internal/config"
	"digesterr/internal/digest"
	"digesterr/internal/feed"
	"digesterr/internal/logbuf"
	"digesterr/internal/store"
)

type Server struct {
	mux       *http.ServeMux
	queries   *store.Queries
	auth      *auth.Service
	poller    *feed.Poller
	generator *digest.Generator
	logs      *logbuf.Buffer
	// agentMailConfigured reports whether agentmail credentials are set,
	// exposed via /api/health so the UI can enable the email-delivery option.
	agentMailConfigured bool
	// publicBaseURL is the absolute base URL of the digesterr web UI, used to
	// build in-app links surfaced to Reader clients (feed home pages, digest
	// article links). Empty means "derive from the request host".
	publicBaseURL string
}

func NewServer(queries *store.Queries, authSvc *auth.Service, logs *logbuf.Buffer, cfg config.Config) *Server {
	s := &Server{
		mux:           http.NewServeMux(),
		queries:       queries,
		auth:          authSvc,
		poller:        feed.NewPoller(queries),
		logs:          logs,
		publicBaseURL: strings.TrimRight(cfg.Server.PublicBaseURL, "/"),
	}

	llmClient := digest.NewLLMClient(cfg.LLM.Endpoint, cfg.LLM.APIKey, cfg.LLM.Model)
	s.generator = digest.NewGenerator(queries, llmClient, s.poller)
	// Optional agentmail email delivery: enabled only when both credentials
	// are set; otherwise the generator's sender stays nil and the feature is
	// inert.
	s.generator.SetEmailSender(digest.NewAgentMailSender(cfg.AgentMail.Token, cfg.AgentMail.InboxID))
	s.agentMailConfigured = cfg.AgentMail.Token != "" && cfg.AgentMail.InboxID != ""

	// Data retention and provider credentials come from the merged config
	// (defaults < config file < env < CLI flags). The expiry age is shared by
	// feed refresh (Poller) and digest generation (Generator calls the same
	// Poller expiry path).
	s.poller.SetExpiry(cfg.Feed.ExpireItems.Duration(), cfg.Feed.ExpireDigests.Duration())
	s.poller.SetRedditCredentials(cfg.Reddit.ClientID, cfg.Reddit.ClientSecret)

	mux := s.mux

	// Health endpoints are unauthenticated so cluster probes work.
	mux.HandleFunc("GET /healthz", s.handleHealthz)
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
	mux.HandleFunc("POST /api/items/{id}/extract", s.requireAuth(s.handleExtractItem))

	mux.HandleFunc("GET /api/digest-configs", s.requireAuth(s.handleListDigestConfigs))
	mux.HandleFunc("POST /api/digest-configs", s.requireAuth(s.handleCreateDigestConfig))
	mux.HandleFunc("GET /api/digest-configs/{id}", s.requireAuth(s.handleGetDigestConfig))
	mux.HandleFunc("PATCH /api/digest-configs/{id}", s.requireAuth(s.handleUpdateDigestConfig))
	mux.HandleFunc("DELETE /api/digest-configs/{id}", s.requireAuth(s.handleDeleteDigestConfig))
	mux.HandleFunc("GET /api/digest-configs/{id}/results", s.requireAuth(s.handleListDigestResults))

	mux.HandleFunc("POST /api/digest-configs/{id}/generate", s.requireAuth(s.handleGenerateDigest))

	mux.HandleFunc("GET /api/digest-results/{id}", s.requireAuth(s.handleGetDigestResult))
	mux.HandleFunc("PATCH /api/digest-results/{id}", s.requireAuth(s.handleUpdateDigestResultStatus))
	mux.HandleFunc("DELETE /api/digest-results/{id}", s.requireAuth(s.handleDeleteDigestResult))
	mux.HandleFunc("GET /api/digest-results/{id}/items", s.requireAuth(s.handleListDigestResultItemsWithData))

	mux.HandleFunc("GET /api/logs", s.requireAuth(s.handleListLogs))

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
	scheduler := digest.NewScheduler(s.queries, s.generator)
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
	return rr.ResponseWriter.Write(b)
}