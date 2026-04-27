package types

// check.go bridges mar-lang's `model` package to the HM type system. It
// translates entities, records, types and aliases into the HM Type AST, and
// builds environments suitable for type-checking each context (entity
// validate, entity authorize, query where, action step, function body).
//
// This is the integration point used by runtime/compile.go (and elsewhere) to
// run the HM checker on a parsed mar-lang program.

import (
	"fmt"
	"strings"
	"unicode"

	"mar/internal/expr"
	"mar/internal/model"
	"mar/internal/parser"
	"mar/internal/sexp"
)

func init() {
	// Wire HM into parser.Parse so that type errors are reported at parse
	// time. The parser package can't import types directly (test file imports
	// parser), so we register from this side.
	parser.RegisterHMCheck(CheckApp)
}

// queryDisplayName converts a stored canonical query name (e.g. "todos_by_title")
// back to the dashed form used in user-facing error messages ("todos-by-title").
func queryDisplayName(name string) string {
	return strings.ReplaceAll(name, "_", "-")
}

// atLine returns " at line N" for non-zero N, or "" otherwise. Used to enrich
// error messages with source location when the model captured a line number.
func atLine(lineNo int) string {
	if lineNo <= 0 {
		return ""
	}
	return fmt.Sprintf(" (line %d)", lineNo)
}

// canonicalFieldName mirrors parser.canonicalFieldName: replaces "-" with "_".
// Used to look up canonical query/action/function names from raw source.
func canonicalFieldName(value string) string {
	return strings.ReplaceAll(value, "-", "_")
}

// canonicalTypeName mirrors parser.canonicalTypeName: it turns "post" into
// "Post" and "vet-visit" into "VetVisit". Kept inline (not imported) to avoid
// circular deps between types and parser packages.
func canonicalTypeName(symbol string) string {
	parts := strings.FieldsFunc(symbol, func(r rune) bool {
		return r == '-' || r == '_'
	})
	var b strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(part)
		b.WriteRune(unicode.ToUpper(runes[0]))
		if len(runes) > 1 {
			b.WriteString(string(runes[1:]))
		}
	}
	return b.String()
}

// AppTypes is the set of named types declared in an App: entity records,
// user-defined records, enum/tagged unions, type aliases, plus placeholder
// types for user-defined functions, query and action signatures.
//
// Computed once and shared across all check functions; placeholders are
// mutated as inference progresses (resolved through the shared Subst).
type AppTypes struct {
	Entities      map[string]TRecord // by entity name
	Records       map[string]TRecord // by record name
	Unions        map[string]TUnion  // by enum/type name
	Aliases       map[string]TRecord // by alias name (treated like records)
	FunctionTypes map[string]Type     // by function name; arrow with placeholder vars
	FunctionParams map[string][]string // by function name; ordered parameter names
	QueryTypes    map[string]Type     // by query name; arrow params -> list entity
	ActionTypes   map[string]Type     // by action name; arrow input -> result

	// ScreenParamTypes holds per-screen ordered param TVar placeholders.
	// Cross-screen calls (go / list-destination) unify against these so that
	// (go profile-detail user) constrains profile-detail's user param.
	ScreenParamTypes map[string][]Type // by screen name

	// ScreenMsgTypes holds the nominal sum type of messages for each screen.
	// Built from `(msg ...)` declarations. Variant payload types start as
	// fresh vars and are resolved as the screen's update/init/command bodies
	// constrain them.
	ScreenMsgTypes map[string]TUnion // by screen name
}

