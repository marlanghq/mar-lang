package runtime

import (
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"mar/internal/parityfixture"
	"mar/internal/parser"
	"mar/internal/typecheck"
)

// A Decimal holds at most 34 significant digits and 34 places after the point,
// and zeros at the right end of the fraction count against neither (ADR 0039).
// These are the language-level vectors, on the server only;
// parityfixture.DecimalBoundCases is the part that runs on all three runtimes
// (TestTheServerAnswersTheDecimalBoundCases below, and its browser and phone
// halves in internal/jsserve and internal/iosbundle).

// decimalOutcome evaluates `x = <src>` and answers the value's display, or the
// error the evaluation raised.
func decimalOutcome(t *testing.T, src string) string {
	t.Helper()
	mod, err := parser.Parse("module M exposing (..)\nx = " + src + "\n")
	if err != nil {
		return "parse: " + err.Error()
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("%s: typecheck: %v", src, err)
	}
	loaded, err := LoadModule(mod)
	if err != nil {
		return err.Error()
	}
	v, err := loaded.Get("x")
	if err != nil {
		return err.Error()
	}
	return strings.Trim(v.Display(), `"`)
}

func TestADecimalHasAtMost34Places(t *testing.T) {
	z := strings.Repeat
	places34 := "0." + z("0", 33) + "1"
	eighteen := func(lit string) string { return "[ " + strings.TrimSuffix(z(lit+", ", 18), ", ") + " ]" }
	for _, c := range []struct{ src, want string }{
		// Nothing that fits changes: the scale a value carries is its display.
		{"Decimal.toString (0.10 * 0.10)", "0.0100"},
		{"Decimal.toString (1.50 + 2.25)", "3.75"},

		// Zeros at the right end do not count: they go, only as many as needed.
		{"Decimal.toString (0.5 * 0." + z("0", 33) + "2)", places34},
		{"Decimal.toString (List.product " + eighteen("0.10") + ")", "0." + z("0", 17) + "1" + z("0", 16)},
		{"Decimal.toString (0.5" + z("0", 33) + " + 9.0)", "9.5" + z("0", 32)},
		{"Decimal.toString (Decimal.toScale Decimal.HalfEven 34 12345.6)", "12345.6" + z("0", 28)},
		{"Decimal.toString (Decimal.rounded Decimal.HalfEven 34 (12345.0 / 1.0))", "12345." + z("0", 29)},

		// A digit that changes the value never goes: those are errors.
		{"Decimal.toString (0.1 * " + places34 + ")", "*: Decimal overflow — the result needs more than 34 decimal places"},
		{"Decimal.toString (List.product " + eighteen("0.11") + ")", "*: Decimal overflow — the result needs more than 34 decimal places"},
		{"Decimal.toString (" + places34 + " + " + z("9", 34) + ".0)", "+: Decimal overflow — the result exceeds 34 significant digits"},
		{"Decimal.toString (Decimal.withRemainder 34 (0.1 / 0.3)).remainder", "Decimal.withRemainder: Decimal overflow — the result needs more than 34 decimal places"},
		{"Decimal.toString (Decimal.withRemainder 34 (1.0 / 0.3)).remainder", "Decimal.rounded: Decimal overflow — the result exceeds 34 significant digits"},

		// A remainder that fits, even when q * b on the way carried more.
		{"Decimal.toString (Decimal.withRemainder 34 (1.0 / 0.5)).remainder", "0." + z("0", 34)},
		{"Decimal.toString (Decimal.withRemainder 2 (10.00 / 3)).remainder", "0.01"},

		// Asking for more than 34 places is refused as a request.
		{"Decimal.toString (Decimal.toScale Decimal.HalfEven 35 1.5)", "Decimal.toScale: scale 35 is more than the 34 decimal places a Decimal can hold"},
		{"Decimal.toString (Decimal.rounded Decimal.HalfEven 35 (1.0 / 3.0))", "Decimal.rounded: scale 35 is more than the 34 decimal places a Decimal can hold"},
		{"Decimal.toString (Decimal.withRemainder 35 (1.0 / 3.0)).quotient", "Decimal.withRemainder: scale 35 is more than the 34 decimal places a Decimal can hold"},
		{"Decimal.toString (Decimal.rounded Decimal.HalfEven 99 (1.0 / 0.0))", "Decimal.rounded: scale 99 is more than the 34 decimal places a Decimal can hold"},

		// Text: Decimal.fromString.
		{`Maybe.withDefault "Nothing" (Maybe.map Decimal.toString (Decimal.fromString "` + places34 + `"))`, places34},
		{`Maybe.withDefault "Nothing" (Maybe.map Decimal.toString (Decimal.fromString "0.` + z("0", 34) + `1"))`, "Nothing"},
		{`Maybe.withDefault "Nothing" (Maybe.map Decimal.toString (Decimal.fromString "1.` + z("0", 40) + `"))`, "1." + z("0", 33)},
		{`Maybe.withDefault "Nothing" (Maybe.map Decimal.toString (Decimal.fromString "-0.` + z("0", 40) + `"))`, "0." + z("0", 34)},

		// Literals: the same rule at compile time.
		{"Decimal.toString 0." + z("0", 40), "0." + z("0", 34)},
		{"Decimal.toString 1." + z("0", 34), "1." + z("0", 33)},
		{"Decimal.toString 0." + z("0", 34) + "1", "parse: decimal literal 0." + z("0", 34) + "1 has more than 34 decimal places (Decimal's maximum)"},
	} {
		got := decimalOutcome(t, c.src)
		if strings.HasPrefix(got, "parse: ") && strings.HasPrefix(c.want, "parse: ") {
			if !strings.Contains(got, strings.TrimPrefix(c.want, "parse: ")) {
				t.Errorf("%s:\n got %s\nwant %s", c.src, got, c.want)
			}
			continue
		}
		if got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.src, got, c.want)
		}
	}
}

