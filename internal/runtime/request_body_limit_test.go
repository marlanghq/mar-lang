package runtime

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequestBodyLimitDefaultIs1MB(t *testing.T) {
	requireSQLite3(t)

	app := mustParseApp(t, `
(define-entity todo
    (fields
      ((title string)))
    (authorize
      ((create true))))

(define-app body-limit-api
  (entities todo))
`)
	app.Database = filepath.Join(t.TempDir(), "body-limit-default.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}

	tooLargeTitle := strings.Repeat("a", 1024*1024)
	body := `{"title":"` + tooLargeTitle + `"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/todos", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.handleHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized request body, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Request body too large") {
		t.Fatalf("expected body-too-large message, got %s", rec.Body.String())
	}
}

func TestRequestBodyLimitCanBeOverridden(t *testing.T) {
	requireSQLite3(t)

	app := mustParseApp(t, `
(define app-config
  ((system
     ((http-max-request-body-mb 2)))))

(define-entity todo
    (fields
      ((title string)))
    (authorize
      ((create true))))

(define-app body-limit-api
  (config app-config)
  (entities todo))
`)
	app.Database = filepath.Join(t.TempDir(), "body-limit-custom.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}

	largeButAllowedTitle := strings.Repeat("b", 1200*1024)
	body := `{"title":"` + largeButAllowedTitle + `"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/todos", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.handleHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for allowed request body, got %d body=%s", rec.Code, rec.Body.String())
	}
}
