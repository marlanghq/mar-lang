// Backup subcommand for mar-runtime: produces a Level-2 restorable
// bundle via internal/admin/snapshot.go. Invoked over SSH by
// `mar fly database backup` (ad-hoc snapshots that land in the
// catalog) and as the SnapshotFunc supplied to admin.StartScheduler
// (auto-backups on a periodic interval).

package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mar/internal/admin"
	"mar/internal/project"
	"mar/internal/runtime"
)

// runRuntimeBackup is the CLI entry point. Produces a snapshot at
// the path the caller provides. (A future refactor may switch this
// to "force a snapshot into the catalog directory" with auto-naming
// via NewCatalogID: for now the explicit path keeps the SSH-driven
// flow in `mar fly database backup` straightforward.)
func runRuntimeBackup(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stderr, "usage: mar-runtime backup OUT_PATH")
		return 2
	}
	outPath := args[0]

	manifest, projectDir, err := loadRuntimeManifestWith(false /*resolveEnv*/)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mar-runtime backup: %v\n", err)
		return 1
	}
	dbPath, _ := project.ResolveDatabasePath(manifest, projectDir)
	if dbPath == "" {
		fmt.Fprintln(os.Stderr, "mar-runtime backup: no database configured for this project")
		return 1
	}
	// Before opening it. `runtime.OpenDB` CREATES a missing SQLite file —
	// measured — so a backup pointed at the wrong path made a brand-new
	// empty database, snapshotted THAT, and printed "backup written" with a
	// plausible byte count and exit 0. Someone taking a backup before a
	// migration got a file that restores to nothing, and was told it worked.
	//
	// There is no reading of "back this up" that means "create it first".
	if _, statErr := os.Stat(dbPath); statErr != nil {
		// Absolute: a relative path is what made the mistake possible, and
		// repeating it back does not say where the tool actually looked.
		shown := dbPath
		if abs, absErr := filepath.Abs(dbPath); absErr == nil {
			shown = abs
		}
		fmt.Fprintf(os.Stderr,
			"mar-runtime backup: there is no database at %s, so there is nothing to back up.\n"+
				"  The path comes from `database.path` in mar.json and is resolved against the "+
				"working directory, which is %s.\n"+
				"  Run this where the app runs\n",
			shown, workingDirOr(projectDir))
		return 1
	}
	runtime.SetDBPath(dbPath)

	db, err := runtime.OpenDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mar-runtime backup: open db: %v\n", err)
		return 1
	}

	if err := admin.WriteSnapshot(admin.SnapshotInputs{
		DB:         db,
		Manifest:   manifest,
		ProjectDir: projectDir,
		OutPath:    outPath,
		Now:        time.Now().UTC(),
		MarVersion: hostBuildInfoOrDev(),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "mar-runtime backup: %v\n", err)
		return 1
	}

	fi, _ := os.Stat(outPath)
	var sizeStr string
	if fi != nil {
		sizeStr = fmt.Sprintf(" (%d bytes)", fi.Size())
	}
	// What was captured, not just how big it came out. A byte count alone
	// reads the same whether the bundle holds every row or nothing at all,
	// which is exactly how an empty snapshot passed for a real one.
	fmt.Printf("backup written: %s%s\n", outPath, sizeStr)
	if summary := capturedSummary(db); summary != "" {
		fmt.Printf("  %s\n", summary)
	}
	return 0
}

// hostBuildInfoOrDev returns the mar version stamped at build time.
// Today mar-runtime doesn't carry a version variable populated via
// -ldflags. Returns "dev" until that's wired.
func hostBuildInfoOrDev() string {
	return "dev"
}

// capturedSummary says what the snapshot holds, in the terms someone taking a
// backup is actually asking about: how many tables and how many rows.
func capturedSummary(db *sql.DB) string {
	rows, err := db.Query(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return ""
		}
		tables = append(tables, name)
	}
	if rows.Err() != nil {
		return ""
	}
	if len(tables) == 0 {
		return "the database has no tables: this bundle restores to nothing"
	}
	total := 0
	for _, name := range tables {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM "` + strings.ReplaceAll(name, `"`, `""`) + `"`).Scan(&n); err != nil {
			return fmt.Sprintf("%d table%s", len(tables), plural(len(tables)))
		}
		total += n
	}
	return fmt.Sprintf("%d table%s, %d row%s",
		len(tables), plural(len(tables)), total, plural(total))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// workingDirOr names where relative paths are resolved from, falling back to
// what the manifest loader reported when the working directory is unreadable.
func workingDirOr(projectDir string) string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return projectDir
}
