package expr

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"mar/internal/sexp"
)

type ParserOptions struct {
	AllowedVariables map[string]struct{}
	AllowedFunctions map[string]int
	AllowedCommands  map[string]int
	AllowedRecords   map[string][]string
	AllowedVariants  map[string]int
}

// Parse parses a lispy Mar expression into an executable AST.
func Parse(input string, opts ParserOptions) (Expr, error) {
	node, err := sexp.ParseOne(input)
	if err != nil {
		return nil, err
	}
	return compileNode(node, opts)
}

func compileNode(node sexp.Node, opts ParserOptions) (Expr, error) {
	switch node.Kind {
	case sexp.KindString:
		return Literal{Value: node.Value}, nil
	case sexp.KindNumber:
		if strings.Contains(node.Value, ".") {
			value, err := ParseDecimal(node.Value)
			if err != nil {
				return nil, fmt.Errorf("invalid number %q", node.Value)
			}
			return Literal{Value: value}, nil
		}
		value, err := strconv.ParseInt(node.Value, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q", node.Value)
		}
		return Literal{Value: value}, nil
	case sexp.KindSymbol:
		switch node.Value {
		case "true":
			return Literal{Value: true}, nil
		case "false":
			return Literal{Value: false}, nil
		}
		name := normalizeSymbol(node.Value)
		if _, ok := opts.AllowedVariables[name]; ok {
			return Variable{Name: name}, nil
		}
		if _, ok := opts.AllowedFunctions[name]; ok {
			return FunctionRef{Name: name}, nil
		}
		if _, ok := opts.AllowedRecords[name]; ok {
			return FunctionRef{Name: name}, nil
		}
		if _, ok := opts.AllowedVariants[name]; ok {
			return FunctionRef{Name: name}, nil
		}
		if IsBuiltinValueName(name) {
			return Variable{Name: name}, nil
		}
		return nil, fmt.Errorf("unknown identifier %q", node.Value)
	case sexp.KindList:
		return compileList(node, opts)
	default:
		return nil, fmt.Errorf("unsupported expression node %q", node.Kind)
	}
}

func compileList(node sexp.Node, opts ParserOptions) (Expr, error) {
	if len(node.Children) == 0 {
		return Literal{Value: []any{}}, nil
	}
	head := node.Children[0]
	if head.Kind != sexp.KindSymbol {
		return compileListLiteral(node.Children, opts)
	}

	name := normalizeSymbol(head.Value)
	if _, ok := opts.AllowedVariables[name]; ok && !isBuiltinFunctionName(name) {
		switch head.Value {
		case "if", "cond", "let", "let*", "begin", "lambda", "match", "get", "assoc", "error", "from", "create", "update", "delete", "command", "go", "back", "not", "-", "+", "*", "/", "=", "!=", ">", ">=", "<", "<=", "and", "or":
		default:
			if _, isFunction := opts.AllowedFunctions[name]; !isFunction {
				return compileListLiteral(node.Children, opts)
			}
		}
	}

	op := head.Value
	args := node.Children[1:]
	switch op {
	case "if":
		if len(args) != 3 {
			return nil, fmt.Errorf("if expects 3 arguments")
		}
		condition, err := compileNode(args[0], opts)
		if err != nil {
			return nil, err
		}
		thenExpr, err := compileNode(args[1], opts)
		if err != nil {
			return nil, err
		}
		elseExpr, err := compileNode(args[2], opts)
		if err != nil {
			return nil, err
		}
		return If{Condition: condition, Then: thenExpr, Else: elseExpr}, nil
	case "cond":
		return compileCond(args, opts)
	case "let":
		return compileLet(args, opts, false)
	case "let*":
		return compileLet(args, opts, true)
	case "begin":
		return compileBegin(args, opts)
	case "lambda":
		return compileLambda(args, opts)
	case "match":
		return compileMatch(args, opts)
	case "get":
		return compileGet(args, opts)
	case "assoc":
		return compileAssoc(args, opts)
	case "error":
		if len(args) != 1 {
			return nil, fmt.Errorf("error expects 1 argument")
		}
		if args[0].Kind != sexp.KindString {
			return nil, fmt.Errorf("error expects a string literal")
		}
		return Error{Message: args[0].Value}, nil
	case "from", "create", "update", "delete", "command", "go", "back":
		if err := validateOpaqueOperation(op, args, opts); err != nil {
			return nil, err
		}
		return Opaque{Kind: normalizeSymbol(op), Source: sexp.InlineString(node)}, nil
	case "not":
		if len(args) != 1 {
			return nil, fmt.Errorf("not expects 1 argument")
		}
		right, err := compileNode(args[0], opts)
		if err != nil {
			return nil, err
		}
		return Unary{Op: "not", Right: right}, nil
	case "-", "+", "*", "/", "=", "!=", ">", ">=", "<", "<=", "and", "or":
		return compileOperator(op, args, opts)
	default:
		name := normalizeSymbol(op)
		if name == "matches" {
			return compileRegexMatch(args, opts)
		}
		if fields, ok := opts.AllowedRecords[name]; ok {
			compiledArgs, err := compileRecordConstructorArgs(op, fields, args, opts)
			if err != nil {
				return nil, err
			}
			return RecordConstructor{Name: name, Fields: fields, Args: compiledArgs}, nil
		}
		if arity, ok := opts.AllowedVariants[name]; ok {
			if len(args) != arity {
				return nil, fmt.Errorf("%s expects %d arguments", op, arity)
			}
			compiledArgs := make([]Expr, 0, len(args))
			for _, arg := range args {
				expr, err := compileNode(arg, opts)
				if err != nil {
					return nil, err
				}
				compiledArgs = append(compiledArgs, expr)
			}
			return TaggedConstructor{Tag: name, Args: compiledArgs}, nil
		}
		if !isBuiltinFunctionName(name) {
			arity, ok := opts.AllowedFunctions[name]
			if !ok {
				return nil, fmt.Errorf("unknown function %q", op)
			}
			if arity >= 0 && len(args) != arity {
				return nil, fmt.Errorf("%s expects %d arguments", op, arity)
			}
		}
		compiledArgs := make([]Expr, 0, len(args))
		for _, arg := range args {
			expr, err := compileNode(arg, opts)
			if err != nil {
				return nil, err
			}
			compiledArgs = append(compiledArgs, expr)
		}
		return Call{Name: name, Args: compiledArgs}, nil
	}
}

