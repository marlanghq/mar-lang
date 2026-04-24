package parser

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"mar/internal/model"
	"mar/internal/sexp"
)

type frontendTypeKind string

const (
	frontendTypeAny       frontendTypeKind = "any"
	frontendTypeNever     frontendTypeKind = "never"
	frontendTypeBool      frontendTypeKind = "bool"
	frontendTypeInt       frontendTypeKind = "int"
	frontendTypeDecimal   frontendTypeKind = "decimal"
	frontendTypeString    frontendTypeKind = "string"
	frontendTypeDate      frontendTypeKind = "date"
	frontendTypeDateTime  frontendTypeKind = "datetime"
	frontendTypeCursor    frontendTypeKind = "cursor"
	frontendTypeEmptyList frontendTypeKind = "empty-list"
	frontendTypeUnknown   frontendTypeKind = "unknown"
	frontendTypeList      frontendTypeKind = "list"
	frontendTypeRecord    frontendTypeKind = "record"
	frontendTypeUnion     frontendTypeKind = "union"
)

type frontendType struct {
	Kind         frontendTypeKind
	Name         string
	Element      *frontendType
	Fields       map[string]frontendType
	FieldOrder   []string
	Variants     map[string][]frontendType
	VariantNames map[string][]string
}

func (t frontendType) String() string {
	switch t.Kind {
	case frontendTypeAny:
		return "any"
	case frontendTypeUnknown:
		return t.Name
	case frontendTypeNever:
		return "never"
	case frontendTypeBool, frontendTypeInt, frontendTypeDecimal, frontendTypeString, frontendTypeDate, frontendTypeDateTime, frontendTypeCursor:
		return string(t.Kind)
	case frontendTypeEmptyList:
		return "()"
	case frontendTypeList:
		if t.Element == nil {
			return "(list any)"
		}
		return fmt.Sprintf("(list %s)", t.Element.String())
	case frontendTypeRecord:
		if t.Name != "" {
			return t.Name
		}
		return "record"
	case frontendTypeUnion:
		if len(t.Variants) == 0 {
			return "union"
		}
		parts := make([]string, 0, len(t.Variants))
		for _, tag := range sortedVariantTags(t.Variants) {
			values := t.Variants[tag]
			if len(values) == 0 {
				parts = append(parts, "("+tag+")")
				continue
			}
			args := make([]string, 0, len(values))
			for _, value := range values {
				args = append(args, value.String())
			}
			parts = append(parts, fmt.Sprintf("(%s %s)", tag, strings.Join(args, " ")))
		}
		return strings.Join(parts, " | ")
	default:
		return string(t.Kind)
	}
}

func anyFrontendType() frontendType {
	return frontendType{Kind: frontendTypeAny}
}

func unknownFrontendType(name string) frontendType {
	return frontendType{Kind: frontendTypeUnknown, Name: name}
}

func neverFrontendType() frontendType {
	return frontendType{Kind: frontendTypeNever}
}

func boolFrontendType() frontendType {
	return frontendType{Kind: frontendTypeBool}
}

func intFrontendType() frontendType {
	return frontendType{Kind: frontendTypeInt}
}

func decimalFrontendType() frontendType {
	return frontendType{Kind: frontendTypeDecimal}
}

func stringFrontendType() frontendType {
	return frontendType{Kind: frontendTypeString}
}

func dateFrontendType() frontendType {
	return frontendType{Kind: frontendTypeDate}
}

func dateTimeFrontendType() frontendType {
	return frontendType{Kind: frontendTypeDateTime}
}

func cursorFrontendType() frontendType {
	return frontendType{Kind: frontendTypeCursor}
}

func listFrontendType(element frontendType) frontendType {
	copy := element
	return frontendType{Kind: frontendTypeList, Element: &copy}
}

func emptyListFrontendType() frontendType {
	return frontendType{Kind: frontendTypeEmptyList}
}

func recordFrontendType(name string, fields map[string]frontendType) frontendType {
	return recordFrontendTypeWithOrder(name, fields, nil)
}

func recordFrontendTypeWithOrder(name string, fields map[string]frontendType, fieldOrder []string) frontendType {
	cloned := map[string]frontendType{}
	for key, value := range fields {
		cloned[key] = value
	}
	order := make([]string, 0, len(fields))
	if len(fieldOrder) > 0 {
		order = append(order, fieldOrder...)
	} else {
		for key := range fields {
			order = append(order, key)
		}
		sort.Strings(order)
	}
	return frontendType{Kind: frontendTypeRecord, Name: name, Fields: cloned, FieldOrder: order}
}

func taggedFrontendType(tag string, values ...frontendType) frontendType {
	return namedTaggedFrontendType(tag, nil, values...)
}

func namedTaggedFrontendType(tag string, names []string, values ...frontendType) frontendType {
	cloned := make([]frontendType, 0, len(values))
	cloned = append(cloned, values...)
	variantNames := map[string][]string{}
	if len(names) > 0 {
		clonedNames := make([]string, 0, len(names))
		clonedNames = append(clonedNames, names...)
		variantNames[tag] = clonedNames
	}
	return frontendType{Kind: frontendTypeUnion, Variants: map[string][]frontendType{tag: cloned}, VariantNames: variantNames}
}

func frontendMaybeType(inner frontendType) frontendType {
	return mergeFrontendTypes(namedTaggedFrontendType("just", []string{"value"}, inner), taggedFrontendType("nothing"))
}

func frontendResultType(errType frontendType, okType frontendType) frontendType {
	return mergeFrontendTypes(namedTaggedFrontendType("err", []string{"error"}, errType), namedTaggedFrontendType("ok", []string{"value"}, okType))
}

func frontendUnitType() frontendType {
	return taggedFrontendType("unit")
}

func currentUserFrontendType() frontendType {
	value, err := unionFrontendTypeFromVariants([]model.TypeVariant{
		{
			Name: "authenticated",
			Fields: []model.RecordField{
				{Name: "id", Type: "int"},
				{Name: "email", Type: "string"},
				{Name: "role", Type: "string"},
			},
		},
		{Name: "anonymous"},
	}, func(_ model.TypeVariant, field model.RecordField, _ int) (frontendType, error) {
		return parseBuiltinFrontendType(field.Type)
	})
	if err != nil {
		return anyFrontendType()
	}
	return value
}

func parseBuiltinFrontendType(name string) (frontendType, error) {
	switch name {
	case "int":
		return intFrontendType(), nil
	case "string":
		return stringFrontendType(), nil
	case "bool":
		return boolFrontendType(), nil
	case "decimal":
		return decimalFrontendType(), nil
	case "date":
		return dateFrontendType(), nil
	case "datetime":
		return dateTimeFrontendType(), nil
	case "cursor":
		return cursorFrontendType(), nil
	default:
		return anyFrontendType(), nil
	}
}

func unionFrontendTypeFromVariants(variants []model.TypeVariant, resolve func(model.TypeVariant, model.RecordField, int) (frontendType, error)) (frontendType, error) {
	var out frontendType
	for variantIndex, variant := range variants {
		payload := make([]frontendType, 0, len(variant.Fields))
		names := make([]string, 0, len(variant.Fields))
		for fieldIndex, field := range variant.Fields {
			value, err := resolve(variant, field, fieldIndex)
			if err != nil {
				return frontendType{}, err
			}
			payload = append(payload, value)
			names = append(names, field.Name)
		}
		current := namedTaggedFrontendType(variant.Name, names, payload...)
		if variantIndex == 0 {
			out = current
			continue
		}
		out = mergeFrontendTypes(out, current)
	}
	if len(variants) == 0 {
		return anyFrontendType(), nil
	}
	return out, nil
}

type frontendTypeEnv map[string]frontendType

type frontendTypeChecker struct {
	functions    map[string]*model.Function
	records      map[string]*model.Record
	types        map[string]*model.EnumType
	entities     map[string]*model.Entity
	screenParams map[string][]frontendType
	screenMsgs   map[string]map[string][]frontendType

	namedTypeCache      map[string]frontendType
	namedTypeStack      map[string]bool
	functionStack       map[string]bool
	functionConstraints map[string]map[string]frontendType
}

func newFrontendTypeChecker(functions map[string]*model.Function, records map[string]*model.Record, types map[string]*model.EnumType, entities map[string]*model.Entity, screenParams map[string][]frontendType, screenMsgs map[string]map[string][]frontendType) *frontendTypeChecker {
	return &frontendTypeChecker{
		functions:           functions,
		records:             records,
		types:               types,
		entities:            entities,
		screenParams:        screenParams,
		screenMsgs:          screenMsgs,
		namedTypeCache:      map[string]frontendType{},
		namedTypeStack:      map[string]bool{},
		functionStack:       map[string]bool{},
		functionConstraints: map[string]map[string]frontendType{},
	}
}

func (c *frontendTypeChecker) inferInitModelType(screen model.FrontendScreen) (frontendType, error) {
	if strings.TrimSpace(screen.InitExpression) == "" {
		return anyFrontendType(), nil
	}
	node, err := sexp.ParseOne(screen.InitExpression)
	if err != nil {
		return frontendType{}, err
	}
	return c.inferTransitionModelType(node, c.baseEnv(screen))
}

func (c *frontendTypeChecker) validateUpdate(screen model.FrontendScreen, modelType frontendType) error {
	if strings.TrimSpace(screen.UpdateBody) == "" {
		return nil
	}
	node, err := sexp.ParseOne(screen.UpdateBody)
	if err != nil {
		return err
	}
	env := c.baseEnv(screen)
	if screen.UpdateMessage != "" {
		env[screen.UpdateMessage] = c.screenMessageType(screen)
	}
	if screen.UpdateModel != "" {
		env[screen.UpdateModel] = modelType
	}
	updateModelType, err := c.inferTransitionModelType(node, env)
	if err != nil {
		return err
	}
	if !frontendAssignable(updateModelType, modelType) {
		return fmt.Errorf("screen update must keep model type %s, got %s", modelType.String(), updateModelType.String())
	}
	return nil
}

