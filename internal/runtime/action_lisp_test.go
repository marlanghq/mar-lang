package runtime

import (
	"net/http"
	"path/filepath"
	"testing"
)

func TestLispyActionEndpointRunsAgainstRuntime(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "action-lisp.db"), `
(define-entity todo
    (fields
      ((title string)
       (done bool)))
    (authorize
      (((read create update delete) true))))

(define-action complete-todo
    (input
      ((todo-id int)))
    (update todo todo-id
      ((done true))))

(define-app demo
  (backend
    (entities todo)
    (actions complete-todo)))
`)
	defer r.Close()

	createRec := doRuntimeRequest(r, http.MethodPost, "/todos", `{"title":"ship it","done":false}`, "")
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 when creating todo, got %d body=%s", createRec.Code, createRec.Body.String())
	}

	actionRec := doRuntimeRequest(r, http.MethodPost, "/actions/complete-todo", `{"todo_id":1}`, "")
	if actionRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from action endpoint, got %d body=%s", actionRec.Code, actionRec.Body.String())
	}

	row, ok, err := queryRow(r.DB, `SELECT done FROM todo WHERE id = 1`)
	if err != nil {
		t.Fatalf("query todo failed: %v", err)
	}
	if !ok {
		t.Fatal("expected todo row to exist")
	}
	if row["done"] != int64(1) {
		t.Fatalf("expected action to mark todo as done, got %#v", row["done"])
	}
}
