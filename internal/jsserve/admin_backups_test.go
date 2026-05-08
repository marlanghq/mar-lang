package jsserve

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mar/internal/admin"
	"mar/internal/runtime"
)

// stubExitForTest swaps out scheduleRestoreExit so test runs don't
// kill the test process when performRestore completes successfully.
// Returns a cleanup func the caller defers.
func stubExitForTest(t *testing.T) (called *bool) {
	t.Helper()
	flag := false
	prev := scheduleRestoreExit
	scheduleRestoreExit = func() { flag = true }
	t.Cleanup(func() { scheduleRestoreExit = prev })
	return &flag
}

// makeFakeBundle writes a valid tarball at `path` containing the
// three Level-2 entries: metadata.json + mar.json + mar.db. The
// caller controls the schemaFingerprint inside metadata so tests
// can drive both the match-and-restore and mismatch-and-refuse
// paths.
func makeFakeBundle(t *testing.T, path string, fingerprint string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	meta := admin.SnapshotMetadata{
		CreatedAtUnixMs:   time.Now().UnixMilli(),
		CreatedAtRFC3339:  time.Now().UTC().Format(time.RFC3339),
		MarVersion:        "test",
		BuildTarget:       "test-test",
		AppName:           "test",
		EnvRefs:           nil,
		SchemaFingerprint: fingerprint,
	}
	metaBytes, _ := json.Marshal(meta)

	entries := []struct {
		name string
		data []byte
	}{
		{"metadata.json", metaBytes},
		{"mar.json", []byte(`{"name":"test"}`)},
		// Minimal SQLite header bytes ('SQLite format 3\x00') + zero
		// pad to 100 bytes (the SQLite header size) so the file is at
		// least syntactically a SQLite db. Restore won't run queries
		// against this; it only needs to be present and copyable.
		{"mar.db", append([]byte("SQLite format 3\x00"), make([]byte, 84)...)},
	}

	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name: e.name,
			Mode: 0o644,
			Size: int64(len(e.data)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
}

// TestExtractBundleForRestore_HappyPath — staged dir gets mar.db,
// returned metadata matches what the bundle had.
func TestExtractBundleForRestore_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	bundlePath := filepath.Join(tmp, "bundle.tar.gz")
	makeFakeBundle(t, bundlePath, "sha256:fake")

	stage := filepath.Join(tmp, "stage")
	os.MkdirAll(stage, 0o755)

	meta, err := extractBundleForRestore(bundlePath, stage)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if meta.SchemaFingerprint != "sha256:fake" {
		t.Errorf("metadata fingerprint: got %q, want %q",
			meta.SchemaFingerprint, "sha256:fake")
	}
	stagedDB := filepath.Join(stage, "mar.db")
	if _, err := os.Stat(stagedDB); err != nil {
		t.Errorf("expected mar.db in stage; got %v", err)
	}
}

// TestExtractBundleForRestore_MissingMetadata — a malformed bundle
// (no metadata.json entry) returns an error so the swap never
// proceeds.
func TestExtractBundleForRestore_MissingMetadata(t *testing.T) {
	tmp := t.TempDir()
	bundlePath := filepath.Join(tmp, "bad.tar.gz")
	out, _ := os.Create(bundlePath)
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	// Just one entry, not metadata.json.
	tw.WriteHeader(&tar.Header{Name: "mar.db", Size: 4})
	tw.Write([]byte("data"))
	tw.Close()
	gz.Close()
	out.Close()

	stage := t.TempDir()
	if _, err := extractBundleForRestore(bundlePath, stage); err == nil {
		t.Error("expected error for missing metadata.json; got nil")
	}
}

// adminBackupsTestServer builds an admin test server with a real
// SQLite DB and a populated catalog directory, returning the
// authenticated cookie for hitting the API.
func adminBackupsTestServer(t *testing.T) (srv *adminTestSrv, cleanup func()) {
	t.Helper()
	server, c := adminTestServer(t, []string{"admin@x.com"})
	stubExitForTest(t)

	// Sign in.
	out := captureStdout(t, func() {
		_, _ = postJSON(t, server.Client(), server.URL+"/_mar/admin/auth/request-code",
			map[string]string{"email": "admin@x.com"})
	})
	code := extractSinkCode(t, out)
	verifyResp, _ := postJSON(t, server.Client(), server.URL+"/_mar/admin/auth/verify-code",
		map[string]string{"email": "admin@x.com", "code": code})
	var token string
	for _, ck := range verifyResp.Cookies() {
		if ck.Name == "mar_admin_session" {
			token = ck.Value
		}
	}
	if token == "" {
		t.Fatal("auth setup: no cookie")
	}

	return &adminTestSrv{
		URL:    server.URL,
		Client: server.Client(),
		token:  token,
	}, c
}

type adminTestSrv struct {
	URL    string
	Client *http.Client
	token  string
}

// TestListBackups_EmptyCatalog returns items: [].
func TestListBackups_EmptyCatalog(t *testing.T) {
	srv, cleanup := adminBackupsTestServer(t)
	defer cleanup()

	req, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/_mar/admin/api/database-backups", nil)
	req.AddCookie(&http.Cookie{Name: "mar_admin_session", Value: srv.token})
	resp, err := srv.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status: %d", resp.StatusCode)
	}
	var got map[string]any
	json.NewDecoder(resp.Body).Decode(&got)
	items, _ := got["items"].([]any)
	if len(items) != 0 {
		t.Errorf("expected empty items; got %v", items)
	}
}

