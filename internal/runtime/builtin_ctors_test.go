package runtime

import (
	"testing"

	"mar/internal/ctorgen"
)

// Every qualified builtin constructor the typechecker will accept has to be a
// value the Go runtime can produce. Anything else is a program that passes
// `mar check` and dies when it runs.
//
// This is not hypothetical. `Keyboard.*` and `Gamepad.*` were registered in the
// JS and Swift runtimes and in no Go table, so a shared module holding
//
//	jumpKey : Keyboard.Key
//	jumpKey = Keyboard.Space
//
// compiled clean and then killed the SERVER at boot with
// `unbound constructor: Keyboard.Space`, 121 names one import away from
// taking a backend down. The set is generated for all three runtimes now; this
// asserts the Go end of it actually resolves, rather than trusting that the
// generated file is wired in.
func TestEveryGeneratedCtorResolvesInBaseEnv(t *testing.T) {
	env := BaseEnv()
	entries := ctorgen.Entries()
	if len(entries) == 0 {
		t.Fatal("ctorgen produced no entries")
	}
	for _, e := range entries {
		v, ok := env.Lookup(e.Qualified)
		if !ok {
			t.Errorf("%s is a constructor the typechecker accepts, and the Go runtime has no value for it "+
				"(a server evaluating it dies with \"unbound constructor\")", e.Qualified)
			continue
		}
		if e.Arity == 0 {
			c, isCtor := v.(VCtor)
			if !isCtor {
				t.Errorf("%s: want a VCtor value, got %T", e.Qualified, v)
				continue
			}
			if c.Tag != e.Tag {
				t.Errorf("%s: tag %q, want %q — the renderers match on the bare tag", e.Qualified, c.Tag, e.Tag)
			}
			continue
		}
		fn, isFn := v.(VFn)
		if !isFn {
			t.Errorf("%s takes %d argument(s), so it has to be a function; got %T", e.Qualified, e.Arity, v)
			continue
		}
		if fn.Arity != e.Arity {
			t.Errorf("%s: arity %d, want %d", e.Qualified, fn.Arity, e.Arity)
		}
	}
}
