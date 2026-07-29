package typecheck

import (
	"strconv"
	"strings"

	"mar/internal/ast"
)

// Exhaustiveness and reachability are the same question asked twice.
//
// "Is this `case` missing a possibility?" is "would one more branch, matching
// anything, still be useful?". "Is this branch dead?" is "is it useful against
// the branches above it?". Maranget's usefulness algorithm answers both, which
// is why they live in one file and share one specialization step — and why
// implementing lists fixed reachability for free.
//
// The shape is a pattern MATRIX: one row per branch, one column per value
// being matched. A case starts one column wide and grows a column per
// constructor argument as the algorithm descends.

// unboundedScalars are the types whose values cannot be enumerated, so no
// list of patterns is ever exhaustive over them and only a catch-all closes
// the case. Lists are deliberately absent: their patterns CAN be exhaustive
// (`[]` together with `x :: rest`), which is why they get a real signature in
// typeSignature instead of living here.
var unboundedScalars = map[string]string{
	"Int":    "Int",
	"String": "String",
	"Char":   "Char",
}

// ctorSig is one constructor of a type's signature: how a pattern names it,
// and the types of the values it carries.
type ctorSig struct {
	key  string // matched against headCtor's key
	show string // how a witness prints it
	args []Type
}

// typeSignature lists the constructors of a type, and says whether that list
// is the whole story. `complete` is the pivotal bit: for a union or a list it
// is true, so covering every constructor is enough; for Int, String and Char
// it is false, because their values cannot be enumerated and only a wildcard
// can close them.
func typeSignature(t Type, env *TypeEnv) (sigs []ctorSig, complete bool) {
	switch ty := t.(type) {
	case TUnit:
		return []ctorSig{{key: "()", show: "()"}}, true
	case TTuple:
		// A tuple has exactly one shape, so matching it is never a choice;
		// the interesting question is always about its members.
		return []ctorSig{{key: "#tuple", show: "tuple", args: ty.Members}}, true
	case TCon:
		if _, unbounded := unboundedScalars[ty.Name]; unbounded {
			return nil, false
		}
		if ty.Name == "List" && len(ty.Args) == 1 {
			// `[]` and `::` are a complete signature the same way a union's
			// constructors are. Naming them here is what lets `case xs of []
			// -> …; x :: rest -> …` be recognised as exhaustive, and
			// `[ a ]` alone be recognised as not.
			elem := ty.Args[0]
			return []ctorSig{
				{key: "[]", show: "[]"},
				{key: "::", show: "::", args: []Type{elem, t}},
			}, true
		}
		ct, ok := env.LookupCustom(ty.Name)
		if !ok {
			return nil, false
		}
		for _, name := range ct.CtorOrder {
			info := ct.Constructors[name]
			args := make([]Type, len(info.Args))
			for i, a := range info.Args {
				args[i] = instantiateCtorArg(a, info.Result, ty.Args)
			}
			sigs = append(sigs, ctorSig{key: name, show: name, args: args})
		}
		return sigs, true
	}
	// Type variables, records and anything else the checker cannot enumerate.
	return nil, false
}

// headCtor reads a pattern as a constructor applied to sub-patterns. A
// wildcard or a binding name is not a constructor, which is what `ok = false`
// means — those are the patterns that match everything.
//
// A fixed-length list pattern is read as a cons cell whose tail is the rest of
// the list, so `[ a, b ]` and `a :: b :: []` are literally the same thing to
// the algorithm and nothing downstream needs to know there were two spellings.
func headCtor(p ast.Pattern) (key string, args []ast.Pattern, ok bool) {
	switch pat := p.(type) {
	case *ast.PVar, *ast.PWildcard:
		return "", nil, false
	case *ast.PRecord:
		// Record patterns only bind fields; there is nothing to refute.
		return "", nil, false
	case *ast.PCtor:
		// Name is already the bare tag — PCtor keeps the qualifier in Module —
		// so a qualified pattern lines up with the signature without stripping.
		return pat.Name, pat.Args, true
	case *ast.PUnit:
		return "()", nil, true
	case *ast.PTuple:
		return "#tuple", pat.Members, true
	case *ast.PCons:
		return "::", []ast.Pattern{pat.Head, pat.Tail}, true
	case *ast.PList:
		if len(pat.Elements) == 0 {
			return "[]", nil, true
		}
		rest := &ast.PList{Pos: pat.Pos, Elements: pat.Elements[1:]}
		return "::", []ast.Pattern{pat.Elements[0], rest}, true
	case *ast.PInt:
		return "int:" + strconv.FormatInt(pat.Value, 10), nil, true
	case *ast.PString:
		return "str:" + pat.Value, nil, true
	case *ast.PChar:
		return "chr:" + string(pat.Value), nil, true
	}
	return "", nil, false
}

