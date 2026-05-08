package main

import "testing"

// TestHumanBytes pins the formatting used in `mar fly database
// backup download`'s success line. We don't try to be exhaustive —
// just the boundary values plus a representative real DB size.
func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{4_096, "4.0 KB"},
		{1_048_576, "1.0 MB"},
		{4_300_000, "4.1 MB"},
		{1_073_741_824, "1.0 GB"},
	}
	for _, tc := range cases {
		got := humanBytes(tc.in)
		if got != tc.want {
			t.Errorf("humanBytes(%d): got %q, want %q", tc.in, got, tc.want)
		}
	}
}
