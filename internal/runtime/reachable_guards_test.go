package runtime_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"mar/internal/typecheck"
)

// Which errors does a CHECKED program still have to be able to receive?
//
// The runtime is full of guards that read `Name: expected Int (got %T)`. Most
// of them are unreachable: the checker's type for that builtin pins the
// argument, so a program that type-checks cannot get there, and the guard is a
// statement about a bug in Mar rather than about the user's code.
//
// The ones that ARE reachable are the ones whose declared type does NOT pin
// the argument -- a bare type variable, which asserts nothing. That is a
// mechanical distinction, so it is a test rather than a judgement:
//
//   - a guard whose argument the type pins may say what it likes; nobody sees it
//   - a guard whose argument the type does NOT pin is a message a user reads,
//     and it must not name a Go type
//
// This found `Repo.create : Entity a -> b -> Task a` and its two siblings:
// three guards a program can reach, all three answering "(got runtime.VInt)".
// The compile-time half is in internal/typecheck (the shape lint now refuses
// an argument that cannot be a record at all); this is what is left over when
// the lint cannot be sure, and it is the message that reaches the user.
func TestAGuardAProgramCanReachDoesNotNameAGoType(t *testing.T) {
	guard := regexp.MustCompile(`fmt\.Errorf\("([A-Za-z.]+): expected ([^(]+?) \(got %T\)", args\[(\d)\]\)`)

	syms := typecheck.BaseQualifiedSymbols()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var reachable, pinned int
	var problems []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range guard.FindAllStringSubmatch(string(b), -1) {
			name := m[1]
			i, _ := strconv.Atoi(m[3])
			ty, ok := syms[name]
			if !ok {
				// Not a name a program can write: the drift tests own that
				// question, and an unwritable name has no reachable guard.
				continue
			}
			arg, found := nthArgOf(ty, i)
			if !found {
				problems = append(problems, name+": the guard reads argument "+m[3]+
					", and the declared type has no such argument.\n"+
					"    One of the two is wrong, and the type is what the program obeys.")
				continue
			}
			if _, isVar := typecheck.StripForall(arg).(typecheck.TVar); !isVar {
				pinned++
				continue
			}
			reachable++
			problems = append(problems, name+": argument "+m[3]+" is a bare type variable, so a"+
				" checked program CAN reach this guard, and it answers with Go's name for the value"+
				" (%T).\n    Say what the value is in Mar's words -- marKindOf does it -- or pin the"+
				" argument in the type so the guard becomes unreachable.")
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
	if !t.Failed() {
		t.Logf("%d argument guards, all with the argument pinned by the declared type;"+
			" %d reachable and still naming a Go type", pinned, reachable)
	}
}

func nthArgOf(t typecheck.Type, i int) (typecheck.Type, bool) {
	for {
		fa, ok := typecheck.StripForall(t).(typecheck.TArrow)
		if !ok {
			return nil, false
		}
		if i == 0 {
			return fa.From, true
		}
		i--
		t = fa.To
	}
}

// And the message itself, since a test that only forbids `%T` would pass on
// anything. The three Repo guards are the ones a program can reach, so their
// wording is the wording a user actually sees.
func TestTheReachableGuardsSayWhatTheValueIsInMarsWords(t *testing.T) {
	b, err := os.ReadFile("repo.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	for _, want := range []string{
		`"Repo.create: expected an input record, got %s", marKindOf(args[1])`,
		`"Repo.update: expected a patch record, got %s", marKindOf(args[2])`,
		`"Repo.findBy: expected a filter record, got %s", marKindOf(args[1])`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("repo.go no longer carries:\n  %s", want)
		}
	}
}
