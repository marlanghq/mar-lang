package runtime

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"mar/internal/expr"
	"mar/internal/model"
)

type readAuthSQLExpr struct {
	sql     string
	args    []any
	isNull  bool
	isBool  bool
	boolVal bool
}

func (r *Runtime) buildListQuery(entity *model.Entity, auth authSession, queryValues url.Values) (string, []any, error) {
	table, err := quoteIdentifier(entity.Table)
	if err != nil {
		return "", nil, err
	}
	pk, err := quoteIdentifier(entity.PrimaryKey)
	if err != nil {
		return "", nil, err
	}

	query := fmt.Sprintf("SELECT * FROM %s", table)
	var (
		args          []any
		whereClauses  []string
	)
	if whereSQL, whereArgs, ok := r.listReadAuthorizationWhere(entity, auth); ok {
		whereClauses = append(whereClauses, whereSQL)
		args = append(args, whereArgs...)
	}
	filterSQL, filterArgs, err := buildListFilterWhere(entity, queryValues)
	if err != nil {
		return "", nil, err
	}
	if filterSQL != "" {
		whereClauses = append(whereClauses, filterSQL)
		args = append(args, filterArgs...)
	}
	if len(whereClauses) > 0 {
		query += " WHERE " + strings.Join(whereClauses, " AND ")
	}
	query += fmt.Sprintf(" ORDER BY %s DESC", pk)
	return query, args, nil
}

func buildListFilterWhere(entity *model.Entity, queryValues url.Values) (string, []any, error) {
	if entity == nil || len(queryValues) == 0 {
		return "", nil, nil
	}

	clauses := []string{}
	args := []any{}
	keys := make([]string, 0, len(queryValues))
	for key := range queryValues {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		values := queryValues[key]
		field := runtimeFindEntityField(entity, key)
		if field == nil {
			return "", nil, newAPIError(400, "invalid_list_filter", fmt.Sprintf("Unknown filter field %s", key))
		}
		if len(values) != 1 {
			return "", nil, newAPIError(400, "invalid_list_filter", fmt.Sprintf("Filter field %s must appear only once", key))
		}
		dbValue, err := normalizeListFilterValue(field, values[0])
		if err != nil {
			return "", nil, newAPIError(400, "invalid_list_filter", err.Error())
		}
		column, err := quoteIdentifier(model.FieldStorageName(field))
		if err != nil {
			return "", nil, err
		}
		clauses = append(clauses, fmt.Sprintf("%s = ?", column))
		args = append(args, dbValue)
	}
	return strings.Join(clauses, " AND "), args, nil
}

func runtimeFindEntityField(entity *model.Entity, name string) *model.Field {
	if entity == nil {
		return nil
	}
	for i := range entity.Fields {
		if entity.Fields[i].Name == name {
			return &entity.Fields[i]
		}
	}
	return nil
}

func normalizeListFilterValue(field *model.Field, raw string) (any, error) {
	if field == nil {
		return nil, fmt.Errorf("filter field is required")
	}
	switch field.Type {
	case "Int":
		n, ok := toInt64(strings.TrimSpace(raw))
		if !ok {
			return nil, fmt.Errorf("filter %s must be Int", field.Name)
		}
		return n, nil
	case "Date":
		n, ok := toInt64(strings.TrimSpace(raw))
		if !ok {
			return nil, fmt.Errorf("filter %s must be Date (Unix milliseconds)", field.Name)
		}
		return normalizeDateMillis(n), nil
	case "DateTime":
		n, ok := toInt64(strings.TrimSpace(raw))
		if !ok {
			return nil, fmt.Errorf("filter %s must be DateTime (Unix milliseconds)", field.Name)
		}
		return n, nil
	case "Float":
		f, ok := toFloat64(strings.TrimSpace(raw))
		if !ok {
			return nil, fmt.Errorf("filter %s must be Float", field.Name)
		}
		return f, nil
	case "Bool":
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "true":
			return int64(1), nil
		case "false":
			return int64(0), nil
		default:
			return nil, fmt.Errorf("filter %s must be Bool", field.Name)
		}
	case "String":
		return raw, nil
	default:
		if len(field.EnumValues) > 0 {
			for _, enumValue := range field.EnumValues {
				if raw == enumValue {
					return raw, nil
				}
			}
			return nil, fmt.Errorf("filter %s must be one of: %s", field.Name, strings.Join(field.EnumValues, ", "))
		}
		return nil, fmt.Errorf("unsupported filter type %s", field.Type)
	}
}