// TestListBackups_WithEntries shows everything in the catalog dir
// newest-first.
func TestListBackups_WithEntries(t *testing.T) {
	srv, cleanup := adminBackupsTestServer(t)
	defer cleanup()

	dbPath := runtime.CurrentDBPath()
	catalogDir := admin.CatalogDir(dbPath)
	os.MkdirAll(catalogDir, 0o755)
	makeFakeBundle(t, filepath.Join(catalogDir, "2026-05-08-100000.tar.gz"), "x")
	makeFakeBundle(t, filepath.Join(catalogDir, "2026-05-08-120000.tar.gz"), "x")

	req, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/_mar/admin/api/database-backups", nil)
	req.AddCookie(&http.Cookie{Name: "mar_admin_session", Value: srv.token})
	resp, _ := srv.Client.Do(req)
	defer resp.Body.Close()
	var got map[string]any
	json.NewDecoder(resp.Body).Decode(&got)
	items, _ := got["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 items; got %d", len(items))
	}
	first, _ := items[0].(map[string]any)
	if first["id"] != "2026-05-08-120000" {
		t.Errorf("expected newest first; got %v", first["id"])
	}
}

// TestRestoreBackup_RefusesSchemaMismatch — bundle with a different
// fingerprint returns 409 + descriptive error; the live DB is
// untouched, no .bak created, no exit scheduled.
func TestRestoreBackup_RefusesSchemaMismatch(t *testing.T) {
	srv, cleanup := adminBackupsTestServer(t)
	defer cleanup()
	exitCalled := stubExitForTest(t)

	dbPath := runtime.CurrentDBPath()
	catalogDir := admin.CatalogDir(dbPath)
	os.MkdirAll(catalogDir, 0o755)
	bundlePath := filepath.Join(catalogDir, "2026-05-08-100000.tar.gz")
	makeFakeBundle(t, bundlePath, "sha256:wrong-fingerprint")

	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/_mar/admin/api/database-backup/2026-05-08-100000/restore", nil)
	req.AddCookie(&http.Cookie{Name: "mar_admin_session", Value: srv.token})
	resp, err := srv.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status: got %d, want 409", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("schema_mismatch")) {
		t.Errorf("expected schema_mismatch in body; got %s", body)
	}
	if *exitCalled {
		t.Error("exit must NOT be scheduled on schema mismatch")
	}
	// .bak shouldn't exist either.
	matches, _ := filepath.Glob(dbPath + ".bak-*")
	if len(matches) > 0 {
		t.Errorf("no .bak should be created on mismatch; found %v", matches)
	}
}

// TestRestoreBackup_AcceptsMatchingSchema — when fingerprints
// match, the swap happens, response is 200, exit is scheduled.
func TestRestoreBackup_AcceptsMatchingSchema(t *testing.T) {
	srv, cleanup := adminBackupsTestServer(t)
	defer cleanup()
	exitCalled := stubExitForTest(t)

	// Compute the live DB's fingerprint and make a bundle with the
	// SAME value so the restore proceeds.
	db, _ := runtime.OpenDB()
	liveFingerprint, _ := admin.SchemaFingerprint(db)

	dbPath := runtime.CurrentDBPath()
	catalogDir := admin.CatalogDir(dbPath)
	os.MkdirAll(catalogDir, 0o755)
	bundlePath := filepath.Join(catalogDir, "2026-05-08-100000.tar.gz")
	makeFakeBundle(t, bundlePath, liveFingerprint)

	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/_mar/admin/api/database-backup/2026-05-08-100000/restore", nil)
	req.AddCookie(&http.Cookie{Name: "mar_admin_session", Value: srv.token})
	resp, err := srv.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d, want 200; body=%s", resp.StatusCode, body)
	}

	// .bak file should exist now (the live mar.db was renamed).
	matches, _ := filepath.Glob(dbPath + ".bak-*")
	if len(matches) == 0 {
		t.Errorf("expected .bak file to be created; none found")
	}

	// Exit should have been "scheduled" (test stub captures the call).
	if !*exitCalled {
		t.Error("expected scheduleRestoreExit to be called on success")
	}
}

// TestDownloadBackup streams the bundle bytes with the right
// Content-Disposition for the browser save dialog.
func TestDownloadBackup(t *testing.T) {
	srv, cleanup := adminBackupsTestServer(t)
	defer cleanup()

	dbPath := runtime.CurrentDBPath()
	catalogDir := admin.CatalogDir(dbPath)
	os.MkdirAll(catalogDir, 0o755)
	makeFakeBundle(t, filepath.Join(catalogDir, "2026-05-08-100000.tar.gz"), "x")

	req, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/_mar/admin/api/database-backup/2026-05-08-100000", nil)
	req.AddCookie(&http.Cookie{Name: "mar_admin_session", Value: srv.token})
	resp, _ := srv.Client.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status: %d", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd == "" {
		t.Error("expected Content-Disposition header for download")
	}
}

// TestDownloadBackup_UnknownIDIs404 prevents enumeration of arbitrary
// catalog paths.
func TestDownloadBackup_UnknownIDIs404(t *testing.T) {
	srv, cleanup := adminBackupsTestServer(t)
	defer cleanup()

	req, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/_mar/admin/api/database-backup/does-not-exist", nil)
	req.AddCookie(&http.Cookie{Name: "mar_admin_session", Value: srv.token})
	resp, _ := srv.Client.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
}

// TestRestoreBackup_RequiresAuth — POST without session → 401.
func TestRestoreBackup_RequiresAuth(t *testing.T) {
	server, cleanup := adminTestServer(t, []string{"admin@x.com"})
	defer cleanup()

	req, _ := http.NewRequest(http.MethodPost,
		server.URL+"/_mar/admin/api/database-backup/2026-05-08-100000/restore", nil)
	resp, _ := server.Client().Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", resp.StatusCode)
	}
}
