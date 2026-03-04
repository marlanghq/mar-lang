package runtime

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestPublicServesEmbeddedFiles(t *testing.T) {
	requireSQLite3(t)

	publicDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(publicDir, "index.html"), []byte("<html><body>hello</body></html>"), 0o644); err != nil {
		t.Fatalf("write index.html failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(publicDir, "app.js"), []byte("console.log('ok')"), 0o644); err != nil {
		t.Fatalf("write app.js failed: %v", err)
	}

	app := mustParseApp(t, `
app FrontApi
database "./front.db"

public {
  dir "./frontend/dist"
  mount "/"
  spa_fallback "index.html"
}

entity Todo {
  title: String
}
`)
	app.Database = filepath.Join(t.TempDir(), "public-files.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}
	r.SetPublicFiles(os.DirFS(publicDir))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hello") {
		t.Fatalf("expected index html body, got %q", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/app.js", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /app.js, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "console.log") {
		t.Fatalf("expected JS body, got %q", rec.Body.String())
	}
}

func TestPublicSPAFallbackForRoutesWithoutFileExtension(t *testing.T) {
	requireSQLite3(t)

	publicDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(publicDir, "index.html"), []byte("<html><body>spa</body></html>"), 0o644); err != nil {
		t.Fatalf("write index.html failed: %v", err)
	}

	app := mustParseApp(t, `
app FrontApi
database "./front.db"

public {
  dir "./frontend/dist"
  mount "/"
  spa_fallback "index.html"
}

entity Todo {
  title: String
}
`)
	app.Database = filepath.Join(t.TempDir(), "public-spa.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}
	r.SetPublicFiles(os.DirFS(publicDir))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/orders/123", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for SPA fallback, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "spa") {
		t.Fatalf("expected fallback html, got %q", rec.Body.String())
	}
}

func TestPublicSPAFallbackDoesNotMaskMissingAssets(t *testing.T) {
	requireSQLite3(t)

	publicDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(publicDir, "index.html"), []byte("<html><body>spa</body></html>"), 0o644); err != nil {
		t.Fatalf("write index.html failed: %v", err)
	}

	app := mustParseApp(t, `
app FrontApi
database "./front.db"

public {
  dir "./frontend/dist"
  mount "/"
  spa_fallback "index.html"
}

entity Todo {
  title: String
}
`)
	app.Database = filepath.Join(t.TempDir(), "public-asset-404.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}
	r.SetPublicFiles(os.DirFS(publicDir))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/missing.js", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing asset, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Route not found") {
		t.Fatalf("expected route-not-found error body, got %q", rec.Body.String())
	}
}

func TestAdminPanelServedUnderBelmPrefix(t *testing.T) {
	requireSQLite3(t)

	app := mustParseApp(t, `
app FrontApi
database "./front.db"

public {
  dir "./frontend/dist"
  mount "/"
  spa_fallback "index.html"
}

entity Todo {
  title: String
}
`)
	app.Database = filepath.Join(t.TempDir(), "admin-prefix.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}
	r.SetPublicFiles(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html><body>public</body></html>")},
	})
	r.SetAdminFiles(fstest.MapFS{
		"index.html":   &fstest.MapFile{Data: []byte("<html><body>admin</body></html>")},
		"dist/app.js":  &fstest.MapFile{Data: []byte("console.log('admin')")},
		"dist/app.css": &fstest.MapFile{Data: []byte("body{}")},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_belm/admin", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /_belm/admin, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "admin") {
		t.Fatalf("expected admin html body, got %q", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/_belm/admin/dist/app.js", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /_belm/admin/dist/app.js, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "admin") {
		t.Fatalf("expected admin js body, got %q", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "public") {
		t.Fatalf("expected public html body, got %q", rec.Body.String())
	}
}
