// Boundary shape lint: catches record-shape mismatches at framework
// callsites that Mar's polymorphic Repo / Auth.config signatures let
// through.
//
// Mar's HM typechecker accepts the patches / filter / signup records
// passed to Repo.create / Repo.update / Repo.findBy / Auth.config as
// fully-polymorphic `b`. Concrete column shapes are only enforced at
// runtime, inside repoCreateInner et al.: see internal/typecheck/env.go
// around line 710 for the documented trade-off.
//
// This pass closes the most common cases by walking the AST after
// typecheck:
//
//   1. Find every top-level `name = Entity.define "table" { schema }`
//      and capture the schema (column name → kind: Int / String /
//      Bool / Time / Enum / Serial).
//   2. For every Repo.create / Repo.update / Repo.findBy callsite
//      whose entity argument resolves to a known binding, check the
//      record-literal argument against that schema:
//        - unknown column name → error
//        - literal value doesn't fit column kind → error
//        - for `create`, missing required column → error
//   3. For Auth.config record literals, find the `entity` + `signup`
//      fields. If `signup = \e -> { record }`, check the record
//      against the entity (timestamps get a pass: the framework
//      auto-fills them, see runtime/auth.go::fillTimestampsForSignup).
//
// The pass is intentionally conservative: it only fires when the AST
// gives a definitive answer. Anything dynamic (record arg passed via
// variable, signup body wrapped in `if`/`let`, entity bound from a
// non-Entity.define expression) is skipped. False negatives are
// fine; false positives would erode trust in the lint and aren't.

package typecheck

import (
	"fmt"
	"strings"

	"mar/internal/ast"
)

// ShapeIssue is one finding from the shape lint. Positions match
// the offending field literal (or the record literal if the issue
// is "missing column"). Module is the dotted path of the module
// where the issue was found, so the CLI can resolve back to the
// source file for the snippet renderer.
type ShapeIssue struct {
	Module  string
	Pos     ast.Pos
	Message string
}

// Error makes ShapeIssue compatible with diag.SourceError's
// position-aware printer. Format mirrors typecheck.InferError so
// the CLI gets the same "Type error: …" / "  --> path:line:col"
// treatment for free.
func (s *ShapeIssue) Error() string {
	return fmt.Sprintf("shape error at %d:%d: %s", s.Pos.Line, s.Pos.Column, s.Message)
}

// Position exposes the AST position so diag.positionOf can pull
// (line, col) for the snippet block. Mirrors what
// typecheck.InferError exposes via its `Pos` field.
func (s *ShapeIssue) Position() ast.Pos { return s.Pos }

// columnKind is the runtime-shape category a column carries. Used
// when comparing literal values to the entity's expected types.
type columnKind int

const (
	kindUnknown columnKind = iota
	kindInt
	kindString
	kindBool
	kindTime
	kindEnum
	kindDecimal
)

func (k columnKind) String() string {
	switch k {
	case kindInt:
		return "Int"
	case kindString:
		return "String"
	case kindBool:
		return "Bool"
	case kindTime:
		return "Time"
	case kindEnum:
		return "enum"
	case kindDecimal:
		return "Decimal"
	}
	return "Unknown"
}

// columnInfo describes one column on an entity.
type columnInfo struct {
	kind   columnKind
	serial bool // auto-incrementing PK; not required in create payloads

	// acceptedCtors is the set an `Entity.enum [A, B]` column takes. The
	// row type's own constructor list can be LARGER, and nothing compared
	// the two: the program could build a value the column refuses. See
	// compareRowWithColumns.
	acceptedCtors []string
}

// entitySchema is the resolved view of an `Entity.define` binding.
type entitySchema struct {
	tableName string // the string literal passed to Entity.define
	columns   map[string]columnInfo
	order     []string // declaration order, for stable error messages

	// define is the `Entity.define { ... }` expression itself, and module
	// and bind name the declaration it came from. They exist so the row
	// type can be looked up and compared with these columns: the schema
	// says what the database holds, the row type says what the program
	// believes it reads back, and nothing was checking that they agree.
	define ast.Expr
	module string
	bind   string
}

// RunShapeLint walks every parsed module and reports record-shape
// problems at framework-boundary callsites that the polymorphic
// types in BaseEnv let through. Returns nil when clean.
//
// `exprTypes` is the per-expression type map produced by the
// typechecker when expression tracking is enabled. The lint uses it
// to validate non-literal record values like `body = input.body`:
// without it, only literal values can be checked. Pass nil if
// inference info isn't available; literal-only mode still catches
// the bulk of mistakes.
//
// In addition to record-shape checks, the lint also validates
// entity declarations themselves:
//   - the `name` field must be a string literal (no dynamic names)
//   - the literal must match the portable identifier shape
//   - the literal must not start with reserved prefixes
//   - the same literal name must not appear in two Entity.define calls
//     across the project
//
// These checks fire even when no Repo.* callsites exist; an entity
// declaration is itself a target.
func RunShapeLint(mods []*ast.Module, exprTypes map[ast.Expr]Type) []ShapeIssue {
	schemas, defineIssues := extractEntities(mods)
	issues := append([]ShapeIssue(nil), defineIssues...)
	if len(schemas) == 0 {
		return issues
	}
	ctx := lintCtx{schemas: schemas, exprTypes: exprTypes, unions: unionCtors(mods)}
	issues = append(issues, checkRowTypesAgainstSchemas(schemas, ctx)...)
	for _, m := range mods {
		modName := strings.Join(m.Name, ".")
		for _, d := range m.Decls {
			v, ok := d.(*ast.ValueDecl)
			if !ok {
				continue
			}
			for _, raw := range walkExpr(v.Body, ctx) {
				raw.Module = modName
				issues = append(issues, raw)
			}
		}
	}
	return issues
}

