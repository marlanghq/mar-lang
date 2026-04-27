// Package checkapp_test runs end-to-end HM type-check tests against real
// mar-lang programs (mini-twitter and friends). Lives outside `internal/types`
// so that the types package can import `internal/parser` without a cycle.
package checkapp_test

import (
	"os"
	"path/filepath"
	"testing"

	"mar/internal/expr"
	"mar/internal/model"
	"mar/internal/parser"
	"mar/internal/types"
)

func loadMiniTwitter(t *testing.T) *model.App {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := wd
	for i := 0; i < 5; i++ {
		path := filepath.Join(root, "examples", "mini-twitter.mar")
		if _, err := os.Stat(path); err == nil {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			app, err := parser.Parse(string(data))
			if err != nil {
				t.Fatalf("parse mini-twitter: %v", err)
			}
			return app
		}
		root = filepath.Dir(root)
	}
	t.Fatalf("mini-twitter.mar not found from %s", wd)
	return nil
}

func TestHMChecksMiniTwitterEntityValidates(t *testing.T) {
	app := loadMiniTwitter(t)
	at, err := types.BuildAppTypes(app)
	if err != nil {
		t.Fatalf("BuildAppTypes: %v", err)
	}

	for i := range app.Entities {
		entity := &app.Entities[i]
		if entity.Validate == "" {
			continue
		}
		t.Run("validate/"+entity.Name, func(t *testing.T) {
			parsed, err := parseEntityExpression(app, entity, entity.Validate, false)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if err := at.CheckEntityValidate(parsed, entity, app); err != nil {
				t.Errorf("HM check failed: %v", err)
			}
		})
	}
}

func TestHMChecksMiniTwitterEntityAuthorizes(t *testing.T) {
	app := loadMiniTwitter(t)
	at, err := types.BuildAppTypes(app)
	if err != nil {
		t.Fatalf("BuildAppTypes: %v", err)
	}

	for i := range app.Entities {
		entity := &app.Entities[i]
		for _, auth := range entity.Authorizations {
			t.Run("authorize/"+entity.Name+"/"+auth.Action, func(t *testing.T) {
				parsed, err := parseEntityExpression(app, entity, auth.Expression, true)
				if err != nil {
					t.Fatalf("parse: %v", err)
				}
				if err := at.CheckEntityAuthorize(parsed, entity, auth.Action, app); err != nil {
					t.Errorf("HM check failed: %v", err)
				}
			})
		}
	}
}

func TestHMChecksMiniTwitterQueries(t *testing.T) {
	app := loadMiniTwitter(t)
	at, err := types.BuildAppTypes(app)
	if err != nil {
		t.Fatalf("BuildAppTypes: %v", err)
	}

	for i := range app.Queries {
		query := &app.Queries[i]
		if query.Where == "" {
			continue
		}
		t.Run("query/"+query.Name, func(t *testing.T) {
			entity := types.FindEntityByName(app, query.Entity)
			if entity == nil {
				t.Fatalf("entity %s not found", query.Entity)
			}
			parsed, err := parseQueryExpression(app, entity, query, query.Where)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := at.CheckQueryWhere(parsed, query, app); err != nil {
				t.Errorf("HM check failed: %v", err)
			}
		})
	}
}

func TestHMChecksMiniTwitterActionSteps(t *testing.T) {
	app := loadMiniTwitter(t)
	at, err := types.BuildAppTypes(app)
	if err != nil {
		t.Fatalf("BuildAppTypes: %v", err)
	}

	for i := range app.Actions {
		action := &app.Actions[i]
		t.Run("action/"+action.Name, func(t *testing.T) {
			env, err := at.ActionEnv(action, app)
			if err != nil {
				t.Fatalf("ActionEnv: %v", err)
			}
			for _, step := range action.Steps {
				entity := types.FindEntityByName(app, step.Entity)
				if entity == nil {
					t.Errorf("step entity %s not found", step.Entity)
					continue
				}
				stepEnv := env
				if step.Alias != "" {
					stepEnv = stepEnv.Bind(step.Alias, at.Entities[entity.Name])
				}
				for _, value := range step.Values {
					field := findEntityFieldByName(entity, value.Field)
					if field == nil {
						t.Errorf("step field %s not found in entity %s", value.Field, entity.Name)
						continue
					}
					expected, err := at.EntityFieldType(app, field)
					if err != nil {
						t.Errorf("EntityFieldType for %s: %v", value.Field, err)
						continue
					}
					parsed, err := parseActionStepExpression(app, value.Expression, action)
					if err != nil {
						t.Errorf("parse step %s.%s: %v", entity.Name, value.Field, err)
						continue
					}
					if err := at.CheckActionStep(parsed, expected, stepEnv); err != nil {
						t.Errorf("HM check %s.%s failed: %v", entity.Name, value.Field, err)
					}
				}
			}
		})
	}
}

