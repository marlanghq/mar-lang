package runtime

import (
	"errors"
	"testing"
)

// Effect.fail's value must reach the client as-is, not wrapped in the Go-side
// "effect error: " prefix that err.Error() adds. The frontend turns this body
// into a ServerError, and the operator's snake_case codes (e.g. "not_found")
// must survive intact so frontend matching works.
func TestServerErrorResponseStripsEffectErrorPrefix(t *testing.T) {
	resp := serverErrorResponse(effectError{value: VString{V: "not_found"}})
	rec, ok := resp.(VRecord)
	if !ok {
		t.Fatalf("serverErrorResponse should return a VRecord, got %T", resp)
	}
	body, ok := rec.Fields["body"].(VString)
	if !ok {
		t.Fatalf("response has no string body field: %+v", rec.Fields)
	}
	if body.V != "not_found" {
		t.Fatalf("body = %q, want %q (raw code, no 'effect error:' prefix)", body.V, "not_found")
	}
}

// A non-effectError (a real internal failure) must be sanitized, never leaked
// to the client.
func TestServerErrorResponseSanitizesInternalError(t *testing.T) {
	resp := serverErrorResponse(errors.New("table users: syntax error near SELECT"))
	body := resp.(VRecord).Fields["body"].(VString)
	if body.V != "internal server error" {
		t.Fatalf("internal error must be sanitized; body = %q", body.V)
	}
}

// serviceErrorString folds the Service.Error union to its display string. The
// JS (serviceErrorToStringJS) and Swift runtimes carry byte-identical copies
// of these messages, so a change here is a cross-runtime drift to mirror.
func TestServiceErrorString(t *testing.T) {
	cases := []struct {
		name string
		v    Value
		want string
	}{
		{"Offline", VCtor{Tag: "Offline"}, "Can't reach the server. Check your connection and try again."},
		{"Unauthorized", VCtor{Tag: "Unauthorized"}, "Your session has expired. Please sign in again."},
		{"ServerError carries the server message", VCtor{Tag: "ServerError", Args: []Value{VString{V: "boom_code"}}}, "boom_code"},
	}
	for _, c := range cases {
		if got := serviceErrorString(c.v); got != c.want {
			t.Errorf("%s: serviceErrorString = %q, want %q", c.name, got, c.want)
		}
	}
}
