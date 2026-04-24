package parser

import (
	"fmt"

	"mar/internal/expr"
	"mar/internal/model"
	"mar/internal/sexp"
)

type backendTypeChecker struct {
	*frontendTypeChecker
}

func newBackendTypeChecker(functions map[string]*model.Function, records map[string]*model.Record, types map[string]*model.EnumType, entities map[string]*model.Entity) *backendTypeChecker {
	return &backendTypeChecker{
		frontendTypeChecker: newFrontendTypeChecker(functions, records, types, entities, nil, nil),
	}
}

func validateFunctionExpression(fn *model.Function, functions map[string]*model.Function, records map[string]*model.Record, types map[string]*model.EnumType, entities map[string]*model.Entity) error {
	allowed := expr.AllowedVariablesWithBuiltins(parameterVariables(fn.Parameters))
	if _, err := expr.Parse(fn.Expression, expr.ParserOptions{
		AllowedVariables: allowed,
		AllowedFunctions: allowedFunctionArities(functions),
		AllowedRecords:   allowedRecordFields(records),
		AllowedVariants:  allowedTypeVariants(types),
	}); err != nil {
		return fmt.Errorf("function %s: %w", fn.Name, err)
	}
	return nil
}

func validateEntityExpressions(entity *model.Entity, functions map[string]*model.Function, records map[string]*model.Record, types map[string]*model.EnumType, entities map[string]*model.Entity) error {
	allowed := queryAllowedVariables(entity)
	allowedFunctions := allowedFunctionArities(functions)
	allowedRecords := allowedRecordFields(records)
	allowedVariants := allowedTypeVariants(types)
	checker := newBackendTypeChecker(functions, records, types, entities)

	if entity.Validate != "" {
		if _, err := expr.Parse(entity.Validate, expr.ParserOptions{
			AllowedVariables: allowed,
			AllowedFunctions: allowedFunctions,
			AllowedRecords:   allowedRecords,
			AllowedVariants:  allowedVariants,
		}); err != nil {
			return fmt.Errorf("entity %s validate: %w", entity.Name, err)
		}
		validateType, err := checker.inferBackendExprType(entity.Validate, checker.entityEnv(entity, false))
		if err != nil {
			return fmt.Errorf("entity %s validate: %w", entity.Name, err)
		}
		if !frontendAssignable(validateType, boolFrontendType()) {
			return fmt.Errorf("entity %s validate: expression must return bool, got %s", entity.Name, validateType.String())
		}
	}

	allowedWithBuiltins := expr.AllowedVariablesWithBuiltins(allowed)
	for _, auth := range entity.Authorizations {
		if _, err := expr.Parse(auth.Expression, expr.ParserOptions{
			AllowedVariables: allowedWithBuiltins,
			AllowedFunctions: allowedFunctions,
			AllowedRecords:   allowedRecords,
			AllowedVariants:  allowedVariants,
		}); err != nil {
			return fmt.Errorf("entity %s authorize %s: %w", entity.Name, auth.Action, err)
		}
		authType, err := checker.inferBackendExprType(auth.Expression, checker.entityEnv(entity, true))
		if err != nil {
			return fmt.Errorf("entity %s authorize %s: %w", entity.Name, auth.Action, err)
		}
		if !frontendAssignable(authType, boolFrontendType()) {
			return fmt.Errorf("entity %s authorize %s: expression must return bool, got %s", entity.Name, auth.Action, authType.String())
		}
	}
	return nil
}

func validateBackendQueryWhere(queryName, raw string, entity *model.Entity, params []string, functions map[string]*model.Function, records map[string]*model.Record, types map[string]*model.EnumType, entities map[string]*model.Entity) (map[string]string, error) {
	checker := newBackendTypeChecker(functions, records, types, entities)
	env := checker.entityEnv(entity, true)
	paramTypes, err := checker.inferQueryParamTypes(raw, params, env)
	if err != nil {
		return nil, fmt.Errorf("query %s where: %w", queryName, err)
	}
	for _, param := range params {
		if value, ok := paramTypes[param]; ok {
			env[param] = value
		} else {
			return nil, fmt.Errorf("query %s parameter %s: type could not be inferred", queryName, param)
		}
	}
	valueType, err := checker.inferBackendExprType(raw, env)
	if err != nil {
		return nil, fmt.Errorf("query %s where: %w", queryName, err)
	}
	if !frontendAssignable(valueType, boolFrontendType()) {
		return nil, fmt.Errorf("query %s where: expression must return bool, got %s", queryName, valueType.String())
	}
	out := map[string]string{}
	for _, param := range params {
		valueType := paramTypes[param]
		modelType, ok := frontendTypeToQueryParameterType(valueType)
		if !ok {
			return nil, fmt.Errorf("query %s parameter %s: expected primitive type, got %s", queryName, param, valueType.String())
		}
		out[param] = modelType
	}
	return out, nil
}

