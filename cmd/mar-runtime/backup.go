// Backup subcommand for mar-runtime — produces a Level-2 restorable
// bundle (see docs/admin-panel.md and the in-conversation design):
//
//   <out>.tar.gz
//   ├── metadata.json     # timestamp, mar version, build target,
//   │                       app name, env:VAR refs declared in mar.json
//   ├── mar.json          # the deployed config (env:VAR refs intact,
//   │                       no resolved secret values)
//   └── mar.db            # consistent snapshot via VACUUM INTO
//
// Invoked over SSH by `mar fly backup`. Doesn't include the runtime
// binary (Level-3 territory we deferred). Doesn't include Fly secrets
// — those live in Fly KV by design and the restore flow tells the
// operator which env vars to re-set.

package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"time"

	"mar/internal/project"
	"mar/internal/runtime"
)

// runRuntimeBackup produces the bundle at the given path. Caller is
// responsible for cleanup (the Fly orchestration in `mar fly backup`
// removes the file from the machine after sftp pulls it locally).
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

	// Stage the three files in a tmp dir, then tar+gzip the dir into
	// outPath. Cleanup via defer regardless of success.
	stage, err := os.MkdirTemp("", "mar-backup-stage-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mar-runtime backup: %v\n", err)
		return 1
	}
	defer os.RemoveAll(stage)

	// 1. mar.db snapshot via VACUUM INTO. Consistent under WAL,
	// doesn't block writers, output is a single self-contained .db
	// file (no -wal/-shm sidecars).
	dbSnap := filepath.Join(stage, "mar.db")
	db, err := runtime.OpenDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mar-runtime backup: open db: %v\n", err)
		return 1
	}
	if _, err := db.Exec("VACUUM INTO ?", dbSnap); err != nil {
		fmt.Fprintf(os.Stderr, "mar-runtime backup: snapshot: %v\n", err)
		return 1
	}

	// 2. mar.json — copied verbatim from the project source dir
	// (the bundled or extracted location). env:VAR refs stay
	// intact; resolved values never touch this file.
	manifestSrc := filepath.Join(projectDir, "mar.json")
	manifestRaw, err := os.ReadFile(manifestSrc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mar-runtime backup: read manifest: %v\n", err)
		return 1
	}
	if err := os.WriteFile(filepath.Join(stage, "mar.json"), manifestRaw, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "mar-runtime backup: stage manifest: %v\n", err)
		return 1
	}

	// 3. metadata.json — describes WHEN, WHAT, and WHICH SECRETS
	// the restore flow will need. Machine-readable JSON so a future
	// `mar fly restore` can validate compatibility before applying.
	meta := backupMetadata{
		CreatedAtUnixMs: time.Now().UnixMilli(),
		CreatedAtRFC3339: time.Now().UTC().Format(time.RFC3339),
		MarVersion:       hostBuildInfoOrDev(),
		BuildTarget:      goruntime.GOOS + "-" + goruntime.GOARCH,
		AppName:          manifestName(manifest),
		EnvRefs:          project.EnvRefsFromBytes(manifestRaw),
	}
	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mar-runtime backup: marshal metadata: %v\n", err)
		return 1
	}
	if err := os.WriteFile(filepath.Join(stage, "metadata.json"),
		append(metaBytes, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "mar-runtime backup: stage metadata: %v\n", err)
		return 1
	}

	// 4. Tar+gzip the staged dir into outPath.
	if err := writeTarGz(stage, outPath); err != nil {
		fmt.Fprintf(os.Stderr, "mar-runtime backup: archive: %v\n", err)
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

// backupMetadata is the shape of metadata.json inside the bundle.
// Stable across releases — older `mar fly restore` should be able
// to read newer bundles for diagnostics, even if it can't apply
// them. Add new fields, never rename or remove.
type backupMetadata struct {
	CreatedAtUnixMs  int64    `json:"createdAtUnixMs"`
	CreatedAtRFC3339 string   `json:"createdAtRFC3339"`
	MarVersion       string   `json:"marVersion"`
	BuildTarget      string   `json:"buildTarget"`
	AppName          string   `json:"appName"`
	EnvRefs          []string `json:"envRefs"`
}

// hostBuildInfoOrDev returns the mar version stamped at build time.
// Today mar-runtime doesn't carry a version variable populated via
// -ldflags (cmd/mar does, but we don't replicate the wiring here
// since prod backups don't depend on it for correctness — just for
// metadata.json display). Returns "dev" until that's wired.
func hostBuildInfoOrDev() string {
	return "dev"
}

// manifestName returns the `name` field from the manifest, or
// "(unknown)" if not present. Used in metadata so the restore flow
// can sanity-check that the bundle matches the target deployment.
func manifestName(m *project.Manifest) string {
	if m == nil || m.Name == "" {
		return "(unknown)"
	}
	return m.Name
}


// writeTarGz creates a gzipped tarball at outPath containing every
// file under stageDir. File names inside the archive are
// stage-relative (no leading dir) so the bundle layout matches
// what the docstring promises.
func writeTarGz(stageDir, outPath string) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()

	gz := gzip.NewWriter(out)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	entries, err := os.ReadDir(stageDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue // backup stage is flat by construction
		}
		full := filepath.Join(stageDir, e.Name())
		info, err := os.Stat(full)
		if err != nil {
			return err
		}
		hdr := &tar.Header{
			Name:    e.Name(),
			Mode:    0o644,
			Size:    info.Size(),
			ModTime: info.ModTime(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(full)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, f)
		f.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}
