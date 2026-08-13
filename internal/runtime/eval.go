package runtime

import (
	"fmt"
	"math/big"
	"sort"
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

// internalErrorf reports a state the TYPE CHECKER already rules out: an `if`
// whose condition is not a Bool, a field read on something that is not a
// record, applying a value that is not a function.
//
// These are not mistakes a user can make in a program that compiles, so
// reporting them the same way as "no such file" sends the reader looking for a
// bug in their own code that is not there. Reaching one means the checker let
// something through, or an elaboration mark the evaluator depends on went
// missing (ADR 0016, ADR 0017): a compiler bug, and the message says so.
//
// Keeping them as errors rather than panics is deliberate: the server's
// request boundary turns an error into a 500 for one request, while a panic in
// a goroutine takes the process down. The guard is the invariant; the wording
// is what tells the two kinds of failure apart.
func internalErrorf(pos ast.Pos, format string, args ...any) *EvalError {
	return &EvalError{
		Pos: pos,
		Message: "internal error: " + fmt.Sprintf(format, args...) +
			" — a checked program cannot do this, so this is a bug in Mar rather than in your code. Please report it.",
	}
}

// Eval evaluates an AST expression against a runtime environment.
//
// This is the entry point from outside the evaluator, so it starts the call
// chain at depth zero; `evalAt` carries the depth from there.
func Eval(e ast.Expr, env *Env) (Value, error) {
	return evalAt(e, env, 0)
}

func evalAt(e ast.Expr, env *Env, depth int) (Value, error) {
	switch n := e.(type) {
	case *ast.EInt:
		// AsDecimal is the typechecker's elaboration: this literal sat in a
		// Decimal context, so it IS a Decimal. Scale 0: an integer literal
		// has no fractional digits, and `+` takes the larger scale, so
		// `1 + 1.50` lands on 2.50 without a rule of its own.
		if n.AsDecimal {
			return VDecimal{Coef: big.NewInt(n.Value), Scale: 0}, nil
		}
		return VInt{V: n.Value}, nil
	case *ast.EDecimal:
		coef, ok := new(big.Int).SetString(n.Coef, 10)
		if !ok {
			return nil, errorf(n.Pos, "invalid decimal literal")
		}
		return VDecimal{Coef: coef, Scale: n.Scale}, nil
	case *ast.EString:
		return VString{V: n.Value}, nil
	case *ast.EChar:
		return VChar{V: n.Value}, nil
	case *ast.EUnit:
		return VUnit{}, nil

	case *ast.EVar:
		if v, ok := lookupElaborated(n.Impl, env); ok {
			return v, nil
		}
		v, ok := env.Lookup(n.Name)
		if !ok {
			return nil, errorf(n.Pos, "unbound name: %s", n.Name)
		}
		return v, nil

	case *ast.EQualified:
		if v, ok := lookupElaborated(n.Impl, env); ok {
			return v, nil
		}
		key := joinName(n.Module, n.Name)
		if v, ok := env.Lookup(key); ok {
			return v, nil
		}
		if v, ok := env.Lookup(n.Name); ok {
			return v, nil
		}
		return nil, errorf(n.Pos, "unbound qualified name: %s", key)

	case *ast.ECtor:
		// Constructors are looked up like values (registered at module
		// load). Qualified ones (Shared.Created, Service.Offline) resolve
		// through the dotted binding the loader registers per module
		// export; no bare fallback, mirroring the typechecker.
		ctorName := n.Name
		if len(n.Module) > 0 {
			ctorName = joinName(n.Module, n.Name)
		}
		v, ok := env.Lookup(ctorName)
		if !ok {
			return nil, errorf(n.Pos, "unbound constructor: %s", ctorName)
		}
		return v, nil

	case *ast.EApp:
		fn, err := evalAt(n.Fn, env, depth)
		if err != nil {
			return nil, err
		}
		arg, err := evalAt(n.Arg, env, depth)
		if err != nil {
			return nil, err
		}
		return applyAt(fn, arg, depth)

	case *ast.EBinop:
		op, ok := env.Lookup(n.Op)
		if !ok {
			return nil, errorf(n.Pos, "unknown operator: %s", n.Op)
		}
		left, err := evalAt(n.Left, env, depth)
		if err != nil {
			return nil, err
		}
		right, err := evalAt(n.Right, env, depth)
		if err != nil {
			return nil, err
		}
		// Every operator is a two-argument builtin, and reaching it through
		// applyAt twice means two argument slices and one throwaway
		// partially-applied closure for each `+` in the program. Calling the
		// builtin directly skips all of it. The conditions are checked rather
		// than assumed so anything unusual falls through to the slow path.
		if f, isFn := op.(VFn); isFn && f.Native != nil && f.Arity == 2 && len(f.Applied) == 0 {
			if depth >= MaxCallDepth {
				return nil, errorf(n.Pos, "too much recursion: more than %d nested calls. "+
					"A function is calling itself without reaching a base case", MaxCallDepth)
			}
			args := []Value{left, right}
			// `|>` and `<|` APPLY an operand, so the depth has to ride along
			// here exactly as it does in applyAt: otherwise recursion written
			// with a pipe would slip past the guard.
			for i := range args {
				if af, isF := args[i].(VFn); isF {
					af.Depth = depth + 1
					args[i] = af
				}
			}
			return f.Native(args)
		}
		out, err := applyAt(op, left, depth)
		if err != nil {
			return nil, err
		}
		return applyAt(out, right, depth)

	case *ast.ENegate:
		v, err := evalAt(n.Inner, env, depth)
		if err != nil {
			return nil, err
		}
		switch v := v.(type) {
		case VInt:
			return VInt{V: -v.V}, nil
		case VDecimal:
			return VDecimal{Coef: new(big.Int).Neg(v.Coef), Scale: v.Scale}, nil
		}
		return nil, internalErrorf(n.Pos, "negate applied to something that is not a number")

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
		c, err := evalAt(n.Cond, env, depth)
		if err != nil {
			return nil, err
		}
		b, ok := c.(VBool)
		if !ok {
			return nil, internalErrorf(n.Cond.Position(), "the condition of an `if` evaluated to something that is not a Bool")
		}
		if b.V {
			return evalAt(n.Then, env, depth)
		}
		return evalAt(n.Else, env, depth)

	case *ast.ELet:
		cur := env
		for _, b := range n.Bindings {
			val, err := evalAt(b.Body, cur, depth)
			if err != nil {
				return nil, err
			}
			cur = bindPattern(b.Pattern, val, cur)
		}
		return evalAt(n.Body, cur, depth)

	case *ast.ETuple:
		members := make([]Value, len(n.Members))
		for i, m := range n.Members {
			v, err := evalAt(m, env, depth)
			if err != nil {
				return nil, err
			}
			members[i] = v
		}
		return VTuple{Members: members}, nil

	case *ast.EList:
		elems := make([]Value, len(n.Elements))
		for i, e := range n.Elements {
			v, err := evalAt(e, env, depth)
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
			v, err := evalAt(f.Value, env, depth)
			if err != nil {
				return nil, err
			}
			fields[f.Name] = v
			order = append(order, f.Name)
		}
		return VRecord{Fields: fields, Order: order}, nil

	case *ast.ERecordUpdate:
		base, err := evalAt(n.Record, env, depth)
		if err != nil {
			return nil, err
		}
		rec, ok := base.(VRecord)
		if !ok {
			return nil, internalErrorf(n.Pos, "a record update was applied to something that is not a record")
		}
		newFields := make(map[string]Value, len(rec.Fields))
		for k, v := range rec.Fields {
			newFields[k] = v
		}
		for _, f := range n.Fields {
			v, err := evalAt(f.Value, env, depth)
			if err != nil {
				return nil, err
			}
			newFields[f.Name] = v
		}
		return VRecord{Fields: newFields, Order: rec.Order}, nil

	case *ast.EFieldAccess:
		base, err := evalAt(n.Record, env, depth)
		if err != nil {
			return nil, err
		}
		rec, ok := base.(VRecord)
		if !ok {
			return nil, internalErrorf(n.Pos, "a field was read from something that is not a record (got %T)", base)
		}
		v, ok := rec.Fields[n.Field]
		if !ok {
			return nil, errorf(n.Pos, "%s", missingFieldMessage(n.Field, rec))
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
					return nil, fmt.Errorf("%s", missingFieldMessage(field, rec))
				}
				return v, nil
			},
			Arity: 1,
		}, nil

	case *ast.ECase:
		subject, err := evalAt(n.Subject, env, depth)
		if err != nil {
			return nil, err
		}
		for _, branch := range n.Branches {
			bindings, ok := matchPattern(branch.Pattern, subject)
			if ok {
				branchEnv := env.BindMany(bindings)
				return evalAt(branch.Body, branchEnv, depth)
			}
		}
		return nil, errorf(n.Pos, "no case branch matched")

	default:
		return nil, errorf(e.Position(), "eval: not yet supported: %T", e)
	}
}

