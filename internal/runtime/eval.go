package runtime

import (
	"fmt"
	"strings"

	"mar/internal/ast"
)

// EvalError carries position info for a runtime error.
type EvalError struct {
	Pos     ast.Pos
	Message string
}

func (e *EvalError) Error() string {
	return fmt.Sprintf("runtime error at %d:%d: %s", e.Pos.Line, e.Pos.Column, e.Message)
}

func errorf(pos ast.Pos, format string, args ...any) *EvalError {
	return &EvalError{Pos: pos, Message: fmt.Sprintf(format, args...)}
}

// Eval evaluates an AST expression against a runtime environment.
func Eval(e ast.Expr, env *Env) (Value, error) {
	switch n := e.(type) {
	case *ast.EInt:
		return VInt{V: n.Value}, nil
	case *ast.EFloat:
		return VFloat{V: n.Value}, nil
	case *ast.EString:
		return VString{V: n.Value}, nil
	case *ast.EUnit:
		return VUnit{}, nil

	case *ast.EVar:
		v, ok := env.Lookup(n.Name)
		if !ok {
			return nil, errorf(n.Pos, "unbound name: %s", n.Name)
		}
		return v, nil

	case *ast.EQualified:
		key := joinName(n.Module, n.Name)
		if v, ok := env.Lookup(key); ok {
			return v, nil
		}
		if v, ok := env.Lookup(n.Name); ok {
			return v, nil
		}
		return nil, errorf(n.Pos, "unbound qualified name: %s", key)

	case *ast.ECtor:
		// Constructors are looked up like values (registered at module load).
		v, ok := env.Lookup(n.Name)
		if !ok {
			return nil, errorf(n.Pos, "unbound constructor: %s", n.Name)
		}
		return v, nil

	case *ast.EApp:
		fn, err := Eval(n.Fn, env)
		if err != nil {
			return nil, err
		}
		arg, err := Eval(n.Arg, env)
		if err != nil {
			return nil, err
		}
		return apply(fn, arg)

	case *ast.EBinop:
		op, ok := env.Lookup(n.Op)
		if !ok {
			return nil, errorf(n.Pos, "unknown operator: %s", n.Op)
		}
		left, err := Eval(n.Left, env)
		if err != nil {
			return nil, err
		}
		right, err := Eval(n.Right, env)
		if err != nil {
			return nil, err
		}
		out, err := apply(op, left)
		if err != nil {
			return nil, err
		}
		return apply(out, right)

	case *ast.ENegate:
		v, err := Eval(n.Inner, env)
		if err != nil {
			return nil, err
		}
		switch v := v.(type) {
		case VInt:
			return VInt{V: -v.V}, nil
		case VFloat:
			return VFloat{V: -v.V}, nil
		}
		return nil, errorf(n.Pos, "negate: unsupported type")

	case *ast.ELambda:
		paramNames := make([]string, len(n.Params))
		for i, p := range n.Params {
			switch pv := p.(type) {
			case *ast.PVar:
				paramNames[i] = pv.Name
			case *ast.PWildcard:
				// Use a unique name that the body won't reference.
				paramNames[i] = fmt.Sprintf("__wild%d", i)
			default:
				return nil, errorf(n.Pos, "lambda params must be names or _ (got %T)", p)
			}
		}
		return VFn{
			Params: paramNames,
			Body:   n.Body,
			Env:    env,
			Arity:  len(paramNames),
		}, nil

	case *ast.EIf:
		c, err := Eval(n.Cond, env)
		if err != nil {
			return nil, err
		}
		b, ok := c.(VBool)
		if !ok {
			return nil, errorf(n.Cond.Position(), "if condition not Bool")
		}
		if b.V {
			return Eval(n.Then, env)
		}
		return Eval(n.Else, env)

	case *ast.ELet:
		cur := env
		for _, b := range n.Bindings {
			val, err := Eval(b.Body, cur)
			if err != nil {
				return nil, err
			}
			cur = bindPattern(b.Pattern, val, cur)
		}
		return Eval(n.Body, cur)

	case *ast.ETuple:
		members := make([]Value, len(n.Members))
		for i, m := range n.Members {
			v, err := Eval(m, env)
			if err != nil {
				return nil, err
			}
			members[i] = v
		}
		return VTuple{Members: members}, nil

	case *ast.EList:
		elems := make([]Value, len(n.Elements))
		for i, e := range n.Elements {
			v, err := Eval(e, env)
			if err != nil {
				return nil, err
			}
			elems[i] = v
		}
		return VList{Elements: elems}, nil

	case *ast.ERecord:
		fields := make(map[string]Value, len(n.Fields))
		order := make([]string, 0, len(n.Fields))
		for _, f := range n.Fields {
			v, err := Eval(f.Value, env)
			if err != nil {
				return nil, err
			}
			fields[f.Name] = v
			order = append(order, f.Name)
		}
		return VRecord{Fields: fields, Order: order}, nil

	case *ast.ERecordUpdate:
		base, err := Eval(n.Record, env)
		if err != nil {
			return nil, err
		}
		rec, ok := base.(VRecord)
		if !ok {
			return nil, errorf(n.Pos, "record update on non-record")
		}
		newFields := make(map[string]Value, len(rec.Fields))
		for k, v := range rec.Fields {
			newFields[k] = v
		}
		for _, f := range n.Fields {
			v, err := Eval(f.Value, env)
			if err != nil {
				return nil, err
			}
			newFields[f.Name] = v
		}
		return VRecord{Fields: newFields, Order: rec.Order}, nil

	case *ast.EFieldAccess:
		base, err := Eval(n.Record, env)
		if err != nil {
			return nil, err
		}
		rec, ok := base.(VRecord)
		if !ok {
			return nil, errorf(n.Pos, "field access on non-record (got %T)", base)
		}
		v, ok := rec.Fields[n.Field]
		if !ok {
			return nil, errorf(n.Pos, "no field %q in record", n.Field)
		}
		return v, nil

	case *ast.EFieldAccessor:
		// .foo as a function: \r -> r.foo
		field := n.Field
		return VFn{
			Native: func(args []Value) (Value, error) {
				rec, ok := args[0].(VRecord)
				if !ok {
					return nil, fmt.Errorf("field accessor .%s: not a record", field)
				}
				v, ok := rec.Fields[field]
				if !ok {
					return nil, fmt.Errorf("field accessor .%s: missing field", field)
				}
				return v, nil
			},
			Arity: 1,
		}, nil

	case *ast.ECase:
		subject, err := Eval(n.Subject, env)
		if err != nil {
			return nil, err
		}
		for _, branch := range n.Branches {
			bindings, ok := matchPattern(branch.Pattern, subject)
			if ok {
				branchEnv := env.BindMany(bindings)
				return Eval(branch.Body, branchEnv)
			}
		}
		return nil, errorf(n.Pos, "no case branch matched")

	default:
		return nil, errorf(e.Position(), "eval: not yet supported: %T", e)
	}
}

