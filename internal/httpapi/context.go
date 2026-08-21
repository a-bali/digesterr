package httpapi

import (
	"context"
	"net/http"
)

type contextKey string

const userIDKey contextKey = "userID"

func contextWithUserID(r *http.Request, userID int64) context.Context {
	return context.WithValue(r.Context(), userIDKey, userID)
}

func userIDFromContext(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(userIDKey).(int64)
	return id, ok
}

// authedUserID returns the authenticated user's ID. Handlers behind
// requireAuth always have it; the bool is false only for unauthenticated
// contexts (defensive — callers typically respond 401).
func authedUserID(r *http.Request) int64 {
	id, _ := userIDFromContext(r.Context())
	return id
}
