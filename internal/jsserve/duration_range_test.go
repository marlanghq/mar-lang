package jsserve

import (
	"os/exec"
	"strings"
	"testing"
)

// A Duration is a whole number of milliseconds inside the range of Int, so a
// constructor or a Time.diff whose interval does not fit refuses, in the
// sentence the server and the phone use (internal/runtime durationOutOfRange).
// Run, not scanned: a source scan cannot tell a refusal from one that was
// switched off.
func TestTheBrowserRefusesADurationPastInt(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	const tail = " is outside the range of a Duration (-9007199254740991 to 9007199254740991 milliseconds)"
	for _, c := range []struct{ expr, want string }{
		{"String.fromInt (Time.toSeconds (Time.weeks 9007199254740991))",
			"THREW: Duration out of range: Time.weeks 9007199254740991" + tail},
		{"String.fromInt (Time.toSeconds (Time.seconds 9007199254741))",
			"THREW: Duration out of range: Time.seconds 9007199254741" + tail},
		{"String.fromInt (Time.toSeconds (Time.seconds 9007199254740))",
			"9007199254740"},
	} {
		got := encodeInBrowser(t, node, c.expr)
		if !strings.Contains(got, c.want) {
			t.Errorf("%s\n  want %q\n  got  %q", c.expr, c.want, got)
		}
	}
}
