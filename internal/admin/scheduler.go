// Auto-backup scheduler — the goroutine that wakes periodically,
// produces a snapshot via VACUUM INTO, packs it into the catalog,
// and prunes oldest entries to honor the retention policy.
//
// One scheduler per process. StartScheduler is idempotent (sync.Once);
// stopping is via context cancellation. The scheduler doesn't own
// the lifecycle of the database connection or the manifest — those
// are passed in.
//
// The actual snapshot+tar work is delegated to a SnapshotFunc the
// caller provides. This keeps the package independent of cmd/mar-
// runtime's bundle layout (which lives there to avoid import cycles
// — that file references the manifest, which references project.*,
// which we don't want admin to depend on).

package admin

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"time"
)

// SnapshotFunc captures the work of producing a single backup
// tarball at `outPath` from the live database. Implementations:
//
//   - cmd/mar-runtime: full backup via VACUUM INTO + metadata.json
//
// The scheduler invokes this on each tick (subject to skip-if-recent).
type SnapshotFunc func(db *sql.DB, outPath string, now time.Time) error

// SchedulerConfig is the input to StartScheduler. All fields are
// required.
type SchedulerConfig struct {
	DB             *sql.DB
	CatalogDir     string
	Interval       time.Duration
	RetentionCount int
	Snapshot       SnapshotFunc

	// Logger receives one-line status messages: "[mar auto-backup]
	// took 2026-... 4.2 KB", "skipped: recent backup exists", etc.
	// May be nil; defaults to writing to os.Stderr.
	Logger func(string)
}

// schedulerOnce ensures we don't start multiple schedulers per
// process. Tests pass nil ctx + nil cancel via StartSchedulerForTest
// to bypass — production goes through StartScheduler.
var schedulerOnce sync.Once

// StartScheduler kicks off the auto-backup loop in a background
// goroutine. Idempotent within a process. The returned context's
// Done() is the goroutine's exit signal — wire to the parent
// context so process shutdown stops the scheduler cleanly.
func StartScheduler(parent context.Context, cfg SchedulerConfig) context.CancelFunc {
	ctx, cancel := context.WithCancel(parent)
	schedulerOnce.Do(func() {
		go runScheduler(ctx, cfg)
	})
	return cancel
}

// StartSchedulerForTest is the test-friendly entry point — bypasses
// the sync.Once guard so multiple tests can spin up + tear down
// schedulers without leaking goroutines into other tests. Production
// should always use StartScheduler.
func StartSchedulerForTest(parent context.Context, cfg SchedulerConfig) context.CancelFunc {
	ctx, cancel := context.WithCancel(parent)
	go runScheduler(ctx, cfg)
	return cancel
}

func runScheduler(ctx context.Context, cfg SchedulerConfig) {
	log := cfg.Logger
	if log == nil {
		log = func(s string) { fmt.Fprintln(os.Stderr, "[mar auto-backup] "+s) }
	}
	if err := os.MkdirAll(cfg.CatalogDir, 0o755); err != nil {
		log(fmt.Sprintf("could not create catalog dir %s: %v", cfg.CatalogDir, err))
		return
	}

	// First tick: skip-if-recent. If a backup exists and is younger
	// than `Interval`, defer the next tick proportionally. Prevents
	// hot-reload / quick restart loops from triggering redundant
	// backups.
	first := computeFirstDelay(cfg.CatalogDir, cfg.Interval, time.Now)

	timer := time.NewTimer(first)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			tick(cfg, log)
			timer.Reset(cfg.Interval)
		}
	}
}

// tick runs one backup cycle: snapshot + prune. Errors are logged
// but never abort the scheduler — a transient failure shouldn't
// kill the loop forever.
func tick(cfg SchedulerConfig, log func(string)) {
	now := time.Now().UTC()
	id := NewCatalogID(now)
	out := CatalogPath(cfg.CatalogDir, id)
	if err := cfg.Snapshot(cfg.DB, out, now); err != nil {
		log(fmt.Sprintf("snapshot %s failed: %v", id, err))
		// Best-effort: remove a partial file if the snapshot bailed.
		_ = os.Remove(out)
		return
	}
	info, err := os.Stat(out)
	var size string
	if err == nil {
		size = humanBytes(info.Size())
	}
	log(fmt.Sprintf("took %s %s", id, size))

	removed, err := PruneCatalog(cfg.CatalogDir, cfg.RetentionCount)
	if err != nil {
		log(fmt.Sprintf("prune warning: %v", err))
	}
	for _, r := range removed {
		log(fmt.Sprintf("pruned %s", r))
	}
}

// computeFirstDelay decides how long to wait before the first tick.
// If the catalog has a backup younger than `interval`, defer until
// `interval` has elapsed since that backup. Otherwise tick almost
// immediately (1 second grace so boot logs land before backup
// chatter — capped by `interval` so test setups with tiny intervals
// don't get penalized).
func computeFirstDelay(catalogDir string, interval time.Duration, now func() time.Time) time.Duration {
	grace := time.Second
	if interval < grace {
		grace = interval
	}
	entry, ok, err := MostRecentCatalogEntry(catalogDir)
	if err != nil || !ok {
		return grace
	}
	elapsed := now().Sub(entry.CreatedAt)
	if elapsed >= interval {
		return grace
	}
	return interval - elapsed
}

// humanBytes is a tiny formatter for the log line. Doesn't need to
// be exposed — the same conversion happens elsewhere via human-
// readable formatters in the CLI.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