func (c *frontendTypeChecker) baseEnv(screen model.FrontendScreen) frontendTypeEnv {
	env := frontendTypeEnv{
		"current_user": currentUserFrontendType(),
	}
	for index, param := range screen.Parameters {
		env[param] = c.screenParameterType(screen.Name, index)
	}
	return env
}

func (c *frontendTypeChecker) screenParameterType(screenName string, index int) frontendType {
	types := c.screenParams[screenName]
	if index < 0 || index >= len(types) {
		return anyFrontendType()
	}
	value := types[index]
	if value.Kind == "" {
		return anyFrontendType()
	}
	return value
}

func (c *frontendTypeChecker) screenMessageType(screen model.FrontendScreen) frontendType {
	payloads := map[string][]frontendType{}
	if c != nil && c.screenMsgs != nil {
		payloads = c.screenMsgs[screen.Name]
	}
	variants := make([]model.TypeVariant, 0, len(screen.Messages))
	for _, message := range screen.Messages {
		variant := model.TypeVariant{Name: message.Name}
		for _, param := range message.Parameters {
			variant.Fields = append(variant.Fields, model.RecordField{Name: param})
		}
		variants = append(variants, variant)
	}
	if len(screen.Messages) == 0 {
		return anyFrontendType()
	}
	value, err := unionFrontendTypeFromVariants(variants, func(variant model.TypeVariant, _ model.RecordField, fieldIndex int) (frontendType, error) {
		if inferred, ok := payloads[variant.Name]; ok && fieldIndex < len(inferred) && inferred[fieldIndex].Kind != "" {
			return inferred[fieldIndex], nil
		}
		return anyFrontendType(), nil
	})
	if err != nil {
		return anyFrontendType()
	}
	return value
}

func (c *frontendTypeChecker) inferTransitionModelType(node sexp.Node, env frontendTypeEnv) (frontendType, error) {
	if node.Kind == sexp.KindList && len(node.Children) > 0 && node.Children[0].Kind == sexp.KindSymbol {
		switch node.Children[0].Value {
		case "if":
			condition, err := c.inferExprType(node.Children[1], env)
			if err != nil {
				return frontendType{}, err
			}
			if !frontendAssignable(condition, boolFrontendType()) {
				return frontendType{}, fmt.Errorf("if condition must be bool, got %s", condition.String())
			}
			thenType, err := c.inferTransitionModelType(node.Children[2], env)
			if err != nil {
				return frontendType{}, err
			}
			elseType, err := c.inferTransitionModelType(node.Children[3], env)
			if err != nil {
				return frontendType{}, err
			}
			merged, err := mergeCompatibleFrontendTypes(thenType, elseType)
			if err != nil {
				return frontendType{}, err
			}
			if frontendTypeHasUnknown(merged) {
				return frontendType{}, fmt.Errorf("if result type could not be inferred")
			}
			return merged, nil
		case "cond":
			if len(node.Children) < 2 {
				return frontendType{}, fmt.Errorf("cond expects at least 1 clause")
			}
			var merged frontendType
			var hasType bool
			clauses := node.Children[1:]
			for index, clause := range clauses {
				if clause.Kind != sexp.KindList || len(clause.Children) != 2 {
					return frontendType{}, fmt.Errorf("cond clauses must look like (test expr)")
				}
				head := clause.Children[0]
				isElse := head.Kind == sexp.KindSymbol && head.Value == "else"
				if isElse && index != len(clauses)-1 {
					return frontendType{}, fmt.Errorf("cond else clause must be last")
				}
				if !isElse {
					testType, err := c.inferExprType(head, env)
					if err != nil {
						return frontendType{}, err
					}
					if !frontendAssignable(testType, boolFrontendType()) {
						return frontendType{}, fmt.Errorf("cond test must be bool, got %s", testType.String())
					}
				}
				bodyType, err := c.inferTransitionModelType(clause.Children[1], env)
				if err != nil {
					return frontendType{}, err
				}
				if !hasType {
					merged = bodyType
					hasType = true
					continue
				}
				merged, err = mergeCompatibleFrontendTypes(merged, bodyType)
				if err != nil {
					return frontendType{}, err
				}
			}
			if !condHasFinalElse(clauses) {
				return frontendType{}, fmt.Errorf("cond requires a final else clause")
			}
			if frontendTypeHasUnknown(merged) {
				return frontendType{}, fmt.Errorf("cond result type could not be inferred")
			}
			return merged, nil
		case "match":
			return c.inferMatchTransitionModelType(node.Children[1:], env)
		case "let":
			return c.inferLetTransitionModelType(node.Children[1:], env, false)
		case "let*":
			return c.inferLetTransitionModelType(node.Children[1:], env, true)
		case "begin":
			if len(node.Children) < 2 {
				return frontendType{}, fmt.Errorf("begin expects at least 1 expression")
			}
			for _, exprNode := range node.Children[1 : len(node.Children)-1] {
				if _, err := c.inferExprType(exprNode, env); err != nil {
					return frontendType{}, err
				}
			}
			return c.inferTransitionModelType(node.Children[len(node.Children)-1], env)
		}
	}

	if node.Kind != sexp.KindList || len(node.Children) != 2 {
		return frontendType{}, fmt.Errorf("screen transition must return (model effects)")
	}
	return c.inferExprType(node.Children[0], env)
}

func (c *frontendTypeChecker) inferExprType(node sexp.Node, env frontendTypeEnv) (frontendType, error) {
	switch node.Kind {
	case sexp.KindString:
		return stringFrontendType(), nil
	case sexp.KindNumber:
		if strings.Contains(node.Value, ".") {
			return decimalFrontendType(), nil
		}
		return intFrontendType(), nil
	case sexp.KindSymbol:
		switch node.Value {
		case "true", "false":
			return boolFrontendType(), nil
		}
		name := canonicalFieldName(node.Value)
		if value, ok := env[name]; ok {
			return value, nil
		}
		if strings.Contains(node.Value, ".") {
			return c.inferDottedSymbolType(node.Value, env)
		}
		return frontendType{}, fmt.Errorf("unknown identifier %q", node.Value)
	case sexp.KindList:
		if len(node.Children) == 0 {
			return emptyListFrontendType(), nil
		}
		head := node.Children[0]
		if head.Kind != sexp.KindSymbol {
			return c.inferListLiteralType(node.Children, env)
		}
		switch head.Value {
		case "if":
			return c.inferIfExprType(node.Children[1:], env)
		case "cond":
			return c.inferCondExprType(node.Children[1:], env)
		case "let":
			return c.inferLetExprType(node.Children[1:], env, false)
		case "let*":
			return c.inferLetExprType(node.Children[1:], env, true)
		case "begin":
			if len(node.Children) < 2 {
				return frontendType{}, fmt.Errorf("begin expects at least 1 expression")
			}
			var out frontendType
			for _, child := range node.Children[1:] {
				value, err := c.inferExprType(child, env)
				if err != nil {
					return frontendType{}, err
				}
				out = value
			}
			return out, nil
		case "match":
			return c.inferMatchExprType(node.Children[1:], env)
		case "get":
			return c.inferGetExprType(node.Children[1:], env)
		case "assoc":
			return c.inferAssocExprType(node.Children[1:], env)
		case "error":
			if len(node.Children) != 2 || node.Children[1].Kind != sexp.KindString {
				return frontendType{}, fmt.Errorf("error expects a string literal")
			}
			return neverFrontendType(), nil
		case "not":
			if len(node.Children) != 2 {
				return frontendType{}, fmt.Errorf("not expects 1 argument")
			}
			value, err := c.inferExprType(node.Children[1], env)
			if err != nil {
				return frontendType{}, err
			}
			if !frontendAssignable(value, boolFrontendType()) {
				return frontendType{}, fmt.Errorf("not expects bool, got %s", value.String())
			}
			return boolFrontendType(), nil
		case "=", "!=", ">", ">=", "<", "<=", "and", "or", "+", "-", "*", "/":
			return c.inferOperatorType(head.Value, node.Children[1:], env)
		case "contains", "starts-with", "ends-with":
			if len(node.Children) != 3 {
				return frontendType{}, fmt.Errorf("%s expects 2 arguments", head.Value)
			}
			for _, child := range node.Children[1:] {
				value, err := c.inferExprType(child, env)
				if err != nil {
					return frontendType{}, err
				}
				if !frontendAssignable(value, stringFrontendType()) {
					return frontendType{}, fmt.Errorf("%s expects string arguments, got %s", head.Value, value.String())
				}
			}
			return boolFrontendType(), nil
		case "matches":
			if len(node.Children) != 3 {
				return frontendType{}, fmt.Errorf("matches expects 2 arguments")
			}
			if node.Children[1].Kind != sexp.KindString {
				return frontendType{}, fmt.Errorf("matches expects a static regex literal as first argument")
			}
			if _, err := regexp.Compile(node.Children[1].Value); err != nil {
				return frontendType{}, fmt.Errorf("matches regex is invalid: %w", err)
			}
			value, err := c.inferExprType(node.Children[2], env)
			if err != nil {
				return frontendType{}, err
			}
			if !frontendAssignable(value, stringFrontendType()) {
				return frontendType{}, fmt.Errorf("matches expects string arguments, got %s", value.String())
			}
			return boolFrontendType(), nil
		case "length":
			if len(node.Children) != 2 {
				return frontendType{}, fmt.Errorf("length expects 1 argument")
			}
			value, err := c.inferExprType(node.Children[1], env)
			if err != nil {
				return frontendType{}, err
			}
			if !frontendLengthSupported(value) {
				return frontendType{}, fmt.Errorf("length expects string or list, got %s", value.String())
			}
			return intFrontendType(), nil
		case "just":
			if len(node.Children) != 2 {
				return frontendType{}, fmt.Errorf("just expects 1 argument")
			}
			value, err := c.inferExprType(node.Children[1], env)
			if err != nil {
				return frontendType{}, err
			}
			return frontendMaybeType(value), nil
		case "nothing":
			if len(node.Children) != 1 {
				return frontendType{}, fmt.Errorf("nothing expects 0 arguments")
			}
			return frontendMaybeType(unknownFrontendType("value")), nil
		case "unit":
			if len(node.Children) != 1 {
				return frontendType{}, fmt.Errorf("unit expects 0 arguments")
			}
			return frontendUnitType(), nil
		case "ok":
			if len(node.Children) != 2 {
				return frontendType{}, fmt.Errorf("ok expects 1 argument")
			}
			value, err := c.inferExprType(node.Children[1], env)
			if err != nil {
				return frontendType{}, err
			}
			return frontendResultType(unknownFrontendType("err"), value), nil
		case "err":
			if len(node.Children) != 2 {
				return frontendType{}, fmt.Errorf("err expects 1 argument")
			}
			value, err := c.inferExprType(node.Children[1], env)
			if err != nil {
				return frontendType{}, err
			}
			return frontendResultType(value, unknownFrontendType("ok")), nil
		case "command", "go", "back", "from", "create", "update", "delete":
			return frontendType{}, fmt.Errorf("%s can only appear in the appropriate screen/runtime position", head.Value)
		default:
			return c.inferCallType(head.Value, node.Children[1:], env)
		}
	default:
		return frontendType{}, fmt.Errorf("unsupported expression node %q", node.Kind)
	}
}