// lintCtx bundles the static state the walk needs. Threading a
// single struct through means new checks can add lookup tables
// without changing every helper signature.
type lintCtx struct {
	schemas   map[string]entitySchema
	exprTypes map[ast.Expr]Type // optional; nil = no inference info

	// unions maps a union type's name to the constructors it declares,
	// under both the bare name and the canonical `Module.Name` (ADR 0027),
	// because a checked type comes back canonical and the declaration is
	// written bare. Used to compare a row field's type with the set its
	// `Entity.enum` column accepts.
	unions map[string][]string
}

// extractEntities scans every top-level value declaration looking
// for `name = Entity.define { name = "table", columns = ..., uniques = ... }`.
// Successful matches go into the returned map under BOTH the bare
// binding name and the fully-qualified `Module.Name` so callers from
// a different module (or the same one) can resolve either way.
//
// Also returns any issues found at the declaration site itself:
// non-literal name, invalid name format, duplicate names across the
// project.
func extractEntities(mods []*ast.Module) (map[string]entitySchema, []ShapeIssue) {
	out := map[string]entitySchema{}
	var issues []ShapeIssue
	// Track which literal name was declared first so duplicate-name
	// errors can point at both locations.
	nameDecls := map[string]nameDeclSite{}

	for _, m := range mods {
		modName := strings.Join(m.Name, ".")
		for _, d := range m.Decls {
			v, ok := d.(*ast.ValueDecl)
			if !ok {
				continue
			}
			parse, ok := tryParseEntityDefine(v.Body)
			if !ok {
				continue
			}
			// Validate the name. Skip registering the schema if the
			// name is invalid: the rest of the lint can't make sense
			// of a broken declaration. The user still gets the error.
			issues = append(issues, validateEntityDefineName(parse, modName)...)
			if parse.nameLiteral == "" {
				// Either dynamic name (already reported) or invalid
				// shape; don't register.
				continue
			}
			// Duplicate detection: same literal name declared elsewhere.
			if prior, dup := nameDecls[parse.nameLiteral]; dup {
				issues = append(issues, ShapeIssue{
					Module: modName,
					Pos:    parse.namePos,
					Message: fmt.Sprintf(
						"Entity.define: name %q is already declared in module %q.\n"+
							"  Each entity must have a unique table name.\n"+
							"  Other declaration: %s.mar (around line %d)",
						parse.nameLiteral, prior.module, prior.module, prior.pos.Line),
				})
				// Don't overwrite the first registration: keep the
				// "first wins" mental model consistent with the
				// runtime registry.
				continue
			}
			nameDecls[parse.nameLiteral] = nameDeclSite{module: modName, pos: parse.namePos}

			schema := parse.schema
			schema.define, schema.module, schema.bind = v.Body, modName, v.Name
			for _, issue := range checkUniques(parse.uniques, schema) {
				issue.Module = modName
				issues = append(issues, issue)
			}
			out[v.Name] = schema
			if modName != "" {
				out[modName+"."+v.Name] = schema
			}
		}
	}
	return out, issues
}

// nameDeclSite records where a literal table name was first
// declared so the duplicate-detection error can name both sites.
type nameDeclSite struct {
	module string
	pos    ast.Pos
}

// parsedEntityDefine is the resolved Entity.define call: the
// columns/uniques the rest of the lint cares about, plus the bits
// the name-validation step needs (literal value + position for
// pointing the error at).
type parsedEntityDefine struct {
	schema      entitySchema
	nameLiteral string     // "" when name wasn't a string literal
	uniques     *ast.EList // the uniques list, when it is a literal one
	namePos     ast.Pos    // position of the name value (or the spec record if missing)
	nameKnown   bool       // false when the `name` field is missing OR non-literal
	specPos     ast.Pos    // position of the spec record literal itself
}

// tryParseEntityDefine recognizes the new single-record call shape:
//
//	Entity.define { name = "table", columns = { ... }, uniques = [...] }
//
// In the AST that's:
//
//	EApp { Fn: EQualified{"Entity","define"},
//	       Arg: ERecord{ name, columns, uniques } }
//
// Returns ok=false on anything else: the binding stays unknown to
// the lint and any Repo.* against it is silently skipped (as a
// non-Entity.define expression).
//
// The returned parsedEntityDefine reports the name literal AND its
// position separately so name-validation errors can point at the
// exact source location. When the `name` field is missing or
// non-literal, nameLiteral is "" and nameKnown is false.
func tryParseEntityDefine(e ast.Expr) (parsedEntityDefine, bool) {
	app, ok := e.(*ast.EApp)
	if !ok {
		return parsedEntityDefine{}, false
	}
	if !isQualified(app.Fn, "Entity", "define") {
		return parsedEntityDefine{}, false
	}
	spec, ok := app.Arg.(*ast.ERecord)
	if !ok {
		return parsedEntityDefine{}, false
	}

	out := parsedEntityDefine{specPos: spec.Position()}
	out.namePos = spec.Position() // fallback if name field is absent

	var columnsRec *ast.ERecord
	var uniquesList *ast.EList
	for _, f := range spec.Fields {
		switch f.Name {
		case "name":
			out.namePos = f.Value.Position()
			if lit, ok := f.Value.(*ast.EString); ok {
				out.nameLiteral = lit.Value
				out.nameKnown = true
			}
			// If it's not a literal, we leave nameLiteral empty and
			// nameKnown false: the validator will flag it.
		case "columns":
			if rec, ok := f.Value.(*ast.ERecord); ok {
				columnsRec = rec
			}
		case "uniques":
			// Read, not merely acknowledged. Three things about this list
			// are facts about the literal — a column it names that the
			// entity does not declare, an empty index, a column repeated
			// inside one index — and all three were caught only at boot.
			// The columns are right here in the same expression.
			if list, ok := f.Value.(*ast.EList); ok {
				uniquesList = list
			}
		}
	}

	if columnsRec != nil {
		cols := map[string]columnInfo{}
		var order []string
		for _, f := range columnsRec.Fields {
			info, ok := parseColumnType(f.Value)
			if !ok {
				continue
			}
			cols[f.Name] = info
			order = append(order, f.Name)
		}
		out.schema = entitySchema{tableName: out.nameLiteral, columns: cols, order: order}
		out.uniques = uniquesList
	}
	return out, true
}

