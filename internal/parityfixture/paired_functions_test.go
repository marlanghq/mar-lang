package parityfixture

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The structural axis that found `plural` diverging in silence: a function name
// implemented in two or more runtimes is a pair that has to agree, whether or
// not any comment says so. An UNCLAIMED pair is worse than a claimed one --
// nobody even wrote down that it mattered -- and the lexical promise census
// cannot see it.
//
// I ran this scan by hand three times today, which by this repository's own
// doctrine makes it a memory rather than a check. It is a check now.
//
// What it does NOT do is demand a justification per pair. Sixty-nine rows of
// bookkeeping would rot into noise, which is the failure mode the deferral-
// comment census was rejected for. It pins the COUNT: a new pair fails the
// test and asks what covers it, which is the moment the question is cheap to
// answer.
func TestEveryFunctionImplementedTwiceIsAccountedFor(t *testing.T) {
	// Two numbers, because one is not enough. Measured: 106 names carry a
	// second implementation, and 253 implementations exist across them.
	//
	// The calendar of a Time is written out on all three runtimes, because
	// no host library is proleptic Gregorian everywhere: daysFromCivil,
	// civilFromDays, daysInMonth, isLeapYear, inTimeRange, timeOf,
	// timeOutOfRange, timeFromYMD, timeAddDays, timeAddMonths and
	// parseTimeISO on all three, timeFields and formatTimeISO on the server
	// and the phone (the browser reads Date's UTC getters and toISOString,
	// which agree in the range). What compares them: the Time, JSON and Decode
	// blocks of the corpus on all three, parityfixture.RangeCases run to
	// completion or refusal on all three, and the server's calendar against
	// Go's time package in internal/runtime timerange_test.go.
	//
	// A Date is a day of the same calendar, written out on all three too:
	// dateOf, dateOutOfRange, dateFromYMD, dateAddMonths, formatDateISO and
	// parseDateISO on all three, and twoDates (the argument check of
	// Date.before, Date.after and Date.diffDays) on the server and the phone.
	// The Date, JSON, Decode and Equality blocks of the corpus and
	// parityfixture.RangeCases compare them. `localDay` (the two clients) is
	// the day Date.today reads from the device's clock; the datepicker tests
	// of internal/jsserve and internal/iosbundle hold each to TodayIn, worked
	// out from Go's time package, in nine time zones.
	//
	// `durationOutOfRange` (all three) is the sentence a Duration that does not
	// fit raises; internal/runtime int_producers_test.go holds the three
	// sources to the same words, and the corpus's Decode block pins the one a
	// decoder can return. `numberLiteralOf` (the two clients) reads a JSON
	// number's own text, for the `__duration` payload; the Decode block's
	// duration cases compare it with the server's json.Number.
	//
	// `dictFromPairs` and `setFromItems` are trios. The phone rebuilds a
	// Dict or a Set with them where the wire shape converts its keys (a tuple
	// key arrives as a JSON array), and its Dict.fromList and Set.fromList
	// build with them, so the conformance corpus, duplicate keys included,
	// compares all three. The wire round trip (parityfixture.WireCases)
	// compares them on the way in.
	//
	// The newest six are a Decimal's two bounds (ADR 0039), written once per
	// runtime: errDecimalOverflow, errDecimalPlaces and errScaleArgument (the
	// three sentences), decimalFromWireNumber (a JSON number's text, now on all
	// three), and placesToDrop and decimalFromDigits (the rule itself, on the
	// server and the phone; the browser's are decPlacesToDrop and
	// decFromDigits). What compares them is parityfixture.DecimalBoundCases,
	// one program per case on each runtime, which checks the value returned or
	// the sentence raised, and `numPlaces` in Source for the JSON number. Two
	// pairs went: plainDigits (both clients now read the exponent the server's
	// way) and digitCount (the bound no longer counts digits that way).
	//
	// The newest three arrived with the browser's standard library catching up
	// with the other two runtimes. `applyMany` (browser and phone) feeds a
	// stdlib callback all its arguments at once; `dictFromPairs` and
	// `setFromItems` (browser and server) build a Dict or a Set by sorting once
	// instead of inserting one item at a time, keeping the LAST pair for a key
	// and the FIRST of equal items, as insertion did. What compares them is the
	// conformance corpus, which runs every fold, indexedMap, sortWith and
	// Dict/Set.fromList case (duplicate keys included) on all three runtimes.
	// The browser's Random also lost `scramble` and `seedState` (it now steps
	// the 64-bit state as two 32-bit halves); the server and the phone still
	// pair them, so the names stay and two implementations go.
	//
	// `evalApp` came just before them: the browser applies a call with several
	// arguments in one step the way the server already did (the call happens
	// where the nested one-argument applications would make it, with no partial
	// application built in between). What compares the two is behaviour, not a
	// table: every multi-argument call in the corpus and the conformance
	// programs goes through both.
	//
	// The last five arrived together and are compared by `numNotation` and
	// `numRefusals` in Source: liftNumberLiterals, numberFromLiteral,
	// parseJSONExact, plainDigits (gone since) and rawNumberOf -- the two clients' shared
	// answer to reading a JSON number without letting a float64 near it.
	//
	// `simpleCase` is the newest, and it is a pair rather than a trio on
	// purpose: the server has no such function because Go's unicode package
	// already answers with the simple mapping, so only the two clients need
	// one (ADR 0038 clause 7). What compares them is not a value in Source but
	// `simple_case_test.go`, which rebuilds both tables from Go's Unicode data
	// and diffs the two source files against it, plus `Text.charUpperSharpS`
	// and `Text.charLowerDotted` in the corpus for the behaviour.
	//
	// Counting names alone misses a function gaining a THIRD implementation:
	// adding decToScale to the browser, where the phone and the server already
	// had it, left the name count unmoved and the test silent. Measured, not
	// reasoned -- the sabotage was supposed to fail and did not.
	const (
		pairs           = 106
		implementations = 253
	)

	js := namesIn(t, []string{"../jsserve/runtime.js"},
		`function ([a-z][A-Za-z0-9]{4,})\s*\(`, `const ([a-z][A-Za-z0-9]{4,})\s*=\s*\(`)
	swift := namesIn(t, globOf(t, "../iosbundle/template/Sources/*.swift"),
		`func ([a-z][A-Za-z0-9]{4,})\s*[(<]`)
	server := namesIn(t, goSourcesIn(t, "../runtime"),
		`(?m)^func ([a-z][A-Za-z0-9]{4,})\(`)

	for who, set := range map[string]map[string]bool{"browser": js, "phone": swift, "server": server} {
		if len(set) < 40 {
			t.Fatalf("only %d functions found in the %s runtime; the scan is wrong, not the code",
				len(set), who)
		}
	}

	shared := map[string]int{}
	for _, set := range []map[string]bool{js, swift, server} {
		for name := range set {
			shared[name]++
		}
	}
	for name, n := range shared {
		if n < 2 {
			delete(shared, name)
		}
	}
	total := 0
	for _, n := range shared {
		total += n
	}

	var names []string
	for n := range shared {
		names = append(names, n)
	}
	sort.Strings(names)

	if len(names) != pairs {
		t.Errorf("%d function names are implemented in more than one runtime, and this test "+
			"expects %d.\n"+
			"    Up: a new pair. Say what compares the two -- a value in Source, a shared\n"+
			"    table, or a behavioural test -- and raise the number.\n"+
			"    Down: a pair went away. Lower it.\n\n"+
			"    The list:\n      %s", len(names), pairs, strings.Join(names, "\n      "))
	}
	if total != implementations {
		t.Errorf("those names carry %d implementations between them, and this test expects %d.\n"+
			"    The name count can sit still while a pair becomes a TRIPLE, which is a\n"+
			"    third copy of something two runtimes already had to keep in step.",
			total, implementations)
	}
	if !t.Failed() {
		t.Logf("%d function names carry a second implementation somewhere, %d implementations in all",
			len(names), total)
	}
}

func namesIn(t *testing.T, paths []string, patterns ...string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, pat := range patterns {
			for _, m := range regexp.MustCompile(pat).FindAllStringSubmatch(string(b), -1) {
				out[m[1]] = true
			}
		}
	}
	return out
}

func globOf(t *testing.T, pattern string) []string {
	t.Helper()
	files, err := filepath.Glob(pattern)
	if err != nil || len(files) == 0 {
		t.Fatalf("no files for %s: %v", pattern, err)
	}
	return files
}

func goSourcesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	if len(out) == 0 {
		t.Fatalf("no Go sources in %s", dir)
	}
	return out
}
