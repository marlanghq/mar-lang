package admin

import (
	"strings"
	"testing"
)

// TestSanitizeForLog_Patterns — pin each redaction pattern. Cases
// are paired: each "before" goes through SanitizeForLog and the
// result must (a) not contain the secret, and (b) contain
// `<omitted>` somewhere.
func TestSanitizeForLog_Patterns(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		secret string // value we don't want surviving
	}{
		// Bearer tokens
		{"bearer lowercase", "Authorization: bearer abc.def.ghi", "abc.def.ghi"},
		{"Bearer capitalized", "Authorization: Bearer s3cr3t-tok3n", "s3cr3t-tok3n"},

		// Sensitive query params
		{"?token=", "/api/x?token=deadbeef", "deadbeef"},
		{"&token= midstring", "/api/x?foo=1&token=zzz&bar=2", "zzz"},
		{"?api_key=", "/api/x?api_key=AKIAdeadbeef", "AKIAdeadbeef"},
		{"?api-key= dashed", "/api/x?api-key=xyz", "xyz"},
		{"?access_token=", "/api/x?access_token=ya29.foo", "ya29.foo"},
		{"?password=", "/login?password=hunter2", "hunter2"},
		{"?secret=", "/api/x?secret=topsecret", "topsecret"},

		// `code: ...` / `code=...` in plain text
		{"plain code colon", "your code: 123456", "123456"},
		{"plain code equals", "code=987654 was wrong", "987654"},

		// JSON fields
		{"json token", `body: {"token":"abc.def"}`, "abc.def"},
		{"json code", `body: {"code":"123456"}`, "123456"},
		{"json email", `body: {"email":"alice@example.com"}`, "alice@example.com"},

		// Stray email anywhere
		{"email in path", "/api/users/alice@example.com/profile", "alice@example.com"},
		{"email in error", "failed to load bob.smith+tag@corp.io", "bob.smith+tag@corp.io"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := SanitizeForLog(tc.in)
			if strings.Contains(out, tc.secret) {
				t.Errorf("secret %q leaked: in=%q out=%q", tc.secret, tc.in, out)
			}
			if !strings.Contains(out, "<omitted>") {
				t.Errorf("no <omitted> marker: in=%q out=%q", tc.in, out)
			}
		})
	}
}

// TestSanitizeForLog_PreservesContext — sanitization replaces only
// the sensitive value, leaving the surrounding key visible so the
// log is still useful for debugging "what kind of thing was here".
func TestSanitizeForLog_PreservesContext(t *testing.T) {
	cases := []struct {
		in       string
		wantSubs []string // substrings that MUST still be present after sanitization
	}{
		{"?token=xyz", []string{"?token=", "<omitted>"}},
		{"Authorization: Bearer abc", []string{"Authorization:", "Bearer", "<omitted>"}},
		{`{"code":"123"}`, []string{`"code":`, "<omitted>"}},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			out := SanitizeForLog(tc.in)
			for _, sub := range tc.wantSubs {
				if !strings.Contains(out, sub) {
					t.Errorf("missing %q in sanitized output %q (in=%q)", sub, out, tc.in)
				}
			}
		})
	}
}

// TestSanitizeForLog_NoOpOnBenign — strings without anything
// sensitive pass through unchanged. Confirms we're not over-
// scrubbing routine paths and error messages.
func TestSanitizeForLog_NoOpOnBenign(t *testing.T) {
	cases := []string{
		"",
		"/",
		"/api/posts",
		"/api/posts/123",
		"db connection refused",
		"not found",
	}
	for _, tc := range cases {
		if got := SanitizeForLog(tc); got != tc {
			t.Errorf("benign input mutated: in=%q out=%q", tc, got)
		}
	}
}

// TestRequestLogger_SanitizesPathOnRecord — integration test. A
// request log entry recorded with a sensitive Path (email in URL,
// token in query) must come out of Snapshot with the secret
// redacted. The Method/Status/UserEmail/etc. fields stay intact.
func TestRequestLogger_SanitizesPathOnRecord(t *testing.T) {
	l := NewRequestLogger(10)
	l.Record(RequestLog{
		Method:    "GET",
		Path:      "/api/users/alice@example.com/profile?token=deadbeef",
		Status:    200,
		UserEmail: "admin@op.com",
	})
	snap := l.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("want 1 entry, got %d", len(snap))
	}
	got := snap[0]

	// Secret bits gone
	if strings.Contains(got.Path, "alice@example.com") {
		t.Errorf("email leaked in path: %q", got.Path)
	}
	if strings.Contains(got.Path, "deadbeef") {
		t.Errorf("token leaked in path: %q", got.Path)
	}
	if !strings.Contains(got.Path, "<omitted>") {
		t.Errorf("no <omitted> marker in path: %q", got.Path)
	}

	// Non-sensitive fields pass through
	if got.Method != "GET" {
		t.Errorf("method changed: %q", got.Method)
	}
	if got.Status != 200 {
		t.Errorf("status changed: %d", got.Status)
	}
	// UserEmail is intentionally preserved — admins need to see
	// who made the request. SanitizeForLog must not touch it.
	if got.UserEmail != "admin@op.com" {
		t.Errorf("UserEmail should be preserved verbatim, got %q", got.UserEmail)
	}
}