// checkUniques compares the `uniques` list with the columns declared beside
// it. All three of these were caught only at boot, and all three are facts
// about one expression: measured, `uniques = [ [ "emial" ] ]` checked clean
// and the app refused to start with
// `uniques[0] references column "emial" which is not declared`.
func checkUniques(list *ast.EList, schema entitySchema) []ShapeIssue {
	if list == nil {
		return nil
	}
	var issues []ShapeIssue
	for i, entry := range list.Elements {
		inner, ok := entry.(*ast.EList)
		if !ok {
			continue // not a literal list: the boot check still has it
		}
		if len(inner.Elements) == 0 {
			issues = append(issues, ShapeIssue{
				Pos: inner.Position(),
				Message: fmt.Sprintf(
					"Entity.define %q: uniques[%d] is empty, and a unique index needs at least one column",
					schema.tableName, i),
			})
			continue
		}
		seen := map[string]bool{}
		for _, colExpr := range inner.Elements {
			lit, ok := colExpr.(*ast.EString)
			if !ok {
				continue
			}
			if seen[lit.Value] {
				issues = append(issues, ShapeIssue{
					Pos: colExpr.Position(),
					Message: fmt.Sprintf(
						"Entity.define %q: uniques[%d] names %q twice, and an index over one column twice is the index over it once",
						schema.tableName, i, lit.Value),
				})
				continue
			}
			seen[lit.Value] = true
			if _, declared := schema.columns[lit.Value]; !declared {
				issues = append(issues, ShapeIssue{
					Pos: colExpr.Position(),
					Message: fmt.Sprintf(
						"Entity.define %q: uniques[%d] names %q, which is not a column of this entity (has: %s)",
						schema.tableName, i, lit.Value, strings.Join(schema.order, ", ")),
				})
			}
		}
	}
	return issues
}

// validateEntityDefineName runs the static name-validation rules
// against a parsed Entity.define call. Emits issues for:
//   - missing `name` field
//   - `name` field that isn't a string literal (dynamic names rejected)
//   - empty name
//   - name starting with reserved prefix (sqlite_ / _mar_)
//   - name containing characters outside [A-Za-z0-9_] or starting with a digit
//
// Doesn't validate against duplicates: that's done by the caller
// once it has visibility into the full project.
func validateEntityDefineName(p parsedEntityDefine, module string) []ShapeIssue {
	var issues []ShapeIssue
	add := func(msg string, pos ast.Pos) {
		issues = append(issues, ShapeIssue{Module: module, Pos: pos, Message: msg})
	}
	if !p.nameKnown {
		add("Entity.define: `name` must be a string literal — dynamic names are not supported.\n"+
			"  Use a constant: `tableName = \"users\"`, then `name = tableName`\n"+
			"  is still a literal at the call site.", p.namePos)
		return issues
	}
	if p.nameLiteral == "" {
		add("Entity.define: name cannot be empty", p.namePos)
		return issues
	}
	if strings.HasPrefix(p.nameLiteral, "sqlite_") {
		add(fmt.Sprintf(
			"Entity.define: name %q starts with reserved prefix `sqlite_` (SQLite-internal)",
			p.nameLiteral), p.namePos)
	}
	if strings.HasPrefix(p.nameLiteral, "_mar_") {
		add(fmt.Sprintf(
			"Entity.define: name %q starts with reserved prefix `_mar_` (framework-internal)",
			p.nameLiteral), p.namePos)
	}
	for i, r := range p.nameLiteral {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
		isDigit := r >= '0' && r <= '9'
		if i == 0 {
			if !isLetter {
				add(fmt.Sprintf(
					"Entity.define: name %q must start with a letter or underscore (got %q)",
					p.nameLiteral, r), p.namePos)
				break
			}
		} else if !isLetter && !isDigit {
			add(fmt.Sprintf(
				"Entity.define: name %q contains invalid character %q (allowed: letters, digits, underscore)",
				p.nameLiteral, r), p.namePos)
			break
		}
	}
	return issues
}

// parseColumnType maps an Entity.* expression to its runtime kind.
// Recognizes:
//   - Entity.serial                  → Int + serial flag
//   - Entity.int       constraint    → Int
//   - Entity.text      constraint    → String
//   - Entity.bool      constraint    → Bool
//   - Entity.timestamp constraint    → Time
//   - Entity.enum [...] constraint   → Enum
func parseColumnType(e ast.Expr) (columnInfo, bool) {
	if isQualified(e, "Entity", "serial") {
		return columnInfo{kind: kindInt, serial: true}, true
	}
	app, ok := e.(*ast.EApp)
	if !ok {
		return columnInfo{}, false
	}
	// Entity.X constraint → outer Fn is EQualified{"Entity", X}
	if isQualified(app.Fn, "Entity", "int") {
		return columnInfo{kind: kindInt}, true
	}
	if isQualified(app.Fn, "Entity", "text") {
		return columnInfo{kind: kindString}, true
	}
	if isQualified(app.Fn, "Entity", "bool") {
		return columnInfo{kind: kindBool}, true
	}
	if isQualified(app.Fn, "Entity", "timestamp") {
		return columnInfo{kind: kindTime}, true
	}
	// Entity.enum [A, B] constraint / Entity.decimal scale constraint
	//   →  EApp { EApp { Entity.X, arg }, constraint }
	if innerApp, ok := app.Fn.(*ast.EApp); ok {
		if isQualified(innerApp.Fn, "Entity", "enum") {
			return columnInfo{kind: kindEnum, acceptedCtors: ctorListNames(innerApp.Arg)}, true
		}
		if isQualified(innerApp.Fn, "Entity", "decimal") {
			return columnInfo{kind: kindDecimal}, true
		}
	}
	return columnInfo{}, false
}

