package typecheck

import (
	"fmt"
	"sort"
	"strings"

	"mar/internal/ast"
)

// CheckResult holds the outcome of type-checking a module.
type CheckResult struct {
	// ValueTypes maps top-level value name -> generalized scheme.
	ValueTypes map[string]Type
	// TypeDecls registered: alias name -> body, custom type name -> info.
	TypeAliases map[string]TypeAlias
	CustomTypes map[string]CustomType

	// ExprTypes maps every expression node the inferencer visited to
	// its post-substitution type. Populated when CheckModuleWith
	// enables Subst.EnableExprTracking — used by the shape lint
	// (shape_lint.go) to validate non-literal record values that the
	// polymorphic framework signatures don't constrain.
	ExprTypes map[ast.Expr]Type
}

// TypeAlias holds the registered form of a `type alias` declaration.
//
// `ParamIDs` is index-aligned with `Params`: `ParamIDs[i]` is the
// TVar ID that occurrences of `Params[i]` were rewritten to when
// `Body` was built. At alias-use time we substitute ParamIDs[i] →
// user's i-th type argument, which makes parametric aliases like
// `type alias Pair a b = (a, b)` resolve correctly. Without this,
// the resolver couldn't reconstruct the param-name → TVar-ID
// mapping after the fact and parametric alias inlining silently
// dropped substitutions.
type TypeAlias struct {
	Name     string
	Params   []string
	ParamIDs []int
	Body     Type
}

// CustomType holds the registered form of a `type X = A | B Int` declaration.
type CustomType struct {
	Name         string
	Params       []string
	Constructors map[string]CustomCtor
	CtorOrder    []string
	// Module is the qualified prefix under which a BUILTIN union's
	// constructors exist at runtime ("Canvas" → Canvas.Translate, "Keyboard"
	// → Keyboard.KeyW). It is the single source the ctor-registry generator
	// (internal/ctorgen) reads to emit the JS + Swift registrations, so a
	// builtin union is registered everywhere or nowhere — never half. Empty
	// means "not part of the generated registry": user-declared types, the
	// core types with native representations (Bool, Maybe, Result), and the
	// unions whose constructors are deliberately GLOBAL bare names (Order,
	// Method, Pointer, CanvasMode), which each runtime registers by hand.
	Module string
}

// CustomCtor describes one constructor: its arg types (in order) and the
// resulting type (TCon over Params).
type CustomCtor struct {
	Args   []Type
	Result Type
}

// bareBuiltinCtors maps each constructor Mar auto-exposes without a module
// prefix (Elm's default-import set) to the type it belongs to. A user type
// may not redefine one of these names: it would silently shadow the
// built-in in that module and resolve `Ok` / `True` / etc. to the wrong
// constructor. Elm rejects the same clash.
var bareBuiltinCtors = map[string]string{
	"Just": "Maybe", "Nothing": "Maybe",
	"Ok": "Result", "Err": "Result",
	"True": "Bool", "False": "Bool",
	"LT": "Order", "EQ": "Order", "GT": "Order",
	"GET": "Method", "POST": "Method", "PUT": "Method",
	"PATCH": "Method", "DELETE": "Method",
}

// reservedTypeNames are built-in type names a user `type` or `type alias`
// may not reuse: doing so shadows the built-in for the whole module and
// produces baffling unification errors (e.g. `Result` against `Result e a`).
// The scope is the language vocabulary, not the internal view-host phantom
// types (Section / Text / Input / ...), several of which are common domain
// words app authors legitimately model.
var reservedTypeNames = map[string]bool{
	"Int": true, "String": true, "Bool": true, "Char": true, "Decimal": true,
	"Time": true, "Duration": true, "Order": true,
	"List": true, "Dict": true, "Set": true,
	"Maybe": true, "Result": true,
	"Task": true, "Cmd": true, "Sub": true, "View": true, "Service": true, "Entity": true,
	"Auth": true, "Page": true, "Method": true,
}

// CheckModule runs the full type-check pass over a parsed module using the
// default (BaseEnv) value environment.
func CheckModule(mod *ast.Module) (*CheckResult, error) {
	return CheckModuleWith(mod, BaseEnv(), nil, nil)
}

