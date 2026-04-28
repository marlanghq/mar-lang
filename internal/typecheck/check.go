package typecheck

import (
	"fmt"

	"mar/internal/ast"
)

// CheckResult holds the outcome of type-checking a module.
type CheckResult struct {
	// ValueTypes maps top-level value name -> generalized scheme.
	ValueTypes map[string]Type
	// TypeDecls registered: alias name -> body, custom type name -> info.
	TypeAliases map[string]TypeAlias
	CustomTypes map[string]CustomType
}

// TypeAlias holds the registered form of a `type alias` declaration.
type TypeAlias struct {
	Name   string
	Params []string
	Body   Type
}

// CustomType holds the registered form of a `type X = A | B Int` declaration.
type CustomType struct {
	Name         string
	Params       []string
	Constructors map[string]CustomCtor
	CtorOrder    []string
}

// CustomCtor describes one constructor: its arg types (in order) and the
// resulting type (TCon over Params).
type CustomCtor struct {
	Args   []Type
	Result Type
}

// CheckModule runs the full type-check pass over a parsed module.
//
// Order:
//  1. Pre-register type declarations (aliases + custom types) in a type env.
//  2. Register all custom-type constructors as values in the value env.
//  3. Pre-register annotations as monomorphic placeholders for recursion.
//  4. Infer each value declaration's body. Unify with annotation if present.
//  5. Generalize the result and register the final scheme.
//
// Returns a CheckResult plus the first error, if any.
func CheckModule(mod *ast.Module) (*CheckResult, error) {
	res := &CheckResult{
		ValueTypes:  map[string]Type{},
		TypeAliases: map[string]TypeAlias{},
		CustomTypes: map[string]CustomType{},
	}

	tEnv := newTypeNameEnv()
	valueEnv := BaseEnv()

	// --- Pass 1: type declarations ---
	for _, d := range mod.Decls {
		switch n := d.(type) {
		case *ast.TypeAliasDecl:
			body, err := convertTypeExpr(n.Body, tEnv, n.Params)
			if err != nil {
				return nil, errorf(n.Pos, "in type alias %s: %v", n.Name, err)
			}
			alias := TypeAlias{Name: n.Name, Params: n.Params, Body: body}
			res.TypeAliases[n.Name] = alias
			tEnv.aliases[n.Name] = alias

		case *ast.CustomTypeDecl:
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
			resultType := TCon{Name: n.Name, Args: paramVars}

			// Register the type itself in the env so its own ctors can reference it.
			tEnv.customs[n.Name] = ct
			tEnv.paramScopes = append(tEnv.paramScopes, paramVarIDs)

			for _, c := range n.Constructors {
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
			}

			tEnv.paramScopes = tEnv.paramScopes[:len(tEnv.paramScopes)-1]
			tEnv.customs[n.Name] = ct
			res.CustomTypes[n.Name] = ct
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
		tInferred, err := Infer(body, valueEnv, s)
		if err != nil {
			return nil, err
		}
		// If annotation, unify with the annotation BODY (instantiated).
		// We use the body, not the TForall, because TForall isn't directly
		// unifiable. The body's vars are the named ones, fresh per-decl.
		if body, has := annotationBodies[v.Name]; has {
			if err := Unify(tInferred, body, s); err != nil {
				return nil, errorf(v.Pos, "%s: %v", v.Name, err)
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

	return res, nil
}

// --- Type name environment for resolving type expressions ---

type typeNameEnv struct {
	aliases  map[string]TypeAlias
	customs  map[string]CustomType
	// paramScopes: stack of currently-in-scope type parameter names -> var IDs
	paramScopes []map[string]int
}

func newTypeNameEnv() *typeNameEnv {
	return &typeNameEnv{
		aliases: map[string]TypeAlias{},
		customs: map[string]CustomType{},
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

// convertTypeExpr converts an AST type expression to a Type, using tEnv for
// looking up named types and the optional `params` slice as a fresh scope of
// universally-quantified type variables.
func convertTypeExpr(te ast.TypeExpr, tEnv *typeNameEnv, params []string) (Type, error) {
	if len(params) == 0 {
		return convertTypeExprWithIDs(te, tEnv, nil)
	}
	ids := make(map[string]int, len(params))
	for _, p := range params {
		ids[p] = FreshVar().ID
	}
	return convertTypeExprWithIDs(te, tEnv, ids)
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
		// Free type var = fresh
		return FreshVar(), nil

	case *ast.TypeCon:
		if len(t.Module) > 0 {
			return nil, fmt.Errorf("qualified type names not yet supported: %v.%s", t.Module, t.Name)
		}
		args := make([]Type, len(t.Args))
		for i, a := range t.Args {
			at, err := convertTypeExprWithIDs(a, tEnv, nil)
			if err != nil {
				return nil, err
			}
			args[i] = at
		}
		// Resolve aliases (substitute params)
		if alias, ok := tEnv.aliases[t.Name]; ok {
			if len(args) != len(alias.Params) {
				return nil, fmt.Errorf("type alias %s expects %d arguments, got %d", t.Name, len(alias.Params), len(args))
			}
			subst := map[int]Type{}
			for i, p := range alias.Params {
				// Find the parameter's TVar in alias.Body and substitute.
				if id, ok := paramIDForName(alias.Body, p); ok {
					subst[id] = args[i]
				}
			}
			return substituteVars(alias.Body, subst), nil
		}
		// Built-in primitives
		switch t.Name {
		case "Int":
			return TInt, nil
		case "Float":
			return TFloat, nil
		case "String":
			return TString, nil
		case "Bool":
			return TBool, nil
		case "Char":
			return TChar, nil
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
				tail = FreshVar()
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

// paramIDForName finds the TVar ID inside body that corresponds to type param `name`.
// This is a heuristic: alias body was built with a fresh paramID per param name.
// Since we built it ourselves, the binding is in the alias body somewhere as the
// only TVar with that ID. For now, walk the body looking for a TVar — since
// we used fresh IDs per occurrence, this won't generally work for multiple
// params; we record param IDs separately.
//
// TODO: store paramIDs alongside TypeAlias so we don't need this.
func paramIDForName(body Type, name string) (int, bool) {
	// Placeholder: this function is currently a stub; real implementation
	// needs us to record param IDs at alias creation time. For MVP we leave
	// alias inlining incomplete.
	return 0, false
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
