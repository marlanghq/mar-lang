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
	// Every shape an argument guard is written in, not one of them. The first
	// version of this census matched only `Name: expected X (got %T)` and so
	// counted 57 of the runtime's guards while claiming to count them all;
	// `must be a X (got %T)` and the asDecimal-style helpers went unseen. The
	// rule that actually holds is broader and simpler: a message that prints a
	// value with %T, or a helper that will, applied to a numbered argument.
	//
	// A guard that already answers in Mar's words is matched too, on purpose:
	// fixing one must not remove it from the census, or the three this test
	// exists for would drop out of view the moment they were fixed.
	leaking := []*regexp.Regexp{
		// fmt.Errorf("Name: ... %T ...", args[i])
		regexp.MustCompile(`fmt\.Errorf\("([A-Za-z][A-Za-z.]*)[^"]*%T[^"]*", *args\[(\d)\]`),
		// asDecimal("Name", args[i]) and its siblings
		regexp.MustCompile(`as[A-Z][A-Za-z]*\("([A-Za-z][A-Za-z.]*)", *args\[(\d)\]`),
	}
	worded := regexp.MustCompile(`fmt\.Errorf\("([A-Za-z][A-Za-z.]*)[^"]*", *marKindOf\(args\[(\d)\]`)

	syms := typecheck.AllQualifiedSymbols()
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
		type hit struct {
			m       []string
			leaksGo bool
		}
		var matches []hit
		for _, g := range leaking {
			for _, m := range g.FindAllStringSubmatch(string(b), -1) {
				matches = append(matches, hit{m, true})
			}
		}
		for _, m := range worded.FindAllStringSubmatch(string(b), -1) {
			matches = append(matches, hit{m, false})
		}
		for _, h := range matches {
			m := h.m
			name := m[1]
			i, _ := strconv.Atoi(m[2])
			ty, ok := syms[name]
			if !ok {
				// A guard naming something that is not a builtin is either a
				// typo or dead code, and either way it silently leaves the
				// census: measured, renaming one guard's builtin dropped it
				// from the count without failing anything. Names that are
				// deliberately not builtins are listed, with the reason.
				if why, allowed := notABuiltin[name]; allowed {
					_ = why
					continue
				}
				problems = append(problems, name+": this guard names something that is not a"+
					" builtin, so nothing here can say whether a program can reach it.\n"+
					"    Fix the name, or list it in notABuiltin with the reason.")
				continue
			}
			arg, found := nthArgOf(ty, i)
			if !found {
				problems = append(problems, name+": the guard reads argument "+m[2]+
					", and the declared type has no such argument.\n"+
					"    One of the two is wrong, and the type is what the program obeys.")
				continue
			}
			if _, isVar := typecheck.StripForall(arg).(typecheck.TVar); !isVar {
				pinned++
				continue
			}
			reachable++
			if !h.leaksGo {
				// Reachable, and it says what the value is in Mar's words.
				// That is the whole ask; it stays counted so the number a
				// reader sees is the number of guards a program can hit.
				continue
			}
			problems = append(problems, name+": argument "+m[2]+" is a bare type variable, so a"+
				" checked program CAN reach this guard, and it answers with Go's name for the value"+
				" (%T).\n    Say what the value is in Mar's words -- marKindOf does it -- or pin the"+
				" argument in the type so the guard becomes unreachable.")
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
	// The floor. Measured: with the glob changed to match nothing, every
	// number above is zero and this test passed -- the census could stop
	// looking entirely and stay green, which is the exact failure the rule
	// two commits ago was written about, broken here in the same session.
	//
	// The total is pinned rather than merely non-zero, because the interesting
	// direction is a guard silently LEAVING the population: rewording one out
	// of the patterns above is invisible otherwise.
	const total = 77
	if pinned+reachable != total {
		t.Errorf("the census sees %d argument guards and expects %d.\n"+
			"    Up: a new guard needs its argument checked against the declared type.\n"+
			"    Down: a guard left the patterns above, and leaving them is how one\n"+
			"    stops being watched. Adjust the number deliberately.", pinned+reachable, total)
	}
	if !t.Failed() {
		t.Logf("%d argument guards: %d unreachable because the declared type pins the"+
			" argument, %d reachable and answering in Mar's words", pinned+reachable, pinned, reachable)
	}
}

// Guards whose leading word is not a builtin's name. Each needs a reason,
// because the alternative is a typo that leaves the census in silence.
var notABuiltin = map[string]string{}

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