// CheckModuleWith runs the full type-check pass over a parsed module using
// the given starting environment plus pre-known type aliases and custom
// types (typically imported from other modules).
//
// Order:
//  1. Pre-register type declarations (aliases + custom types) in a type env.
//  2. Register all custom-type constructors as values in the value env.
//  3. Pre-register annotations as monomorphic placeholders for recursion.
//  4. Infer each value declaration's body. Unify with annotation if present.
//  5. Generalize the result and register the final scheme.
//
// Returns a CheckResult plus the first error, if any.
func CheckModuleWith(
	mod *ast.Module,
	valueEnv *TypeEnv,
	importedAliases map[string]TypeAlias,
	importedCustoms map[string]CustomType,
) (*CheckResult, error) {
	res := &CheckResult{
		ValueTypes:  map[string]Type{},
		TypeAliases: map[string]TypeAlias{},
		CustomTypes: map[string]CustomType{},
	}

	// This module's own prefix. Types it declares are canonically
	// `Module.Name` (ADR 0027). The synthetic `__entry` module apphost
	// appends has no name; its types keep the bare form.
	modPrefix := strings.Join([]string(mod.Name), ".")

	tEnv := newTypeNameEnv()
	// Imported types arrive keyed CANONICALLY (`Shared.User`). Each is
	// registered under that key and ALSO bound to its bare tail, because
	// writing `User` after a plain `import Shared` is the established idiom
	// — 40 sites across the examples do it. Restricting the bare form to
	// `exposing` lists is a separate tightening this change does not make.
	for k, v := range importedAliases {
		tEnv.aliases[k] = v
		tEnv.bindBare(baseName(k), k)
	}
	for k, v := range importedCustoms {
		tEnv.customs[k] = v
		tEnv.bindBare(baseName(k), k)
		// Imported customs also need to be visible at the value-env
		// level for exhaustiveness checking to find them — under the
		// canonical key, which is what a TCon now carries.
		valueEnv.RegisterCustom(k, v)
	}

	// Process `import M exposing (foo, bar, ...)` clauses: for each
	// listed name, bind the bare form so the user can write `foo`
	// instead of `M.foo`. Items with `Open: true` (e.g. `Type(..)`)
	// also expose all constructors of the type.
	//
	// `exposing (..)` binds EVERYTHING the module exports: every
	// `M.name` already registered in the env (values, ctors, and for
	// builtin modules like UI the whole vocabulary) comes in bare.
	// Type names need no extra handling — imported aliases/customs
	// are already visible unqualified (see the loops above).
	for _, imp := range mod.Imports {
		if len(imp.Exposing.Items) == 0 && !imp.Exposing.All {
			continue
		}
		modName := strings.Join(imp.Module, ".")
		if imp.Exposing.All {
			for name, t := range valueEnv.ExportsOf(modName) {
				valueEnv = valueEnv.Bind(name, t)
			}
		}
		for _, item := range imp.Exposing.Items {
			qual := modName + "." + item.Name
			if t, ok := valueEnv.Lookup(qual); ok {
				valueEnv = valueEnv.Bind(item.Name, t)
			}
			// Type names: naming a type in an `exposing` list binds its bare
			// form. The loops above already bound every imported type's bare
			// tail, so this is only about being explicit — the lookup is by
			// the canonical key either way.
			if alias, ok := importedAliases[qual]; ok {
				tEnv.aliases[qual] = alias
				tEnv.bindBare(item.Name, qual)
			}
			if ct, ok := importedCustoms[qual]; ok {
				tEnv.customs[qual] = ct
				tEnv.bindBare(item.Name, qual)
				if item.Open {
					// Expose constructors as bare values too.
					for _, ctorName := range ct.CtorOrder {
						if t, ok := valueEnv.Lookup(modName + "." + ctorName); ok {
							valueEnv = valueEnv.Bind(ctorName, t)
						}
					}
				}
			}
		}
	}

	// Claim every name this module declares BEFORE any body is converted.
	// Dependency order handles the common case, but mutually recursive types
	// have no order that works: whichever is built first references the other
	// before it exists. Without this the forward reference fell through to an
	// opaque bare TCon while the declaration produced the canonical one, and
	// the two halves of the same type refused to unify.
	for _, d := range mod.Decls {
		switch n := d.(type) {
		case *ast.TypeAliasDecl:
			tEnv.declareBare(n.Name, canonicalIn(modPrefix, n.Name))
		case *ast.CustomTypeDecl:
			tEnv.declareBare(n.Name, canonicalIn(modPrefix, n.Name))
		}
	}

	// --- Pass 1: type declarations ---
	// Dependency order, not source order: an alias has to be registered
	// before a body that names it, or it silently becomes an opaque TCon.
	// See typeDeclsInDependencyOrder.
	for _, d := range typeDeclsInDependencyOrder(mod) {
		switch n := d.(type) {
		case *ast.TypeAliasDecl:
			if reservedTypeNames[n.Name] {
				return nil, errorf(n.Pos, "the name `%s` is reserved for a built-in type; rename your type alias (for example, `My%s`)", n.Name, n.Name)
			}
			// Build the param → TVar-ID scope first so we can both
			// thread it into the body conversion AND record the
			// per-position IDs on the alias for later substitution.
			// `convertTypeExpr` would have built this internally and
			// thrown the mapping away — by doing it here we keep
			// both halves.
			paramIDs := make([]int, len(n.Params))
			scope := map[string]int{}
			for i, p := range n.Params {
				v := FreshVar()
				paramIDs[i] = v.ID
				scope[p] = v.ID
			}
			var body Type
			var err error
			if len(n.Params) == 0 {
				body, err = convertTypeExprWithIDs(n.Body, tEnv, nil)
			} else {
				body, err = convertTypeExprWithIDs(n.Body, tEnv, scope)
			}
			if err != nil {
				return nil, errorf(n.Pos, "in type alias %s: %v", n.Name, err)
			}
			alias := TypeAlias{Name: n.Name, Params: n.Params, ParamIDs: paramIDs, Body: body}
			res.TypeAliases[n.Name] = alias
			aliasKey := canonicalIn(modPrefix, n.Name)
			tEnv.aliases[aliasKey] = alias
			tEnv.declareBare(n.Name, aliasKey)

			// A record type alias doubles as a positional constructor, the
			// same as Elm: `type alias Point = { x : Int, y : Int }` also
			// introduces `Point : Int -> Int -> Point`, building the record
			// from its fields in declaration order. Registering it as a
			// named value (rather than desugaring to an anonymous lambda
			// before inference) is what keeps the good diagnostics: a
			// misapplied `Point` reports "the Nth argument to `Point`",
			// pointing at the offending argument, instead of a generic
			// "cannot unify" anchored at the whole binding. The runtime
			// meaning is supplied by a post-typecheck desugar in the loader.
			if rec, ok := body.(TRecord); ok && rec.Tail == nil {
				fieldTypes := make([]Type, len(rec.Order))
				for i, fname := range rec.Order {
					fieldTypes[i] = rec.Fields[fname]
				}
				ctorType := buildCtorType(fieldTypes, body)
				if len(paramIDs) > 0 {
					ctorType = TForall{Vars: paramIDs, Body: ctorType}
				}
				valueEnv = valueEnv.Bind(n.Name, ctorType)
				res.ValueTypes[n.Name] = ctorType
			}

		case *ast.CustomTypeDecl:
			if reservedTypeNames[n.Name] {
				return nil, errorf(n.Pos, "the name `%s` is reserved for a built-in type; rename your type (for example, `My%s`)", n.Name, n.Name)
			}
			ct := CustomType{
				Name:         n.Name,
				Params:       n.Params,
				Constructors: map[string]CustomCtor{},
			}
			// The result type all constructors share: TCon{Name, [TVar(p1), TVar(p2), ...]}
			paramVars := make([]Type, len(n.Params))
			paramVarIDs := make(map[string]int, len(n.Params))
			for i, p := range n.Params {
				v := FreshVar()
				paramVars[i] = v
				paramVarIDs[p] = v.ID
			}
			// The canonical name IS the identity (ADR 0027). Two modules that
			// both declare `Color` used to produce one TCon{"Color"}, which
			// unified with itself across the boundary and let a `case` the
			// checker had proven total fall through at runtime.
			ctKey := canonicalIn(modPrefix, n.Name)
			resultType := TCon{Name: ctKey, Args: paramVars}

			// Register the type itself in the env so its own ctors can reference it.
			tEnv.customs[ctKey] = ct
			tEnv.declareBare(n.Name, ctKey)
			tEnv.paramScopes = append(tEnv.paramScopes, paramVarIDs)

			for _, c := range n.Constructors {
				if owner, ok := bareBuiltinCtors[c.Name]; ok {
					return nil, errorf(c.Pos, "the name `%s` is reserved for a built-in constructor (from %s); rename it", c.Name, owner)
				}
				ctorArgs := make([]Type, len(c.Args))
				for i, argExpr := range c.Args {
					at, err := convertTypeExprWithIDs(argExpr, tEnv, paramVarIDs)
					if err != nil {
						return nil, errorf(c.Pos, "in constructor %s: %v", c.Name, err)
					}
					ctorArgs[i] = at
				}
				ct.Constructors[c.Name] = CustomCtor{Args: ctorArgs, Result: resultType}
				ct.CtorOrder = append(ct.CtorOrder, c.Name)

				// Register constructor in value env.
				// Type: forall <params>. arg1 -> arg2 -> ... -> Result
				ctorType := buildCtorType(ctorArgs, resultType)
				if len(n.Params) > 0 {
					ids := make([]int, 0, len(paramVarIDs))
					for _, id := range paramVarIDs {
						ids = append(ids, id)
					}
					ctorType = TForall{Vars: ids, Body: ctorType}
				}
				valueEnv = valueEnv.Bind(c.Name, ctorType)
				// Also expose the ctor in res.ValueTypes so the
				// project loader can register a qualified
				// `Module.Ctor` form for downstream imports
				// (`import M exposing (T(..))` and module-qualified
				// references both rely on this).
				res.ValueTypes[c.Name] = ctorType
			}

			tEnv.paramScopes = tEnv.paramScopes[:len(tEnv.paramScopes)-1]
			tEnv.customs[ctKey] = ct
			res.CustomTypes[n.Name] = ct
			// Make the custom-type registration visible at the value-env
			// level too — exhaustiveness checking in inferCase reads it by
			// the TCon's name, which is now the canonical one.
			valueEnv.RegisterCustom(ctKey, ct)
		}
	}

	// --- Pass 2: annotations as polymorphic schemes ---
	// In annotations, every named type var should refer to the SAME fresh
	// var across the whole type (so `Box a -> a` ties the two `a`s together)
	// AND those vars are universally quantified (an annotation `id : a -> a`
	// declares a polymorphic value). We collect names, assign one ID each,
	// convert the body, and wrap in TForall.
	annotations := map[string]Type{}
	annotationBodies := map[string]Type{}
	annotationVars := map[string][]int{}
	for _, d := range mod.Decls {
		if a, ok := d.(*ast.AnnotationDecl); ok {
			scope := buildAnnotationScope(a.Type)
			t, err := convertTypeExprWithIDs(a.Type, tEnv, scope)
			if err != nil {
				return nil, errorf(a.Pos, "in annotation %s: %v", a.Name, err)
			}
			ids := make([]int, 0, len(scope))
			for _, id := range scope {
				ids = append(ids, id)
			}
			annotationBodies[a.Name] = t
			annotationVars[a.Name] = ids
			if len(ids) > 0 {
				annotations[a.Name] = TForall{Vars: ids, Body: t}
			} else {
				annotations[a.Name] = t
			}
		}
	}
	// Pre-bind every value name (even those without annotation) to a fresh var
	// so that recursive references resolve.
	//
	// For annotated values, bind the SCHEME (TForall) so recursive references
	// instantiate it (giving polymorphism). For unannotated values, bind a
	// fresh var that will be unified during inference.
	for _, d := range mod.Decls {
		if v, ok := d.(*ast.ValueDecl); ok {
			if t, has := annotations[v.Name]; has {
				valueEnv = valueEnv.Bind(v.Name, t)
			} else {
				valueEnv = valueEnv.Bind(v.Name, FreshVar())
			}
		}
	}

	// --- Pass 3: infer each value decl ---
	s := NewSubst()
	// Enable per-expression type recording so the shape lint
	// (boundary-shape checks downstream of typecheck) can look up
	// the inferred type of non-literal record values like
	// `body = input.body`. The map is extracted into the
	// CheckResult below.
	s.EnableExprTracking()
	for _, d := range mod.Decls {
		v, ok := d.(*ast.ValueDecl)
		if !ok {
			continue
		}
		// If params, build a lambda equivalent.
		body := v.Body
		if len(v.Params) > 0 {
			body = &ast.ELambda{Pos: v.Pos, Params: v.Params, Body: body}
		}
		// Bidirectional coercion for typed paths: when the annotation
		// is `Path r` and the body is a bare String literal, parse the
		// literal at compile time, derive the params row from the
		// `{name:Type}` segments, and unify against the annotation's
		// row. The runtime keeps the value as a String (no AST rewrite
		// needed) — page builders + linkTo / Nav.pushTo re-parse it
		// when they need the segments.
		//
		// Only applies when an annotation is present. Without one we
		// can't know the expected type, so a bare String literal stays
		// String — the user must declare `notesDetail : Path { id : Int }`.
		if annotBody, has := annotationBodies[v.Name]; has {
			if str, ok := body.(*ast.EString); ok {
				if pathRow, isPath := pathRowOfAnnot(s.Apply(annotBody)); isPath {
					row, err := elaboratePathLiteral(str.Value, tEnv)
					if err != nil {
						return nil, errorf(str.Pos, "%s: %v", v.Name, err)
					}
					if err := Unify(pathRow, row, s); err != nil {
						return nil, errorf(str.Pos, "%s: path %q does not match annotation: %v", v.Name, str.Value, err)
					}
					continue
				}
			}
		}
		tInferred, err := Infer(body, valueEnv, s)
		if err != nil {
			return nil, err
		}
		// If annotation, unify with the annotation BODY (instantiated).
		// We use the body, not the TForall, because TForall isn't directly
		// unifiable. The body's vars are the named ones, fresh per-decl.
		if annot, has := annotationBodies[v.Name]; has {
			if err := Unify(tInferred, annot, s); err != nil {
				return nil, errorf(v.Pos, "%s: %v", v.Name, err)
			}
			// Service.declare's path literal carries typed `{name:Type}`
			// params that must name fields of the request type. Validate
			// them here, where the annotation has fixed req/resp.
			if err := validateServicePath(v, s.Apply(annot), tEnv, s); err != nil {
				return nil, err
			}
		} else {
			// No annotation: unify with the placeholder so recursive references resolve.
			if existing, ok := valueEnv.Lookup(v.Name); ok {
				if err := Unify(existing, tInferred, s); err != nil {
					return nil, errorf(v.Pos, "%s: %v", v.Name, err)
				}
			}
		}
	}

	// --- Pass 4: generalize and store ---
	for _, d := range mod.Decls {
		v, ok := d.(*ast.ValueDecl)
		if !ok {
			continue
		}
		if _, has := annotations[v.Name]; has {
			// Use the explicitly-given scheme; rebuild from scratch to get
			// fresh display IDs.
			body := s.Apply(annotationBodies[v.Name])
			ids := annotationVars[v.Name]
			if len(ids) > 0 {
				res.ValueTypes[v.Name] = TForall{Vars: ids, Body: body}
			} else {
				res.ValueTypes[v.Name] = body
			}
		} else {
			t, _ := valueEnv.Lookup(v.Name)
			t = s.Apply(t)
			t = Generalize(BaseEnv(), t, s)
			res.ValueTypes[v.Name] = t
		}
	}

	// Reject illegal dependency cycles (non-function values that
	// reference themselves directly or transitively). The runtime would
	// otherwise eagerly evaluate them and hit a placeholder, producing
	// a confusing error like "+: unsupported types".
	if err := checkValueCycles(mod); err != nil {
		return nil, err
	}

	// With cycles ruled out, put the value declarations in an order the
	// runtimes can evaluate straight through. Source order stops being
	// load-bearing: a value may read a value declared below it.
	orderValueDecls(mod)

	// Snapshot the post-substitution expression types so consumers
	// (shape lint, future LSP hover) get concrete shapes instead of
	// raw type variables. The substitution `s` would otherwise be
	// dropped when this function returns.
	res.ExprTypes = s.ExtractExprTypes()

	// Elaboration: an integer literal whose `number` variable resolved to
	// Decimal becomes a Decimal at runtime. This is where the compiler
	// stops keeping what it learned to itself — types are erased at the
	// runtime boundary, so a decision the checker made has to be written
	// back into the tree to survive. Doing it on the node means the
	// serializer carries it to the JS and Swift runtimes for free.
	Elaborate(res.ExprTypes)

	// Everything the runtimes cannot re-derive is now written into the
	// tree. Mark it, so the evaluating and serializing sides can refuse a
	// module that never came through here.
	mod.MarkElaborated()

	return res, nil
}

