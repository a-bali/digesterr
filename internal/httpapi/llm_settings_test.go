package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"digesterr/internal/auth"
	"digesterr/internal/config"
	"digesterr/internal/logbuf"
	"digesterr/internal/store"
)

// setupLLMSettingsServer builds a server whose env default model is
// "env-default-model" and returns it with a signed-in user.
func setupLLMSettingsServer(t *testing.T) (*Server, store.User, string) {
	t.Helper()
	db, queries := setupTestDB(t)
	authSvc := auth.NewService("test-jwt-secret-for-testing", queries, db)
	cfg := config.Default()
	cfg.LLM.Model = "env-default-model"
	server := NewServer(db, queries, authSvc, logbuf.New(100), cfg)
	user := createTestUser(t, queries, "llm-settings@example.com", "password123")
	return server, user, getTestToken(t, server.auth, &user)
}

func decodeLLMSettings(t *testing.T, w *httptest.ResponseRecorder) llmSettingsResponse {
	t.Helper()
	var resp llmSettingsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding settings response: %v (body %q)", err, w.Body.String())
	}
	return resp
}

func TestGetLLMSettingsDefaultsToEnv(t *testing.T) {
	server, _, token := setupLLMSettingsServer(t)

	w := makeRequest(server, http.MethodGet, "/api/settings/llm", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200: %s", w.Code, w.Body.String())
	}
	resp := decodeLLMSettings(t, w)
	if resp.Model != "env-default-model" || resp.DefaultModel != "env-default-model" || resp.Overridden {
		t.Errorf("GET = %+v, want model+default env-default-model, overridden=false", resp)
	}
}

func TestUpdateLLMSettingsSetsOverride(t *testing.T) {
	server, user, token := setupLLMSettingsServer(t)

	w := makeRequest(server, http.MethodPut, "/api/settings/llm", map[string]string{"model": "  custom-model  "}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200: %s", w.Code, w.Body.String())
	}
	resp := decodeLLMSettings(t, w)
	if resp.Model != "custom-model" || resp.DefaultModel != "env-default-model" || !resp.Overridden {
		t.Errorf("PUT = %+v, want model custom-model, default env-default-model, overridden=true", resp)
	}

	stored, err := server.queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("reading user: %v", err)
	}
	if stored.LlmModel != "custom-model" {
		t.Errorf("stored llm_model = %q, want trimmed %q", stored.LlmModel, "custom-model")
	}

	w = makeRequest(server, http.MethodGet, "/api/settings/llm", nil, token)
	if got := decodeLLMSettings(t, w); got.Model != "custom-model" || !got.Overridden {
		t.Errorf("GET after PUT = %+v, want the override", got)
	}
}

func TestUpdateLLMSettingsSavingDefaultStoresNoOverride(t *testing.T) {
	server, user, token := setupLLMSettingsServer(t)

	w := makeRequest(server, http.MethodPut, "/api/settings/llm", map[string]string{"model": "env-default-model"}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if resp := decodeLLMSettings(t, w); resp.Model != "env-default-model" || resp.Overridden {
		t.Errorf("PUT default = %+v, want env default without override", resp)
	}
	stored, err := server.queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("reading user: %v", err)
	}
	if stored.LlmModel != "" {
		t.Errorf("stored llm_model = %q, want empty (no override pinned)", stored.LlmModel)
	}
}

func TestUpdateLLMSettingsRejectsBadInput(t *testing.T) {
	server, _, token := setupLLMSettingsServer(t)

	w := makeRequest(server, http.MethodPut, "/api/settings/llm", map[string]string{"model": strings.Repeat("m", 201)}, token)
	if w.Code != http.StatusBadRequest {
		t.Errorf("overlong model: status = %d, want 400", w.Code)
	}

	w = makeRequest(server, http.MethodPut, "/api/settings/llm", "not-an-object", token)
	if w.Code != http.StatusBadRequest {
		t.Errorf("malformed body: status = %d, want 400", w.Code)
	}
}

