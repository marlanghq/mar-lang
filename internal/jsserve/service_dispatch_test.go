package jsserve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mar/internal/runtime"
)

// echoService builds a real service route (via the production
// ExposedServiceToRoute wrapper) whose handler echoes its assembled
// request back as the response. That lets the test assert exactly which
// typed request the verb/path machinery reconstructed from the wire.
func echoService(verb, path string) runtime.Value {
	handler := runtime.VFn{
		Arity: 1,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			in := args[0]
			return runtime.VEffect{Run: func() (runtime.Value, error) { return in, nil }}, nil
		},
	}
	es := runtime.VExposedService{Service: runtime.VService{Verb: verb, Path: path, Handler: handler}}
	return runtime.ExposedServiceToRoute(es)
}

// TestServiceDispatchVerbAndPathParams drives the full HTTP path —
// dispatchBackend routing + the ExposedServiceToRoute wrapper's typed
// request assembly — for each verb, including typed {id:Int} path params
// and path+body merge.
func TestServiceDispatchVerbAndPathParams(t *testing.T) {
	withMaxBodyBytes(t, 1<<20)

	routes := []runtime.Value{
		echoService("GET", "/things/{id:Int}"),
		echoService("POST", "/things"),
		echoService("PUT", "/things/{id:Int}"),
		echoService("DELETE", "/things/{id:Int}"),
	}

	// do issues a request through dispatchBackend and returns the
	// status + the space-stripped response body. status 0 means no
	// route matched.
	do := func(method, target, body string) (int, string) {
		var r *http.Request
		if body == "" {
			r = httptest.NewRequest(method, target, nil)
		} else {
			r = httptest.NewRequest(method, target, strings.NewReader(body))
		}
		w := httptest.NewRecorder()
		if !dispatchBackend(routes, r.URL.Path, w, r) {
			return 0, "<unmatched>"
		}
		return w.Code, strings.ReplaceAll(w.Body.String(), " ", "")
	}

	t.Run("GET with typed path param", func(t *testing.T) {
		code, body := do("GET", "/things/5", "")
		if code != 200 || !strings.Contains(body, `"id":5`) {
			t.Fatalf("GET /things/5 -> %d %s (want 200 with id:5 as a number)", code, body)
		}
	})

	t.Run("POST with JSON body", func(t *testing.T) {
		code, body := do("POST", "/things", `{"name":"hi"}`)
		if code != 200 || !strings.Contains(body, `"name":"hi"`) {
			t.Fatalf("POST /things -> %d %s", code, body)
		}
	})

	t.Run("PUT merges path param and body", func(t *testing.T) {
		code, body := do("PUT", "/things/7", `{"name":"y"}`)
		if code != 200 || !strings.Contains(body, `"id":7`) || !strings.Contains(body, `"name":"y"`) {
			t.Fatalf("PUT /things/7 -> %d %s (want id:7 + name:y)", code, body)
		}
	})

	t.Run("DELETE with typed path param", func(t *testing.T) {
		code, body := do("DELETE", "/things/9", "")
		if code != 200 || !strings.Contains(body, `"id":9`) {
			t.Fatalf("DELETE /things/9 -> %d %s", code, body)
		}
	})

	t.Run("verb mismatch does not match", func(t *testing.T) {
		// PATCH has no route; the GET/PUT/DELETE routes on the same
		// path pattern must not answer a PATCH.
		if _, body := do("PATCH", "/things/5", `{}`); body != "<unmatched>" {
			t.Fatalf("PATCH /things/5 should be unmatched, got %s", body)
		}
	})

	t.Run("unknown path does not match", func(t *testing.T) {
		if _, body := do("GET", "/nope", ""); body != "<unmatched>" {
			t.Fatalf("GET /nope should be unmatched, got %s", body)
		}
	})
}
