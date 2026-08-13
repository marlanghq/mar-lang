package runtime

import (
	"math/big"
	"strings"
	"testing"

	"mar/internal/parser"
	"mar/internal/typecheck"
)

// Conformance vectors for the Decimal model (docs/proposals/decimal.md).
// The same vectors are exercised against the JS runtime in the E2E
// harness, if a value here changes, the runtimes have drifted.

func TestDecimalLiteralDisplay(t *testing.T) {
	cases := map[string]string{
		"1.5":    "1.5",
		"1.50":   "1.50", // scale is remembered, 1.50 stays 1.50
		"0.05":   "0.05",
		"-0.05":  "-0.05",
		"-12.50": "-12.50",
		"3.0":    "3.0",
		"0.000":  "0.000",
	}
	for src, want := range cases {
		if got := runValue(t, src); got != want {
			t.Errorf("%s: got %s, want %s", src, got, want)
		}
	}
}

func TestDecimalExactArithmetic(t *testing.T) {
	cases := map[string]string{
		"0.1 + 0.2":          "0.3", // the reason Decimal exists
		"1.10 + 2.20":        "3.30",
		"0.5 - 0.25":         "0.25", // result carries the larger scale
		"1.5 * 1.5":          "2.25", // scales add under *
		"0.10 * 0.10":        "0.0100",
		"2.5 * 4.0":          "10.00",
		"1.5 + -0.5":         "1.0",
		"-(1.5)":             "-1.5",
		"Decimal.abs (-2.5)": "2.5",
	}
	for src, want := range cases {
		if got := runValue(t, src); got != want {
			t.Errorf("%s: got %s, want %s", src, got, want)
		}
	}
}

func TestDecimalEqualityIsNumeric(t *testing.T) {
	cases := map[string]string{
		"1.50 == 1.5":  "True", // scale is display metadata, not identity
		"1.50 == 1.51": "False",
		"1.5 < 1.55":   "True",
		"2.50 <= 2.5":  "True",
		"-0.1 > -0.2":  "True",
	}
	for src, want := range cases {
		if got := runValue(t, src); got != want {
			t.Errorf("%s: got %s, want %s", src, got, want)
		}
	}
}

func TestIntegerDivisionSlashSlash(t *testing.T) {
	cases := map[string]string{
		"7 // 2":   "3",
		"-7 // 2":  "-3", // truncation toward zero
		"7 // -2":  "-3",
		"-7 // -2": "3",
		"7 // 0":   "0", // total, matches the other runtimes
	}
	for src, want := range cases {
		if got := runValue(t, src); got != want {
			t.Errorf("%s: got %s, want %s", src, got, want)
		}
	}
}

func TestDecimalRoundedResolver(t *testing.T) {
	cases := map[string]string{
		// The flagship pipeline: name the precision or it doesn't exist.
		"Decimal.rounded Decimal.HalfEven 4 (1.0 / 3.0)":  "0.3333",
		"Decimal.rounded Decimal.HalfEven 2 (2.0 / 3.0)":  "0.67",
		"1.0 / 3.0 |> Decimal.rounded Decimal.HalfEven 4": "0.3333",

		// Ties: HalfEven goes to the even neighbour, HalfUp away from zero.
		"Decimal.rounded Decimal.HalfEven 0 (5.0 / 2.0)":  "2",
		"Decimal.rounded Decimal.HalfEven 0 (7.0 / 2.0)":  "4",
		"Decimal.rounded Decimal.HalfUp 0 (5.0 / 2.0)":    "3",
		"Decimal.rounded Decimal.HalfUp 0 (-5.0 / 2.0)":   "-3",
		"Decimal.rounded Decimal.HalfEven 0 (-5.0 / 2.0)": "-2",

		// Directional modes on a negative quotient (-3.5).
		"Decimal.rounded Decimal.Down 0 (-7.0 / 2.0)":    "-3",
		"Decimal.rounded Decimal.Up 0 (-7.0 / 2.0)":      "-4",
		"Decimal.rounded Decimal.Floor 0 (-7.0 / 2.0)":   "-4",
		"Decimal.rounded Decimal.Ceiling 0 (-7.0 / 2.0)": "-3",

		// Exact quotients pass through unrounded at the asked scale.
		"Decimal.rounded Decimal.HalfEven 2 (1.0 / 4.0)": "0.25",
		"Decimal.rounded Decimal.HalfEven 2 (9.0 / 3.0)": "3.00",

		// Mixed scales in the operands.
		"Decimal.rounded Decimal.HalfEven 2 (10.00 / 4.0)": "2.50",
		"Decimal.rounded Decimal.HalfEven 3 (0.1 / 0.3)":   "0.333",

		// Division by zero: total, zero at the asked scale (matches //).
		"Decimal.rounded Decimal.HalfEven 2 (5.0 / 0.0)": "0.00",
	}
	for src, want := range cases {
		if got := runValue(t, src); got != want {
			t.Errorf("%s: got %s, want %s", src, got, want)
		}
	}
}