func compileRegexMatch(args []sexp.Node, opts ParserOptions) (Expr, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("matches expects 2 arguments")
	}
	if args[0].Kind != sexp.KindString {
		return nil, fmt.Errorf("matches expects a static regex literal as first argument")
	}
	pattern, err := regexp.Compile(args[0].Value)
	if err != nil {
		return nil, fmt.Errorf("matches regex is invalid: %w", err)
	}
	text, err := compileNode(args[1], opts)
	if err != nil {
		return nil, err
	}
	return RegexMatch{Pattern: pattern, Text: text}, nil
}

func compileRecordConstructorArgs(recordName string, fields []string, args []sexp.Node, opts ParserOptions) ([]Expr, error) {
	if len(args) != len(fields) {
		return nil, fmt.Errorf("%s expects %d arguments", recordName, len(fields))
	}
	if len(args) == 0 {
		return []Expr{}, nil
	}

	if recordArgsAreNamed(args) {
		byField := map[string]Expr{}
		allowedFields := map[string]struct{}{}
		for _, field := range fields {
			allowedFields[field] = struct{}{}
		}
		for _, arg := range args {
			fieldName := normalizeSymbol(arg.Children[0].Value)
			if _, ok := allowedFields[fieldName]; !ok {
				return nil, fmt.Errorf("%s has no field %q", recordName, arg.Children[0].Value)
			}
			if _, exists := byField[fieldName]; exists {
				return nil, fmt.Errorf("%s field %q is set more than once", recordName, arg.Children[0].Value)
			}
			value, err := compileNode(arg.Children[1], opts)
			if err != nil {
				return nil, err
			}
			byField[fieldName] = value
		}
		compiledArgs := make([]Expr, 0, len(fields))
		for _, field := range fields {
			value, ok := byField[field]
			if !ok {
				return nil, fmt.Errorf("%s missing field %q", recordName, field)
			}
			compiledArgs = append(compiledArgs, value)
		}
		return compiledArgs, nil
	}

	compiledArgs := make([]Expr, 0, len(args))
	for _, arg := range args {
		expr, err := compileNode(arg, opts)
		if err != nil {
			return nil, err
		}
		compiledArgs = append(compiledArgs, expr)
	}
	return compiledArgs, nil
}

func recordArgsAreNamed(args []sexp.Node) bool {
	for _, arg := range args {
		if arg.Kind != sexp.KindList || len(arg.Children) != 2 || arg.Children[0].Kind != sexp.KindSymbol {
			return false
		}
	}
	return true
}