func (r *Runtime) listReadAuthorizationWhere(entity *model.Entity, auth authSession) (string, []any, bool) {
	if r == nil || entity == nil || !r.appAuthEnabled() {
		return "", nil, false
	}
	if r.allowAdminBuiltInUserAccess(entity, "read", auth) {
		return "", nil, false
	}

	authorizers := r.authorizers[entity.Name]
	rule, ok := authorizers["read"]
	if !ok {
		return "", nil, false
	}

	compiled, ok := r.compileReadAuthPredicateSQL(entity, auth, rule)
	if !ok || compiled.sql == "" {
		return "", nil, false
	}
	if compiled.isBool {
		if compiled.boolVal {
			return "", nil, false
		}
		return "0", nil, true
	}
	return compiled.sql, compiled.args, true
}

func (r *Runtime) compileReadAuthPredicateSQL(entity *model.Entity, auth authSession, node expr.Expr) (readAuthSQLExpr, bool) {
	switch n := node.(type) {
	case expr.Literal:
		return readAuthPredicateExprFromValue(n.Value)
	case expr.Variable:
		if expr.IsBuiltinValueName(n.Name) {
			value, ok := readAuthBuiltinValue(auth, n.Name)
			if !ok {
				return readAuthSQLExpr{}, false
			}
			return readAuthPredicateExprFromValue(value)
		}
		if value, ok := r.enumLiteralValues[n.Name]; ok {
			return readAuthPredicateExprFromValue(value)
		}
		return readAuthSQLExpr{}, false
	case expr.Unary:
		if n.Op != "not" {
			return readAuthSQLExpr{}, false
		}
		right, ok := r.compileReadAuthPredicateSQL(entity, auth, n.Right)
		if !ok {
			return readAuthSQLExpr{}, false
		}
		if right.isBool {
			return readAuthSQLExpr{sql: boolSQL(!right.boolVal), isBool: true, boolVal: !right.boolVal}, true
		}
		return readAuthSQLExpr{
			sql:  fmt.Sprintf("(NOT %s)", right.sql),
			args: append([]any{}, right.args...),
		}, true
	case expr.Binary:
		switch n.Op {
		case "and", "or":
			left, ok := r.compileReadAuthPredicateSQL(entity, auth, n.Left)
			if !ok {
				return readAuthSQLExpr{}, false
			}
			right, ok := r.compileReadAuthPredicateSQL(entity, auth, n.Right)
			if !ok {
				return readAuthSQLExpr{}, false
			}
			if simplified, ok := simplifyLogicalPredicateSQL(n.Op, left, right); ok {
				return simplified, true
			}
			return readAuthSQLExpr{
				sql:  fmt.Sprintf("(%s %s %s)", left.sql, sqlLogicalOp(n.Op), right.sql),
				args: append(append([]any{}, left.args...), right.args...),
			}, true
		case "==", "!=", ">", ">=", "<", "<=":
			left, ok := r.compileReadAuthScalarSQL(entity, auth, n.Left)
			if !ok {
				return readAuthSQLExpr{}, false
			}
			right, ok := r.compileReadAuthScalarSQL(entity, auth, n.Right)
			if !ok {
				return readAuthSQLExpr{}, false
			}
			if simplified, ok := simplifyComparisonPredicateSQL(n.Op, left, right); ok {
				return simplified, true
			}
			if (n.Op == "==" || n.Op == "!=") && (left.isNull || right.isNull) {
				op := "IS"
				if n.Op == "!=" {
					op = "IS NOT"
				}
				return readAuthSQLExpr{
					sql:  fmt.Sprintf("(%s %s %s)", left.sql, op, right.sql),
					args: append(append([]any{}, left.args...), right.args...),
				}, true
			}
			if left.isNull || right.isNull {
				return readAuthSQLExpr{}, false
			}
			return readAuthSQLExpr{
				sql:  fmt.Sprintf("(%s %s %s)", left.sql, sqlComparisonOp(n.Op), right.sql),
				args: append(append([]any{}, left.args...), right.args...),
			}, true
		default:
			return readAuthSQLExpr{}, false
		}
	default:
		return readAuthSQLExpr{}, false
	}
}

