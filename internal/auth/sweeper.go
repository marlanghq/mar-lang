package auth

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"
)

// StartSweeper launches a background goroutine that calls SweepExpired
// on a fixed cadence. Returns immediately; the returned `stop` function
// cancels the sweeper and returns once the goroutine has exited
// (synchronous teardown — important for tests so the next test doesn't
// race against a leftover goroutine writing to the same DB).
//
// Behaviour:
//   - Runs an immediate sweep at start, BEFORE the first tick. Long-
//     idled deployments (process restarted after weeks) shouldn't have
//     to wait `interval` to reclaim space.
//   - On each tick, calls SweepExpired with the current wall-clock time.
//     Errors are logged to stderr but don't stop the loop — a transient
//     DB issue shouldn't permanently break expiration cleanup.
//   - When the context is cancelled (or `stop()` is called), returns.
//
// Production callers pass 24h. Tests pass milliseconds.
func StartSweeper(ctx context.Context, db *sql.DB, interval time.Duration) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)

		// Initial sweep before the ticker. Reclaims data accumulated
		// while the process was off; also serves as a smoke check that
		// the DELETE statements still parse against the live schema.
		runOnce(db)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runOnce(db)
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

// runOnce executes one sweep, logging any error. Pulled out so the
// initial pre-tick sweep and the per-tick sweep share one body.
func runOnce(db *sql.DB) {
	if err := SweepExpired(db, time.Now().Unix()); err != nil {
		fmt.Fprintf(os.Stderr, "[mar auth] sweep failed: %v\n", err)
	}
}
