package runtime

import (
	"encoding/json"
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
(define app-config
  ((database "./front.db")
   (public
     ((dir "./frontend/dist")
      (mount "/")
      (spa-fallback "index.html")))))

(define todo
  (entity
    (fields
      ((title string)))))

(define-app front-api
  (config app-config)
  (entities todo))
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
(define app-config
  ((database "./front.db")
   (public
     ((dir "./frontend/dist")
      (mount "/")
      (spa-fallback "index.html")))))

(define todo
  (entity
    (fields
      ((title string)))))

(define-app front-api
  (config app-config)
  (entities todo))
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
(define app-config
  ((database "./front.db")
   (public
     ((dir "./frontend/dist")
      (mount "/")
      (spa-fallback "index.html")))))

(define todo
  (entity
    (fields
      ((title string)))))

(define-app front-api
  (config app-config)
  (entities todo))
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

func TestRootRedirectsToAppUIWhenNoPublicAppIsConfigured(t *testing.T) {
	requireSQLite3(t)

	app := mustParseApp(t, `
(define app-config
  ((database "./todo.db")))

(define todo
  (entity
    (fields
      ((title string)))))

(define-app todo-api
  (config app-config)
  (entities todo))
`)
	app.Database = filepath.Join(t.TempDir(), "root-redirect.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}
	r.SetAppUIFiles(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html><body>admin</body></html>")},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 for / without public app, got %d body=%s", rec.Code, rec.Body.String())
	}
	if location := rec.Header().Get("Location"); location != "/_mar" {
		t.Fatalf("expected redirect to /_mar, got %q", location)
	}
}

func TestAppUIServedUnderMarPrefix(t *testing.T) {
	requireSQLite3(t)

	app := mustParseApp(t, `
(define app-config
  ((database "./front.db")
   (public
     ((dir "./frontend/dist")
      (mount "/")
      (spa-fallback "index.html")))))

(define todo
  (entity
    (fields
      ((title string)))))

(define-app front-api
  (config app-config)
  (entities todo))
`)
	app.Database = filepath.Join(t.TempDir(), "app-ui-prefix.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}
	r.SetPublicFiles(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html><body>public</body></html>")},
	})
	r.SetAppUIFiles(fstest.MapFS{
		"index.html":   &fstest.MapFile{Data: []byte("<html><body>admin</body></html>")},
		"dist/app.js":  &fstest.MapFile{Data: []byte("console.log('admin')")},
		"dist/app.css": &fstest.MapFile{Data: []byte("body{}")},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_mar", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /_mar, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "admin") {
		t.Fatalf("expected app UI html body, got %q", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/_mar/dist/app.js", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /_mar/dist/app.js, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "admin") {
		t.Fatalf("expected app UI js body, got %q", rec.Body.String())
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

func TestSchemaEndpointStillWorksUnderMarPrefix(t *testing.T) {
	requireSQLite3(t)

	app := mustParseApp(t, `
(define app-config
  ((database "./front.db")))

(define todo
  (entity
    (fields
      ((title string)))))

(define-app front-api
  (config app-config)
  (entities todo))
`)
	app.Database = filepath.Join(t.TempDir(), "mar-schema-endpoint.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}
	r.SetAppUIFiles(fstest.MapFS{
		"index.html":   &fstest.MapFile{Data: []byte("<html><body>app-ui</body></html>")},
		"schema":       &fstest.MapFile{Data: []byte("not-json")},
		"dist/app.js":  &fstest.MapFile{Data: []byte("console.log('app-ui')")},
		"dist/app.css": &fstest.MapFile{Data: []byte("body{}")},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_mar/schema", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /_mar/schema, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"appName":"front-api"`) {
		t.Fatalf("expected schema JSON body, got %q", rec.Body.String())
	}
}

func TestAppUIIndexEmbedsBootstrapSchemaAndVersion(t *testing.T) {
	requireSQLite3(t)

	app := mustParseApp(t, `
(define app-config
  ((database "./front.db")))

(define todo
  (entity
    (fields
      ((title string)))))

(define-app front-api
  (config app-config)
  (entities todo))
`)
	app.Database = filepath.Join(t.TempDir(), "mar-app-ui-bootstrap.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}
	r.SetVersionInfo(VersionInfo{ManifestHash: "sha256:testhash"})
	r.SetAppUIFiles(fstest.MapFS{
		"index.html":  &fstest.MapFile{Data: []byte(`<html><body><script id="mar-bootstrap" type="application/json">__MAR_BOOTSTRAP_JSON__</script></body></html>`)},
		"dist/app.js": &fstest.MapFile{Data: []byte("console.log('app-ui')")},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_mar", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /_mar, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "__MAR_BOOTSTRAP_JSON__") {
		t.Fatalf("expected bootstrap placeholder to be replaced, got %q", body)
	}
	if !strings.Contains(body, `"appName":"front-api"`) {
		t.Fatalf("expected embedded schema app name, got %q", body)
	}
	if !strings.Contains(body, `"manifestHash":"sha256:testhash"`) {
		t.Fatalf("expected embedded manifest hash, got %q", body)
	}
}

func TestSchemaEndpointIncludesFrontend(t *testing.T) {
	requireSQLite3(t)

	app := mustParseApp(t, `
(define app-config
  ((database "./blog.db")))

(define post
  (entity
    (fields
      ((title string)))))

(define-screen home
  (view
    (section
      (title "Blog")
      (text "Browse posts"))))

(define-screen post-detail
  (view
    (section
      (title "Post")
      (text "Post detail"))))

(define-app blog
  (config app-config)
  (entities post)
  (screens home post-detail))
`)
	app.Database = filepath.Join(t.TempDir(), "mar-schema-frontend.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_mar/schema", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /_mar/schema, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode schema response: %v", err)
	}

	screensPayload, ok := payload["screens"].(map[string]any)
	if !ok {
		t.Fatalf("expected screens object, got %#v", payload["screens"])
	}
	screens, ok := screensPayload["screens"].([]any)
	if !ok || len(screens) != 2 {
		t.Fatalf("expected 2 screens, got %#v", screensPayload["screens"])
	}
	first, ok := screens[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first screen object, got %#v", screens[0])
	}
	if first["name"] != "Home" {
		t.Fatalf("unexpected first screen: %#v", first)
	}
}
