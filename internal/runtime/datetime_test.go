package runtime

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestEntityCRUDSupportsDateTimeFields(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "datetime-crud.db"), `
(define event
  (entity
    (fields
      ((title string)
       (starts-at datetime)))
    (authorize
      (((read create update delete)
         true)))))

(define-app todo-api
  (entities event))
`)

	rec := doRuntimeRequest(r, http.MethodPost, "/events", `{"title":"Launch","starts_at":1742203200000}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rec.Code, rec.Body.String())
	}

	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response failed: %v body=%s", err, rec.Body.String())
	}
	if created["starts_at"] != float64(1742203200000) {
		t.Fatalf("expected starts_at to round-trip as Unix milliseconds, got %#v", created["starts_at"])
	}

	listRec := doRuntimeRequest(r, http.MethodGet, "/events", "", "")
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", listRec.Code, listRec.Body.String())
	}

	var rows []map[string]any
	if err := json.Unmarshal(listRec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode list response failed: %v body=%s", err, listRec.Body.String())
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0]["starts_at"] != float64(1742203200000) {
		t.Fatalf("expected listed starts_at to round-trip as Unix milliseconds, got %#v", rows[0]["starts_at"])
	}
}

func TestEntityCRUDNormalizesDateFieldsToUtcMidnight(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "date-crud.db"), `
(define student
  (entity
    (fields
      ((name string)
       (birthday date)))
    (authorize
      (((read create update delete)
         true)))))

(define-app todo-api
  (entities student))
`)

	rec := doRuntimeRequest(r, http.MethodPost, "/students", `{"name":"Ada","birthday":1742203500123}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rec.Code, rec.Body.String())
	}

	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response failed: %v body=%s", err, rec.Body.String())
	}

	expected := float64(time.Date(2025, time.March, 17, 0, 0, 0, 0, time.UTC).UnixMilli())
	if created["birthday"] != expected {
		t.Fatalf("expected birthday to normalize to UTC midnight, got %#v", created["birthday"])
	}
}
