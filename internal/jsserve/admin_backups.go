// Database backup endpoints for the admin panel.
//
//   GET  /_mar/admin/api/database-backups
//        — list catalog entries (newest first)
//
//   GET  /_mar/admin/api/database-backup/<id>
//        — download the bundle .tar.gz
//
//   POST /_mar/admin/api/database-backup/<id>/restore
//        — atomic-swap the live mar.db with the bundle's mar.db,
//          then exit the process so Fly auto-restart picks up the
//          new DB (or in dev, the user re-runs mar dev).
//          Refuses with 409 if schemas don't match.

package jsserve

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mar/internal/admin"
	"mar/internal/runtime"
)

// mountAdminBackupHandlers registers the three database-backup
// routes. Called from mountAdminHandlers alongside the other /api/*
// endpoints.
func mountAdminBackupHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/_mar/admin/api/database-backups", handleAdminListBackups)
	// Single prefix handler that parses /<id>[/restore] from the
	// remainder. Simpler than registering one mux entry per action.
	mux.HandleFunc("/_mar/admin/api/database-backup/", handleAdminBackupAction)
}

// handleAdminListBackups returns the catalog newest-first.
func handleAdminListBackups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := gateAdminSession(w, r); !ok {
		return
	}

	dbPath := runtime.CurrentDBPath()
	if dbPath == "" {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	entries, err := admin.ListCatalog(admin.CatalogDir(dbPath))
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"id":          e.ID,
			"sizeBytes":   e.SizeBytes,
			"createdAtMs": e.CreatedAt.UnixMilli(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// handleAdminBackupAction dispatches /<id> (download) and
// /<id>/restore based on path tail and method.
func handleAdminBackupAction(w http.ResponseWriter, r *http.Request) {
	if _, ok := gateAdminSession(w, r); !ok {
		return
	}

	tail := strings.TrimPrefix(r.URL.Path, "/_mar/admin/api/database-backup/")
	tail = strings.Trim(tail, "/")
	if tail == "" {
		http.Error(w, "missing backup id", http.StatusBadRequest)
		return
	}

	parts := strings.SplitN(tail, "/", 2)
	id := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}

	dbPath := runtime.CurrentDBPath()
	if dbPath == "" {
		writeAuthError(w, http.StatusServiceUnavailable, "no_database")
		return
	}
	bundlePath := admin.CatalogPath(admin.CatalogDir(dbPath), id)
	if _, err := os.Stat(bundlePath); err != nil {
		writeAuthError(w, http.StatusNotFound, "unknown_backup")
		return
	}

	switch action {
	case "":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		serveBundleDownload(w, bundlePath)
	case "restore":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		performRestore(w, dbPath, bundlePath)
	default:
		http.Error(w, "unknown action", http.StatusNotFound)
	}
}

// serveBundleDownload streams the tarball with appropriate headers
// so the browser saves it instead of trying to render.
func serveBundleDownload(w http.ResponseWriter, bundlePath string) {
	f, err := os.Open(bundlePath)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "open_failed")
		return
	}
	defer f.Close()
	info, _ := f.Stat()
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(bundlePath)))
	if info != nil {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	}
	_, _ = io.Copy(w, f)
}

// scheduleRestoreExit is the function performRestore calls to bring
// the process down after the swap so Fly auto-restart picks up the
// new mar.db. Production = os.Exit(2) after a 1.5s grace. Tests
// inject a no-op so the test process survives.
//
// Var (not const) so a test can swap it via SetRestoreExitFuncForTest.
// Defaults to the real exit.
var scheduleRestoreExit = func() {
	go func() {
		time.Sleep(1500 * time.Millisecond)
		fmt.Fprintln(os.Stderr, "[mar admin] restore complete; exiting for restart")
		os.Exit(2)
	}()
}

// SetRestoreExitFuncForTest swaps the post-restore exit hook to a
// caller-provided function. Test-only; production should never
// touch this. The caller is responsible for restoring the default
// after the test finishes.
func SetRestoreExitFuncForTest(fn func()) {
	scheduleRestoreExit = fn
}

