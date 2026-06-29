// restore-db subcommand for mar-runtime.
//
//   mar-runtime restore-db [PROJECT_DIR] BUNDLE_PATH [--dry-run]
//
// Replaces the project's live SQLite database with the one inside the
// backup bundle, after a series of safety checks. The current file is
// saved next to the live DB as <name>.bak-<timestamp> so the swap is
// reversible.
//
// Design notes captured in the now-deleted BACKLOG entry ("Admin —
// Database restore CLI"). Key choices, summarized:
//
//   - No --force flag. Schema mismatch is a hard stop with no override.
//     If an operator really wants to restore an incompatible bundle
//     they can do the file swap by hand; the CLI's purpose is to be
//     the safe path.
//   - No --yes flag. Restore is destructive; the confirmation prompt
//     is the brake. We ask for the literal word "restore" (not "yes"),
//     so a tab/enter sequence in an automated context can't fall
//     through.
//   - --dry-run is the one supported flag. Inspection + plan output,
//     no side effects.
//   - Server detection via flock on the live DB. The runtime acquires
//     the same lock at startup (see runtime.HoldDBLock); if we can't
//     get it, a server is running and the operator must stop it.
//     Platform-neutral — no mentions of fly, systemd, supervisor.

package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mar/internal/admin"
	"mar/internal/project"
	"mar/internal/runtime"
)

func runRuntimeRestore(args []string) int {
	var dryRun bool
	positional := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "-h", "--help":
			printRestoreUsage()
			return 2
		case "--dry-run":
			dryRun = true
		default:
			if strings.HasPrefix(a, "--") {
				fmt.Fprintf(os.Stderr, "mar-runtime restore-db: unknown flag %q\n", a)
				printRestoreUsage()
				return 2
			}
			positional = append(positional, a)
		}
	}

	var projectDir, bundlePath string
	switch len(positional) {
	case 1:
		projectDir, bundlePath = ".", positional[0]
	case 2:
		projectDir, bundlePath = positional[0], positional[1]
	default:
		printRestoreUsage()
		return 2
	}

	return doRestore(restoreOpts{
		projectDir: projectDir,
		bundlePath: bundlePath,
		dryRun:     dryRun,
		stdout:     os.Stdout,
		stderr:     os.Stderr,
		stdin:      os.Stdin,
		now:        time.Now,
	})
}

// restoreOpts collects the inputs doRestore needs. Test code builds
// its own opts to drive the flow with stub stdin/stdout/now.
type restoreOpts struct {
	projectDir string
	bundlePath string
	dryRun     bool
	stdout     io.Writer
	stderr     io.Writer
	stdin      io.Reader
	now        func() time.Time
}