// Elaborate writes the checker's numeric decisions back onto the tree, in the
// two places where a `number` can resolve to Decimal and the runtime would
// otherwise have no way to know:
//
//   - an integer literal whose context typed it as Decimal, and
//   - a reference to List.sum / List.product instantiated at Decimal, which
//     picks the implementation whose empty-list zero is a Decimal.
//
// Anything still a variable — a literal in a genuinely polymorphic position,
// or one nobody constrained — is left alone: it stays `number` in the type and
// Int in the value, which is the behavior that existed before this pass.
//
// Exported because the module checker is not the only caller: the REPL runs
// Infer directly, and a node it typed as Decimal has to be elaborated before
// Eval sees it, or the type-checker and the runtime disagree — a disagreement
// that shows up as `+: expected Int` on `1 + 1.50`.
func Elaborate(exprTypes map[ast.Expr]Type) {
	for e, t := range exprTypes {
		switch n := e.(type) {
		case *ast.EInt:
			if isDecimalType(t) {
				n.AsDecimal = true
			}
		case *ast.EQualified:
			n.Impl = decimalImplFor(joinName(n.Module, n.Name), t)
		case *ast.EVar:
			n.Impl = decimalImplFor(n.Name, t)
		}
	}
}

func isDecimalType(t Type) bool {
	con, ok := t.(TCon)
	return ok && con.Name == TDecimal.Name && len(con.Args) == 0
}

