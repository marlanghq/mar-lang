package runtime

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/parser"
)

func TestCreateUnknownFieldSuggestsClosestName(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "unknown-field-create.db"), `
(define todo
  (entity
    (fields
      ((title string)))))

(define-app todo-api
  (entities todo))
`)

	rec := doRuntimeRequest(r, http.MethodPost, "/todos", `{"titel":"Buy milk"}`, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `Did you mean \"title\"?`) {
		t.Fatalf("expected Did you mean suggestion, got body=%s", rec.Body.String())
	}
}

func mustNewRuntimeFromSource(t *testing.T, dbPath, src string) *Runtime {
	t.Helper()

	app, err := parser.Parse(strings.TrimSpace(src) + "\n")
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
