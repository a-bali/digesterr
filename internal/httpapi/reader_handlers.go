package httpapi

import (
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
// guarded by readerAuth because this is where the client supplies its API key
// (via the Email or Passwd form field) in exchange for SID/LSID/Auth tokens.
func (s *Server) handleReaderClientLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeText(w, http.StatusUnauthorized, "Error=BadAuthentication")
		return
	}

	key := strings.TrimSpace(r.Form.Get("Email"))
	if key == "" {
		key = strings.TrimSpace(r.Form.Get("Passwd"))
	}

	user, err := s.queries.GetUserByAPIKey(r.Context(), key)
	if err != nil {
		writeText(w, http.StatusUnauthorized, "Error=BadAuthentication")
		return
	}

	writeText(w, http.StatusOK, fmt.Sprintf("SID=%s\nLSID=%s\nAuth=%s", user.ApiKey, user.ApiKey, user.ApiKey))
}
