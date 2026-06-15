package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"digesterr/internal/store"
)

type createCategoryRequest struct {
	Name string `json:"name"`
}

func (s *Server) handleListCategories(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	categories, err := s.queries.ListCategories(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list categories")
		return
	}
	if categories == nil {
		categories = []store.Category{}
	}
	writeJSON(w, http.StatusOK, categories)
}

func (s *Server) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req createCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	cat, err := s.queries.CreateCategory(r.Context(), store.CreateCategoryParams{
		UserID: userID,
		Name:   req.Name,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create category")
		return
	}
	writeJSON(w, http.StatusCreated, cat)
}

func (s *Server) handleGetCategory(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	cat, err := s.queries.GetCategory(r.Context(), store.GetCategoryParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "category not found")
		return
	}
	writeJSON(w, http.StatusOK, cat)
}

func (s *Server) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req createCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cat, err := s.queries.UpdateCategory(r.Context(), store.UpdateCategoryParams{
		ID:     id,
		UserID: userID,
		Name:   req.Name,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "category not found")
		return
	}
	writeJSON(w, http.StatusOK, cat)
}

func (s *Server) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	deleted, err := s.queries.DeleteCategory(r.Context(), store.DeleteCategoryParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil || deleted == 0 {
		writeError(w, http.StatusNotFound, "category not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "deleted"})
}