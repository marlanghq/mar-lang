package runtime

// Decimal: the exact base-10 number (docs/proposals/decimal.md).
//
// A VDecimal is an integer coefficient (big.Int, bounded to 34
// significant digits, the decimal128 significand) plus a scale: the
// number of digits after the point. 12.50 is coefficient 1250, scale
// 2. Values carry their scale (display is faithful: "1.50" prints
// with two places) while equality and ordering are numeric (1.50 ==
// 1.5).
//
// + - * are exact and closed: add/sub align scales, mul adds them,
// and nothing in them ever rounds. Division does not even produce a
// number: `/` builds a VDivision, the inert exact quotient, and only
// the two resolvers (Decimal.rounded / withRemainder) turn
// it into a value. That is where the rounding decision is written,
// and the only place any digit can be dropped.
//
// Two bounds, the same on all three runtimes (Go big.Int here, BigInt in
// runtime.js, a digit array on iOS): at most 34 significant digits and at
// most 34 places after the point (ADR 0039). The places bound is what keeps
// every operation cheap: before it, a value carried any scale it was given,
// and `1e-99999999` turned "add 1" into 98 s and 1.96 GB. Zeros at the right
// end of the fraction count against neither bound: fitDecimal drops them, only
// as many as needed, and never a digit that changes the value. Anything else
// over a bound is a runtime abort with a clear message.

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

const maxDecimalDigits = 34

// maxDecimalPlaces bounds the SCALE, which maxDecimalDigits does not: 1e-50 is
// one significant digit with fifty places. runtime.js (MAX_DECIMAL_PLACES) and
// MarDecimal.swift (DecMath.maxDecimalPlaces) name the same bound, and
// shared_constants_test.go holds the three to one value.
const maxDecimalPlaces = 34

var (
	bigOne = big.NewInt(1)
	bigTwo = big.NewInt(2)
	bigTen = big.NewInt(10)
)

func pow10(n int) *big.Int {
	return new(big.Int).Exp(bigTen, big.NewInt(int64(n)), nil)
}

func errDecimalOverflow(op string) error {
	return fmt.Errorf("%s: Decimal overflow — the result exceeds %d significant digits", op, maxDecimalDigits)
}

func errDecimalPlaces(op string) error {
	return fmt.Errorf("%s: Decimal overflow — the result needs more than %d decimal places", op, maxDecimalPlaces)
}

// errScaleArgument is the refusal of a scale a caller ASKED for: it is not a
// result that happened to be too fine, it is a request no Decimal can honor.
func errScaleArgument(op string, scale int64) error {
	return fmt.Errorf("%s: scale %d is more than the %d decimal places a Decimal can hold", op, scale, maxDecimalPlaces)
}

// placesToDrop is the whole rule of the two bounds. digits is a coefficient's
// magnitude in base ten with no leading zeros, and not zero; scale is how many
// of those digits sit after the point. It answers how many zeros at the right
// end must go for the value to fit both bounds, or the error when zeros are
// not enough: Mar never drops a digit that changes the value.
//
// Only zeros AFTER the point can go. A zero before it is part of the number
// (1e34 is thirty-five digits, and an error). When zeros cannot mend the
// result, the error names the places if more than 34 would remain once every
// zero that can go went, and the digits otherwise: 99.1 times
// 0.1234567890123456789012345678901234 is reported as places, and 99.0 times
// the same, whose one zero brings the places to 34, as digits.
//
// runtime.js (decPlacesToDrop) and MarDecimal.swift (DecMath.placesToDrop)
// carry the same rule; parityfixture.DecimalBoundCases runs the same programs
// on the three (TestTheServerAnswersTheDecimalBoundCases and its browser and
// phone halves) and holds them to the same answers. The parser has a copy for
// literals (decimalParts), which TestALiteralAndFromStringFitTheSameWay
// compares.
func placesToDrop(op string, digits string, scale int) (int, error) {
	over := max(scale-maxDecimalPlaces, len(digits)-maxDecimalDigits, 0)
	if over == 0 {
		return 0, nil
	}
	zeros := min(len(digits)-len(strings.TrimRight(digits, "0")), scale)
	if over > zeros {
		if scale-zeros > maxDecimalPlaces {
			return 0, errDecimalPlaces(op)
		}
		return 0, errDecimalOverflow(op)
	}
	return over, nil
}

