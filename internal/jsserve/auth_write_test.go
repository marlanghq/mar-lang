package jsserve

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// The signed-in user is written by the Mar wire encoder (runtime.LoadUserJSON)
// and sent as those bytes, so /_auth/whoami and /_auth/verify-code spell a
// value exactly like a Service response does. Any other answer is ordinary
// JSON. Both say what they are and may not be sniffed as anything else.
func TestTheSignedInUserGoesOutAsTheWireEncoderWroteIt(t *testing.T) {
	user := json.RawMessage("{\"name\":\"</b> &   /\"}")
	rec := httptest.NewRecorder()
	writeJSON(rec, 200, user)
	if got, want := rec.Body.String(), string(user)+"\n"; got != want {
		t.Fatalf("body %q, want %q", got, want)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}

	rec = httptest.NewRecorder()
	writeJSON(rec, 200, map[string]bool{"ok": true})
	if got := rec.Body.String(); got != "{\"ok\":true}\n" {
		t.Fatalf("an ordinary answer: %q", got)
	}
	rec = httptest.NewRecorder()
	writeJSON(rec, 200, nil)
	if got := rec.Body.String(); got != "null\n" {
		t.Fatalf("a signed-out answer: %q", got)
	}
}
