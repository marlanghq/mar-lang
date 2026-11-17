package jsserve

import (
	"os/exec"
	"strings"
	"testing"

	"mar/internal/parityfixture"
)

// The browser's half of parityfixture.RangeCases, run, not scanned.
func TestTheBrowserKeepsATimeAndADateInsideTheirRanges(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	for _, c := range parityfixture.RangeCases {
		got := encodeInBrowser(t, node, c.Expr)
		want := c.Says
		if c.Refused {
			want = "THREW: " + c.Says + c.Tail
		}
		if !strings.Contains(got, want) {
			t.Errorf("%s\n  want %q\n  got  %q", c.Expr, want, got)
		}
	}
}