// performRestore is the atomic-swap-and-exit flow:
//
//   1. Read the bundle's metadata.json + extract its mar.db to a
//      staging file.
//   2. Compare bundle's schemaFingerprint with the live DB's. Refuse
//      with 409 on mismatch.
//   3. Rename live mar.db → mar.db.bak-<timestamp>; move staged
//      mar.db → live path.
//   4. Respond 200 + "restartScheduled".
//   5. After a brief delay, os.Exit(2). Fly auto-restart kicks in
//      and the new process opens the restored DB. In dev (mar dev),
//      the user re-runs mar dev manually.
//
// Caller is responsible for ensuring no writes to the live DB
// during steps 3-5 — which is impossible at HTTP level. The
// process-exit afterward forces a clean reopen on the restored
// state. Anything written between the swap and the exit is lost
// (operator was told to expect this; restore is destructive).
func performRestore(w http.ResponseWriter, livePath, bundlePath string) {
	// Step 1: extract bundle into a stage dir.
	stage, err := os.MkdirTemp("", "mar-restore-stage-*")
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "stage_failed")
		return
	}
	// We deliberately DON'T defer RemoveAll(stage) here — once we
	// commit to the swap, the staged mar.db gets MOVED out (rename),
	// and the rest of the dir is harmless to leave. Cleanup happens
	// best-effort at the end of this function.

	bundleMeta, err := extractBundleForRestore(bundlePath, stage)
	if err != nil {
		_ = os.RemoveAll(stage)
		writeAuthError(w, http.StatusInternalServerError, "extract_failed")
		return
	}
	stagedDB := filepath.Join(stage, "mar.db")
	if _, err := os.Stat(stagedDB); err != nil {
		_ = os.RemoveAll(stage)
		writeAuthError(w, http.StatusInternalServerError, "bundle_missing_db")
		return
	}

	// Step 2: schema compatibility.
	liveDB, err := runtime.OpenDB()
	if err != nil {
		_ = os.RemoveAll(stage)
		writeAuthError(w, http.StatusServiceUnavailable, "no_database")
		return
	}
	liveFingerprint, err := admin.SchemaFingerprint(liveDB)
	if err != nil {
		_ = os.RemoveAll(stage)
		writeAuthError(w, http.StatusInternalServerError, "fingerprint_failed")
		return
	}
	if bundleMeta.SchemaFingerprint != liveFingerprint {
		_ = os.RemoveAll(stage)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":             "schema_mismatch",
			"bundleFingerprint": bundleMeta.SchemaFingerprint,
			"liveFingerprint":   liveFingerprint,
			"hint": "this backup was taken against a different schema (likely a migration ran since). " +
				"restore manually by deploying the matching binary first.",
		})
		return
	}

	// Step 3: atomic swap. Rename live → .bak-TS first; then move
	// staged into place. Both renames are atomic on the same
	// filesystem (the catalog lives in the same volume as the live
	// DB by construction).
	bakPath := fmt.Sprintf("%s.bak-%s", livePath, time.Now().UTC().Format("2006-01-02-150405"))
	if err := os.Rename(livePath, bakPath); err != nil {
		_ = os.RemoveAll(stage)
		writeAuthError(w, http.StatusInternalServerError, "swap_failed")
		return
	}
	// Also stash the WAL/SHM sidecars if they exist — they belong
	// to the old DB and need to move out of the way.
	for _, sidecar := range []string{livePath + "-wal", livePath + "-shm"} {
		if _, err := os.Stat(sidecar); err == nil {
			_ = os.Rename(sidecar, sidecar+".bak-restoring")
		}
	}
	if err := os.Rename(stagedDB, livePath); err != nil {
		// Roll back the live → .bak rename so we don't leave the
		// system without a mar.db.
		_ = os.Rename(bakPath, livePath)
		_ = os.RemoveAll(stage)
		writeAuthError(w, http.StatusInternalServerError, "install_failed")
		return
	}
	_ = os.RemoveAll(stage)

	// Step 4: respond with "restart scheduled". Client should
	// surface a banner and start polling /whoami until the server
	// is back up.
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"restartScheduled": true,
		"backupOf":         bakPath,
	})

	// Step 5: schedule the exit. The 1.5s grace lets the response
	// land before the process dies. Fly's restart_policy brings the
	// machine back up. (Tests can swap this for a no-op via
	// SetRestoreExitFuncForTest.)
	scheduleRestoreExit()
}

// extractBundleForRestore extracts the tarball entries we need
// (metadata.json + mar.db) into stageDir and returns the parsed
// metadata. mar.json is in the bundle but unused for restore — we
// only swap the database.
func extractBundleForRestore(bundlePath, stageDir string) (*admin.SnapshotMetadata, error) {
	f, err := os.Open(bundlePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	var meta *admin.SnapshotMetadata
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		// Defensive: skip absolute / parent-traversal paths.
		name := filepath.Base(hdr.Name)
		if name != hdr.Name {
			continue
		}
		switch name {
		case "mar.db":
			out, err := os.Create(filepath.Join(stageDir, "mar.db"))
			if err != nil {
				return nil, err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return nil, err
			}
			if err := out.Close(); err != nil {
				return nil, err
			}
		case "metadata.json":
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, err
			}
			var m admin.SnapshotMetadata
			if err := json.Unmarshal(data, &m); err != nil {
				return nil, err
			}
			meta = &m
		}
	}
	if meta == nil {
		return nil, fmt.Errorf("bundle missing metadata.json")
	}
	return meta, nil
}