// ctorListNames reads the constructor names out of `Entity.enum [ A, B ]`.
// Anything that is not a list of plain names yields nil, and the comparison
// that uses it is skipped rather than guessed at.
func ctorListNames(e ast.Expr) []string {
	list, ok := e.(*ast.EList)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list.Elements))
	for _, el := range list.Elements {
		switch n := el.(type) {
		case *ast.ECtor:
			out = append(out, n.Name)
		case *ast.EVar:
			out = append(out, n.Name)
		case *ast.EQualified:
			out = append(out, n.Name)
		default:
			return nil
		}
	}
	return out
}

// isQualified is true when e == ModulePath.Name. Compares both the
// path length and the segment names exactly.
func isQualified(e ast.Expr, mod, name string) bool {
	q, ok := e.(*ast.EQualified)
	if !ok {
		return false
	}
	return len(q.Module) == 1 && q.Module[0] == mod && q.Name == name
}

// walkExpr recursively visits every sub-expression looking for
// framework callsites the lint understands. Calls into matchRepoOp
// and matchAuthConfig at each EApp.
func walkExpr(e ast.Expr, ctx lintCtx) []ShapeIssue {
	if e == nil {
		return nil
	}
	var issues []ShapeIssue
	if app, ok := e.(*ast.EApp); ok {
		issues = append(issues, matchRepoOp(app, ctx)...)
		issues = append(issues, matchAuthConfig(app, ctx)...)
	}
	issues = append(issues, walkChildren(e, ctx)...)
	return issues
}

// walkChildren dispatches into the children of e: generic AST
// recursion for the lint. Stays narrow on purpose: any expr the
// lint introduces support for is handled at the parent (walkExpr's
// EApp branch), so this function only walks subtrees forward.
func walkChildren(e ast.Expr, ctx lintCtx) []ShapeIssue {
	var issues []ShapeIssue
	switch n := e.(type) {
	case *ast.EApp:
		issues = append(issues, walkExpr(n.Fn, ctx)...)
		issues = append(issues, walkExpr(n.Arg, ctx)...)
	case *ast.EBinop:
		issues = append(issues, walkExpr(n.Left, ctx)...)
		issues = append(issues, walkExpr(n.Right, ctx)...)
	case *ast.ELambda:
		issues = append(issues, walkExpr(n.Body, ctx)...)
	case *ast.EIf:
		issues = append(issues, walkExpr(n.Cond, ctx)...)
		issues = append(issues, walkExpr(n.Then, ctx)...)
		issues = append(issues, walkExpr(n.Else, ctx)...)
	case *ast.ELet:
		for _, b := range n.Bindings {
			issues = append(issues, walkExpr(b.Body, ctx)...)
		}
		issues = append(issues, walkExpr(n.Body, ctx)...)
	case *ast.ETuple:
		for _, m := range n.Members {
			issues = append(issues, walkExpr(m, ctx)...)
		}
	case *ast.EList:
		for _, el := range n.Elements {
			issues = append(issues, walkExpr(el, ctx)...)
		}
	case *ast.ERecord:
		for _, f := range n.Fields {
			issues = append(issues, walkExpr(f.Value, ctx)...)
		}
	case *ast.ERecordUpdate:
		issues = append(issues, walkExpr(n.Record, ctx)...)
		for _, f := range n.Fields {
			issues = append(issues, walkExpr(f.Value, ctx)...)
		}
	case *ast.EFieldAccess:
		issues = append(issues, walkExpr(n.Record, ctx)...)
	case *ast.ECase:
		issues = append(issues, walkExpr(n.Subject, ctx)...)
		for _, b := range n.Branches {
			issues = append(issues, walkExpr(b.Body, ctx)...)
		}
	case *ast.ENegate:
		issues = append(issues, walkExpr(n.Inner, ctx)...)
	}
	return issues
}

// flattenApp turns nested EApp { EApp { f, a1 }, a2 } back into
// (f, [a1, a2]). Easier to pattern-match against multi-arg builtin
// signatures.
func flattenApp(e ast.Expr) (head ast.Expr, args []ast.Expr) {
	if app, ok := e.(*ast.EApp); ok {
		h, a := flattenApp(app.Fn)
		return h, append(a, app.Arg)
	}
	return e, nil
}

// resolveEntityName returns the lookup key for an entity expression.
// Bare `users` → "users"; qualified `Backend.Users.users` →
// "Backend.Users.users". Anything else (function call, field access)
// returns "" and the caller skips the check.
func resolveEntityName(e ast.Expr) string {
	switch n := e.(type) {
	case *ast.EVar:
		return n.Name
	case *ast.EQualified:
		return strings.Join(n.Module, ".") + "." + n.Name
	}
	return ""
}