func frontendTypeToQueryParameterType(value frontendType) (string, bool) {
	switch value.Kind {
	case frontendTypeBool:
		return "Bool", true
	case frontendTypeInt:
		return "Int", true
	case frontendTypeDecimal:
		return "Decimal", true
	case frontendTypeString:
		return "String", true
	case frontendTypeDate:
		return "Date", true
	case frontendTypeDateTime:
		return "DateTime", true
	default:
		return "", false
	}
}

func (c *backendTypeChecker) inferQueryParamTypes(raw string, params []string, baseEnv frontendTypeEnv) (map[string]frontendType, error) {
	node, err := sexp.ParseOne(raw)
	if err != nil {
		return nil, err
	}
	paramSet := map[string]struct{}{}
	env := cloneFrontendEnv(baseEnv)
	for _, param := range params {
		paramSet[param] = struct{}{}
		env[param] = anyFrontendType()
	}
	out := map[string]frontendType{}
	if err := c.collectQueryParamTypes(node, env, paramSet, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *backendTypeChecker) collectQueryParamTypes(node sexp.Node, env frontendTypeEnv, params map[string]struct{}, out map[string]frontendType) error {
	if node.Kind != sexp.KindList || len(node.Children) == 0 {
		return nil
	}
	if node.Children[0].Kind == sexp.KindSymbol {
		switch node.Children[0].Value {
		case "=", "!=", ">", ">=", "<", "<=":
			for index := 1; index < len(node.Children)-1; index++ {
				if err := c.collectQueryParamPairType(node.Children[index], node.Children[index+1], env, params, out); err != nil {
					return err
				}
				if err := c.collectQueryParamPairType(node.Children[index+1], node.Children[index], env, params, out); err != nil {
					return err
				}
			}
		case "has-role?", "has_role?":
			if len(node.Children) == 3 {
				if err := c.constrainSymbol(node.Children[2], params, out, stringFrontendType()); err != nil {
					return err
				}
			}
		default:
			name := canonicalFieldName(node.Children[0].Value)
			if fn := c.functions[name]; fn != nil {
				constraints, err := c.functionParameterConstraints(fn)
				if err != nil {
					return err
				}
				for index, arg := range node.Children[1:] {
					if index >= len(fn.Parameters) {
						break
					}
					expected, ok := constraints[fn.Parameters[index]]
					if !ok {
						continue
					}
					if err := c.constrainSymbol(arg, params, out, expected); err != nil {
						return err
					}
				}
			}
		}
	}
	for _, child := range node.Children {
		if err := c.collectQueryParamTypes(child, env, params, out); err != nil {
			return err
		}
	}
	return nil
}

func (c *backendTypeChecker) collectQueryParamPairType(candidate sexp.Node, other sexp.Node, env frontendTypeEnv, params map[string]struct{}, out map[string]frontendType) error {
	if candidate.Kind != sexp.KindSymbol {
		return nil
	}
	param := canonicalFieldName(candidate.Value)
	if _, ok := params[param]; !ok {
		return nil
	}
	valueType, err := c.inferExprType(other, env)
	if err != nil {
		return nil
	}
	if valueType.Kind == frontendTypeAny {
		return nil
	}
	if current, ok := out[param]; ok {
		merged, err := mergeCompatibleFrontendTypes(current, valueType)
		if err != nil {
			return fmt.Errorf("query parameter %s has incompatible inferred types: %w", param, err)
		}
		out[param] = merged
		return nil
	}
	out[param] = valueType
	return nil
}

func validateActionExpressions(action *model.Action, aliases map[string]*model.TypeAlias, functions map[string]*model.Function, records map[string]*model.Record, types map[string]*model.EnumType, entities map[string]*model.Entity) error {
	if action == nil {
		return nil
	}
	checker := newBackendTypeChecker(functions, records, types, entities)
	env := checker.baseFunctionEnv()

	alias := aliases[action.InputAlias]
	if alias == nil {
		return fmt.Errorf("action %s: missing input alias %s", action.Name, action.InputAlias)
	}
	inputFields := map[string]frontendType{}
	for _, field := range alias.Fields {
		value, err := checker.aliasFieldType(field)
		if err != nil {
			return fmt.Errorf("action %s input %s: %w", action.Name, field.Name, err)
		}
		env[field.Name] = value
		inputFields[field.Name] = value
	}
	env["input"] = recordFrontendTypeWithOrder(alias.Name, inputFields, aliasFieldOrder(alias))

	for _, step := range action.Steps {
		entity := entities[step.Entity]
		if entity == nil {
			return fmt.Errorf("action %s: unknown entity %s", action.Name, step.Entity)
		}
		for _, value := range step.Values {
			field := findEntityField(entity, value.Field)
			if field == nil {
				return fmt.Errorf("action %s step %s %s: unknown field %s", action.Name, step.Kind, entity.Name, value.Field)
			}
			valueType, err := checker.inferBackendExprType(value.Expression, env)
			if err != nil {
				return fmt.Errorf("action %s step %s %s field %s: %w", action.Name, step.Kind, entity.Name, value.Field, err)
			}
			expected, err := checker.entityInputFieldType(*field)
			if err != nil {
				return fmt.Errorf("action %s step %s %s field %s: %w", action.Name, step.Kind, entity.Name, value.Field, err)
			}
			if !frontendAssignable(valueType, expected) {
				return fmt.Errorf("action %s step %s %s field %s: expects %s, got %s", action.Name, step.Kind, entity.Name, value.Field, expected.String(), valueType.String())
			}
		}
		if step.Alias != "" {
			entityType, err := checker.entityRecordType(entity)
			if err != nil {
				return fmt.Errorf("action %s step %s %s alias %s: %w", action.Name, step.Kind, entity.Name, step.Alias, err)
			}
			env[step.Alias] = entityType
		}
	}
	return nil
}

func (c *backendTypeChecker) inferBackendExprType(raw string, env frontendTypeEnv) (frontendType, error) {
	node, err := sexp.ParseOne(raw)
	if err != nil {
		return frontendType{}, err
	}
	return c.inferExprType(node, env)
}

func (c *backendTypeChecker) entityEnv(entity *model.Entity, includeAuth bool) frontendTypeEnv {
	env := frontendTypeEnv{}
	if includeAuth {
		for key, value := range c.baseFunctionEnv() {
			env[key] = value
		}
	}
	if entity == nil {
		return env
	}
	for _, field := range entity.Fields {
		value, err := c.entityFieldType(field)
		if err != nil {
			env[field.Name] = anyFrontendType()
			continue
		}
		env[field.Name] = value
	}
	return env
}

func (c *backendTypeChecker) aliasFieldType(field model.AliasField) (frontendType, error) {
	if field.RelationEntity != "" {
		entity := c.entities[field.RelationEntity]
		if entity == nil {
			return anyFrontendType(), nil
		}
		pk := backendPrimaryField(entity)
		if pk == nil {
			return anyFrontendType(), nil
		}
		return c.entityFieldType(*pk)
	}
	switch field.Type {
	case "String":
		return stringFrontendType(), nil
	case "Bool":
		return boolFrontendType(), nil
	case "Int":
		return intFrontendType(), nil
	case "Decimal":
		return decimalFrontendType(), nil
	case "Date":
		return dateFrontendType(), nil
	case "DateTime":
		return dateTimeFrontendType(), nil
	default:
		if len(field.EnumValues) > 0 {
			return stringFrontendType(), nil
		}
		return anyFrontendType(), nil
	}
}

func (c *backendTypeChecker) entityInputFieldType(field model.Field) (frontendType, error) {
	if field.RelationEntity != "" {
		entity := c.entities[field.RelationEntity]
		if entity == nil {
			return anyFrontendType(), nil
		}
		pk := backendPrimaryField(entity)
		if pk == nil {
			return anyFrontendType(), nil
		}
		return c.entityInputFieldType(*pk)
	}
	switch field.Type {
	case "String":
		return stringFrontendType(), nil
	case "Bool":
		return boolFrontendType(), nil
	case "Int":
		return intFrontendType(), nil
	case "Decimal":
		return decimalFrontendType(), nil
	case "Date":
		return dateFrontendType(), nil
	case "DateTime":
		return dateTimeFrontendType(), nil
	default:
		if len(field.EnumValues) > 0 {
			return stringFrontendType(), nil
		}
		return anyFrontendType(), nil
	}
}

func (c *backendTypeChecker) entityRecordType(entity *model.Entity) (frontendType, error) {
	if entity == nil {
		return anyFrontendType(), nil
	}
	fields := map[string]frontendType{}
	order := make([]string, 0, len(entity.Fields))
	for _, field := range entity.Fields {
		value, err := c.entityFieldType(field)
		if err != nil {
			return frontendType{}, err
		}
		fields[field.Name] = value
		order = append(order, field.Name)
	}
	return recordFrontendTypeWithOrder(entity.Name, fields, order), nil
}

func aliasFieldOrder(alias *model.TypeAlias) []string {
	out := make([]string, 0, len(alias.Fields))
	for _, field := range alias.Fields {
		out = append(out, field.Name)
	}
	return out
}

func backendPrimaryField(entity *model.Entity) *model.Field {
	if entity == nil {
		return nil
	}
	for i := range entity.Fields {
		if entity.Fields[i].Name == entity.PrimaryKey {
			return &entity.Fields[i]
		}
	}
	return nil
}
