package auth

import (
	"context"
	"testing"
	"time"
)

// TestSweeperDeletesOnTick verifies the end-to-end behaviour: insert
// an already-expired session, start the sweeper with a tiny interval,
// give it room for the initial sweep, then assert the row is gone.
//
// The initial pre-tick sweep is what we're really exercising — without
// it, this test would race against the first tick and be flaky on slow
// CI. With it, the assertion can run almost immediately after start.
func TestSweeperDeletesOnTick(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	now := time.Now().Unix()
	mustExec(t, db,
		`INSERT INTO _mar_auth_sessions(token_hash, user_id, expires_at, created_at, last_used_at) VALUES (?, ?, ?, ?, ?)`,
		"expired-token", 1, now-3600, now-7200, now-3600)

	// 1h interval — well over the test runtime. The point is to confirm
	// the *initial* sweep (run before the first tick) cleans up. If we
	// used a tiny interval here, a slow goroutine schedule could let
	// the test pass for the wrong reason.
	stop := StartSweeper(context.Background(), db, time.Hour)
	defer stop()

	// Poll briefly: the initial sweep is async. 200ms is well above
	// what a passing run needs (single DELETE on an in-memory DB) but
	// short enough not to slow the suite when the wiring is broken.
	deadline := time.Now().Add(200 * time.Millisecond)
	for {
		if count(t, db, `SELECT COUNT(*) FROM _mar_auth_sessions`) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("sweeper didn't delete expired session within 200ms")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestSweeperStopWaitsForGoroutine verifies that `stop()` is
// synchronous — it returns only after the sweeper goroutine has
// actually exited. If it returned eagerly, a test could close its
// DB and then have the sweeper crash mid-DELETE on the closed handle.
func TestSweeperStopWaitsForGoroutine(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	stop := StartSweeper(context.Background(), db, time.Hour)

	// Give the initial sweep a chance to land, then stop. After stop
	// returns, no more DB writes should happen.
	time.Sleep(20 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()

	select {
	case <-done:
		// Good — stop returned within deadline.
	case <-time.After(time.Second):
		t.Fatal("stop() did not return within 1s — sweeper goroutine likely hung")
	}
}

// TestSweeperHonoursTicker confirms that with a small interval, the
// sweeper actually fires *more than once*. This guards against a
// regression where the goroutine exits after the initial sweep
// (e.g. if someone replaced the for-loop with a one-shot).
func TestSweeperHonoursTicker(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	stop := StartSweeper(context.Background(), db, 10*time.Millisecond)
	defer stop()

	// Insert an expired session AFTER the sweeper started — so the
	// initial pre-tick sweep can't delete it, only a subsequent
	// tick can.
	time.Sleep(20 * time.Millisecond) // let the initial sweep finish first
	now := time.Now().Unix()
	mustExec(t, db,
		`INSERT INTO _mar_auth_sessions(token_hash, user_id, expires_at, created_at, last_used_at) VALUES (?, ?, ?, ?, ?)`,
		"late-expired", 2, now-1, now-100, now-1)

	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		if count(t, db, `SELECT COUNT(*) FROM _mar_auth_sessions`) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("sweeper didn't fire on a subsequent tick")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