// decimalImplFor returns the Decimal-specific native implementation for a
// reference to name, or "" when the reference is anything else or its
// instantiation is not Decimal. The returned names are registered in the
// runtimes but deliberately not in the typecheck env, which is what keeps
// them unwritable from Mar source: the language keeps one name, List.sum.
func decimalImplFor(name string, t Type) string {
	var impl string
	switch name {
	case "List.sum", "listSum":
		impl = "listSumDecimal"
	case "List.product", "listProduct":
		impl = "listProductDecimal"
	default:
		return ""
	}
	// The reference's own type is the instantiated arrow, List a -> a.
	arrow, ok := t.(TArrow)
	if !ok || !isDecimalType(arrow.To) {
		return ""
	}
	return impl
}

// --- Type name environment for resolving type expressions ---

// A type is identified by `Module.Name`, and the bare name is an explicit
// alias for it — the same shape the VALUE namespace has always had (ADR 0027).
//
// `aliases` and `customs` are keyed CANONICALLY: `Frontend.Global.Model`, not
// `Model`. Builtins have no user module and keep their bare key (`Device`), or
// their own dotted one when they already carry a qualifier (`Service.Error`).
//
// `bare` is the only way an unqualified name resolves, and it is built per
// module from that module's own declarations plus its `exposing` imports —
// never from a shared pool. When two candidates claim one bare name the entry
// keeps BOTH: having an ambiguity in scope is fine, referencing it is not, and
// the error at the use site can name them. That distinction is what makes this
// change non-breaking — see the ADR for the measurement.
type typeNameEnv struct {
	aliases map[string]TypeAlias
	customs map[string]CustomType
	bare    map[string][]string // bare name -> canonical keys claiming it
	// paramScopes: stack of currently-in-scope type parameter names -> var IDs
	paramScopes []map[string]int
}

