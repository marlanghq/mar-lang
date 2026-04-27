// Package types — screens.go adds HM type-checking for screens (init, update,
// view, embedded effects). Screens have rich structure that doesn't naturally
// fit one expression, so this file walks the existing model.FrontendScreen
// shape and type-checks each embedded expression with the appropriate env.
//
// Conventions used here:
//   - The screen's model type is whatever the init expression's first
//     element evaluates to (a record/value constructor).
//   - The screen's msg type is the nominal TUnion declared via `(msg ...)`.
//   - Commands (`(command (query args...) ok-msg fail-msg)`) require:
//       - the called name resolves to a query/action signature
//       - ok-msg and fail-msg are variants of the screen's msg union
//       - the query's return type matches the ok-msg payload
//   - Navigation: `(go screen-name args...)` — args must match the target
//     screen's parameters; `(back)` is parameterless.

package types

import (
	"fmt"
	"strings"

	"mar/internal/expr"
	"mar/internal/model"
	"mar/internal/sexp"
)

// CheckScreens runs HM-style validation on every screen in the app:
// init, update, view item embedded expressions, and command/go/back forms.
//
// Returns the first type error found. Contributes new bindings to the shared
// substitution so msg payload types can be inferred from how they're used.
func (at *AppTypes) CheckScreens(app *model.App, baseEnv *TypeEnv, subst *Subst, parseOpts func(map[string]struct{}, bool) expr.ParserOptions) error {
	if app.Screens == nil {
		return nil
	}

	screenByName := map[string]*model.FrontendScreen{}
	for i := range app.Screens.Screens {
		screenByName[app.Screens.Screens[i].Name] = &app.Screens.Screens[i]
	}

	// Use the AppTypes-wide screen param registry so cross-screen
	// constraints share the same TVars across all checks.
	screenParams := at.ScreenParamTypes

	for i := range app.Screens.Screens {
		screen := &app.Screens.Screens[i]
		if err := at.checkScreen(app, screen, baseEnv, subst, parseOpts, screenByName, screenParams); err != nil {
			return err
		}
	}
	return nil
}

func (at *AppTypes) checkScreen(
	app *model.App,
	screen *model.FrontendScreen,
	baseEnv *TypeEnv,
	subst *Subst,
	parseOpts func(map[string]struct{}, bool) expr.ParserOptions,
	screenByName map[string]*model.FrontendScreen,
	screenParams map[string][]Type,
) error {
	msgUnion := at.ScreenMsgTypes[screen.Name]

	// Build base env: builtins + screen params + named records/entities.
	env := baseEnv
	for i, p := range screen.Parameters {
		env = env.Bind(p, screenParams[screen.Name][i])
	}

	// Determine model type from init expression. If init absent, model is
	// fresh (will be unified by the update body).
	modelType := Type(FreshVar())
	if screen.InitExpression != "" {
		mt, err := at.checkScreenInit(app, screen, env, subst, msgUnion, screenByName, screenParams)
		if err != nil {
			return fmt.Errorf("screen %s init: %w", screen.Name, err)
		}
		modelType = mt
	}

	// Update body: msg + model in scope, body is `(match msg ((tag args...) (model_expr cmds)) ...)`
	if screen.UpdateBody != "" {
		updateEnv := env
		if screen.UpdateMessage != "" {
			updateEnv = updateEnv.Bind(screen.UpdateMessage, msgUnion)
		}
		if screen.UpdateModel != "" {
			updateEnv = updateEnv.Bind(screen.UpdateModel, modelType)
		}
		if err := at.checkScreenUpdate(app, screen, updateEnv, subst, msgUnion, modelType, screenByName, screenParams); err != nil {
			return fmt.Errorf("screen %s update: %w", screen.Name, err)
		}
	}

	// View body: model in scope, plus each model field as bare identifier
	// (mar-lang convention: in view items you write `submitting` not
	// `model.submitting`).
	viewEnv := env
	if screen.ViewModel != "" {
		viewEnv = viewEnv.Bind(screen.ViewModel, modelType)
	}
	resolvedModel := subst.Apply(modelType)
	if rec, ok := resolvedModel.(TRecord); ok {
		for name, t := range rec.Fields {
			viewEnv = viewEnv.Bind(name, t)
		}
	}
	if err := at.checkScreenView(app, screen, viewEnv, subst, msgUnion, screenByName, screenParams); err != nil {
		return fmt.Errorf("screen %s view: %w", screen.Name, err)
	}
	_ = parseOpts // reserved: deeper expressions may need parser opts later
	return nil
}