// fitDecimal is how every result is made: coef at scale, held to both bounds.
// A value that fits comes back exactly as given, so nothing that fitted before
// the places bound existed changes its scale or its display. Zero always fits,
// with at most maxDecimalPlaces places.
func fitDecimal(op string, coef *big.Int, scale int) (VDecimal, error) {
	if coef.Sign() == 0 {
		return VDecimal{Coef: coef, Scale: min(scale, maxDecimalPlaces)}, nil
	}
	if scale <= maxDecimalPlaces && coef.CmpAbs(decimalDigitsCeiling) < 0 {
		return VDecimal{Coef: coef, Scale: scale}, nil
	}
	digits := new(big.Int).Abs(coef).String()
	drop, err := placesToDrop(op, digits, scale)
	if err != nil {
		return VDecimal{}, err
	}
	if drop == 0 {
		return VDecimal{Coef: coef, Scale: scale}, nil
	}
	c, _ := new(big.Int).SetString(digits[:len(digits)-drop], 10)
	if coef.Sign() < 0 {
		c.Neg(c)
	}
	return VDecimal{Coef: c, Scale: scale - drop}, nil
}

// decimalDigitsCeiling is 10^34, the smallest magnitude with 35 digits: below
// it fitDecimal needs no digit count at all, which is every ordinary result.
var decimalDigitsCeiling = pow10(maxDecimalDigits)

// alignDecimals brings two decimals to a common scale, returning the
// rescaled coefficients and that scale. Exact: the smaller-scale
// coefficient is multiplied by a power of ten.
func alignDecimals(a, b VDecimal) (*big.Int, *big.Int, int) {
	ca, cb := a.Coef, b.Coef
	scale := a.Scale
	switch {
	case a.Scale < b.Scale:
		ca = new(big.Int).Mul(ca, pow10(b.Scale-a.Scale))
		scale = b.Scale
	case b.Scale < a.Scale:
		cb = new(big.Int).Mul(cb, pow10(a.Scale-b.Scale))
	}
	return ca, cb, scale
}

func decAdd(a, b VDecimal) (VDecimal, error) {
	ca, cb, scale := alignDecimals(a, b)
	return fitDecimal("+", new(big.Int).Add(ca, cb), scale)
}

func decSub(a, b VDecimal) (VDecimal, error) {
	ca, cb, scale := alignDecimals(a, b)
	return fitDecimal("-", new(big.Int).Sub(ca, cb), scale)
}

func decMul(a, b VDecimal) (VDecimal, error) {
	return fitDecimal("*", new(big.Int).Mul(a.Coef, b.Coef), a.Scale+b.Scale)
}

func decCompare(a, b VDecimal) int {
	ca, cb, _ := alignDecimals(a, b)
	return ca.Cmp(cb)
}

// decString renders the canonical, scale-faithful form: "-12.50",
// "0.05", "7" (scale 0 has no point).
func decString(v VDecimal) string {
	neg := v.Coef.Sign() < 0
	digits := new(big.Int).Abs(v.Coef).String()
	var out string
	if v.Scale == 0 {
		out = digits
	} else {
		if len(digits) <= v.Scale {
			digits = strings.Repeat("0", v.Scale-len(digits)+1) + digits
		}
		cut := len(digits) - v.Scale
		out = digits[:cut] + "." + digits[cut:]
	}
	if neg && out != "0" && strings.Trim(out, "0.") != "" {
		out = "-" + out
	}
	return out
}

