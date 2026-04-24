package runtime

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/expr"
	"mar/internal/model"
)

func TestLispyQueryEndpointRunsAgainstRuntime(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "query-lisp.db"), `
(define todo
  (entity
    (fields
      ((title string)
       (done bool)))
    (authorize
      (((read create update delete) true)))))

(define (open-todos)
  (query todo
    (where (= done false))
    (order-by title asc)))

(define-app demo
  (backend
    (entities todo)
    (queries open-todos)))
`)
	defer r.Close()

	for _, body := range []string{
		`{"title":"ship it","done":false}`,
		`{"title":"archive it","done":true}`,
	} {
		createRec := doRuntimeRequest(r, http.MethodPost, "/todos", body, "")
		if createRec.Code != http.StatusCreated {
			t.Fatalf("expected 201 when creating todo, got %d body=%s", createRec.Code, createRec.Body.String())
		}
	}

	queryRec := doRuntimeRequest(r, http.MethodGet, "/queries/open-todos", "", "")
	if queryRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from query endpoint, got %d body=%s", queryRec.Code, queryRec.Body.String())
	}

	var rows []map[string]any
	if err := json.Unmarshal(queryRec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode query response failed: %v body=%s", err, queryRec.Body.String())
	}
	if len(rows) != 1 {
		t.Fatalf("expected one open todo, got %#v", rows)
	}
	if rows[0]["title"] != "ship it" {
		t.Fatalf("expected open todo title, got %#v", rows[0]["title"])
	}

	postRec := doRuntimeRequest(r, http.MethodPost, "/queries/open-todos", `{}`, "")
	if postRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST query endpoint, got %d body=%s", postRec.Code, postRec.Body.String())
	}
}

func TestSchemaPayloadExposesPublicQueryPath(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "query-schema.db"), `
(define todo
  (entity
    (fields
      ((title string)))))

(define (open-todos)
  (query todo))

(define-app demo
  (backend
    (entities todo)
    (queries open-todos)))
`)
	defer r.Close()

	rec := doRuntimeRequest(r, http.MethodGet, "/_mar/schema", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from schema endpoint, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Queries []struct {
			Name   string `json:"name"`
			Path   string `json:"path"`
			Entity string `json:"entity"`
		} `json:"queries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode schema failed: %v", err)
	}
	if len(payload.Queries) != 1 {
		t.Fatalf("expected one query in schema, got %#v", payload.Queries)
	}
	if payload.Queries[0].Name != "open_todos" || payload.Queries[0].Path != "/queries/open-todos" || payload.Queries[0].Entity != "Todo" {
		t.Fatalf("unexpected query payload: %#v", payload.Queries[0])
	}
}

func TestLispyQueryEndpointAcceptsParameters(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "query-params.db"), `
(define post
  (entity
    (fields
      ((body string)
       (author int)))
    (authorize
      (((read create update delete) true)))))

(define (posts-by-author wanted-author)
  (query post
    (where (= author wanted-author))
    (order-by body asc)))

(define-app demo
  (backend
    (entities post)
    (queries posts-by-author)))
`)
	defer r.Close()

	for _, body := range []string{
		`{"body":"one","author":1}`,
		`{"body":"two","author":2}`,
	} {
		createRec := doRuntimeRequest(r, http.MethodPost, "/posts", body, "")
		if createRec.Code != http.StatusCreated {
			t.Fatalf("expected 201 when creating post, got %d body=%s", createRec.Code, createRec.Body.String())
		}
	}

	queryRec := doRuntimeRequest(r, http.MethodGet, "/queries/posts-by-author?wanted_author=2", "", "")
	if queryRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from parameterized query endpoint, got %d body=%s", queryRec.Code, queryRec.Body.String())
	}

	var rows []map[string]any
	if err := json.Unmarshal(queryRec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode query response failed: %v body=%s", err, queryRec.Body.String())
	}
	if len(rows) != 1 || rows[0]["body"] != "two" {
		t.Fatalf("expected one post by author 2, got %#v", rows)
	}
}

func TestLispyQueryEndpointValidatesParametersWithInferredTypes(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "query-param-types.db"), `
(define post
  (entity
    (fields
      ((body string)
       (author int)))
    (authorize
      (((read create update delete) true)))))

(define (posts-by-author wanted-author)
  (query post
    (where (= author wanted-author))))

(define-app demo
  (backend
    (entities post)
    (queries posts-by-author)))
`)
	defer r.Close()

	rec := doRuntimeRequest(r, http.MethodGet, "/queries/posts-by-author?wanted_author=abc", "", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid query parameter, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "wanted_author must be Int") {
		t.Fatalf("expected typed query parameter error, got body=%s", rec.Body.String())
	}
}

func TestQueryPredicateRequiresBoolAtRuntime(t *testing.T) {
	r := &Runtime{
		enumLiteralValues: map[string]any{},
		functions:         map[string]expr.UserFunction{},
	}
	entity := &model.Entity{
		Name: "Todo",
		Fields: []model.Field{
			{Name: "title", Type: "String"},
		},
	}
	query := &model.Query{
		Name:   "bad_where",
		Entity: "Todo",
		Where:  "title",
	}

	matches, err := r.queryPredicate(query, entity)
	if err != nil {
		t.Fatalf("queryPredicate returned error: %v", err)
	}
	_, err = matches(authSession{}, map[string]any{"title": "ship it"}, nil)
	if err == nil {
		t.Fatal("expected query where type error")
	}
	apiErr, ok := err.(*apiError)
	if !ok {
		t.Fatalf("expected apiError, got %T: %v", err, err)
	}
	if apiErr.Status != http.StatusInternalServerError || apiErr.Code != "query_misconfigured" {
		t.Fatalf("unexpected api error: %+v", apiErr)
	}
}
