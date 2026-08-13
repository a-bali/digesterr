// Package logbuf provides a small, thread-safe ring buffer for keeping the
// most recent log records in memory so they can be surfaced through the API.
package logbuf

import (
	"sync"
	"time"
)

// Level is a log severity level, stored as the string form for display.
type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Entry is a single captured log record.
type Entry struct {
	Time    time.Time         `json:"time"`
	Level   string            `json:"level"`
	Type    string            `json:"type"`
	Message string            `json:"message"`
	Attrs   map[string]string `json:"attrs,omitempty"`
}

// Buffer is a fixed-capacity ring buffer of log entries.
type Buffer struct {
	mu      sync.RWMutex
	entries []Entry
	next    int
	size    int
}

// New returns a ring buffer holding at most capacity entries (>=1).
func New(capacity int) *Buffer {
	if capacity < 1 {
		capacity = 1
	}
	return &Buffer{
		entries: make([]Entry, capacity),
		size:    capacity,
	}
}

// Add appends an entry, evicting the oldest when full.
func (b *Buffer) Add(e Entry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries[b.next] = e
	b.next = (b.next + 1) % b.size
}

// List returns up to limit entries, newest first. limit <= 0 means all.
func (b *Buffer) List(limit int) []Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if limit <= 0 || limit > b.size {
		limit = b.size
	}
	out := make([]Entry, 0, limit)
	for i := 0; i < limit; i++ {
		idx := (b.next - 1 - i + 2*b.size) % b.size
		if idx < 0 || idx >= b.size {
			continue
		}
		e := b.entries[idx]
		if e.Time.IsZero() {
			continue // unwritten slot
		}
		out = append(out, e)
	}
	return out
}

// Len returns the number of entries currently stored.
func (b *Buffer) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	n := 0
	for _, e := range b.entries {
		if !e.Time.IsZero() {
			n++
		}
	}
	return n
}
