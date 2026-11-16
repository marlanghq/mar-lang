package parityfixture

import (
	"strings"
	"time"
)

// DecimalBoundCases are what a Decimal does at its two bounds: at most 34
// significant digits and at most 34 places, zeros at the right end of the
// fraction counting against neither (ADR 0039). Each is one String value,
// `x = <Expr>`, and the three runtimes must answer it the same way: the value
// it returns, or the sentence it RAISES.
//
// They cannot ride in Source. A Mar error is not catchable by design, so a
// value that raises stops the module that holds it; here every case is its own
// one-value program. The sentences are the point: three hand-ported
// implementations of exact arithmetic each word their own refusal, and a
// source scan proves the words are present, not that they are reached.
var DecimalBoundCases = func() []struct{ Name, Expr, Says string } {
	z := strings.Repeat
	places34 := "0." + z("0", 33) + "1"
	eighteen := func(lit string) string { return "[ " + strings.TrimSuffix(z(lit+", ", 18), ", ") + " ]" }
	const placesErr = ": Decimal overflow — the result needs more than 34 decimal places"
	const digitsErr = ": Decimal overflow — the result exceeds 34 significant digits"
	const argErr = ": scale 35 is more than the 34 decimal places a Decimal can hold"
	fromString := func(text string) string {
		return `Maybe.withDefault "Nothing" (Maybe.map Decimal.toString (Decimal.fromString "` + text + `"))`
	}
	return []struct{ Name, Expr, Says string }{
		// Zeros at the right end go, only as many as needed.
		{"productDropsAZero", "Decimal.toString (0.5 * 0." + z("0", 33) + "2)", places34},
		{"productOfTenths", "Decimal.toString (List.product " + eighteen("0.10") + ")", "0." + z("0", 17) + "1" + z("0", 16)},
		{"sumDropsAZero", "Decimal.toString (0.5" + z("0", 33) + " + 9.0)", "9.5" + z("0", 32)},
		{"toScaleStopsAtTheDigits", "Decimal.toString (Decimal.toScale Decimal.HalfEven 34 12345.6)", "12345.6" + z("0", 28)},
		{"roundedStopsAtTheDigits", "Decimal.toString (Decimal.rounded Decimal.HalfEven 34 (12345.0 / 1.0))", "12345." + z("0", 29)},
		{"remainderFitsAfterAWideProduct", "Decimal.toString (Decimal.withRemainder 34 (1.0 / 0.5)).remainder", "0." + z("0", 34)},
		// The remainder is worked out with q at n places, the zeros the fit
		// took from a quotient over 34 digits put back: 1 / 1 at 34 is q = 1.
		// at 33 places, and the remainder still shows 34; 74691 / 11 at 31 is
		// q = 6790.09…09 at 30, and the remainder 1e-30 shows 31.
		{"remainderOfOneByOne", "Decimal.toString (Decimal.withRemainder 34 (1 / 1)).remainder", "0." + z("0", 34)},
		{"remainderKeepsItsPlaces", "Decimal.toString (Decimal.withRemainder 31 (74691 / 11)).remainder", "0." + z("0", 29) + "10"},
		{"remainderOfMoney", "Decimal.toString (Decimal.withRemainder 2 (10.00 / 3)).remainder", "0.01"},
		{"zeroKeepsAtMost34", fromString("-0." + z("0", 40)), "0." + z("0", 34)},
		{"fromStringDropsZeros", fromString("1." + z("0", 40)), "1." + z("0", 33)},
		{"fromStringAt34", fromString(places34), places34},
		{"zeroProductKeepsAtMost34", "Decimal.toString (0.0 * " + places34 + ")", "0." + z("0", 34)},

		// A digit that changes the value never goes.
		{"productNeeds35", "Decimal.toString (0.1 * " + places34 + ")", "*" + placesErr},
		{"productOfElevenths", "Decimal.toString (List.product " + eighteen("0.11") + ")", "*" + placesErr},
		{"sumNeeds35Digits", "Decimal.toString (" + places34 + " + " + z("9", 34) + ".0)", "+" + digitsErr},
		{"remainderNeeds35", "Decimal.toString (Decimal.withRemainder 34 (0.1 / 0.3)).remainder", "Decimal.withRemainder" + placesErr},
		{"quotientNeeds35Digits", "Decimal.toString (Decimal.withRemainder 34 (1.0 / 0.3)).quotient", "Decimal.rounded" + digitsErr},
		{"fromStringAt35", fromString("0." + z("0", 34) + "1"), "Nothing"},

		// More than 34 places is refused as a request.
		{"toScale35", "Decimal.toString (Decimal.toScale Decimal.HalfEven 35 1.5)", "Decimal.toScale" + argErr},
		{"rounded35", "Decimal.toString (Decimal.rounded Decimal.HalfEven 35 (1.0 / 3.0))", "Decimal.rounded" + argErr},
		{"rounded35ByZero", "Decimal.toString (Decimal.rounded Decimal.HalfEven 35 (1.0 / 0.0))", "Decimal.rounded" + argErr},
		{"withRemainder35", "Decimal.toString (Decimal.withRemainder 35 (1.0 / 3.0)).quotient", "Decimal.withRemainder" + argErr},
	}
}()