// MaxCallDepth bounds how deep the evaluator will recurse before refusing.
//
// Runaway recursion used to take the whole process with it. Go's stack
// overflow is a FATAL error, not a panic: `recover()` never runs, the deferred
// handlers at the request boundary never run, and a server answering other
// users dies because one request called a function that calls itself. Measured
// before this guard: about 1.1 seconds to consume the 1 GB default stack.
//
// The limit is chosen to be far past any honest recursion and far short of the
// stack Go will kill us over. Mar's own list functions are Go loops, so user
// recursion is usually shallow; a hand-written recursive fold over a large list
// is the deep case, and 100k frames covers it with room to spare.
const MaxCallDepth = 100_000

// Apply applies a function value to one argument, handling currying.
// Exported entry point used by the unified server to invoke handlers.
func Apply(fn Value, arg Value) (Value, error) {
	return apply(fn, arg)
}

// apply is the entry point for callers outside the evaluator: chiefly the
// builtins that invoke a user function (List.map, Dict.foldl, Random.andThen).
// It resumes the depth the function value was stamped with rather than starting
// over at zero: without that, recursion routed through a higher-order builtin
// would reset the count on every lap and never trip the guard.
func apply(fn Value, arg Value) (Value, error) {
	if f, ok := fn.(VFn); ok {
		return applyAt(fn, arg, f.Depth)
	}
	return applyAt(fn, arg, 0)
}

