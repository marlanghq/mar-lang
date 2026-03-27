package runtime

import (
	"fmt"

	"mar/internal/expr"
	"mar/internal/model"
)

// compileExpressions parses and caches rule/authorization expressions for runtime evaluation.
func (r *Runtime) compileExpressions() error {
	for _, entity := range r.App.Entities {
		fieldVars := map[string]struct{}{}
		for _, field := range entity.Fields {
			fieldVars[field.Name] = struct{}{}
		}

		compiledRules := make([]compiledRule, 0, len(entity.Rules))
		for _, rule := range entity.Rules {
			exprNode, err := expr.Parse(rule.Expression, expr.ParserOptions{AllowedVariables: fieldVars})
			if err != nil {
				return fmt.Errorf("compile rule %s.%q: %w", entity.Name, rule.Expression, err)
			}
			compiledRules = append(compiledRules, compiledRule{
				Message:    rule.Message,
				Expression: rule.Expression,
				Expr:       exprNode,
			})
		}
		r.rules[entity.Name] = compiledRules

		exprVars := expr.AllowedVariablesWithBuiltins(fieldVars)
		authorizers := map[string]expr.Expr{}
		for _, rule := range entity.Authorizations {
			node, err := expr.Parse(rule.Expression, expr.ParserOptions{AllowedVariables: exprVars})
			if err != nil {
				return fmt.Errorf("compile authorization %s.%s: %w", entity.Name, rule.Action, err)
			}
			authorizers[rule.Action] = node
		}
		r.authorizers[entity.Name] = authorizers
	}

	if r.appAuthEnabled() && r.authUser == nil {
		return fmt.Errorf("built-in User entity not found")
	}
	return nil
}

// findField returns a field by name from an entity definition.
func findField(entity *model.Entity, name string) *model.Field {
	for i := range entity.Fields {
		if entity.Fields[i].Name == name {
			return &entity.Fields[i]
		}
	}
	return nil
}

// primaryField returns the field marked as the entity primary key.
func primaryField(entity *model.Entity) *model.Field {
	for i := range entity.Fields {
		if entity.Fields[i].Name == entity.PrimaryKey {
			return &entity.Fields[i]
		}
	}
	return nil
}

// typeToSQLite maps Mar primitive field types to SQLite column types.
func typeToSQLite(fieldType string) string {
	switch fieldType {
	case "Int":
		return "INTEGER"
	case "Date", "DateTime":
		return "INTEGER"
	case "String":
		return "TEXT"
	case "Bool":
		return "INTEGER"
	case "Float":
		return "REAL"
	default:
		return "TEXT"
	}
}
