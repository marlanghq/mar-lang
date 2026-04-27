package parser

import (
	"fmt"
	"strings"

	"mar/internal/model"
	"mar/internal/sexp"
)
func validateUnusedTopLevelDefinitions(appDecl *appDef, queries map[string]*queryDef, actions map[string]*actionDef, screens map[string]*screenDef, values map[string]namedValue) error {
	selectedQueries := make(map[string]struct{}, len(appDecl.QuerySymbols))
	for _, symbol := range appDecl.QuerySymbols {
		selectedQueries[symbol] = struct{}{}
	}
	for name := range queries {
		if _, ok := selectedQueries[name]; !ok {
			return fmt.Errorf("query %q is defined but not exposed in define-app", name)
		}
	}

	selectedActions := make(map[string]struct{}, len(appDecl.ActionSymbols))
	for _, symbol := range appDecl.ActionSymbols {
		selectedActions[symbol] = struct{}{}
	}
	for name := range actions {
		if _, ok := selectedActions[name]; !ok {
			return fmt.Errorf("action %q is defined but not exposed in define-app", name)
		}
	}

	selectedScreens := make(map[string]struct{}, len(appDecl.ScreenSymbols))
	for _, symbol := range appDecl.ScreenSymbols {
		selectedScreens[symbol] = struct{}{}
	}
	for name := range screens {
		if _, ok := selectedScreens[name]; !ok {
			return fmt.Errorf("screen %q is defined but not exposed in define-app", name)
		}
	}
	return nil
}

func validateUnusedFunctions(app *model.App) error {
	if app == nil {
		return nil
	}
	functionByName := map[string]*model.Function{}
	for i := range app.Functions {
		functionByName[app.Functions[i].Name] = &app.Functions[i]
	}
	used := map[string]bool{}
	for _, entity := range app.Entities {
		if strings.TrimSpace(entity.Validate) != "" {
			collectUsedFunctionsFromExpression(entity.Validate, functionByName, used)
		}
		for _, auth := range entity.Authorizations {
			if strings.TrimSpace(auth.Expression) != "" {
				collectUsedFunctionsFromExpression(auth.Expression, functionByName, used)
			}
		}
	}
	for _, query := range app.Queries {
		if strings.TrimSpace(query.Where) != "" {
			collectUsedFunctionsFromExpression(query.Where, functionByName, used)
		}
	}
	for _, action := range app.Actions {
		for _, step := range action.Steps {
			for _, value := range step.Values {
				collectUsedFunctionsFromExpression(value.Expression, functionByName, used)
			}
		}
	}
	if app.Screens != nil {
		for _, screen := range app.Screens.Screens {
			if strings.TrimSpace(screen.InitExpression) != "" {
				collectUsedFunctionsFromExpression(screen.InitExpression, functionByName, used)
			}
			if strings.TrimSpace(screen.UpdateBody) != "" {
				collectUsedFunctionsFromExpression(screen.UpdateBody, functionByName, used)
			}
			for _, section := range screen.Sections {
				for _, item := range section.Items {
					collectUsedFunctionsFromItem(item, functionByName, used)
				}
			}
			if screen.View != nil {
				collectUsedFunctionsFromViewNode(*screen.View, functionByName, used)
			}
		}
	}

	for _, fn := range app.Functions {
		if !used[fn.Name] {
			return fmt.Errorf("function %q is defined but never used", fn.Name)
		}
	}
	return nil
}

func collectUsedFunctionsFromExpression(raw string, functions map[string]*model.Function, used map[string]bool) {
	node, err := sexp.ParseOne(raw)
	if err != nil {
		return
	}
	collectUsedFunctionsFromNode(node, functions, used)
}

func collectUsedFunctionsFromNode(node sexp.Node, functions map[string]*model.Function, used map[string]bool) {
	if node.Kind != sexp.KindList || len(node.Children) == 0 {
		return
	}
	if node.Children[0].Kind == sexp.KindSymbol {
		name := canonicalFunctionName(node.Children[0].Value)
		if fn := functions[name]; fn != nil {
			if !used[name] {
				used[name] = true
				collectUsedFunctionsFromExpression(fn.Expression, functions, used)
			}
		}
	}
	for _, child := range node.Children[1:] {
		collectUsedFunctionsFromNode(child, functions, used)
	}
}
func collectUsedFunctionsFromItem(item model.FrontendItem, functions map[string]*model.Function, used map[string]bool) {
	if strings.TrimSpace(item.Condition) != "" {
		collectUsedFunctionsFromExpression(item.Condition, functions, used)
	}
	if strings.TrimSpace(item.Message) != "" {
		collectUsedFunctionsFromExpression(item.Message, functions, used)
	}
	if strings.TrimSpace(item.Text) != "" {
		collectUsedFunctionsFromExpression(item.Text, functions, used)
	}
	for _, value := range item.Values {
		collectUsedFunctionsFromExpression(value.Expression, functions, used)
	}
}

func collectUsedFunctionsFromViewNode(node model.FrontendViewNode, functions map[string]*model.Function, used map[string]bool) {
	if strings.TrimSpace(node.Message) != "" {
		collectUsedFunctionsFromExpression(node.Message, functions, used)
	}
	for _, child := range node.Children {
		collectUsedFunctionsFromViewNode(child, functions, used)
	}
}