func TestResetLLMSettingsFallsBackToEnv(t *testing.T) {
	server, user, token := setupLLMSettingsServer(t)

	if _, err := server.queries.UpdateUserLLMModel(t.Context(), store.UpdateUserLLMModelParams{LlmModel: "custom-model", ID: user.ID}); err != nil {
		t.Fatalf("seeding override: %v", err)
	}

	w := makeRequest(server, http.MethodDelete, "/api/settings/llm", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if resp := decodeLLMSettings(t, w); resp.Model != "env-default-model" || resp.Overridden {
		t.Errorf("DELETE = %+v, want env default without override", resp)
	}

	// An empty PUT is the same reset: it must not store an empty model name.
	w = makeRequest(server, http.MethodPut, "/api/settings/llm", map[string]string{"model": "   "}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("empty PUT status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if resp := decodeLLMSettings(t, w); resp.Model != "env-default-model" || resp.Overridden {
		t.Errorf("empty PUT = %+v, want a reset to the env default", resp)
	}
}

func TestResolveLLMClientHonorsOverride(t *testing.T) {
	server, user, _ := setupLLMSettingsServer(t)

	if got := server.resolveLLMClient(t.Context(), user.ID); got != server.llmClient {
		t.Error("no override: want the shared default client, not a copy")
	}
	if got := server.resolveLLMClient(t.Context(), user.ID).Model(); got != "env-default-model" {
		t.Errorf("default client model = %q, want env-default-model", got)
	}

	if _, err := server.queries.UpdateUserLLMModel(t.Context(), store.UpdateUserLLMModelParams{LlmModel: "user-model", ID: user.ID}); err != nil {
		t.Fatalf("seeding override: %v", err)
	}
	resolved := server.resolveLLMClient(t.Context(), user.ID)
	if resolved == server.llmClient {
		t.Fatal("override: got the shared client, want a WithModel copy")
	}
	if resolved.Model() != "user-model" {
		t.Errorf("override client model = %q, want user-model", resolved.Model())
	}
	if server.llmClient.Model() != "env-default-model" {
		t.Errorf("shared client model changed to %q, WithModel must copy", server.llmClient.Model())
	}
}

func TestResolveLLMClientUnknownUserUsesDefault(t *testing.T) {
	server, _, _ := setupLLMSettingsServer(t)
	if got := server.resolveLLMClient(t.Context(), 999999).Model(); got != "env-default-model" {
		t.Errorf("unknown user model = %q, want the env default", got)
	}
}

// The model list rides on GET /api/settings/llm so the dialog can offer a picker.
func TestGetLLMSettingsIncludesModelList(t *testing.T) {
	server, _, token := setupLLMSettingsServer(t)
	server.SetLLMModelsFunc(func(context.Context) ([]string, error) {
		return []string{"virtual-chat", "virtual-free"}, nil
	})

	w := makeRequest(server, http.MethodGet, "/api/settings/llm", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	resp := decodeLLMSettings(t, w)
	if len(resp.Models) != 2 || resp.Models[0] != "virtual-chat" {
		t.Errorf("models = %v, want the endpoint list", resp.Models)
	}
}

// The list is a convenience, so a failing endpoint must NOT break loading the
// settings: the dialog then shows a plain text field. Asserting the 200 keeps a
// future refactor from turning this into an error path.
func TestGetLLMSettingsSurvivesModelListFailure(t *testing.T) {
	server, _, token := setupLLMSettingsServer(t)
	server.SetLLMModelsFunc(func(context.Context) ([]string, error) {
		return nil, errors.New("gateway unreachable")
	})

	w := makeRequest(server, http.MethodGet, "/api/settings/llm", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even when the model list fails (body %s)", w.Code, w.Body.String())
	}
	resp := decodeLLMSettings(t, w)
	if len(resp.Models) != 0 {
		t.Errorf("models = %v, want empty on failure", resp.Models)
	}
	if resp.Model != "env-default-model" {
		t.Errorf("model = %q, want the effective model still reported", resp.Model)
	}
}
