package jsserve

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mar/internal/admin"
	"mar/internal/runtime"
)

// makeFakeBundle writes a valid tarball at `path` containing the
// three Level-2 entries: metadata.json + mar.json + mar.db. Used by
// the catalog / download tests that need a real .tar.gz on disk.
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
		// least syntactically a SQLite db.
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

// adminBackupsTestServer builds an admin test server with a real
// SQLite DB and a populated catalog directory, returning the
// authenticated cookie for hitting the API.
func adminBackupsTestServer(t *testing.T) (srv *adminTestSrv, cleanup func()) {
	t.Helper()
	server, c := adminTestServer(t, []string{"admin@x.com"})

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

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/_mar/admin/api/database-backups", nil)
	req.AddCookie(&http.Cookie{Name: "mar_admin_session", Value: srv.token})
	resp, err := srv.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status: %d", resp.StatusCode)
	}
	var body struct {
		Items []any `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 0 {
		t.Errorf("expected empty items; got %d", len(body.Items))
	}
}

// TestListBackups_WithEntries — items come back newest-first with
// the size+createdAt fields the UI needs.
func TestListBackups_WithEntries(t *testing.T) {
	srv, cleanup := adminBackupsTestServer(t)
	defer cleanup()

	dbPath := runtime.CurrentDBPath()
	catalogDir := admin.CatalogDir(dbPath)
	os.MkdirAll(catalogDir, 0o755)
	makeFakeBundle(t, filepath.Join(catalogDir, "2026-05-07-100000.tar.gz"), "x")
	makeFakeBundle(t, filepath.Join(catalogDir, "2026-05-08-120000.tar.gz"), "x")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/_mar/admin/api/database-backups", nil)
	req.AddCookie(&http.Cookie{Name: "mar_admin_session", Value: srv.token})
	resp, err := srv.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("expected 2 items; got %d", len(body.Items))
	}
	// Newest first.
	if body.Items[0]["id"] != "2026-05-08-120000" {
		t.Errorf("expected newest first; got %v", body.Items[0]["id"])
	}
	if _, ok := body.Items[0]["sizeBytes"]; !ok {
		t.Error("missing sizeBytes field")
	}
	if _, ok := body.Items[0]["createdAtMs"]; !ok {
		t.Error("missing createdAtMs field")
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
	resp, err := srv.Client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status: %d", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd == "" {
		t.Error("expected Content-Disposition header for download")
	}
}

// TestDownloadBackup_UnknownIDIs404 prevents enumeration of arbitrary
// catalog paths. Uses a well-shaped catalog id (YYYY-MM-DD-HHMMSS)
// that doesn't exist on disk — the shape check passes, the file
// lookup fails. Malformed ids go through TestDownloadBackup_InvalidIDIs400.
func TestDownloadBackup_UnknownIDIs404(t *testing.T) {
	srv, cleanup := adminBackupsTestServer(t)
	defer cleanup()

	req, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/_mar/admin/api/database-backup/2020-01-01-000000", nil)
	req.AddCookie(&http.Cookie{Name: "mar_admin_session", Value: srv.token})
	resp, err := srv.Client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
}

// TestDownloadBackup_InvalidIDIs400 — ids that don't match the
// catalog shape (path-traversal attempts, random strings) get
// rejected at the shape check before any filesystem lookup. Defense
// in depth: prevents the os.Stat → 404 path from ever touching
// crafted ids on disk.
func TestDownloadBackup_InvalidIDIs400(t *testing.T) {
	srv, cleanup := adminBackupsTestServer(t)
	defer cleanup()

	cases := []string{
		"does-not-exist",  // wrong shape (no time component)
		"2020-13-99-XXXX", // catalog-ish shape but invalid date
		"random",          // bare word
		"abc",             // too short
	}
	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet,
				srv.URL+"/_mar/admin/api/database-backup/"+id, nil)
			req.AddCookie(&http.Cookie{Name: "mar_admin_session", Value: srv.token})
			resp, err := srv.Client.Do(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("id=%q: status got %d, want 400", id, resp.StatusCode)
			}
		})
	}
}

// TestDownloadBackup_RequiresAuth — GET without session → 401.
func TestDownloadBackup_RequiresAuth(t *testing.T) {
	server, cleanup := adminTestServer(t, []string{"admin@x.com"})
	defer cleanup()

	req, _ := http.NewRequest(http.MethodGet,
		server.URL+"/_mar/admin/api/database-backup/2026-05-08-100000", nil)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", resp.StatusCode)
	}
}

// slugifyName turns a project name into a filesystem-safe download
// prefix; backupFilename uses it so a downloaded backup is identifiable
// in a shared Downloads folder (e.g. "myapp-2026-05-08-100000.tar.gz").
func TestSlugifyName(t *testing.T) {
	cases := map[string]string{
		"My App":     "my-app",
		"Mar Admin":  "mar-admin",
		"todo":       "todo",
		"  Spaces  ": "spaces",
		"a/b c":      "a-b-c",
		"my-app":     "my-app",
		"":           "",
		"---":        "",
	}
	for in, want := range cases {
		if got := slugifyName(in); got != want {
			t.Errorf("slugifyName(%q) = %q, want %q", in, got, want)
		}
	}
}
