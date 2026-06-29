// Database backup endpoints for the admin panel.
//
//   GET  /_mar/admin/api/database-backups
//        — list catalog entries (newest first)
//
//   GET  /_mar/admin/api/database-backup/<id>
//        — download the bundle .tar.gz
//
// Restore is intentionally NOT exposed over HTTP: it needs a process
// restart + supervisor coordination that varies by platform, and the
// `mar dev` loop has no supervisor at all. The supported restore flow
// is the `mar-runtime restore-db` CLI — operator downloads the bundle,
// stops the process, runs the restore command, restarts.

package jsserve

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"mar/internal/admin"
	"mar/internal/runtime"
)

// mountAdminBackupHandlers registers the database-backup routes.
// Called from mountAdminHandlers alongside the other /api/*
// endpoints.
func mountAdminBackupHandlers(mux *http.ServeMux) {
	// Both endpoints sit behind the gateway rate limiter
	// (mar.json["rateLimit"], per-IP). Backup download is bandwidth-
	// heavy so the gateway is doubly useful here.
	mux.HandleFunc("/_mar/admin/api/database-backups", rateLimit(handleAdminListBackups))
	mux.HandleFunc("/_mar/admin/api/database-backup/", rateLimit(handleAdminBackupAction))
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

// handleAdminBackupAction serves GET /<id> (download the bundle).
// Restore is handled out-of-band by the `mar-runtime restore-db`
// CLI — see file-level comment.
func handleAdminBackupAction(w http.ResponseWriter, r *http.Request) {
	if _, ok := gateAdminSession(w, r); !ok {
		return
	}

	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/_mar/admin/api/database-backup/"), "/")
	if id == "" {
		http.Error(w, "missing backup id", http.StatusBadRequest)
		return
	}

	// Defense-in-depth against path traversal: validate the id shape
	// before it ever reaches filepath.Join. Even though we admin-gate
	// the route and os.Stat would 404 on a non-existent file, a
	// crafted id like "..%2Fsomething" would otherwise round-trip
	// through Join and could match any .tar.gz on disk readable by
	// the process. IsValidCatalogID rejects anything that isn't the
	// canonical YYYY-MM-DD-HHMMSS layout.
	if !admin.IsValidCatalogID(id) {
		writeAuthError(w, http.StatusBadRequest, "invalid_backup_id")
		return
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

	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	serveBundleDownload(w, bundlePath)
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
		fmt.Sprintf(`attachment; filename="%s"`, backupFilename(filepath.Base(bundlePath))))
	if info != nil {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	}
	_, _ = io.Copy(w, f)
}

// backupFilename prefixes the bundle's base name with a filesystem-safe
// slug of the project name (mar.json "name"), so a download landing in a
// shared Downloads folder is identifiable — e.g.
// "myapp-2026-06-09-120000.tar.gz". Falls back to the bare base name when
// no project name is configured.
func backupFilename(base string) string {
	if slug := slugifyName(currentPWA().Name); slug != "" {
		return slug + "-" + base
	}
	return base
}

// slugifyName lowercases s and keeps only [a-z0-9._], collapsing every
// other run of characters to a single dash. Returns "" when nothing
// usable remains.
func slugifyName(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' {
			b.WriteRune(r)
			dash = false
		} else if b.Len() > 0 && !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
