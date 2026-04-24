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

func inferScreenParameterTypes(frontend *model.Frontend, actions []model.Action, aliases map[string]*model.TypeAlias, functions map[string]*model.Function, records map[string]*model.Record, types map[string]*model.EnumType, entities map[string]*model.Entity) (map[string][]frontendType, error) {
	_ = actions
	_ = aliases
	if frontend == nil {
		return map[string][]frontendType{}, nil
	}
	screenByName := map[string]model.FrontendScreen{}
	result := map[string][]frontendType{}
	for _, screen := range frontend.Screens {
		screenByName[screen.Name] = screen
		result[screen.Name] = make([]frontendType, len(screen.Parameters))
	}

	for iteration := 0; iteration < 8; iteration++ {
		changed := false
		typeChecker := newFrontendTypeChecker(functions, records, types, entities, result, nil)
		for _, screen := range frontend.Screens {
			for _, section := range screen.Sections {
				for _, item := range section.Items {
					itemChanged, err := inferScreenItemParameterTypes(item, screenByName, typeChecker, result)
					if err != nil {
						return nil, fmt.Errorf("screen %s: %w", screen.Name, err)
					}
					changed = changed || itemChanged
				}
			}
			if strings.TrimSpace(screen.InitExpression) != "" {
				initChanged, err := inferScreenParametersFromTransition(screen, screen.InitExpression, typeChecker, screenByName, result)
				if err != nil {
					return nil, fmt.Errorf("screen %s init: %w", screen.Name, err)
				}
				changed = changed || initChanged
			}
			if strings.TrimSpace(screen.UpdateBody) != "" {
				updateChanged, err := inferScreenParametersFromUpdate(screen, typeChecker, screenByName, result)
				if err != nil {
					return nil, fmt.Errorf("screen %s update: %w", screen.Name, err)
				}
				changed = changed || updateChanged
			}
		}
		if !changed {
			break
		}
	}
	for _, screen := range frontend.Screens {
		for index, param := range screen.Parameters {
			if frontendTypeIsUnresolved(result[screen.Name][index]) {
				return nil, fmt.Errorf("screen %s parameter %s type could not be inferred", screen.Name, param)
			}
		}
	}
	return result, nil
}

func frontendActionInputTypes(actions []model.Action, aliases map[string]*model.TypeAlias, typeChecker *frontendTypeChecker) (map[string][]frontendType, error) {
	out := map[string][]frontendType{}
	for _, action := range actions {
		alias := aliases[action.InputAlias]
		if alias == nil {
			continue
		}
		values := make([]frontendType, 0, len(alias.Fields))
		for _, field := range alias.Fields {
			value, err := typeChecker.parseTypeExpr(field.Type)
			if err != nil {
				return nil, fmt.Errorf("action %s input %s: %w", action.Name, field.Name, err)
			}
			values = append(values, value)
		}
		out[action.Name] = values
	}
	return out, nil
}

func inferScreenParametersFromLocalUsage(screen model.FrontendScreen, typeChecker *frontendTypeChecker, actionInputs map[string][]frontendType, result map[string][]frontendType) (bool, error) {
	if len(screen.Parameters) == 0 {
		return false, nil
	}
	changed := false
	for _, section := range screen.Sections {
		for _, item := range section.Items {
			next, err := inferScreenParameterTypesFromItemUsage(screen, item, typeChecker, actionInputs, result)
			if err != nil {
				return false, err
			}
			changed = changed || next
		}
	}
	for _, raw := range []string{screen.TitleExpression, screen.InitExpression, screen.UpdateBody, screen.ViewBody} {
		next, err := inferScreenParameterTypesFromExpression(screen, raw, typeChecker, actionInputs, result)
		if err != nil {
			return false, err
		}
		changed = changed || next
	}
	return changed, nil
}

func inferScreenParameterTypesFromItemUsage(screen model.FrontendScreen, item model.FrontendItem, typeChecker *frontendTypeChecker, actionInputs map[string][]frontendType, result map[string][]frontendType) (bool, error) {
	changed := false
	if item.Kind == "field" && len(screen.Parameters) > 0 && item.Field != "" {
		paramType, ok, err := typeChecker.namedType(screen.Parameters[0])
		if err != nil {
			return false, err
		}
		if ok {
			if _, err := frontendRecordFieldType(paramType, item.Field); err == nil {
				next, err := mergeScreenParameterType(result, screen, 0, paramType)
				if err != nil {
					return false, err
				}
				changed = changed || next
			}
		}
	}
	for _, raw := range []string{item.Condition, item.Message} {
		next, err := inferScreenParameterTypesFromExpression(screen, raw, typeChecker, actionInputs, result)
		if err != nil {
			return false, err
		}
		changed = changed || next
	}
	for _, value := range item.Values {
		next, err := inferScreenParameterTypesFromExpression(screen, value.Expression, typeChecker, actionInputs, result)
		if err != nil {
			return false, err
		}
		changed = changed || next
	}
	return changed, nil
}