func (r *Runtime) compileReadAuthScalarSQL(entity *model.Entity, auth authSession, node expr.Expr) (readAuthSQLExpr, bool) {
	switch n := node.(type) {
	case expr.Literal:
		return readAuthScalarExprFromValue(n.Value)
	case expr.Variable:
		if field := findField(entity, n.Name); field != nil {
			column, err := quoteIdentifier(model.FieldStorageName(field))
			if err != nil {
				return readAuthSQLExpr{}, false
			}
			return readAuthSQLExpr{sql: column}, true
		}
		if expr.IsBuiltinValueName(n.Name) {
			value, ok := readAuthBuiltinValue(auth, n.Name)
			if !ok {
				return readAuthSQLExpr{}, false
			}
			return readAuthScalarExprFromValue(value)
		}
		if value, ok := r.enumLiteralValues[n.Name]; ok {
			return readAuthScalarExprFromValue(value)
		}
		return readAuthSQLExpr{}, false
	default:
		return readAuthSQLExpr{}, false
	}
}

func readAuthPredicateExprFromValue(value any) (readAuthSQLExpr, bool) {
	switch v := value.(type) {
	case bool:
		if v {
			return readAuthSQLExpr{sql: "1", isBool: true, boolVal: true}, true
		}
		return readAuthSQLExpr{sql: "0", isBool: true, boolVal: false}, true
	case nil:
		return readAuthSQLExpr{sql: "0", isBool: true, boolVal: false}, true
	default:
		return readAuthSQLExpr{}, false
	}
}

func readAuthScalarExprFromValue(value any) (readAuthSQLExpr, bool) {
	normalized, ok := readAuthSQLValue(value)
	if !ok {
		return readAuthSQLExpr{}, false
	}
	if normalized == nil {
		return readAuthSQLExpr{sql: "NULL", isNull: true}, true
	}
	return readAuthSQLExpr{sql: "?", args: []any{normalized}}, true
}

func readAuthBuiltinValue(auth authSession, name string) (any, bool) {
	switch name {
	case "anonymous":
		return !auth.Authenticated, true
	case "user_authenticated":
		return auth.Authenticated, true
	case "user_email":
		return auth.Email, true
	case "user_id":
		return auth.UserID, true
	case "user_role":
		return auth.Role, true
	default:
		return nil, false
	}
}

func readAuthSQLValue(value any) (any, bool) {
	switch v := value.(type) {
	case nil:
		return nil, true
	case bool:
		if v {
			return int64(1), true
		}
		return int64(0), true
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case string:
		return v, true
	default:
		return nil, false
	}
}

func sqlLogicalOp(op string) string {
	if op == "and" {
		return "AND"
	}
	return "OR"
}

func sqlComparisonOp(op string) string {
	if op == "==" {
		return "="
	}
	return op
}

func boolSQL(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func simplifyLogicalPredicateSQL(op string, left, right readAuthSQLExpr) (readAuthSQLExpr, bool) {
	if !left.isBool && !right.isBool {
		return readAuthSQLExpr{}, false
	}

	switch op {
	case "and":
		if left.isBool && !left.boolVal {
			return readAuthSQLExpr{sql: "0", isBool: true, boolVal: false}, true
		}
		if right.isBool && !right.boolVal {
			return readAuthSQLExpr{sql: "0", isBool: true, boolVal: false}, true
		}
		if left.isBool && left.boolVal {
			return right, true
		}
		if right.isBool && right.boolVal {
			return left, true
		}
	case "or":
		if left.isBool && left.boolVal {
			return readAuthSQLExpr{sql: "1", isBool: true, boolVal: true}, true
		}
		if right.isBool && right.boolVal {
			return readAuthSQLExpr{sql: "1", isBool: true, boolVal: true}, true
		}
		if left.isBool && !left.boolVal {
			return right, true
		}
		if right.isBool && !right.boolVal {
			return left, true
		}
	}

	return readAuthSQLExpr{}, false
}

func simplifyComparisonPredicateSQL(op string, left, right readAuthSQLExpr) (readAuthSQLExpr, bool) {
	if len(left.args) == 1 && len(right.args) == 1 && left.sql == "?" && right.sql == "?" {
		result, ok := compareReadAuthConstantArgs(op, left.args[0], right.args[0])
		if !ok {
			return readAuthSQLExpr{}, false
		}
		return readAuthSQLExpr{sql: boolSQL(result), isBool: true, boolVal: result}, true
	}
	return readAuthSQLExpr{}, false
}

func compareReadAuthConstantArgs(op string, left, right any) (bool, bool) {
	switch op {
	case "==":
		return left == right, true
	case "!=":
		return left != right, true
	default:
		return false, false
	}
}
