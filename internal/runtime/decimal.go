package runtime

// Decimal — the exact base-10 number (docs/proposals/decimal.md).
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
// number — `/` builds a VDivision, the inert exact quotient, and only
// the two resolvers (Decimal.rounded / withRemainder) turn
// it into a value. That is where the rounding decision is written,
// and the only place any digit can be dropped.
//
// The 34-digit bound is what keeps the three runtimes bit-identical:
// Go big.Int here, BigInt in runtime.js, Foundation.Decimal-backed
// words on iOS. Exceeding it is a runtime abort with a clear message.

import (
	"fmt"
	"math/big"
	"strings"
)

const maxDecimalDigits = 34

var (
	bigOne = big.NewInt(1)
	bigTwo = big.NewInt(2)
	bigTen = big.NewInt(10)
)

func pow10(n int) *big.Int {
	return new(big.Int).Exp(bigTen, big.NewInt(int64(n)), nil)
}

// digitCount returns the number of significant decimal digits in c
// (zero counts as one digit).
func digitCount(c *big.Int) int {
	if c.Sign() == 0 {
		return 1
	}
	return len(new(big.Int).Abs(c).String())
}

func checkDecimalBound(op string, c *big.Int) error {
	if digitCount(c) > maxDecimalDigits {
		return fmt.Errorf("%s: Decimal overflow — the result exceeds %d significant digits", op, maxDecimalDigits)
	}
	return nil
}

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
	sum := new(big.Int).Add(ca, cb)
	if err := checkDecimalBound("+", sum); err != nil {
		return VDecimal{}, err
	}
	return VDecimal{Coef: sum, Scale: scale}, nil
}

func decSub(a, b VDecimal) (VDecimal, error) {
	ca, cb, scale := alignDecimals(a, b)
	diff := new(big.Int).Sub(ca, cb)
	if err := checkDecimalBound("-", diff); err != nil {
		return VDecimal{}, err
	}
	return VDecimal{Coef: diff, Scale: scale}, nil
}

func decMul(a, b VDecimal) (VDecimal, error) {
	prod := new(big.Int).Mul(a.Coef, b.Coef)
	if err := checkDecimalBound("*", prod); err != nil {
		return VDecimal{}, err
	}
	return VDecimal{Coef: prod, Scale: a.Scale + b.Scale}, nil
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

// parseDecimalString accepts the canonical form (optional sign,
// digits, optional point and digits) and rejects anything else.
func parseDecimalString(s string) (VDecimal, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return VDecimal{}, fmt.Errorf("invalid Decimal %q", s)
	}
	neg := false
	switch t[0] {
	case '-':
		neg = true
		t = t[1:]
	case '+':
		t = t[1:]
	}
	intPart := t
	fracPart := ""
	if dot := strings.IndexByte(t, '.'); dot >= 0 {
		intPart, fracPart = t[:dot], t[dot+1:]
	}
	if intPart == "" && fracPart == "" {
		return VDecimal{}, fmt.Errorf("invalid Decimal %q", s)
	}
	for _, r := range intPart + fracPart {
		if r < '0' || r > '9' {
			return VDecimal{}, fmt.Errorf("invalid Decimal %q", s)
		}
	}
	coef, ok := new(big.Int).SetString(zeroWhenEmpty(intPart+fracPart), 10)
	if !ok {
		return VDecimal{}, fmt.Errorf("invalid Decimal %q", s)
	}
	if err := checkDecimalBound("Decimal", coef); err != nil {
		return VDecimal{}, err
	}
	if neg {
		coef.Neg(coef)
	}
	return VDecimal{Coef: coef, Scale: len(fracPart)}, nil
}

func zeroWhenEmpty(s string) string {
	if strings.TrimLeft(s, "0") == "" {
		return "0"
	}
	return s
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
	if err := checkDecimalBound("Decimal.rounded", q); err != nil {
		return VDecimal{}, err
	}
	return VDecimal{Coef: q, Scale: scale}, nil
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
// Widening is exact (append zeros); narrowing rounds per mode — the
// same divmod machinery as the resolvers, dividing by one.
func decToScale(v VDecimal, scale int, mode string) (VDecimal, error) {
	if scale < 0 {
		return VDecimal{}, fmt.Errorf("Decimal.toScale: negative scale %d", scale)
	}
	if scale >= v.Scale {
		coef := new(big.Int).Mul(v.Coef, pow10(scale-v.Scale))
		if err := checkDecimalBound("Decimal.toScale", coef); err != nil {
			return VDecimal{}, err
		}
		return VDecimal{Coef: coef, Scale: scale}, nil
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
	if !r.Coef.IsInt64() {
		return 0, fmt.Errorf("Decimal: value does not fit an Int")
	}
	return r.Coef.Int64(), nil
}

func decimalFromInt(n int64) VDecimal {
	return VDecimal{Coef: big.NewInt(n), Scale: 0}
}

// decimalCoefficientAtScale returns the integer coefficient of d at
// the given fixed scale — the storage form of an Entity.decimal
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
			if !r.Coef.IsInt64() {
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
			var q VDecimal
			if dv.Den.Coef.Sign() == 0 {
				q = VDecimal{Coef: big.NewInt(0), Scale: int(scale)}
			} else {
				q, err = roundQuotient(dv.Num, dv.Den, int(scale), "Down")
				if err != nil {
					return nil, err
				}
			}
			// remainder = a - q*b, computed with the exact ops so the
			// invariant q * b + r == a holds by construction.
			qb, err := decMul(q, dv.Den)
			if err != nil {
				return nil, err
			}
			r, err := decSub(dv.Num, qb)
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
