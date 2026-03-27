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
app TodoApi

entity Event {
  title: String
  starts_at: DateTime
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

func TestEntityCRUDNormalizesDateFieldsToUtcMidnight(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "date-crud.db"), `
app TodoApi

entity Student {
  name: String
  birthday: Date
  authorize all when true
}
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

func TestActionsSupportDateTimeInputFields(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "datetime-action.db"), `
app TodoApi

entity Event {
  title: String
  starts_at: DateTime
  authorize all when true
}

type alias ScheduleEventInput =
  { starts_at: DateTime
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

func TestActionsSupportUpdateAndDeleteSteps(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "action-update-delete.db"), `
app TodoApi

entity Todo {
  title: String
  done: Bool default false
  authorize all when true
}

type alias RenameTodoInput =
  { id: Int
  , title: String
  }

type alias DeleteTodoInput =
  { id: Int
  }

action renameTodo {
  input: RenameTodoInput

  update Todo {
    id: input.id
    title: input.title
  }
}

action deleteTodo {
  input: DeleteTodoInput

  delete Todo {
    id: input.id
  }
}
`)

	createRec := doRuntimeRequest(r, http.MethodPost, "/todos", `{"title":"Before"}`, "")
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 create, got %d body=%s", createRec.Code, createRec.Body.String())
	}

	updateRec := doRuntimeRequest(r, http.MethodPost, "/actions/renameTodo", `{"id":1,"title":"After"}`, "")
	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected 200 update action, got %d body=%s", updateRec.Code, updateRec.Body.String())
	}

	getRec := doRuntimeRequest(r, http.MethodGet, "/todos/1", "", "")
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200 get, got %d body=%s", getRec.Code, getRec.Body.String())
	}

	var updated map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode get response failed: %v body=%s", err, getRec.Body.String())
	}
	if updated["title"] != "After" {
		t.Fatalf("expected updated title After, got %#v", updated["title"])
	}

	deleteRec := doRuntimeRequest(r, http.MethodPost, "/actions/deleteTodo", `{"id":1}`, "")
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("expected 200 delete action, got %d body=%s", deleteRec.Code, deleteRec.Body.String())
	}

	missingRec := doRuntimeRequest(r, http.MethodGet, "/todos/1", "", "")
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d body=%s", missingRec.Code, missingRec.Body.String())
	}
}

func TestActionsSupportAliasedLoadAndStepOutputs(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "action-load-alias.db"), `
app TodoApi

entity Todo {
  title: String
  done: Bool default false
  authorize all when true
}

entity AuditLog {
  todoId: Int
  message: String
  authorize all when true
}

type alias CompleteTodoInput =
  { id: Int
  }

action completeTodo {
  input: CompleteTodoInput

  todo = load Todo {
    id: input.id
  }

  updatedTodo = update Todo {
    id: todo.id
    title: todo.title + " done"
    done: true
  }

  audit = create AuditLog {
    todoId: updatedTodo.id
    message: updatedTodo.title
  }
}
`)

	createRec := doRuntimeRequest(r, http.MethodPost, "/todos", `{"title":"Before"}`, "")
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 create, got %d body=%s", createRec.Code, createRec.Body.String())
	}

	actionRec := doRuntimeRequest(r, http.MethodPost, "/actions/completeTodo", `{"id":1}`, "")
	if actionRec.Code != http.StatusOK {
		t.Fatalf("expected 200 action, got %d body=%s", actionRec.Code, actionRec.Body.String())
	}

	getTodoRec := doRuntimeRequest(r, http.MethodGet, "/todos/1", "", "")
	if getTodoRec.Code != http.StatusOK {
		t.Fatalf("expected 200 get todo, got %d body=%s", getTodoRec.Code, getTodoRec.Body.String())
	}

	var todo map[string]any
	if err := json.Unmarshal(getTodoRec.Body.Bytes(), &todo); err != nil {
		t.Fatalf("decode todo failed: %v body=%s", err, getTodoRec.Body.String())
	}
	if todo["title"] != "Before done" {
		t.Fatalf("expected updated title, got %#v", todo["title"])
	}
	if todo["done"] != true {
		t.Fatalf("expected done true, got %#v", todo["done"])
	}

	listAuditRec := doRuntimeRequest(r, http.MethodGet, "/audit_logs", "", "")
	if listAuditRec.Code != http.StatusOK {
		t.Fatalf("expected 200 audit list, got %d body=%s", listAuditRec.Code, listAuditRec.Body.String())
	}

	var auditRows []map[string]any
	if err := json.Unmarshal(listAuditRec.Body.Bytes(), &auditRows); err != nil {
		t.Fatalf("decode audit list failed: %v body=%s", err, listAuditRec.Body.String())
	}
	if len(auditRows) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(auditRows))
	}
	if auditRows[0]["message"] != "Before done" {
		t.Fatalf("expected aliased create to use updated title, got %#v", auditRows[0]["message"])
	}
}
