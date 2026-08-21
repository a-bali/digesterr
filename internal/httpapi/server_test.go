package httpapi

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"digesterr/internal/auth"
	"digesterr/internal/config"
	"digesterr/internal/logbuf"
	"digesterr/internal/store"
)

func setupTestDB(t *testing.T) (*sql.DB, *store.Queries) {
	t.Helper()
	db, err := openTestDB()
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, store.New(db)
}

func setupTestServer(t *testing.T) (*Server, *store.Queries) {
	t.Helper()
	db, queries := setupTestDB(t)
	authSvc := auth.NewService("test-jwt-secret-for-testing", queries)
	server := NewServer(db, queries, authSvc, logbuf.New(100), config.Default())
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

// TestAuthResponsesDoNotLeakSecrets guards against serializing the bcrypt
// hash or Reader API key inside the user object of auth responses.
func TestAuthResponsesDoNotLeakSecrets(t *testing.T) {
	server, _ := setupTestServer(t)

	w := makeRequest(server, http.MethodPost, "/api/auth/signup", map[string]string{
		"email":    "leak@example.com",
		"password": "password123",
	}, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("signup: expected 201, got %d", w.Code)
	}
	var signup struct {
		User   map[string]any `json:"user"`
		APIKey string         `json:"api_key"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &signup); err != nil {
		t.Fatalf("decode signup response: %v", err)
	}
	for _, field := range []string{"password_hash", "api_key"} {
		if _, present := signup.User[field]; present {
			t.Errorf("signup user object contains %q", field)
		}
	}
	if signup.APIKey == "" {
		t.Error("signup response must still return api_key at top level")
	}

	w = makeRequest(server, http.MethodPost, "/api/auth/signin", map[string]string{
		"email":    "leak@example.com",
		"password": "password123",
	}, "")
	if w.Code != http.StatusOK {
		t.Fatalf("signin: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var signin struct {
		User map[string]any `json:"user"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &signin); err != nil {
		t.Fatalf("decode signin response: %v", err)
	}
	for _, field := range []string{"password_hash", "api_key"} {
		if _, present := signin.User[field]; present {
			t.Errorf("signin user object contains %q", field)
		}
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
	w := makeRequest(server, http.MethodGet, "/api/feeds", nil, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", w.Code)
	}
}

func TestFeedCRUD(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createTestUser(t, queries, "feed@example.com", "password123")
	token := getTestToken(t, server.auth, &user)

	body := map[string]interface{}{
		"category": "Tech",
		"type":     "rss",
		"name":     "Test Feed",
		"url":      "https://example.com/feed.xml",
	}
	w := makeRequest(server, http.MethodPost, "/api/feeds", body, token)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created store.Feed
	json.NewDecoder(w.Body).Decode(&created)
	if created.Category != "Tech" {
		t.Fatalf("expected category 'Tech', got %q", created.Category)
	}

	w = makeRequest(server, http.MethodGet, "/api/feeds", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var feeds []store.Feed
	json.NewDecoder(w.Body).Decode(&feeds)
	if len(feeds) != 1 || feeds[0].Name != "Test Feed" {
		t.Fatalf("expected 1 feed named Test Feed, got %v", feeds)
	}

	// Updating with an empty category removes it.
	w = makeRequest(server, http.MethodPatch, "/api/feeds/"+itoa(created.ID), map[string]interface{}{
		"category": "",
	}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated store.Feed
	json.NewDecoder(w.Body).Decode(&updated)
	if updated.Category != "" {
		t.Fatalf("expected empty category after clearing, got %q", updated.Category)
	}
}