func (c *frontendTypeChecker) inferExprTypeWithExpected(node sexp.Node, expected frontendType, env frontendTypeEnv) (frontendType, error) {
	if node.Kind == sexp.KindList && len(node.Children) == 0 && expected.Kind == frontendTypeList {
		return expected, nil
	}
	if node.Kind == sexp.KindList && len(node.Children) > 0 && node.Children[0].Kind == sexp.KindSymbol {
		switch node.Children[0].Value {
		case "just":
			payload, ok := frontendMaybePayload(expected)
			if !ok {
				break
			}
			if len(node.Children) != 2 {
				return frontendType{}, fmt.Errorf("just expects 1 argument")
			}
			value, err := c.inferExprTypeWithExpected(node.Children[1], payload, env)
			if err != nil {
				return frontendType{}, err
			}
			if !frontendAssignable(value, payload) {
				return frontendType{}, fmt.Errorf("just expects %s, got %s", payload.String(), value.String())
			}
			return expected, nil
		case "nothing":
			if _, ok := frontendMaybePayload(expected); !ok {
				break
			}
			if len(node.Children) != 1 {
				return frontendType{}, fmt.Errorf("nothing expects 0 arguments")
			}
			return expected, nil
		case "ok":
			_, okPayload, ok := frontendResultPayloads(expected)
			if !ok {
				break
			}
			if len(node.Children) != 2 {
				return frontendType{}, fmt.Errorf("ok expects 1 argument")
			}
			value, err := c.inferExprTypeWithExpected(node.Children[1], okPayload, env)
			if err != nil {
				return frontendType{}, err
			}
			if !frontendAssignable(value, okPayload) {
				return frontendType{}, fmt.Errorf("ok expects %s, got %s", okPayload.String(), value.String())
			}
			return expected, nil
		case "err":
			errPayload, _, ok := frontendResultPayloads(expected)
			if !ok {
				break
			}
			if len(node.Children) != 2 {
				return frontendType{}, fmt.Errorf("err expects 1 argument")
			}
			value, err := c.inferExprTypeWithExpected(node.Children[1], errPayload, env)
			if err != nil {
				return frontendType{}, err
			}
			if !frontendAssignable(value, errPayload) {
				return frontendType{}, fmt.Errorf("err expects %s, got %s", errPayload.String(), value.String())
			}
			return expected, nil
		}
	}
	return c.inferExprType(node, env)
}

func (c *frontendTypeChecker) inferDottedSymbolType(raw string, env frontendTypeEnv) (frontendType, error) {
	parts := strings.Split(raw, ".")
	if len(parts) == 0 {
		return frontendType{}, fmt.Errorf("unknown identifier %q", raw)
	}
	current, ok := env[canonicalFieldName(parts[0])]
	if !ok {
		return frontendType{}, fmt.Errorf("unknown identifier %q", raw)
	}
	for _, part := range parts[1:] {
		field := canonicalFieldName(part)
		if current.Kind == frontendTypeAny {
			return anyFrontendType(), nil
		}
		if current.Kind != frontendTypeRecord {
			return frontendType{}, fmt.Errorf("get expects a record-like value, got %s", current.String())
		}
		next, ok := current.Fields[field]
		if !ok {
			return frontendType{}, fmt.Errorf("record %s has no field %q", current.Name, field)
		}
		current = next
	}
	return current, nil
}

func (c *frontendTypeChecker) inferListLiteralType(nodes []sexp.Node, env frontendTypeEnv) (frontendType, error) {
	if len(nodes) == 0 {
		return emptyListFrontendType(), nil
	}
	current := anyFrontendType()
	for index, child := range nodes {
		value, err := c.inferExprType(child, env)
		if err != nil {
			return frontendType{}, err
		}
		if index == 0 {
			current = value
			continue
		}
		merged, err := mergeCompatibleFrontendTypes(current, value)
		if err != nil {
			return frontendType{}, fmt.Errorf("list literal has incompatible element types: %w", err)
		}
		current = merged
	}
	if frontendTypeHasUnknown(current) {
		return frontendType{}, fmt.Errorf("list element type could not be inferred")
	}
	return listFrontendType(current), nil
}

func (c *frontendTypeChecker) inferIfExprType(args []sexp.Node, env frontendTypeEnv) (frontendType, error) {
	if len(args) != 3 {
		return frontendType{}, fmt.Errorf("if expects 3 arguments")
	}
	condition, err := c.inferExprType(args[0], env)
	if err != nil {
		return frontendType{}, err
	}
	if !frontendAssignable(condition, boolFrontendType()) {
		return frontendType{}, fmt.Errorf("if condition must be bool, got %s", condition.String())
	}
	thenType, err := c.inferExprType(args[1], env)
	if err != nil {
		return frontendType{}, err
	}
	elseType, err := c.inferExprType(args[2], env)
	if err != nil {
		return frontendType{}, err
	}
	merged, err := mergeCompatibleFrontendTypes(thenType, elseType)
	if err != nil {
		return frontendType{}, err
	}
	if frontendTypeHasUnknown(merged) {
		return frontendType{}, fmt.Errorf("if result type could not be inferred")
	}
	return merged, nil
}

func (c *frontendTypeChecker) inferCondExprType(clauses []sexp.Node, env frontendTypeEnv) (frontendType, error) {
	if len(clauses) == 0 {
		return frontendType{}, fmt.Errorf("cond expects at least 1 clause")
	}
	var merged frontendType
	var hasType bool
	for index, clause := range clauses {
		if clause.Kind != sexp.KindList || len(clause.Children) != 2 {
			return frontendType{}, fmt.Errorf("cond clauses must look like (test expr)")
		}
		head := clause.Children[0]
		isElse := head.Kind == sexp.KindSymbol && head.Value == "else"
		if isElse && index != len(clauses)-1 {
			return frontendType{}, fmt.Errorf("cond else clause must be last")
		}
		if !isElse {
			testType, err := c.inferExprType(head, env)
			if err != nil {
				return frontendType{}, err
			}
			if !frontendAssignable(testType, boolFrontendType()) {
				return frontendType{}, fmt.Errorf("cond test must be bool, got %s", testType.String())
			}
		}
		bodyType, err := c.inferExprType(clause.Children[1], env)
		if err != nil {
			return frontendType{}, err
		}
		if !hasType {
			merged = bodyType
			hasType = true
			continue
		}
		merged, err = mergeCompatibleFrontendTypes(merged, bodyType)
		if err != nil {
			return frontendType{}, err
		}
	}
	if !condHasFinalElse(clauses) {
		return frontendType{}, fmt.Errorf("cond requires a final else clause")
	}
	if frontendTypeHasUnknown(merged) {
		return frontendType{}, fmt.Errorf("cond result type could not be inferred")
	}
	return merged, nil
}

func condHasFinalElse(clauses []sexp.Node) bool {
	if len(clauses) == 0 {
		return false
	}
	last := clauses[len(clauses)-1]
	return last.Kind == sexp.KindList &&
		len(last.Children) == 2 &&
		last.Children[0].Kind == sexp.KindSymbol &&
		last.Children[0].Value == "else"
}

func (c *frontendTypeChecker) inferLetExprType(args []sexp.Node, env frontendTypeEnv, sequential bool) (frontendType, error) {
	child, err := c.bindLetEnv(args, env, sequential)
	if err != nil {
		return frontendType{}, err
	}
	return c.inferExprType(args[1], child)
}

func (c *frontendTypeChecker) inferLetTransitionModelType(args []sexp.Node, env frontendTypeEnv, sequential bool) (frontendType, error) {
	child, err := c.bindLetEnv(args, env, sequential)
	if err != nil {
		return frontendType{}, err
	}
	return c.inferTransitionModelType(args[1], child)
}

func (c *frontendTypeChecker) bindLetEnv(args []sexp.Node, env frontendTypeEnv, sequential bool) (frontendTypeEnv, error) {
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
	child := cloneFrontendEnv(env)
	if sequential {
		for _, binding := range args[0].Children {
			if binding.Kind != sexp.KindList || len(binding.Children) != 2 || binding.Children[0].Kind != sexp.KindSymbol {
				return nil, fmt.Errorf("let bindings must look like (name expr)")
			}
			value, err := c.inferExprType(binding.Children[1], child)
			if err != nil {
				return nil, err
			}
			name := canonicalFieldName(binding.Children[0].Value)
			if frontendTypeHasUnknown(value) {
				return nil, fmt.Errorf("let binding %s type could not be inferred", name)
			}
			child[name] = value
		}
		return child, nil
	}
	pending := map[string]frontendType{}
	for _, binding := range args[0].Children {
		if binding.Kind != sexp.KindList || len(binding.Children) != 2 || binding.Children[0].Kind != sexp.KindSymbol {
			return nil, fmt.Errorf("let bindings must look like (name expr)")
		}
		value, err := c.inferExprType(binding.Children[1], env)
		if err != nil {
			return nil, err
		}
		name := canonicalFieldName(binding.Children[0].Value)
		if frontendTypeHasUnknown(value) {
			return nil, fmt.Errorf("let binding %s type could not be inferred", name)
		}
		pending[name] = value
	}
	for key, value := range pending {
		child[key] = value
	}
	return child, nil
}

