package main

import "testing"

// TestDiscoverEnvRefs covers the textual scan that builds the
// envRefs list in metadata.json. The restore flow uses this to
// tell the operator which Fly secrets to re-set, so accuracy
// matters.
func TestDiscoverEnvRefs(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "no refs",
			raw:  `{"name":"x"}`,
			want: []string{},
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
			name: "literal env: in value (no closing quote on ref) is skipped gracefully",
			raw:  `{"x": "env:`,
			want: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := discoverEnvRefs([]byte(tc.raw))
			if !equalStringSlices(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