// splitDecimalText reads the canonical form (optional sign, digits, optional
// point and digits) and answers its parts, or ok=false for anything else.
func splitDecimalText(s string) (neg bool, intPart, fracPart string, ok bool) {
	t := strings.TrimSpace(s)
	if t == "" {
		return false, "", "", false
	}
	switch t[0] {
	case '-':
		neg = true
		t = t[1:]
	case '+':
		t = t[1:]
	}
	intPart = t
	if dot := strings.IndexByte(t, '.'); dot >= 0 {
		intPart, fracPart = t[:dot], t[dot+1:]
	}
	if intPart == "" && fracPart == "" {
		return false, "", "", false
	}
	for _, r := range intPart + fracPart {
		if r < '0' || r > '9' {
			return false, "", "", false
		}
	}
	return neg, intPart, fracPart, true
}

// decimalFromDigits builds a Decimal from text: digits is the coefficient with
// no leading zeros ("" for zero) and scale is how many of them sit after the
// point. Both bounds are applied to the TEXT, before big.Int reads it, so what
// big.Int reads is at most 34 digits. SetString is quadratic in the digits and
// the text can be a whole request body: a megabyte of digits cost 1 to 5 s of
// CPU and 2.4 GB of allocation to be refused, measured, and `0.<a million
// zeros>1` was accepted with a scale of a million.
func decimalFromDigits(op string, neg bool, digits string, scale int) (VDecimal, error) {
	if digits == "" {
		return VDecimal{Coef: big.NewInt(0), Scale: min(scale, maxDecimalPlaces)}, nil
	}
	drop, err := placesToDrop(op, digits, scale)
	if err != nil {
		return VDecimal{}, err
	}
	coef, _ := new(big.Int).SetString(digits[:len(digits)-drop], 10)
	if neg {
		coef.Neg(coef)
	}
	return VDecimal{Coef: coef, Scale: scale - drop}, nil
}

// parseDecimalString backs `Decimal.fromString` and Mar's own `{"__dec":"…"}`
// wire form: the canonical form only, no exponent, held to both bounds.
func parseDecimalString(s string) (VDecimal, error) {
	neg, intPart, fracPart, ok := splitDecimalText(s)
	if !ok {
		return VDecimal{}, fmt.Errorf("invalid Decimal %q", s)
	}
	return decimalFromDigits("Decimal", neg, strings.TrimLeft(intPart+fracPart, "0"), len(fracPart))
}

// decimalFromWireNumber reads a JSON number into an exact Decimal, accepting
// the exponent notation a JSON literal may carry.
//
// It is separate from parseDecimalString on purpose. That function backs
// `Decimal.fromString` and Mar's own `{"__dec":"…"}` wire form, and what those
// accept is the language's business; this is the boundary's business, where the
// document was written by something else. Every JSON serializer prints a large
// number in exponent form, so refusing it here meant the SAME NUMBER decoded or
// did not depending on how the sender chose to spell it:
//
//	{"v":1000000000000000000000000000000}   decoded exactly
//	{"v":1e30}                              refused
//
// Notation is not information. Both are 10^30, both fit Mar's 34 significant
// digits, and both now arrive as the same exact Decimal.
//
// The exponent is applied by MOVING THE POINT, never by multiplying a float:
// 1e30 becomes the coefficient 1 followed by thirty zeros, not the double
// nearest to 10^30. That is the whole reason this exists -- a value Mar can
// hold exactly used to arrive as an approximation of itself.
//
// The exponent is the sender's, so nothing is built from it before the bounds
// are known to hold. `1e-99999999` is eleven bytes: it used to arrive as a
// one-digit coefficient with 99,999,999 places, and printing it built a 100 MB
// string and adding 1 to it then ran for 98 s, 1.96 GB allocated across the
// two, measured. `1e999999999` used to raise ten to its exponent only to count
// the digits and say no.
func decimalFromWireNumber(s string) (VDecimal, error) {
	mantissa, exp := s, 0
	for i, r := range s {
		if r == 'e' || r == 'E' {
			n, err := strconv.Atoi(s[i+1:])
			if err != nil {
				return VDecimal{}, fmt.Errorf("invalid Decimal %q", s)
			}
			mantissa, exp = s[:i], n
			break
		}
	}
	neg, intPart, fracPart, ok := splitDecimalText(mantissa)
	if !ok {
		return VDecimal{}, fmt.Errorf("invalid Decimal %q", s)
	}
	digits := strings.TrimLeft(intPart+fracPart, "0")
	// The number ends with len(fracPart) - exp places. An exponent below
	// -(34 + the digits) leaves more than 34 of them whatever zeros the digits
	// end with, and is answered before the subtraction, which wraps around at
	// math.MinInt64: that is how `1e-9223372036854775808` used to decode as 1.
	if exp < -(maxDecimalPlaces + len(digits)) {
		if digits == "" {
			return VDecimal{Coef: big.NewInt(0), Scale: maxDecimalPlaces}, nil
		}
		return VDecimal{}, errDecimalPlaces("Decimal")
	}
	scale := len(fracPart) - exp
	if scale >= 0 {
		return decimalFromDigits("Decimal", neg, digits, scale)
	}
	// The point moved past the last digit: pad with zeros rather than carry a
	// negative scale, which nothing downstream expects. Those zeros are before
	// the point, so they count, and the padding is refused before it is built.
	// Zero is the one coefficient no padding can push past the bound, and it
	// needs none.
	if digits == "" {
		return VDecimal{Coef: big.NewInt(0), Scale: 0}, nil
	}
	if -scale > maxDecimalDigits-len(digits) {
		return VDecimal{}, errDecimalOverflow("Decimal")
	}
	coef, _ := new(big.Int).SetString(digits, 10)
	coef.Mul(coef, pow10(-scale))
	if neg {
		coef.Neg(coef)
	}
	return VDecimal{Coef: coef, Scale: 0}, nil
}