func (c *frontendTypeChecker) inferMatchExprType(args []sexp.Node, env frontendTypeEnv) (frontendType, error) {
	subjectType, clauses, err := c.prepareMatch(args, env)
	if err != nil {
		return frontendType{}, err
	}
	var merged frontendType
	var hasType bool
	for _, clause := range clauses {
		bodyType, err := c.inferExprType(clause.Body, clause.Env)
		if err != nil {
			return frontendType{}, err
		}
		if !hasType {
			merged = bodyType
			hasType = true
			continue
		}
		merged, err = mergeCompatibleFrontendTypes(merged, bodyType)
		if err != nil {
			return frontendType{}, err
		}
	}
	if !hasType {
		return subjectType, nil
	}
	if frontendTypeHasUnknown(merged) {
		return frontendType{}, fmt.Errorf("match result type could not be inferred")
	}
	return merged, nil
}

func (c *frontendTypeChecker) inferMatchTransitionModelType(args []sexp.Node, env frontendTypeEnv) (frontendType, error) {
	_, clauses, err := c.prepareMatch(args, env)
	if err != nil {
		return frontendType{}, err
	}
	var merged frontendType
	var hasType bool
	for _, clause := range clauses {
		bodyType, err := c.inferTransitionModelType(clause.Body, clause.Env)
		if err != nil {
			return frontendType{}, err
		}
		if !hasType {
			merged = bodyType
			hasType = true
			continue
		}
		merged, err = mergeCompatibleFrontendTypes(merged, bodyType)
		if err != nil {
			return frontendType{}, err
		}
	}
	if frontendTypeHasUnknown(merged) {
		return frontendType{}, fmt.Errorf("match result type could not be inferred")
	}
	return merged, nil
}

type preparedMatchClause struct {
	Body sexp.Node
	Env  frontendTypeEnv
}

func (c *frontendTypeChecker) prepareMatch(args []sexp.Node, env frontendTypeEnv) (frontendType, []preparedMatchClause, error) {
	if len(args) < 2 {
		return frontendType{}, nil, fmt.Errorf("match expects a subject and at least 1 clause")
	}
	subjectType, err := c.inferExprType(args[0], env)
	if err != nil {
		return frontendType{}, nil, err
	}
	if frontendTypeHasUnknown(subjectType) {
		return frontendType{}, nil, fmt.Errorf("match subject type could not be inferred")
	}
	if subjectType.Kind == frontendTypeAny {
		prepared := make([]preparedMatchClause, 0, len(args)-1)
		for _, clause := range args[1:] {
			if clause.Kind != sexp.KindList || len(clause.Children) != 2 {
				return frontendType{}, nil, fmt.Errorf("match clauses must look like (pattern expr)")
			}
			bindings, err := wildcardMatchBindings(clause.Children[0])
			if err != nil {
				return frontendType{}, nil, err
			}
			child := cloneFrontendEnv(env)
			for key, value := range bindings {
				child[key] = value
			}
			prepared = append(prepared, preparedMatchClause{Body: clause.Children[1], Env: child})
		}
		return subjectType, prepared, nil
	}
	if subjectType.Kind != frontendTypeUnion {
		return frontendType{}, nil, fmt.Errorf("match expects a tagged value, got %s", subjectType.String())
	}
	seen := map[string]bool{}
	prepared := make([]preparedMatchClause, 0, len(args)-1)
	for _, clause := range args[1:] {
		if clause.Kind != sexp.KindList || len(clause.Children) != 2 {
			return frontendType{}, nil, fmt.Errorf("match clauses must look like (pattern expr)")
		}
		pattern, err := c.matchPatternFromNode(clause.Children[0], subjectType)
		if err != nil {
			return frontendType{}, nil, err
		}
		if seen[pattern.Tag] {
			return frontendType{}, nil, fmt.Errorf("match repeats clause %q", pattern.Tag)
		}
		seen[pattern.Tag] = true
		child := cloneFrontendEnv(env)
		for key, value := range pattern.Bindings {
			child[key] = value
		}
		prepared = append(prepared, preparedMatchClause{Body: clause.Children[1], Env: child})
	}
	missing := missingFrontendTags(subjectType.Variants, seen)
	if len(missing) > 0 {
		return frontendType{}, nil, fmt.Errorf("match is not exhaustive; missing %s", strings.Join(missing, ", "))
	}
	return subjectType, prepared, nil
}

type frontendMatchPattern struct {
	Tag      string
	Bindings map[string]frontendType
}

func (c *frontendTypeChecker) matchPatternFromNode(node sexp.Node, subjectType frontendType) (frontendMatchPattern, error) {
	switch node.Kind {
	case sexp.KindSymbol:
		tag := canonicalFieldName(node.Value)
		payload, ok := subjectType.Variants[tag]
		if !ok {
			return frontendMatchPattern{}, fmt.Errorf("match pattern %q is not valid for %s", node.Value, subjectType.String())
		}
		if len(payload) != 0 {
			return frontendMatchPattern{}, c.matchPatternArityError(node.Value, payload, subjectType)
		}
		return frontendMatchPattern{Tag: tag, Bindings: map[string]frontendType{}}, nil
	case sexp.KindList:
		if len(node.Children) == 0 || node.Children[0].Kind != sexp.KindSymbol {
			return frontendMatchPattern{}, fmt.Errorf("match pattern tag must be a symbol")
		}
		tag := canonicalFieldName(node.Children[0].Value)
		payload, ok := subjectType.Variants[tag]
		if !ok {
			return frontendMatchPattern{}, fmt.Errorf("match pattern %q is not valid for %s", node.Children[0].Value, subjectType.String())
		}
		if len(payload) != len(node.Children)-1 {
			return frontendMatchPattern{}, c.matchPatternArityError(node.Children[0].Value, payload, subjectType)
		}
		bindings := map[string]frontendType{}
		for index, child := range node.Children[1:] {
			if child.Kind != sexp.KindSymbol {
				return frontendMatchPattern{}, fmt.Errorf("match pattern bindings must be symbols")
			}
			bindings[canonicalFieldName(child.Value)] = payload[index]
		}
		return frontendMatchPattern{Tag: tag, Bindings: bindings}, nil
	default:
		return frontendMatchPattern{}, fmt.Errorf("unsupported match pattern")
	}
}

func (c *frontendTypeChecker) matchPatternArityError(rawTag string, payload []frontendType, subjectType frontendType) error {
	tag := canonicalFieldName(rawTag)
	names := subjectType.VariantNames[tag]
	if len(names) != len(payload) {
		names = genericVariantPayloadNames(len(payload))
	}
	if len(names) == 0 {
		return fmt.Errorf("match pattern %q expects 0 values", rawTag)
	}
	return fmt.Errorf("match pattern %q expects %d values: %s", rawTag, len(payload), strings.Join(names, " "))
}

func genericVariantPayloadNames(count int) []string {
	out := make([]string, 0, count)
	for index := 1; index <= count; index++ {
		out = append(out, fmt.Sprintf("value%d", index))
	}
	return out
}

func (c *frontendTypeChecker) inferGetExprType(args []sexp.Node, env frontendTypeEnv) (frontendType, error) {
	if len(args) != 2 || args[1].Kind != sexp.KindSymbol {
		return frontendType{}, fmt.Errorf("get expects 2 arguments")
	}
	target, err := c.inferExprType(args[0], env)
	if err != nil {
		return frontendType{}, err
	}
	if target.Kind != frontendTypeRecord {
		return frontendType{}, fmt.Errorf("get expects a record-like value, got %s", target.String())
	}
	field := canonicalFieldName(args[1].Value)
	value, ok := target.Fields[field]
	if !ok {
		return frontendType{}, fmt.Errorf("record %s has no field %q", target.Name, field)
	}
	return value, nil
}

func (c *frontendTypeChecker) inferAssocExprType(args []sexp.Node, env frontendTypeEnv) (frontendType, error) {
	if len(args) < 2 {
		return frontendType{}, fmt.Errorf("assoc expects a target and at least 1 update")
	}
	target, err := c.inferExprType(args[0], env)
	if err != nil {
		return frontendType{}, err
	}
	if target.Kind != frontendTypeRecord {
		return frontendType{}, fmt.Errorf("assoc expects a record-like value, got %s", target.String())
	}
	for _, update := range args[1:] {
		if update.Kind != sexp.KindList || len(update.Children) != 2 || update.Children[0].Kind != sexp.KindSymbol {
			return frontendType{}, fmt.Errorf("assoc updates must look like (field expr)")
		}
		field := canonicalFieldName(update.Children[0].Value)
		current, ok := target.Fields[field]
		if !ok {
			return frontendType{}, fmt.Errorf("record %s has no field %q", target.Name, field)
		}
		value, err := c.inferExprTypeWithExpected(update.Children[1], current, env)
		if err != nil {
			return frontendType{}, err
		}
		if !frontendAssignable(value, current) {
			return frontendType{}, fmt.Errorf("assoc field %q expects %s, got %s", field, current.String(), value.String())
		}
	}
	return target, nil
}