func applyAt(fn Value, arg Value, depth int) (Value, error) {
	if depth >= MaxCallDepth {
		return nil, fmt.Errorf("too much recursion: more than %d nested calls. "+
			"A function is calling itself without reaching a base case", MaxCallDepth)
	}
	f, ok := fn.(VFn)
	if !ok {
		return nil, internalErrorf(ast.Pos{}, "a value that is not a function was applied (got %T)", fn)
	}
	applied := append(append([]Value{}, f.Applied...), arg)
	if len(applied) < f.Arity {
		// Partial application: return a new closure. It keeps this call's
		// depth so that finishing the application later resumes the count.
		return VFn{
			Params:  f.Params,
			Body:    f.Body,
			Env:     f.Env,
			Native:  f.Native,
			Applied: applied,
			Arity:   f.Arity,
			Depth:   depth,
		}, nil
	}
	// Fully applied
	if f.Native != nil {
		// A builtin receives plain values and has no way to be told how deep
		// it already is, so the depth rides on the function arguments it is
		// about to call. `List.foldl (\_ _ -> loop n) 0 [1]` recurses forever
		// through Go frames; stamping the lambda is what lets the guard see it.
		// Indexed rather than ranged, to avoid copying each interface value.
		// Measured as a wash against `range`: kept for the smaller loop body.
		for i := range applied {
			if af, isFn := applied[i].(VFn); isFn {
				af.Depth = depth + 1
				applied[i] = af
			}
		}
		return f.Native(applied)
	}
	// Closure: bind params in env, evaluate body
	env := f.Env
	for i, name := range f.Params {
		env = env.Bind(name, applied[i])
	}
	body, ok := f.Body.(ast.Expr)
	if !ok {
		return nil, internalErrorf(ast.Pos{}, "a closure carries a body that is not an expression")
	}
	return evalAt(body, env, depth+1)
}

func joinName(mod ast.ModuleName, name string) string {
	if len(mod) == 0 {
		return name
	}
	return strings.Join(mod, ".") + "." + name
}

// lookupElaborated resolves the implementation the typechecker chose for a
// reference (ast.EVar.Impl / ast.EQualified.Impl). Empty means the checker had
// nothing to say, which is every reference but a Decimal List.sum. A miss
// falls through to the ordinary name so an unelaborated tree still runs.
func lookupElaborated(impl string, env *Env) (Value, bool) {
	if impl == "" {
		return nil, false
	}
	return env.Lookup(impl)
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
	case *ast.PChar:
		cv, ok := v.(VChar)
		return ok && cv.V == p.Value
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
	case *ast.PRecord:
		// `{ f1, f2, ... }`: bind each listed field's value into the
		// local scope. Partial-match semantics: the value record may
		// have additional fields beyond those listed; we just ignore
		// them. The typechecker has already verified that every
		// listed field exists on the value's static type, so a
		// missing field here means the runtime value is shaped
		// differently than typecheck thought, which would itself be
		// a typechecker bug. We treat that as "doesn't match" rather
		// than panic.
		rv, ok := v.(VRecord)
		if !ok {
			return false
		}
		for _, fname := range p.Fields {
			fv, present := rv.Fields[fname]
			if !present {
				return false
			}
			bindings[fname] = fv
		}
		return true
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
		// the bound value has the wrong shape: leave env unchanged
		// rather than crash.
		return env
	}
	return env.BindMany(bindings)
}

// missingFieldMessage names the field that is not there and lists the ones
// that are. The wording matches the JS and Swift runtimes, so the same mistake
// reads the same wherever a program runs; only the browser adds a line about
// reloading the page, which is the one place that advice applies.
//
// The typechecker makes this unreachable for records built from this program's
// types, so reaching it means a record arrived from outside them: decoded wire
// data, or a model a dev server preserved across a reload.
//
// Field order is the record's own where it has one; a record built by decoding
// may not, and sorted names beat map iteration order, which would print a
// different list every run.
func missingFieldMessage(name string, rec VRecord) string {
	had := rec.Order
	if len(had) == 0 {
		had = make([]string, 0, len(rec.Fields))
		for k := range rec.Fields {
			had = append(had, k)
		}
		sort.Strings(had)
	}
	list := strings.Join(had, ", ")
	if list == "" {
		list = "(no fields)"
	}
	return "record has no field `" + name + "`\n\n" +
		"this record has: " + list + "\n\n" +
		"reading a field that does not exist is a type error, so this record " +
		"did not come from this program's types."
}
