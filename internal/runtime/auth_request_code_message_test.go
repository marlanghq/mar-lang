package runtime

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/parser"
)

func TestRequestCodeMessageIncludesDevConsoleHintInDevMode(t *testing.T) {
	requireSQLite3(t)
	t.Setenv("MAR_DEV_MODE", "1")

	r := mustNewRuntimeWithoutExplicitAuth(t, filepath.Join(t.TempDir(), "request-code-dev-message.db"))

	rec := doRuntimeRequest(r, http.MethodPost, "/auth/request-code", `{"email":"dev@example.com"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from request-code, got %d body=%s", rec.Code, rec.Body.String())
	}

	var response struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode request-code response failed: %v body=%s", err, rec.Body.String())
	}
	if !strings.Contains(response.Message, "You are running in dev mode, so check the console output.") {
		t.Fatalf("expected dev-mode console hint in message, got %q", response.Message)
	}
}

func TestRequestCodeMessageStaysGenericOutsideDevMode(t *testing.T) {
	requireSQLite3(t)
	t.Setenv("MAR_DEV_MODE", "")
	t.Setenv("TEST_SMTP_PASSWORD", "secret")
	restore := stubSMTPDial(t)
	defer restore()

	r := mustNewRuntimeForSMTPAuth(t, filepath.Join(t.TempDir(), "request-code-generic-message.db"))

	rec := doRuntimeRequest(r, http.MethodPost, "/auth/request-code", `{"email":"generic@example.com"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from request-code, got %d body=%s", rec.Code, rec.Body.String())
	}

	var response struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode request-code response failed: %v body=%s", err, rec.Body.String())
	}
	const expected = "If this email exists, a code was sent."
	if response.Message != expected {
		t.Fatalf("expected %q, got %q", expected, response.Message)
	}
}

func mustNewRuntimeWithoutExplicitAuth(t *testing.T, dbPath string) *Runtime {
	t.Helper()

	app, err := parser.Parse(strings.TrimSpace(`
(define todo
  (entity
    (fields
      ((title string)))))

(define-app todo-api
  (entities todo))
`) + "\n")
	if err != nil {
		t.Fatalf("failed to parse app: %v", err)
	}
	app.Database = dbPath

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}
	return r
}

func mustNewRuntimeForSMTPAuth(t *testing.T, dbPath string) *Runtime {
	t.Helper()

	app, err := parser.Parse(strings.TrimSpace(`
(define app-auth
  ((from "no-reply@example.com")
   (smtp-host "127.0.0.1")
   (smtp-port 587)
   (smtp-username "resend")
   (smtp-password-env "TEST_SMTP_PASSWORD")
   (smtp-starttls false)))

(define todo
  (entity
    (fields
      ((title string)))))

(define-app todo-api
  (auth app-auth)
  (entities todo))
`) + "\n")
	if err != nil {
		t.Fatalf("failed to parse app: %v", err)
	}
	app.Database = dbPath

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}
	return r
}