// BuildAppTypes constructs an AppTypes from an *model.App. Resolves all
// declared types into their HM representation. Does not yet check any
// expressions.
func BuildAppTypes(app *model.App) (*AppTypes, error) {
	if app == nil {
		return nil, fmt.Errorf("BuildAppTypes: nil app")
	}
	at := &AppTypes{
		Entities:       map[string]TRecord{},
		Records:        map[string]TRecord{},
		Unions:         map[string]TUnion{},
		Aliases:        map[string]TRecord{},
		FunctionTypes:    map[string]Type{},
		FunctionParams:   map[string][]string{},
		QueryTypes:       map[string]Type{},
		ActionTypes:      map[string]Type{},
		ScreenMsgTypes:   map[string]TUnion{},
		ScreenParamTypes: map[string][]Type{},
	}

	// Forward-declare all named types so cross-references resolve regardless
	// of declaration order: entities, unions, records.
	for i := range app.Entities {
		entity := &app.Entities[i]
		at.Entities[entity.Name] = TRecord{Name: entity.Name}
	}
	for _, typ := range app.Types {
		at.Unions[typ.Name] = TUnion{Name: typ.Name}
	}
	for _, r := range app.Records {
		at.Records[r.Name] = TRecord{Name: r.Name}
	}

	// Fill entity fields first (relations only reference primary keys).
	for i := range app.Entities {
		entity := &app.Entities[i]
		fields := map[string]Type{}
		order := make([]string, 0, len(entity.Fields))
		for _, f := range entity.Fields {
			ft, err := at.EntityFieldType(app, &f)
			if err != nil {
				return nil, fmt.Errorf("entity %s field %s: %w", entity.Name, f.Name, err)
			}
			fields[f.Name] = ft
			order = append(order, f.Name)
		}
		at.Entities[entity.Name] = TRecord{Name: entity.Name, Fields: fields, Order: order}
	}

	// Fill tagged unions (variant payloads can reference entities, records,
	// or other unions thanks to the forward declarations above).
	for _, typ := range app.Types {
		variants := map[string][]Type{}
		fieldNames := map[string][]string{}
		order := make([]string, 0, len(typ.Variants))
		if len(typ.Values) > 0 {
			for _, v := range typ.Values {
				variants[v] = nil
				order = append(order, v)
			}
		}
		for _, v := range typ.Variants {
			payload := make([]Type, 0, len(v.Fields))
			names := make([]string, 0, len(v.Fields))
			for _, f := range v.Fields {
				ft, err := at.parseFieldType(f.Type)
				if err != nil {
					return nil, fmt.Errorf("type %s variant %s: %w", typ.Name, v.Name, err)
				}
				payload = append(payload, ft)
				names = append(names, f.Name)
			}
			variants[v.Name] = payload
			fieldNames[v.Name] = names
			order = append(order, v.Name)
		}
		at.Unions[typ.Name] = TUnion{
			Name:         typ.Name,
			Variants:     variants,
			VariantOrder: order,
			FieldNames:   fieldNames,
		}
	}

	// User function placeholder types — must be created before checks run so
	// recursive and mutually-recursive references resolve to the same TVars.
	for _, fn := range app.Functions {
		paramTypes := make([]Type, len(fn.Parameters))
		for i := range fn.Parameters {
			paramTypes[i] = FreshVar()
		}
		ret := FreshVar()
		at.FunctionTypes[fn.Name] = TArrow(paramTypes, ret)
		// Store names for nicer error messages ("function X parameter <name>").
		params := make([]string, len(fn.Parameters))
		copy(params, fn.Parameters)
		at.FunctionParams[fn.Name] = params
		registerFunctionParamNames(fn.Name, params)
	}

	// Query signatures — each query becomes a function from its declared
	// parameters to a list of the entity it reads. Parameter types are fresh
	// vars at first; CheckApp's pass over query-where clauses will resolve
	// them via the shared Subst.
	for _, q := range app.Queries {
		entityRec, ok := at.Entities[q.Entity]
		if !ok {
			// Try canonical name in case storage differs.
			entityRec, ok = at.Entities[canonicalTypeName(q.Entity)]
		}
		if !ok {
			// Unknown entity — skip; CheckApp will report the error elsewhere.
			continue
		}
		paramTypes := make([]Type, len(q.Parameters))
		for i := range q.Parameters {
			paramTypes[i] = FreshVar()
		}
		at.QueryTypes[q.Name] = TArrow(paramTypes, TList(entityRec))
	}

	// Action signatures — each action takes the input alias as a record and
	// returns result with a polymorphic ok type. The parameter list mirrors
	// the alias fields in declaration order (acts as a flattened call).
	for _, action := range app.Actions {
		alias := FindAlias(app, action.InputAlias)
		if alias == nil {
			continue
		}
		paramTypes := make([]Type, 0, len(alias.Fields))
		for _, f := range alias.Fields {
			t, err := AliasFieldType(app, &f)
			if err != nil {
				continue // best-effort
			}
			paramTypes = append(paramTypes, t)
		}
		// Result type: polymorphic for now. Frente F3 (Cmd Msg) refines.
		at.ActionTypes[action.Name] = TArrow(paramTypes, TResult(FreshVar(), TUnit()))
	}

	// Screen parameters: fresh TVar placeholders. Cross-screen call sites
	// (go, list destination) unify their argument types against these.
	if app.Screens != nil {
		for _, screen := range app.Screens.Screens {
			params := make([]Type, len(screen.Parameters))
			for i := range screen.Parameters {
				params[i] = FreshVar()
			}
			at.ScreenParamTypes[screen.Name] = params
		}
	}

	// Screen msg sum types (Frente F4). Each screen's `(msg ...)` declarations
	// become a nominal TUnion. Variant payload types are fresh vars resolved
	// later by uses in update bodies and command callbacks.
	if app.Screens != nil {
		for _, screen := range app.Screens.Screens {
			variants := map[string][]Type{}
			variantOrder := make([]string, 0, len(screen.Messages))
			fieldNames := map[string][]string{}
			for _, msg := range screen.Messages {
				payload := make([]Type, len(msg.Parameters))
				names := make([]string, len(msg.Parameters))
				for i, p := range msg.Parameters {
					payload[i] = FreshVar()
					names[i] = p
				}
				variants[msg.Name] = payload
				variantOrder = append(variantOrder, msg.Name)
				fieldNames[msg.Name] = names
			}
			at.ScreenMsgTypes[screen.Name] = TUnion{
				Name:         screen.Name + "Msg",
				Variants:     variants,
				VariantOrder: variantOrder,
				FieldNames:   fieldNames,
			}
		}
	}

	// Fill user-defined records (may reference entities, unions, other records).
	for _, r := range app.Records {
		fields := map[string]Type{}
		order := make([]string, 0, len(r.Fields))
		for _, f := range r.Fields {
			ft, err := at.parseFieldType(f.Type)
			if err != nil {
				return nil, fmt.Errorf("record %s field %s: %w", r.Name, f.Name, err)
			}
			fields[f.Name] = ft
			order = append(order, f.Name)
		}
		at.Records[r.Name] = TRecord{Name: r.Name, Fields: fields, Order: order}
	}

	// Input aliases — used by actions.
	for _, alias := range app.InputAliases {
		fields := map[string]Type{}
		order := make([]string, 0, len(alias.Fields))
		for _, f := range alias.Fields {
			ft, err := AliasFieldType(app, &f)
			if err != nil {
				return nil, fmt.Errorf("alias %s field %s: %w", alias.Name, f.Name, err)
			}
			fields[f.Name] = ft
			order = append(order, f.Name)
		}
		at.Aliases[alias.Name] = TRecord{Name: alias.Name, Fields: fields, Order: order}
	}

	return at, nil
}