func compileListLiteral(children []sexp.Node, opts ParserOptions) (Expr, error) {
	items := make([]Expr, 0, len(children))
	for _, child := range children {
		expr, err := compileNode(child, opts)
		if err != nil {
			return nil, err
		}
		items = append(items, expr)
	}
	return ListLiteral{Items: items}, nil
}

func compileCond(args []sexp.Node, opts ParserOptions) (Expr, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("cond expects at least 1 clause")
	}
	clauses := make([]CondClause, 0, len(args))
	for index, arg := range args {
		if arg.Kind != sexp.KindList || len(arg.Children) != 2 {
			return nil, fmt.Errorf("cond clauses must look like (test expr)")
		}
		head := arg.Children[0]
		if head.Kind == sexp.KindSymbol && head.Value == "else" {
			if index != len(args)-1 {
				return nil, fmt.Errorf("cond else clause must be last")
			}
			body, err := compileNode(arg.Children[1], opts)
			if err != nil {
				return nil, err
			}
			clauses = append(clauses, CondClause{Else: true, Body: body})
			continue
		}
		test, err := compileNode(head, opts)
		if err != nil {
			return nil, err
		}
		body, err := compileNode(arg.Children[1], opts)
		if err != nil {
			return nil, err
		}
		clauses = append(clauses, CondClause{Test: test, Body: body})
	}
	if len(clauses) == 0 || !clauses[len(clauses)-1].Else {
		return nil, fmt.Errorf("cond requires a final else clause")
	}
	return Cond{Clauses: clauses}, nil
}

func compileLet(args []sexp.Node, opts ParserOptions, sequential bool) (Expr, error) {
	if len(args) != 2 {
		name := "let"
		if sequential {
			name = "let*"
		}
		return nil, fmt.Errorf("%s expects bindings and a body", name)
	}
	if args[0].Kind != sexp.KindList {
		return nil, fmt.Errorf("let bindings must be a list")
	}
	bindings := make([]Binding, 0, len(args[0].Children))
	allowed := copySet(opts.AllowedVariables)
	for _, bindingNode := range args[0].Children {
		if bindingNode.Kind != sexp.KindList || len(bindingNode.Children) != 2 {
			return nil, fmt.Errorf("let bindings must look like (name expr)")
		}
		nameNode := bindingNode.Children[0]
		if nameNode.Kind != sexp.KindSymbol {
			return nil, fmt.Errorf("let binding name must be a symbol")
		}
		name := normalizeSymbol(nameNode.Value)
		bindingOpts := ParserOptions{
			AllowedVariables: allowed,
			AllowedFunctions: opts.AllowedFunctions,
			AllowedCommands:  opts.AllowedCommands,
			AllowedRecords:   opts.AllowedRecords,
			AllowedVariants:  opts.AllowedVariants,
		}
		value, err := compileNode(bindingNode.Children[1], bindingOpts)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, Binding{Name: name, Value: value})
		if sequential {
			allowed[name] = struct{}{}
		}
	}
	bodyOpts := ParserOptions{
		AllowedVariables: allowed,
		AllowedFunctions: opts.AllowedFunctions,
		AllowedCommands:  opts.AllowedCommands,
		AllowedRecords:   opts.AllowedRecords,
		AllowedVariants:  opts.AllowedVariants,
	}
	if !sequential {
		for _, binding := range bindings {
			bodyOpts.AllowedVariables[binding.Name] = struct{}{}
		}
	}
	body, err := compileNode(args[1], bodyOpts)
	if err != nil {
		return nil, err
	}
	return Let{Bindings: bindings, Body: body, Sequential: sequential}, nil
}

func compileBegin(args []sexp.Node, opts ParserOptions) (Expr, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("begin expects at least 1 expression")
	}
	compiled := make([]Expr, 0, len(args))
	for _, arg := range args {
		expr, err := compileNode(arg, opts)
		if err != nil {
			return nil, err
		}
		compiled = append(compiled, expr)
	}
	return Begin{Expressions: compiled}, nil
}

