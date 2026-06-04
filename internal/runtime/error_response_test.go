package runtime

import (
	"fmt"
	"strings"
	"testing"
)

// An internal failure (a type mismatch, an encode error, a wrapped SQL error)
// must never reach the client: the body is generic and the detail is logged
// server-side only.
func TestServerErrorResponseSanitizesInternalDetail(t *testing.T) {
	resp, ok := serverErrorResponse(fmt.Errorf(`Repo.create: near "user": syntax error`)).(VRecord)
	if !ok {
		t.Fatalf("serverErrorResponse did not return a Response record")
	}
	if got := statusOf(t, resp); got != 500 {
		t.Fatalf("status = %d, want 500", got)
	}
	body := bodyOf(t, resp)
	if body != "internal server error" {
		t.Fatalf("body = %q, want a generic message", body)
	}
	if strings.Contains(body, "syntax error") || strings.Contains(body, "Repo.create") {
		t.Fatalf("internal detail leaked to client body: %q", body)
	}
}

// A user-authored Effect.fail value is intentional, app-level error text (the
// `String` error channel of `Effect String resp`) and must still reach the
// client unchanged.
func TestServerErrorResponsePreservesUserFailure(t *testing.T) {
	resp, ok := serverErrorResponse(effectError{value: VString{V: "order not found"}}).(VRecord)
	if !ok {
		t.Fatalf("serverErrorResponse did not return a Response record")
	}
	if !strings.Contains(bodyOf(t, resp), "order not found") {
		t.Fatalf("user failure message was not preserved: %q", bodyOf(t, resp))
	}
}
