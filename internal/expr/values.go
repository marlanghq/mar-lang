package expr

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

func ToBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case nil:
		return false
	case string:
		return t != ""
	case int:
		return t != 0
	case int64:
		return t != 0
	case float64:
		return t != 0
	default:
		return true
	}
}

func ToFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return 0, false
		}
		return t, true
	case float32:
		f := float64(t)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

func ToString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case nil:
		return "", false
	case bool:
		if t {
			return "true", true
		}
		return "false", true
	case int:
		return fmt.Sprintf("%d", t), true
	case int64:
		return fmt.Sprintf("%d", t), true
	case float64:
		return fmt.Sprintf("%g", t), true
	default:
		return fmt.Sprintf("%v", t), true
	}
}

func Equal(a, b any) bool {
	if af, ok := ToFloat(a); ok {
		if bf, ok := ToFloat(b); ok {
			return af == bf
		}
	}
	return fmt.Sprintf("%#v", a) == fmt.Sprintf("%#v", b)
}

// Compare returns cmp (-1/0/1), ok, err.
func Compare(a, b any) (int, bool, error) {
	if af, ok := ToFloat(a); ok {
		bf, ok := ToFloat(b)
		if !ok {
			return 0, false, nil
		}
		switch {
		case af < bf:
			return -1, true, nil
		case af > bf:
			return 1, true, nil
		default:
			return 0, true, nil
		}
	}
	as, aok := a.(string)
	bs, bok := b.(string)
	if !aok || !bok {
		return 0, false, nil
	}
	switch {
	case as < bs:
		return -1, true, nil
	case as > bs:
		return 1, true, nil
	default:
		return 0, true, nil
	}
}

func Contains(left, right string) bool {
	if left == "" {
		return false
	}
	return strings.Contains(left, right)
}

func StartsWith(left, right string) bool {
	if left == "" {
		return false
	}
	return strings.HasPrefix(left, right)
}

func EndsWith(left, right string) bool {
	if left == "" {
		return false
	}
	return strings.HasSuffix(left, right)
}

func Length(v any) int {
	switch t := v.(type) {
	case string:
		return len(t)
	case []any:
		return len(t)
	default:
		return 0
	}
}

func Matches(subject, pattern string) (bool, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false, fmt.Errorf("invalid regex %q: %w", pattern, err)
	}
	return re.MatchString(subject), nil
}
