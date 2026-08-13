package main

import "testing"

// parentForSubcommand powers the "did you mean" hint when someone
// types a sub-subcommand (logs, status, secrets, ...) at the top
// level. It's a static map: no manifest lookup on this hot path.
// Pin the cases so a future refactor doesn't accidentally route them
// through disk reads or drop a sub.
func TestParentForSubcommand_staticSubs(t *testing.T) {
	cases := map[string]string{
		"preview":  "fly",
		"destroy":  "fly",
		"logs":     "fly",
		"status":   "fly",
		"secrets":  "fly",
		"backup":   "fly database",
		"backups":  "fly database",
		"database": "fly",
		"db":       "fly",
		"plan":     "migrate",
		"frobnitz": "",
	}
	for sub, want := range cases {
		if got := parentForSubcommand(sub); got != want {
			t.Errorf("parentForSubcommand(%q) = %q, want %q", sub, got, want)
		}
	}
}
