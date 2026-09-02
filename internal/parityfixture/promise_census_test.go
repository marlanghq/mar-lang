package parityfixture

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Scattered through the three runtimes are comments that promise two
// implementations behave the same: "Mirrors the Go runtime", "Keep these
// messages identical". A comment is a promise, not a guarantee. Nothing makes
// one true, nothing notices when one stops being true, and the reader has no
// way to tell a promise that is checked from one that is merely stated.
//
// This is the census of them. It does not verify the promises — the
// comparators named below do that. What it does is make the SET enumerable:
// add a new promise and this fails until you say which comparator covers it,
// or record it as a gap. The gap count is in the table too, so a gap is a
// number somebody can watch instead of a thing nobody knows about.
//
// The comparators:
//
//	fixture   the program in this package, evaluated by all three runtimes
//	          (internal/jsserve decode_shape_parity_test.go for the browser,
//	          internal/iosbundle swift_core_test.go for the phone)
//	screens   the web/iOS screen harness, internal/parity
//	sound     the playback comparator, internal/conformance
//	generated all three sides are rendered from one source, so they cannot
//	          disagree; internal/mathgen and its staleness test
//	gap       no comparator; the reason is in the note
type promiseSource struct {
	path       string
	promises   int
	comparator string
	note       string
}

var promiseCensus = []promiseSource{
	{"jsserve/runtime.js", 11, "fixture", "8 of the 11 are values in Source and 1 (the math kernels) is generated; the domain-code boundary and RegisterEnumType need a server, and are gaps inside this file"},
	{"runtime/admin.go", 1, "gap", "admin payload shapes have no second implementation to disagree with — the phone does not render the admin pages"},
	{"runtime/device.go", 1, "gap", "touchOnly / canHover read the host's matchMedia; there is no host in either harness"},
	{"runtime/path.go", 1, "fixture", "`link` encodes a space and a slash, which is where the three answers used to differ"},
	{"iosbundle/MarBuiltins.swift", 4, "fixture", "builtin tables: durations, listMove, charEdges, seeded"},
	{"iosbundle/MarValue.swift", 2, "fixture", "decimalScale and bigInt are the two representations that used to drift"},
	{"iosbundle/MarPath.swift", 2, "fixture", "the phone's half of `link` and `enumPath`"},
	{"iosbundle/MarIntRange.swift", 1, "fixture", "`bigInt` and `decimalEdge` ride the bound; the refusal's wording is pinned in internal/runtime"},
	{"iosbundle/MarJSONCodec.swift", 1, "fixture", "collections: Set and Dict round-trip and keep their order"},
	{"iosbundle/MarEval.swift", 1, "fixture", "`no case branch matched`, matched by text on both clients"},
	{"iosbundle/MarHTTP.swift", 3, "gap", "the network boundary: no harness makes a real request from the phone"},
	{"iosbundle/MarRenderer.swift", 2, "screens", "the renderer is compared by the screens it produces"},
	{"iosbundle/MarPageRuntime.swift", 4, "screens", "page lifecycle, compared by the navigation fixture"},
	{"iosbundle/MarDispatcher.swift", 1, "screens", "dispatch is observable only as a screen that changed"},
	{"iosbundle/MarSound.swift", 2, "sound", "the playback comparator reads the automation events"},
}

// The wordings actually in use. "Mirrors internal/..." is here because a
// promise that names the other file by PATH is the same promise as one that
// names it by runtime, and the first pass of this regex missed every one.
var promiseRe = regexp.MustCompile(`(?i)(keep (these|them|it)[^.]*(identical|in sync)|must match the |mirrors (the (go|js|swift|javascript)|internal/))`)

func TestEveryCrossRuntimePromiseIsAccountedFor(t *testing.T) {
	files := map[string]string{}
	add := func(label, path string) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		files[label] = string(b)
	}
	add("jsserve/runtime.js", "../jsserve/runtime.js")
	for _, p := range []string{"admin.go", "device.go", "path.go"} {
		add("runtime/"+p, filepath.Join("../runtime", p))
	}
	swift, err := filepath.Glob("../iosbundle/template/Sources/*.swift")
	if err != nil || len(swift) == 0 {
		t.Fatalf("no Swift sources: %v", err)
	}
	for _, p := range swift {
		add("iosbundle/"+filepath.Base(p), p)
	}

	counted := map[string]int{}
	for label, body := range files {
		for _, line := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "--") {
				continue
			}
			if promiseRe.MatchString(trimmed) {
				counted[label]++
			}
		}
	}

	expected := map[string]promiseSource{}
	for _, p := range promiseCensus {
		expected[p.path] = p
	}

	var problems []string
	for label, n := range counted {
		want, ok := expected[label]
		if !ok {
			problems = append(problems, fmt.Sprintf(
				"%s makes %d cross-runtime promise(s) and is not in the census.\n"+
					"    Add it, and say which comparator makes the promise true.", label, n))
			continue
		}
		if want.promises != n {
			problems = append(problems, fmt.Sprintf(
				"%s: census says %d promise(s), the file makes %d.\n"+
					"    A new promise needs a comparator; a removed one needs the count lowered.",
				label, want.promises, n))
		}
	}
	for label, want := range expected {
		if _, ok := counted[label]; !ok {
			problems = append(problems, fmt.Sprintf(
				"%s is in the census with %d promise(s) but the file makes none.\n"+
					"    Lower the count, or drop the row.", label, want.promises))
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}

	byComparator := map[string]int{}
	for _, p := range promiseCensus {
		byComparator[p.comparator] += p.promises
	}
	total := 0
	for _, n := range byComparator {
		total += n
	}
	if !t.Failed() {
		t.Logf("%d cross-runtime promises: %d compared by the fixture, %d by screens, %d by sound, %d with no comparator",
			total, byComparator["fixture"], byComparator["screens"], byComparator["sound"], byComparator["gap"])
	}
}

// The fixture is only worth anything if both clients actually read it. Either
// test could quietly go back to a private copy of the source, and the other
// would keep passing — which is the failure this package exists to prevent.
func TestBothClientsCompareAgainstThisFixture(t *testing.T) {
	for _, f := range []string{
		"../jsserve/decode_shape_parity_test.go",
		"../iosbundle/swift_core_test.go",
	} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		body := string(b)
		for _, need := range []string{"parityfixture.Source", "parityfixture.Names"} {
			if !strings.Contains(body, need) {
				t.Errorf("%s does not use %s: it is comparing its own copy, and the two clients can drift apart",
					filepath.Base(f), need)
			}
		}
	}
}