func TestHMCheckAppMiniTwitterTopLevel(t *testing.T) {
	// Top-level smoke: full CheckApp on mini-twitter must succeed.
	app := loadMiniTwitter(t)
	if err := types.CheckApp(app); err != nil {
		t.Fatalf("CheckApp(mini-twitter) failed: %v", err)
	}
}

func findEntityFieldByName(entity *model.Entity, name string) *model.Field {
	for i := range entity.Fields {
		if entity.Fields[i].Name == name {
			return &entity.Fields[i]
		}
	}
	return nil
}

func parseEntityExpression(app *model.App, entity *model.Entity, raw string, includeBuiltins bool) (expr.Expr, error) {
	fieldVars := map[string]struct{}{}
	for _, f := range entity.Fields {
		fieldVars[f.Name] = struct{}{}
	}
	for _, typ := range app.Types {
		for _, v := range typ.Values {
			fieldVars[v] = struct{}{}
		}
		for _, v := range typ.Variants {
			fieldVars[v.Name] = struct{}{}
		}
	}

	allowedVars := fieldVars
	if includeBuiltins {
		allowedVars = expr.AllowedVariablesWithBuiltins(fieldVars)
	}

	functionArities := map[string]int{}
	for _, fn := range app.Functions {
		functionArities[fn.Name] = len(fn.Parameters)
	}
	recordFields := map[string][]string{}
	for _, r := range app.Records {
		fields := make([]string, 0, len(r.Fields))
		for _, f := range r.Fields {
			fields = append(fields, f.Name)
		}
		recordFields[r.Name] = fields
	}
	variantArities := map[string]int{}
	for _, typ := range app.Types {
		for _, v := range typ.Variants {
			variantArities[v.Name] = len(v.Fields)
		}
	}

	return expr.Parse(raw, expr.ParserOptions{
		AllowedVariables: allowedVars,
		AllowedFunctions: functionArities,
		AllowedRecords:   recordFields,
		AllowedVariants:  variantArities,
	})
}

func parseQueryExpression(app *model.App, entity *model.Entity, query *model.Query, raw string) (expr.Expr, error) {
	fieldVars := map[string]struct{}{}
	for _, f := range entity.Fields {
		fieldVars[f.Name] = struct{}{}
	}
	for _, p := range query.Parameters {
		fieldVars[p] = struct{}{}
	}
	allowedVars := expr.AllowedVariablesWithBuiltins(fieldVars)

	functionArities := map[string]int{}
	for _, fn := range app.Functions {
		functionArities[fn.Name] = len(fn.Parameters)
	}

	return expr.Parse(raw, expr.ParserOptions{
		AllowedVariables: allowedVars,
		AllowedFunctions: functionArities,
	})
}

func parseActionStepExpression(app *model.App, raw string, action *model.Action) (expr.Expr, error) {
	allowedVars := map[string]struct{}{}
	alias := types.FindAlias(app, action.InputAlias)
	if alias != nil {
		for _, f := range alias.Fields {
			allowedVars[f.Name] = struct{}{}
		}
	}
	for _, step := range action.Steps {
		if step.Alias != "" {
			allowedVars[step.Alias] = struct{}{}
		}
	}
	allowedVars["input"] = struct{}{}
	allowedVars = expr.AllowedVariablesWithBuiltins(allowedVars)

	functionArities := map[string]int{}
	for _, fn := range app.Functions {
		functionArities[fn.Name] = len(fn.Parameters)
	}
	recordFields := map[string][]string{}
	for _, r := range app.Records {
		fields := make([]string, 0, len(r.Fields))
		for _, f := range r.Fields {
			fields = append(fields, f.Name)
		}
		recordFields[r.Name] = fields
	}

	return expr.Parse(raw, expr.ParserOptions{
		AllowedVariables: allowedVars,
		AllowedFunctions: functionArities,
		AllowedRecords:   recordFields,
	})
}
