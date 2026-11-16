package parityfixture

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"mar/internal/runtime"
)

// The three runtimes stop runaway recursion at three different depths, and that
// is right: a Go goroutine stack is not a V8 stack is not a phone's main
// thread. Both clients say so where the number is written, and the Swift one
// puts it plainly -- "a program that recurses 5000 deep does not work on a
// phone today either. This turns the crash into a message."
//
// What nothing said is the DIRECTION. The clients are the tighter budget on
// purpose, so a program that runs on a phone runs on the server; the asymmetry
// is only honest that way round. The server's number is DERIVED -- it reads the
// process stack ceiling and divides by the worst measured frame -- so it moves
// when someone raises worstCaseFrameBytes or the ceiling shrinks, and nothing
// noticed if it slipped under the clients'.
//
// Measured when this was written: server 11904, browser 512, phone 512.
//
// What this catches, having tried each: raising a client above the server
// (fails), and either constant being renamed so the scan stops seeing it
// (fails). What it does NOT catch on its own is the server's derivation
// shrinking, because minCallDepth clamps it at 1000 and both clients sit under
// that -- the invariant is held by the clamp there, not by this test. Lower
// minCallDepth and this fires; that is the honest description of its reach.
func TestTheServerAllowsAtLeastAsMuchRecursionAsTheClients(t *testing.T) {
	js := constantIn(t, "../jsserve/runtime.js",
		`MAX_CALL_DEPTH = ([0-9_]+)`)
	swift := constantIn(t, "../iosbundle/template/Sources/MarEval.swift",
		`maxCallDepth = ([0-9_]+)`)

	server := runtime.MaxCallDepth
	if server < js || server < swift {
		t.Errorf("the server stops recursion at %d, the browser at %d and the phone at %d.\n"+
			"    The clients are meant to be the tighter budget, so that a program which runs\n"+
			"    on a phone runs on the server. This way round, a program works on the phone\n"+
			"    and dies on the server, and the reasoning written next to each number is\n"+
			"    backwards.", server, js, swift)
	}

	// A floor on the clients too: below this an app breaks on honest recursion,
	// and the message stops being a guard and becomes an obstacle.
	const clientFloor = 256
	if js < clientFloor || swift < clientFloor {
		t.Errorf("browser %d, phone %d: below %d an ordinary recursive function stops working",
			js, swift, clientFloor)
	}

	if !t.Failed() {
		t.Logf("recursion stops at %d on the server, %d in the browser, %d on the phone",
			server, js, swift)
	}
}

func constantIn(t *testing.T, path, pattern string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	m := regexp.MustCompile(pattern).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatalf("%s: no match for %s; the constant moved and this test stopped looking",
			path, pattern)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("%s: %q is not a number", path, m[1])
	}
	return n
}

// The other number three runtimes have to share, and this one by EQUALITY
// rather than by direction. decimal.go states the stake itself: "The 34-digit
// bound is what keeps the three runtimes bit-identical". A money calculation
// that overflows at 34 digits on the server and at some other width in the
// browser does not fail differently -- it SUCCEEDS differently, and the two
// answers are both plausible.
//
// The promise census did not cover it: that pass looks for a comment saying one
// implementation mirrors another, and this one states a shared constraint
// instead. Same kind of claim, different words, so it needed its own check.
//
// The SENTENCES are identical in all three too. They are raised, not
// returned, so they cannot ride in Source; DecimalBoundCases compares them as
// raised, one program per case, and this checks the words are there at all.
func TestTheThreeRuntimesBoundDecimalsAtTheSameWidth(t *testing.T) {
	widths := map[string]int{
		"server":  constantIn(t, "../runtime/decimal.go", `maxDecimalDigits = ([0-9_]+)`),
		"browser": constantIn(t, "../jsserve/runtime.js", `MAX_DEC_DIGITS = ([0-9_]+)`),
		"phone":   constantIn(t, "../iosbundle/template/Sources/MarDecimal.swift", `maxDigits = ([0-9_]+)`),
	}
	first := widths["server"]
	for who, n := range widths {
		if n != first {
			t.Errorf("%s bounds a Decimal at %d digits and the server at %d.\n"+
				"    decimal.go says this bound is what keeps the three bit-identical, so a\n"+
				"    money calculation would not fail differently -- it would SUCCEED\n"+
				"    differently, with two plausible answers.", who, n, first)
		}
	}

	// The second bound, the places (ADR 0039). shared_constants_test.go
	// already compares the three constants by their common name; this pins
	// that each runtime HAS one, so a rename cannot drop it out of that scan.
	places := map[string]int{
		"server":  constantIn(t, "../runtime/decimal.go", `maxDecimalPlaces = ([0-9_]+)`),
		"browser": constantIn(t, "../jsserve/runtime.js", `MAX_DECIMAL_PLACES = ([0-9_]+)`),
		"phone":   constantIn(t, "../iosbundle/template/Sources/MarDecimal.swift", `maxDecimalPlaces = ([0-9_]+)`),
	}
	for who, n := range places {
		if n != places["server"] {
			t.Errorf("%s bounds a Decimal at %d places and the server at %d", who, n, places["server"])
		}
	}

	// The sentences are raised, so the behaviour is compared by
	// parityfixture.DecimalBoundCases, one program per case; this is the
	// cheaper check that the words are there at all.
	for _, sentence := range []string{
		": Decimal overflow — the result exceeds ",
		": Decimal overflow — the result needs more than ",
		" decimal places a Decimal can hold",
	} {
		for _, f := range []struct{ label, path string }{
			{"server", "../runtime/decimal.go"},
			{"browser", "../jsserve/runtime.js"},
			{"phone", "../iosbundle/template/Sources/MarDecimal.swift"},
		} {
			b, err := os.ReadFile(f.path)
			if err != nil {
				t.Fatalf("%s: %v", f.label, err)
			}
			if !strings.Contains(string(b), sentence) {
				t.Errorf("the %s runtime no longer says %q at a Decimal's bounds",
					f.label, sentence)
			}
		}
	}

	if !t.Failed() {
		t.Logf("all three bound a Decimal at %d significant digits and %d places, and word the bounds the same", first, places["server"])
	}
}
