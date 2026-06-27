package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"digesterr/internal/auth"
	"digesterr/internal/digest"
	"digesterr/internal/feed"
	"digesterr/internal/store"
)

type Server struct {
	mux       *http.ServeMux
	queries   *store.Queries
	auth      *auth.Service
	poller    *feed.Poller
	generator *digest.Generator
}

func NewServer(queries *store.Queries, authSvc *auth.Service) *Server {
	s := &Server{
		mux:     http.NewServeMux(),
		queries: queries,
		auth:    authSvc,
		poller:  feed.NewPoller(queries),
	}

	llmEndpoint := envOr("LLM_ENDPOINT", "https://api.openai.com/v1")
	llmAPIKey := envOr("LLM_API_KEY", "")
	llmModel := envOr("LLM_MODEL", "gpt-4o-mini")
	llmClient := digest.NewLLMClient(llmEndpoint, llmAPIKey, llmModel)
	s.generator = digest.NewGenerator(queries, llmClient, s.poller)

	mux := s.mux

	mux.HandleFunc("POST /api/auth/signup", s.handleSignup)
	mux.HandleFunc("POST /api/auth/signin", s.handleSignin)
	mux.HandleFunc("POST /api/auth/refresh", s.handleRefresh)

	mux.HandleFunc("POST /api/auth/change-password", s.requireAuth(s.handleChangePassword))
	mux.HandleFunc("POST /api/auth/signout", s.requireAuth(s.handleSignout))

	mux.HandleFunc("GET /api/categories", s.requireAuth(s.handleListCategories))
	mux.HandleFunc("POST /api/categories", s.requireAuth(s.handleCreateCategory))
	mux.HandleFunc("GET /api/categories/{id}", s.requireAuth(s.handleGetCategory))
	mux.HandleFunc("PATCH /api/categories/{id}", s.requireAuth(s.handleUpdateCategory))
	mux.HandleFunc("DELETE /api/categories/{id}", s.requireAuth(s.handleDeleteCategory))

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
	if status >= 500 && status < 600 {
		slog.Warn("request failed with server error", "method", r.Method, "path", r.URL.Path, "status", status)
	}
	slog.Info("request", "method", r.Method, "path", r.URL.Path, "status", status, "duration", time.Since(start))
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

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}