func inferScreenParameterTypesFromExpression(screen model.FrontendScreen, raw string, typeChecker *frontendTypeChecker, actionInputs map[string][]frontendType, result map[string][]frontendType) (bool, error) {
	if strings.TrimSpace(raw) == "" {
		return false, nil
	}
	node, err := sexp.ParseOne(raw)
	if err != nil {
		return false, err
	}
	return inferScreenParameterTypesFromNode(screen, node, typeChecker, actionInputs, result)
}

func inferScreenParameterTypesFromNode(screen model.FrontendScreen, node sexp.Node, typeChecker *frontendTypeChecker, actionInputs map[string][]frontendType, result map[string][]frontendType) (bool, error) {
	changed := false
	if node.Kind == sexp.KindSymbol && strings.Contains(node.Value, ".") {
		next, err := inferScreenParameterTypesFromDottedSymbol(screen, node.Value, typeChecker, result)
		if err != nil {
			return false, err
		}
		changed = changed || next
	}
	if node.Kind != sexp.KindList || len(node.Children) == 0 {
		return changed, nil
	}
	if node.Children[0].Kind == sexp.KindSymbol {
		head := canonicalFunctionName(node.Children[0].Value)
		if signature, ok := actionInputs[head]; ok {
			for index, expected := range signature {
				argIndex := index + 1
				if argIndex >= len(node.Children) || node.Children[argIndex].Kind != sexp.KindSymbol {
					continue
				}
				argName := canonicalFieldName(node.Children[argIndex].Value)
				paramIndex := screenParameterIndex(screen, argName)
				if paramIndex < 0 {
					continue
				}
				next, err := mergeScreenParameterType(result, screen, paramIndex, expected)
				if err != nil {
					return false, err
				}
				changed = changed || next
			}
		}
	}
	for _, child := range node.Children {
		next, err := inferScreenParameterTypesFromNode(screen, child, typeChecker, actionInputs, result)
		if err != nil {
			return false, err
		}
		changed = changed || next
	}
	return changed, nil
}

func inferScreenParameterTypesFromDottedSymbol(screen model.FrontendScreen, raw string, typeChecker *frontendTypeChecker, result map[string][]frontendType) (bool, error) {
	parts := strings.Split(raw, ".")
	if len(parts) < 2 {
		return false, nil
	}
	root := canonicalFieldName(parts[0])
	paramIndex := screenParameterIndex(screen, root)
	if paramIndex < 0 {
		return false, nil
	}
	named, ok, err := typeChecker.namedType(root)
	if err != nil || !ok {
		return false, err
	}
	current := named
	for _, part := range parts[1:] {
		next, err := frontendRecordFieldType(current, part)
		if err != nil {
			return false, nil
		}
		current = next
	}
	return mergeScreenParameterType(result, screen, paramIndex, named)
}

func screenParameterIndex(screen model.FrontendScreen, name string) int {
	for index, param := range screen.Parameters {
		if canonicalFieldName(param) == canonicalFieldName(name) {
			return index
		}
	}
	return -1
}

func inferScreenItemParameterTypes(item model.FrontendItem, screens map[string]model.FrontendScreen, typeChecker *frontendTypeChecker, result map[string][]frontendType) (bool, error) {
	switch item.Kind {
	case "link":
		target, ok := screens[item.Target]
		if !ok {
			return false, fmt.Errorf("link references unknown screen %q", item.Target)
		}
		if len(target.Parameters) > 0 {
			return false, fmt.Errorf("link to %s requires %d argument(s), but link does not provide any", target.Name, len(target.Parameters))
		}
	case "list":
		if item.Destination == "" {
			return false, nil
		}
		target, ok := screens[item.Destination]
		if !ok {
			return false, fmt.Errorf("list destination references unknown screen %q", item.Destination)
		}
		if len(target.Parameters) == 0 {
			return false, nil
		}
		if len(target.Parameters) != 1 {
			return false, fmt.Errorf("list destination %s expects %d arguments, but list open provides exactly 1 row", target.Name, len(target.Parameters))
		}
		argType, ok, err := typeChecker.namedType(canonicalFieldName(item.Entity))
		if err != nil {
			return false, err
		}
		if !ok {
			return false, fmt.Errorf("unknown entity %q", item.Entity)
		}
		changed, err := mergeScreenParameterType(result, target, 0, argType)
		if err != nil {
			return false, err
		}
		return changed, nil
	}
	return false, nil
}