// roundingModeTag extracts the Decimal.Rounding constructor tag.
func roundingModeTag(v Value) (string, error) {
	c, ok := v.(VCtor)
	if !ok {
		return "", fmt.Errorf("expected a Decimal.Rounding value")
	}
	switch c.Tag {
	case "HalfEven", "HalfUp", "Down", "Up", "Floor", "Ceiling":
		return c.Tag, nil
	}
	return "", fmt.Errorf("unknown rounding mode %s", c.Tag)
}

// roundQuotient computes num/den rounded to `scale` places under
// `mode`. num and den are decimal (coef, scale) pairs; den must be
// nonzero (callers handle the total-division zero case). The identity
// worked in integers: value = numC * 10^(scale + denS - numS) / denC,
// rounded per mode.
func roundQuotient(num, den VDecimal, scale int, mode string) (VDecimal, error) {
	// Bring the numerator to the target scale relative to the
	// denominator: shift = scale + den.Scale - num.Scale.
	shift := scale + den.Scale - num.Scale
	n := new(big.Int).Set(num.Coef)
	d := new(big.Int).Set(den.Coef)
	if shift > 0 {
		n.Mul(n, pow10(shift))
	} else if shift < 0 {
		d.Mul(d, pow10(-shift))
	}
	negative := (n.Sign() < 0) != (d.Sign() < 0)
	n.Abs(n)
	d.Abs(d)

	q, r := new(big.Int).QuoRem(n, d, new(big.Int))

	roundUp := false
	if r.Sign() != 0 {
		switch mode {
		case "Down":
			// toward zero: never up
		case "Up":
			roundUp = true
		case "Floor":
			roundUp = negative
		case "Ceiling":
			roundUp = !negative
		case "HalfUp", "HalfEven":
			twice := new(big.Int).Mul(r, bigTwo)
			switch twice.Cmp(d) {
			case 1:
				roundUp = true
			case 0:
				if mode == "HalfUp" {
					roundUp = true
				} else {
					// banker's: to the even neighbour
					roundUp = q.Bit(0) == 1
				}
			}
		}
	}
	if roundUp {
		q.Add(q, bigOne)
	}
	if negative && q.Sign() != 0 {
		q.Neg(q)
	}
	return fitDecimal("Decimal.rounded", q, scale)
}