// BaseEnvWithApp builds a TypeEnv that includes BaseEnv builtins plus all
// app-level named types (entities, records, unions, aliases) and user
// function signatures with fresh placeholder vars (later replaced by
// actually-inferred types).
func (at *AppTypes) BaseEnvWithApp(app *model.App) *TypeEnv {
	env := BaseEnv()

	// Records and entities are types, not values — but in mar-lang they are
	// also constructors (the parser turns `(post body)` into RecordConstructor).
	// Bind their record type so the inferer can find it under the same name.
	for name, rec := range at.Records {
		env = env.Bind(name, rec)
	}
	for name, rec := range at.Entities {
		env = env.Bind(name, rec)
	}
	for name, alias := range at.Aliases {
		env = env.Bind(name, alias)
	}

	// Tagged unions: bind by name. Tag-based lookup is handled by the inferer
	// walking the env (see findTaggedUnion in infer.go).
	for name, u := range at.Unions {
		env = env.Bind(name, u)
	}

	// User functions: bind the cached placeholder types from at.FunctionTypes
	// (created once in BuildAppTypes). Once function inference resolves the
	// vars in a shared Subst, every later lookup sees the resolved types via
	// Subst.Apply.
	for name, t := range at.FunctionTypes {
		env = env.Bind(name, t)
	}

	// Queries and actions are first-class typed services in the env. Frente
	// F3 will use these to type-check `(command (query-name args...) ...)`
	// and `(command (action-name args...) ...)` calls.
	for name, t := range at.QueryTypes {
		env = env.Bind(name, t)
	}
	for name, t := range at.ActionTypes {
		env = env.Bind(name, t)
	}

	return env
}

