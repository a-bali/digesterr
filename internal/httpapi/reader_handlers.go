package httpapi

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func writeText(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprint(w, body)
}

// handleReaderToken responds with the authenticated user's API key as plain
// text. Google Reader clients echo this back as the T= query parameter.
func (s *Server) handleReaderToken(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	user, err := s.queries.GetUserByID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeText(w, http.StatusOK, user.ApiKey)
}

type readerUserInfo struct {
	UserID              string `json:"userId"`
	UserName            string `json:"userName"`
	UserProfileID       string `json:"userProfileId"`
	UserEmail           string `json:"userEmail"`
	IsBloggerUser       bool   `json:"isBloggerUser"`
	SignupTimeSec       int64  `json:"signupTimeSec"`
	IsMultiLoginEnabled bool   `json:"isMultiLoginEnabled"`
}

func (s *Server) handleReaderUserInfo(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	uid := strconv.FormatInt(userID, 10)
	writeJSON(w, http.StatusOK, readerUserInfo{
		UserID:              uid,
		UserName:            "digesterr",
		UserProfileID:       uid,
		UserEmail:           "",
		IsBloggerUser:       false,
		SignupTimeSec:       0,
		IsMultiLoginEnabled: false,
	})
}

// handleReaderClientLogin is the Google Reader ClientLogin endpoint. It is not
// guarded by readerAuth because this is where the client authenticates: the
// Email field carries the username (email) and the Passwd field carries the
// API key. On success it returns SID/LSID/Auth tokens; every subsequent
// request then authenticates with "GoogleLogin auth=<key>" (or X-Auth-Token).
func (s *Server) handleReaderClientLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeText(w, http.StatusUnauthorized, "Error=BadAuthentication")
		return
	}

	email := strings.TrimSpace(r.Form.Get("Email"))
	apiKey := strings.TrimSpace(r.Form.Get("Passwd"))
	if email == "" || apiKey == "" {
		writeText(w, http.StatusUnauthorized, "Error=BadAuthentication")
		return
	}

	user, err := s.queries.GetUserByEmail(r.Context(), email)
	if err != nil {
		writeText(w, http.StatusUnauthorized, "Error=BadAuthentication")
		return
	}

	// Constant-time compare, and reject an empty stored key so a user created
	// before the API-key feature can't authenticate with a blank password.
	if user.ApiKey == "" || subtle.ConstantTimeCompare([]byte(user.ApiKey), []byte(apiKey)) != 1 {
		writeText(w, http.StatusUnauthorized, "Error=BadAuthentication")
		return
	}

	writeText(w, http.StatusOK, fmt.Sprintf("SID=%s\nLSID=%s\nAuth=%s", user.ApiKey, user.ApiKey, user.ApiKey))
}