// matchRepoOp inspects an application against the Repo.* surface.
// Repo.create / Repo.findBy take (entity, record); Repo.update takes
// (entity, id, patch). Only the record/patch argument is checked
// against the entity's columns. Repo.update / Repo.findBy use the
// "subset" mode (missing fields fine); Repo.create uses "full" mode
// (missing required columns flagged).
func matchRepoOp(app *ast.EApp, ctx lintCtx) []ShapeIssue {
	head, args := flattenApp(app)
	op := ""
	requireFull := false
	switch {
	case isQualified(head, "Repo", "create"):
		op = "create"
		requireFull = true
	case isQualified(head, "Repo", "update"):
		op = "update"
	case isQualified(head, "Repo", "findBy"):
		op = "findBy"
	case isQualified(head, "Repo", "findById"):
		return needsPrimaryKey(app, args, "findById", ctx)
	case isQualified(head, "Repo", "deleteById"):
		return needsPrimaryKey(app, args, "deleteById", ctx)
	}
	if op == "" {
		return nil
	}
	// Four Repo operations need the entity's primary key, and the runtime
	// refuses each of them by name when there is none. Whether an entity has
	// an `Entity.serial` column is written in its declaration, so a call that
	// can only ever fail does not have to wait for a request to say so.
	//
	// Measured: an app with a serial-less entity boots clean, creates the
	// table, and answers 500 to every `Repo.create` — which is nearly every
	// app — with "entity rows has no Entity.serial primary key" reaching
	// only the operator's log.
	//
	// Only the CALL is refused, not the entity. An entity read with
	// `Repo.all` or `Repo.findBy` never needs a key, and an app doing only
	// that works today.
	var entityExpr, recordExpr ast.Expr
	switch op {
	case "create", "findBy":
		if len(args) != 2 {
			return nil
		}
		entityExpr = args[0]
		recordExpr = args[1]
	case "update":
		if len(args) != 3 {
			return nil
		}
		entityExpr = args[0]
		recordExpr = args[2]
	}
	name := resolveEntityName(entityExpr)
	schema, ok := ctx.schemas[name]
	if !ok {
		return nil
	}
	if issues := needsPrimaryKey(app, args, op, ctx); len(issues) > 0 {
		return issues
	}
	if rec, isLiteral := recordExpr.(*ast.ERecord); isLiteral {
		return checkRecordAgainstSchema(rec, schema, "Repo."+op, requireFull, ctx)
	}
	// The record does not have to be written at the call site to be known.
	// Binding it to a name one line above used to hide it completely:
	// measured, `Repo.findBy users { emial = "ada" }` is a shape error and
	// `payload = { emial = "ada" }` followed by `Repo.findBy users payload`
	// checks clean and answers 500 to every request.
	return checkRecordTypeAgainstSchema(recordExpr, schema, "Repo."+op, requireFull, ctx)
}

// matchAuthConfig recognizes Auth.config { entity = X, signup = (\e -> {...}) }
// and checks the signup record against X's schema. Only the direct
// shape, no `if`/`let`/effect chains, is supported here.
func matchAuthConfig(app *ast.EApp, ctx lintCtx) []ShapeIssue {
	head, args := flattenApp(app)
	if !isQualified(head, "Auth", "config") {
		return nil
	}
	if len(args) != 1 {
		return nil
	}
	cfg, ok := args[0].(*ast.ERecord)
	if !ok {
		return nil
	}
	var entityExpr, signupExpr ast.Expr
	for _, f := range cfg.Fields {
		switch f.Name {
		case "entity":
			entityExpr = f.Value
		case "signup":
			signupExpr = f.Value
		}
	}
	if entityExpr == nil || signupExpr == nil {
		return nil
	}
	lambda, ok := signupExpr.(*ast.ELambda)
	if !ok {
		return nil
	}
	rec, ok := lambda.Body.(*ast.ERecord)
	if !ok {
		return nil
	}
	name := resolveEntityName(entityExpr)
	schema, ok := ctx.schemas[name]
	if !ok {
		return nil
	}
	// Signup record is a partial create: required columns the
	// framework auto-fills (timestamps) get a pass; everything else
	// follows the full-create rules.
	return checkSignupRecord(rec, schema, ctx)
}

// checkRecordAgainstSchema is the workhorse: for each field in the
// record literal, verify the name exists in the schema and the
// value matches the column kind: using literal pattern matching
// first, then falling back to the typechecker's per-expression type
// map when the value isn't a literal. When requireFull is true,
// also flag every non-serial, non-timestamp column the record omits.
func checkRecordAgainstSchema(rec *ast.ERecord, schema entitySchema, op string, requireFull bool, ctx lintCtx) []ShapeIssue {
	var issues []ShapeIssue
	seen := map[string]bool{}
	for _, f := range rec.Fields {
		seen[f.Name] = true
		col, ok := schema.columns[f.Name]
		if !ok {
			issues = append(issues, ShapeIssue{
				Pos: f.Pos,
				Message: fmt.Sprintf("%s: field %q is not a column of %q (have: %s)",
					op, f.Name, schema.tableName, strings.Join(schema.order, ", ")),
			})
			continue
		}
		if msg := checkValueAgainstKind(f.Value, col.kind, ctx); msg != "" {
			issues = append(issues, ShapeIssue{
				Pos:     f.Pos,
				Message: fmt.Sprintf("%s: field %q expects %s; %s", op, f.Name, col.kind, msg),
			})
		}
	}
	if requireFull {
		for _, name := range schema.order {
			col := schema.columns[name]
			if col.serial {
				continue // auto-filled by DB
			}
			if col.kind == kindTime {
				// Timestamps in Repo.create still need a value, but
				// the typical pattern threads Time.now in via an
				// Effect chain: we'd flag that as missing too. To
				// avoid false positives, allow timestamps to be
				// silently omitted from create literals; the runtime
				// will catch genuine omissions.
				continue
			}
			if !seen[name] {
				issues = append(issues, ShapeIssue{
					Pos:     rec.Pos,
					Message: fmt.Sprintf("%s: missing column %q (%s) on %q", op, name, col.kind, schema.tableName),
				})
			}
		}
	}
	return issues
}

