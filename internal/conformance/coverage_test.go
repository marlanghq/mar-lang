package conformance

import (
	"sort"
	"strings"
	"testing"

	"mar/internal/typecheck"
)

// The gate. A stdlib function nobody exercises is a function whose runtimes are
// free to disagree, so not testing one has to be a build failure rather than an
// omission somebody might notice.
func TestCorpusCoversEveryFunctionInScope(t *testing.T) {
	var missing []string
	for _, name := range typecheck.BaseEnv().Names() {
		i := strings.Index(name, ".")
		if i <= 0 {
			continue // bare operators and globals; not module surface
		}
		if !Scope[name[:i]] {
			continue
		}
		if _, excused := OutOfScopeFunc[name]; excused {
			continue
		}
		if !strings.Contains(Source, name) {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("%d stdlib functions are not exercised by the corpus, so the runtimes\n"+
			"are free to disagree about them. Add a case to Source for each, or move\n"+
			"its module to OutOfScope, or the function to OutOfScopeFunc,\n"+
			"with a reason:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// And the same gate again for the OPERATORS, which neither of the other two
// walks can see: the module walk skips dotless names, and BareGlobals holds
// named functions rather than symbols. So `<`, `>`, `<=`, `>=`, `==` and `/=`
// had three implementations and nothing comparing them — the same shape of
// blind spot that hid `modBy`, and it hid a real one: comparison returned 0
// for every tuple and list on the web and on iOS, which Dict and Set read as
// "equal" and acted on by merging distinct keys.
//
// Spelled out here rather than derived, because an operator is a token and
// the corpus is text: `>` appears inside `->` on nearly every line, so a
// substring search would pass for a symbol nobody had tested. Each one names
// the CASE that exercises it instead.
func TestCorpusCoversEveryOperator(t *testing.T) {
	operators := map[string]string{
		"<":  "Ordering.ltInt",
		">":  "Ordering.tupleGe",
		"<=": "Ordering.leEqualTuple",
		">=": "Ordering.tupleGe",
		"==": "Math.degEqDeci",
		"/=": "Ordering.neInt",
	}
	want, err := Expectations()
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for op, caseName := range operators {
		if _, ok := want[caseName]; !ok {
			missing = append(missing, op+" (its case "+caseName+" is gone from the corpus)")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("%d operators have lost the corpus case that exercised them, so the\n"+
			"runtimes are free to disagree about them again:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// The same gate for the names spelled WITHOUT a qualifier. The module walk
// above skips them by construction ("bare operators and globals; not module
// surface"), and that blind spot is where `modBy` lived: defined in all three
// runtimes, exercised in none, and wrong in one for as long as it took a game
// to leave a window minimised overnight.
func TestCorpusCoversEveryBareGlobal(t *testing.T) {
	var missing []string
	for name := range typecheck.BareGlobals() {
		if _, explained := OutOfScopeBare[name]; explained {
			continue
		}
		if !strings.Contains(Source, name) {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("%d bare globals are not exercised by the corpus, so the runtimes are\n"+
			"free to disagree about them. Add a case to Source for each, or explain it\n"+
			"in OutOfScopeBare:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}

// Every stdlib module is either compared here or explained. A module in
// neither list fails the build, which is what keeps a newly added one from
// going untested by simply not being noticed: the first run of this check
// found JSON, whose encoder every client and server share.
func TestEveryStdlibModuleIsClassified(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range typecheck.BaseEnv().Names() {
		if i := strings.Index(name, "."); i > 0 {
			seen[name[:i]] = true
		}
	}
	var unclassified []string
	for module := range seen {
		if Scope[module] {
			continue
		}
		if _, explained := OutOfScope[module]; !explained {
			unclassified = append(unclassified, module)
		}
	}
	sort.Strings(unclassified)
	if len(unclassified) > 0 {
		t.Errorf("%d stdlib modules are neither in Scope nor explained in OutOfScope:\n  %s\n"+
			"Decide whether their answers must be identical across runtimes. If yes, add\n"+
			"cases to Source and put them in Scope; if no, put them in OutOfScope with the\n"+
			"reason.", len(unclassified), strings.Join(unclassified, "\n  "))
	}

	// And the reverse: a module that stops existing should not leave a stale
	// excuse behind, which would read as coverage of something that is gone.
	var stale []string
	for module := range OutOfScope {
		if !seen[module] {
			stale = append(stale, module)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("OutOfScope explains %d modules the stdlib no longer has:\n  %s",
			len(stale), strings.Join(stale, "\n  "))
	}
}

// The expectations have to parse, and every one of them has to name a case the
// corpus can actually produce. Without this, a typo in a label would look like
// a passing test that quietly checks one thing less.
func TestExpectationsAreWellFormed(t *testing.T) {
	want, err := Expectations()
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("no expectations parsed")
	}
	for name := range want {
		i := strings.Index(name, ".")
		if i <= 0 {
			t.Errorf("expectation %q is not qualified by a block name", name)
			continue
		}
		block, label := name[:i], name[i+1:]
		found := false
		for _, b := range Blocks {
			if b == block {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expectation %q names block %q, which `results` does not join", name, block)
		}
		if !strings.Contains(Source, `"`+label+`="`) {
			t.Errorf("expectation %q has no matching case label in the corpus", name)
		}
	}
}

// An exemption that outlives its reason weakens the gate silently: the
// function is gone, or its module moved out of Scope, and the entry sits there
// looking like a decision somebody made about code that exists.
//
// The module-level table already has this check; the per-function one did not.
// It is the smaller table and the one whose entries are easiest to forget,
// because a function is renamed far more often than a module is.
func TestNoFunctionExemptionOutlivesItsFunction(t *testing.T) {
	inScope := map[string]bool{}
	for _, name := range typecheck.BaseEnv().Names() {
		i := strings.Index(name, ".")
		if i <= 0 {
			continue
		}
		if Scope[name[:i]] {
			inScope[name] = true
		}
	}

	var stale []string
	for name, why := range OutOfScopeFunc {
		if inScope[name] {
			continue
		}
		reason := "the stdlib no longer has it"
		if i := strings.Index(name, "."); i > 0 && !Scope[name[:i]] {
			reason = "its module `" + name[:i] + "` is no longer in Scope, so the whole module is excused already"
		}
		stale = append(stale, name+" — "+reason+" (excused as: "+why+")")
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("%d function exemption(s) no longer apply to anything:\n  %s\n\n"+
			"Remove them. An exemption nobody can reach reads as a decision about live code.",
			len(stale), strings.Join(stale, "\n  "))
	}
}

// The gate that keeps the Text block from quietly becoming ASCII again.
//
// Every function below answers a question about text whose answer depends on
// how the runtime represents a String: it measures, indexes, compares, or maps
// character by character. ASCII cannot tell three representations apart, so a
// case built from ASCII exercises the name and proves nothing about the
// answer.
//
// Spelled out rather than derived, for the same reason the operator gate is:
// nothing in a function's type says whether its answer depends on encoding.
// `String.fromInt` takes no text and cannot say; `String.padLeft` measures,
// and a measurement is where a representation shows through.
func TestTextBlockFeedsEveryEncodingSensitiveFunction(t *testing.T) {
	block := textBlock(t)
	if !strings.ContainsFunc(block, func(r rune) bool { return r > 127 }) {
		t.Fatal("the Text block contains no character outside ASCII, which is the one thing it exists to do")
	}

	sensitive := []string{
		// Measure, so they inherit "what is a Character".
		"String.length", "String.padLeft", "String.padRight", "String.foldl",
		// Yield or take Chars, so they say what a Character IS.
		"String.toList", "String.fromList", "String.cons",
		"String.map", "String.filter", "String.any",
		// Report a position, which is a measurement with an offset.
		"String.indexes",
		// Compare, so they inherit "when are two Strings the same".
		"String.contains", "String.startsWith", "String.endsWith",
		"String.split", "String.replace",
		// Map case, which Unicode allows to change the LENGTH of a string.
		"String.toUpper", "String.toLower", "Char.toUpper", "Char.toLower",
		// Classify a Char, and the classes are Unicode's, not ASCII's.
		"Char.isAlpha", "Char.isUpper", "Char.isLower", "Char.isDigit",
		// Cross the Char/Int boundary, where anything above the BMP is at risk.
		"Char.toCode", "Char.fromCode",
		// Order and identity: what Dict and Set are built out of.
		"List.sort", "Dict.get", "Dict.size", "Set.size",
	}
	var missing []string
	for _, name := range sensitive {
		if !strings.Contains(block, name) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d encoding-sensitive function(s) are exercised only over ASCII:\n  %s\n\n"+
			"Add a case to textCases that feeds each one text that is not ASCII, or take it off\n"+
			"this list with a reason. Its runtimes are free to disagree until then.",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// Two sources of truth for one case is how a pinned divergence outlives its
// fix: the expectation goes green and the pin nobody deleted keeps claiming
// the runtimes disagree.
func TestNoCaseIsBothExpectedAndPinned(t *testing.T) {
	want, err := Expectations()
	if err != nil {
		t.Fatal(err)
	}
	var both []string
	for name := range KnownTextGaps {
		if w, ok := want[name]; ok {
			both = append(both, name+" (expected "+w+")")
		}
	}
	if len(both) > 0 {
		sort.Strings(both)
		t.Errorf("%d case(s) carry a hand-written expectation AND a KnownTextGaps pin:\n  %s\n\n"+
			"Keep one. If the runtimes now agree, delete the pin; if they still differ, delete\n"+
			"the expectation.", len(both), strings.Join(both, "\n  "))
	}
}

// textBlock returns the source of textCases, so the gate above asks about the
// block that is supposed to be non-ASCII rather than about a Source that
// contains an accented comment somewhere else.
func textBlock(t *testing.T) string {
	t.Helper()
	const start, end = "textCases =", "\nlistCases : String"
	i := strings.Index(Source, start)
	if i < 0 {
		t.Fatal("the corpus has no textCases block; it was renamed or removed")
	}
	j := strings.Index(Source[i:], end)
	if j < 0 {
		t.Fatal("textCases is no longer followed by listCases; this slice needs a new end marker")
	}
	return Source[i : i+j]
}