func asDecimal(op string, v Value) (VDecimal, error) {
	d, ok := v.(VDecimal)
	if !ok {
		return VDecimal{}, fmt.Errorf("%s: expected Decimal", op)
	}
	return d, nil
}

func asDivision(op string, v Value) (VDivision, error) {
	d, ok := v.(VDivision)
	if !ok {
		return VDivision{}, fmt.Errorf("%s: expected Decimal.Division", op)
	}
	return d, nil
}

func intFromValue(op string, v Value) (int64, error) {
	i, ok := v.(VInt)
	if !ok {
		return 0, fmt.Errorf("%s: expected Int", op)
	}
	return i.V, nil
}

// decToScale rescales a decimal to `scale` places under `mode`.
// Widening is exact (append zeros); narrowing rounds per mode: the
// same divmod machinery as the resolvers, dividing by one.
func decToScale(v VDecimal, scale int, mode string) (VDecimal, error) {
	if scale < 0 {
		return VDecimal{}, fmt.Errorf("Decimal.toScale: negative scale %d", scale)
	}
	if scale > maxDecimalPlaces {
		return VDecimal{}, errScaleArgument("Decimal.toScale", int64(scale))
	}
	if scale >= v.Scale {
		return fitDecimal("Decimal.toScale", new(big.Int).Mul(v.Coef, pow10(scale-v.Scale)), scale)
	}
	one := VDecimal{Coef: big.NewInt(1), Scale: 0}
	return roundQuotient(v, one, scale, mode)
}

func decToInt(v VDecimal, mode string) (int64, error) {
	one := VDecimal{Coef: big.NewInt(1), Scale: 0}
	r, err := roundQuotient(v, one, 0, mode)
	if err != nil {
		return 0, err
	}
	// IsInt64 is the wrong bound: Mar's Int is 53 bits, not 64. Using the
	// wider one let this hand back numbers the browser refuses, so the same
	// money computation succeeded on the server and failed in the page.
	if !r.Coef.IsInt64() || !inIntRange(r.Coef.Int64()) {
		return 0, fmt.Errorf("Decimal: value does not fit an Int")
	}
	return r.Coef.Int64(), nil
}

func decimalFromInt(n int64) VDecimal {
	return VDecimal{Coef: big.NewInt(n), Scale: 0}
}

// decimalCoefficientAtScale returns the integer coefficient of d at
// the given fixed scale: the storage form of an Entity.decimal
// column. Exact only: a value with more fractional digits than the
// scale errors instead of silently rounding.
func decimalCoefficientAtScale(d VDecimal, scale int) (int64, error) {
	if d.Scale <= scale {
		c := new(big.Int).Mul(d.Coef, pow10(scale-d.Scale))
		if !c.IsInt64() {
			return 0, fmt.Errorf("value %s does not fit the column", decString(d))
		}
		return c.Int64(), nil
	}
	q, r := new(big.Int).QuoRem(d.Coef, pow10(d.Scale-scale), new(big.Int))
	if r.Sign() != 0 {
		return 0, fmt.Errorf("value %s has more than %d decimal place(s); round explicitly with Decimal.toScale", decString(d), scale)
	}
	if !q.IsInt64() {
		return 0, fmt.Errorf("value %s does not fit the column", decString(d))
	}
	return q.Int64(), nil
}

