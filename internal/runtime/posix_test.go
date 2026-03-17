package runtime

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
)

func TestEntityCRUDSupportsPosixFields(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "posix-crud.db"), `
app TodoApi

entity Event {
  title: String
  starts_at: Posix
  authorize all when true
}
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

func TestActionsSupportPosixInputFields(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "posix-action.db"), `
app TodoApi

entity Event {
  title: String
  starts_at: Posix
  authorize all when true
}

type alias ScheduleEventInput =
  { starts_at: Posix
  }

action scheduleEvent {
  input: ScheduleEventInput

  create Event {
    title: "Launch"
    starts_at: input.starts_at
  }
}
`)

	rec := doRuntimeRequest(r, http.MethodPost, "/actions/scheduleEvent", `{"starts_at":1742203200000}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rec.Code, rec.Body.String())
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
		t.Fatalf("expected action-created starts_at to round-trip as Unix milliseconds, got %#v", rows[0]["starts_at"])
	}
}
