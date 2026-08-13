package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"digesterr/internal/auth"
	"digesterr/internal/store"
)

// generateAPIKey returns a random 32-byte key hex-encoded (64 lowercase hex chars).
func generateAPIKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func refreshCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	secure := r.TLS != nil
	return &http.Cookie{
		Name:     "refresh_token",
		Value:    value,
		Path:     "/api/auth/refresh",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

type signupRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	var req signupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password are required")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	apiKey, err := generateAPIKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate api key")
		return
	}

	user, err := s.queries.CreateUser(r.Context(), store.CreateUserParams{
		Email:        req.Email,
		PasswordHash: hash,
		ApiKey:       apiKey,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "email already exists")
		return
	}

	accessToken, err := s.auth.GenerateAccessToken(&user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	refreshToken, err := s.auth.CreateRefreshToken(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create refresh token")
		return
	}

	http.SetCookie(w, refreshCookie(r, refreshToken, int(auth.RefreshTokenDuration.Seconds())))

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"user":         user,
		"access_token": accessToken,
		"api_key":      apiKey,
	})
}

type signinRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) handleSignin(w http.ResponseWriter, r *http.Request) {
	var req signinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user, err := s.queries.GetUserByEmail(r.Context(), req.Email)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if !auth.CheckPassword(req.Password, user.PasswordHash) {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	accessToken, err := s.auth.GenerateAccessToken(&user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	refreshToken, err := s.auth.CreateRefreshToken(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create refresh token")
		return
	}

	http.SetCookie(w, refreshCookie(r, refreshToken, int(auth.RefreshTokenDuration.Seconds())))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user":         user,
		"access_token": accessToken,
	})
}

func (s *Server) handleSignout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("refresh_token"); err == nil {
		s.auth.DeleteRefreshToken(r.Context(), cookie.Value)
	}
	http.SetCookie(w, refreshCookie(r, "", -1))
	writeJSON(w, http.StatusOK, map[string]string{"message": "signed out"})
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("refresh_token")
	if err != nil {
		var req refreshRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RefreshToken == "" {
			writeError(w, http.StatusUnauthorized, "no refresh token provided")
			return
		}
		cookie = &http.Cookie{Value: req.RefreshToken}
	}

	rt, err := s.auth.ValidateRefreshToken(r.Context(), cookie.Value)
	if err != nil {
		if errors.Is(err, auth.ErrTokenExpired) {
			writeError(w, http.StatusUnauthorized, "refresh token expired")
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}

	user, err := s.queries.GetUserByID(r.Context(), rt.UserID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}

	s.auth.DeleteRefreshToken(r.Context(), cookie.Value)
	accessToken, err := s.auth.GenerateAccessToken(&user)
	if err != nil {
		slog.Error("refresh token: failed to generate access token", "error", err, "user_id", user.ID)
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	newRefresh, err := s.auth.CreateRefreshToken(r.Context(), user.ID)
	if err != nil {
		slog.Error("refresh token: failed to create refresh token", "error", err, "user_id", user.ID)
		writeError(w, http.StatusInternalServerError, "failed to create refresh token")
		return
	}

	slog.Debug("token refreshed", "user_id", user.ID, "access_token", accessToken[:8]+"...")

	http.SetCookie(w, refreshCookie(r, newRefresh, int(auth.RefreshTokenDuration.Seconds())))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"access_token": accessToken,
	})
}

type changePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.OldPassword == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "old_password and new_password are required")
		return
	}

	user, err := s.queries.GetUserByID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}
	if !auth.CheckPassword(req.OldPassword, user.PasswordHash) {
		writeError(w, http.StatusUnauthorized, "invalid old password")
		return
	}

	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	_, err = s.queries.UpdateUserPassword(r.Context(), store.UpdateUserPasswordParams{
		ID:           userID,
		PasswordHash: newHash,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update password")
		return
	}

	s.auth.DeleteAllUserRefreshTokens(r.Context(), userID)
	http.SetCookie(w, refreshCookie(r, "", -1))
	writeJSON(w, http.StatusOK, map[string]string{"message": "password changed"})
}