// CheckApp runs the HM checker against every supported expression context in
// an app: entity validate, entity authorize, query where, action step values,
// and user function bodies. Returns the first error encountered, or nil if
// every checked expression is well-typed.
//
// Uses a single shared Subst so that constraints flowing from one context
// (e.g. function body inference) inform later contexts (e.g. entity validate
// calling that function).
//
// Scope intentionally excludes screen init/update/view: those require Cmd/View
// types planned for later frentes.
func CheckApp(app *model.App) error {
	if err := CheckTermination(app); err != nil {
		return err
	}
	at, err := BuildAppTypes(app)
	if err != nil {
		return fmt.Errorf("HM build types: %w", err)
	}

	parseOpts := makeParseOpts(app)
	subst := NewSubst()
	baseEnv := at.BaseEnvWithApp(app)

	// 1. User functions first — populates subst with concrete types for each
	// function placeholder so subsequent contexts (entity validate, queries,
	// etc.) see real types when they call user functions.
	for i := range app.Functions {
		fn := &app.Functions[i]
		fnVars := map[string]struct{}{}
		for _, p := range fn.Parameters {
			fnVars[p] = struct{}{}
		}
		parsed, err := expr.Parse(fn.Expression, parseOpts(fnVars, true))
		if err != nil {
			return fmt.Errorf("function %s%s: %w", fn.Name, atLine(fn.LineNo), err)
		}
		if err := at.checkFunctionInSharedEnv(parsed, fn, baseEnv, subst); err != nil {
			return fmt.Errorf("%w%s", err, atLine(fn.LineNo))
		}
	}

	// 2. Entities — validates and authorizations.
	for i := range app.Entities {
		entity := &app.Entities[i]
		entityVars := map[string]struct{}{}
		for _, f := range entity.Fields {
			entityVars[f.Name] = struct{}{}
		}
		for _, typ := range app.Types {
			for _, v := range typ.Values {
				entityVars[v] = struct{}{}
			}
			for _, v := range typ.Variants {
				entityVars[v.Name] = struct{}{}
			}
		}

		entityEnv := baseEnv
		for name, t := range at.Entities[entity.Name].Fields {
			entityEnv = entityEnv.Bind(name, t)
		}

		if entity.Validate != "" {
			parsed, err := expr.Parse(entity.Validate, parseOpts(entityVars, false))
			if err != nil {
				return fmt.Errorf("entity %s validate: %w", entity.Name, err)
			}
			got, err := Infer(parsed, entityEnv, subst)
			if err != nil {
				return fmt.Errorf("entity %s validate: %w", entity.Name, err)
			}
			if err := Unify(got, TBool(), subst); err != nil {
				return fmt.Errorf("entity %s validate: expression must return bool, got %s", entity.Name, PrettyType(subst.Apply(got)))
			}
		}

		for _, auth := range entity.Authorizations {
			parsed, err := expr.Parse(auth.Expression, parseOpts(entityVars, true))
			if err != nil {
				return fmt.Errorf("entity %s authorize %s%s: %w", entity.Name, auth.Action, atLine(auth.LineNo), err)
			}
			got, err := Infer(parsed, entityEnv, subst)
			if err != nil {
				return fmt.Errorf("entity %s authorize %s%s: %w", entity.Name, auth.Action, atLine(auth.LineNo), err)
			}
			if err := Unify(got, TBool(), subst); err != nil {
				return fmt.Errorf("entity %s authorize %s%s: expression must return bool, got %s", entity.Name, auth.Action, atLine(auth.LineNo), PrettyType(subst.Apply(got)))
			}
		}
	}

	// 3. Queries. Also populates app.Queries[i].ParameterTypes so the runtime
	// can decode HTTP query string params correctly.
	for i := range app.Queries {
		query := &app.Queries[i]
		if query.Where == "" {
			if len(query.Parameters) > 0 {
				return fmt.Errorf("query %s parameter %s: type could not be inferred", query.Name, query.Parameters[0])
			}
			continue
		}
		entity := FindEntityByName(app, query.Entity)
		if entity == nil {
			continue
		}
		queryVars := map[string]struct{}{}
		for _, f := range entity.Fields {
			queryVars[f.Name] = struct{}{}
		}
		for _, p := range query.Parameters {
			queryVars[p] = struct{}{}
		}
		parsed, err := expr.Parse(query.Where, parseOpts(queryVars, true))
		if err != nil {
			return fmt.Errorf("query %s where: %w", query.Name, err)
		}
		queryEnv := baseEnv
		for name, t := range at.Entities[entity.Name].Fields {
			queryEnv = queryEnv.Bind(name, t)
		}
		paramVars := map[string]TVar{}
		for _, p := range query.Parameters {
			v := FreshVar()
			paramVars[p] = v
			queryEnv = queryEnv.Bind(p, v)
		}
		got, err := Infer(parsed, queryEnv, subst)
		if err != nil {
			return fmt.Errorf("query %s where: %w", queryDisplayName(query.Name), err)
		}
		if err := Unify(got, TBool(), subst); err != nil {
			return fmt.Errorf("query %s where: expression must return bool, got %s", queryDisplayName(query.Name), subst.Apply(got))
		}
		// Populate ParameterTypes from inferred vars.
		if query.ParameterTypes == nil {
			query.ParameterTypes = map[string]string{}
		}
		for _, p := range query.Parameters {
			v := paramVars[p]
			resolved := subst.Apply(v)
			marType, ok := primitiveTypeName(resolved)
			if !ok {
				return fmt.Errorf("query %s parameter %s: type could not be inferred", queryDisplayName(query.Name), p)
			}
			query.ParameterTypes[p] = marType
		}
	}

	// 5. Screens (init / update / view + command/go/back cross-references).
	if err := at.CheckScreens(app, baseEnv, subst, parseOpts); err != nil {
		return err
	}

	// 4. Actions.
	for i := range app.Actions {
		action := &app.Actions[i]
		actionEnv, err := at.actionEnvOnto(baseEnv, action, app)
		if err != nil {
			return fmt.Errorf("HM action env %s: %w", action.Name, err)
		}
		actionVars := map[string]struct{}{}
		for env := actionEnv; env != nil; env = env.parent {
			for name := range env.bindings {
				actionVars[name] = struct{}{}
			}
		}
		stepEnv := actionEnv
		for _, step := range action.Steps {
			entity := FindEntityByName(app, step.Entity)
			if entity == nil {
				return fmt.Errorf("HM action %s step: unknown entity %s", action.Name, step.Entity)
			}
			if step.Alias != "" {
				stepEnv = stepEnv.Bind(step.Alias, at.Entities[entity.Name])
				actionVars[step.Alias] = struct{}{}
			}
			for _, value := range step.Values {
				field := findField(entity, value.Field)
				if field == nil {
					return fmt.Errorf("HM action %s: entity %s has no field %s", action.Name, entity.Name, value.Field)
				}
				expected, err := at.EntityFieldType(app, field)
				if err != nil {
					return fmt.Errorf("HM action %s field %s: %w", action.Name, value.Field, err)
				}
				parsed, err := expr.Parse(value.Expression, parseOpts(actionVars, true))
				if err != nil {
					return fmt.Errorf("action %s step %s %s field %s: %w", action.Name, step.Kind, entity.Name, value.Field, err)
				}
				got, err := Infer(parsed, stepEnv, subst)
				if err != nil {
					return fmt.Errorf("action %s step %s %s field %s: %w", action.Name, step.Kind, entity.Name, value.Field, err)
				}
				// Try direct, then optional unwrap.
				if err := Unify(expected, got, subst); err != nil {
					if con, ok := expected.(TCon); ok && con.Name == "maybe" && len(con.Args) == 1 {
						if err2 := Unify(con.Args[0], got, subst); err2 == nil {
							continue
						}
					}
					return fmt.Errorf("action %s step %s %s field %s: expects %s, got %s", action.Name, step.Kind, entity.Name, value.Field, subst.Apply(expected), subst.Apply(got))
				}
			}
		}
	}

	return nil
}

