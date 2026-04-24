package expr

import (
	"fmt"
	"regexp"
)

// Expr is a parsed expression node.
type Expr interface {
	Eval(ctx map[string]any) (any, error)
}

type Literal struct {
	Value any
}

func (l Literal) Eval(_ map[string]any) (any, error) {
	return l.Value, nil
}

type ListLiteral struct {
	Items []Expr
}

func (l ListLiteral) Eval(ctx map[string]any) (any, error) {
	out := make([]any, 0, len(l.Items))
	for _, item := range l.Items {
		value, err := item.Eval(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}

type Variable struct {
	Name string
}

func (v Variable) Eval(ctx map[string]any) (any, error) {
	return ctx[v.Name], nil
}

type FunctionRef struct {
	Name string
}

func (f FunctionRef) Eval(_ map[string]any) (any, error) {
	return namedFunction(f), nil
}

type RegexMatch struct {
	Pattern *regexp.Regexp
	Text    Expr
}

func (m RegexMatch) Eval(ctx map[string]any) (any, error) {
	value, err := m.Text.Eval(ctx)
	if err != nil {
		return nil, err
	}
	text, err := RequireString(value)
	if err != nil {
		return nil, fmt.Errorf("matches expects string arguments")
	}
	return m.Pattern.MatchString(text), nil
}

type Unary struct {
	Op    string
	Right Expr
}

func (u Unary) Eval(ctx map[string]any) (any, error) {
	right, err := u.Right.Eval(ctx)
	if err != nil {
		return nil, err
	}
	switch u.Op {
	case "not":
		value, err := RequireBool(right)
		if err != nil {
			return nil, err
		}
		return !value, nil
	case "-":
		n, ok := ToDecimal(right)
		if !ok {
			return nil, fmt.Errorf("operator - expects number")
		}
		zero := NewDecimalFromInt(0)
		return zero.Sub(n), nil
	default:
		return nil, fmt.Errorf("unknown unary operator %q", u.Op)
	}
}

type Binary struct {
	Op    string
	Left  Expr
	Right Expr
}

func exactIntResult(left any, right any, result Decimal) (int64, bool) {
	if !isRuntimeInt(left) || !isRuntimeInt(right) {
		return 0, false
	}
	return result.IsInt64()
}

func isRuntimeInt(value any) bool {
	switch value.(type) {
	case int, int64:
		return true
	default:
		return false
	}
}

type If struct {
	Condition Expr
	Then      Expr
	Else      Expr
}

func (i If) Eval(ctx map[string]any) (any, error) {
	condition, err := i.Condition.Eval(ctx)
	if err != nil {
		return nil, err
	}
	ok, err := RequireBool(condition)
	if err != nil {
		return nil, err
	}
	if ok {
		return i.Then.Eval(ctx)
	}
	return i.Else.Eval(ctx)
}

type CondClause struct {
	Test Expr
	Body Expr
	Else bool
}

type Cond struct {
	Clauses []CondClause
}

func (c Cond) Eval(ctx map[string]any) (any, error) {
	for _, clause := range c.Clauses {
		if clause.Else {
			return clause.Body.Eval(ctx)
		}
		value, err := clause.Test.Eval(ctx)
		if err != nil {
			return nil, err
		}
		ok, err := RequireBool(value)
		if err != nil {
			return nil, err
		}
		if ok {
			return clause.Body.Eval(ctx)
		}
	}
	return nil, fmt.Errorf("cond had no matching clause")
}

type Binding struct {
	Name  string
	Value Expr
}

type Let struct {
	Bindings   []Binding
	Body       Expr
	Sequential bool
}

func (l Let) Eval(ctx map[string]any) (any, error) {
	child := cloneContext(ctx)
	if l.Sequential {
		for _, binding := range l.Bindings {
			value, err := binding.Value.Eval(child)
			if err != nil {
				return nil, err
			}
			child[binding.Name] = value
		}
		return l.Body.Eval(child)
	}

	values := make([]any, 0, len(l.Bindings))
	for _, binding := range l.Bindings {
		value, err := binding.Value.Eval(ctx)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	for index, binding := range l.Bindings {
		child[binding.Name] = values[index]
	}
	return l.Body.Eval(child)
}

type Begin struct {
	Expressions []Expr
}

func (b Begin) Eval(ctx map[string]any) (any, error) {
	var result any
	for _, expression := range b.Expressions {
		value, err := expression.Eval(ctx)
		if err != nil {
			return nil, err
		}
		result = value
	}
	return result, nil
}

type Lambda struct {
	Params []string
	Body   Expr
}

func (l Lambda) Eval(ctx map[string]any) (any, error) {
	return closure{
		Params: l.Params,
		Body:   l.Body,
		Env:    cloneContext(ctx),
	}, nil
}

type RaisedError struct {
	Message string
}

func (r RaisedError) Error() string {
	return r.Message
}

type Error struct {
	Message string
}

func (e Error) Eval(_ map[string]any) (any, error) {
	return nil, RaisedError(e)
}

type Opaque struct {
	Kind   string
	Source string
}

func (o Opaque) Eval(ctx map[string]any) (any, error) {
	bindings := map[string]any{}
	for key, value := range ctx {
		if key == functionsContextKey {
			continue
		}
		bindings[key] = value
	}
	return map[string]any{
		"kind":     o.Kind,
		"source":   o.Source,
		"bindings": bindings,
	}, nil
}

type UserFunction struct {
	Params []string
	Body   Expr
}

type RecordConstructor struct {
	Name   string
	Fields []string
	Args   []Expr
}

func (r RecordConstructor) Eval(ctx map[string]any) (any, error) {
	out := map[string]any{}
	for index, field := range r.Fields {
		value, err := r.Args[index].Eval(ctx)
		if err != nil {
			return nil, err
		}
		out[field] = value
	}
	return out, nil
}

type TaggedConstructor struct {
	Tag  string
	Args []Expr
}

func (t TaggedConstructor) Eval(ctx map[string]any) (any, error) {
	values := make([]any, 0, len(t.Args))
	for _, arg := range t.Args {
		value, err := arg.Eval(ctx)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return TaggedValue{Tag: t.Tag, Values: values}, nil
}

const functionsContextKey = "__functions"

func FunctionsContextKey() string {
	return functionsContextKey
}

type callable interface {
	Call(args []any, ctx map[string]any) (any, error)
}

type namedFunction struct {
	Name string
}

func (n namedFunction) Call(args []any, ctx map[string]any) (any, error) {
	fn, ok := lookupUserFunction(ctx, n.Name)
	if !ok {
		return nil, fmt.Errorf("unknown function %q", n.Name)
	}
	if len(args) != len(fn.Params) {
		return nil, fmt.Errorf("%s expects %d arguments", n.Name, len(fn.Params))
	}
	child := cloneContext(ctx)
	for index, param := range fn.Params {
		child[param] = args[index]
	}
	return fn.Body.Eval(child)
}

type closure struct {
	Params []string
	Body   Expr
	Env    map[string]any
}

func (c closure) Call(args []any, _ map[string]any) (any, error) {
	if len(args) != len(c.Params) {
		return nil, fmt.Errorf("lambda expects %d arguments", len(c.Params))
	}
	child := cloneContext(c.Env)
	for index, param := range c.Params {
		child[param] = args[index]
	}
	return c.Body.Eval(child)
}

type TaggedValue struct {
	Tag    string
	Values []any
}

type MatchPattern struct {
	Tag  string
	Vars []string
}

type MatchClause struct {
	Pattern MatchPattern
	Body    Expr
}

type Match struct {
	Subject Expr
	Clauses []MatchClause
}

func (m Match) Eval(ctx map[string]any) (any, error) {
	subject, err := m.Subject.Eval(ctx)
	if err != nil {
		return nil, err
	}
	for _, clause := range m.Clauses {
		bindings, ok := matchPattern(clause.Pattern, subject)
		if !ok {
			continue
		}
		child := cloneContext(ctx)
		for name, value := range bindings {
			child[name] = value
		}
		return clause.Body.Eval(child)
	}
	return nil, fmt.Errorf("match had no matching clause")
}

type Get struct {
	Target Expr
	Field  string
}

func (g Get) Eval(ctx map[string]any) (any, error) {
	target, err := g.Target.Eval(ctx)
	if err != nil {
		return nil, err
	}
	record, ok := target.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("get expects a record-like value")
	}
	value, ok := record[g.Field]
	if !ok {
		return nil, fmt.Errorf("record has no field %q", g.Field)
	}
	return value, nil
}

type FieldUpdate struct {
	Field string
	Value Expr
}

type Assoc struct {
	Target  Expr
	Updates []FieldUpdate
}

func (a Assoc) Eval(ctx map[string]any) (any, error) {
	target, err := a.Target.Eval(ctx)
	if err != nil {
		return nil, err
	}
	record, ok := target.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("assoc expects a record-like value")
	}
	out := map[string]any{}
	for key, value := range record {
		out[key] = value
	}
	for _, update := range a.Updates {
		if _, ok := record[update.Field]; !ok {
			return nil, fmt.Errorf("record has no field %q", update.Field)
		}
		value, err := update.Value.Eval(ctx)
		if err != nil {
			return nil, err
		}
		out[update.Field] = value
	}
	return out, nil
}

func (b Binary) Eval(ctx map[string]any) (any, error) {
	switch b.Op {
	case "and":
		left, err := b.Left.Eval(ctx)
		if err != nil {
			return nil, err
		}
		leftBool, err := RequireBool(left)
		if err != nil {
			return nil, err
		}
		if !leftBool {
			return false, nil
		}
		right, err := b.Right.Eval(ctx)
		if err != nil {
			return nil, err
		}
		return RequireBool(right)
	case "or":
		left, err := b.Left.Eval(ctx)
		if err != nil {
			return nil, err
		}
		leftBool, err := RequireBool(left)
		if err != nil {
			return nil, err
		}
		if leftBool {
			return true, nil
		}
		right, err := b.Right.Eval(ctx)
		if err != nil {
			return nil, err
		}
		return RequireBool(right)
	}

	left, err := b.Left.Eval(ctx)
	if err != nil {
		return nil, err
	}
	right, err := b.Right.Eval(ctx)
	if err != nil {
		return nil, err
	}

	switch b.Op {
	case "==":
		return Equal(left, right), nil
	case "!=":
		return !Equal(left, right), nil
	case ">", ">=", "<", "<=":
		cmp, ok, err := Compare(left, right)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("operator %s expects int or decimal", b.Op)
		}
		switch b.Op {
		case ">":
			return cmp > 0, nil
		case ">=":
			return cmp >= 0, nil
		case "<":
			return cmp < 0, nil
		default:
			return cmp <= 0, nil
		}
	case "+", "-", "*", "/":
		ln, lok := ToDecimal(left)
		rn, rok := ToDecimal(right)
		if !lok || !rok {
			return nil, fmt.Errorf("operator %s expects numbers", b.Op)
		}
		switch b.Op {
		case "+":
			out := ln.Add(rn)
			if value, ok := exactIntResult(left, right, out); ok {
				return value, nil
			}
			return out, nil
		case "-":
			out := ln.Sub(rn)
			if value, ok := exactIntResult(left, right, out); ok {
				return value, nil
			}
			return out, nil
		case "*":
			out := ln.Mul(rn)
			if value, ok := exactIntResult(left, right, out); ok {
				return value, nil
			}
			return out, nil
		default:
			return ln.Quo(rn)
		}
	default:
		return nil, fmt.Errorf("unknown operator %q", b.Op)
	}
}

type Call struct {
	Name string
	Args []Expr
}

func (c Call) Eval(ctx map[string]any) (any, error) {
	vals := make([]any, 0, len(c.Args))
	for _, arg := range c.Args {
		v, err := arg.Eval(ctx)
		if err != nil {
			return nil, err
		}
		vals = append(vals, v)
	}

	switch c.Name {
	case "contains":
		if len(vals) != 2 {
			return nil, fmt.Errorf("contains expects 2 arguments")
		}
		needle, err := RequireString(vals[0])
		if err != nil {
			return nil, fmt.Errorf("contains expects string arguments")
		}
		text, err := RequireString(vals[1])
		if err != nil {
			return nil, fmt.Errorf("contains expects string arguments")
		}
		return Contains(text, needle), nil
	case "starts_with":
		if len(vals) != 2 {
			return nil, fmt.Errorf("starts-with expects 2 arguments")
		}
		prefix, err := RequireString(vals[0])
		if err != nil {
			return nil, fmt.Errorf("starts-with expects string arguments")
		}
		text, err := RequireString(vals[1])
		if err != nil {
			return nil, fmt.Errorf("starts-with expects string arguments")
		}
		return StartsWith(text, prefix), nil
	case "ends_with":
		if len(vals) != 2 {
			return nil, fmt.Errorf("ends-with expects 2 arguments")
		}
		suffix, err := RequireString(vals[0])
		if err != nil {
			return nil, fmt.Errorf("ends-with expects string arguments")
		}
		text, err := RequireString(vals[1])
		if err != nil {
			return nil, fmt.Errorf("ends-with expects string arguments")
		}
		return EndsWith(text, suffix), nil
	case "length":
		if len(vals) != 1 {
			return nil, fmt.Errorf("length expects 1 argument")
		}
		return Length(vals[0])
	case "just":
		if len(vals) != 1 {
			return nil, fmt.Errorf("just expects 1 argument")
		}
		return TaggedValue{Tag: "just", Values: []any{vals[0]}}, nil
	case "nothing":
		if len(vals) != 0 {
			return nil, fmt.Errorf("nothing expects 0 arguments")
		}
		return TaggedValue{Tag: "nothing"}, nil
	case "unit":
		if len(vals) != 0 {
			return nil, fmt.Errorf("unit expects 0 arguments")
		}
		return TaggedValue{Tag: "unit"}, nil
	case "ok":
		if len(vals) != 1 {
			return nil, fmt.Errorf("ok expects 1 argument")
		}
		return TaggedValue{Tag: "ok", Values: []any{vals[0]}}, nil
	case "err":
		if len(vals) != 1 {
			return nil, fmt.Errorf("err expects 1 argument")
		}
		return TaggedValue{Tag: "err", Values: []any{vals[0]}}, nil
	case "authenticated?":
		if len(vals) != 1 {
			return nil, fmt.Errorf("authenticated? expects 1 argument")
		}
		user, ok := vals[0].(TaggedValue)
		if !ok {
			return nil, fmt.Errorf("authenticated? expects current-user")
		}
		return user.Tag == "authenticated" && len(user.Values) == 3, nil
	case "anonymous?":
		if len(vals) != 1 {
			return nil, fmt.Errorf("anonymous? expects 1 argument")
		}
		user, ok := vals[0].(TaggedValue)
		if !ok {
			return nil, fmt.Errorf("anonymous? expects current-user")
		}
		return user.Tag == "anonymous" && len(user.Values) == 0, nil
	case "same_user?":
		if len(vals) != 2 {
			return nil, fmt.Errorf("same-user? expects 2 arguments")
		}
		user, ok := vals[0].(TaggedValue)
		if !ok {
			return nil, fmt.Errorf("same-user? expects current-user as first argument")
		}
		if user.Tag != "authenticated" || len(user.Values) != 3 {
			return false, nil
		}
		left, ok := ToDecimal(user.Values[0])
		if !ok {
			return nil, fmt.Errorf("same-user? expects authenticated user id")
		}
		right, ok := ToDecimal(vals[1])
		if !ok {
			return nil, fmt.Errorf("same-user? expects user id as second argument")
		}
		return left.Cmp(right) == 0, nil
	case "has_role?":
		if len(vals) != 2 {
			return nil, fmt.Errorf("has-role? expects 2 arguments")
		}
		user, ok := vals[0].(TaggedValue)
		if !ok {
			return nil, fmt.Errorf("has-role? expects current-user as first argument")
		}
		role, err := RequireString(vals[1])
		if err != nil {
			return nil, fmt.Errorf("has-role? expects role as second argument")
		}
		if user.Tag != "authenticated" || len(user.Values) != 3 {
			return false, nil
		}
		currentRole, err := RequireString(user.Values[2])
		if err != nil {
			return nil, fmt.Errorf("has-role? expects authenticated user role")
		}
		return currentRole == role, nil
	case "cons":
		if len(vals) != 2 {
			return nil, fmt.Errorf("cons expects 2 arguments")
		}
		list, ok := ToList(vals[1])
		if !ok {
			return nil, fmt.Errorf("cons expects a list as second argument")
		}
		return append([]any{vals[0]}, list...), nil
	case "first":
		if len(vals) != 1 {
			return nil, fmt.Errorf("first expects 1 argument")
		}
		list, ok := ToList(vals[0])
		if !ok {
			return nil, fmt.Errorf("first expects a list")
		}
		if len(list) == 0 {
			return TaggedValue{Tag: "nothing"}, nil
		}
		return TaggedValue{Tag: "just", Values: []any{list[0]}}, nil
	case "rest":
		if len(vals) != 1 {
			return nil, fmt.Errorf("rest expects 1 argument")
		}
		list, ok := ToList(vals[0])
		if !ok {
			return nil, fmt.Errorf("rest expects a list")
		}
		if len(list) == 0 {
			return []any{}, nil
		}
		out := make([]any, len(list)-1)
		copy(out, list[1:])
		return out, nil
	case "empty?":
		if len(vals) != 1 {
			return nil, fmt.Errorf("empty? expects 1 argument")
		}
		list, ok := ToList(vals[0])
		if !ok {
			return nil, fmt.Errorf("empty? expects a list")
		}
		return len(list) == 0, nil
	case "map":
		if len(vals) != 2 {
			return nil, fmt.Errorf("map expects 2 arguments")
		}
		fn, ok := vals[0].(callable)
		if !ok {
			return nil, fmt.Errorf("map expects a function as first argument")
		}
		list, ok := ToList(vals[1])
		if !ok {
			return nil, fmt.Errorf("map expects a list as second argument")
		}
		out := make([]any, 0, len(list))
		for _, item := range list {
			value, err := fn.Call([]any{item}, ctx)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		return out, nil
	case "filter":
		if len(vals) != 2 {
			return nil, fmt.Errorf("filter expects 2 arguments")
		}
		fn, ok := vals[0].(callable)
		if !ok {
			return nil, fmt.Errorf("filter expects a function as first argument")
		}
		list, ok := ToList(vals[1])
		if !ok {
			return nil, fmt.Errorf("filter expects a list as second argument")
		}
		out := make([]any, 0, len(list))
		for _, item := range list {
			value, err := fn.Call([]any{item}, ctx)
			if err != nil {
				return nil, err
			}
			keep, err := RequireBool(value)
			if err != nil {
				return nil, err
			}
			if keep {
				out = append(out, item)
			}
		}
		return out, nil
	case "fold_left":
		if len(vals) != 3 {
			return nil, fmt.Errorf("fold-left expects 3 arguments")
		}
		fn, ok := vals[0].(callable)
		if !ok {
			return nil, fmt.Errorf("fold-left expects a function as first argument")
		}
		list, ok := ToList(vals[2])
		if !ok {
			return nil, fmt.Errorf("fold-left expects a list as third argument")
		}
		acc := vals[1]
		for _, item := range list {
			value, err := fn.Call([]any{acc, item}, ctx)
			if err != nil {
				return nil, err
			}
			acc = value
		}
		return acc, nil
	case "fold_right":
		if len(vals) != 3 {
			return nil, fmt.Errorf("fold-right expects 3 arguments")
		}
		fn, ok := vals[0].(callable)
		if !ok {
			return nil, fmt.Errorf("fold-right expects a function as first argument")
		}
		list, ok := ToList(vals[2])
		if !ok {
			return nil, fmt.Errorf("fold-right expects a list as third argument")
		}
		acc := vals[1]
		for index := len(list) - 1; index >= 0; index-- {
			value, err := fn.Call([]any{list[index], acc}, ctx)
			if err != nil {
				return nil, err
			}
			acc = value
		}
		return acc, nil
	default:
		if fn, ok := lookupUserFunction(ctx, c.Name); ok {
			if len(vals) != len(fn.Params) {
				return nil, fmt.Errorf("%s expects %d arguments", c.Name, len(fn.Params))
			}
			child := cloneContext(ctx)
			for index, param := range fn.Params {
				child[param] = vals[index]
			}
			return fn.Body.Eval(child)
		}
		return nil, fmt.Errorf("unknown function %q", c.Name)
	}
}

func matchPattern(pattern MatchPattern, subject any) (map[string]any, bool) {
	tagged, ok := subject.(TaggedValue)
	if !ok {
		return nil, false
	}
	if tagged.Tag != pattern.Tag || len(tagged.Values) != len(pattern.Vars) {
		return nil, false
	}
	bindings := map[string]any{}
	for index, name := range pattern.Vars {
		bindings[name] = tagged.Values[index]
	}
	return bindings, true
}

func cloneContext(ctx map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range ctx {
		out[key] = value
	}
	return out
}

func lookupUserFunction(ctx map[string]any, name string) (UserFunction, bool) {
	raw, ok := ctx[functionsContextKey]
	if !ok {
		return UserFunction{}, false
	}
	functions, ok := raw.(map[string]UserFunction)
	if !ok {
		return UserFunction{}, false
	}
	fn, ok := functions[name]
	return fn, ok
}