func newTypeNameEnv() *typeNameEnv {
	e := &typeNameEnv{
		aliases: builtinTypeAliases(),
		customs: map[string]CustomType{},
		bare:    map[string][]string{},
	}
	for name := range e.aliases {
		e.bindBare(name, name)
	}
	return e
}

// bindBare records that `bare` may resolve to `canonical`. Re-binding the same
// canonical is a no-op, so importing a module twice (or naming a type in an
// `exposing` list that is also visible whole) never manufactures ambiguity.
func (e *typeNameEnv) bindBare(bare, canonical string) {
	for _, c := range e.bare[bare] {
		if c == canonical {
			return
		}
	}
	e.bare[bare] = append(e.bare[bare], canonical)
}

// declareBare binds a name the module declares ITSELF, which shadows every
// import of that name rather than competing with it.
//
// Ordinary lexical scoping, and here it is load-bearing: the framework's own
// idiom gives each page a `Model` and a `Msg`, and a page that reads shared
// state imports a module that has them too. Treating those as rival candidates
// would make `Msg` ambiguous inside the page that declared it, which is absurd
// — the nearest binding is obviously the one meant. Elm rejects the clash
// instead, but Elm pages do not import each other's modules the way
// Page.withShared makes normal (ADR 0026).
func (e *typeNameEnv) declareBare(bare, canonical string) {
	e.bare[bare] = []string{canonical}
}

