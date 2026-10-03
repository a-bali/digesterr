package httpapi

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestCommentCacheCloseStopsSweeper is the regression test for the goroutine
// leak: every Server constructs a commentCache, whose cleanup loop used to block
// on its ticker forever with no way to stop it. The suite builds hundreds of
// servers, so a test-timeout goroutine dump was wall-to-wall cleanup loops.
//
// The assertion is on runtime goroutine stacks because that is the only reliable
// way to observe "this specific goroutine is gone" — the goroutine holds no
// reference a test can inspect.
func TestCommentCacheCloseStopsSweeper(t *testing.T) {
	c := newCommentCache(time.Minute)

	// The sweeper starts lazily on first insert, so exercise the path.
	c.set(1, nil)
	waitForSweeper(t, true)

	c.Close()
	waitForSweeper(t, false)

	// Idempotent: a second Close (production shutdown + test cleanup can both
	// call it) must not panic on a double close of the stop channel.
	c.Close()
}

// TestCommentCacheIsLazyWithoutWrites pins the second half: a cache that never
// stores anything must not start a sweeper at all, which is what keeps the ~13
// test servers constructed outside setupTestServer from leaking.
func TestCommentCacheIsLazyWithoutWrites(t *testing.T) {
	base := cleanupSweeperCount()

	c := newCommentCache(time.Minute)
	_ = c
	// Give a would-be goroutine a chance to appear.
	time.Sleep(20 * time.Millisecond)

	if got := cleanupSweeperCount(); got != base {
		t.Fatalf("sweeper goroutines = %d after constructing an unused cache, want %d (sweeper is not lazy)", got, base)
	}

	// And it does start once something is cached, or expiry would never run.
	c.set(1, nil)
	waitForSweeper(t, true)
	c.Close()
}

// cleanupSweeperCount counts live commentCache.cleanup goroutines.
func cleanupSweeperCount() int {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	return strings.Count(string(buf[:n]), "(*commentCache).cleanup(")
}

// waitForSweeper polls until the sweeper is present (want=true) or absent
// (want=false), so the test tolerates goroutine scheduling instead of racing it.
func waitForSweeper(t *testing.T, want bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if (cleanupSweeperCount() > 0) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := cleanupSweeperCount()
	if want {
		t.Fatalf("sweeper goroutine never started (count=%d)", got)
	}
	t.Fatalf("sweeper goroutine still running after Close (count=%d)", got)
}
