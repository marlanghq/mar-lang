package runtime

import (
	"fmt"
	goruntime "runtime"
	"strings"
	"testing"
	"time"
)

// A JSON number carries at most maxDecimalPlaces places into a Decimal, in
// either spelling, because no Decimal holds more (ADR 0039). decodeJSON is what
// a service's request body and JSON.decode both go through (service.go and
// json.go), so this is the boundary as a client on the network meets it.
//
// The reason is a cost, not a taste: `1e-99999999` is eleven bytes and used to
// arrive as a one-digit coefficient with 99,999,999 places, and printing it and
// adding 1 to it took 98 s and 1.96 GB, measured. Zeros at the right end of the
// fraction do not count, so a zero fits at any scale and `10e-35` is 1e-34. The
// same table runs on the other two runtimes as `numPlaces` in
// internal/parityfixture.
func TestAJSONNumberCarriesAtMost34DecimalPlaces(t *testing.T) {
	refused := func(lit string) string {
		return "JSON: " + lit + " is not a number Mar can hold exactly: an Int is at most 9007199254740991 and a Decimal at most 34 significant digits and 34 decimal places"
	}
	places34 := "0." + strings.Repeat("0", 33) + "1"
	places35 := "0." + strings.Repeat("0", 34) + "1"
	for _, c := range []struct{ lit, want string }{
		// The bound itself, from both sides, in both spellings.
		{"1e-34", places34},
		{"1e-35", refused("1e-35")},
		{places34, places34},
		{places35, refused(places35)},
		{"1.5e-33", "0." + strings.Repeat("0", 32) + "15"},
		{"15e-35", refused("15e-35")},
		// Zeros at the right end do not count: a zero fits at any scale, with
		// at most 34 places, and a trailing zero is dropped to fit.
		{"0e-34", "0." + strings.Repeat("0", 34)},
		{"-0e-34", "0." + strings.Repeat("0", 34)},
		{"0e-35", "0." + strings.Repeat("0", 34)},
		{"10e-35", places34},
		{"1.0e-34", places34},
		{"100e-36", places34},
		{"0." + strings.Repeat("0", 34) + "10", refused("0." + strings.Repeat("0", 34) + "10")},
		{"0." + strings.Repeat("0", 33) + "10", places34},
		// The numbers that were the problem.
		{"1e-99999999", refused("1e-99999999")},
		{"1e-999999999", refused("1e-999999999")},
		{"-1e-999999999", refused("-1e-999999999")},
		{"0e-999999999", "0." + strings.Repeat("0", 34)},
		{"0e-9223372036854775808", "0." + strings.Repeat("0", 34)},
		// math.MinInt64 as the exponent wrapped the subtraction around and
		// decoded as 1.
		{"1e-9223372036854775808", refused("1e-9223372036854775808")},
		// The other direction is unchanged: padding past 34 digits is refused,
		// zero needs no padding, an exponent outside 64 bits is not read.
		{"1e33", "1" + strings.Repeat("0", 33)},
		{"1e34", refused("1e34")},
		{"1e9223372036854775807", refused("1e9223372036854775807")},
		{"0e999999999", "0"},
		{"0e9223372036854775807", "0"},
		{"0e9223372036854775808", refused("0e9223372036854775808")},
		// A positive exponent can take places away from a long fraction.
		{"0." + strings.Repeat("0", 39) + "1e10", "0." + strings.Repeat("0", 29) + "1"},
		{"0.000e2", "0.0"},
	} {
		start := time.Now()
		v, err := decodeJSON(c.lit)
		took := time.Since(start)
		var got string
		switch {
		case err != nil:
			got = err.Error()
		default:
			d, ok := v.(VDecimal)
			if !ok {
				t.Errorf("%s: decoded as %T, want a Decimal", c.lit, v)
				continue
			}
			// A regression accepts a scale in the millions; printing it
			// would take the test minutes and the log gigabytes.
			if d.Scale > 2*maxDecimalPlaces {
				got = fmt.Sprintf("a Decimal with %d places", d.Scale)
			} else {
				got = decString(d)
			}
		}
		if got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.lit, got, c.want)
		}
		// Generous on purpose: what this catches is a billion-digit number
		// being built, which takes seconds, not a slow machine.
		if took > 200*time.Millisecond {
			t.Errorf("%s took %v to decode; the bound has to be checked before anything is built", c.lit, took)
		}
	}
}

// The 34-digit bound is counted before the digits are parsed. big.Int reads
// decimal text in quadratic time, and a request body can be a megabyte of one
// number: refusing it used to cost 1 to 5 s of CPU and 2.4 GB of allocation,
// in all three of the forms that reach parseDecimalString from outside. The
// allocation is what this measures, because it does not depend on the load on
// the machine; the refusal itself must stay the same sentence.
func TestAMegabyteOfDigitsIsRefusedBeforeItIsRead(t *testing.T) {
	digits := strings.Repeat("7", 1<<20)
	for _, c := range []struct{ name, doc, want string }{
		{"JSON number", `{"v":` + digits + `}`,
			"JSON: " + digits + " is not a number Mar can hold exactly: an Int is at most 9007199254740991 and a Decimal at most 34 significant digits and 34 decimal places"},
		{"__dec", `{"__dec":"` + digits + `"}`,
			"the value should be a decimal number, got `" + digits + "`"},
	} {
		var before, after goruntime.MemStats
		goruntime.GC()
		goruntime.ReadMemStats(&before)
		_, err := decodeJSON(c.doc)
		goruntime.ReadMemStats(&after)
		if err == nil {
			t.Errorf("%s: a million digits decoded", c.name)
			continue
		}
		if err.Error() != c.want {
			t.Errorf("%s: refused with a different sentence:\n got %.120s...\nwant %.120s...", c.name, err.Error(), c.want)
		}
		if mb := (after.TotalAlloc - before.TotalAlloc) >> 20; mb > 64 {
			t.Errorf("%s: refusing a megabyte of digits allocated %d MB; the bound has to be counted before big.Int reads them", c.name, mb)
		}
	}
	// Decimal.fromString is the third door, from a String field.
	var before, after goruntime.MemStats
	goruntime.GC()
	goruntime.ReadMemStats(&before)
	if _, err := parseDecimalString(digits); err == nil {
		t.Error("Decimal.fromString: a million digits parsed")
	}
	goruntime.ReadMemStats(&after)
	if mb := (after.TotalAlloc - before.TotalAlloc) >> 20; mb > 64 {
		t.Errorf("Decimal.fromString: refusing a megabyte of digits allocated %d MB", mb)
	}
}