func compileLambda(args []sexp.Node, opts ParserOptions) (Expr, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("lambda expects parameters and a body")
	}
	paramsNode := args[0]
	if paramsNode.Kind != sexp.KindList {
		return nil, fmt.Errorf("lambda parameters must be a list")
	}
	params := make([]string, 0, len(paramsNode.Children))
	allowed := copySet(opts.AllowedVariables)
	for _, paramNode := range paramsNode.Children {
		if paramNode.Kind != sexp.KindSymbol {
			return nil, fmt.Errorf("lambda parameters must be symbols")
		}
		name := normalizeSymbol(paramNode.Value)
		params = append(params, name)
		allowed[name] = struct{}{}
	}
	body, err := compileNode(args[1], ParserOptions{
		AllowedVariables: allowed,
		AllowedFunctions: opts.AllowedFunctions,
		AllowedCommands:  opts.AllowedCommands,
		AllowedRecords:   opts.AllowedRecords,
		AllowedVariants:  opts.AllowedVariants,
	})
	if err != nil {
		return nil, err
	}
	return Lambda{Params: params, Body: body}, nil
}

func compileMatch(args []sexp.Node, opts ParserOptions) (Expr, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("match expects a subject and at least 1 clause")
	}
	subject, err := compileNode(args[0], opts)
	if err != nil {
		return nil, err
	}
	clauses := make([]MatchClause, 0, len(args)-1)
	for _, clauseNode := range args[1:] {
		if clauseNode.Kind != sexp.KindList || len(clauseNode.Children) != 2 {
			return nil, fmt.Errorf("match clauses must look like (pattern expr)")
		}
		pattern, err := compileMatchPattern(clauseNode.Children[0])
		if err != nil {
			return nil, err
		}
		allowed := copySet(opts.AllowedVariables)
		for _, name := range pattern.Vars {
			allowed[name] = struct{}{}
		}
		body, err := compileNode(clauseNode.Children[1], ParserOptions{
			AllowedVariables: allowed,
			AllowedFunctions: opts.AllowedFunctions,
			AllowedCommands:  opts.AllowedCommands,
			AllowedRecords:   opts.AllowedRecords,
			AllowedVariants:  opts.AllowedVariants,
		})
		if err != nil {
			return nil, err
		}
		clauses = append(clauses, MatchClause{Pattern: pattern, Body: body})
	}
	return Match{Subject: subject, Clauses: clauses}, nil
}

func compileMatchPattern(node sexp.Node) (MatchPattern, error) {
	switch node.Kind {
	case sexp.KindSymbol:
		return MatchPattern{Tag: normalizeSymbol(node.Value)}, nil
	case sexp.KindList:
		if len(node.Children) == 0 {
			return MatchPattern{}, fmt.Errorf("match pattern cannot be empty")
		}
		head := node.Children[0]
		if head.Kind != sexp.KindSymbol {
			return MatchPattern{}, fmt.Errorf("match pattern tag must be a symbol")
		}
		pattern := MatchPattern{Tag: normalizeSymbol(head.Value)}
		for _, child := range node.Children[1:] {
			if child.Kind != sexp.KindSymbol {
				return MatchPattern{}, fmt.Errorf("match pattern bindings must be symbols")
			}
			pattern.Vars = append(pattern.Vars, normalizeSymbol(child.Value))
		}
		return pattern, nil
	default:
		return MatchPattern{}, fmt.Errorf("unsupported match pattern")
	}
}

func compileGet(args []sexp.Node, opts ParserOptions) (Expr, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("get expects 2 arguments")
	}
	target, err := compileNode(args[0], opts)
	if err != nil {
		return nil, err
	}
	if args[1].Kind != sexp.KindSymbol {
		return nil, fmt.Errorf("get field must be a symbol")
	}
	return Get{Target: target, Field: normalizeSymbol(args[1].Value)}, nil
}

func compileAssoc(args []sexp.Node, opts ParserOptions) (Expr, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("assoc expects a target and at least 1 update")
	}
	target, err := compileNode(args[0], opts)
	if err != nil {
		return nil, err
	}
	updates := make([]FieldUpdate, 0, len(args)-1)
	for _, updateNode := range args[1:] {
		if updateNode.Kind != sexp.KindList || len(updateNode.Children) != 2 {
			return nil, fmt.Errorf("assoc updates must look like (field expr)")
		}
		nameNode := updateNode.Children[0]
		if nameNode.Kind != sexp.KindSymbol {
			return nil, fmt.Errorf("assoc field must be a symbol")
		}
		value, err := compileNode(updateNode.Children[1], opts)
		if err != nil {
			return nil, err
		}
		updates = append(updates, FieldUpdate{Field: normalizeSymbol(nameNode.Value), Value: value})
	}
	return Assoc{Target: target, Updates: updates}, nil
}

