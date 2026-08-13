package logbuf

import (
	"testing"
	"time"
)

func addN(b *Buffer, n int) {
	for i := 0; i < n; i++ {
		b.Add(Entry{
			Time:    time.Unix(int64(i), 0),
			Level:   "info",
			Type:    "app",
			Message: "m",
		})
	}
}

func TestBufferBasics(t *testing.T) {
	b := New(3)
	addN(b, 3)
	if got := b.Len(); got != 3 {
		t.Fatalf("Len = %d, want 3", got)
	}
	list := b.List(0)
	if len(list) != 3 {
		t.Fatalf("List(0) len = %d, want 3", len(list))
	}
	// Newest first.
	if list[0].Time.Unix() != 2 || list[2].Time.Unix() != 0 {
		t.Fatalf("expected newest-first order, got %v", list)
	}
}

func TestBufferEviction(t *testing.T) {
	b := New(3)
	addN(b, 5)
	if got := b.Len(); got != 3 {
		t.Fatalf("Len = %d, want 3 after eviction", got)
	}
	list := b.List(0)
	// Oldest two (0,1) evicted; newest 2,3,4 remain.
	if list[0].Time.Unix() != 4 || list[2].Time.Unix() != 2 {
		t.Fatalf("expected eviction keeping newest, got %v", list)
	}
}

func TestBufferLimit(t *testing.T) {
	b := New(10)
	addN(b, 10)
	list := b.List(4)
	if len(list) != 4 {
		t.Fatalf("List(4) len = %d, want 4", len(list))
	}
	if list[0].Time.Unix() != 9 {
		t.Fatalf("expected newest first, got %v", list)
	}
}

func TestBufferEmpty(t *testing.T) {
	b := New(4)
	if got := b.Len(); got != 0 {
		t.Fatalf("Len = %d, want 0", got)
	}
	if got := b.List(0); len(got) != 0 {
		t.Fatalf("List on empty = %d, want 0", len(got))
	}
}
