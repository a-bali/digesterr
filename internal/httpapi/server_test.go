package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"digesterr/internal/auth"
	"digesterr/internal/logbuf"
	"digesterr/internal/store"
)

func setupTestDB(t *testing.T) *store.Queries {
	t.Helper()
	db, err := openTestDB()
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return store.New(db)
}

func setupTestServer(t *testing.T) (*Server, *store.Queries) {
	t.Helper()
	queries := setupTestDB(t)
	authSvc := auth.NewService("test-jwt-secret-for-testing", queries)
	server := NewServer(queries, authSvc, logbuf.New(100))
	return server, queries
}

func makeRequest(server *Server, method, path string, body interface{}, token string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if buf.Len() > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	return w
}

func createTestUser(t *testing.T, queries *store.Queries, email, password string) store.User {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}
	user, err := queries.CreateUser(t.Context(), store.CreateUserParams{
		Email:        email,
		PasswordHash: hash,
		ApiKey:       "",
	})
	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}
	return user
}

func getTestToken(t *testing.T, authSvc *auth.Service, user *store.User) string {
	t.Helper()
	token, err := authSvc.GenerateAccessToken(user)
	if err != nil {
		t.Fatalf("failed to generate test token: %v", err)
	}
	return token
}

func TestSignup(t *testing.T) {
	server, _ := setupTestServer(t)
	body := map[string]string{
		"email":    "test@example.com",
		"password": "password123",
	}
	w := makeRequest(server, http.MethodPost, "/api/auth/signup", body, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSignupDuplicateEmail(t *testing.T) {
	server, _ := setupTestServer(t)
	body := map[string]string{
		"email":    "test@example.com",
		"password": "password123",
	}
	w := makeRequest(server, http.MethodPost, "/api/auth/signup", body, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", w.Code)
	}
	w = makeRequest(server, http.MethodPost, "/api/auth/signup", body, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("expected status 409 for duplicate, got %d", w.Code)
	}
}

func TestSignupMissingFields(t *testing.T) {
	server, _ := setupTestServer(t)
	w := makeRequest(server, http.MethodPost, "/api/auth/signup", map[string]string{"email": ""}, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
}

func TestSignin(t *testing.T) {
	server, queries := setupTestServer(t)
	createTestUser(t, queries, "test@example.com", "password123")
	body := map[string]string{
		"email":    "test@example.com",
		"password": "password123",
	}
	w := makeRequest(server, http.MethodPost, "/api/auth/signin", body, "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSigninWrongPassword(t *testing.T) {
	server, queries := setupTestServer(t)
	createTestUser(t, queries, "test@example.com", "password123")
	body := map[string]string{
		"email":    "test@example.com",
		"password": "wrongpassword",
	}
	w := makeRequest(server, http.MethodPost, "/api/auth/signin", body, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", w.Code)
	}
}

func TestUnauthenticatedAccess(t *testing.T) {
	server, _ := setupTestServer(t)
	w := makeRequest(server, http.MethodGet, "/api/categories", nil, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", w.Code)
	}
}

func TestCategoryCRUD(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "cat@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	w := makeRequest(server, http.MethodPost, "/api/categories", map[string]string{"name": "Tech"}, token)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	w = makeRequest(server, http.MethodGet, "/api/categories", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var categories []store.Category
	json.NewDecoder(w.Body).Decode(&categories)
	if len(categories) != 1 || categories[0].Name != "Tech" {
		t.Fatalf("expected 1 category named Tech, got %v", categories)
	}
}

func TestFeedCRUD(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "feed@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	cat, _ := queries.CreateCategory(t.Context(), store.CreateCategoryParams{
		UserID: user.ID,
		Name:   "Tech",
	})

	body := map[string]interface{}{
		"category_id": cat.ID,
		"type":        "rss",
		"name":        "Test Feed",
		"url":         "https://example.com/feed.xml",
	}
	w := makeRequest(server, http.MethodPost, "/api/feeds", body, token)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	w = makeRequest(server, http.MethodGet, "/api/feeds", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestDeleteCategory(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "delcat@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	_, _ = queries.CreateCategory(t.Context(), store.CreateCategoryParams{
		UserID: user.ID,
		Name:   "ToDelete",
	})

	w := makeRequest(server, http.MethodGet, "/api/categories", nil, token)
	var categories []store.Category
	json.NewDecoder(w.Body).Decode(&categories)
	catID := categories[0].ID

	w = makeRequest(server, http.MethodDelete, "/api/categories/"+itoa(catID), nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}