func inferScreenParametersFromUpdate(screen model.FrontendScreen, typeChecker *frontendTypeChecker, screens map[string]model.FrontendScreen, result map[string][]frontendType) (bool, error) {
	modelType, err := typeChecker.inferInitModelType(screen)
	if err != nil {
		return false, err
	}
	env := typeChecker.baseEnv(screen)
	if screen.UpdateMessage != "" {
		env[screen.UpdateMessage] = typeChecker.screenMessageType(screen)
	}
	if screen.UpdateModel != "" {
		env[screen.UpdateModel] = modelType
	}
	return inferGoParametersFromTransition(screen.UpdateBody, env, typeChecker, screens, result)
}

func inferScreenParametersFromTransition(screen model.FrontendScreen, raw string, typeChecker *frontendTypeChecker, screens map[string]model.FrontendScreen, result map[string][]frontendType) (bool, error) {
	return inferGoParametersFromTransition(raw, typeChecker.baseEnv(screen), typeChecker, screens, result)
}

func inferGoParametersFromTransition(raw string, env frontendTypeEnv, typeChecker *frontendTypeChecker, screens map[string]model.FrontendScreen, result map[string][]frontendType) (bool, error) {
	node, err := sexp.ParseOne(raw)
	if err != nil {
		return false, err
	}
	return walkGoTransition(node, env, typeChecker, screens, result)
}

func walkGoTransition(node sexp.Node, env frontendTypeEnv, typeChecker *frontendTypeChecker, screens map[string]model.FrontendScreen, result map[string][]frontendType) (bool, error) {
	if node.Kind == sexp.KindList && len(node.Children) > 0 && node.Children[0].Kind == sexp.KindSymbol {
		switch node.Children[0].Value {
		case "if":
			left, err := walkGoTransition(node.Children[2], env, typeChecker, screens, result)
			if err != nil {
				return false, err
			}
			right, err := walkGoTransition(node.Children[3], env, typeChecker, screens, result)
			if err != nil {
				return false, err
			}
			return left || right, nil
		case "cond":
			changed := false
			for _, clause := range node.Children[1:] {
				next, err := walkGoTransition(clause.Children[1], env, typeChecker, screens, result)
				if err != nil {
					return false, err
				}
				changed = changed || next
			}
			return changed, nil
		case "match":
			_, clauses, err := typeChecker.prepareMatch(node.Children[1:], env)
			if err != nil {
				return false, err
			}
			changed := false
			for _, clause := range clauses {
				next, err := walkGoTransition(clause.Body, clause.Env, typeChecker, screens, result)
				if err != nil {
					return false, err
				}
				changed = changed || next
			}
			return changed, nil
		case "let":
			child, err := typeChecker.bindLetEnv(node.Children[1:], env, false)
			if err != nil {
				return false, err
			}
			return walkGoTransition(node.Children[2], child, typeChecker, screens, result)
		case "let*":
			child, err := typeChecker.bindLetEnv(node.Children[1:], env, true)
			if err != nil {
				return false, err
			}
			return walkGoTransition(node.Children[2], child, typeChecker, screens, result)
		case "begin":
			if len(node.Children) < 2 {
				return false, nil
			}
			return walkGoTransition(node.Children[len(node.Children)-1], env, typeChecker, screens, result)
		}
	}
	if node.Kind != sexp.KindList || len(node.Children) != 2 || node.Children[1].Kind != sexp.KindList {
		return false, nil
	}
	return walkGoEffects(node.Children[1], env, typeChecker, screens, result)
}

