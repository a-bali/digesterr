package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"digesterr/internal/digest"
	"digesterr/internal/store"
)

// maxLLMModelLength caps a per-user model override. Model names and gateway
// aliases are short strings; anything longer is a paste mistake, and the value
// rides on every outbound LLM request, so it stays bounded.
const maxLLMModelLength = 200

// llmSettingsResponse is the JSON shape of GET /api/settings/llm. model is the
// effective model for the caller (their override, or the env default when they
// have none); default_model is the env default the reset button falls back to;
// overridden reports whether model came from their own setting.
type llmSettingsResponse struct {
	Model        string `json:"model"`
	DefaultModel string `json:"default_model"`
	Overridden   bool   `json:"overridden"`
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
	})
}

// handleUpdateLLMSettings stores the caller's model override. An empty model
// clears the override (same as DELETE): the request then means "reset", not
// "use an empty model name".
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
