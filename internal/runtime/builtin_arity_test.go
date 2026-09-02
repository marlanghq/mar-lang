package runtime_test

import (
	"sort"
	"strings"
	"testing"

	"mar/internal/runtime"
	"mar/internal/typecheck"
)

// A builtin's type says how many arguments it takes; its implementation says
// how many it waits for. Nothing compared the two.
//
// The drift tests answer "does this name exist in all three runtimes", which
// is the question that was asked after a missing name took a server down. They
// do not answer "and does it take the same arguments" — and an implementation
// that waits for MORE than its type promises can never be finished by a
// program that type-checks: applying it to every argument the type has leaves
// a partially applied FUNCTION sitting where the type says a value.
//
// That is what `Service.call` did on the Go side: arity 4 against a type of 3,
// so `Service.call svc req toMsg` produced a `VFn` where the program's own
// annotation said `Cmd msg`, and the stub's "only available in the browser
// runtime" message — the entire reason that stub exists — was unreachable.
//
// The reverse (implementation arity BELOW the type) is ordinary and expected:
// a builtin may take two arguments and return a function for the third.
func TestNoBuiltinWaitsForMoreArgumentsThanItsTypeHas(t *testing.T) {
	env := runtime.BaseEnv()

	var bad []string
	for name, ty := range typecheck.AllQualifiedSymbols() {
		v, ok := env.Lookup(name)
		if !ok {
			continue // the drift test owns "is it defined at all"
		}
		fn, ok := v.(runtime.VFn)
		if !ok {
			continue
		}
		if want := arrowArity(ty); fn.Arity > want {
			bad = append(bad, name+": type takes "+itoa(want)+
				" argument(s), the implementation waits for "+itoa(fn.Arity))
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Fatalf("%d builtin(s) can never be fully applied by a checked program:\n  %s\n\n"+
			"Applying one to every argument its type has yields a function where the type says a value.",
			len(bad), strings.Join(bad, "\n  "))
	}
}

// arrowArity counts the arrows a caller can actually supply arguments to:
// the ones at the top level, not those inside a parenthesised argument.
func arrowArity(t typecheck.Type) int {
	n := 0
	for {
		switch v := t.(type) {
		case typecheck.TForall:
			t = v.Body
		case *typecheck.TForall:
			t = v.Body
		case typecheck.TArrow:
			n++
			t = v.To
		case *typecheck.TArrow:
			n++
			t = v.To
		default:
			return n
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
