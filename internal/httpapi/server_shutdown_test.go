package httpapi

import (
	"context"
	"testing"
	"time"

	"digesterr/internal/config"
)

func TestExtractConcurrencyDefault(t *testing.T) {
	if got := extractConcurrency(config.Default()); got != 4 {
		t.Errorf("default extract concurrency = %d, want 4", got)
	}
	cfg := config.Default()
	cfg.Feed.ExtractConcurrency = 7
	if got := extractConcurrency(cfg); got != 7 {
		t.Errorf("configured extract concurrency = %d, want 7", got)
	}
	cfg.Feed.ExtractConcurrency = -1
	if got := extractConcurrency(cfg); got != 4 {
		t.Errorf("negative extract concurrency = %d, want fallback 4", got)
	}
}

// WaitBackground must return as soon as ctx expires even if tracked work is
// still running: shutdown must not hang forever on a stuck background job.
func TestWaitBackgroundRespectsContextDeadline(t *testing.T) {
	server, _ := setupTestServer(t)

	release := make(chan struct{})
	server.bg.Add(1)
	go func() {
		defer server.bg.Done()
		<-release
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	server.WaitBackground(ctx)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("WaitBackground blocked %v, want it to return on ctx deadline", elapsed)
	}
	close(release)
}

// WaitBackground returns promptly when there is no tracked work.
func TestWaitBackgroundReturnsWhenIdle(t *testing.T) {
	server, _ := setupTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		server.WaitBackground(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("WaitBackground did not return with no background work")
	}
}