func (c *frontendTypeChecker) inferOperatorType(op string, args []sexp.Node, env frontendTypeEnv) (frontendType, error) {
	if len(args) == 0 {
		return frontendType{}, fmt.Errorf("%s expects at least 1 argument", op)
	}
	if op == "-" && len(args) == 1 {
		value, err := c.inferExprType(args[0], env)
		if err != nil {
			return frontendType{}, err
		}
		if !isFrontendNumberType(value) {
			return frontendType{}, fmt.Errorf("operator - expects number, got %s", value.String())
		}
		return value, nil
	}
	if len(args) < 2 {
		return frontendType{}, fmt.Errorf("%s expects at least 2 arguments", op)
	}
	values := make([]frontendType, 0, len(args))
	for _, arg := range args {
		value, err := c.inferExprType(arg, env)
		if err != nil {
			return frontendType{}, err
		}
		values = append(values, value)
	}
	switch op {
	case "and", "or":
		for _, value := range values {
			if !frontendAssignable(value, boolFrontendType()) {
				return frontendType{}, fmt.Errorf("operator %s expects bool, got %s", op, value.String())
			}
		}
		return boolFrontendType(), nil
	case "=", "!=":
		for _, value := range values[1:] {
			if !frontendComparableForEquality(values[0], value) {
				return frontendType{}, fmt.Errorf("operator %s expects compatible values, got %s and %s", op, values[0].String(), value.String())
			}
		}
		return boolFrontendType(), nil
	case ">", ">=", "<", "<=":
		current := values[0]
		if !isFrontendOrderedType(current) {
			return frontendType{}, fmt.Errorf("operator %s expects ordered values, got %s", op, current.String())
		}
		for _, value := range values[1:] {
			if !isFrontendOrderedType(value) || !frontendOrderedTypesCompatible(current, value) {
				return frontendType{}, fmt.Errorf("operator %s expects compatible ordered values, got %s and %s", op, current.String(), value.String())
			}
		}
		return boolFrontendType(), nil
	case "+", "-", "*", "/":
		current := values[0]
		if !isFrontendNumberType(current) {
			return frontendType{}, fmt.Errorf("operator %s expects numbers, got %s", op, current.String())
		}
		for _, value := range values[1:] {
			if !isFrontendNumberType(value) {
				return frontendType{}, fmt.Errorf("operator %s expects numbers, got %s", op, value.String())
			}
			current = mergeFrontendTypes(current, value)
		}
		if op == "/" {
			return decimalFrontendType(), nil
		}
		return current, nil
	default:
		return frontendType{}, fmt.Errorf("unknown operator %q", op)
	}
}

func (c *frontendTypeChecker) inferCallType(rawName string, args []sexp.Node, env frontendTypeEnv) (frontendType, error) {
	name := canonicalFieldName(rawName)
	if record, ok, err := c.namedType(name); err != nil {
		return frontendType{}, err
	} else if ok && record.Kind == frontendTypeRecord {
		return c.inferRecordConstructorType(record, rawName, args, env)
	}
	if typ, variant, ok := c.variantConstructor(name); ok {
		return c.inferVariantConstructorType(typ, variant, rawName, args, env)
	}
	if fn := c.functions[name]; fn != nil {
		return c.inferUserFunctionReturn(fn, args, env)
	}
	switch rawName {
	case "number->string":
		if len(args) != 1 {
			return frontendType{}, fmt.Errorf("number->string expects 1 argument")
		}
		value, err := c.inferExprType(args[0], env)
		if err != nil {
			return frontendType{}, err
		}
		if !isFrontendNumberType(value) {
			return frontendType{}, fmt.Errorf("number->string expects number, got %s", value.String())
		}
		return stringFrontendType(), nil
	case "date->string":
		if len(args) != 1 {
			return frontendType{}, fmt.Errorf("date->string expects 1 argument")
		}
		value, err := c.inferExprType(args[0], env)
		if err != nil {
			return frontendType{}, err
		}
		if !frontendAssignable(value, dateFrontendType()) {
			return frontendType{}, fmt.Errorf("date->string expects date, got %s", value.String())
		}
		return stringFrontendType(), nil
	case "datetime->string":
		if len(args) != 1 {
			return frontendType{}, fmt.Errorf("datetime->string expects 1 argument")
		}
		value, err := c.inferExprType(args[0], env)
		if err != nil {
			return frontendType{}, err
		}
		if !frontendAssignable(value, dateTimeFrontendType()) {
			return frontendType{}, fmt.Errorf("datetime->string expects datetime, got %s", value.String())
		}
		return stringFrontendType(), nil
	}
	switch name {
	case "string_append":
		for _, arg := range args {
			value, err := c.inferExprType(arg, env)
			if err != nil {
				return frontendType{}, err
			}
			if !frontendAssignable(value, stringFrontendType()) {
				return frontendType{}, fmt.Errorf("string-append expects string arguments, got %s", value.String())
			}
		}
		return stringFrontendType(), nil
	case "authenticated?":
		if len(args) != 1 {
			return frontendType{}, fmt.Errorf("authenticated? expects 1 argument")
		}
		userType, err := c.inferExprType(args[0], env)
		if err != nil {
			return frontendType{}, err
		}
		if !frontendAssignable(userType, currentUserFrontendType()) {
			return frontendType{}, fmt.Errorf("authenticated? expects current-user, got %s", userType.String())
		}
		return boolFrontendType(), nil
	case "anonymous?":
		if len(args) != 1 {
			return frontendType{}, fmt.Errorf("anonymous? expects 1 argument")
		}
		userType, err := c.inferExprType(args[0], env)
		if err != nil {
			return frontendType{}, err
		}
		if !frontendAssignable(userType, currentUserFrontendType()) {
			return frontendType{}, fmt.Errorf("anonymous? expects current-user, got %s", userType.String())
		}
		return boolFrontendType(), nil
	case "same_user?":
		if len(args) != 2 {
			return frontendType{}, fmt.Errorf("same-user? expects 2 arguments")
		}
		userType, err := c.inferExprType(args[0], env)
		if err != nil {
			return frontendType{}, err
		}
		if !frontendAssignable(userType, currentUserFrontendType()) {
			return frontendType{}, fmt.Errorf("same-user? expects current-user as first argument, got %s", userType.String())
		}
		idType, err := c.inferExprType(args[1], env)
		if err != nil {
			return frontendType{}, err
		}
		if !frontendAssignable(idType, intFrontendType()) {
			return frontendType{}, fmt.Errorf("same-user? expects int as second argument, got %s", idType.String())
		}
		return boolFrontendType(), nil
	case "has_role?":
		if len(args) != 2 {
			return frontendType{}, fmt.Errorf("has-role? expects 2 arguments")
		}
		userType, err := c.inferExprType(args[0], env)
		if err != nil {
			return frontendType{}, err
		}
		if !frontendAssignable(userType, currentUserFrontendType()) {
			return frontendType{}, fmt.Errorf("has-role? expects current-user as first argument, got %s", userType.String())
		}
		roleType, err := c.inferExprType(args[1], env)
		if err != nil {
			return frontendType{}, err
		}
		if !frontendAssignable(roleType, stringFrontendType()) {
			return frontendType{}, fmt.Errorf("has-role? expects string as second argument, got %s", roleType.String())
		}
		return boolFrontendType(), nil
	case "first":
		if len(args) != 1 {
			return frontendType{}, fmt.Errorf("first expects 1 argument")
		}
		listType, err := c.inferExprType(args[0], env)
		if err != nil {
			return frontendType{}, err
		}
		if listType.Kind == frontendTypeEmptyList {
			return frontendType{}, fmt.Errorf("first cannot infer the element type of empty list")
		}
		if listType.Kind != frontendTypeList || listType.Element == nil {
			return frontendType{}, fmt.Errorf("first expects a list, got %s", listType.String())
		}
		return frontendMaybeType(*listType.Element), nil
	case "rest":
		if len(args) != 1 {
			return frontendType{}, fmt.Errorf("rest expects 1 argument")
		}
		listType, err := c.inferExprType(args[0], env)
		if err != nil {
			return frontendType{}, err
		}
		if listType.Kind == frontendTypeEmptyList {
			return frontendType{}, fmt.Errorf("rest cannot infer the element type of empty list")
		}
		if listType.Kind != frontendTypeList || listType.Element == nil {
			return frontendType{}, fmt.Errorf("rest expects a list, got %s", listType.String())
		}
		return listType, nil
	case "empty?":
		if len(args) != 1 {
			return frontendType{}, fmt.Errorf("empty? expects 1 argument")
		}
		listType, err := c.inferExprType(args[0], env)
		if err != nil {
			return frontendType{}, err
		}
		if listType.Kind == frontendTypeEmptyList {
			return boolFrontendType(), nil
		}
		if listType.Kind != frontendTypeList {
			return frontendType{}, fmt.Errorf("empty? expects a list, got %s", listType.String())
		}
		return boolFrontendType(), nil
	case "cons":
		if len(args) != 2 {
			return frontendType{}, fmt.Errorf("cons expects 2 arguments")
		}
		headType, err := c.inferExprType(args[0], env)
		if err != nil {
			return frontendType{}, err
		}
		listType, err := c.inferExprType(args[1], env)
		if err != nil {
			return frontendType{}, err
		}
		if listType.Kind == frontendTypeEmptyList {
			return listFrontendType(headType), nil
		}
		if listType.Kind != frontendTypeList || listType.Element == nil {
			return frontendType{}, fmt.Errorf("cons expects a list as second argument, got %s", listType.String())
		}
		elem, err := mergeCompatibleFrontendTypes(headType, *listType.Element)
		if err != nil {
			return frontendType{}, err
		}
		return listFrontendType(elem), nil
	case "map":
		return c.inferMapType(args, env)
	case "filter":
		return c.inferFilterType(args, env)
	case "fold_left":
		return c.inferFoldType(args, env, false)
	case "fold_right":
		return c.inferFoldType(args, env, true)
	}
	return frontendType{}, fmt.Errorf("unknown function %q", rawName)
}

func (c *frontendTypeChecker) inferRecordConstructorType(record frontendType, rawName string, args []sexp.Node, env frontendTypeEnv) (frontendType, error) {
	fields := orderedFrontendRecordFields(record)
	if len(args) != len(fields) {
		return frontendType{}, fmt.Errorf("%s expects %d arguments", rawName, len(fields))
	}
	if len(args) == 0 {
		return record, nil
	}
	if frontendRecordArgsAreNamed(args) {
		seen := map[string]bool{}
		for _, arg := range args {
			field := canonicalFieldName(arg.Children[0].Value)
			expected, ok := record.Fields[field]
			if !ok {
				return frontendType{}, fmt.Errorf("%s has no field %q", rawName, field)
			}
			if seen[field] {
				return frontendType{}, fmt.Errorf("%s field %q is set more than once", rawName, field)
			}
			seen[field] = true
			value, err := c.inferExprTypeWithExpected(arg.Children[1], expected, env)
			if err != nil {
				return frontendType{}, err
			}
			if !frontendAssignable(value, expected) {
				return frontendType{}, fmt.Errorf("%s field %q expects %s, got %s", rawName, field, expected.String(), value.String())
			}
		}
		for _, field := range fields {
			if !seen[field] {
				return frontendType{}, fmt.Errorf("%s missing field %q", rawName, field)
			}
		}
		return record, nil
	}
	for index, field := range fields {
		expected := record.Fields[field]
		value, err := c.inferExprTypeWithExpected(args[index], expected, env)
		if err != nil {
			return frontendType{}, err
		}
		if !frontendAssignable(value, expected) {
			return frontendType{}, fmt.Errorf("%s field %q expects %s, got %s", rawName, field, expected.String(), value.String())
		}
	}
	return record, nil
}

