package project

import (
	"reflect"
	"testing"
)

func TestEnvRefsFromBytes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "no refs returns nil",
			raw:  `{"name": "x"}`,
			want: nil,
		},
		{
			name: "single ref",
			raw:  `{"auth": {"sessionSecret": "env:SESSION"}}`,
			want: []string{"SESSION"},
		},
		{
			name: "multiple refs sorted",
			raw: `{
				"auth": {"sessionSecret": "env:SESSION"},
				"mail": {"smtpPassword": "env:RESEND_API_KEY"}
			}`,
			want: []string{"RESEND_API_KEY", "SESSION"},
		},
		{
			name: "duplicate ref deduped",
			raw: `{
				"a": "env:SHARED",
				"b": "env:SHARED"
			}`,
			want: []string{"SHARED"},
		},
		{
			name: "lowercase rejected (not a valid POSIX env var name)",
			raw:  `{"x": "env:lower_case"}`,
			want: nil,
		},
		{
			name: "starts with digit rejected",
			raw:  `{"x": "env:9STARTS_WITH_DIGIT"}`,
			want: nil,
		},
		{
			name: "underscore prefix accepted",
			raw:  `{"x": "env:_INTERNAL"}`,
			want: []string{"_INTERNAL"},
		},
		{
			name: "embedded in larger string is skipped",
			// "env:" inside a value that isn't quoted as a top-
			// level reference shouldn't match. The regex requires
			// `"env:NAME"` with quotes on both sides.
			raw:  `{"prose": "see env:FOO for details"}`,
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EnvRefsFromBytes([]byte(tc.raw))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSessionSecretEnvVar — extracts the env var name from
// auth.sessionSecret when it's an env:VAR reference. Used by
// `mar fly provision` to know which secret to auto-generate
// (instead of prompting the operator for a value they wouldn't
// know how to generate correctly).
func TestSessionSecretEnvVar(t *testing.T) {
	cases := []struct {
		name string
		m    *Manifest
		want string
	}{
		{"nil manifest", nil, ""},
		{"no auth block", &Manifest{}, ""},
		{"no sessionSecret", &Manifest{Auth: &AuthConfig{}}, ""},
		{"literal secret (dev mode)", &Manifest{Auth: &AuthConfig{SessionSecret: "literal-secret-12345"}}, ""},
		{"env ref", &Manifest{Auth: &AuthConfig{SessionSecret: "env:SESSION_SECRET"}}, "SESSION_SECRET"},
		{"env ref with custom name", &Manifest{Auth: &AuthConfig{SessionSecret: "env:MY_APP_SESSION_KEY"}}, "MY_APP_SESSION_KEY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SessionSecretEnvVar(tc.m)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