// Apply applies a function value to one argument, handling currying.
// Exported entry point used by the unified server to invoke handlers.
func Apply(fn Value, arg Value) (Value, error) {
	return apply(fn, arg)
}

func apply(fn Value, arg Value) (Value, error) {
	f, ok := fn.(VFn)
	if !ok {
		return nil, fmt.Errorf("apply: not a function (got %T)", fn)
	}
	applied := append(append([]Value{}, f.Applied...), arg)
	if len(applied) < f.Arity {
		// Partial application: return a new closure.
		return VFn{
			Params:  f.Params,
			Body:    f.Body,
			Env:     f.Env,
			Native:  f.Native,
			Applied: applied,
			Arity:   f.Arity,
		}, nil
	}
	// Fully applied
	if f.Native != nil {
		return f.Native(applied)
	}
	// Closure: bind params in env, evaluate body
	env := f.Env
	for i, name := range f.Params {
		env = env.Bind(name, applied[i])
	}
	body, ok := f.Body.(ast.Expr)
	if !ok {
		return nil, fmt.Errorf("apply: closure body is not an Expr")
	}
	return Eval(body, env)
}

func joinName(mod ast.ModuleName, name string) string {
	if len(mod) == 0 {
		return name
	}
	return strings.Join(mod, ".") + "." + name
}

// matchPattern attempts to match v against pat. Returns the bindings if
// successful (possibly empty), or ok=false if the pattern doesn't match.
func matchPattern(pat ast.Pattern, v Value) (map[string]Value, bool) {
	out := map[string]Value{}
	if !matchInto(pat, v, out) {
		return nil, false
	}
	return out, true
}

func matchInto(pat ast.Pattern, v Value, bindings map[string]Value) bool {
	switch p := pat.(type) {
	case *ast.PWildcard:
		return true
	case *ast.PVar:
		bindings[p.Name] = v
		return true
	case *ast.PInt:
		iv, ok := v.(VInt)
		return ok && iv.V == p.Value
	case *ast.PString:
		sv, ok := v.(VString)
		return ok && sv.V == p.Value
	case *ast.PUnit:
		_, ok := v.(VUnit)
		return ok
	case *ast.PCtor:
		cv, ok := v.(VCtor)
		if !ok || cv.Tag != p.Name || len(cv.Args) != len(p.Args) {
			return false
		}
		for i, sub := range p.Args {
			if !matchInto(sub, cv.Args[i], bindings) {
				return false
			}
		}
		return true
	case *ast.PTuple:
		tv, ok := v.(VTuple)
		if !ok || len(tv.Members) != len(p.Members) {
			return false
		}
		for i, sub := range p.Members {
			if !matchInto(sub, tv.Members[i], bindings) {
				return false
			}
		}
		return true
	case *ast.PList:
		lv, ok := v.(VList)
		if !ok || len(lv.Elements) != len(p.Elements) {
			return false
		}
		for i, sub := range p.Elements {
			if !matchInto(sub, lv.Elements[i], bindings) {
				return false
			}
		}
		return true
	case *ast.PCons:
		lv, ok := v.(VList)
		if !ok || len(lv.Elements) == 0 {
			return false
		}
		if !matchInto(p.Head, lv.Elements[0], bindings) {
			return false
		}
		// Tail value is the rest of the list.
		rest := VList{Elements: lv.Elements[1:]}
		return matchInto(p.Tail, rest, bindings)
	}
	return false
}

// bindPattern adds a value-pattern binding to an env (no fallible matching).
// Used for `let x = ...`.
func bindPattern(pat ast.Pattern, v Value, env *Env) *Env {
	bindings, ok := matchPattern(pat, v)
	if !ok {
		// Shouldn't happen for irrefutable patterns. The type checker
		// rejects refutable patterns in `let`, so reaching here means
		// the bound value has the wrong shape — leave env unchanged
		// rather than crash.
		return env
	}
	return env.BindMany(bindings)
}