// known reports whether a canonical key names a type this module can see.
func (e *typeNameEnv) known(canonical string) bool {
	if _, ok := e.aliases[canonical]; ok {
		return true
	}
	_, ok := e.customs[canonical]
	return ok
}

// resolveTypeName maps a written type name to its canonical key.
//
// Qualified (`A.T`): resolves to `A.T` or fails. It never falls back to the
// bare name, which is the whole defect this closes — the qualifier used to be
// discarded, so `A.T` could land on B's `T`, and a typo'd `A.Tpyo` became an
// opaque type instead of an error.
//
// Unqualified: only what this module declared or imported by name. `found` is
// false for anything else, and the caller falls through to the primitives and
// the opaque-TCon case that builtin nominal types (View, Page, Sub) rely on.
func (e *typeNameEnv) resolveTypeName(module []string, name string) (canonical string, found bool, err error) {
	if len(module) > 0 {
		full := strings.Join(module, ".") + "." + name
		if qualifiedBuiltinTypes[full] {
			return full, true, nil
		}
		if e.known(full) {
			return full, true, nil
		}
		return "", false, fmt.Errorf("`%s` has no type `%s`%s",
			strings.Join(module, "."), name, e.didYouMean(strings.Join(module, "."), name))
	}
	cands := e.bare[name]
	switch len(cands) {
	case 0:
		return "", false, nil
	case 1:
		// Returned even when the body is not registered yet: mutually
		// recursive types reference each other before both are built, and
		// the forward reference has to land on the SAME identity or the
		// two halves never unify.
		return cands[0], true, nil
	default:
		sorted := append([]string(nil), cands...)
		sort.Strings(sorted)
		return "", false, fmt.Errorf("`%s` is ambiguous here: it could be %s.\n"+
			"  Qualify it (write the module name), or import only one of them",
			name, humanList(sorted))
	}
}

// didYouMean lists what the module DOES offer, when it offers anything. The
// old behaviour turned a wrong qualified name into an opaque type, so the
// complaint surfaced wherever that type was first used instead of here.
func (e *typeNameEnv) didYouMean(module, _ string) string {
	prefix := module + "."
	var have []string
	for k := range e.aliases {
		if strings.HasPrefix(k, prefix) {
			have = append(have, strings.TrimPrefix(k, prefix))
		}
	}
	for k := range e.customs {
		if strings.HasPrefix(k, prefix) {
			have = append(have, strings.TrimPrefix(k, prefix))
		}
	}
	if len(have) == 0 {
		return ""
	}
	sort.Strings(have)
	return ". It has " + humanList(have)
}

