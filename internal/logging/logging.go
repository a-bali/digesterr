// Package logging provides the shared slog infrastructure for the digesterr
// backend: level configuration (env var / flag parsing) and a handler that
// stamps every record with a stable "category" attribute.
//
// Usage: packages log through the slog default logger (slog.Info, slog.Error,
// ...). main wires a *logging.Handler into the chain, so every emitted record
// automatically carries a category derived from the caller's source package.
// Call sites that need a specific category (e.g. item-level records from the
// http layer) can override it explicitly:
//
//	logger := slog.With(logging.CategoryAttr(logging.CategoryItem))
//	logger.Info("item saved", "item_id", id)
package logging

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"runtime"
	"strings"
)

// Category values emitted on every log record.
const (
	CategoryHTTP   = "http"
	CategoryFeed   = "feed"
	CategoryItem   = "item"
	CategoryDigest = "digest"
	CategoryApp    = "app"
	CategoryAuth   = "auth"
)

// CategoryKey is the attribute key used for the category on every record.
const CategoryKey = "category"

// CategoryAttr returns an attribute that sets the record category explicitly.
// It overrides the source-derived default when present on a record.
func CategoryAttr(cat string) slog.Attr {
	return slog.String(CategoryKey, cat)
}

// ClassifyPath maps a source file path to a stable category:
// internal/httpapi -> http, internal/digest -> digest, internal/feed -> feed,
// internal/auth -> auth, cmd/digesterr -> app, anything else -> its dir name.
func ClassifyPath(file string) string {
	switch {
	case strings.Contains(file, "internal/httpapi"):
		return CategoryHTTP
	case strings.Contains(file, "internal/digest"):
		return CategoryDigest
	case strings.Contains(file, "internal/feed"):
		return CategoryFeed
	case strings.Contains(file, "internal/auth"):
		return CategoryAuth
	case strings.Contains(file, "cmd/digesterr"):
		return CategoryApp
	}
	dir := path.Base(path.Dir(file))
	if dir == "" || dir == "." {
		return "other"
	}
	return dir
}

// CategoryFor derives a category from a source PC. It needs
// HandlerOptions.AddSource enabled to have PC information; otherwise it
// falls back to CategoryApp.
func CategoryFor(pc uintptr, opts *slog.HandlerOptions) string {
	if opts == nil || !opts.AddSource || pc == 0 {
		return CategoryApp
	}
	frames := runtime.CallersFrames([]uintptr{pc})
	f, _ := frames.Next()
	if f.File == "" {
		return CategoryApp
	}
	return ClassifyPath(f.File)
}

// ParseLevel converts a level name to a slog.Level. Accepted values (case-
// insensitive): debug, info, warn (warning), error. An empty string means
// "unset" and maps to the default LevelInfo. Unknown names return ok=false.
func ParseLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, true
	case "debug":
		return slog.LevelDebug, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	}
	return slog.LevelInfo, false
}

// Handler wraps a base slog.Handler and guarantees every record carries a
// "category" attribute. An explicit category attribute (set via slog.With or
// passed as a record attr) wins; otherwise the category is derived from the
// caller's source path (requires AddSource in HandlerOptions).
type Handler struct {
	base slog.Handler
	opts *slog.HandlerOptions
	// explicit is a category set via WithAttrs (slog.With). The base handler
	// will already emit it, so Handle must not stamp a second value.
	explicit string
}

// NewHandler wraps base with category stamping.
func NewHandler(base slog.Handler, opts *slog.HandlerOptions) *Handler {
	return &Handler{base: base, opts: opts}
}

// Enabled reports whether the record should be processed (delegates to base).
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}

// Handle stamps the category attribute (when missing) and forwards to base.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	// A category already on the record (inline attr) wins untouched; a
	// category from slog.With is emitted by the base handler, so only derive
	// and stamp when neither is present.
	if !recordHasAttr(r, CategoryKey) && h.explicit == "" {
		r.AddAttrs(CategoryAttr(CategoryFor(r.PC, h.opts)))
	}
	return h.base.Handle(ctx, r)
}

// WithAttrs forwards to the base handler, remembering an explicit category.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := &Handler{base: h.base.WithAttrs(attrs), opts: h.opts, explicit: h.explicit}
	if v, ok := categoryFromAttrs(attrs); ok {
		next.explicit = v
	}
	return next
}

// WithGroup forwards to the base handler.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{base: h.base.WithGroup(name), opts: h.opts}
}

// recordHasAttr reports whether the record already carries the given top-level
// attribute key.
func recordHasAttr(r slog.Record, key string) bool {
	has := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			has = true
			return false
		}
		return true
	})
	return has
}

// categoryFromAttrs extracts a category value from a top-level attribute
// list (as passed to WithAttrs), reporting whether one was found.
func categoryFromAttrs(attrs []slog.Attr) (string, bool) {
	for _, a := range attrs {
		if a.Key == CategoryKey {
			return fmt.Sprint(a.Value.Any()), true
		}
	}
	return "", false
}