func wildcardAt(pos ast.Pos) ast.Pattern { return &ast.PWildcard{Pos: pos} }

// specialize keeps only the rows whose first pattern can match `sig`, and
// replaces that pattern with its arguments — so the matrix moves one
// constructor deeper. A wildcard row matches every constructor and
// contributes wildcards in place of the arguments it never looked at.
func specialize(rows [][]ast.Pattern, sig ctorSig) [][]ast.Pattern {
	var out [][]ast.Pattern
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		key, args, isCtor := headCtor(row[0])
		switch {
		case !isCtor:
			expanded := make([]ast.Pattern, 0, len(sig.args)+len(row)-1)
			for range sig.args {
				expanded = append(expanded, wildcardAt(row[0].Position()))
			}
			out = append(out, append(expanded, row[1:]...))
		case key == sig.key:
			expanded := make([]ast.Pattern, 0, len(sig.args)+len(row)-1)
			for i := range sig.args {
				if i < len(args) {
					expanded = append(expanded, args[i])
				} else {
					expanded = append(expanded, wildcardAt(row[0].Position()))
				}
			}
			out = append(out, append(expanded, row[1:]...))
		}
	}
	return out
}

// defaultMatrix keeps the rows that match every constructor NOT named in the
// first column, dropping that column. It is how the algorithm reasons about
// the values a set of literals leaves over.
func defaultMatrix(rows [][]ast.Pattern) [][]ast.Pattern {
	var out [][]ast.Pattern
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		if _, _, isCtor := headCtor(row[0]); !isCtor {
			out = append(out, row[1:])
		}
	}
	return out
}

// rootKeys is the set of constructors named in the first column.
func rootKeys(rows [][]ast.Pattern) map[string]bool {
	keys := map[string]bool{}
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		if key, _, isCtor := headCtor(row[0]); isCtor {
			keys[key] = true
		}
	}
	return keys
}

// useful reports whether `q` matches any value the rows above it do not — the
// definition of a branch being reachable.
func useful(rows [][]ast.Pattern, types []Type, q []ast.Pattern, env *TypeEnv) bool {
	if len(types) == 0 {
		// Nothing left to inspect: q is useful exactly when nothing above it
		// already matched everything.
		return len(rows) == 0
	}
	head, rest := types[0], types[1:]
	if key, args, isCtor := headCtor(q[0]); isCtor {
		sigs, _ := typeSignature(head, env)
		sig := ctorSig{key: key}
		for _, s := range sigs {
			if s.key == key {
				sig = s
				break
			}
		}
		// A literal is its own nullary constructor and carries no arguments,
		// so the loop above simply does not find it and `sig.args` stays nil.
		next := append(append([]Type{}, sig.args...), rest...)
		expanded := make([]ast.Pattern, 0, len(sig.args)+len(q)-1)
		for i := range sig.args {
			if i < len(args) {
				expanded = append(expanded, args[i])
			} else {
				expanded = append(expanded, wildcardAt(q[0].Position()))
			}
		}
		return useful(specialize(rows, sig), next, append(expanded, q[1:]...), env)
	}
	// q[0] is a wildcard. If the rows above already name every constructor of
	// a complete signature, the wildcard is only useful where one of those
	// constructors still leaves something over.
	sigs, complete := typeSignature(head, env)
	if complete && coversEveryCtor(rows, sigs) {
		for _, sig := range sigs {
			next := append(append([]Type{}, sig.args...), rest...)
			expanded := make([]ast.Pattern, 0, len(sig.args)+len(q)-1)
			for range sig.args {
				expanded = append(expanded, wildcardAt(q[0].Position()))
			}
			if useful(specialize(rows, sig), next, append(expanded, q[1:]...), env) {
				return true
			}
		}
		return false
	}
	return useful(defaultMatrix(rows), rest, q[1:], env)
}