func walkGoEffects(node sexp.Node, env frontendTypeEnv, typeChecker *frontendTypeChecker, screens map[string]model.FrontendScreen, result map[string][]frontendType) (bool, error) {
	if node.Kind == sexp.KindList && len(node.Children) > 0 && node.Children[0].Kind == sexp.KindSymbol {
		switch node.Children[0].Value {
		case "if":
			left, err := walkGoEffects(node.Children[2], env, typeChecker, screens, result)
			if err != nil {
				return false, err
			}
			right, err := walkGoEffects(node.Children[3], env, typeChecker, screens, result)
			if err != nil {
				return false, err
			}
			return left || right, nil
		case "cond":
			changed := false
			for _, clause := range node.Children[1:] {
				next, err := walkGoEffects(clause.Children[1], env, typeChecker, screens, result)
				if err != nil {
					return false, err
				}
				changed = changed || next
			}
			return changed, nil
		case "match":
			_, clauses, err := typeChecker.prepareMatch(node.Children[1:], env)
			if err != nil {
				return false, err
			}
			changed := false
			for _, clause := range clauses {
				next, err := walkGoEffects(clause.Body, clause.Env, typeChecker, screens, result)
				if err != nil {
					return false, err
				}
				changed = changed || next
			}
			return changed, nil
		case "let":
			child, err := typeChecker.bindLetEnv(node.Children[1:], env, false)
			if err != nil {
				return false, err
			}
			return walkGoEffects(node.Children[2], child, typeChecker, screens, result)
		case "let*":
			child, err := typeChecker.bindLetEnv(node.Children[1:], env, true)
			if err != nil {
				return false, err
			}
			return walkGoEffects(node.Children[2], child, typeChecker, screens, result)
		case "begin":
			if len(node.Children) < 2 {
				return false, nil
			}
			return walkGoEffects(node.Children[len(node.Children)-1], env, typeChecker, screens, result)
		case "go":
			return inferGoCall(node, env, typeChecker, screens, result)
		case "command", "back":
			return false, nil
		}
	}
	if node.Kind != sexp.KindList {
		return false, nil
	}
	changed := false
	for _, child := range node.Children {
		next, err := walkGoEffects(child, env, typeChecker, screens, result)
		if err != nil {
			return false, err
		}
		changed = changed || next
	}
	return changed, nil
}

func inferGoCall(node sexp.Node, env frontendTypeEnv, typeChecker *frontendTypeChecker, screens map[string]model.FrontendScreen, result map[string][]frontendType) (bool, error) {
	if len(node.Children) < 2 || node.Children[1].Kind != sexp.KindSymbol {
		return false, fmt.Errorf("go expects a screen")
	}
	target := canonicalScreenName(node.Children[1].Value)
	targetScreen, ok := screens[target]
	if !ok {
		return false, fmt.Errorf("go references unknown screen %q", node.Children[1].Value)
	}
	params, ok := result[target]
	if !ok {
		return false, fmt.Errorf("go references unknown screen %q", node.Children[1].Value)
	}
	args := node.Children[2:]
	if len(args) != len(params) {
		return false, fmt.Errorf("go to %s expects %d argument(s), got %d", target, len(params), len(args))
	}
	changed := false
	for index, arg := range args {
		value, err := typeChecker.inferExprType(arg, env)
		if err != nil {
			return false, err
		}
		next, err := mergeScreenParameterType(result, targetScreen, index, value)
		if err != nil {
			return false, err
		}
		changed = changed || next
	}
	return changed, nil
}

func mergeScreenParameterType(result map[string][]frontendType, target model.FrontendScreen, index int, argType frontendType) (bool, error) {
	current := result[target.Name][index]
	if current.Kind == "" {
		result[target.Name][index] = argType
		return true, nil
	}
	merged, err := mergeCompatibleFrontendTypes(current, argType)
	if err != nil {
		parameterName := fmt.Sprintf("#%d", index+1)
		if index < len(target.Parameters) && target.Parameters[index] != "" {
			parameterName = target.Parameters[index]
		}
		return false, fmt.Errorf("screen %s parameter %s has incompatible callers: %w", target.Name, parameterName, err)
	}
	if merged.String() != current.String() {
		result[target.Name][index] = merged
		return true, nil
	}
	return false, nil
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

func collectUsedFunctionsFromItem(item model.FrontendItem, functions map[string]*model.Function, used map[string]bool) {
	if strings.TrimSpace(item.Condition) != "" {
		collectUsedFunctionsFromExpression(item.Condition, functions, used)
	}
	if strings.TrimSpace(item.Message) != "" {
		collectUsedFunctionsFromExpression(item.Message, functions, used)
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
