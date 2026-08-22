package typecheck

import (
	"fmt"

	"mar/internal/ast"
)

// Declaring the same name twice in one module used to get one of three
// answers, and none of them was a diagnostic.
//
// Two top-level VALUES panicked the compiler outright: orderValueDecls
// deduplicates by name while it walks, then writes the shortened result back
// by slot index, and runs off the end. That panic is not confined to the CLI —
// it takes the language server down with it, and it kills `mar dev` on hot
// reload, because both call the checker from a long-lived process.
//
// Two `type` declarations produced a silently wrong program. Both canonicalise
// to the same name, so the second overwrites the first in the type env while
// the FIRST type's constructors are still bound as values. The checker then
// proves a `case` total against the second type's constructors, and the case
// falls through at run time — a checked program reaching "no case branch
// matched", which is the exact class of bug ADR-0022 exists to make
// impossible.
//
// Everything else was quiet last-wins shadowing: the earlier declaration
// simply stopped existing, with no line of output to say so.
//
// The rule enforced here is the one a reader already assumes — within a
// module, a name is declared once — and it is enforced per NAMESPACE, because
// Mar legitimately spends one name twice when an annotation introduces a
// value:
//
//	greeting : String      the annotation namespace
//	greeting = "hi"        the value namespace
//
// Types and type aliases share a namespace with each other: both register
// under the same canonical name, and the custom type wins regardless of which
// came first. Constructors live in the VALUE namespace, which is why a record
// alias — whose name is also a constructor that builds the record — collides
// with one.
func checkDuplicateDecls(mod *ast.Module) error {
	// One map per namespace. The parser preserves source order in Decls, so
	// whatever is already in a map is genuinely the earlier declaration.
	annotations := map[string]declSite{}
	types := map[string]declSite{}
	values := map[string]declSite{}

	claim := func(ns map[string]declSite, name string, site declSite) error {
		if prior, dup := ns[name]; dup {
			return duplicateError(name, site, prior)
		}
		ns[name] = site
		return nil
	}

	for _, d := range mod.Decls {
		switch n := d.(type) {
		case *ast.AnnotationDecl:
			if err := claim(annotations, n.Name, declSite{n.Pos, "a type annotation", "type annotations"}); err != nil {
				return err
			}
			if err := checkRecordFields(n.Type); err != nil {
				return err
			}

		case *ast.ValueDecl:
			if err := claim(values, n.Name, declSite{n.Pos, "a definition", "definitions"}); err != nil {
				return err
			}

		case *ast.TypeAliasDecl:
			if err := claim(types, n.Name, declSite{n.Pos, "a type alias", "type aliases"}); err != nil {
				return err
			}
			if err := checkRecordFields(n.Body); err != nil {
				return err
			}
			// A closed record alias also claims its own name as a value:
			// `type alias Point = { x : Int }` introduces
			// `Point : Int -> Point`. An open row (`{ r | x : Int }`)
			// builds no constructor, so it claims nothing.
			if rec, ok := n.Body.(*ast.TypeRecord); ok && rec.Extends == "" {
				site := declSite{n.Pos, fmt.Sprintf("the record constructor that `type alias %s` introduces", n.Name), "record constructors"}
				if err := claim(values, n.Name, site); err != nil {
					return err
				}
			}

		case *ast.CustomTypeDecl:
			if err := claim(types, n.Name, declSite{n.Pos, "a custom type", "custom types"}); err != nil {
				return err
			}
			for _, c := range n.Constructors {
				site := declSite{c.Pos, fmt.Sprintf("a constructor of `%s`", n.Name), fmt.Sprintf("constructors of `%s`", n.Name)}
				if err := claim(values, c.Name, site); err != nil {
					return err
				}
				for _, arg := range c.Args {
					if err := checkRecordFields(arg); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// declSite is where a name was claimed and how to describe that claim in
// prose. `what` is written to drop into "here it is <what>", `plural` into
// "this module has two <plural> named ...".
type declSite struct {
	pos    ast.Pos
	what   string
	plural string
}

func duplicateError(name string, later, prior declSite) *InferError {
	// Point at the LATER declaration: that is the one the reader just wrote,
	// and the one the compiler was about to let quietly replace the first.
	if prior.what == later.what {
		return errorf(later.pos,
			"this module has two %s named `%s` — the first is on line %d.\n"+
				"  The second silently replaces the first, so what you wrote first can never run.\n"+
				"  Rename one of them, or delete the copy",
			prior.plural, name, prior.pos.Line)
	}
	return errorf(later.pos,
		"`%s` is already %s on line %d, and here it is %s.\n"+
			"  Both claim the same name in this module, so only the second one is reachable.\n"+
			"  Rename one of them",
		name, prior.what, prior.pos.Line, later.what)
}

// checkRecordFields walks a type expression for a record that lists the same
// field twice. `{ x : Int, x : String }` type-checked and then built a record
// carrying `x` twice, with the declared `x : Int` gone — the pattern side has
// rejected this since inferPattern was written, and the type side never did.
func checkRecordFields(t ast.TypeExpr) error {
	switch n := t.(type) {
	case *ast.TypeRecord:
		seen := map[string]ast.Pos{}
		for _, f := range n.Fields {
			if prior, dup := seen[f.Name]; dup {
				return errorf(f.Pos,
					"this record type lists the field `%s` twice — the first is on line %d.\n"+
						"  A record has one value per field, so the second declaration is the only one that survives.\n"+
						"  Delete one of them",
					f.Name, prior.Line)
			}
			seen[f.Name] = f.Pos
			if err := checkRecordFields(f.Type); err != nil {
				return err
			}
		}
	case *ast.TypeArrow:
		if err := checkRecordFields(n.From); err != nil {
			return err
		}
		return checkRecordFields(n.To)
	case *ast.TypeTuple:
		for _, m := range n.Members {
			if err := checkRecordFields(m); err != nil {
				return err
			}
		}
	case *ast.TypeCon:
		for _, a := range n.Args {
			if err := checkRecordFields(a); err != nil {
				return err
			}
		}
	}
	return nil
}

// The same rule one level down: within one group of patterns, a name is bound
// once. `f x x = x` and `case p of ( x, x ) ->` both type-checked and quietly
// kept whichever binding came last, so half of what the reader wrote had no
// effect — and in a `case`, `( x, x )` reads like a claim that the two halves
// are EQUAL, which is the one thing it did not mean. Elm rejects both.
//
// A group is whatever binds at once: all the parameters of a function or
// lambda, one branch's pattern, one let binding's pattern. Wildcards bind
// nothing and may repeat freely, which is the whole point of `_`.
func checkPatternNames(pats ...ast.Pattern) error {
	seen := map[string]ast.Pos{}
	var walk func(p ast.Pattern) error
	walk = func(p ast.Pattern) error {
		switch n := p.(type) {
		case *ast.PVar:
			if prior, dup := seen[n.Name]; dup {
				return errorf(n.Pos,
					"`%s` is bound twice here — the first is on line %d, column %d.\n"+
						"  Only the second binding survives, so the first name could never be read.\n"+
						"  Rename one of them, or use `_` for the one you do not need",
					n.Name, prior.Line, prior.Column)
			}
			seen[n.Name] = n.Pos
		case *ast.PCtor:
			for _, a := range n.Args {
				if err := walk(a); err != nil {
					return err
				}
			}
		case *ast.PTuple:
			for _, m := range n.Members {
				if err := walk(m); err != nil {
					return err
				}
			}
		case *ast.PList:
			for _, e := range n.Elements {
				if err := walk(e); err != nil {
					return err
				}
			}
		case *ast.PCons:
			if err := walk(n.Head); err != nil {
				return err
			}
			return walk(n.Tail)
		case *ast.PRecord:
			// A record pattern binds each field name it lists. A field
			// listed twice inside ONE record keeps the older, more
			// specific diagnostic that inferPattern has always produced;
			// what is new here is the same name bound elsewhere in the
			// group.
			local := map[string]bool{}
			for _, f := range n.Fields {
				if local[f] {
					return errorf(n.Pos, "record pattern lists field '%s' more than once", f)
				}
				local[f] = true
				if prior, dup := seen[f]; dup {
					return errorf(n.Pos,
						"`%s` is bound twice here — the first is on line %d, column %d.\n"+
							"  This record pattern binds each field it lists, and that name is already taken.\n"+
							"  Rename one of them",
						f, prior.Line, prior.Column)
				}
				seen[f] = n.Pos
			}
		}
		return nil
	}
	for _, p := range pats {
		if p == nil {
			continue
		}
		if err := walk(p); err != nil {
			return err
		}
	}
	return nil
}
