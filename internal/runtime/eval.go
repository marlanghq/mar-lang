package runtime

import (
	"fmt"
	"math/big"
	"runtime/debug"
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
			return nil, internalErrorf(n.Pos, "the parser accepted the decimal literal %q and the evaluator cannot read it", n.Coef)
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
			return nil, internalErrorf(n.Pos, "unbound name: %s", n.Name)
		}
		return v, nil

	case *ast.EQualified:
		if v, ok := lookupElaborated(n.Impl, env); ok {
			return v, nil
		}
		if n.Wire != nil && len(n.Module) == 1 && n.Module[0] == "JSON" && n.Name == "decode" {
			// This occurrence of JSON.decode knows what it is decoding
			// INTO, because the checker wrote the call site's type onto the
			// node. Without it the decoder answered Ok for anything that
			// was valid JSON, and the mismatch surfaced later, somewhere
			// else, as a runtime error about a field.
			//
			// The name is checked, not just the mark: elaboration stamps a
			// shape on Service.call too, and reading that one as a decoder
			// would replace the call with a JSON parser.
			return decodeWithShape(*n.Wire), nil
		}
		if v, ok := env.lookupQualified(n.Module, n.Name); ok {
			return v, nil
		}
		if v, ok := env.Lookup(n.Name); ok {
			return v, nil
		}
		return nil, internalErrorf(n.Pos, "unbound qualified name: %s", joinName(n.Module, n.Name))

	case *ast.ECtor:
		// Constructors are looked up like values (registered at module
		// load). Qualified ones (Shared.Created, Service.Offline) resolve
		// through the dotted binding the loader registers per module
		// export; no bare fallback, mirroring the typechecker.
		v, ok := env.lookupQualified(n.Module, n.Name)
		if !ok {
			return nil, internalErrorf(n.Pos, "unbound constructor: %s", joinName(n.Module, n.Name))
		}
		return v, nil

	case *ast.EApp:
		return evalApp(n, env, depth)

	case *ast.EBinop:
		return evalBinop(n, env, depth)

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
				return nil, internalErrorf(n.Pos, "a lambda parameter is not a name or `_` (got %T)", p)
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
			// The type checker rejects refutable patterns in `let`, so a
			// failed match means the bound value has the wrong shape: leave
			// env unchanged rather than crash.
			if next, ok := matchInto(b.Pattern, val, cur); ok {
				cur = next
			}
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
			return nil, internalErrorf(n.Pos, "%s", missingFieldMessage(n.Field, rec))
		}
		return v, nil

	case *ast.EFieldAccessor:
		// .foo as a function: \r -> r.foo
		field := n.Field
		pos := n.Pos
		return VFn{
			Native: func(args []Value) (Value, error) {
				rec, ok := args[0].(VRecord)
				if !ok {
					return nil, internalErrorf(pos, "the field accessor `.%s` was applied to something that is not a record (got %T)", field, args[0])
				}
				v, ok := rec.Fields[field]
				if !ok {
					return nil, internalErrorf(pos, "%s", missingFieldMessage(field, rec))
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
			if branchEnv, ok := matchInto(branch.Pattern, subject, env); ok {
				return evalAt(branch.Body, branchEnv, depth)
			}
		}
		return nil, errorf(n.Pos, "no case branch matched")

	default:
		return nil, internalErrorf(e.Position(), "the evaluator has no case for %T", e)
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
// The number is DERIVED rather than chosen, because a chosen one goes stale in
// silence. It was 100_000, described in this comment as "far short of the stack
// Go will kill us over", and it was not: measured with a fresh process per
// trial, one Mar call per level, only the cheapest possible shape ever reached
// it.
//
//	deep (n-1)                          tail call   reached the guard at 100k
//	1 + deep (n-1)                                  died between 60k and 80k
//	case n of _ -> 1 + deep (n-1)                   died between 60k and 80k
//	List.length [ deep (n-1) ]                      died between 60k and 80k
//	Tuple.first ( 1 + deep (n-1), 0 )               died between 50k and 55k
//
// The guard was set roughly twice as high as the ceiling for ordinary
// recursion, so it protected exactly one shape — the one the test happened to
// use. See ADR 0033.
//
// Now: ask the process what a goroutine stack may reach, divide by the worst
// per-frame cost measured above, and keep a factor of four in hand. When evalAt
// grows a field, the budget shrinks with it instead of drifting out of date.
var MaxCallDepth = derivedMaxCallDepth()

// worstCaseFrameBytes is the Go stack one Mar-level call can cost, from the
// most expensive shape above: ~50k frames inside a 1 GiB stack. Nested calls
// inside a constructor argument are the expensive case because each argument's
// evaluation nests another evalAt before the outer one returns.
//
// Raise this if a measurement ever finds a costlier shape; do not lower it to
// buy depth.
const worstCaseFrameBytes = 21_000

// stackSafetyFactor is how much of the budget is left unspent. Four means the
// guard fires with three quarters of the stack still untouched, which is the
// room a shape nobody has measured yet gets to be wrong in.
const stackSafetyFactor = 4

// Depth is meaningless below a floor (an app would break on honest recursion)
// and pointless above a ceiling (nothing legitimate goes that deep, and the
// stack is the real limit anyway).
const (
	minCallDepth = 1_000
	maxCallDepth = 100_000
)

// stackCeilingBytes reports how large one goroutine's stack may grow before Go
// kills the process. debug.SetMaxStack returns the PREVIOUS value, so setting
// and immediately restoring reads it without changing anything — which matters
// because this package is imported by the compiler and the LSP, not only by the
// server, and none of them asked to have their limits moved.
func stackCeilingBytes() int {
	limit := debug.SetMaxStack(1 << 30)
	debug.SetMaxStack(limit)
	return limit
}

// derivedMaxCallDepth reads the process's own goroutine stack ceiling instead
// of assuming one. debug.SetMaxStack returns the PREVIOUS value, so setting and
// immediately restoring is how you read it without changing anything — this
// leaves the process exactly as it found it, which matters because the package
// is imported by the compiler and the LSP, not only by the server.
//
// Reading rather than hardcoding also means 32-bit hosts (250 MB default) get a
// budget that fits them, without this file knowing they exist.
func derivedMaxCallDepth() int {
	d := stackCeilingBytes() / worstCaseFrameBytes / stackSafetyFactor
	if d < minCallDepth {
		return minCallDepth
	}
	if d > maxCallDepth {
		return maxCallDepth
	}
	return d
}

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
		return nil, errTooMuchRecursion()
	}
	f, ok := fn.(VFn)
	if !ok {
		return nil, internalErrorf(ast.Pos{}, "a value that is not a function was applied (got %T)", fn)
	}
	if f.Native == nil && len(f.Applied)+1 == f.Arity {
		// The last argument of a closure: the one case that needs no
		// argument slice at all, and by far the commonest call there is.
		return callClosure(f, arg, nil, depth)
	}
	applied := make([]Value, len(f.Applied)+1)
	copy(applied, f.Applied)
	applied[len(f.Applied)] = arg
	return finishApply(f, applied, depth)
}