func coversEveryCtor(rows [][]ast.Pattern, sigs []ctorSig) bool {
	if len(sigs) == 0 {
		return false
	}
	keys := rootKeys(rows)
	for _, s := range sigs {
		if !keys[s.key] {
			return false
		}
	}
	return true
}

// missingWitness builds a concrete value the patterns do not match, so the
// error can show what was forgotten instead of only saying that something was.
// Returns ok = false when the rows already cover everything.
func missingWitness(rows [][]ast.Pattern, types []Type, env *TypeEnv) (witness []string, ok bool) {
	if len(types) == 0 {
		return nil, len(rows) == 0
	}
	head, rest := types[0], types[1:]
	sigs, complete := typeSignature(head, env)
	if complete && coversEveryCtor(rows, sigs) {
		for _, sig := range sigs {
			next := append(append([]Type{}, sig.args...), rest...)
			if sub, found := missingWitness(specialize(rows, sig), next, env); found {
				return append([]string{renderWitness(sig, sub[:len(sig.args)])}, sub[len(sig.args):]...), true
			}
		}
		return nil, false
	}
	sub, found := missingWitness(defaultMatrix(rows), rest, env)
	if !found {
		return nil, false
	}
	// Name a constructor the rows never mentioned, when there is one to name;
	// otherwise the honest witness is "anything else".
	if complete {
		keys := rootKeys(rows)
		for _, sig := range sigs {
			if !keys[sig.key] {
				blanks := make([]string, len(sig.args))
				for i := range blanks {
					blanks[i] = "_"
				}
				return append([]string{renderWitness(sig, blanks)}, sub...), true
			}
		}
	}
	return append([]string{"_"}, sub...), true
}

func renderWitness(sig ctorSig, args []string) string {
	switch sig.key {
	case "::":
		if len(args) == 2 {
			return args[0] + " :: " + args[1]
		}
	case "[]", "()":
		return sig.show
	case "#tuple":
		return "( " + strings.Join(args, ", ") + " )"
	}
	if len(args) == 0 {
		return sig.show
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, sig.show)
	for _, a := range args {
		if strings.ContainsAny(a, " ") {
			a = "(" + a + ")"
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

// checkExhaustive is the entry point: every branch must be reachable, and
// together they must leave nothing unmatched.
func checkExhaustive(subjectType Type, branches []ast.CaseBranch, env *TypeEnv, pos ast.Pos) error {
	types := []Type{subjectType}
	var rows [][]ast.Pattern

	// Reachability first, so a dead branch is reported where it is rather
	// than as a confusing side effect of the coverage message.
	for _, b := range branches {
		row := []ast.Pattern{b.Pattern}
		if !useful(rows, types, row, env) {
			return errorf(b.Pattern.Position(), "this branch can never match: the branches above it already cover every value it would accept")
		}
		rows = append(rows, row)
	}

	witness, missing := missingWitness(rows, types, env)
	if !missing {
		return nil
	}
	if len(witness) == 0 {
		return errorf(pos, "non-exhaustive case: some values are not matched")
	}
	// A bare `_` witness means the type has values that cannot be named —
	// Int, String, Char — so the useful thing to ask for is the catch-all.
	if witness[0] == "_" {
		if unbounded, ok := unboundedScalarName(subjectType); ok {
			return errorf(pos, "non-exhaustive case: %s has more values than a list of patterns can cover, so this case needs a catch-all branch (`_ ->` or a name)", unbounded)
		}
	}
	return errorf(pos, "non-exhaustive case: no branch matches %s", witness[0])
}

func unboundedScalarName(t Type) (string, bool) {
	tc, ok := t.(TCon)
	if !ok {
		return "", false
	}
	name, unbounded := unboundedScalars[tc.Name]
	return name, unbounded
}
