package parityfixture

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// `go test ./...` exiting 0 does not mean the comparisons ran.
//
// Thirty-odd tests skip themselves when node or swiftc is absent, and a
// skipped test reports as `ok`. On a machine without node, every browser-side
// comparison in this repository -- the wire fixture, the auth port, the error
// wording, the router, the fake browser -- quietly does not run, and all
// twenty-four packages still say ok. Without swiftc the same is true of the
// phone. That is the fourth shape of the same failure this suite has: a check
// that is green because it stopped looking.
//
// So the absence is a failure by default, and opting out is deliberate:
//
//	MAR_ALLOW_MISSING_RUNTIMES=1 go test ./...
//
// which is the right thing on a machine that genuinely has neither, and a
// thing somebody had to type.
func TestTheOtherTwoRuntimesAreActuallyPresent(t *testing.T) {
	dependants := countToolDependentTests(t)

	var absent []string
	for tool, n := range map[string]int{"node": dependants["node"], "swiftc": dependants["swiftc"]} {
		if _, err := exec.LookPath(tool); err != nil {
			absent = append(absent, tool+" ("+itoa(n)+" test(s) skip themselves without it)")
		}
	}
	if len(absent) == 0 {
		t.Logf("node and swiftc are both here; %d test(s) depend on node and %d on swiftc",
			dependants["node"], dependants["swiftc"])
		return
	}
	sort.Strings(absent)
	if os.Getenv("MAR_ALLOW_MISSING_RUNTIMES") != "" {
		t.Skipf("missing %s, and MAR_ALLOW_MISSING_RUNTIMES is set", strings.Join(absent, " and "))
	}
	t.Fatalf("missing %s.\n\n"+
		"Every comparison against that runtime skipped itself, and the suite still\n"+
		"passed: a skipped test reports as ok. Install it, or run with\n"+
		"MAR_ALLOW_MISSING_RUNTIMES=1 to say you meant to compare nothing.",
		strings.Join(absent, " and "))
}

// The counts come from the tests themselves rather than a number written here,
// so adding a comparison does not quietly leave this sentence stale.
func countToolDependentTests(t *testing.T) map[string]int {
	t.Helper()
	skip := regexp.MustCompile(`t\.Skipf?\("([^"]*)(node|swiftc)([^"]*)"`)
	out := map[string]int{}
	files, err := filepath.Glob(filepath.Join("..", "*", "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 50 {
		t.Fatalf("only %d test files found; the glob is wrong, not the suite", len(files))
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range skip.FindAllStringSubmatch(string(b), -1) {
			out[m[2]]++
		}
	}
	if out["node"] == 0 || out["swiftc"] == 0 {
		t.Fatalf("found %d node-dependent and %d swiftc-dependent skips; the pattern is wrong",
			out["node"], out["swiftc"])
	}
	return out
}
