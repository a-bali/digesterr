package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"digesterr/internal/auth"
	"digesterr/internal/store"
)

type Server struct {
	mux     *http.ServeMux
	queries *store.Queries
	auth    *auth.Service
}

func NewServer(queries *store.Queries, authSvc *auth.Service) *Server {
	s := &Server{
		mux:     http.NewServeMux(),
		queries: queries,
		auth:    authSvc,
	}

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

	mux.HandleFunc("PATCH /api/items/{id}", s.requireAuth(s.handleUpdateItemStatus))

	mux.HandleFunc("GET /api/digest-configs", s.requireAuth(s.handleListDigestConfigs))
	mux.HandleFunc("POST /api/digest-configs", s.requireAuth(s.handleCreateDigestConfig))
	mux.HandleFunc("GET /api/digest-configs/{id}", s.requireAuth(s.handleGetDigestConfig))
	mux.HandleFunc("PATCH /api/digest-configs/{id}", s.requireAuth(s.handleUpdateDigestConfig))
	mux.HandleFunc("DELETE /api/digest-configs/{id}", s.requireAuth(s.handleDeleteDigestConfig))
	mux.HandleFunc("GET /api/digest-configs/{id}/results", s.requireAuth(s.handleListDigestResults))

	mux.HandleFunc("GET /api/digest-results/{id}", s.requireAuth(s.handleGetDigestResult))
	mux.HandleFunc("PATCH /api/digest-results/{id}", s.requireAuth(s.handleUpdateDigestResultStatus))
	mux.HandleFunc("DELETE /api/digest-results/{id}", s.requireAuth(s.handleDeleteDigestResult))

	return s
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
	slog.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration", time.Since(start))
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.status = code
	rr.ResponseWriter.WriteHeader(code)
}