// makeParseOpts returns a parseOpts factory bound to the app's record /
// function / variant tables.
func makeParseOpts(app *model.App) func(map[string]struct{}, bool) expr.ParserOptions {
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
	return func(extraVars map[string]struct{}, withBuiltins bool) expr.ParserOptions {
		vars := extraVars
		if withBuiltins {
			vars = expr.AllowedVariablesWithBuiltins(extraVars)
		}
		return expr.ParserOptions{
			AllowedVariables: vars,
			AllowedFunctions: functionArities,
			AllowedRecords:   recordFields,
			AllowedVariants:  variantArities,
		}
	}
}

// actionEnvOnto extends a base env with the action's input fields and an
// `input` record bundling them.
func (at *AppTypes) actionEnvOnto(base *TypeEnv, action *model.Action, app *model.App) (*TypeEnv, error) {
	env := base
	alias := FindAlias(app, action.InputAlias)
	if alias == nil {
		return nil, fmt.Errorf("action %s: missing input alias %s", action.Name, action.InputAlias)
	}
	inputFields := map[string]Type{}
	for _, f := range alias.Fields {
		t, err := AliasFieldType(app, &f)
		if err != nil {
			return nil, err
		}
		env = env.Bind(f.Name, t)
		inputFields[f.Name] = t
	}
	order := make([]string, 0, len(alias.Fields))
	for _, f := range alias.Fields {
		order = append(order, f.Name)
	}
	env = env.Bind("input", TRecord{Name: alias.Name, Fields: inputFields, Order: order})
	return env, nil
}

func findField(entity *model.Entity, name string) *model.Field {
	for i := range entity.Fields {
		if entity.Fields[i].Name == name {
			return &entity.Fields[i]
		}
	}
	return nil
}

// CheckEntityValidate runs HM on an entity validate expression and verifies
// it returns bool. Returns nil on success.
func (at *AppTypes) CheckEntityValidate(parsed expr.Expr, entity *model.Entity, app *model.App) error {
	env := at.BaseEnvWithApp(app)
	for name, t := range at.Entities[entity.Name].Fields {
		env = env.Bind(name, t)
	}
	got, err := InferExpr(parsed, env)
	if err != nil {
		return fmt.Errorf("entity %s validate: %w", entity.Name, err)
	}
	if !typesEqual(got, TBool()) {
		return fmt.Errorf("entity %s validate: must return bool, got %s", entity.Name, got)
	}
	return nil
}

