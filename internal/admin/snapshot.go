// Snapshot — produces a single backup tarball from a live database.
// Used both by the auto-backup scheduler (one snapshot per tick) and
// the ad-hoc CLI path (force a snapshot now).
//
// Bundle shape (tarball entries):
//
//   metadata.json   timestamp, mar version, build target, app name,
//                   env:VAR refs, schemaFingerprint
//   mar.json        deployed config, env:VAR refs intact (no values)
//   mar.db          consistent snapshot via VACUUM INTO
//
// Caller passes the manifest + project dir (so we can find mar.json
// to copy verbatim) plus the live DB handle and the destination path.

package admin

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"time"

	"mar/internal/project"
)

// SnapshotInputs is the parameter bundle for WriteSnapshot. All
// fields are required.
type SnapshotInputs struct {
	DB         *sql.DB
	Manifest   *project.Manifest
	ProjectDir string
	OutPath    string
	Now        time.Time

	// MarVersion is the version stamp that lands in metadata.json.
	// Caller should pass the build-time stamp ("dev" if unstamped);
	// admin doesn't reach into the binary's version variables.
	MarVersion string
}

// SnapshotMetadata is the on-disk shape of metadata.json inside the
// bundle. Stable across releases — older readers should be able to
// decode newer bundles for diagnostics. Add fields, never rename.
type SnapshotMetadata struct {
	CreatedAtUnixMs   int64    `json:"createdAtUnixMs"`
	CreatedAtRFC3339  string   `json:"createdAtRFC3339"`
	MarVersion        string   `json:"marVersion"`
	BuildTarget       string   `json:"buildTarget"`
	AppName           string   `json:"appName"`
	EnvRefs           []string `json:"envRefs"`
	SchemaFingerprint string   `json:"schemaFingerprint"`
}

// WriteSnapshot stages the three files in a tmp dir, then tar+gzips
// them at OutPath. Cleanup of the tmp dir is automatic; on failure,
// any partial OutPath is best-effort removed.
func WriteSnapshot(in SnapshotInputs) error {
	if in.DB == nil {
		return fmt.Errorf("admin.WriteSnapshot: nil DB")
	}
	if in.OutPath == "" {
		return fmt.Errorf("admin.WriteSnapshot: empty OutPath")
	}

	stage, err := os.MkdirTemp("", "mar-snap-stage-*")
	if err != nil {
		return fmt.Errorf("admin.WriteSnapshot: mkdir tmp: %w", err)
	}
	defer os.RemoveAll(stage)

	// 1. mar.db via VACUUM INTO. Consistent under WAL, doesn't block
	// writers; output is a single self-contained .db (no -wal/-shm).
	dbSnap := filepath.Join(stage, "mar.db")
	if _, err := in.DB.Exec("VACUUM INTO ?", dbSnap); err != nil {
		_ = os.Remove(in.OutPath) // pre-existing partial, if any
		return fmt.Errorf("admin.WriteSnapshot: VACUUM INTO: %w", err)
	}

	// 2. mar.json — copy verbatim. env:VAR refs stay intact;
	// resolved secret values never touch this file.
	manifestSrc := filepath.Join(in.ProjectDir, "mar.json")
	manifestRaw, err := os.ReadFile(manifestSrc)
	if err != nil {
		return fmt.Errorf("admin.WriteSnapshot: read manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stage, "mar.json"), manifestRaw, 0o644); err != nil {
		return fmt.Errorf("admin.WriteSnapshot: stage manifest: %w", err)
	}

	// 3. metadata.json — describes the snapshot's identity. The
	// schemaFingerprint is what the restore flow uses to refuse
	// applying this bundle against a divergent live schema.
	fingerprint, err := SchemaFingerprint(in.DB)
	if err != nil {
		return fmt.Errorf("admin.WriteSnapshot: fingerprint: %w", err)
	}
	meta := SnapshotMetadata{
		CreatedAtUnixMs:   in.Now.UnixMilli(),
		CreatedAtRFC3339:  in.Now.UTC().Format(time.RFC3339),
		MarVersion:        defaultStr(in.MarVersion, "dev"),
		BuildTarget:       goruntime.GOOS + "-" + goruntime.GOARCH,
		AppName:           manifestName(in.Manifest),
		EnvRefs:           project.EnvRefsFromBytes(manifestRaw),
		SchemaFingerprint: fingerprint,
	}
	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("admin.WriteSnapshot: marshal metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stage, "metadata.json"),
		append(metaBytes, '\n'), 0o644); err != nil {
		return fmt.Errorf("admin.WriteSnapshot: stage metadata: %w", err)
	}

	// 4. Tar+gzip into OutPath.
	if err := writeSnapshotTarGz(stage, in.OutPath); err != nil {
		_ = os.Remove(in.OutPath)
		return fmt.Errorf("admin.WriteSnapshot: archive: %w", err)
	}
	return nil
}

func defaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func manifestName(m *project.Manifest) string {
	if m == nil || m.Name == "" {
		return "(unknown)"
	}
	return m.Name
}

// writeSnapshotTarGz creates a gzipped tarball at outPath
// containing every file directly under stageDir. File names inside
// the archive are stage-relative (no leading dir).
func writeSnapshotTarGz(stageDir, outPath string) error {
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
			continue
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
