package runtime

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
)

func TestEntityCRUDAppliesFieldDefaultsWhenPayloadOmitsValues(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "defaults-crud.db"), `
app TodoApi

entity Todo {
  title: String default "Untitled"
  done: Bool default false
  order_index: Int default 0
  progress: Float default 0.5
  due_at: DateTime default 1742203200000
  authorize read, create, update, delete when anonymous or user_authenticated
}
`)

	rec := doRuntimeRequest(r, http.MethodPost, "/todos", `{}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rec.Code, rec.Body.String())
	}

	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response failed: %v body=%s", err, rec.Body.String())
	}
	if created["title"] != "Untitled" {
		t.Fatalf("expected string default, got %#v", created["title"])
	}
	if created["done"] != false {
		t.Fatalf("expected bool default false, got %#v", created["done"])
	}
	if created["order_index"] != float64(0) {
		t.Fatalf("expected int default 0, got %#v", created["order_index"])
	}
	if created["progress"] != 0.5 {
		t.Fatalf("expected float default 0.5, got %#v", created["progress"])
	}
	if created["due_at"] != float64(1742203200000) {
		t.Fatalf("expected DateTime default, got %#v", created["due_at"])
	}
}
