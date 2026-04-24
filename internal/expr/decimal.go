package expr

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// Decimal is Mar's exact decimal/rational number representation.
type Decimal struct {
	rat big.Rat
}

func NewDecimalFromInt(value int64) Decimal {
	return Decimal{rat: *big.NewRat(value, 1)}
}

func ParseDecimal(raw string) (Decimal, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Decimal{}, fmt.Errorf("invalid decimal %q", raw)
	}
	value, ok := new(big.Rat).SetString(raw)
	if !ok {
		return Decimal{}, fmt.Errorf("invalid decimal %q", raw)
	}
	return Decimal{rat: *value}, nil
}

func (d Decimal) String() string {
	if d.rat.IsInt() {
		return d.rat.Num().String()
	}
	den := d.rat.Denom()
	if denominatorHasOnlyTwosAndFives(den) {
		return finiteDecimalString(&d.rat)
	}
	return d.rat.RatString()
}

func (d Decimal) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d Decimal) Add(other Decimal) Decimal {
	var out big.Rat
	out.Add(&d.rat, &other.rat)
	return Decimal{rat: out}
}

func (d Decimal) Sub(other Decimal) Decimal {
	var out big.Rat
	out.Sub(&d.rat, &other.rat)
	return Decimal{rat: out}
}

func (d Decimal) Mul(other Decimal) Decimal {
	var out big.Rat
	out.Mul(&d.rat, &other.rat)
	return Decimal{rat: out}
}

func (d Decimal) Quo(other Decimal) (Decimal, error) {
	if other.rat.Sign() == 0 {
		return Decimal{}, fmt.Errorf("division by zero")
	}
	var out big.Rat
	out.Quo(&d.rat, &other.rat)
	return Decimal{rat: out}, nil
}

func (d Decimal) Cmp(other Decimal) int {
	return d.rat.Cmp(&other.rat)
}

func (d Decimal) IsInt64() (int64, bool) {
	if !d.rat.IsInt() {
		return 0, false
	}
	if !d.rat.Num().IsInt64() {
		return 0, false
	}
	return d.rat.Num().Int64(), true
}

func denominatorHasOnlyTwosAndFives(value *big.Int) bool {
	den := new(big.Int).Set(value)
	two := big.NewInt(2)
	five := big.NewInt(5)
	zero := big.NewInt(0)
	rem := new(big.Int)
	for den.Cmp(big.NewInt(1)) > 0 {
		rem.Mod(den, two)
		if rem.Cmp(zero) == 0 {
			den.Div(den, two)
			continue
		}
		rem.Mod(den, five)
		if rem.Cmp(zero) == 0 {
			den.Div(den, five)
			continue
		}
		return false
	}
	return true
}

func finiteDecimalString(value *big.Rat) string {
	num := new(big.Int).Set(value.Num())
	den := new(big.Int).Set(value.Denom())
	sign := ""
	if num.Sign() < 0 {
		sign = "-"
		num.Abs(num)
	}
	twos := 0
	fives := 0
	two := big.NewInt(2)
	five := big.NewInt(5)
	zero := big.NewInt(0)
	rem := new(big.Int)
	for den.Cmp(big.NewInt(1)) > 0 {
		rem.Mod(den, two)
		if rem.Cmp(zero) == 0 {
			den.Div(den, two)
			twos++
			continue
		}
		rem.Mod(den, five)
		if rem.Cmp(zero) == 0 {
			den.Div(den, five)
			fives++
			continue
		}
		break
	}
	scale := twos
	if fives > scale {
		scale = fives
	}
	for i := 0; i < scale-twos; i++ {
		num.Mul(num, two)
	}
	for i := 0; i < scale-fives; i++ {
		num.Mul(num, five)
	}
	digits := num.String()
	if scale == 0 {
		return sign + digits
	}
	for len(digits) <= scale {
		digits = "0" + digits
	}
	point := len(digits) - scale
	out := digits[:point] + "." + digits[point:]
	out = strings.TrimRight(out, "0")
	out = strings.TrimRight(out, ".")
	if out == "" {
		out = "0"
	}
	return sign + out
}