func (c *frontendTypeChecker) variantConstructor(name string) (*model.EnumType, *model.TypeVariant, bool) {
	for _, typ := range c.types {
		for i := range typ.Variants {
			if typ.Variants[i].Name == name {
				return typ, &typ.Variants[i], true
			}
		}
	}
	return nil, nil, false
}

func (c *frontendTypeChecker) inferVariantConstructorType(typ *model.EnumType, variant *model.TypeVariant, rawName string, args []sexp.Node, env frontendTypeEnv) (frontendType, error) {
	if len(args) != len(variant.Fields) {
		return frontendType{}, fmt.Errorf("%s expects %d arguments", rawName, len(variant.Fields))
	}
	payload := make([]frontendType, 0, len(variant.Fields))
	names := make([]string, 0, len(variant.Fields))
	for index, field := range variant.Fields {
		expected, err := c.parseTypeExpr(field.Type)
		if err != nil {
			return frontendType{}, err
		}
		value, err := c.inferExprTypeWithExpected(args[index], expected, env)
		if err != nil {
			return frontendType{}, err
		}
		if !frontendAssignable(value, expected) {
			return frontendType{}, fmt.Errorf("%s argument %s expects %s, got %s", rawName, field.Name, expected.String(), value.String())
		}
		payload = append(payload, expected)
		names = append(names, field.Name)
	}
	_ = typ
	return namedTaggedFrontendType(variant.Name, names, payload...), nil
}

func (c *frontendTypeChecker) inferUserFunctionReturn(fn *model.Function, args []sexp.Node, env frontendTypeEnv) (frontendType, error) {
	if len(args) != len(fn.Parameters) {
		return frontendType{}, fmt.Errorf("%s expects %d arguments", fn.Name, len(fn.Parameters))
	}
	constraints, err := c.functionParameterConstraints(fn)
	if err != nil {
		return frontendType{}, err
	}
	keyParts := []string{fn.Name}
	child := c.baseFunctionEnv()
	for index, param := range fn.Parameters {
		value, err := c.inferExprType(args[index], env)
		if err != nil {
			return frontendType{}, err
		}
		if expected, ok := constraints[param]; ok && !frontendAssignable(value, expected) {
			return frontendType{}, fmt.Errorf("%s parameter %s expects %s, got %s", fn.Name, param, expected.String(), value.String())
		}
		child[param] = value
		keyParts = append(keyParts, value.String())
	}
	key := strings.Join(keyParts, "|")
	if c.functionStack[key] {
		return frontendType{}, fmt.Errorf("function %s return type could not be inferred because it is recursive", fn.Name)
	}
	c.functionStack[key] = true
	defer delete(c.functionStack, key)

	node, err := sexp.ParseOne(fn.Expression)
	if err != nil {
		return frontendType{}, err
	}
	returnType, err := c.inferExprType(node, child)
	if err != nil {
		return frontendType{}, err
	}
	if frontendTypeHasUnknown(returnType) {
		return frontendType{}, fmt.Errorf("function %s return type could not be inferred", fn.Name)
	}
	return returnType, nil
}

func (c *frontendTypeChecker) functionParameterConstraints(fn *model.Function) (map[string]frontendType, error) {
	if cached, ok := c.functionConstraints[fn.Name]; ok {
		return cached, nil
	}
	key := "constraints|" + fn.Name
	if c.functionStack[key] {
		return map[string]frontendType{}, nil
	}
	c.functionStack[key] = true
	defer delete(c.functionStack, key)

	node, err := sexp.ParseOne(fn.Expression)
	if err != nil {
		return nil, err
	}
	params := map[string]struct{}{}
	for _, param := range fn.Parameters {
		params[param] = struct{}{}
	}
	out := map[string]frontendType{}
	if err := c.collectFunctionParameterConstraints(node, params, out); err != nil {
		return nil, err
	}
	c.functionConstraints[fn.Name] = out
	return out, nil
}

func (c *frontendTypeChecker) collectFunctionParameterConstraints(node sexp.Node, params map[string]struct{}, out map[string]frontendType) error {
	if node.Kind != sexp.KindList || len(node.Children) == 0 || node.Children[0].Kind != sexp.KindSymbol {
		return nil
	}
	head := node.Children[0].Value
	args := node.Children[1:]
	switch head {
	case "if":
		if len(args) == 3 {
			if err := c.constrainSymbol(args[0], params, out, boolFrontendType()); err != nil {
				return err
			}
		}
	case "cond":
		for _, clause := range args {
			if clause.Kind == sexp.KindList && len(clause.Children) == 2 {
				if !(clause.Children[0].Kind == sexp.KindSymbol && clause.Children[0].Value == "else") {
					if err := c.constrainSymbol(clause.Children[0], params, out, boolFrontendType()); err != nil {
						return err
					}
				}
			}
		}
	case "not":
		if len(args) == 1 {
			if err := c.constrainSymbol(args[0], params, out, boolFrontendType()); err != nil {
				return err
			}
		}
	case "and", "or":
		for _, arg := range args {
			if err := c.constrainSymbol(arg, params, out, boolFrontendType()); err != nil {
				return err
			}
		}
	case "+", "-", "*", "/":
		for _, arg := range args {
			if err := c.constrainSymbol(arg, params, out, decimalFrontendType()); err != nil {
				return err
			}
		}
	case "contains", "starts-with", "ends-with":
		for _, arg := range args {
			if err := c.constrainSymbol(arg, params, out, stringFrontendType()); err != nil {
				return err
			}
		}
	case "matches":
		if len(args) == 2 {
			if err := c.constrainSymbol(args[1], params, out, stringFrontendType()); err != nil {
				return err
			}
		}
	case ">", ">=", "<", "<=", "=", "!=":
		for index := 0; index < len(args)-1; index++ {
			if err := c.constrainSymbolFromNeighbor(args[index], args[index+1], params, out); err != nil {
				return err
			}
			if err := c.constrainSymbolFromNeighbor(args[index+1], args[index], params, out); err != nil {
				return err
			}
		}
	default:
		name := canonicalFieldName(head)
		if called := c.functions[name]; called != nil {
			constraints, err := c.functionParameterConstraints(called)
			if err != nil {
				return err
			}
			for index, arg := range args {
				if index >= len(called.Parameters) {
					break
				}
				expected, ok := constraints[called.Parameters[index]]
				if !ok {
					continue
				}
				if err := c.constrainSymbol(arg, params, out, expected); err != nil {
					return err
				}
			}
		}
	}
	for _, child := range node.Children {
		if err := c.collectFunctionParameterConstraints(child, params, out); err != nil {
			return err
		}
	}
	return nil
}

func (c *frontendTypeChecker) constrainSymbolFromNeighbor(candidate sexp.Node, neighbor sexp.Node, params map[string]struct{}, out map[string]frontendType) error {
	if candidate.Kind != sexp.KindSymbol {
		return nil
	}
	name := canonicalFieldName(candidate.Value)
	if _, ok := params[name]; !ok {
		return nil
	}
	switch neighbor.Kind {
	case sexp.KindNumber:
		return c.constrainSymbol(candidate, params, out, decimalFrontendType())
	case sexp.KindString:
		return c.constrainSymbol(candidate, params, out, stringFrontendType())
	case sexp.KindSymbol:
		switch neighbor.Value {
		case "true", "false":
			return c.constrainSymbol(candidate, params, out, boolFrontendType())
		}
	}
	return nil
}

func (c *frontendTypeChecker) constrainSymbol(node sexp.Node, params map[string]struct{}, out map[string]frontendType, expected frontendType) error {
	if node.Kind != sexp.KindSymbol {
		return nil
	}
	name := canonicalFieldName(node.Value)
	if _, ok := params[name]; !ok {
		return nil
	}
	if current, ok := out[name]; ok {
		merged, err := mergeCompatibleFrontendTypes(current, expected)
		if err != nil {
			return fmt.Errorf("parameter %s has incompatible inferred constraints: %w", name, err)
		}
		out[name] = merged
		return nil
	}
	out[name] = expected
	return nil
}

func (c *frontendTypeChecker) baseFunctionEnv() frontendTypeEnv {
	return frontendTypeEnv{
		"current_user": currentUserFrontendType(),
	}
}

func (c *frontendTypeChecker) inferMapType(args []sexp.Node, env frontendTypeEnv) (frontendType, error) {
	if len(args) != 2 {
		return frontendType{}, fmt.Errorf("map expects 2 arguments")
	}
	listType, err := c.inferExprType(args[1], env)
	if err != nil {
		return frontendType{}, err
	}
	if listType.Kind == frontendTypeEmptyList {
		return frontendType{}, fmt.Errorf("map cannot infer the element type of empty list")
	}
	if listType.Kind != frontendTypeList || listType.Element == nil {
		return frontendType{}, fmt.Errorf("map expects a list as second argument, got %s", listType.String())
	}
	returnType, err := c.inferFunctionValueCall(args[0], []frontendType{*listType.Element}, env)
	if err != nil {
		return frontendType{}, err
	}
	return listFrontendType(returnType), nil
}

