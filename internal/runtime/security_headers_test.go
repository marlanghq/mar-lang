package runtime

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestDefaultSecurityHeaders(t *testing.T) {
	requireSQLite3(t)

	app := mustParseApp(t, `
(define todo
  (entity
    (fields
      ((title string)))))

(define-app security-headers-api
  (entities todo))
`)
	app.Database = filepath.Join(t.TempDir(), "security-defaults.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	r.handleHTTP(rec, req)

	if rec.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatalf("unexpected X-Frame-Options: %q", rec.Header().Get("X-Frame-Options"))
	}
	if rec.Header().Get("Referrer-Policy") != "strict-origin-when-cross-origin" {
		t.Fatalf("unexpected Referrer-Policy: %q", rec.Header().Get("Referrer-Policy"))
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unexpected X-Content-Type-Options: %q", rec.Header().Get("X-Content-Type-Options"))
	}
}

func TestSystemSecurityHeadersOverride(t *testing.T) {
	requireSQLite3(t)

	app := mustParseApp(t, `
(define app-auth
  ((security-frame-policy "deny")
   (security-referrer-policy "no-referrer")
   (security-content-type-nosniff false)))

(define todo
  (entity
    (fields
      ((title string)))))

(define-app security-headers-api
  (auth app-auth)
  (entities todo))
`)
	app.Database = filepath.Join(t.TempDir(), "security-overrides.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	r.handleHTTP(rec, req)

	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("unexpected X-Frame-Options: %q", rec.Header().Get("X-Frame-Options"))
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("unexpected Referrer-Policy: %q", rec.Header().Get("Referrer-Policy"))
	}
	if rec.Header().Get("X-Content-Type-Options") != "" {
		t.Fatalf("expected X-Content-Type-Options to be omitted, got %q", rec.Header().Get("X-Content-Type-Options"))
	}
}
