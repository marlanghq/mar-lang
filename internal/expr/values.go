package expr

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// RequireBool enforces Mar's explicit boolean semantics.
func RequireBool(v any) (bool, error) {
	value, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("expected bool")
	}
	return value, nil
}

// ToDecimal converts supported numeric values to Mar's exact decimal form.
func ToDecimal(v any) (Decimal, bool) {
	switch t := v.(type) {
	case int:
		return NewDecimalFromInt(int64(t)), true
	case int64:
		return NewDecimalFromInt(t), true
	case Decimal:
		return t, true
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return Decimal{}, false
		}
		value, err := ParseDecimal(strconv.FormatFloat(t, 'g', -1, 64))
		return value, err == nil
	case float32:
		f := float64(t)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return Decimal{}, false
		}
		value, err := ParseDecimal(strconv.FormatFloat(f, 'g', -1, 32))
		return value, err == nil
	default:
		return Decimal{}, false
	}
}

// ToList converts a supported list value into a Go slice.
func ToList(v any) ([]any, bool) {
	switch t := v.(type) {
	case []any:
		return t, true
	default:
		return nil, false
	}
}

// RequireString enforces explicit string semantics for string functions.
func RequireString(v any) (string, error) {
	value, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("expected string")
	}
	return value, nil
}

// Equal compares two values using numeric comparison when both sides are numbers.
func Equal(a, b any) bool {
	if af, ok := ToDecimal(a); ok {
		if bf, ok := ToDecimal(b); ok {
			return af.Cmp(bf) == 0
		}
	}
	return reflect.DeepEqual(a, b)
}

// Compare compares two numeric values and returns -1, 0, or 1 when they are comparable.
func Compare(a, b any) (int, bool, error) {
	if af, ok := ToDecimal(a); ok {
		bf, ok := ToDecimal(b)
		if !ok {
			return 0, false, nil
		}
		return af.Cmp(bf), true, nil
	}
	return 0, false, nil
}

// Contains reports whether left contains right.
func Contains(left, right string) bool {
	if left == "" {
		return false
	}
	return strings.Contains(left, right)
}

// StartsWith reports whether left starts with right.
func StartsWith(left, right string) bool {
	if left == "" {
		return false
	}
	return strings.HasPrefix(left, right)
}

// EndsWith reports whether left ends with right.
func EndsWith(left, right string) bool {
	if left == "" {
		return false
	}
	return strings.HasSuffix(left, right)
}

// Length returns the length of supported string and list values.
func Length(v any) (int, error) {
	switch t := v.(type) {
	case string:
		return len(t), nil
	case []any:
		return len(t), nil
	default:
		return 0, fmt.Errorf("length expects string or list")
	}
}