// checkScreenInit type-checks the init expression and returns the inferred
// model type. Init body is structural: `(model_expr (commands...))`.
func (at *AppTypes) checkScreenInit(
	app *model.App,
	screen *model.FrontendScreen,
	env *TypeEnv,
	subst *Subst,
	msgUnion TUnion,
	screenByName map[string]*model.FrontendScreen,
	screenParams map[string][]Type,
) (Type, error) {
	node, err := sexp.ParseOne(screen.InitExpression)
	if err != nil {
		return nil, err
	}
	if node.Kind != sexp.KindList || len(node.Children) != 2 {
		return nil, fmt.Errorf("init body must be (model commands), got %s", screen.InitExpression)
	}
	// Model expression — type-check as expression.
	modelType, err := at.typeCheckSexpInEnv(node.Children[0], env, subst, app)
	if err != nil {
		return nil, fmt.Errorf("init model: %w", err)
	}
	// Commands list — each must be a command/go/back form returning Cmd msg.
	if err := at.checkCommandList(node.Children[1], env, subst, msgUnion, modelType, screenByName, screenParams, app); err != nil {
		return nil, err
	}
	return modelType, nil
}

// checkScreenUpdate validates the update body: a `(match msg ...)` whose
// arms each return `(new_model commands)`.
func (at *AppTypes) checkScreenUpdate(
	app *model.App,
	screen *model.FrontendScreen,
	env *TypeEnv,
	subst *Subst,
	msgUnion TUnion,
	modelType Type,
	screenByName map[string]*model.FrontendScreen,
	screenParams map[string][]Type,
) error {
	node, err := sexp.ParseOne(screen.UpdateBody)
	if err != nil {
		return err
	}
	if node.Kind == sexp.KindList && len(node.Children) > 0 {
		head, _ := nodeSymbol(node.Children[0])
		if head == "match" {
			// (match subject (pattern body) ...)
			for i, clause := range node.Children[2:] {
				if clause.Kind != sexp.KindList || len(clause.Children) < 2 {
					continue
				}
				// pattern bindings
				clauseEnv := env
				if clause.Children[0].Kind == sexp.KindList {
					tagRaw, _ := nodeSymbol(clause.Children[0].Children[0])
					tag := canonicalFieldName(tagRaw)
					payload, ok := msgUnion.Variants[tag]
					if ok {
						for j, varNode := range clause.Children[0].Children[1:] {
							varName, _ := nodeSymbol(varNode)
							if varName == "" || varName == "_" || j >= len(payload) {
								continue
							}
							clauseEnv = clauseEnv.Bind(canonicalFieldName(varName), payload[j])
						}
					}
				}
				// body: structural (new_model commands)
				body := clause.Children[1]
				if err := at.checkUpdateArmBody(body, clauseEnv, subst, msgUnion, modelType, screenByName, screenParams, app); err != nil {
					return fmt.Errorf("update arm %d: %w", i, err)
				}
			}
			return nil
		}
	}
	// Fallback: treat whole body as expression returning (model, cmds).
	return at.checkUpdateArmBody(node, env, subst, msgUnion, modelType, screenByName, screenParams, app)
}