// DecimalLongTextCases are text a sender controls, long enough that reading it
// in more than linear time shows. Each runtime has to answer each one the way
// the others do, and within LongTextBudget (ADR 0039 promises text is fitted
// in time linear in its length). The text is built at run time with
// String.repeat, so the program stays small.
//
// The budget is far from both sides (measured 2026-11-13, Apple silicon). A
// linear read of these took at most 2.4 ms in Go and 6 ms in the browser, and
// on the phone, where every case starts its own process, 100 ms warm and
// 214 ms for the first, cold launch. The browser's first count of trailing
// zeros, /0+$/, restarted at every zero of a run that is not at the end, and
// took 30.5 s on each of the five cases whose zeros are followed by a digit.
// So the budget sits about 14 times above the slowest linear read and 10 times
// below the quadratic one.
var DecimalLongTextCases = func() []struct{ Name, Expr, Says string } {
	z := strings.Repeat
	zeros := `String.repeat 262144 "0"`
	fromText := func(text string) string {
		return `Maybe.withDefault "Nothing" (Maybe.map Decimal.toString (Decimal.fromString (` + text + `)))`
	}
	decode := func(doc string) string {
		return `Result.withDefault "refused" (Result.map (\r -> Decimal.toString r.v) (JSON.decode (` + doc + `)))`
	}
	return []struct{ Name, Expr, Says string }{
		{"fromStringInnerZeros", fromText(`"1" ++ ` + zeros + ` ++ "1"`), "Nothing"},
		{"fromStringInnerZerosAfterThePoint", fromText(`"1." ++ ` + zeros + ` ++ "1"`), "Nothing"},
		{"fromStringTrailingZeros", fromText(`"1." ++ ` + zeros), "1." + z("0", 33)},
		{"fromStringZeroWithManyPlaces", fromText(`"0." ++ ` + zeros), "0." + z("0", 34)},
		{"jsonNumberInnerZeros", decode(`"{\"v\":1" ++ ` + zeros + ` ++ "1}"`), "refused"},
		{"jsonNumberInnerZerosWithExponent", decode(`"{\"v\":1" ++ ` + zeros + ` ++ "1e0}"`), "refused"},
		{"jsonNumberTrailingZeros", decode(`"{\"v\":1." ++ ` + zeros + ` ++ "}"`), "1." + z("0", 33)},
		{"decInnerZeros", decode(`"{\"v\":{\"__dec\":\"1" ++ ` + zeros + ` ++ "1\"}}"`), "refused"},
	}
}()

// LongTextBudget is the time each runtime has for one DecimalLongTextCases
// case; see there for how far it sits from a linear read and a quadratic one.
const LongTextBudget = 3 * time.Second
