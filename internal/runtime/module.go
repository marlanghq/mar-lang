package runtime

import (
	"fmt"
	"strings"

	"mar/internal/ast"
)

// Module is a loaded, evaluatable module.
type Module struct {
	Name string
	Env  *Env // env populated with all top-level decls
}

// LoadModule evaluates all top-level value declarations in mod and returns
// a runtime Module ready for queries.
//
// Two-pass loading enables mutual recursion:
//  1. Pre-bind every value name to a sentinel placeholder.
//  2. Evaluate each value declaration's body. For lambdas, the resulting
//     closure captures env (which contains placeholders); when called,
//     those placeholders will have been overwritten with the real values.
//  3. Replace the placeholder with the real value.
//
// Custom-type constructors are registered as VCtor (for nullary) or VFn
// (for those with payload).
func LoadModule(mod *ast.Module) (*Module, error) {
	env := BaseEnv()

	// Pass 1: register custom-type constructors. Also opportunistically
	// register zero-arg-ctor types in the path-pattern enum registry —
	// that's how Page.dynamic's `{role:Role}` syntax learns the URL ↔
	// ctor mapping. Types with payload ctors are silently skipped
	// (RegisterEnumType filters internally); the typechecker has
	// already rejected those for path use, so we only need to populate
	// the eligible ones.
	for _, d := range mod.Decls {
		ct, ok := d.(*ast.CustomTypeDecl)
		if !ok {
			continue
		}
		ctorNames := make([]string, 0, len(ct.Constructors))
		ctorArities := map[string]int{}
		for _, c := range ct.Constructors {
			env.Define(c.Name, makeCtorValue(c.Name, len(c.Args)))
			ctorNames = append(ctorNames, c.Name)
			ctorArities[c.Name] = len(c.Args)
		}
		RegisterEnumType(ct.Name, ctorNames, ctorArities)
	}

	// Pass 2: pre-bind value names to placeholders so lambdas can capture
	// references that become valid after Pass 3.
	for _, d := range mod.Decls {
		v, ok := d.(*ast.ValueDecl)
		if !ok {
			continue
		}
		env.Define(v.Name, VUnit{}) // placeholder
	}

	// Pass 3: evaluate each value declaration.
	for _, d := range mod.Decls {
		v, ok := d.(*ast.ValueDecl)
		if !ok {
			continue
		}
		body := v.Body
		if len(v.Params) > 0 {
			// Wrap in lambda
			body = &ast.ELambda{Pos: v.Pos, Params: v.Params, Body: body}
		}
		val, err := Eval(body, env)
		if err != nil {
			return nil, err
		}
		env.Define(v.Name, val)
	}

	name := "(unnamed)"
	if len(mod.Name) > 0 {
		name = joinModuleName(mod.Name)
	}
	return &Module{Name: name, Env: env}, nil
}

func joinModuleName(parts []string) string {
	return strings.Join(parts, ".")
}

// makeCtorValue builds the runtime value for a constructor.
// Nullary constructors are direct VCtor values; n-ary constructors are
// functions that, when fully applied, produce a VCtor. The CtorTag field
// preserves the constructor name on the function so callers can
// recognize a constructor without having to apply it.
func makeCtorValue(tag string, arity int) Value {
	if arity == 0 {
		return VCtor{Tag: tag}
	}
	return VFn{
		Arity:   arity,
		CtorTag: tag,
		Native: func(args []Value) (Value, error) {
			return VCtor{Tag: tag, Args: append([]Value{}, args...)}, nil
		},
	}
}

// Get retrieves a top-level value by name from a loaded module.
func (m *Module) Get(name string) (Value, error) {
	v, ok := m.Env.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("module %s: no value named %q", m.Name, name)
	}
	return v, nil
}