// CheckEntityAuthorize is like CheckEntityValidate but also exposes
// current_user via BaseEnv.
func (at *AppTypes) CheckEntityAuthorize(parsed expr.Expr, entity *model.Entity, action string, app *model.App) error {
	env := at.BaseEnvWithApp(app)
	for name, t := range at.Entities[entity.Name].Fields {
		env = env.Bind(name, t)
	}
	got, err := InferExpr(parsed, env)
	if err != nil {
		return fmt.Errorf("entity %s authorize %s: %w", entity.Name, action, err)
	}
	if !typesEqual(got, TBool()) {
		return fmt.Errorf("entity %s authorize %s: must return bool, got %s", entity.Name, action, got)
	}
	return nil
}

// CheckQueryWhere runs HM on a query's where clause. Query parameters are
// added to the env with fresh placeholder vars; the inferred type for each
// param is then derived from how it's used. Returns the inferred parameter
// types (so callers can serialize them onto model.Query.ParameterTypes).
func (at *AppTypes) CheckQueryWhere(parsed expr.Expr, query *model.Query, app *model.App) (map[string]Type, error) {
	entity := FindEntityByName(app, query.Entity)
	if entity == nil {
		return nil, fmt.Errorf("query %s: unknown entity %s", query.Name, query.Entity)
	}
	env := at.BaseEnvWithApp(app)
	for name, t := range at.Entities[entity.Name].Fields {
		env = env.Bind(name, t)
	}
	paramVars := map[string]TVar{}
	for _, p := range query.Parameters {
		v := FreshVar()
		paramVars[p] = v
		env = env.Bind(p, v)
	}
	got, err := InferExpr(parsed, env)
	if err != nil {
		return nil, fmt.Errorf("query %s where: %w", query.Name, err)
	}
	if !typesEqual(got, TBool()) {
		return nil, fmt.Errorf("query %s where: must return bool, got %s", query.Name, got)
	}
	out := map[string]Type{}
	for name, v := range paramVars {
		// Re-resolve via a fresh subst — but InferExpr already applied. Pull
		// the resolved type out of the env via lookup.
		if resolved, ok := env.Lookup(name); ok {
			_ = resolved
		}
		out[name] = v
	}
	return out, nil
}

// CheckActionStep runs HM on an action step value expression. Each step
// assigns a value to an entity field; the inferred value type must match.
//
// Allows assigning an inner type to an optional (maybe) field — the runtime
// auto-wraps. This mirrors how the existing checker treats action input
// values against entity fields (see backend_typecheck.entityInputFieldType).
func (at *AppTypes) CheckActionStep(parsed expr.Expr, expectedFieldType Type, env *TypeEnv) error {
	got, err := InferExpr(parsed, env)
	if err != nil {
		return err
	}
	// Try direct match first.
	s := NewSubst()
	if err := Unify(expectedFieldType, got, s); err == nil {
		return nil
	}
	// If the field is (maybe X), accept a value of type X.
	if con, ok := expectedFieldType.(TCon); ok && con.Name == "maybe" && len(con.Args) == 1 {
		s2 := NewSubst()
		if err := Unify(con.Args[0], got, s2); err == nil {
			return nil
		}
	}
	return fmt.Errorf("step value type mismatch: cannot unify %s with %s", expectedFieldType, got)
}

// ActionEnv builds a TypeEnv suitable for type-checking an action's step
// expressions. It binds each input alias field plus an `input` record
// containing all of them.
func (at *AppTypes) ActionEnv(action *model.Action, app *model.App) (*TypeEnv, error) {
	env := at.BaseEnvWithApp(app)
	alias := FindAlias(app, action.InputAlias)
	if alias == nil {
		return nil, fmt.Errorf("action %s: missing input alias %s", action.Name, action.InputAlias)
	}
	inputFields := map[string]Type{}
	for _, f := range alias.Fields {
		t, err := AliasFieldType(app, &f)
		if err != nil {
			return nil, err
		}
		env = env.Bind(f.Name, t)
		inputFields[f.Name] = t
	}
	order := make([]string, 0, len(alias.Fields))
	for _, f := range alias.Fields {
		order = append(order, f.Name)
	}
	env = env.Bind("input", TRecord{Name: alias.Name, Fields: inputFields, Order: order})
	return env, nil
}

func FindAlias(app *model.App, name string) *model.TypeAlias {
	for i := range app.InputAliases {
		if app.InputAliases[i].Name == name {
			return &app.InputAliases[i]
		}
	}
	return nil
}