// checkSignupRecord is a tighter cousin of checkRecordAgainstSchema:
// missing timestamps are silently accepted (the framework fills them
// in via runtime.fillTimestampsForSignup), missing non-timestamps
// are NOT flagged either (a future iteration could surface them
// once we're confident the lint doesn't over-fire), and any literal
// value supplied for a timestamp column is also a pass: a sentinel
// like `createdAt = 0` would crash the old runtime but now gets
// quietly replaced by Time.now.
func checkSignupRecord(rec *ast.ERecord, schema entitySchema, ctx lintCtx) []ShapeIssue {
	var issues []ShapeIssue
	given := map[string]bool{}
	for _, f := range rec.Fields {
		col, ok := schema.columns[f.Name]
		if !ok {
			issues = append(issues, ShapeIssue{
				Pos: f.Pos,
				Message: fmt.Sprintf("Auth.config.signup: field %q is not a column of %q (have: %s)",
					f.Name, schema.tableName, strings.Join(schema.order, ", ")),
			})
			continue
		}
		// Timestamps get a free pass: see comment above.
		if col.kind == kindTime {
			continue
		}
		if msg := checkValueAgainstKind(f.Value, col.kind, ctx); msg != "" {
			issues = append(issues, ShapeIssue{
				Pos:     f.Pos,
				Message: fmt.Sprintf("Auth.config.signup: field %q expects %s; %s", f.Name, col.kind, msg),
			})
		}
		given[f.Name] = true
	}

	// A column the hook does not fill and the database cannot supply.
	//
	// This used to be left alone, with a note that a future iteration could
	// surface it "once we're confident the lint doesn't over-fire". Measured
	// in the meantime, on an auth entity with a required `name` the hook
	// omits: `mar check` passed, the app booted clean, and the FIRST sign-in
	// answered `500 {"error":"signup_failed"}` — the first user, and nothing
	// naming the column.
	//
	// The two exemptions are the ones Repo.create already makes, for the
	// same reasons: a serial column is filled by the database, and a
	// timestamp by fillTimestampsForSignup.
	for _, name := range schema.order {
		col := schema.columns[name]
		if col.serial || col.kind == kindTime || given[name] {
			continue
		}
		issues = append(issues, ShapeIssue{
			Pos: rec.Pos,
			Message: fmt.Sprintf(
				"Auth.config.signup: %q needs a %s and the hook does not give it one.\n"+
					"  Nothing else can: the database has no default, so the first person to "+
					"sign in gets a 500 and the row is never written.\n"+
					"  Add %q to the record `signup` returns",
				name, col.kind, name),
		})
	}
	return issues
}

// checkValueAgainstKind is the two-stage check used for every field
// value. First it tries the literal-shape check (handles EInt /
// EString / EBool / ECtor: fast, no inference needed). If that's
// inconclusive (the value isn't a literal we can categorize) AND
// the lint has per-expression type info available, it falls back to
// the typechecker's inferred type for the expression. Returns "" on
// acceptable; a short diagnostic suffix otherwise.
func checkValueAgainstKind(e ast.Expr, kind columnKind, ctx lintCtx) string {
	if msg := checkLiteralAgainstKind(e, kind); msg != "" {
		return msg
	}
	// Literal check returned "": either the value matched a literal
	// of the right kind (we're done) or it wasn't a literal we
	// understand. Tell the two apart by re-inspecting the AST node;
	// if it's NOT one of the literal types we handle, try inference.
	switch e.(type) {
	case *ast.EInt, *ast.EString, *ast.ECtor:
		// Literal already passed the check above.
		return ""
	}
	if ctx.exprTypes == nil {
		return ""
	}
	t, ok := ctx.exprTypes[e]
	if !ok {
		return ""
	}
	return checkInferredAgainstKind(t, kind)
}

// checkInferredAgainstKind compares a typechecker-inferred type
// against the expected column kind. Only fires on concrete TCon
// shapes: anything that's still a variable (free row, unresolved
// generic) is treated as "unknown, skip". False negatives are
// acceptable; false positives would be confusing because the
// typechecker already approved the expression's type.
func checkInferredAgainstKind(t Type, kind columnKind) string {
	// Every message here starts with "got ", because the row-type caller
	// slices that prefix off to build its own sentence.
	switch shape := t.(type) {
	case TVar:
		// Nothing is pinned yet, so there is nothing to disagree with.
		return ""
	case TRecord:
		return "got a record"
	case TTuple:
		return "got a tuple"
	case TArrow:
		return "got a function"
	case TUnit:
		return "got ()"
	case TCon:
		_ = shape
	default:
		return ""
	}
	con := t.(TCon)
	// A column holds ONE value. Nothing that groups values can be stored in
	// or read back from one, and `Maybe` never reaches here — the caller
	// answers optionality first, with a better sentence.
	switch con.Name {
	case "List", "Set", "Dict":
		return "got a " + con.Name
	}
	switch con.Name {
	case "Int":
		switch kind {
		case kindInt:
			return ""
		case kindTime:
			return "got Int — timestamps require a Time value (e.g. Time.now)"
		case kindDecimal:
			return "got Int — decimal columns take a Decimal (write 12 as 12.0, or Decimal.fromInt)"
		case kindString, kindBool, kindEnum:
			return "got Int"
		}
	case "String":
		switch kind {
		case kindString, kindEnum:
			return ""
		default:
			return "got String"
		}
	case "Bool":
		if kind == kindBool {
			return ""
		}
		return "got Bool"
	case "Time":
		if kind == kindTime {
			return ""
		}
		return "got Time"
	case "Decimal":
		if kind == kindDecimal {
			return ""
		}
		return "got Decimal"
	}
	// A name this switch does not know is a type the PROGRAM declares — a
	// union, almost always. Against an enum column that is the ordinary
	// case, and compareEnumSets owns it: it compares the constructor sets,
	// which is the sharper question. Against any other column it is a
	// mismatch nobody was reporting: the write path refuses a constructor
	// where TEXT wants a string, and the read hands back the string while
	// the row type promises the union, so a `case` over it reaches "no case
	// branch matched" on a program the checker proved total.
	if con.Name != "Maybe" && kind != kindEnum {
		return "got " + con.Name
	}
	return "" // Maybe (the caller answers it) and the enum case above
}

