package expr

import "fmt"

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

type Variable struct {
	Name string
}

func (v Variable) Eval(ctx map[string]any) (any, error) {
	return ctx[v.Name], nil
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
		return !ToBool(right), nil
	case "-":
		n, ok := ToFloat(right)
		if !ok {
			return nil, fmt.Errorf("operator - expects number")
		}
		return -n, nil
	default:
		return nil, fmt.Errorf("unknown unary operator %q", u.Op)
	}
}

type Binary struct {
	Op    string
	Left  Expr
	Right Expr
}

func (b Binary) Eval(ctx map[string]any) (any, error) {
	switch b.Op {
	case "and":
		left, err := b.Left.Eval(ctx)
		if err != nil {
			return nil, err
		}
		if !ToBool(left) {
			return false, nil
		}
		right, err := b.Right.Eval(ctx)
		if err != nil {
			return nil, err
		}
		return ToBool(right), nil
	case "or":
		left, err := b.Left.Eval(ctx)
		if err != nil {
			return nil, err
		}
		if ToBool(left) {
			return true, nil
		}
		right, err := b.Right.Eval(ctx)
		if err != nil {
			return nil, err
		}
		return ToBool(right), nil
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
			return false, nil
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
	case "+":
		if ls, lok := ToString(left); lok {
			rs, _ := ToString(right)
			return ls + rs, nil
		}
		ln, lok := ToFloat(left)
		rn, rok := ToFloat(right)
		if !lok || !rok {
			return nil, fmt.Errorf("operator + expects numbers or strings")
		}
		return ln + rn, nil
	case "-", "*", "/":
		ln, lok := ToFloat(left)
		rn, rok := ToFloat(right)
		if !lok || !rok {
			return nil, fmt.Errorf("operator %s expects numbers", b.Op)
		}
		switch b.Op {
		case "-":
			return ln - rn, nil
		case "*":
			return ln * rn, nil
		default:
			if rn == 0 {
				return nil, fmt.Errorf("division by zero")
			}
			return ln / rn, nil
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
		left, _ := ToString(vals[0])
		right, _ := ToString(vals[1])
		return Contains(left, right), nil
	case "startsWith":
		if len(vals) != 2 {
			return nil, fmt.Errorf("startsWith expects 2 arguments")
		}
		left, _ := ToString(vals[0])
		right, _ := ToString(vals[1])
		return StartsWith(left, right), nil
	case "endsWith":
		if len(vals) != 2 {
			return nil, fmt.Errorf("endsWith expects 2 arguments")
		}
		left, _ := ToString(vals[0])
		right, _ := ToString(vals[1])
		return EndsWith(left, right), nil
	case "len":
		if len(vals) != 1 {
			return nil, fmt.Errorf("len expects 1 argument")
		}
		return Length(vals[0]), nil
	case "matches":
		if len(vals) != 2 {
			return nil, fmt.Errorf("matches expects 2 arguments")
		}
		subj, _ := ToString(vals[0])
		pattern, _ := ToString(vals[1])
		ok, err := Matches(subj, pattern)
		if err != nil {
			return nil, err
		}
		return ok, nil
	case "isRole":
		if len(vals) != 1 {
			return nil, fmt.Errorf("isRole expects 1 argument")
		}
		role, _ := ToString(ctx["auth_role"])
		expected, _ := ToString(vals[0])
		return role != "" && role == expected, nil
	default:
		return nil, fmt.Errorf("unknown function %q", c.Name)
	}
}
