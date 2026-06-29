package jsserve

import "testing"

// TestRequestLog_SkipsFrameworkInternalPaths — the panel polling itself
// shouldn't crowd out the user's actual app traffic in the recent-
// requests view. /_mar/admin/* and /_mar/reload don't get recorded;
// everything else does.
//
// (The introspection endpoints these used to feed — /api/server-info,
// /api/db-stats, … — were the hand-written SPA's; the Mar-native panel
// reads /_mar/admin/api/mar/* instead. Both live under /_mar/admin/ so
// the prefix classifier covers them either way.)
func TestRequestLog_SkipsFrameworkInternalPaths(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/", false},
		{"/api/users", false},
		{"/_auth/whoami", false},
		{"/_mar/admin", true},
		{"/_mar/admin/", true},
		{"/_mar/admin/program.json", true},
		{"/_mar/admin/api/mar/server-info", true},
		{"/_mar/admin/auth/request-code", true},
		{"/_mar/admin/api/database-backup/2026-01-01-000000", true},
		{"/_mar/reload", true},
	}
	for _, tc := range cases {
		got := isFrameworkInternalPath(tc.path)
		if got != tc.want {
			t.Errorf("path=%q: got %v, want %v", tc.path, got, tc.want)
		}
	}
}