func (c *frontendTypeChecker) inferFilterType(args []sexp.Node, env frontendTypeEnv) (frontendType, error) {
	if len(args) != 2 {
		return frontendType{}, fmt.Errorf("filter expects 2 arguments")
	}
	listType, err := c.inferExprType(args[1], env)
	if err != nil {
		return frontendType{}, err
	}
	if listType.Kind == frontendTypeEmptyList {
		return frontendType{}, fmt.Errorf("filter cannot infer the element type of empty list")
	}
	if listType.Kind != frontendTypeList || listType.Element == nil {
		return frontendType{}, fmt.Errorf("filter expects a list as second argument, got %s", listType.String())
	}
	returnType, err := c.inferFunctionValueCall(args[0], []frontendType{*listType.Element}, env)
	if err != nil {
		return frontendType{}, err
	}
	if !frontendAssignable(returnType, boolFrontendType()) {
		return frontendType{}, fmt.Errorf("filter predicate must return bool, got %s", returnType.String())
	}
	return listType, nil
}

func (c *frontendTypeChecker) inferFoldType(args []sexp.Node, env frontendTypeEnv, right bool) (frontendType, error) {
	name := "fold-left"
	if right {
		name = "fold-right"
	}
	if len(args) != 3 {
		return frontendType{}, fmt.Errorf("%s expects 3 arguments", name)
	}
	accType, err := c.inferExprType(args[1], env)
	if err != nil {
		return frontendType{}, err
	}
	listType, err := c.inferExprType(args[2], env)
	if err != nil {
		return frontendType{}, err
	}
	if listType.Kind == frontendTypeEmptyList {
		return frontendType{}, fmt.Errorf("%s cannot infer the element type of empty list", name)
	}
	if listType.Kind != frontendTypeList || listType.Element == nil {
		return frontendType{}, fmt.Errorf("%s expects a list as third argument, got %s", name, listType.String())
	}
	returnType, err := c.inferFunctionValueCall(args[0], []frontendType{accType, *listType.Element}, env)
	if err != nil {
		return frontendType{}, err
	}
	return mergeCompatibleFrontendTypes(accType, returnType)
}

func (c *frontendTypeChecker) inferFunctionValueCall(node sexp.Node, argTypes []frontendType, env frontendTypeEnv) (frontendType, error) {
	switch node.Kind {
	case sexp.KindSymbol:
		name := canonicalFieldName(node.Value)
		fn := c.functions[name]
		if fn == nil {
			return frontendType{}, fmt.Errorf("%q is not a function", node.Value)
		}
		args := make([]sexp.Node, 0, len(argTypes))
		callEnv := cloneFrontendEnv(env)
		for index, argType := range argTypes {
			placeholder := fmt.Sprintf("__arg_%d", index)
			callEnv[placeholder] = argType
			args = append(args, sexp.Node{Kind: sexp.KindSymbol, Value: placeholder})
		}
		return c.inferUserFunctionReturn(fn, args, callEnv)
	case sexp.KindList:
		if len(node.Children) == 0 || node.Children[0].Kind != sexp.KindSymbol || node.Children[0].Value != "lambda" {
			return frontendType{}, fmt.Errorf("expected a function value")
		}
		if len(node.Children) != 3 || node.Children[1].Kind != sexp.KindList {
			return frontendType{}, fmt.Errorf("lambda expects parameters and a body")
		}
		if len(node.Children[1].Children) != len(argTypes) {
			return frontendType{}, fmt.Errorf("lambda expects %d arguments", len(node.Children[1].Children))
		}
		child := cloneFrontendEnv(env)
		for index, param := range node.Children[1].Children {
			if param.Kind != sexp.KindSymbol {
				return frontendType{}, fmt.Errorf("lambda parameters must be symbols")
			}
			child[canonicalFieldName(param.Value)] = argTypes[index]
		}
		return c.inferExprType(node.Children[2], child)
	default:
		return frontendType{}, fmt.Errorf("expected a function value")
	}
}

func (c *frontendTypeChecker) namedType(name string) (frontendType, bool, error) {
	if cached, ok := c.namedTypeCache[name]; ok {
		return cached, true, nil
	}
	if c.namedTypeStack[name] {
		return frontendType{}, false, fmt.Errorf("type %s is recursive and cannot be inferred", name)
	}
	if record := c.records[name]; record != nil {
		c.namedTypeStack[name] = true
		fields := map[string]frontendType{}
		for _, field := range record.Fields {
			value, err := c.parseTypeExpr(field.Type)
			if err != nil {
				return frontendType{}, false, err
			}
			fields[field.Name] = value
		}
		delete(c.namedTypeStack, name)
		order := make([]string, 0, len(record.Fields))
		for _, field := range record.Fields {
			order = append(order, field.Name)
		}
		out := recordFrontendTypeWithOrder(name, fields, order)
		c.namedTypeCache[name] = out
		return out, true, nil
	}
	if typ := c.types[name]; typ != nil {
		c.namedTypeStack[name] = true
		out, err := unionFrontendTypeFromVariants(typ.Variants, func(_ model.TypeVariant, field model.RecordField, _ int) (frontendType, error) {
			return c.parseTypeExpr(field.Type)
		})
		if err != nil {
			delete(c.namedTypeStack, name)
			return frontendType{}, false, err
		}
		delete(c.namedTypeStack, name)
		c.namedTypeCache[name] = out
		return out, true, nil
	}
	if entity := c.entities[canonicalTypeName(name)]; entity != nil {
		c.namedTypeStack[name] = true
		fields := map[string]frontendType{}
		order := make([]string, 0, len(entity.Fields))
		for _, field := range entity.Fields {
			value, err := c.entityFieldType(field)
			if err != nil {
				return frontendType{}, false, err
			}
			fields[field.Name] = value
			order = append(order, field.Name)
		}
		delete(c.namedTypeStack, name)
		out := recordFrontendTypeWithOrder(entity.Name, fields, order)
		c.namedTypeCache[name] = out
		return out, true, nil
	}
	return frontendType{}, false, nil
}

func (c *frontendTypeChecker) entityFieldType(field model.Field) (frontendType, error) {
	var base frontendType
	if field.RelationEntity != "" {
		entity := c.entities[field.RelationEntity]
		if entity == nil {
			return frontendType{}, fmt.Errorf("unknown relation entity %s", field.RelationEntity)
		}
		for _, candidate := range entity.Fields {
			if candidate.Primary {
				value, err := c.entityFieldType(candidate)
				if err != nil {
					return frontendType{}, err
				}
				base = value
				if field.Optional {
					return frontendMaybeType(base), nil
				}
				return base, nil
			}
		}
		return frontendType{}, fmt.Errorf("relation entity %s has no primary key", field.RelationEntity)
	} else {
		switch field.Type {
		case "String":
			base = stringFrontendType()
		case "Bool":
			base = boolFrontendType()
		case "Int":
			base = intFrontendType()
		case "Decimal":
			base = decimalFrontendType()
		case "Date":
			base = dateFrontendType()
		case "DateTime":
			base = dateTimeFrontendType()
		default:
			if len(field.EnumValues) > 0 {
				base = stringFrontendType()
			} else {
				return frontendType{}, fmt.Errorf("unknown type %s", field.Type)
			}
		}
	}
	if field.Optional {
		return frontendMaybeType(base), nil
	}
	return base, nil
}

func (c *frontendTypeChecker) parseTypeExpr(typeExpr string) (frontendType, error) {
	switch typeExpr {
	case "string", "String":
		return stringFrontendType(), nil
	case "bool", "Bool":
		return boolFrontendType(), nil
	case "int", "Int":
		return intFrontendType(), nil
	case "decimal", "Decimal":
		return decimalFrontendType(), nil
	case "date", "Date":
		return dateFrontendType(), nil
	case "datetime", "DateTime":
		return dateTimeFrontendType(), nil
	case "cursor":
		return cursorFrontendType(), nil
	case "(unit)":
		return frontendUnitType(), nil
	}
	if named, ok, err := c.namedType(typeExpr); err != nil {
		return frontendType{}, err
	} else if ok {
		return named, nil
	}
	node, err := sexp.ParseOne(typeExpr)
	if err != nil {
		return frontendType{}, err
	}
	if node.Kind != sexp.KindList || len(node.Children) == 0 || node.Children[0].Kind != sexp.KindSymbol {
		return anyFrontendType(), nil
	}
	switch node.Children[0].Value {
	case "maybe":
		if len(node.Children) != 2 {
			return frontendType{}, fmt.Errorf("maybe expects one type argument")
		}
		value, err := c.parseTypeExpr(sexp.InlineString(node.Children[1]))
		if err != nil {
			return frontendType{}, err
		}
		return frontendMaybeType(value), nil
	case "list":
		if len(node.Children) != 2 {
			return frontendType{}, fmt.Errorf("list expects one type argument")
		}
		value, err := c.parseTypeExpr(sexp.InlineString(node.Children[1]))
		if err != nil {
			return frontendType{}, err
		}
		return listFrontendType(value), nil
	case "result":
		if len(node.Children) != 3 {
			return frontendType{}, fmt.Errorf("result expects two type arguments")
		}
		errType, err := c.parseTypeExpr(sexp.InlineString(node.Children[1]))
		if err != nil {
			return frontendType{}, err
		}
		okType, err := c.parseTypeExpr(sexp.InlineString(node.Children[2]))
		if err != nil {
			return frontendType{}, err
		}
		return frontendResultType(errType, okType), nil
	default:
		return anyFrontendType(), nil
	}
}

func cloneFrontendEnv(env frontendTypeEnv) frontendTypeEnv {
	out := frontendTypeEnv{}
	for key, value := range env {
		out[key] = value
	}
	return out
}

