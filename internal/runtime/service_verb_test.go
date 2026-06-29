package runtime

import "testing"

// assembleServiceInput is the heart of the verb/path wire protocol: it
// rebuilds a handler's typed request from the URL path (typed params),
// the query string (GET/DELETE), and the body (POST/PUT/PATCH).

func intField(t *testing.T, v Value, name string) int64 {
	t.Helper()
	rec, ok := v.(VRecord)
	if !ok {
		t.Fatalf("expected record, got %T", v)
	}
	n, ok := rec.Fields[name].(VInt)
	if !ok {
		t.Fatalf("field %q: expected Int, got %T", name, rec.Fields[name])
	}
	return n.V
}

func strField(t *testing.T, v Value, name string) string {
	t.Helper()
	rec, ok := v.(VRecord)
	if !ok {
		t.Fatalf("expected record, got %T", v)
	}
	s, ok := rec.Fields[name].(VString)
	if !ok {
		t.Fatalf("field %q: expected String, got %T", name, rec.Fields[name])
	}
	return s.V
}

func TestAssembleGetPathParam(t *testing.T) {
	svc := VService{Verb: "GET", Path: "/things/{id:Int}"}
	v, err := assembleServiceInput(svc, "/things/42", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := intField(t, v, "id"); got != 42 {
		t.Fatalf("id = %d, want 42", got)
	}
}

func TestAssemblePostBody(t *testing.T) {
	svc := VService{Verb: "POST", Path: "/things"}
	v, err := assembleServiceInput(svc, "/things", "", `{"name":"hi"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := strField(t, v, "name"); got != "hi" {
		t.Fatalf("name = %q, want hi", got)
	}
}

func TestAssemblePutPathAndBodyMerge(t *testing.T) {
	svc := VService{Verb: "PUT", Path: "/things/{id:Int}"}
	v, err := assembleServiceInput(svc, "/things/7", "", `{"name":"y"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := intField(t, v, "id"); got != 7 {
		t.Fatalf("id = %d, want 7", got)
	}
	if got := strField(t, v, "name"); got != "y" {
		t.Fatalf("name = %q, want y", got)
	}
}

func TestAssembleGetQueryBlob(t *testing.T) {
	svc := VService{Verb: "GET", Path: "/search"}
	// q = {"term":"hello"} url-encoded
	v, err := assembleServiceInput(svc, "/search", "q=%7B%22term%22%3A%22hello%22%7D", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := strField(t, v, "term"); got != "hello" {
		t.Fatalf("term = %q, want hello", got)
	}
}

func TestAssembleUnitRequest(t *testing.T) {
	// A GET with no path params and no query is a () request.
	svc := VService{Verb: "GET", Path: "/things"}
	v, err := assembleServiceInput(svc, "/things", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.(VUnit); !ok {
		t.Fatalf("expected Unit, got %T", v)
	}
	// A POST with a null body and no path params is also ().
	svc2 := VService{Verb: "POST", Path: "/reset"}
	v2, err := assembleServiceInput(svc2, "/reset", "", "null")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v2.(VUnit); !ok {
		t.Fatalf("expected Unit for null body, got %T", v2)
	}
}

func TestAssembleDeletePathParam(t *testing.T) {
	svc := VService{Verb: "DELETE", Path: "/things/{id:Int}"}
	v, err := assembleServiceInput(svc, "/things/9", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := intField(t, v, "id"); got != 9 {
		t.Fatalf("id = %d, want 9", got)
	}
}
