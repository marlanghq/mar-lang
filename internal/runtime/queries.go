package runtime

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"

	"mar/internal/expr"
	"mar/internal/model"
)

func (r *Runtime) handleQuery(w http.ResponseWriter, requestID, queryName string, auth authSession, values url.Values) error {
	query := r.queriesByName[queryName]
	if query == nil {
		return newAPIError(http.StatusNotFound, "query_not_found", "Query not found")
	}
	params, err := normalizeQueryParams(query, values)
	if err != nil {
		return newAPIError(http.StatusBadRequest, "invalid_query_input", err.Error())
	}

	rows, err := r.runQuery(requestID, query, auth, params)
	if err != nil {
		return err
	}

	r.writeJSON(w, http.StatusOK, rows)
	return nil
}

func (r *Runtime) runQuery(requestID string, query *model.Query, auth authSession, params map[string]any) ([]map[string]any, error) {
	entity := r.entitiesByName[query.Entity]
	if entity == nil {
		return nil, newAPIError(http.StatusInternalServerError, "query_misconfigured", fmt.Sprintf("Query %s references unknown entity %s", query.Name, query.Entity))
	}
	if !r.hasAuthorizer(entity, "read", auth) {
		if !auth.Authenticated {
			return nil, newAPIError(http.StatusUnauthorized, "auth_required", "Authentication required")
		}
		return nil, newAPIError(http.StatusForbidden, "not_authorized", fmt.Sprintf("Not authorized to read %s", entity.Name))
	}

	table, _ := quoteIdentifier(entity.Table)
	rows, err := queryRowsForRequest(r.DB, requestID, fmt.Sprintf("SELECT * FROM %s", table))
	if err != nil {
		return nil, err
	}

	matchesQuery, err := r.queryPredicate(query, entity)
	if err != nil {
		return nil, err
	}

	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		decoded := decodeEntityRow(entity, row)
		allowed, err := r.evaluateAuthorization(entity, "read", auth, decoded)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		match, err := matchesQuery(auth, decoded, params)
		if err != nil {
			return nil, err
		}
		if match {
			out = append(out, decoded)
		}
	}

	if query.OrderBy != "" {
		sort.SliceStable(out, func(i, j int) bool {
			left := fmt.Sprint(out[i][query.OrderBy])
			right := fmt.Sprint(out[j][query.OrderBy])
			if query.OrderDir == "desc" {
				return left > right
			}
			return left < right
		})
	}
	if query.Limit != nil && *query.Limit < len(out) {
		out = out[:*query.Limit]
	}
	return out, nil
}

func (r *Runtime) queryPredicate(query *model.Query, entity *model.Entity) (func(authSession, map[string]any, map[string]any) (bool, error), error) {
	if query.Where == "" {
		return func(authSession, map[string]any, map[string]any) (bool, error) {
			return true, nil
		}, nil
	}

	fieldVars := map[string]struct{}{}
	for _, field := range entity.Fields {
		fieldVars[field.Name] = struct{}{}
	}
	for _, param := range query.Parameters {
		fieldVars[param] = struct{}{}
	}
	for name := range r.enumLiteralValues {
		fieldVars[name] = struct{}{}
	}
	functionArities := map[string]int{}
	for name, fn := range r.functions {
		functionArities[name] = len(fn.Params)
	}
	node, err := expr.Parse(query.Where, expr.ParserOptions{
		AllowedVariables: expr.AllowedVariablesWithBuiltins(fieldVars),
		AllowedFunctions: functionArities,
		AllowedRecords:   r.recordConstructors(),
		AllowedVariants:  r.variantConstructors(),
	})
	if err != nil {
		return nil, err
	}
	return func(auth authSession, row map[string]any, params map[string]any) (bool, error) {
		context := r.evaluationContext(entity, row, auth, true)
		for key, value := range params {
			context[key] = value
		}
		value, err := node.Eval(context)
		if err != nil {
			return false, err
		}
		match, err := expr.RequireBool(value)
		if err != nil {
			return false, newAPIError(http.StatusInternalServerError, "query_misconfigured", fmt.Sprintf("Query %s where must return bool", query.Name))
		}
		return match, nil
	}, nil
}

func normalizeQueryParams(query *model.Query, values url.Values) (map[string]any, error) {
	params := map[string]any{}
	for _, param := range query.Parameters {
		raw := values.Get(param)
		if raw == "" {
			return nil, fmt.Errorf("query %s expects parameter %s", query.Name, param)
		}
		paramType, ok := query.ParameterTypes[param]
		if !ok || paramType == "" {
			return nil, fmt.Errorf("query %s parameter %s has no inferred type", query.Name, param)
		}
		value, err := normalizeQueryParamValue(query.Name, param, paramType, raw)
		if err != nil {
			return nil, err
		}
		params[param] = value
	}
	return params, nil
}

func normalizeQueryParamValue(queryName, param, paramType, raw string) (any, error) {
	switch paramType {
	case "String":
		return raw, nil
	case "Bool":
		switch raw {
		case "true":
			return true, nil
		case "false":
			return false, nil
		default:
			return nil, fmt.Errorf("query %s parameter %s must be Bool", queryName, param)
		}
	case "Int":
		value, ok := toInt64(raw)
		if !ok {
			return nil, fmt.Errorf("query %s parameter %s must be Int", queryName, param)
		}
		return value, nil
	case "Decimal":
		value, ok := toDecimal(raw)
		if !ok {
			return nil, fmt.Errorf("query %s parameter %s must be Decimal", queryName, param)
		}
		return value, nil
	case "Date":
		value, ok := toInt64(raw)
		if !ok {
			return nil, fmt.Errorf("query %s parameter %s must be Date (Unix milliseconds)", queryName, param)
		}
		return normalizeDateMillis(value), nil
	case "DateTime":
		value, ok := toInt64(raw)
		if !ok {
			return nil, fmt.Errorf("query %s parameter %s must be DateTime (Unix milliseconds)", queryName, param)
		}
		return value, nil
	default:
		return nil, fmt.Errorf("query %s parameter %s has unsupported type %s", queryName, param, paramType)
	}
}
