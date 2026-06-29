package auth

import (
	"testing"
	"time"
)

func TestLimiterAllowsUpToMax(t *testing.T) {
	l := NewLimiter(3, time.Hour)
	t.Cleanup(l.Stop)
	for i := 1; i <= 3; i++ {
		ok, _ := l.Allow("alice")
		if !ok {
			t.Fatalf("attempt %d: expected allow, got block", i)
		}
	}
	if ok, _ := l.Allow("alice"); ok {
		t.Fatalf("4th attempt: expected block, got allow")
	}
}

func TestLimiterIsKeyIsolated(t *testing.T) {
	l := NewLimiter(2, time.Hour)
	t.Cleanup(l.Stop)
	for i := 0; i < 2; i++ {
		_, _ = l.Allow("alice")
	}
	if ok, _ := l.Allow("alice"); ok {
		t.Fatalf("alice exhausted; should block")
	}
	// Different key has its own budget.
	if ok, _ := l.Allow("bob"); !ok {
		t.Fatalf("bob is fresh; should allow")
	}
}

func TestLimiterRetryAfterIsPositive(t *testing.T) {
	l := NewLimiter(1, time.Hour)
	t.Cleanup(l.Stop)
	_, _ = l.Allow("alice")
	ok, retry := l.Allow("alice")
	if ok {
		t.Fatalf("expected block on second hit")
	}
	if retry <= 0 {
		t.Fatalf("expected positive retryAfter, got %v", retry)
	}
	if retry > time.Hour {
		t.Fatalf("retryAfter %v exceeds window", retry)
	}
}

func TestLimiterSlidingWindow(t *testing.T) {
	// 2 hits per window. First hit ages out after the window passes,
	// freeing budget for a third attempt.
	l := NewLimiter(2, 50*time.Millisecond)
	t.Cleanup(l.Stop)
	if ok, _ := l.Allow("alice"); !ok {
		t.Fatalf("hit 1 should allow")
	}
	if ok, _ := l.Allow("alice"); !ok {
		t.Fatalf("hit 2 should allow")
	}
	if ok, _ := l.Allow("alice"); ok {
		t.Fatalf("hit 3 should block (within window)")
	}
	// Wait past the window so the earliest hits age out.
	time.Sleep(60 * time.Millisecond)
	if ok, _ := l.Allow("alice"); !ok {
		t.Fatalf("hit 4 should allow after window passed")
	}
}
