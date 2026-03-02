package runtime

import (
	"fmt"

	"belm/internal/expr"
	"belm/internal/model"
)

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

		authVars := map[string]struct{}{
			"auth_authenticated": {},
			"auth_email":         {},
			"auth_user_id":       {},
			"auth_role":          {},
		}
		for name := range fieldVars {
			authVars[name] = struct{}{}
		}
		authorizers := map[string]expr.Expr{}
		for _, rule := range entity.Authorizations {
			node, err := expr.Parse(rule.Expression, expr.ParserOptions{AllowedVariables: authVars, AllowRoleFunc: true})
			if err != nil {
				return fmt.Errorf("compile authorization %s.%s: %w", entity.Name, rule.Action, err)
			}
			authorizers[rule.Action] = node
		}
		r.authorizers[entity.Name] = authorizers
	}

	if r.authEnabled() && r.authUser == nil {
		return fmt.Errorf("auth.user_entity %q not found", r.App.Auth.UserEntity)
	}
	return nil
}

func findField(entity *model.Entity, name string) *model.Field {
	for i := range entity.Fields {
		if entity.Fields[i].Name == name {
			return &entity.Fields[i]
		}
	}
	return nil
}

func primaryField(entity *model.Entity) *model.Field {
	for i := range entity.Fields {
		if entity.Fields[i].Name == entity.PrimaryKey {
			return &entity.Fields[i]
		}
	}
	return nil
}

func typeToSQLite(fieldType string) string {
	switch fieldType {
	case "Int":
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
