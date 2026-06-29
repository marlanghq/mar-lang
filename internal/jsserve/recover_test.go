package jsserve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A panic in any handler must be contained: the client gets a clean 500 with
// no implementation detail, and the stack is logged server-side (to stderr,
// visible in the test output) rather than dropped or leaked.
func TestRecoverPanicReturns500WithoutLeak(t *testing.T) {
	h := recoverPanic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom: secret internal detail")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/anything", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "boom") || strings.Contains(body, "secret") {
		t.Fatalf("panic detail leaked to client: %q", body)
	}
}

// The middleware is a transparent pass-through when the handler does not panic.
func TestRecoverPanicPassesThrough(t *testing.T) {
	h := recoverPanic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("ok"))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTeapot)
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("body = %q, want %q", rec.Body.String(), "ok")
	}
}