// baseName is the tail of a canonical key: `Shared.User` -> `User`. Builtin
// names without a dot are their own tail.
func baseName(canonical string) string {
	if i := strings.LastIndex(canonical, "."); i >= 0 {
		return canonical[i+1:]
	}
	return canonical
}

// canonicalIn prefixes a locally-declared type name with its module. The
// synthetic `__entry` module has no name, so its types stay bare.
func canonicalIn(modPrefix, name string) string {
	if modPrefix == "" {
		return name
	}
	return modPrefix + "." + name
}

func humanList(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return "`" + xs[0] + "`"
	case 2:
		return "`" + xs[0] + "` or `" + xs[1] + "`"
	default:
		quoted := make([]string, len(xs))
		for i, x := range xs {
			quoted[i] = "`" + x + "`"
		}
		return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
	}
}

// builtinTypeAliases seeds the framework-provided type aliases every module
// starts with. Currently just `Device` (docs/proposals/device.md) → the closed
// record `Device.watch` delivers, so an app can annotate `dev : Device` in its
// model without re-declaring the seven fields. Seeded as a type name only (not
// a positional constructor — apps receive the record from the runtime, they
// never build one). A user's own `type alias Device`, should they write one,
// simply overwrites this entry: harmless shadowing, no duplicate error.
func builtinTypeAliases() map[string]TypeAlias {
	return map[string]TypeAlias{
		"Device": {Name: "Device", Params: nil, ParamIDs: nil, Body: TDeviceRecord()},
	}
}

// lookupParam searches the param scope stack for a type variable matching name.
func (e *typeNameEnv) lookupParam(name string) (int, bool) {
	for i := len(e.paramScopes) - 1; i >= 0; i-- {
		if id, ok := e.paramScopes[i][name]; ok {
			return id, true
		}
	}
	return 0, false
}

// convertTypeExprWithIDs converts an AST type expression to a Type,
// using tEnv for looking up named types and an optional paramIDs map
// as a scope of named TVars (each TypeVar named `p` in the AST
// resolves to TVar{ID: paramIDs[p]}). Callers that need to track
// which TVar ID corresponds to which param name allocate the map
// themselves (alias declarations, custom-type declarations); callers
// without named params pass nil.
// qualifiedBuiltinTypes are stdlib types whose canonical name carries a
// dot. The parser splits a qualified upper name into Module + base name, so
// these would otherwise lose their qualifier and collide on the bare tail.
var qualifiedBuiltinTypes = map[string]bool{
	"Service.Error":       true,
	"Decimal.Rounding":    true,
	"Decimal.Division":    true,
	"Auth.RequestOutcome": true,
	"Auth.VerifyOutcome":  true,
	"Random.Generator":    true,
	"Random.Seed":         true,
	"Keyboard.Key":        true,
	"Gamepad.Button":      true,
	"Sound.Wave":          true,
	"App.Shared":          true,
}