// doRestore is the pure flow — separated from runRuntimeRestore so
// tests can drive it with in-memory readers/writers and a frozen
// clock without going through os.Args / os.Stdin.
func doRestore(o restoreOpts) int {
	// 1. Resolve the live DB path from the project's mar.json.
	manifest, err := project.LoadManifestStructure(o.projectDir)
	if err != nil {
		fmt.Fprintf(o.stderr, "mar-runtime restore-db: %v\n", err)
		return 1
	}
	livePath, _ := project.ResolveDatabasePath(manifest, o.projectDir)
	if livePath == "" {
		fmt.Fprintln(o.stderr, "mar-runtime restore-db: no database configured for this project (set database.path in mar.json)")
		return 1
	}

	// 2. Read bundle metadata (just enough to print the summary and
	// extract the schema fingerprint). The mar.db inside the bundle
	// is copied out later, after the user confirms.
	meta, bundleSize, err := readBundleMetadata(o.bundlePath)
	if err != nil {
		fmt.Fprintf(o.stderr, "mar-runtime restore-db: %v\n", err)
		fmt.Fprintln(o.stderr, "  (the bundle file may be malformed; re-download from the admin panel)")
		return 1
	}

	fmt.Fprintln(o.stdout, "Reading bundle...")
	fmt.Fprintf(o.stdout, "  Source:  %s (%s)\n", o.bundlePath, formatBytes(bundleSize))
	fmt.Fprintf(o.stdout, "  Created: %s\n", meta.CreatedAtRFC3339)
	fmt.Fprintln(o.stdout)

	// 3. The live DB must exist — restore swaps in place, it doesn't
	// bootstrap from scratch. Fresh installs should `tar -xzf` the
	// bundle directly.
	liveInfo, err := os.Stat(livePath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(o.stderr, "mar-runtime restore-db: no database at %s.\n", livePath)
			fmt.Fprintln(o.stderr, "  For a fresh install, extract the bundle manually:")
			fmt.Fprintf(o.stderr, "    tar -xzf %s mar.db && mv mar.db %s\n", o.bundlePath, livePath)
			return 1
		}
		fmt.Fprintf(o.stderr, "mar-runtime restore-db: %v\n", err)
		return 1
	}

	fmt.Fprintln(o.stdout, "Checking the live database...")
	fmt.Fprintf(o.stdout, "  Database: %s (%s)\n", livePath, formatBytes(liveInfo.Size()))

	// 4. Acquire the advisory lock first. If a server is running
	// it's holding this same lock; we get ErrDBLocked and tell the
	// operator to stop the server. Doing this before the schema
	// read lets us give a clear "server is running" error instead
	// of an opaque SQLITE_BUSY from the read attempt.
	lockHandle, err := runtime.LockDB(livePath)
	if err != nil {
		if errors.Is(err, runtime.ErrDBLocked) {
			fmt.Fprintln(o.stderr)
			fmt.Fprintln(o.stderr, "The server is using this database. Stop it before running restore-db.")
			return 1
		}
		fmt.Fprintf(o.stderr, "mar-runtime restore-db: lock: %v\n", err)
		return 1
	}
	// Release at function exit. Kernel would also do it on process
	// exit, but being explicit is cheap.
	defer lockHandle.Close()
	fmt.Fprintln(o.stdout, "  Lock:     no process is using it")

	// 5. Compute the live schema fingerprint and compare. Opened
	// with `immutable=1` so SQLite skips its own internal locking
	// (which would otherwise conflict with our advisory flock on
	// the same file). Safe because we hold the lock — nothing is
	// going to write to this file while we read it.
	liveFingerprint, err := fingerprintSQLiteFile(livePath)
	if err != nil {
		fmt.Fprintf(o.stderr, "mar-runtime restore-db: read live schema: %v\n", err)
		return 1
	}
	if meta.SchemaFingerprint != liveFingerprint {
		fmt.Fprintln(o.stderr)
		fmt.Fprintln(o.stderr, "Schema mismatch:")
		fmt.Fprintf(o.stderr, "  bundle:        %s\n", meta.SchemaFingerprint)
		fmt.Fprintf(o.stderr, "  live database: %s\n", liveFingerprint)
		fmt.Fprintln(o.stderr, "The bundle was taken from a database with a different schema. Restore refuses.")
		return 1
	}
	fmt.Fprintln(o.stdout, "  Schema:   matches the bundle")
	fmt.Fprintln(o.stdout)

	// 6. Print the plan.
	backupTS := o.now().UTC().Format("2006-01-02T15-04-05")
	backupName := filepath.Base(livePath) + ".bak-" + backupTS
	backupPath := filepath.Join(filepath.Dir(livePath), backupName)

	fmt.Fprintf(o.stdout, "This will replace %s with the database inside the\n", livePath)
	fmt.Fprintf(o.stdout, "bundle. The current file will be saved next to it as\n")
	fmt.Fprintf(o.stdout, "%s so you can roll back by swapping\n", backupName)
	fmt.Fprintln(o.stdout, "them again.")
	fmt.Fprintln(o.stdout)
	fmt.Fprintln(o.stdout, "The server must not be running while this happens. Start it back up")
	fmt.Fprintln(o.stdout, "after the command finishes.")
	fmt.Fprintln(o.stdout)

	if o.dryRun {
		fmt.Fprintln(o.stdout, "(dry run, no changes made)")
		return 0
	}

	// 7. Prompt for "restore". Anything else aborts.
	fmt.Fprint(o.stdout, `Type "restore" to proceed: `)
	scanner := bufio.NewScanner(o.stdin)
	if !scanner.Scan() {
		fmt.Fprintln(o.stderr)
		fmt.Fprintln(o.stderr, "Aborted (no input).")
		return 1
	}
	if strings.TrimSpace(scanner.Text()) != "restore" {
		fmt.Fprintln(o.stdout, "Aborted.")
		return 1
	}

	// 8. Do the swap. os.Rename is atomic on the same filesystem;
	// if extraction fails we roll back by renaming the .bak back.
	if err := os.Rename(livePath, backupPath); err != nil {
		fmt.Fprintf(o.stderr, "mar-runtime restore-db: move live → backup: %v\n", err)
		return 1
	}
	if err := extractBundleDB(o.bundlePath, livePath); err != nil {
		// Rollback: put the original file back.
		_ = os.Rename(backupPath, livePath)
		fmt.Fprintf(o.stderr, "mar-runtime restore-db: extract bundle: %v\n", err)
		fmt.Fprintln(o.stderr, "  (live database restored to its previous state)")
		return 1
	}
	// SQLite leaves -wal and -shm sidecar files when running in WAL
	// mode. They belong to the old DB; if we leave them in place
	// SQLite will try to reapply their contents over the freshly
	// restored DB on next open, undoing our work.
	_ = os.Remove(livePath + "-wal")
	_ = os.Remove(livePath + "-shm")

	fmt.Fprintln(o.stdout)
	fmt.Fprintln(o.stdout, "Done. Start the server when ready.")
	fmt.Fprintf(o.stdout, "Rollback (if needed): mv %s %s\n", backupPath, livePath)
	return 0
}