// The parser bounds a literal at compile time and parseDecimalString bounds
// the same text at run time; they are two copies of one rule, so they are held
// to the same answer on every text here, both bounds and both sides of each.
func TestALiteralAndFromStringFitTheSameWay(t *testing.T) {
	z := strings.Repeat
	for _, lit := range []string{
		"1.5", "1.50", "0.000", "12345.678",
		"0." + z("0", 33) + "1", "0." + z("0", 34) + "1", "0." + z("0", 33) + "10", "0." + z("0", 34) + "10",
		"0." + z("0", 40), "1." + z("0", 34), "1." + z("0", 40), z("9", 34) + ".0", z("9", 35) + ".0",
		z("9", 33) + ".9", z("9", 34) + ".9", z("1", 20) + "." + z("1", 14), z("1", 20) + "." + z("1", 15),
		z("1", 20) + "." + z("1", 14) + z("0", 30),
	} {
		mod, perr := parser.Parse("module M exposing (..)\nx = " + lit + "\n")
		d, ferr := parseDecimalString(lit)
		if (perr == nil) != (ferr == nil) {
			t.Errorf("%s: the literal %s and fromString %s", lit, okOr(perr), okOr(ferr))
			continue
		}
		if perr != nil {
			continue
		}
		if _, err := typecheck.CheckModule(mod); err != nil {
			t.Fatalf("%s: %v", lit, err)
		}
		loaded, err := LoadModule(mod)
		if err != nil {
			t.Fatalf("%s: %v", lit, err)
		}
		v, _ := loaded.Get("x")
		if got, want := decString(v.(VDecimal)), decString(d); got != want {
			t.Errorf("%s: the literal is %s and fromString is %s", lit, got, want)
		}
	}
}

func okOr(err error) string {
	if err == nil {
		return "fits"
	}
	return "refused (" + err.Error() + ")"
}

// A scale in the millions used to arrive by the one form the JSON-number bound
// did not cover, Mar's own `{"__dec":…}`, and inside a `__set` every insertion
// paid 10^scale to compare: about 23 ms of CPU per six-byte item, before the
// 401 of a protected service. With the bound in the language it is refused
// while it is read, in time and memory proportional to the text.
func TestAHugeScaleIsRefusedWhereverItArrives(t *testing.T) {
	zeros := strings.Repeat("0", 1<<20)
	set := `{"__set":[[{"__dec":"0.` + zeros[:1<<19] + `1"}],[-0.1],[0.1]` + strings.Repeat(",[0.1]", 1000) + `]}`
	for _, c := range []struct{ name, doc string }{
		{"__dec", `{"__dec":"0.` + zeros + `1"}`},
		{"__set", set},
		{"JSON number", `{"v":0.` + zeros + `1}`},
	} {
		var before, after goruntime.MemStats
		goruntime.GC()
		goruntime.ReadMemStats(&before)
		_, err := decodeJSON(c.doc)
		goruntime.ReadMemStats(&after)
		if err == nil {
			t.Errorf("%s: a million places decoded", c.name)
		}
		if mb := (after.TotalAlloc - before.TotalAlloc) >> 20; mb > 64 {
			t.Errorf("%s: refusing a million places allocated %d MB", c.name, mb)
		}
	}
	if _, err := parseDecimalString("0." + zeros + "1"); err == nil {
		t.Error("Decimal.fromString: a million places parsed")
	}
	// Zero is the exception the rule makes on purpose: every one of its places
	// is a zero at the right end, so it fits, at 34.
	v, err := decodeJSON(`{"__dec":"0.` + zeros + `"}`)
	if err != nil {
		t.Fatalf("a zero with a million places: %v", err)
	}
	if d := v.(VDecimal); d.Scale != maxDecimalPlaces {
		t.Errorf("a zero with a million places came back at scale %d", d.Scale)
	}
}

// The server's half of parityfixture.DecimalBoundCases and
// DecimalLongTextCases; the browser's and the phone's run the same tables in
// internal/jsserve and internal/iosbundle.
func TestTheServerAnswersTheDecimalBoundCases(t *testing.T) {
	for _, c := range parityfixture.DecimalBoundCases {
		if got := decimalOutcome(t, c.Expr); got != c.Says {
			t.Errorf("%s:\n got %s\nwant %s", c.Name, got, c.Says)
		}
	}
	for _, c := range parityfixture.DecimalLongTextCases {
		start := time.Now()
		got := decimalOutcome(t, c.Expr)
		if took := time.Since(start); took > parityfixture.LongTextBudget {
			t.Errorf("%s took %v; text has to be read in time linear in its length", c.Name, took)
		}
		if got != c.Says {
			t.Errorf("%s:\n got %s\nwant %s", c.Name, got, c.Says)
		}
	}
}
