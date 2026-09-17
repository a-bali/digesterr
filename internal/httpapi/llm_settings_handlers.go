package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"digesterr/internal/digest"
	"digesterr/internal/store"
)

// maxLLMModelLength caps a per-user model override. Model names and gateway
// aliases are short strings; anything longer is a paste mistake, and the value
// rides on every outbound LLM request, so it stays bounded.
const maxLLMModelLength = 200

// llmSettingsResponse is the JSON shape returned by every /api/settings/llm
// handler. model is the effective model for the caller (their override, or the
// env default when they have none); default_model is the env default the reset
// button falls back to; overridden reports whether model came from their own
// setting. models is the endpoint's offered list, used to populate the picker —
// it is advisory, so it is empty (never an error) when the list cannot be
// fetched. It is filled on GET only: PUT and DELETE answer with a null list,
// which the client must read as "unchanged", not as "no models available".
type llmSettingsResponse struct {
	Model        string   `json:"model"`
	DefaultModel string   `json:"default_model"`
	Overridden   bool     `json:"overridden"`
	Models       []string `json:"models"`
}

// llmModelsTimeout bounds the advisory model-list call. A short budget is right
// because the dialog must open promptly whether or not the endpoint answers; a
// slow list degrades to a text-only field.
const llmModelsTimeout = 10 * time.Second

// llmModelsFunc substitutes the model-list call. Tests use it; nil means the real
// endpoint.
func (s *Server) SetLLMModelsFunc(f func(ctx context.Context) ([]string, error)) {
	s.llmModels = f
}

// availableModels returns the endpoint's model list, or nil when it cannot be
// read. Errors are swallowed deliberately: the picker is a convenience and the
// dialog works without it, so a failure must not break loading the settings.
func (s *Server) availableModels(ctx context.Context) []string {
	if s.llmModels != nil {
		models, err := s.llmModels(ctx)
		if err != nil {
			slog.Debug("model list unavailable", "error", err)
			return nil
		}
		return models
	}
	if s.llmClient == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, llmModelsTimeout)
	defer cancel()
	models, err := s.llmClient.Models(ctx)
	if err != nil {
		slog.Debug("model list unavailable", "error", err)
		return nil
	}
	return models
}

// resolveLLMClient returns the outbound LLM client for a user: their override
// when set, otherwise the shared default client. Every LLM call site must go
// through here so scheduled digests, manual generation, and item summaries all
// honor the same setting.
func (s *Server) resolveLLMClient(ctx context.Context, userID int64) *digest.LLMClient {
	model := s.llmDefaultModel
	if user, err := s.queries.GetUserByID(ctx, userID); err == nil {
		if override := strings.TrimSpace(user.LlmModel); override != "" {
			model = override
		}
	}
	if model == "" || model == s.llmClient.Model() {
		return s.llmClient
	}
	return s.llmClient.WithModel(model)
}

// handleGetLLMSettings reports the caller's effective LLM model, the env
// default, and whether an override is active.
func (s *Server) handleGetLLMSettings(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)

	override := ""
	if user, err := s.queries.GetUserByID(r.Context(), userID); err == nil {
		override = strings.TrimSpace(user.LlmModel)
	}

	model := s.llmDefaultModel
	overridden := false
	if override != "" {
		model = override
		overridden = true
	}
	writeJSON(w, http.StatusOK, llmSettingsResponse{
		Model:        model,
		DefaultModel: s.llmDefaultModel,
		Overridden:   overridden,
		Models:       s.availableModels(r.Context()),
	})
}

// handleUpdateLLMSettings stores the caller's model override. An empty model
// clears the override, as does DELETE: the request then means "reset", not "use
// an empty model name".
func (s *Server) handleUpdateLLMSettings(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)

	var req struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	model := strings.TrimSpace(req.Model)
	if len(model) > maxLLMModelLength {
		writeError(w, http.StatusBadRequest, "model name too long")
		return
	}
	// Saving the configured default is the same intent as Reset: store no
	// override rather than pinning the current value, so a later change to the
	// server default is picked up instead of silently shadowed by an identical
	// override the user never meant to create.
	if model == s.llmDefaultModel {
		model = ""
	}

	if _, err := s.queries.UpdateUserLLMModel(r.Context(), store.UpdateUserLLMModelParams{
		LlmModel: model,
		ID:       userID,
	}); err != nil {
		writeServerError(w, "failed to save LLM model", err)
		return
	}

	effective := s.llmDefaultModel
	overridden := false
	if model != "" {
		effective = model
		overridden = true
	}
	writeJSON(w, http.StatusOK, llmSettingsResponse{
		Model:        effective,
		DefaultModel: s.llmDefaultModel,
		Overridden:   overridden,
	})
}

// handleResetLLMSettings clears the caller's model override so the env default
// applies again.
func (s *Server) handleResetLLMSettings(w http.ResponseWriter, r *http.Request) {
	userID := authedUserID(r)

	if _, err := s.queries.UpdateUserLLMModel(r.Context(), store.UpdateUserLLMModelParams{
		LlmModel: "",
		ID:       userID,
	}); err != nil {
		writeServerError(w, "failed to reset LLM model", err)
		return
	}
	writeJSON(w, http.StatusOK, llmSettingsResponse{
		Model:        s.llmDefaultModel,
		DefaultModel: s.llmDefaultModel,
		Overridden:   false,
	})
}
