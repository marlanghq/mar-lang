package runtime

import (
	"fmt"

	"mar/internal/expr"
	"mar/internal/model"
)

// compileExpressions parses and caches function/validation/authorization expressions for runtime evaluation.
func (r *Runtime) compileExpressions() error {
	functionArities := map[string]int{}
	for _, fn := range r.App.Functions {
		functionArities[fn.Name] = len(fn.Parameters)
	}

	for _, fn := range r.App.Functions {
		node, err := expr.Parse(fn.Expression, expr.ParserOptions{
			AllowedVariables: expr.AllowedVariablesWithBuiltins(functionParameterVariables(fn.Parameters)),
			AllowedFunctions: functionArities,
			AllowedRecords:   r.recordConstructors(),
			AllowedVariants:  r.variantConstructors(),
		})
		if err != nil {
			return fmt.Errorf("compile function %s: %w", fn.Name, err)
		}
		r.functions[fn.Name] = expr.UserFunction{
			Params: fn.Parameters,
			Body:   node,
		}
	}

	for _, entity := range r.App.Entities {
		fieldVars := map[string]struct{}{}
		for _, field := range entity.Fields {
			fieldVars[field.Name] = struct{}{}
		}
		for name := range r.enumLiteralValues {
			fieldVars[name] = struct{}{}
		}

		if entity.Validate != "" {
			node, err := expr.Parse(entity.Validate, expr.ParserOptions{
				AllowedVariables: fieldVars,
				AllowedFunctions: functionArities,
				AllowedRecords:   r.recordConstructors(),
				AllowedVariants:  r.variantConstructors(),
			})
			if err != nil {
				return fmt.Errorf("compile validation %s: %w", entity.Name, err)
			}
			r.validators[entity.Name] = node
		}

		exprVars := expr.AllowedVariablesWithBuiltins(fieldVars)
		authorizers := map[string]expr.Expr{}
		for _, authorization := range entity.Authorizations {
			node, err := expr.Parse(authorization.Expression, expr.ParserOptions{
				AllowedVariables: exprVars,
				AllowedFunctions: functionArities,
				AllowedRecords:   r.recordConstructors(),
				AllowedVariants:  r.variantConstructors(),
			})
			if err != nil {
				return fmt.Errorf("compile authorization %s.%s: %w", entity.Name, authorization.Action, err)
			}
			authorizers[authorization.Action] = node
		}
		r.authorizers[entity.Name] = authorizers
	}

	if r.appAuthEnabled() && r.authUser == nil {
		return fmt.Errorf("built-in User entity not found")
	}
	return nil
}

func functionParameterVariables(params []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, param := range params {
		out[param] = struct{}{}
	}
	return out
}

func (r *Runtime) recordConstructors() map[string][]string {
	out := map[string][]string{}
	if r == nil || r.App == nil {
		return out
	}
	for _, record := range r.App.Records {
		fields := make([]string, 0, len(record.Fields))
		for _, field := range record.Fields {
			fields = append(fields, field.Name)
		}
		out[record.Name] = fields
	}
	return out
}

func (r *Runtime) variantConstructors() map[string]int {
	out := map[string]int{}
	if r == nil || r.App == nil {
		return out
	}
	for _, typ := range r.App.Types {
		for _, variant := range typ.Variants {
			out[variant.Name] = len(variant.Fields)
		}
	}
	return out
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
	case "Decimal":
		return "DECIMAL_TEXT"
	default:
		return "TEXT"
	}
}
