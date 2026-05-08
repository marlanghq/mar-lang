package admin

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestComputeFirstDelay covers the skip-if-recent logic that runs
// once at scheduler boot, before the first tick.
func TestComputeFirstDelay(t *testing.T) {
	dir := t.TempDir()
	interval := time.Hour

	// 1) No catalog yet → fire near-immediately (1s grace, capped by interval).
	got := computeFirstDelay(dir, interval, func() time.Time { return time.Now() })
	if got != time.Second {
		t.Errorf("empty catalog: got %v, want 1s", got)
	}

	// 1b) Tiny interval → grace caps at the interval.
	short := computeFirstDelay(t.TempDir(), 50*time.Millisecond, func() time.Time { return time.Now() })
	if short != 50*time.Millisecond {
		t.Errorf("tiny interval: got %v, want 50ms (interval-capped grace)", short)
	}

	// 2) Recent backup (10min old) with 1h interval → wait ~50min.
	tenMinAgo := time.Now().UTC().Add(-10 * time.Minute)
	mustTouch(t, filepath.Join(dir, NewCatalogID(tenMinAgo)+".tar.gz"))
	got = computeFirstDelay(dir, interval, func() time.Time { return time.Now() })
	want := 50 * time.Minute
	// Allow ±2s slack — the catalog timestamp loses sub-second precision.
	if got < want-2*time.Second || got > want+2*time.Second {
		t.Errorf("recent backup: got %v, want ~%v", got, want)
	}

	// 3) Stale backup (older than interval) → fire near-immediately.
	twoHrAgo := time.Now().UTC().Add(-2 * time.Hour)
	staleDir := t.TempDir()
	mustTouch(t, filepath.Join(staleDir, NewCatalogID(twoHrAgo)+".tar.gz"))
	got = computeFirstDelay(staleDir, interval, func() time.Time { return time.Now() })
	if got != time.Second {
		t.Errorf("stale backup: got %v, want 1s", got)
	}
}

// TestScheduler_TicksAndPrunes — end-to-end with a fake snapshot
// function. Spins up the scheduler with a tiny interval (50ms) and
// retention=2, fires a few cycles, then verifies the catalog has
// exactly 2 entries.
func TestScheduler_TicksAndPrunes(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var calls int
	snap := func(db *sql.DB, outPath string, now time.Time) error {
		mu.Lock()
		calls++
		mu.Unlock()
		// Write a tiny placeholder file — the scheduler doesn't
		// inspect its content.
		f, err := os.Create(outPath)
		if err != nil {
			return err
		}
		f.WriteString("fake bundle")
		return f.Close()
	}

	logged := make([]string, 0, 8)
	var logMu sync.Mutex
	logger := func(s string) {
		logMu.Lock()
		defer logMu.Unlock()
		logged = append(logged, s)
	}

	ctx, cancel := context.WithCancel(context.Background())
	stop := StartSchedulerForTest(ctx, SchedulerConfig{
		DB:             nil, // fake snap doesn't use it
		CatalogDir:     dir,
		Interval:       50 * time.Millisecond,
		RetentionCount: 2,
		Snapshot:       snap,
		Logger:         logger,
	})
	defer stop()
	defer cancel()

	// Wait for at least 5 ticks worth of time so we exercise the
	// prune path (cap=2, so calls 3..5 evict older).
	time.Sleep(400 * time.Millisecond)
	cancel()

	mu.Lock()
	finalCalls := calls
	mu.Unlock()
	if finalCalls < 4 {
		t.Errorf("expected at least 4 ticks; got %d", finalCalls)
	}

	entries, err := ListCatalog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > 2 {
		t.Errorf("retention should cap at 2; got %d entries: %v",
			len(entries), entries)
	}
}

// TestScheduler_StopsOnContextCancel — cancelling the parent
// context terminates the goroutine cleanly.
func TestScheduler_StopsOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	snap := func(db *sql.DB, outPath string, now time.Time) error {
		calls++
		f, _ := os.Create(outPath)
		f.Close()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	StartSchedulerForTest(ctx, SchedulerConfig{
		CatalogDir:     dir,
		Interval:       30 * time.Millisecond,
		RetentionCount: 5,
		Snapshot:       snap,
		Logger:         func(string) {},
	})
	time.Sleep(100 * time.Millisecond)
	beforeCancel := calls
	cancel()
	time.Sleep(100 * time.Millisecond)
	afterCancel := calls
	if afterCancel-beforeCancel > 1 {
		t.Errorf("scheduler should stop ticking after cancel; calls before=%d after=%d",
			beforeCancel, afterCancel)
	}
}

// TestScheduler_SnapshotErrorDoesntKillLoop — a transient failure
// in the snapshot func is logged but the scheduler keeps running.
func TestScheduler_SnapshotErrorDoesntKillLoop(t *testing.T) {
	dir := t.TempDir()
	var calls int
	snap := func(db *sql.DB, outPath string, now time.Time) error {
		calls++
		if calls == 1 {
			return os.ErrPermission // first call fails
		}
		f, _ := os.Create(outPath)
		f.Close()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	StartSchedulerForTest(ctx, SchedulerConfig{
		CatalogDir:     dir,
		Interval:       30 * time.Millisecond,
		RetentionCount: 5,
		Snapshot:       snap,
		Logger:         func(string) {},
	})
	time.Sleep(150 * time.Millisecond)
	if calls < 3 {
		t.Errorf("scheduler should have continued past the first error; calls=%d", calls)
	}
	// At least one entry should exist (from the second+ successful call).
	entries, _ := ListCatalog(dir)
	if len(entries) == 0 {
		t.Errorf("expected at least one successful entry after the failed first call")
	}
}
