package logbuf

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"runtime"
	"strings"
)

// Handler is a slog.Handler that tees every record into a Buffer while still
// forwarding to an underlying handler (e.g. the stderr text handler).
type Handler struct {
	inner slog.Handler
	buf   *Buffer
	opts  *slog.HandlerOptions
}

// NewHandler wraps inner and captures records into buf. The buffer's records
// get a "type" derived from the source package.
func NewHandler(inner slog.Handler, buf *Buffer, opts *slog.HandlerOptions) *Handler {
	return &Handler{inner: inner, buf: buf, opts: opts}
}

// Enabled reports whether the record should be processed (delegates to inner).
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle captures the record into the buffer and forwards to the inner handler.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	entry := Entry{
		Time:    r.Time,
		Level:   r.Level.String(),
		Type:    typeForSource(r.PC, h.opts),
		Message: r.Message,
	}
	if r.NumAttrs() > 0 {
		attrs := make(map[string]string, r.NumAttrs())
		r.Attrs(func(a slog.Attr) bool {
			if a.Key != "" {
				attrs[a.Key] = fmt.Sprint(a.Value.Any())
			}
			return true
		})
		if len(attrs) > 0 {
			entry.Attrs = attrs
		}
	}
	h.buf.Add(entry)
	return h.inner.Handle(ctx, r)
}

// WithAttrs forwards to the inner handler.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{inner: h.inner.WithAttrs(attrs), buf: h.buf, opts: h.opts}
}

// WithGroup forwards to the inner handler.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{inner: h.inner.WithGroup(name), buf: h.buf, opts: h.opts}
}

// typeForSource maps the caller's source file package to a stable log "type".
// It needs AddSource enabled in HandlerOptions to have PC information.
func typeForSource(pc uintptr, opts *slog.HandlerOptions) string {
	if opts == nil || !opts.AddSource {
		return "app"
	}
	frames := runtime.CallersFrames([]uintptr{pc})
	f, _ := frames.Next()
	return ClassifyPath(f.File)
}

// ClassifyPath maps a source file path to a stable log "type":
// internal/httpapi -> http, internal/digest -> digest, internal/feed -> feed,
// internal/auth -> auth, cmd/digesterr -> app, anything else -> its dir name.
func ClassifyPath(file string) string {
	switch {
	case strings.Contains(file, "internal/httpapi"):
		return "http"
	case strings.Contains(file, "internal/digest"):
		return "digest"
	case strings.Contains(file, "internal/feed"):
		return "feed"
	case strings.Contains(file, "internal/auth"):
		return "auth"
	case strings.Contains(file, "cmd/digesterr"):
		return "app"
	}
	dir := path.Base(path.Dir(file))
	if dir == "" || dir == "." {
		return "other"
	}
	return dir
}
