// Backup subcommand for mar-runtime — produces a Level-2 restorable
// bundle via internal/admin/snapshot.go. Invoked over SSH by
// `mar fly database backup` (ad-hoc snapshots that land in the
// catalog) and as the SnapshotFunc supplied to admin.StartScheduler
// (auto-backups on a periodic interval).

package main

import (
	"fmt"
	"os"
	"time"

	"mar/internal/admin"
	"mar/internal/project"
	"mar/internal/runtime"
)

// runRuntimeBackup is the CLI entry point. Produces a snapshot at
// the path the caller provides. (A future refactor may switch this
// to "force a snapshot into the catalog directory" with auto-naming
// via NewCatalogID — for now the explicit path keeps the SSH-driven
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
	fmt.Printf("backup written: %s%s\n", outPath, sizeStr)
	return 0
}

// hostBuildInfoOrDev returns the mar version stamped at build time.
// Today mar-runtime doesn't carry a version variable populated via
// -ldflags. Returns "dev" until that's wired.
func hostBuildInfoOrDev() string {
	return "dev"
}