// checkFunctionInSharedEnv infers a function body using a shared env+subst so
// that recursive and mutually-recursive calls resolve correctly across
// functions. The placeholder for fn (already bound in env by BaseEnvWithApp)
// is unified with (paramTypes -> bodyType).
func (at *AppTypes) checkFunctionInSharedEnv(parsed expr.Expr, fn *model.Function, env *TypeEnv, s *Subst) error {
	placeholder, ok := env.Lookup(fn.Name)
	if !ok {
		return fmt.Errorf("function %s: missing env binding", fn.Name)
	}
	placeholderArrow, ok := placeholder.(TCon)
	if !ok || placeholderArrow.Name != "->" {
		return fmt.Errorf("function %s: env binding is not arrow", fn.Name)
	}
	params, ret, _ := IsArrow(placeholderArrow)
	if len(params) != len(fn.Parameters) {
		return fmt.Errorf("function %s: param count mismatch", fn.Name)
	}
	child := env
	for i, name := range fn.Parameters {
		child = child.Bind(name, params[i])
	}
	bodyT, err := Infer(parsed, child, s)
	if err != nil {
		return fmt.Errorf("function %s: %w", fn.Name, err)
	}
	if err := Unify(ret, bodyT, s); err != nil {
		return fmt.Errorf("function %s: return type mismatch: %w", fn.Name, err)
	}
	return nil
}

// CheckFunction runs HM on a top-level user function. The function name must
// already be in the env (from BaseEnvWithApp) so recursion is possible.
//
// On success, returns the inferred type (an arrow). Caller can re-bind into
// env with this type to refine subsequent inferences.
func (at *AppTypes) CheckFunction(parsed expr.Expr, fn *model.Function, app *model.App) (Type, error) {
	env := at.BaseEnvWithApp(app)
	// Look up the placeholder for this function and align with its params.
	placeholder, ok := env.Lookup(fn.Name)
	if !ok {
		return nil, fmt.Errorf("function %s: missing env binding", fn.Name)
	}
	placeholderArrow, ok := placeholder.(TCon)
	if !ok || placeholderArrow.Name != "->" {
		return nil, fmt.Errorf("function %s: env binding is not arrow", fn.Name)
	}
	params, ret, _ := IsArrow(placeholderArrow)
	if len(params) != len(fn.Parameters) {
		return nil, fmt.Errorf("function %s: param count mismatch", fn.Name)
	}
	// Bind each parameter under its placeholder var name.
	for i, name := range fn.Parameters {
		env = env.Bind(name, params[i])
	}
	s := NewSubst()
	bodyT, err := Infer(parsed, env, s)
	if err != nil {
		return nil, fmt.Errorf("function %s: %w", fn.Name, err)
	}
	if err := Unify(ret, bodyT, s); err != nil {
		return nil, fmt.Errorf("function %s: %w", fn.Name, err)
	}
	return s.Apply(TArrow(params, ret)), nil
}

// ---- helpers ----

func (at *AppTypes) EntityFieldType(app *model.App, f *model.Field) (Type, error) {
	if f.RelationEntity != "" {
		entity := FindEntityByName(app, f.RelationEntity)
		if entity == nil {
			return nil, fmt.Errorf("unknown relation entity %s", f.RelationEntity)
		}
		// Use the related entity's primary key type. Recursively call
		// EntityFieldType so we also respect chained relations.
		for _, candidate := range entity.Fields {
			if candidate.Primary {
				inner, err := at.EntityFieldType(app, &candidate)
				if err != nil {
					return nil, err
				}
				if f.Optional {
					return TMaybe(inner), nil
				}
				return inner, nil
			}
		}
		return nil, fmt.Errorf("entity %s has no primary key", f.RelationEntity)
	}
	base, err := primitiveTypeFromString(f.Type)
	if err != nil {
		// Could be an enum value. Treat as string for now (matches existing checker).
		if len(f.EnumValues) > 0 {
			base = TString()
		} else {
			return nil, err
		}
	}
	if f.Optional {
		return TMaybe(base), nil
	}
	return base, nil
}

func AliasFieldType(app *model.App, f *model.AliasField) (Type, error) {
	if f.RelationEntity != "" {
		entity := FindEntityByName(app, f.RelationEntity)
		if entity == nil {
			return nil, fmt.Errorf("unknown relation entity %s", f.RelationEntity)
		}
		for _, candidate := range entity.Fields {
			if candidate.Primary {
				return primitiveTypeFromString(candidate.Type)
			}
		}
		return nil, fmt.Errorf("entity %s has no primary key", f.RelationEntity)
	}
	base, err := primitiveTypeFromString(f.Type)
	if err != nil {
		if len(f.EnumValues) > 0 {
			return TString(), nil
		}
		return nil, err
	}
	return base, nil
}