func convertTypeExprWithIDs(te ast.TypeExpr, tEnv *typeNameEnv, paramIDs map[string]int) (Type, error) {
	if paramIDs != nil {
		tEnv.paramScopes = append(tEnv.paramScopes, paramIDs)
		defer func() { tEnv.paramScopes = tEnv.paramScopes[:len(tEnv.paramScopes)-1] }()
	}

	switch t := te.(type) {
	case *ast.TypeVar:
		if id, ok := tEnv.lookupParam(t.Name); ok {
			return TVar{ID: id}, nil
		}
		// Reached only from type DECLARATIONS (aliases and custom
		// types): annotations pre-collect every variable name into
		// the scope first (buildAnnotationScope), so an unbound name
		// here means the declaration uses a variable it never
		// declared. The silent FreshVar this used to produce hid
		// typos and the variable-vs-concrete-type confusion; Elm
		// rejects it ("unbound type variable") and so do we.
		return nil, fmt.Errorf("unbound type variable `%s`: declare it as a parameter of the type (e.g. `type T %s = ...`) or use a concrete type", t.Name, t.Name)

	case *ast.TypeCon:
		args := make([]Type, len(t.Args))
		for i, a := range t.Args {
			at, err := convertTypeExprWithIDs(a, tEnv, nil)
			if err != nil {
				return nil, err
			}
			args[i] = at
		}
		// A type is identified by its module (ADR 0027). resolveTypeName
		// turns what was written into the canonical key, or says why it
		// cannot: a qualified name the module does not export, or a bare
		// name two modules both claim.
		canonical, found, err := tEnv.resolveTypeName([]string(t.Module), t.Name)
		if err != nil {
			return nil, err
		}
		if found {
			// Stdlib types whose canonical name carries a dot
			// (Service.Error) are nominal under that whole name.
			if qualifiedBuiltinTypes[canonical] {
				return TCon{Name: canonical, Args: args}, nil
			}
			if ct, ok := tEnv.customs[canonical]; ok {
				// The canonical name IS the identity. Two modules that
				// both declare `Color` used to produce one TCon{"Color"}
				// and unify with each other, which let a `case` the
				// checker had proven total fall through at runtime.
				_ = ct
				return TCon{Name: canonical, Args: args}, nil
			}
		}
		// Resolve aliases (substitute params). ParamIDs[i] is the
		// TVar ID that occurrences of Params[i] were rewritten to
		// when the alias was registered, so we can map directly
		// without walking the body to discover IDs.
		if alias, ok := tEnv.aliases[canonical]; found && ok {
			if len(args) != len(alias.Params) {
				return nil, fmt.Errorf("type alias %s expects %d arguments, got %d", t.Name, len(alias.Params), len(args))
			}
			if len(alias.ParamIDs) == 0 {
				// Non-parametric alias — nothing to substitute,
				// just return the body as-is.
				return alias.Body, nil
			}
			subst := make(map[int]Type, len(alias.ParamIDs))
			for i, id := range alias.ParamIDs {
				subst[id] = args[i]
			}
			return substituteVars(alias.Body, subst), nil
		}
		// Built-in primitives
		switch t.Name {
		case "Int":
			return TInt, nil
		case "Decimal":
			return TDecimal, nil
		case "String":
			return TString, nil
		case "Bool":
			return TBool, nil
		case "Char":
			return TChar, nil
		}
		// Opaque nominal type. `canonical` when the scope knows the name
		// (a local type whose body has not been built yet), the written
		// name otherwise — builtin nominals like View / Page / Sub.
		if found {
			return TCon{Name: canonical, Args: args}, nil
		}
		return TCon{Name: t.Name, Args: args}, nil

	case *ast.TypeArrow:
		from, err := convertTypeExprWithIDs(t.From, tEnv, nil)
		if err != nil {
			return nil, err
		}
		to, err := convertTypeExprWithIDs(t.To, tEnv, nil)
		if err != nil {
			return nil, err
		}
		return TArrow{From: from, To: to}, nil

	case *ast.TypeRecord:
		fields := make(map[string]Type, len(t.Fields))
		order := make([]string, 0, len(t.Fields))
		for _, f := range t.Fields {
			ft, err := convertTypeExprWithIDs(f.Type, tEnv, nil)
			if err != nil {
				return nil, err
			}
			fields[f.Name] = ft
			order = append(order, f.Name)
		}
		var tail Type
		if t.Extends != "" {
			if id, ok := tEnv.lookupParam(t.Extends); ok {
				tail = TVar{ID: id}
			} else {
				// Same rule as the TypeVar case above: only
				// declarations can reach this unbound, and an unbound
				// extension row is a typo or an undeclared parameter.
				return nil, fmt.Errorf("unbound type variable `%s` in record extension: declare it as a parameter of the type", t.Extends)
			}
		}
		return TRecord{Fields: fields, Order: order, Tail: tail}, nil

	case *ast.TypeUnit:
		return TUnit{}, nil

	case *ast.TypeTuple:
		members := make([]Type, len(t.Members))
		for i, m := range t.Members {
			mt, err := convertTypeExprWithIDs(m, tEnv, nil)
			if err != nil {
				return nil, err
			}
			members[i] = mt
		}
		return TTuple{Members: members}, nil
	}
	return nil, fmt.Errorf("unsupported type expression: %T", te)
}

// buildAnnotationScope walks an AST type expression collecting every named
// type variable and assigning one fresh var ID per name. Used so that the
// same name (`a`) becomes the same TVar across an annotation.
func buildAnnotationScope(te ast.TypeExpr) map[string]int {
	scope := map[string]int{}
	collectTypeVarNames(te, scope)
	return scope
}

func collectTypeVarNames(te ast.TypeExpr, out map[string]int) {
	switch t := te.(type) {
	case *ast.TypeVar:
		if _, exists := out[t.Name]; !exists {
			out[t.Name] = FreshVar().ID
		}
	case *ast.TypeCon:
		for _, a := range t.Args {
			collectTypeVarNames(a, out)
		}
	case *ast.TypeArrow:
		collectTypeVarNames(t.From, out)
		collectTypeVarNames(t.To, out)
	case *ast.TypeRecord:
		if t.Extends != "" {
			if _, exists := out[t.Extends]; !exists {
				out[t.Extends] = FreshVar().ID
			}
		}
		for _, f := range t.Fields {
			collectTypeVarNames(f.Type, out)
		}
	case *ast.TypeTuple:
		for _, m := range t.Members {
			collectTypeVarNames(m, out)
		}
	}
}

// buildCtorType constructs a curried arrow type: arg1 -> arg2 -> ... -> result.
func buildCtorType(args []Type, result Type) Type {
	t := result
	for i := len(args) - 1; i >= 0; i-- {
		t = TArrow{From: args[i], To: t}
	}
	return t
}