func printRestoreUsage() {
	fmt.Fprintln(os.Stderr, "usage: mar-runtime restore-db [PROJECT_DIR] BUNDLE_PATH [--dry-run]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  PROJECT_DIR  Path to the project (defaults to \".\").")
	fmt.Fprintln(os.Stderr, "  BUNDLE_PATH  Path to the .tar.gz bundle to restore from.")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  --dry-run    Inspect and check, do not modify anything.")
}

// readBundleMetadata opens the .tar.gz at path, finds metadata.json,
// and decodes it. Also verifies mar.db is present (so we can fail
// early on a malformed bundle without committing to side effects).
// Returns the decoded metadata + the bundle file size in bytes.
func readBundleMetadata(path string) (admin.SnapshotMetadata, int64, error) {
	var zero admin.SnapshotMetadata
	fi, err := os.Stat(path)
	if err != nil {
		return zero, 0, fmt.Errorf("bundle: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return zero, 0, fmt.Errorf("bundle: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return zero, 0, fmt.Errorf("bundle gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var meta *admin.SnapshotMetadata
	var sawDB bool
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return zero, 0, fmt.Errorf("bundle tar: %w", err)
		}
		switch hdr.Name {
		case "metadata.json":
			buf, err := io.ReadAll(tr)
			if err != nil {
				return zero, 0, fmt.Errorf("bundle read metadata: %w", err)
			}
			var m admin.SnapshotMetadata
			if err := json.Unmarshal(buf, &m); err != nil {
				return zero, 0, fmt.Errorf("bundle parse metadata: %w", err)
			}
			meta = &m
		case "mar.db":
			sawDB = true
		}
	}
	if meta == nil {
		return zero, 0, errors.New("bundle missing metadata.json")
	}
	if !sawDB {
		return zero, 0, errors.New("bundle missing mar.db")
	}
	return *meta, fi.Size(), nil
}

// extractBundleDB copies the mar.db entry out of the bundle tarball
// to outPath. Used by the restore swap.
func extractBundleDB(bundlePath, outPath string) error {
	f, err := os.Open(bundlePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Name != "mar.db" {
			continue
		}
		out, err := os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	}
	return errors.New("bundle missing mar.db entry")
}

// fingerprintSQLiteFile opens the SQLite file at path and returns
// its admin.SchemaFingerprint. Uses `immutable=1` so SQLite skips
// its own internal byte-range locking — that locking would conflict
// with the POSIX flock the caller holds on the same fd path. Safe
// because we always call this while holding the flock, so no other
// process can write to the file during the read.
func fingerprintSQLiteFile(path string) (string, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?immutable=1")
	if err != nil {
		return "", err
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return "", err
	}
	return admin.SchemaFingerprint(db)
}

// formatBytes renders an integer byte count in a humane unit. Used
// only for display in the restore plan.
func formatBytes(n int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case n >= GB:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(GB))
	case n >= MB:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(MB))
	case n >= KB:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(KB))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