func frontendAssignable(source frontendType, target frontendType) bool {
	if source.Kind == frontendTypeNever {
		return true
	}
	if source.Kind == frontendTypeAny || target.Kind == frontendTypeAny {
		return true
	}
	if source.Kind == frontendTypeEmptyList {
		return target.Kind == frontendTypeList
	}
	if source.Kind == target.Kind {
		switch source.Kind {
		case frontendTypeBool, frontendTypeInt, frontendTypeDecimal, frontendTypeString, frontendTypeDate, frontendTypeDateTime, frontendTypeCursor:
			return true
		case frontendTypeList:
			if source.Element == nil || target.Element == nil {
				return true
			}
			return frontendAssignable(*source.Element, *target.Element)
		case frontendTypeRecord:
			if source.Name != "" && target.Name != "" {
				return source.Name == target.Name
			}
			for key, value := range target.Fields {
				sourceValue, ok := source.Fields[key]
				if !ok || !frontendAssignable(sourceValue, value) {
					return false
				}
			}
			return true
		case frontendTypeUnion:
			for tag, targetPayload := range target.Variants {
				sourcePayload, ok := source.Variants[tag]
				if !ok {
					continue
				}
				if len(sourcePayload) != len(targetPayload) {
					return false
				}
				for index := range sourcePayload {
					if !frontendAssignable(sourcePayload[index], targetPayload[index]) {
						return false
					}
				}
			}
			for tag := range source.Variants {
				if _, ok := target.Variants[tag]; !ok {
					return false
				}
			}
			return true
		}
	}
	if source.Kind == frontendTypeInt && target.Kind == frontendTypeDecimal {
		return true
	}
	return false
}

func frontendMaybePayload(value frontendType) (frontendType, bool) {
	if value.Kind != frontendTypeUnion || len(value.Variants) != 2 {
		return frontendType{}, false
	}
	just, hasJust := value.Variants["just"]
	nothing, hasNothing := value.Variants["nothing"]
	if !hasJust || !hasNothing || len(just) != 1 || len(nothing) != 0 {
		return frontendType{}, false
	}
	return just[0], true
}

func frontendResultPayloads(value frontendType) (frontendType, frontendType, bool) {
	if value.Kind != frontendTypeUnion || len(value.Variants) != 2 {
		return frontendType{}, frontendType{}, false
	}
	errPayload, hasErr := value.Variants["err"]
	okPayload, hasOK := value.Variants["ok"]
	if !hasErr || !hasOK || len(errPayload) != 1 || len(okPayload) != 1 {
		return frontendType{}, frontendType{}, false
	}
	return errPayload[0], okPayload[0], true
}

func frontendTypeHasUnknown(value frontendType) bool {
	switch value.Kind {
	case frontendTypeUnknown:
		return true
	case frontendTypeList:
		return value.Element != nil && frontendTypeHasUnknown(*value.Element)
	case frontendTypeRecord:
		for _, field := range value.Fields {
			if frontendTypeHasUnknown(field) {
				return true
			}
		}
	case frontendTypeUnion:
		for _, payload := range value.Variants {
			for _, item := range payload {
				if frontendTypeHasUnknown(item) {
					return true
				}
			}
		}
	}
	return false
}

func frontendTypeIsUnresolved(value frontendType) bool {
	if value.Kind == "" || value.Kind == frontendTypeEmptyList {
		return true
	}
	return frontendTypeHasUnknown(value)
}

func frontendLengthSupported(value frontendType) bool {
	switch value.Kind {
	case frontendTypeAny, frontendTypeString, frontendTypeList:
		return true
	default:
		return false
	}
}

func mergeCompatibleFrontendTypes(left frontendType, right frontendType) (frontendType, error) {
	if frontendTypesMergeable(left, right) {
		return mergeFrontendTypes(left, right), nil
	}
	return frontendType{}, fmt.Errorf("incompatible types %s and %s", left.String(), right.String())
}

func frontendTypesMergeable(left frontendType, right frontendType) bool {
	if frontendAssignable(left, right) || frontendAssignable(right, left) {
		return true
	}
	if left.Kind == frontendTypeUnknown || right.Kind == frontendTypeUnknown {
		return true
	}
	if left.Kind == frontendTypeNever || right.Kind == frontendTypeNever {
		return true
	}
	if left.Kind == frontendTypeAny || right.Kind == frontendTypeAny {
		return true
	}
	if left.Kind == frontendTypeEmptyList {
		return right.Kind == frontendTypeList
	}
	if right.Kind == frontendTypeEmptyList {
		return left.Kind == frontendTypeList
	}
	if (left.Kind == frontendTypeInt && right.Kind == frontendTypeDecimal) || (left.Kind == frontendTypeDecimal && right.Kind == frontendTypeInt) {
		return true
	}
	if left.Kind != right.Kind {
		return false
	}
	switch left.Kind {
	case frontendTypeBool, frontendTypeInt, frontendTypeDecimal, frontendTypeString, frontendTypeDate, frontendTypeDateTime, frontendTypeCursor:
		return true
	case frontendTypeList:
		if left.Element == nil || right.Element == nil {
			return true
		}
		return frontendTypesMergeable(*left.Element, *right.Element)
	case frontendTypeUnion:
		for tag, leftPayload := range left.Variants {
			rightPayload, ok := right.Variants[tag]
			if !ok {
				continue
			}
			if len(leftPayload) != len(rightPayload) {
				return false
			}
			for index := range leftPayload {
				if !frontendTypesMergeable(leftPayload[index], rightPayload[index]) {
					return false
				}
			}
		}
		return true
	}
	return false
}

func mergeFrontendTypes(left frontendType, right frontendType) frontendType {
	if left.Kind == frontendTypeNever {
		return right
	}
	if right.Kind == frontendTypeNever {
		return left
	}
	if left.Kind == frontendTypeAny {
		return right
	}
	if right.Kind == frontendTypeAny {
		return left
	}
	if left.Kind == frontendTypeEmptyList {
		return right
	}
	if right.Kind == frontendTypeEmptyList {
		return left
	}
	if left.Kind == frontendTypeUnknown {
		return right
	}
	if right.Kind == frontendTypeUnknown {
		return left
	}
	if left.Kind == frontendTypeInt && right.Kind == frontendTypeDecimal {
		return right
	}
	if left.Kind == frontendTypeDecimal && right.Kind == frontendTypeInt {
		return left
	}
	if left.Kind == right.Kind {
		switch left.Kind {
		case frontendTypeList:
			if left.Element == nil {
				return right
			}
			if right.Element == nil {
				return left
			}
			return listFrontendType(mergeFrontendTypes(*left.Element, *right.Element))
		case frontendTypeUnion:
			variants := map[string][]frontendType{}
			variantNames := map[string][]string{}
			for tag, payload := range left.Variants {
				cloned := make([]frontendType, 0, len(payload))
				cloned = append(cloned, payload...)
				variants[tag] = cloned
				if names, ok := left.VariantNames[tag]; ok {
					clonedNames := make([]string, 0, len(names))
					clonedNames = append(clonedNames, names...)
					variantNames[tag] = clonedNames
				}
			}
			for tag, payload := range right.Variants {
				if current, ok := variants[tag]; ok {
					merged := make([]frontendType, 0, len(payload))
					for index := range payload {
						merged = append(merged, mergeFrontendTypes(current[index], payload[index]))
					}
					variants[tag] = merged
					if _, ok := variantNames[tag]; !ok {
						if names, ok := right.VariantNames[tag]; ok {
							clonedNames := make([]string, 0, len(names))
							clonedNames = append(clonedNames, names...)
							variantNames[tag] = clonedNames
						}
					}
					continue
				}
				cloned := make([]frontendType, 0, len(payload))
				cloned = append(cloned, payload...)
				variants[tag] = cloned
				if names, ok := right.VariantNames[tag]; ok {
					clonedNames := make([]string, 0, len(names))
					clonedNames = append(clonedNames, names...)
					variantNames[tag] = clonedNames
				}
			}
			return frontendType{Kind: frontendTypeUnion, Variants: variants, VariantNames: variantNames}
		}
	}
	return left
}

func orderedFrontendRecordFields(record frontendType) []string {
	out := make([]string, 0, len(record.FieldOrder))
	out = append(out, record.FieldOrder...)
	return out
}

func frontendRecordArgsAreNamed(args []sexp.Node) bool {
	for _, arg := range args {
		if arg.Kind != sexp.KindList || len(arg.Children) != 2 || arg.Children[0].Kind != sexp.KindSymbol {
			return false
		}
	}
	return true
}

func sortedVariantTags(variants map[string][]frontendType) []string {
	out := make([]string, 0, len(variants))
	for tag := range variants {
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

func missingFrontendTags(variants map[string][]frontendType, seen map[string]bool) []string {
	out := []string{}
	for _, tag := range sortedVariantTags(variants) {
		if !seen[tag] {
			out = append(out, tag)
		}
	}
	return out
}

func wildcardMatchBindings(node sexp.Node) (map[string]frontendType, error) {
	switch node.Kind {
	case sexp.KindSymbol:
		return map[string]frontendType{}, nil
	case sexp.KindList:
		if len(node.Children) == 0 || node.Children[0].Kind != sexp.KindSymbol {
			return nil, fmt.Errorf("match pattern tag must be a symbol")
		}
		bindings := map[string]frontendType{}
		for _, child := range node.Children[1:] {
			if child.Kind != sexp.KindSymbol {
				return nil, fmt.Errorf("match pattern bindings must be symbols")
			}
			bindings[canonicalFieldName(child.Value)] = anyFrontendType()
		}
		return bindings, nil
	default:
		return nil, fmt.Errorf("unsupported match pattern")
	}
}

func isFrontendNumberType(value frontendType) bool {
	return value.Kind == frontendTypeInt || value.Kind == frontendTypeDecimal
}

func frontendComparableForEquality(left frontendType, right frontendType) bool {
	return frontendAssignable(left, right) || frontendAssignable(right, left)
}

func isFrontendOrderedType(value frontendType) bool {
	if value.Kind == frontendTypeAny {
		return true
	}
	if isFrontendNumberType(value) {
		return true
	}
	switch value.Kind {
	case frontendTypeDate, frontendTypeDateTime:
		return true
	default:
		return false
	}
}

func frontendOrderedTypesCompatible(left frontendType, right frontendType) bool {
	if left.Kind == frontendTypeAny || right.Kind == frontendTypeAny {
		return true
	}
	if isFrontendNumberType(left) && isFrontendNumberType(right) {
		return true
	}
	return left.Kind == right.Kind && (left.Kind == frontendTypeDate || left.Kind == frontendTypeDateTime)
}
