// Boot-time wiring for the auto-backup scheduler. cmd/mar (mar dev)
// and cmd/mar-runtime (production) both call MaybeStartAutoBackup
// after their setup step is done. Centralizing here keeps the boot
// logic identical in both binaries.

package admin

import (
	"context"
	"database/sql"
	"time"

	"mar/internal/project"
)

// MaybeStartAutoBackup starts the auto-backup goroutine if the
// manifest enables it. Otherwise no-op. Returns the cancel function
// (caller can ignore in main; OS shutdown reaps the goroutine
// either way).
//
// Inputs:
//   - parent context: cancellation kills the scheduler. Pass
//     context.Background() in normal boot; tests pass a controlled
//     context.
//   - db: live SQLite handle. Same handle the app uses; VACUUM INTO
//     is consistent under WAL.
//   - manifest, projectDir: needed for the snapshot's mar.json copy
//     and metadata fields.
//   - dbPath: derives the catalog directory.
//   - marVersion: the version stamp for metadata.json.
func MaybeStartAutoBackup(
	parent context.Context,
	db *sql.DB,
	manifest *project.Manifest,
	projectDir string,
	dbPath string,
	marVersion string,
) context.CancelFunc {
	if manifest == nil || manifest.Database == nil {
		return func() {}
	}
	cfg := manifest.Database.AutoBackup
	if !cfg.AutoBackupEnabled() {
		return func() {}
	}
	if dbPath == "" {
		// No database configured; nothing to back up.
		return func() {}
	}

	interval := time.Duration(cfg.ResolvedIntervalHours()) * time.Hour
	retention := cfg.ResolvedRetentionCount()

	snap := func(db *sql.DB, outPath string, now time.Time) error {
		return WriteSnapshot(SnapshotInputs{
			DB:         db,
			Manifest:   manifest,
			ProjectDir: projectDir,
			OutPath:    outPath,
			Now:        now,
			MarVersion: marVersion,
		})
	}

	return StartScheduler(parent, SchedulerConfig{
		DB:             db,
		CatalogDir:     CatalogDir(dbPath),
		Interval:       interval,
		RetentionCount: retention,
		Snapshot:       snap,
		Logger:         nil, // default to stderr
	})
}