// parseFieldType handles record field type strings, which may be primitives,
// nominal references, or compound types like "(list X)", "(maybe X)",
// "(result E A)". Uses the sexp parser for compound types so nesting works.
func (at *AppTypes) parseFieldType(typeExpr string) (Type, error) {
	trimmed := strings.TrimSpace(typeExpr)
	if base, err := primitiveTypeFromString(trimmed); err == nil {
		return base, nil
	}
	canonical := canonicalTypeName(trimmed)
	if rec, ok := at.Records[canonical]; ok {
		return rec, nil
	}
	if rec, ok := at.Entities[canonical]; ok {
		return rec, nil
	}
	if u, ok := at.Unions[canonical]; ok {
		return u, nil
	}
	if rec, ok := at.Records[trimmed]; ok {
		return rec, nil
	}
	if rec, ok := at.Entities[trimmed]; ok {
		return rec, nil
	}
	if u, ok := at.Unions[trimmed]; ok {
		return u, nil
	}
	// Compound types — parse via sexp.
	if strings.HasPrefix(trimmed, "(") {
		node, err := sexp.ParseOne(trimmed)
		if err != nil {
			return nil, fmt.Errorf("parse type %s: %w", trimmed, err)
		}
		return at.parseFieldTypeNode(node)
	}
	return nil, fmt.Errorf("unknown type %q", trimmed)
}

// parseFieldTypeNode walks a sexp.Node representing a type expression.
func (at *AppTypes) parseFieldTypeNode(node sexp.Node) (Type, error) {
	switch node.Kind {
	case sexp.KindSymbol:
		return at.parseFieldType(node.Value)
	case sexp.KindList:
		if len(node.Children) == 0 {
			return nil, fmt.Errorf("empty type expression")
		}
		head := node.Children[0]
		if head.Kind != sexp.KindSymbol {
			return nil, fmt.Errorf("type head must be a symbol, got %v", head.Kind)
		}
		args := make([]Type, 0, len(node.Children)-1)
		for _, child := range node.Children[1:] {
			t, err := at.parseFieldTypeNode(child)
			if err != nil {
				return nil, err
			}
			args = append(args, t)
		}
		switch head.Value {
		case "list":
			if len(args) != 1 {
				return nil, fmt.Errorf("list expects 1 type arg, got %d", len(args))
			}
			return TList(args[0]), nil
		case "maybe":
			if len(args) != 1 {
				return nil, fmt.Errorf("maybe expects 1 type arg, got %d", len(args))
			}
			return TMaybe(args[0]), nil
		case "result":
			if len(args) != 2 {
				return nil, fmt.Errorf("result expects 2 type args, got %d", len(args))
			}
			return TResult(args[0], args[1]), nil
		}
		return nil, fmt.Errorf("unknown compound type %q", head.Value)
	default:
		return nil, fmt.Errorf("unsupported type node kind %v", node.Kind)
	}
}

// primitiveTypeName returns the mar-lang model name (e.g. "Int", "String")
// for a primitive HM type. Used to populate model.Query.ParameterTypes which
// the runtime needs to decode HTTP query string parameters.
func primitiveTypeName(t Type) (string, bool) {
	con, ok := t.(TCon)
	if !ok {
		return "", false
	}
	switch con.Name {
	case "bool":
		return "Bool", true
	case "int":
		return "Int", true
	case "decimal":
		return "Decimal", true
	case "string":
		return "String", true
	case "date":
		return "Date", true
	case "datetime":
		return "DateTime", true
	}
	return "", false
}

func primitiveTypeFromString(s string) (Type, error) {
	switch s {
	case "String", "string":
		return TString(), nil
	case "Bool", "bool":
		return TBool(), nil
	case "Int", "int":
		return TInt(), nil
	case "Decimal", "decimal":
		return TDecimal(), nil
	case "Date", "date":
		return TDate(), nil
	case "DateTime", "datetime":
		return TDateTime(), nil
	case "Cursor", "cursor":
		return TCursor(), nil
	case "Unit", "unit":
		return TUnit(), nil
	}
	return nil, fmt.Errorf("not a primitive type: %q", s)
}

func FindEntityByName(app *model.App, name string) *model.Entity {
	for i := range app.Entities {
		if app.Entities[i].Name == name {
			return &app.Entities[i]
		}
	}
	return nil
}

// typesEqual reports structural equality, walking through TVar.Ref-equivalents
// (via subst-style resolution if needed). For now it compares stringified
// forms — sufficient for "is this exactly bool".
func typesEqual(a, b Type) bool {
	return a.String() == b.String()
}