// checkLiteralAgainstKind compares a literal value expression with
// the expected column kind. Returns "" when the value is acceptable
// (either matches or isn't a literal we can reason about); returns a
// short diagnostic suffix otherwise. Callers prepend their own
// "FIELD expects KIND; " prefix.
func checkLiteralAgainstKind(e ast.Expr, kind columnKind) string {
	switch v := e.(type) {
	case *ast.EInt:
		switch kind {
		case kindInt:
			return ""
		case kindTime:
			return fmt.Sprintf("got Int literal %d — timestamps require a Time value (Time.now or similar)", v.Value)
		case kindString, kindBool:
			return fmt.Sprintf("got Int literal %d", v.Value)
		}
	case *ast.EString:
		switch kind {
		case kindString, kindEnum:
			return ""
		default:
			return fmt.Sprintf("got String literal %q", v.Value)
		}
	case *ast.ECtor:
		switch kind {
		case kindEnum:
			// Conservative: any constructor that fits the column's
			// declared type passes. We don't enforce that the ctor
			// is in the entity's accepted list: capturing the
			// list at extraction time would cost ~30 LOC, but HM
			// already catches the common case (the ctor's custom
			// type is pinned by `Entity.enum [A, B]` so a ctor of
			// a DIFFERENT custom type fails to unify).
			//
			// The remaining gap is "schema accepts a strict subset
			// of the type's ctors": e.g. `Role = Admin | Member |
			// Owner` but `Entity.enum [Admin, Member]`. Then writing
			// `role = Owner` typechecks (Owner : Role) but the
			// SQLite CHECK constraint rejects it at INSERT time.
			//
			// All real examples in this repo list every ctor of the
			// type, so the gap is theoretical. Reopen when someone
			// actually hits the runtime CHECK failure in production.
			return ""
		case kindBool:
			if v.Name == "True" || v.Name == "False" {
				return ""
			}
			return fmt.Sprintf("got constructor %s", v.Name)
		default:
			return fmt.Sprintf("got constructor %s", v.Name)
		}
	}
	return "" // non-literal: skip
}

// checkRecordTypeAgainstSchema is checkRecordAgainstSchema for a record that
// arrives as a value rather than as syntax.
//
// It speaks only about a CLOSED record type. An open one (`{ r | x : Int }`)
// constrains the fields it names and says nothing about the rest, so a missing
// column there is not a missing column; a type variable says nothing at all.
// Both are passed over, because a lint that guesses is worse than one that is
// quiet.
func checkRecordTypeAgainstSchema(e ast.Expr, schema entitySchema, op string, requireFull bool, ctx lintCtx) []ShapeIssue {
	if ctx.exprTypes == nil {
		return nil
	}
	t, ok := ctx.exprTypes[e]
	if !ok {
		return nil
	}
	rec, ok := StripForall(t).(TRecord)
	if !ok || rec.Tail != nil {
		return nil
	}
	pos := e.Position()
	var issues []ShapeIssue
	seen := map[string]bool{}
	for _, fname := range rec.Order {
		seen[fname] = true
		col, known := schema.columns[fname]
		if !known {
			issues = append(issues, ShapeIssue{
				Pos: pos,
				Message: fmt.Sprintf("%s: field %q is not a column of %q (have: %s)",
					op, fname, schema.tableName, strings.Join(schema.order, ", ")),
			})
			continue
		}
		if msg := checkInferredAgainstKind(rec.Fields[fname], col.kind); msg != "" {
			issues = append(issues, ShapeIssue{
				Pos:     pos,
				Message: fmt.Sprintf("%s: field %q expects %s; %s", op, fname, col.kind, msg),
			})
		}
	}
	if !requireFull {
		return issues
	}
	for _, name := range schema.order {
		col := schema.columns[name]
		// Serial columns are filled by the database, and a timestamp is
		// typically threaded in through an effect chain — the same two
		// exemptions the literal path makes, for the same reasons.
		if col.serial || col.kind == kindTime || seen[name] {
			continue
		}
		issues = append(issues, ShapeIssue{
			Pos:     pos,
			Message: fmt.Sprintf("%s: missing column %q (%s) on %q", op, name, col.kind, schema.tableName),
		})
	}
	return issues
}

// checkRowTypesAgainstSchemas compares what an entity SAYS it reads back with
// what its columns actually hold.
//
// `users : Entity User` names two things that were never compared: the row
// type, which every read is typed as, and the columns, which decide what a
// read produces. A disagreement is not caught anywhere, and the sharpest one
// breaks the totality guarantee. Measured end to end:
//
//	tag : Maybe String   over a NOT NULL TEXT column
//	mar check            passes, and PROVES the `case` over it total
//	Repo.create ... { tag = Just "verde" }   the Just is unwrapped, "verde" stored
//	Repo.all             hands back a String where the type says Maybe String
//	case tag of Just t -> ... ; Nothing -> ...   no case branch matched
//
// ADR 0022 says every `case` is total. This is the one way a checked program
// reaches a branch that cannot match, so it is refused where it is written.
//
// The other direction — a COLUMN with no field in the row type — is left
// alone. Measured, it reads back fine: the record carries a field the type
// does not mention, and no program can observe it because naming it would not
// type-check. Where it does bite is `Repo.create`, which must supply every
// column, and the call site already says so.
func checkRowTypesAgainstSchemas(schemas map[string]entitySchema, ctx lintCtx) []ShapeIssue {
	if ctx.exprTypes == nil {
		return nil
	}
	var issues []ShapeIssue
	seen := map[string]bool{} // each schema is registered twice (bare and qualified)
	for _, schema := range schemas {
		if schema.define == nil || seen[schema.module+"."+schema.bind] {
			continue
		}
		seen[schema.module+"."+schema.bind] = true
		row, ok := entityRowType(schema.define, ctx)
		if !ok {
			continue
		}
		issues = append(issues, compareRowWithColumns(row, schema, ctx)...)
	}
	sortIssues(issues)
	return issues
}

// entityRowType reads the `T` out of the `Entity T` an `Entity.define` call
// has. A row type that is still open or a variable says nothing to compare.
func entityRowType(define ast.Expr, ctx lintCtx) (TRecord, bool) {
	t, ok := ctx.exprTypes[define]
	if !ok {
		return TRecord{}, false
	}
	con, ok := StripForall(t).(TCon)
	if !ok || con.Name != "Entity" || len(con.Args) != 1 {
		return TRecord{}, false
	}
	rec, ok := StripForall(con.Args[0]).(TRecord)
	if !ok || rec.Tail != nil {
		return TRecord{}, false
	}
	return rec, true
}