func TestDecimalWithRemainderResolver(t *testing.T) {
	// The lossless decomposition: quotient * divisor + remainder == dividend.
	// The remainder's scale derives from the exact ops that computed it
	// (a - q*b, so max(aScale, asked + bScale)): same convention as
	// Java's BigDecimal.divideAndRemainder. Divide by a whole-number
	// Decimal (scale 0) and the remainder lands at the asked scale.
	cases := map[string]string{
		// Splitting R$ 100 three ways: 33.33 each, 0.01 left over.
		"Decimal.withRemainder 2 (100.00 / Decimal.fromInt 3)": "{ quotient = 33.33, remainder = 0.01 }",
		"Decimal.withRemainder 2 (100.0 / 3.0)":                "{ quotient = 33.33, remainder = 0.010 }",
		"Decimal.withRemainder 2 (10.0 / 4.0)":                 "{ quotient = 2.50, remainder = 0.000 }",
		"Decimal.withRemainder 0 (7.0 / 2.0)":                  "{ quotient = 3, remainder = 1.0 }",
		// Negative dividend: quotient truncates toward zero, remainder
		// keeps the dividend's sign: the invariant still holds.
		"Decimal.withRemainder 2 (-100.00 / Decimal.fromInt 3)": "{ quotient = -33.33, remainder = -0.01 }",
		// Zero divisor: q = 0, r = dividend (0 * 0 + a == a).
		"Decimal.withRemainder 2 (5.0 / 0.0)": "{ quotient = 0.00, remainder = 5.000 }",
	}
	for src, want := range cases {
		if got := runValue(t, src); got != want {
			t.Errorf("%s: got %s, want %s", src, got, want)
		}
	}
}

func TestDecimalConversions(t *testing.T) {
	cases := map[string]string{
		"Decimal.fromInt 3":                        "3",
		"Decimal.fromInt 3 + 0.5":                  "3.5",
		"Decimal.fromCents 1234":                   "12.34",
		"Decimal.toCents 12.34":                    "1234",
		"Decimal.toCents 12.3":                     "1230",
		"Decimal.truncate 2.9":                     "2",
		"Decimal.truncate (-2.9)":                  "-2",
		"Decimal.round 2.5":                        "2", // HalfEven
		"Decimal.round 3.5":                        "4",
		"Decimal.floor (-2.1)":                     "-3",
		"Decimal.ceiling (-2.1)":                   "-2",
		"Decimal.toIntWith Decimal.Up 2.1":         "3",
		"Decimal.toScale Decimal.HalfEven 2 1.005": "1.00", // 100|5 → even stays
		"Decimal.toScale Decimal.HalfEven 2 1.015": "1.02",
		"Decimal.toScale Decimal.HalfEven 4 1.5":   "1.5000",
		"Decimal.toString 1.50":                    `"1.50"`,
		"Decimal.fromString \"1.50\"":              "Just 1.50",
		"Decimal.fromString \"abc\"":               "Nothing",
		"Decimal.compare 1.5 1.50":                 "EQ",
		"Decimal.compare 1.5 2.0":                  "LT",
		"Decimal.zero":                             "0",
	}
	for src, want := range cases {
		if got := runValue(t, src); got != want {
			t.Errorf("%s: got %s, want %s", src, got, want)
		}
	}
}

func TestDecimalToCentsRejectsFinerScale(t *testing.T) {
	full := "module M exposing (..)\nx = Decimal.toCents 1.005\n"
	mod, err := parser.Parse(full)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("typecheck: %v", err)
	}
	_, err = LoadModule(mod)
	if err == nil {
		t.Fatal("Decimal.toCents 1.005 should error (scale 3), got success")
	}
	if !strings.Contains(err.Error(), "round explicitly") {
		t.Fatalf("error should point at explicit rounding, got: %v", err)
	}
}

func TestDecimalOverflowErrors(t *testing.T) {
	// 20 digits * 20 digits = ~40 digits > 34: multiplication must
	// refuse rather than silently truncate.
	full := "module M exposing (..)\nx = 12345678901234567890.0 * 12345678901234567890.0\n"
	mod, err := parser.Parse(full)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("typecheck: %v", err)
	}
	_, err = LoadModule(mod)
	if err == nil {
		t.Fatal("34-digit overflow should error, got success")
	}
	if !strings.Contains(err.Error(), "overflow") {
		t.Fatalf("expected overflow error, got: %v", err)
	}
}

func TestDecimalJSONRoundTrip(t *testing.T) {
	d := VDecimal{Coef: big.NewInt(1250), Scale: 2} // 12.50
	enc, err := encodeValue(d)
	if err != nil {
		t.Fatal(err)
	}
	if enc != `{"__dec":"12.50"}` {
		t.Fatalf("encode: got %s", enc)
	}
	back, err := decodeJSON(enc)
	if err != nil {
		t.Fatal(err)
	}
	rd, ok := back.(VDecimal)
	if !ok {
		t.Fatalf("decode: got %T", back)
	}
	if decString(rd) != "12.50" {
		t.Fatalf("round-trip: got %s", decString(rd))
	}

	// A plain fractional JSON number decodes textually: exactly.
	v, err := decodeJSON(`{"price": 19.99}`)
	if err != nil {
		t.Fatal(err)
	}
	rec := v.(VRecord)
	price, ok := rec.Fields["price"].(VDecimal)
	if !ok {
		t.Fatalf("price: got %T, want VDecimal", rec.Fields["price"])
	}
	if decString(price) != "19.99" {
		t.Fatalf("price: got %s", decString(price))
	}

	// Division never crosses the wire.
	if _, err := encodeValue(VDivision{Num: d, Den: d}); err == nil {
		t.Fatal("encoding a Division should error")
	}
}