func (at *AppTypes) checkUpdateArmBody(
	body sexp.Node,
	env *TypeEnv,
	subst *Subst,
	msgUnion TUnion,
	modelType Type,
	screenByName map[string]*model.FrontendScreen,
	screenParams map[string][]Type,
	app *model.App,
) error {
	// Update arm bodies in mar-lang can be either:
	//   - structural tuple (new_model commands)
	//   - (if cond then else) where then/else are themselves tuples
	//   - (match subject ...) similarly
	if body.Kind == sexp.KindList && len(body.Children) > 0 {
		head, _ := nodeSymbol(body.Children[0])
		switch head {
		case "if":
			if len(body.Children) != 4 {
				return fmt.Errorf("if expects 3 args")
			}
			condT, err := at.typeCheckSexpInEnv(body.Children[1], env, subst, app)
			if err != nil {
				return fmt.Errorf("if condition: %w", err)
			}
			if err := Unify(condT, TBool(), subst); err != nil {
				return fmt.Errorf("if condition must be bool: %w", err)
			}
			if err := at.checkUpdateArmBody(body.Children[2], env, subst, msgUnion, modelType, screenByName, screenParams, app); err != nil {
				return fmt.Errorf("if then: %w", err)
			}
			return at.checkUpdateArmBody(body.Children[3], env, subst, msgUnion, modelType, screenByName, screenParams, app)
		case "match":
			// (match subject (pattern body) ...)
			if len(body.Children) < 3 {
				return fmt.Errorf("match expects subject and clauses")
			}
			for _, clause := range body.Children[2:] {
				if clause.Kind != sexp.KindList || len(clause.Children) < 2 {
					continue
				}
				if err := at.checkUpdateArmBody(clause.Children[1], env, subst, msgUnion, modelType, screenByName, screenParams, app); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if body.Kind != sexp.KindList || len(body.Children) != 2 {
		return fmt.Errorf("update arm must be (new_model commands), got %s", sexp.InlineString(body))
	}
	// model expression
	gotModel, err := at.typeCheckSexpInEnv(body.Children[0], env, subst, app)
	if err != nil {
		return fmt.Errorf("model expression: %w", err)
	}
	if err := Unify(modelType, gotModel, subst); err != nil {
		return fmt.Errorf("model expression returns %s, expected %s", subst.Apply(gotModel), subst.Apply(modelType))
	}
	// commands list
	return at.checkCommandList(body.Children[1], env, subst, msgUnion, modelType, screenByName, screenParams, app)
}

// checkCommandList expects a list of `(command ...)`, `(go ...)`, `(back)` forms.
func (at *AppTypes) checkCommandList(
	listNode sexp.Node,
	env *TypeEnv,
	subst *Subst,
	msgUnion TUnion,
	modelType Type,
	screenByName map[string]*model.FrontendScreen,
	screenParams map[string][]Type,
	app *model.App,
) error {
	if listNode.Kind != sexp.KindList {
		return fmt.Errorf("commands must be a list, got %s", sexp.InlineString(listNode))
	}
	for i, cmd := range listNode.Children {
		if err := at.checkCommand(cmd, env, subst, msgUnion, screenByName, screenParams, app); err != nil {
			return fmt.Errorf("command %d: %w", i, err)
		}
	}
	_ = modelType
	return nil
}

func (at *AppTypes) checkCommand(
	cmd sexp.Node,
	env *TypeEnv,
	subst *Subst,
	msgUnion TUnion,
	screenByName map[string]*model.FrontendScreen,
	screenParams map[string][]Type,
	app *model.App,
) error {
	if cmd.Kind != sexp.KindList || len(cmd.Children) == 0 {
		return fmt.Errorf("command must be a list, got %s", sexp.InlineString(cmd))
	}
	head, _ := nodeSymbol(cmd.Children[0])
	switch head {
	case "command":
		// (command (target args...) ok-msg fail-msg)
		if len(cmd.Children) != 4 {
			return fmt.Errorf("command expects (command (target args...) ok-msg fail-msg)")
		}
		callNode := cmd.Children[1]
		if callNode.Kind != sexp.KindList || len(callNode.Children) == 0 {
			return fmt.Errorf("command target must be a call")
		}
		targetNameRaw, _ := nodeSymbol(callNode.Children[0])
		// Names in QueryTypes / ActionTypes are canonical (underscored).
		targetName := canonicalFieldName(targetNameRaw)
		// Arg expressions
		argTypes := make([]Type, 0, len(callNode.Children)-1)
		for _, argNode := range callNode.Children[1:] {
			t, err := at.typeCheckSexpInEnv(argNode, env, subst, app)
			if err != nil {
				return fmt.Errorf("command target arg: %w", err)
			}
			argTypes = append(argTypes, t)
		}
		// Look up target as query or action
		var targetReturn Type
		if scheme, ok := at.QueryTypes[targetName]; ok {
			fn := Instantiate(scheme)
			result := FreshVar()
			expected := TArrow(argTypes, result)
			if err := Unify(fn, expected, subst); err != nil {
				return fmt.Errorf("query %s: %w", targetName, err)
			}
			targetReturn = result
		} else if scheme, ok := at.ActionTypes[targetName]; ok {
			fn := Instantiate(scheme)
			result := FreshVar()
			expected := TArrow(argTypes, result)
			if err := Unify(fn, expected, subst); err != nil {
				return fmt.Errorf("action %s: %w", targetName, err)
			}
			targetReturn = result
		} else {
			return fmt.Errorf("unknown command target %q", targetName)
		}
		// ok-msg: variant of the screen's msg union. Payload can be either
		// the target's return type or empty (fire-and-forget signal).
		okRaw, _ := nodeSymbol(cmd.Children[2])
		okName := canonicalFieldName(okRaw)
		if err := at.checkMsgRefFlexible(okName, targetReturn, msgUnion, subst); err != nil {
			return fmt.Errorf("ok-msg %s: %w", okRaw, err)
		}
		// fail-msg: variant of msg union. Payload can be string (error message)
		// or empty.
		failRaw, _ := nodeSymbol(cmd.Children[3])
		failName := canonicalFieldName(failRaw)
		if err := at.checkMsgRefFlexible(failName, TString(), msgUnion, subst); err != nil {
			return fmt.Errorf("fail-msg %s: %w", failRaw, err)
		}
		return nil

	case "go":
		// (go screen-name args...)
		if len(cmd.Children) < 2 {
			return fmt.Errorf("go expects (go screen args...)")
		}
		targetName, _ := nodeSymbol(cmd.Children[1])
		canonical := canonicalTypeName(targetName)
		target, ok := screenByName[canonical]
		if !ok {
			target, ok = screenByName[targetName]
		}
		if !ok {
			return fmt.Errorf("go target screen %q not found", targetName)
		}
		params := screenParams[target.Name]
		if len(cmd.Children)-2 != len(params) {
			return fmt.Errorf("go %s expects %d arguments, got %d", target.Name, len(params), len(cmd.Children)-2)
		}
		for i, argNode := range cmd.Children[2:] {
			gotT, err := at.typeCheckSexpInEnv(argNode, env, subst, app)
			if err != nil {
				return fmt.Errorf("go %s arg %d: %w", target.Name, i, err)
			}
			if err := Unify(params[i], gotT, subst); err != nil {
				return fmt.Errorf("go %s arg %d: %w", target.Name, i, err)
			}
		}
		return nil

	case "back":
		if len(cmd.Children) != 1 {
			return fmt.Errorf("back expects no arguments")
		}
		return nil
	}
	return fmt.Errorf("unknown command form %q", head)
}

// checkMsgRef verifies that a msg name is a variant of msgUnion and that the
// supplied payload types unify with the variant's declared payload.
func (at *AppTypes) checkMsgRef(name string, gotPayload []Type, msgUnion TUnion, subst *Subst) error {
	if name == "" {
		return fmt.Errorf("missing msg name")
	}
	declared, ok := msgUnion.Variants[name]
	if !ok {
		return fmt.Errorf("not a variant of %s", msgUnion.Name)
	}
	if len(declared) != len(gotPayload) {
		return fmt.Errorf("payload arity mismatch: variant has %d, got %d", len(declared), len(gotPayload))
	}
	for i := range declared {
		if err := Unify(declared[i], gotPayload[i], subst); err != nil {
			return fmt.Errorf("payload %d: %w", i, err)
		}
	}
	return nil
}

// checkMsgRefFlexible accepts either a msg variant with the given payload
// type or a no-payload variant (fire-and-forget). Used for ok-msg / fail-msg
// references in command callbacks where the screen may discard the result.
func (at *AppTypes) checkMsgRefFlexible(name string, expectedPayload Type, msgUnion TUnion, subst *Subst) error {
	if name == "" {
		return fmt.Errorf("missing msg name")
	}
	declared, ok := msgUnion.Variants[name]
	if !ok {
		return fmt.Errorf("not a variant of %s", msgUnion.Name)
	}
	switch len(declared) {
	case 0:
		return nil // fire-and-forget OK
	case 1:
		if err := Unify(declared[0], expectedPayload, subst); err != nil {
			return fmt.Errorf("payload: %w", err)
		}
		return nil
	}
	return fmt.Errorf("variant has %d payload values, expected 0 or 1", len(declared))
}

// checkScreenView walks view items and validates embedded expressions
// (filters, conditions, action values, form filters, title expressions).
func (at *AppTypes) checkScreenView(
	app *model.App,
	screen *model.FrontendScreen,
	env *TypeEnv,
	subst *Subst,
	msgUnion TUnion,
	screenByName map[string]*model.FrontendScreen,
	screenParams map[string][]Type,
) error {
	if screen.TitleExpression != "" {
		_, err := at.typeCheckSexpStringInEnv(screen.TitleExpression, env, subst, app)
		if err != nil {
			return fmt.Errorf("title expression: %w", err)
		}
	}
	for _, section := range screen.Sections {
		for _, item := range section.Items {
			if err := at.checkViewItem(item, env, subst, msgUnion, screenByName, screenParams, app); err != nil {
				return fmt.Errorf("section item %s: %w", item.Kind, err)
			}
		}
	}
	for _, ti := range screen.ToolbarItems {
		if err := at.checkViewItem(ti.Item, env, subst, msgUnion, screenByName, screenParams, app); err != nil {
			return fmt.Errorf("toolbar item %s: %w", ti.Item.Kind, err)
		}
	}
	return nil
}

func (at *AppTypes) checkViewItem(
	item model.FrontendItem,
	env *TypeEnv,
	subst *Subst,
	msgUnion TUnion,
	screenByName map[string]*model.FrontendScreen,
	screenParams map[string][]Type,
	app *model.App,
) error {
	// Cross-screen link: a list/children item with a destination passes the
	// iterated entity row to that destination's first parameter. Unify the
	// entity record with the target screen's first param type.
	if (item.Kind == "list" || item.Kind == "children") && item.Destination != "" && item.Entity != "" {
		entityName := canonicalTypeName(item.Entity)
		entityType, ok := at.Entities[entityName]
		if !ok {
			entityType, ok = at.Entities[item.Entity]
		}
		if ok {
			target := canonicalTypeName(item.Destination)
			params, found := screenParams[target]
			if !found {
				params, found = screenParams[item.Destination]
			}
			if found && len(params) > 0 {
				if err := Unify(params[0], entityType, subst); err != nil {
					return fmt.Errorf("list destination %s: %w", item.Destination, err)
				}
			}
		}
	}
	if item.Condition != "" {
		t, err := at.typeCheckSexpStringInEnv(item.Condition, env, subst, app)
		if err != nil {
			return fmt.Errorf("condition: %w", err)
		}
		if err := Unify(t, TBool(), subst); err != nil {
			return fmt.Errorf("condition must be bool, got %s", subst.Apply(t))
		}
	}
	if item.Filter != "" {
		t, err := at.typeCheckSexpStringInEnv(item.Filter, env, subst, app)
		if err != nil {
			return fmt.Errorf("filter: %w", err)
		}
		if err := Unify(t, TBool(), subst); err != nil {
			return fmt.Errorf("filter must be bool, got %s", subst.Apply(t))
		}
	}
	if item.Disabled != "" {
		t, err := at.typeCheckSexpStringInEnv(item.Disabled, env, subst, app)
		if err != nil {
			return fmt.Errorf("disabled: %w", err)
		}
		if err := Unify(t, TBool(), subst); err != nil {
			return fmt.Errorf("disabled must be bool, got %s", subst.Apply(t))
		}
	}
	for _, formField := range item.FormFields {
		if formField.Filter != "" {
			t, err := at.typeCheckSexpStringInEnv(formField.Filter, env, subst, app)
			if err != nil {
				return fmt.Errorf("form-field %s filter: %w", formField.Field, err)
			}
			if err := Unify(t, TBool(), subst); err != nil {
				return fmt.Errorf("form-field %s filter must be bool, got %s", formField.Field, subst.Apply(t))
			}
		}
	}
	for _, value := range item.Values {
		// Value expression is assigned to the entity field; we don't currently
		// know which entity at this layer, so skip type-checking against
		// expected here. The action step pass already handles action values.
		_, err := at.typeCheckSexpStringInEnv(value.Expression, env, subst, app)
		if err != nil {
			return fmt.Errorf("value %s: %w", value.Field, err)
		}
	}
	// Text item content: type-check unless it's a bare quoted string literal.
	// Examples to type-check: `p.bogus`, `(string-append "x" y)`.
	// Skip: `"hello world"`.
	if item.Kind == "text" && item.Text != "" && !isBareStringLiteral(item.Text) {
		if _, err := at.typeCheckSexpStringInEnv(item.Text, env, subst, app); err != nil {
			return fmt.Errorf("text content: %w", err)
		}
	}
	_ = msgUnion
	_ = screenByName
	_ = screenParams
	return nil
}

// isBareStringLiteral reports whether raw looks like a single string literal
// (e.g. `"hello"`) with no surrounding expression context. Used to skip
// type-checking pure text content.
func isBareStringLiteral(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) < 2 {
		return false
	}
	if trimmed[0] != '"' || trimmed[len(trimmed)-1] != '"' {
		return false
	}
	// Make sure there's no unescaped quote in the middle that would mean
	// multiple literals or a more complex form.
	inner := trimmed[1 : len(trimmed)-1]
	escaped := false
	for _, c := range inner {
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			return false
		}
	}
	return true
}

// typeCheckSexpInEnv parses a sexp.Node as a mar-lang expression and runs HM
// inference under env. Returns the inferred type.
func (at *AppTypes) typeCheckSexpInEnv(node sexp.Node, env *TypeEnv, subst *Subst, app *model.App) (Type, error) {
	return at.typeCheckSexpStringInEnv(sexp.InlineString(node), env, subst, app)
}

func (at *AppTypes) typeCheckSexpStringInEnv(raw string, env *TypeEnv, subst *Subst, app *model.App) (Type, error) {
	// Exclude type/record/union/function names from allowedVars: those are
	// bound in env so the inferer can find them, but the expression parser
	// treats them via dedicated AllowedRecords/AllowedVariants/AllowedFunctions
	// so they shouldn't be in AllowedVariables (else the parser tries to
	// treat them as first-class callables).
	skip := map[string]struct{}{}
	for name := range at.Records {
		skip[name] = struct{}{}
	}
	for name := range at.Entities {
		skip[name] = struct{}{}
	}
	for name := range at.Unions {
		skip[name] = struct{}{}
	}
	for name := range at.Aliases {
		skip[name] = struct{}{}
	}
	for name := range at.FunctionTypes {
		skip[name] = struct{}{}
	}
	for name := range at.QueryTypes {
		skip[name] = struct{}{}
	}
	for name := range at.ActionTypes {
		skip[name] = struct{}{}
	}

	allowedVars := map[string]struct{}{}
	for e := env; e != nil; e = e.parent {
		for name := range e.bindings {
			if _, isType := skip[name]; isType {
				continue
			}
			allowedVars[name] = struct{}{}
		}
	}
	allowedVars = expr.AllowedVariablesWithBuiltins(allowedVars)

	functionArities := map[string]int{}
	for _, fn := range app.Functions {
		functionArities[fn.Name] = len(fn.Parameters)
	}
	recordFields := map[string][]string{}
	for _, r := range app.Records {
		fs := make([]string, 0, len(r.Fields))
		for _, f := range r.Fields {
			fs = append(fs, f.Name)
		}
		recordFields[r.Name] = fs
	}
	variantArities := map[string]int{}
	for _, typ := range app.Types {
		for _, v := range typ.Variants {
			variantArities[v.Name] = len(v.Fields)
		}
	}

	parsed, err := expr.Parse(raw, expr.ParserOptions{
		AllowedVariables: allowedVars,
		AllowedFunctions: functionArities,
		AllowedRecords:   recordFields,
		AllowedVariants:  variantArities,
	})
	if err != nil {
		return nil, err
	}
	return Infer(parsed, env, subst)
}

func nodeSymbol(node sexp.Node) (string, bool) {
	if node.Kind != sexp.KindSymbol {
		return "", false
	}
	return node.Value, true
}