func compareRowWithColumns(row TRecord, schema entitySchema, ctx lintCtx) []ShapeIssue {
	var issues []ShapeIssue
	pos := schema.define.Position()
	for _, name := range row.Order {
		col, known := schema.columns[name]
		if !known {
			issues = append(issues, ShapeIssue{
				Module: schema.module,
				Pos:    pos,
				Message: fmt.Sprintf(
					"Entity.define: the row type of %q has a field %q and %q has no such column (has: %s).\n"+
						"  Every read is typed as the row type and produces the columns, so that field "+
						"is missing from every record the database hands back",
					schema.bind, name, schema.tableName, strings.Join(schema.order, ", ")),
			})
			continue
		}
		if isOptional(row.Fields[name]) {
			issues = append(issues, ShapeIssue{
				Module: schema.module,
				Pos:    pos,
				Message: fmt.Sprintf(
					"Entity.define: the row type of %q says %q is optional, and the column always holds a value.\n"+
						"  A `Just` written there is unwrapped and stored bare, and the read hands back "+
						"a %s where the type says `Maybe %s` — so a `case` over it reaches "+
						"\"no case branch matched\", on a program the checker proved total.\n"+
						"  Declare it as `%s`",
					schema.bind, name, col.kind, col.kind, col.kind),
			})
			continue
		}
		if issue := compareEnumSets(name, row.Fields[name], col, schema, ctx); issue != nil {
			issues = append(issues, *issue)
			continue
		}
		if msg := checkInferredAgainstKind(row.Fields[name], col.kind); msg != "" {
			issues = append(issues, ShapeIssue{
				Module: schema.module,
				Pos:    pos,
				Message: fmt.Sprintf(
					"Entity.define: the row type of %q says %q is %s; the column holds %s.\n"+
						"  Every read is typed as the row type, so each row comes back as something "+
						"its own type does not describe",
					schema.bind, name, msg[len("got "):], col.kind),
			})
		}
	}
	return issues
}

// isOptional recognises `Maybe X`, which is the shape with no honest reading
// against a column that is always filled.
func isOptional(t Type) bool {
	con, ok := StripForall(t).(TCon)
	return ok && con.Name == "Maybe" && len(con.Args) == 1
}

// unionCtors indexes every union declared in the project by the constructors
// it has. Read from the AST rather than from a TypeEnv so the lint keeps the
// arguments it already takes.
func unionCtors(mods []*ast.Module) map[string][]string {
	out := map[string][]string{}
	for _, m := range mods {
		modName := strings.Join(m.Name, ".")
		for _, d := range m.Decls {
			ct, ok := d.(*ast.CustomTypeDecl)
			if !ok {
				continue
			}
			names := make([]string, 0, len(ct.Constructors))
			for _, c := range ct.Constructors {
				names = append(names, c.Name)
			}
			out[ct.Name] = names
			if modName != "" {
				out[modName+"."+ct.Name] = names
			}
		}
	}
	return out
}

// compareEnumSets reports a row field whose type has constructors the column
// does not accept.
//
// Measured on a running app, `status : Status` with three constructors over
// `Entity.enum [ Open, Closed ]`:
//
//	POST {"status":{"__ctor":"Open"}}      200, stored
//	POST {"status":{"__ctor":"Pending"}}   500 Internal Server Error
//
// `Pending` is a perfectly good `Status`, the wire guard accepts it because
// the contract says `Status`, and the write fails on a CHECK the source never
// mentions. Both halves — the type's constructor list and the column's
// accepted list — are written down in the same file.
//
// The other direction is left alone: a column accepting MORE than the type
// has is a set nothing in the program can produce, and the read path already
// refuses a row holding one.
func compareEnumSets(fieldName string, fieldType Type, col columnInfo, schema entitySchema, ctx lintCtx) *ShapeIssue {
	if col.kind != kindEnum || len(col.acceptedCtors) == 0 {
		return nil
	}
	con, ok := StripForall(fieldType).(TCon)
	if !ok {
		return nil
	}
	declared, known := ctx.unions[con.Name]
	if !known || len(declared) == 0 {
		return nil
	}
	accepted := map[string]bool{}
	for _, c := range col.acceptedCtors {
		accepted[c] = true
	}
	var missing []string
	for _, c := range declared {
		if !accepted[c] {
			missing = append(missing, "`"+c+"`")
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &ShapeIssue{
		Module: schema.module,
		Pos:    schema.define.Position(),
		Message: fmt.Sprintf(
			"Entity.define: %q holds %s, and the column does not accept %s.\n"+
				"  The program can build %s and the column refuses it, so writing one fails "+
				"inside a request on a CHECK the source never mentions.\n"+
				"  Add %s to the `Entity.enum` list, or remove %s from the type",
			fieldName, con.Name, strings.Join(missing, ", "),
			pickOne(missing), strings.Join(missing, ", "), strings.Join(missing, ", ")),
	}
}

func pickOne(names []string) string {
	if len(names) == 0 {
		return "one"
	}
	return names[0]
}

// pkOperations are the Repo calls that address a row by its primary key. Each
// is refused by the runtime, by name, when the entity has no `Entity.serial`.
var pkOperations = map[string]bool{
	"create": true, "update": true, "findById": true, "deleteById": true,
}

// needsPrimaryKey reports a call that can only ever fail because the entity it
// names has no serial column.
func needsPrimaryKey(app *ast.EApp, args []ast.Expr, op string, ctx lintCtx) []ShapeIssue {
	if !pkOperations[op] || len(args) == 0 {
		return nil
	}
	schema, ok := ctx.schemas[resolveEntityName(args[0])]
	if !ok {
		return nil
	}
	for _, name := range schema.order {
		if schema.columns[name].serial {
			return nil
		}
	}
	return []ShapeIssue{{
		Pos: app.Position(),
		Message: fmt.Sprintf(
			"Repo.%s: %q has no `Entity.serial` column, and this call addresses a row by its key.\n"+
				"  It fails on every request, with the table already created and the app booted clean.\n"+
				"  Give %q an `Entity.serial` column, or read it with `Repo.all` / `Repo.findBy`, "+
				"which need no key",
			op, schema.tableName, schema.tableName),
	}}
}
