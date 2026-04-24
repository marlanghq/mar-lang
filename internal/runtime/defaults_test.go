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
(define todo
  (entity
    (fields
      ((title string)
       (done bool)
       (order-index int)
       (progress decimal)
       (due-at datetime)))
    (defaults
      ((title "Untitled")
       (done false)
       (order-index 0)
       (progress 0.5)
       (due-at 1742203200000)))
    (authorize
      (((read create update delete)
         true)))))

(define-app todo-api
  (entities todo))
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
	if created["progress"] != "0.5" {
		t.Fatalf("expected decimal default 0.5, got %#v", created["progress"])
	}
	if created["due_at"] != float64(1742203200000) {
		t.Fatalf("expected DateTime default, got %#v", created["due_at"])
	}
}

func TestEntityCRUDPreservesDecimalAsExactString(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "decimal-crud.db"), `
(define product
  (entity
    (fields
      ((price decimal)))
    (authorize
      (((read create update delete)
         true)))))

(define-app product-api
  (entities product))
`)

	rec := doRuntimeRequest(r, http.MethodPost, "/products", `{"price":"0.10"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rec.Code, rec.Body.String())
	}

	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response failed: %v body=%s", err, rec.Body.String())
	}
	if created["price"] != "0.1" {
		t.Fatalf("expected exact decimal string, got %#v", created["price"])
	}
}
