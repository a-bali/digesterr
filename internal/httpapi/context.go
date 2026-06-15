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