func decimalBuiltins() map[string]Value {
	just := func(v Value) Value { return VCtor{Tag: "Just", Args: []Value{v}} }
	nothing := VCtor{Tag: "Nothing"}

	return map[string]Value{
		"decimalZero": VDecimal{Coef: big.NewInt(0), Scale: 0},
		"decimalFromInt": nativeFn(1, func(args []Value) (Value, error) {
			n, err := intFromValue("Decimal.fromInt", args[0])
			if err != nil {
				return nil, err
			}
			return decimalFromInt(n), nil
		}),
		"decimalFromCents": nativeFn(1, func(args []Value) (Value, error) {
			n, err := intFromValue("Decimal.fromCents", args[0])
			if err != nil {
				return nil, err
			}
			return VDecimal{Coef: big.NewInt(n), Scale: 2}, nil
		}),
		"decimalToCents": nativeFn(1, func(args []Value) (Value, error) {
			d, err := asDecimal("Decimal.toCents", args[0])
			if err != nil {
				return nil, err
			}
			if d.Scale > 2 {
				return nil, fmt.Errorf("Decimal.toCents: value has scale %d (more than 2 places); round explicitly first with Decimal.toScale", d.Scale)
			}
			r, err := decToScale(d, 2, "Down")
			if err != nil {
				return nil, err
			}
			if !r.Coef.IsInt64() || !inIntRange(r.Coef.Int64()) {
				return nil, fmt.Errorf("Decimal.toCents: value does not fit an Int")
			}
			return VInt{V: r.Coef.Int64()}, nil
		}),
		"decimalTruncate": nativeFn(1, func(args []Value) (Value, error) {
			d, err := asDecimal("Decimal.truncate", args[0])
			if err != nil {
				return nil, err
			}
			n, err := decToInt(d, "Down")
			if err != nil {
				return nil, err
			}
			return VInt{V: n}, nil
		}),
		"decimalRound": nativeFn(1, func(args []Value) (Value, error) {
			d, err := asDecimal("Decimal.round", args[0])
			if err != nil {
				return nil, err
			}
			n, err := decToInt(d, "HalfEven")
			if err != nil {
				return nil, err
			}
			return VInt{V: n}, nil
		}),
		"decimalFloor": nativeFn(1, func(args []Value) (Value, error) {
			d, err := asDecimal("Decimal.floor", args[0])
			if err != nil {
				return nil, err
			}
			n, err := decToInt(d, "Floor")
			if err != nil {
				return nil, err
			}
			return VInt{V: n}, nil
		}),
		"decimalCeiling": nativeFn(1, func(args []Value) (Value, error) {
			d, err := asDecimal("Decimal.ceiling", args[0])
			if err != nil {
				return nil, err
			}
			n, err := decToInt(d, "Ceiling")
			if err != nil {
				return nil, err
			}
			return VInt{V: n}, nil
		}),
		"decimalToIntWith": nativeFn(2, func(args []Value) (Value, error) {
			mode, err := roundingModeTag(args[0])
			if err != nil {
				return nil, fmt.Errorf("Decimal.toIntWith: %v", err)
			}
			d, err := asDecimal("Decimal.toIntWith", args[1])
			if err != nil {
				return nil, err
			}
			n, err := decToInt(d, mode)
			if err != nil {
				return nil, err
			}
			return VInt{V: n}, nil
		}),
		"decimalToScale": nativeFn(3, func(args []Value) (Value, error) {
			mode, err := roundingModeTag(args[0])
			if err != nil {
				return nil, fmt.Errorf("Decimal.toScale: %v", err)
			}
			scale, err := intFromValue("Decimal.toScale", args[1])
			if err != nil {
				return nil, err
			}
			d, err := asDecimal("Decimal.toScale", args[2])
			if err != nil {
				return nil, err
			}
			return decToScale(d, int(scale), mode)
		}),
		"decimalAbs": nativeFn(1, func(args []Value) (Value, error) {
			d, err := asDecimal("Decimal.abs", args[0])
			if err != nil {
				return nil, err
			}
			return VDecimal{Coef: new(big.Int).Abs(d.Coef), Scale: d.Scale}, nil
		}),
		"decimalNegate": nativeFn(1, func(args []Value) (Value, error) {
			d, err := asDecimal("Decimal.negate", args[0])
			if err != nil {
				return nil, err
			}
			return VDecimal{Coef: new(big.Int).Neg(d.Coef), Scale: d.Scale}, nil
		}),
		"decimalCompare": nativeFn(2, func(args []Value) (Value, error) {
			a, err := asDecimal("Decimal.compare", args[0])
			if err != nil {
				return nil, err
			}
			b, err := asDecimal("Decimal.compare", args[1])
			if err != nil {
				return nil, err
			}
			switch decCompare(a, b) {
			case -1:
				return VCtor{Tag: "LT"}, nil
			case 1:
				return VCtor{Tag: "GT"}, nil
			}
			return VCtor{Tag: "EQ"}, nil
		}),
		"decimalFromString": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("Decimal.fromString: expected String")
			}
			d, err := parseDecimalString(s.V)
			if err != nil {
				return nothing, nil
			}
			return just(d), nil
		}),
		"decimalToString": nativeFn(1, func(args []Value) (Value, error) {
			d, err := asDecimal("Decimal.toString", args[0])
			if err != nil {
				return nil, err
			}
			return VString{V: decString(d)}, nil
		}),
		// --- Division resolvers: the only exits from Decimal.Division ---
		"decimalRounded": nativeFn(3, func(args []Value) (Value, error) {
			mode, err := roundingModeTag(args[0])
			if err != nil {
				return nil, fmt.Errorf("Decimal.rounded: %v", err)
			}
			scale, err := intFromValue("Decimal.rounded", args[1])
			if err != nil {
				return nil, err
			}
			dv, err := asDivision("Decimal.rounded", args[2])
			if err != nil {
				return nil, err
			}
			if scale < 0 {
				return nil, fmt.Errorf("Decimal.rounded: negative scale %d", scale)
			}
			if scale > maxDecimalPlaces {
				return nil, errScaleArgument("Decimal.rounded", scale)
			}
			if dv.Den.Coef.Sign() == 0 {
				// Total, matching Int's `//`: zero at the requested scale.
				return VDecimal{Coef: big.NewInt(0), Scale: int(scale)}, nil
			}
			return roundQuotient(dv.Num, dv.Den, int(scale), mode)
		}),
		"decimalWithRemainder": nativeFn(2, func(args []Value) (Value, error) {
			scale, err := intFromValue("Decimal.withRemainder", args[0])
			if err != nil {
				return nil, err
			}
			dv, err := asDivision("Decimal.withRemainder", args[1])
			if err != nil {
				return nil, err
			}
			if scale < 0 {
				return nil, fmt.Errorf("Decimal.withRemainder: negative scale %d", scale)
			}
			if scale > maxDecimalPlaces {
				return nil, errScaleArgument("Decimal.withRemainder", scale)
			}
			var q VDecimal
			if dv.Den.Coef.Sign() == 0 {
				q = VDecimal{Coef: big.NewInt(0), Scale: int(scale)}
			} else {
				q, err = roundQuotient(dv.Num, dv.Den, int(scale), "Down")
				if err != nil {
					return nil, err
				}
			}
			// remainder = a - q*b, exactly, so q * b + r == a holds by
			// construction. q*b is NOT fitted on its own: it carries n + the
			// divisor's places, which can be more than 34 on the way to a
			// remainder that fits, and only the remainder is a value the
			// program sees. It is formed from q at n places, the zeros the
			// fit took from a quotient over 34 digits put back, so the
			// remainder carries max(scale a, n + scale b) places, fitted, in
			// every case and not only when q kept all of its own (ADR 0039).
			// When the remainder needs more than 34 places its error names
			// Decimal.withRemainder; a quotient over 34 digits is reported by
			// roundQuotient as Decimal.rounded, as it was before the bound.
			qc := q.Coef
			if pad := int(scale) - q.Scale; pad > 0 {
				qc = new(big.Int).Mul(qc, pow10(pad))
			}
			qb := VDecimal{Coef: new(big.Int).Mul(qc, dv.Den.Coef), Scale: int(scale) + dv.Den.Scale}
			ca, cb, rs := alignDecimals(dv.Num, qb)
			r, err := fitDecimal("Decimal.withRemainder", new(big.Int).Sub(ca, cb), rs)
			if err != nil {
				return nil, err
			}
			return VRecord{
				Fields: map[string]Value{"quotient": q, "remainder": r},
				Order:  []string{"quotient", "remainder"},
			}, nil
		}),
	}
}
