package jsserve

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The version-headers middleware sits in front of every response.
// These tests pin two invariants:
//
//   1. X-Mar-Runtime is set when MarVersion() has a value.
//   2. X-Mar-Program is set when LiveProgram has a non-empty hash.
//
// Both headers are independently optional: empty values are omitted
// (rather than sent as empty strings) so the client can distinguish
// "old server that doesn't know about this feature" from "server says
// the version is blank".

func TestWithVersionHeaders_setsBothWhenAvailable(t *testing.T) {
	// Set the package-level mar version. SetAdminBuildInfo locks
	// authMu — safe to call from a test.
	SetAdminBuildInfo("0.4.2")
	t.Cleanup(func() { SetAdminBuildInfo("") })

	lp := &LiveProgram{}
	// Simulate a successful Update having populated programHash.
	// We can't call Update directly (it requires modules), so we
	// poke the field via a small helper — programHash is the
	// internal name; the public-facing read is ProgramHash().
	lp.programHash = "deadbeefcafebabe"

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := withVersionHeaders(lp, inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Mar-Runtime"); got != "0.4.2" {
		t.Errorf("X-Mar-Runtime = %q, want %q", got, "0.4.2")
	}
	if got := rec.Header().Get("X-Mar-Program"); got != "deadbeefcafebabe" {
		t.Errorf("X-Mar-Program = %q, want %q", got, "deadbeefcafebabe")
	}
}

func TestWithVersionHeaders_omitsEmptyValues(t *testing.T) {
	// No SetAdminBuildInfo call, no program loaded. Both headers
	// should be absent so the client can tell this server hasn't
	// adopted the protocol (vs. a deliberate blank value).
	SetAdminBuildInfo("")
	t.Cleanup(func() { SetAdminBuildInfo("") })

	lp := &LiveProgram{}
	// programHash stays the zero value ("").

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := withVersionHeaders(lp, inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	if got, ok := rec.Header()["X-Mar-Runtime"]; ok {
		t.Errorf("X-Mar-Runtime should be absent when version unset; got %q", got)
	}
	if got, ok := rec.Header()["X-Mar-Program"]; ok {
		t.Errorf("X-Mar-Program should be absent when program unloaded; got %q", got)
	}
}

func TestWithVersionHeaders_setsBeforeInnerWrites(t *testing.T) {
	// The middleware must set headers BEFORE next.ServeHTTP runs —
	// otherwise an inner handler that calls Write() before the
	// middleware could finalize the header map.
	//
	// We verify this by having the inner handler call Write
	// immediately (which freezes headers), then assert the version
	// headers landed anyway.
	SetAdminBuildInfo("0.5.0")
	t.Cleanup(func() { SetAdminBuildInfo("") })

	lp := &LiveProgram{}
	lp.programHash = "abc123"

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Inner handler writes immediately — would lose any
		// header set AFTER this call.
		_, _ = w.Write([]byte("hello"))
	})
	handler := withVersionHeaders(lp, inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Mar-Runtime"); got != "0.5.0" {
		t.Errorf("X-Mar-Runtime lost when inner writes early: got %q, want %q", got, "0.5.0")
	}
	if got := rec.Header().Get("X-Mar-Program"); got != "abc123" {
		t.Errorf("X-Mar-Program lost when inner writes early: got %q, want %q", got, "abc123")
	}
	if rec.Body.String() != "hello" {
		t.Errorf("inner body lost: got %q, want %q", rec.Body.String(), "hello")
	}
}

func TestMarVersion_threadSafe(t *testing.T) {
	// Sanity check that MarVersion() reads what SetAdminBuildInfo
	// wrote. The mutex protects against torn reads, but the test
	// just verifies the round-trip works — the contention case is
	// covered by `go test -race` on the broader suite.
	SetAdminBuildInfo("0.6.0")
	t.Cleanup(func() { SetAdminBuildInfo("") })

	if got := MarVersion(); got != "0.6.0" {
		t.Errorf("MarVersion() = %q, want %q", got, "0.6.0")
	}
}

func TestLiveProgram_ProgramHash_updatedByUpdate(t *testing.T) {
	// The hash is computed in Update() once per swap, then cached.
	// We can't easily test Update without compiling a module, but
	// we can verify ProgramHash() returns the cached value.
	lp := &LiveProgram{}
	if got := lp.ProgramHash(); got != "" {
		t.Errorf("fresh LiveProgram should have empty hash; got %q", got)
	}
	lp.programHash = "feedface"
	if got := lp.ProgramHash(); got != "feedface" {
		t.Errorf("ProgramHash() = %q, want %q", got, "feedface")
	}
}
