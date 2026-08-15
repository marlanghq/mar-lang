package runtime

import (
	"strings"
	"testing"
)

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
	v, err := assembleServiceInput(svc, "/things", "", `{"name":"hi","locale":"en"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := strField(t, v, "name"); got != "hi" {
		t.Fatalf("name = %q, want hi", got)
	}
}

func TestAssemblePutPathAndBodyMerge(t *testing.T) {
	svc := VService{Verb: "PUT", Path: "/things/{id:Int}"}
	v, err := assembleServiceInput(svc, "/things/7", "", `{"name":"y","locale":"en"}`)
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

// A path parameter is the only part of a request the SERVER matched on:
// routing chose this handler because the URL had this shape. A caller that
// names it again in the payload is contradicting the URL, and the request is
// refused rather than silently resolved (ADR 0032).
//
// The tests above this one — TestAssemblePutPathAndBodyMerge and its sibling
// in internal/jsserve — use payload keys that do NOT collide with the path
// param, so they prove the merge HAPPENS without pinning who wins. That gap
// is what let `PUT /things/1` with `{"id":2}` act on thing 2 while every log
// recorded thing 1.
func TestAssembleRejectsPathParamRepeatedInPayload(t *testing.T) {
	cases := []struct {
		name       string
		verb       string
		path       string
		reqPath    string
		query      string
		body       string
		wantReject bool
		mentions   string
	}{
		{
			name: "PUT body contradicts the URL", verb: "PUT",
			path: "/things/{id:Int}", reqPath: "/things/1",
			body: `{"id":2,"name":"y"}`, wantReject: true, mentions: "request body",
		},
		{
			// Same value is still a contradiction in shape, and refusing it
			// keeps the rule statable in one line: a path parameter appears
			// in the URL and nowhere else.
			name: "PUT body repeats the URL's own value", verb: "PUT",
			path: "/things/{id:Int}", reqPath: "/things/1",
			body: `{"id":1,"name":"y"}`, wantReject: true, mentions: "request body",
		},
		{
			name: "GET query contradicts the URL", verb: "GET",
			path: "/things/{id:Int}", reqPath: "/things/1",
			query: `q=%7B%22id%22%3A2%7D`, wantReject: true, mentions: "query string",
		},
		{
			// CONTROL. Without a case that must be ACCEPTED, a fix that
			// rejected everything would pass the three above.
			name: "no collision is untouched", verb: "PUT",
			path: "/things/{id:Int}", reqPath: "/things/7",
			body: `{"name":"y","locale":"en"}`, wantReject: false,
		},
		{
			// CONTROL. A field merely SHARING a name with nothing in the
			// path must still pass on a path that declares no params.
			name: "no path params at all", verb: "POST",
			path: "/things", reqPath: "/things",
			body: `{"id":2,"name":"y"}`, wantReject: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := VService{Verb: c.verb, Path: c.path}
			v, err := assembleServiceInput(svc, c.reqPath, c.query, c.body)
			if !c.wantReject {
				if err != nil {
					t.Fatalf("rejected a request with no collision: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("accepted a payload that renames the path parameter; got %v", v)
			}
			if !strings.Contains(err.Error(), "path parameter") {
				t.Errorf("error should say what the problem is, got: %v", err)
			}
			if c.mentions != "" && !strings.Contains(err.Error(), c.mentions) {
				t.Errorf("error should name the half to edit (%q), got: %v", c.mentions, err)
			}
		})
	}
}