// applyBatch applies f to several arguments in one step. It computes exactly
// what feeding them to applyAt one at a time computes, without the closure and
// the argument slice each intermediate step would build and throw away: a
// three-argument call used to allocate two partial applications on the way to
// its third argument.
//
// The caller has already made applyAt's depth check, and guarantees
// 2 <= len(args) <= f.Arity-len(f.Applied): a batch never runs past the point
// where the function is saturated, because what a saturated call returns (and
// whether it fails) has to be known before the next argument is looked at.
func applyBatch(f VFn, args []Value, depth int) (Value, error) {
	if f.Native == nil && len(f.Applied)+len(args) == f.Arity {
		return callClosure(f, nil, args, depth)
	}
	applied := make([]Value, len(f.Applied)+len(args))
	copy(applied, f.Applied)
	copy(applied[len(f.Applied):], args)
	return finishApply(f, applied, depth)
}

// finishApply is the tail both entry points share once the arguments are
// gathered into a slice the callee may keep.
func finishApply(f VFn, applied []Value, depth int) (Value, error) {
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

// callClosure runs a closure whose last arguments have just arrived: the ones
// it already held, then `last` (one argument) or `rest` (a batch). Parameters
// are bound straight into the environment, in order, so the call allocates one
// small frame per parameter and nothing else.
func callClosure(f VFn, last Value, rest []Value, depth int) (Value, error) {
	env := f.Env
	held := len(f.Applied)
	for i, v := range f.Applied {
		env = env.Bind(f.Params[i], v)
	}
	if rest == nil {
		env = env.Bind(f.Params[held], last)
	} else {
		for i, v := range rest {
			env = env.Bind(f.Params[held+i], v)
		}
	}
	body, ok := f.Body.(ast.Expr)
	if !ok {
		return nil, internalErrorf(ast.Pos{}, "a closure carries a body that is not an expression")
	}
	return evalAt(body, env, depth+1)
}

func errTooMuchRecursion() error {
	return fmt.Errorf("too much recursion: more than %d nested calls. "+
		"A function is calling itself without reaching a base case", MaxCallDepth)
}

// applyArgs is apply folded over several arguments, `apply(apply(fn, a), b)`,
// for the builtins that feed a callback more than one (List.foldl, Dict.map,
// List.sortWith). Same result and same depth accounting, without the partial
// application in between while fn is still waiting for more than one.
func applyArgs(fn Value, args ...Value) (Value, error) {
	cur := fn
	for i := 0; i < len(args); {
		if f, ok := cur.(VFn); ok && len(args)-i > 1 {
			if waiting := f.Arity - len(f.Applied); waiting > 1 {
				// apply(f, a) would build a partial stamped with f.Depth, and
				// applying the next argument to that resumes at the same
				// depth: one check covers the batch.
				if f.Depth >= MaxCallDepth {
					return nil, errTooMuchRecursion()
				}
				take := min(waiting, len(args)-i)
				next, err := applyBatch(f, args[i:i+take], f.Depth)
				if err != nil {
					return nil, err
				}
				cur = next
				i += take
				continue
			}
		}
		next, err := apply(cur, args[i])
		if err != nil {
			return nil, err
		}
		cur = next
		i++
	}
	return cur, nil
}

// spineMax is how many arguments of one written call are gathered on the Go
// stack. A longer call still works: the part of the spine beyond it is
// evaluated as the head.
const spineMax = 6

// evalApp evaluates an application. `f a b c` parses as ((f a) b) c, and the
// straightforward reading of that tree applies one argument at a time, which
// builds a partial application for every argument but the last. This walks the
// spine instead and hands the function as many arguments as it is waiting for.
//
// The order of everything observable is the tree's: the head first, then each
// argument left to right, and a call that becomes saturated runs BEFORE the
// arguments after it are evaluated. That last part is not a detail. Mar is
// pure but not total, so which of two failures a program reports depends on
// which one is reached first.
func evalApp(n *ast.EApp, env *Env, depth int) (Value, error) {
	if _, nested := n.Fn.(*ast.EApp); !nested {
		fn, err := evalAt(n.Fn, env, depth)
		if err != nil {
			return nil, err
		}
		arg, err := evalAt(n.Arg, env, depth)
		if err != nil {
			return nil, err
		}
		return applyAt(fn, arg, depth)
	}

	var exprs [spineMax]ast.Expr
	count := 0
	head := ast.Expr(n)
	for count < spineMax {
		app, ok := head.(*ast.EApp)
		if !ok {
			break
		}
		count++
		exprs[spineMax-count] = app.Arg
		head = app.Fn
	}
	args := exprs[spineMax-count:]

	cur, err := evalAt(head, env, depth)
	if err != nil {
		return nil, err
	}
	var vals [spineMax]Value
	for i := 0; i < len(args); {
		take := 1
		f, isFn := cur.(VFn)
		if isFn {
			if waiting := f.Arity - len(f.Applied); waiting > 1 {
				take = min(waiting, len(args)-i)
			}
		}
		if take == 1 {
			arg, err := evalAt(args[i], env, depth)
			if err != nil {
				return nil, err
			}
			if cur, err = applyAt(cur, arg, depth); err != nil {
				return nil, err
			}
			i++
			continue
		}
		for j := 0; j < take; j++ {
			v, err := evalAt(args[i+j], env, depth)
			if err != nil {
				return nil, err
			}
			// applyAt refuses at the depth limit as soon as it is handed the
			// first argument, before the second is evaluated. So does this.
			if j == 0 && depth >= MaxCallDepth {
				return nil, errTooMuchRecursion()
			}
			vals[j] = v
		}
		if cur, err = applyBatch(f, vals[:take], depth); err != nil {
			return nil, err
		}
		i += take
	}
	return cur, nil
}

// evalBinop evaluates `left op right`.
//
// Every operator Mar has is a builtin, and none can be redefined: a program
// has no syntax that binds an operator's name. So the operator is resolved
// here, from the same Go functions BaseEnv registers, instead of by walking
// the environment from the innermost parameter out to the root on every `+`.
// An operator this table does not know takes the general path, which looks it
// up and reports it.
func evalBinop(n *ast.EBinop, env *Env, depth int) (Value, error) {
	shortCircuit := n.Op == "&&" || n.Op == "||"
	impl := binopImpl(n.Op)
	if impl == nil && !shortCircuit {
		return evalBinopGeneral(n, env, depth)
	}
	left, err := evalAt(n.Left, env, depth)
	if err != nil {
		return nil, err
	}
	// `&&` and `||` SHORT-CIRCUIT: when the left operand already decides
	// the answer, the right one is never evaluated.
	//
	// This is not an optimisation, and purity does not make it one. Mar is
	// pure but not TOTAL: pure checked code has exactly two ways to fail,
	// and both are reachable from an operand. `n < 1 && 9007199254740991 + n > 0`
	// used to raise an Int overflow even though the left side said False,
	// and `depth < 80 && walk tree > 0` used to spend the stack the guard
	// existed to protect. A guard that its own language ignores is not a
	// guard. Elm short-circuits here, so this also closes an Elm divergence
	// that was never declared (ADR: Elm-fidelity).
	if shortCircuit {
		lb, isBool := left.(VBool)
		if !isBool {
			return nil, internalErrorf(n.Pos, "an operand of `%s` evaluated to something that is not a Bool", n.Op)
		}
		if n.Op == "&&" && !lb.V {
			return VBool{V: false}, nil
		}
		if n.Op == "||" && lb.V {
			return VBool{V: true}, nil
		}
		right, err := evalAt(n.Right, env, depth)
		if err != nil {
			return nil, err
		}
		rb, isBool := right.(VBool)
		if !isBool {
			return nil, internalErrorf(n.Pos, "an operand of `%s` evaluated to something that is not a Bool", n.Op)
		}
		return rb, nil
	}
	right, err := evalAt(n.Right, env, depth)
	if err != nil {
		return nil, err
	}
	if depth >= MaxCallDepth {
		return nil, errorf(n.Pos, "too much recursion: more than %d nested calls. "+
			"A function is calling itself without reaching a base case", MaxCallDepth)
	}
	// `|>` and `<|` APPLY an operand and `::` keeps one, so the depth has to
	// ride along on a function operand exactly as it does in applyAt:
	// otherwise recursion written with a pipe would slip past the guard. The
	// other operators neither call nor keep what they are given.
	if n.Op == "|>" || n.Op == "<|" || n.Op == "::" {
		left, right = stampDepth(left, depth+1), stampDepth(right, depth+1)
	}
	return impl(left, right)
}

func stampDepth(v Value, depth int) Value {
	if f, isFn := v.(VFn); isFn {
		f.Depth = depth
		return f
	}
	return v
}

// evalBinopGeneral resolves the operator through the environment. Nothing a
// checked program writes reaches it: it is what reports an operator the
// evaluator has never heard of.
func evalBinopGeneral(n *ast.EBinop, env *Env, depth int) (Value, error) {
	op, ok := env.Lookup(n.Op)
	if !ok {
		return nil, internalErrorf(n.Pos, "unknown operator: %s", n.Op)
	}
	left, err := evalAt(n.Left, env, depth)
	if err != nil {
		return nil, err
	}
	right, err := evalAt(n.Right, env, depth)
	if err != nil {
		return nil, err
	}
	out, err := applyAt(op, left, depth)
	if err != nil {
		return nil, err
	}
	return applyAt(out, right, depth)
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

// matchInto matches v against pat. On success it returns env extended with
// the pattern's variables; on failure it returns ok=false and the env it was
// given is untouched (binding never mutates an Env, so a half-matched pattern
// leaves nothing behind for the next branch to see).
//
// Variables are bound in the order the pattern lists them, each in its own
// frame, straight into the environment: a `case` used to build a Go map for
// every branch it TRIED, and copy it into a second one for the branch that
// matched.
func matchInto(pat ast.Pattern, v Value, env *Env) (*Env, bool) {
	switch p := pat.(type) {
	case *ast.PWildcard:
		return env, true
	case *ast.PVar:
		return env.Bind(p.Name, v), true
	case *ast.PInt:
		iv, ok := v.(VInt)
		return env, ok && iv.V == p.Value
	case *ast.PString:
		sv, ok := v.(VString)
		return env, ok && sv.V == p.Value
	case *ast.PChar:
		cv, ok := v.(VChar)
		return env, ok && cv.V == p.Value
	case *ast.PUnit:
		_, ok := v.(VUnit)
		return env, ok
	case *ast.PCtor:
		cv, ok := v.(VCtor)
		if !ok || cv.Tag != p.Name || len(cv.Args) != len(p.Args) {
			return env, false
		}
		for i, sub := range p.Args {
			if env, ok = matchInto(sub, cv.Args[i], env); !ok {
				return env, false
			}
		}
		return env, true
	case *ast.PTuple:
		tv, ok := v.(VTuple)
		if !ok || len(tv.Members) != len(p.Members) {
			return env, false
		}
		for i, sub := range p.Members {
			if env, ok = matchInto(sub, tv.Members[i], env); !ok {
				return env, false
			}
		}
		return env, true
	case *ast.PList:
		lv, ok := v.(VList)
		if !ok || len(lv.Elements) != len(p.Elements) {
			return env, false
		}
		for i, sub := range p.Elements {
			if env, ok = matchInto(sub, lv.Elements[i], env); !ok {
				return env, false
			}
		}
		return env, true
	case *ast.PCons:
		lv, ok := v.(VList)
		if !ok || len(lv.Elements) == 0 {
			return env, false
		}
		if env, ok = matchInto(p.Head, lv.Elements[0], env); !ok {
			return env, false
		}
		// Tail value is the rest of the list.
		rest := VList{Elements: lv.Elements[1:]}
		return matchInto(p.Tail, rest, env)
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
			return env, false
		}
		for _, fname := range p.Fields {
			fv, present := rv.Fields[fname]
			if !present {
				return env, false
			}
			env = env.Bind(fname, fv)
		}
		return env, true
	}
	return env, false
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
