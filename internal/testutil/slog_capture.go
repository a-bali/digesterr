// Test helpers for asserting on slog output in behavioral tests.
//
// Use CaptureSlog to install a category-stamping capturing handler as the
// slog default for the duration of a test, then assert on the emitted
// records: levels, categories, and attributes.
package testutil

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"

	"digesterr/internal/logging"
)

// LogCapture is a slog.Handler that records every emitted record for
// assertions in tests. Records are captured with source attribution enabled
// so the logging.Handler can derive categories.
type LogCapture struct {
	mu *sync.Mutex
	// records is a shared slice pointer so handlers derived via WithAttrs
	// append into the same header the capture reads.
	records *[]slog.Record
	level   slog.Level
	// attrs are attributes bound via WithAttrs; applied to every record that
	// passes through this handler (mirrors base-handler behavior).
	attrs []slog.Attr
}

// NewLogCapture returns a capture that records records at or above level.
func NewLogCapture(level slog.Level) *LogCapture {
	return &LogCapture{mu: &sync.Mutex{}, records: &[]slog.Record{}, level: level}
}

// Enabled reports whether level passes the configured threshold.
func (c *LogCapture) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= c.level
}

// Handle records the record (with pre-bound attrs merged in).
func (c *LogCapture) Handle(ctx context.Context, r slog.Record) error {
	clone := r.Clone()
	clone.AddAttrs(c.attrs...)
	c.mu.Lock()
	*c.records = append(*c.records, clone)
	c.mu.Unlock()
	return nil
}

// WithAttrs returns a capture sharing the same record store with the given
// attrs pre-bound.
func (c *LogCapture) WithAttrs(attrs []slog.Attr) slog.Handler {
	nc := &LogCapture{level: c.level, records: c.records, mu: c.mu}
	nc.attrs = make([]slog.Attr, 0, len(c.attrs)+len(attrs))
	nc.attrs = append(nc.attrs, c.attrs...)
	nc.attrs = append(nc.attrs, attrs...)
	return nc
}

// WithGroup returns the same capture (the app does not use groups).
func (c *LogCapture) WithGroup(name string) slog.Handler {
	return c
}

// Records returns a copy of all captured records.
func (c *LogCapture) Records() []slog.Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]slog.Record, len(*c.records))
	copy(out, *c.records)
	return out
}

// Find returns the first captured record with the given message.
func (c *LogCapture) Find(msg string) (slog.Record, bool) {
	for _, r := range c.Records() {
		if r.Message == msg {
			return r, true
		}
	}
	return slog.Record{}, false
}

// Warns returns all captured records at warn level or above.
func (c *LogCapture) Warns() []slog.Record {
	var out []slog.Record
	for _, r := range c.Records() {
		if r.Level >= slog.LevelWarn {
			out = append(out, r)
		}
	}
	return out
}

// AttrMap flattens a record's top-level attributes into a map (last value
// wins for duplicate keys).
func AttrMap(r slog.Record) map[string]string {
	m := make(map[string]string)
	r.Attrs(func(a slog.Attr) bool {
		if a.Key != "" {
			m[a.Key] = fmt.Sprint(a.Value.Any())
		}
		return true
	})
	return m
}

// CaptureSlog installs a category-stamping capturing handler as the slog
// default for the duration of the test and returns the capture. Records are
// captured at or above level; use slog.LevelDebug to see everything.
func CaptureSlog(t *testing.T, level slog.Level) *LogCapture {
	t.Helper()
	old := slog.Default()
	capture := NewLogCapture(level)
	opts := &slog.HandlerOptions{Level: level, AddSource: true}
	slog.SetDefault(slog.New(logging.NewHandler(capture, opts)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return capture
}

// AssertAllCategorized fails the test if any captured record is missing a
// category attribute. Call it at the end of tests that exercise log-emitting
// code paths to enforce the "every log line has a category" rule.
func AssertAllCategorized(t *testing.T, c *LogCapture) {
	t.Helper()
	for _, r := range c.Records() {
		if cat, ok := AttrMap(r)[logging.CategoryKey]; !ok || cat == "" {
			t.Errorf("record %q (level %s) missing category attribute", r.Message, r.Level)
		}
	}
}