func compileOperator(op string, args []sexp.Node, opts ParserOptions) (Expr, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("%s expects at least 1 argument", op)
	}
	if op == "-" && len(args) == 1 {
		right, err := compileNode(args[0], opts)
		if err != nil {
			return nil, err
		}
		return Unary{Op: "-", Right: right}, nil
	}
	if len(args) < 2 {
		return nil, fmt.Errorf("%s expects at least 2 arguments", op)
	}

	first, err := compileNode(args[0], opts)
	if err != nil {
		return nil, err
	}
	mapped := op
	if op == "=" {
		mapped = "=="
	}
	current := first
	for _, arg := range args[1:] {
		right, err := compileNode(arg, opts)
		if err != nil {
			return nil, err
		}
		current = Binary{Op: mapped, Left: current, Right: right}
	}
	return current, nil
}

func normalizeSymbol(value string) string {
	switch value {
	case "current-user":
		return "current_user"
	}
	return strings.ReplaceAll(value, "-", "_")
}

func isBuiltinFunctionName(name string) bool {
	switch name {
	case "authenticated?", "anonymous?", "same_user?", "has_role?", "contains", "starts_with", "ends_with", "length", "matches", "just", "nothing", "unit", "ok", "err", "cons", "first", "rest", "empty?", "map", "filter", "fold_left", "fold_right":
		return true
	default:
		return false
	}
}

func validateOpaqueOperation(op string, args []sexp.Node, opts ParserOptions) error {
	switch op {
	case "from":
		if len(args) == 0 {
			return fmt.Errorf("from expects an entity")
		}
		if args[0].Kind != sexp.KindSymbol {
			return fmt.Errorf("from expects an entity symbol")
		}
	case "create":
		if len(args) != 2 {
			return fmt.Errorf("create expects an entity and a values list")
		}
		if args[0].Kind != sexp.KindSymbol {
			return fmt.Errorf("create expects an entity symbol")
		}
		if err := validatePairList(args[1], "create values"); err != nil {
			return err
		}
	case "update":
		if len(args) != 3 {
			return fmt.Errorf("update expects an entity, id expression, and a values list")
		}
		if args[0].Kind != sexp.KindSymbol {
			return fmt.Errorf("update expects an entity symbol")
		}
		if err := validatePairList(args[2], "update values"); err != nil {
			return err
		}
	case "delete":
		if len(args) != 2 {
			return fmt.Errorf("delete expects an entity and an id expression")
		}
		if args[0].Kind != sexp.KindSymbol {
			return fmt.Errorf("delete expects an entity symbol")
		}
	case "command":
		if len(args) != 3 {
			return fmt.Errorf("command expects a backend call, a success reply, and a failure reply")
		}
		call := args[0]
		if call.Kind != sexp.KindList || len(call.Children) == 0 {
			return fmt.Errorf("command expects a backend call like (load-orders) or (like-post post-id)")
		}
		if call.Children[0].Kind != sexp.KindSymbol {
			return fmt.Errorf("command backend call must start with a symbol")
		}
		commandName := normalizeSymbol(call.Children[0].Value)
		arity, ok := opts.AllowedCommands[commandName]
		if !ok {
			return fmt.Errorf("command can only call a query or action, got %q", call.Children[0].Value)
		}
		if len(call.Children)-1 != arity {
			return fmt.Errorf("%s expects %d arguments", call.Children[0].Value, arity)
		}
		if args[1].Kind != sexp.KindSymbol {
			return fmt.Errorf("command reply message must be a symbol")
		}
		if args[2].Kind != sexp.KindSymbol {
			return fmt.Errorf("command failure reply must be a symbol")
		}
	case "go":
		if len(args) < 1 {
			return fmt.Errorf("go expects a destination")
		}
		if args[0].Kind != sexp.KindSymbol {
			return fmt.Errorf("go destination must be a symbol")
		}
	case "back":
		if len(args) != 0 {
			return fmt.Errorf("back does not accept arguments")
		}
	}
	return nil
}

func validatePairList(node sexp.Node, label string) error {
	if node.Kind != sexp.KindList {
		return fmt.Errorf("%s must be a list", label)
	}
	for _, child := range node.Children {
		if child.Kind != sexp.KindList || len(child.Children) != 2 {
			return fmt.Errorf("%s entries must look like (field expr)", label)
		}
		if child.Children[0].Kind != sexp.KindSymbol {
			return fmt.Errorf("%s field names must be symbols", label)
		}
	}
	return nil
}

func copySet(input map[string]struct{}) map[string]struct{} {
	out := map[string]struct{}{}
	for key := range input {
		out[key] = struct{}{}
	}
	return out
}
