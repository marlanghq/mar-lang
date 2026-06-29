// Boundary shape lint — catches record-shape mismatches at framework
// callsites that Mar's polymorphic Repo / Auth.config signatures let
// through.
//
// Mar's HM typechecker accepts the patches / filter / signup records
// passed to Repo.create / Repo.update / Repo.findBy / Auth.config as
// fully-polymorphic `b`. Concrete column shapes are only enforced at
// runtime, inside repoCreateInner et al. — see internal/typecheck/env.go
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
//      against the entity (timestamps get a pass — the framework
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
	}
	return "Unknown"
}

// columnInfo describes one column on an entity.
type columnInfo struct {
	kind   columnKind
	serial bool // auto-incrementing PK; not required in create payloads
}

// entitySchema is the resolved view of an `Entity.define` binding.
type entitySchema struct {
	tableName string // the string literal passed to Entity.define
	columns   map[string]columnInfo
	order     []string // declaration order, for stable error messages
}

// RunShapeLint walks every parsed module and reports record-shape
// problems at framework-boundary callsites that the polymorphic
// types in BaseEnv let through. Returns nil when clean.
//
// `exprTypes` is the per-expression type map produced by the
// typechecker when expression tracking is enabled. The lint uses it
// to validate non-literal record values like `body = input.body` —
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
	ctx := lintCtx{schemas: schemas, exprTypes: exprTypes}
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
			// name is invalid — the rest of the lint can't make sense
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
				// Don't overwrite the first registration — keep the
				// "first wins" mental model consistent with the
				// runtime registry.
				continue
			}
			nameDecls[parse.nameLiteral] = nameDeclSite{module: modName, pos: parse.namePos}

			out[v.Name] = parse.schema
			if modName != "" {
				out[modName+"."+v.Name] = parse.schema
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
	nameLiteral string  // "" when name wasn't a string literal
	namePos     ast.Pos // position of the name value (or the spec record if missing)
	nameKnown   bool    // false when the `name` field is missing OR non-literal
	specPos     ast.Pos // position of the spec record literal itself
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
// Returns ok=false on anything else — the binding stays unknown to
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
	for _, f := range spec.Fields {
		switch f.Name {
		case "name":
			out.namePos = f.Value.Position()
			if lit, ok := f.Value.(*ast.EString); ok {
				out.nameLiteral = lit.Value
				out.nameKnown = true
			}
			// If it's not a literal, we leave nameLiteral empty and
			// nameKnown false — the validator will flag it.
		case "columns":
			if rec, ok := f.Value.(*ast.ERecord); ok {
				columnsRec = rec
			}
		}
		// uniques is parsed at runtime only — the lint doesn't need to
		// understand its values, just acknowledge the field exists.
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
	}
	return out, true
}

// validateEntityDefineName runs the static name-validation rules
// against a parsed Entity.define call. Emits issues for:
//   - missing `name` field
//   - `name` field that isn't a string literal (dynamic names rejected)
//   - empty name
//   - name starting with reserved prefix (sqlite_ / _mar_)
//   - name containing characters outside [A-Za-z0-9_] or starting with a digit
//
// Doesn't validate against duplicates — that's done by the caller
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
	// Entity.enum [A, B] constraint
	//   →  EApp { EApp { Entity.enum, list }, constraint }
	if innerApp, ok := app.Fn.(*ast.EApp); ok {
		if isQualified(innerApp.Fn, "Entity", "enum") {
			return columnInfo{kind: kindEnum}, true
		}
	}
	return columnInfo{}, false
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

// walkChildren dispatches into the children of e — generic AST
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
	}
	if op == "" {
		return nil
	}
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
	rec, ok := recordExpr.(*ast.ERecord)
	if !ok {
		return nil
	}
	name := resolveEntityName(entityExpr)
	schema, ok := ctx.schemas[name]
	if !ok {
		return nil
	}
	return checkRecordAgainstSchema(rec, schema, "Repo."+op, requireFull, ctx)
}

// matchAuthConfig recognizes Auth.config { entity = X, signup = (\e -> {...}) }
// and checks the signup record against X's schema. Only the direct
// shape — no `if`/`let`/effect chains — is supported here.
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
	// Signup record is a partial create — required columns the
	// framework auto-fills (timestamps) get a pass; everything else
	// follows the full-create rules.
	return checkSignupRecord(rec, schema, ctx)
}

// checkRecordAgainstSchema is the workhorse: for each field in the
// record literal, verify the name exists in the schema and the
// value matches the column kind — using literal pattern matching
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
				// Effect chain — we'd flag that as missing too. To
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
// value supplied for a timestamp column is also a pass — a sentinel
// like `createdAt = 0` would crash the old runtime but now gets
// quietly replaced by Time.now.
func checkSignupRecord(rec *ast.ERecord, schema entitySchema, ctx lintCtx) []ShapeIssue {
	var issues []ShapeIssue
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
		// Timestamps get a free pass — see comment above.
		if col.kind == kindTime {
			continue
		}
		if msg := checkValueAgainstKind(f.Value, col.kind, ctx); msg != "" {
			issues = append(issues, ShapeIssue{
				Pos:     f.Pos,
				Message: fmt.Sprintf("Auth.config.signup: field %q expects %s; %s", f.Name, col.kind, msg),
			})
		}
	}
	return issues
}

// checkValueAgainstKind is the two-stage check used for every field
// value. First it tries the literal-shape check (handles EInt /
// EString / EBool / ECtor — fast, no inference needed). If that's
// inconclusive (the value isn't a literal we can categorize) AND
// the lint has per-expression type info available, it falls back to
// the typechecker's inferred type for the expression. Returns "" on
// acceptable; a short diagnostic suffix otherwise.
func checkValueAgainstKind(e ast.Expr, kind columnKind, ctx lintCtx) string {
	if msg := checkLiteralAgainstKind(e, kind); msg != "" {
		return msg
	}
	// Literal check returned "" — either the value matched a literal
	// of the right kind (we're done) or it wasn't a literal we
	// understand. Tell the two apart by re-inspecting the AST node;
	// if it's NOT one of the literal types we handle, try inference.
	switch e.(type) {
	case *ast.EInt, *ast.EString, *ast.EFloat, *ast.ECtor:
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
// shapes — anything that's still a variable (free row, unresolved
// generic) is treated as "unknown, skip". False negatives are
// acceptable; false positives would be confusing because the
// typechecker already approved the expression's type.
func checkInferredAgainstKind(t Type, kind columnKind) string {
	con, ok := t.(TCon)
	if !ok {
		// TVar / TRecord / TArrow etc. — the value isn't a primitive
		// scalar, which is mismatched for every column kind we know.
		// Skip rather than report (the column might genuinely take a
		// scalar that's typed as a record alias somewhere).
		return ""
	}
	switch con.Name {
	case "Int":
		switch kind {
		case kindInt:
			return ""
		case kindTime:
			return "got Int — timestamps require a Time value (e.g. Time.now)"
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
	case "Float":
		return "got Float"
	}
	return "" // unknown constructor (Maybe, List, custom types) — skip
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
	case *ast.EFloat:
		return fmt.Sprintf("got Float literal %g", v.Value)
	case *ast.ECtor:
		switch kind {
		case kindEnum:
			// Conservative: any constructor that fits the column's
			// declared type passes. We don't enforce that the ctor
			// is in the entity's accepted list — capturing the
			// list at extraction time would cost ~30 LOC, but HM
			// already catches the common case (the ctor's custom
			// type is pinned by `Entity.enum [A, B]` so a ctor of
			// a DIFFERENT custom type fails to unify).
			//
			// The remaining gap is "schema accepts a strict subset
			// of the type's ctors" — e.g. `Role = Admin | Member |
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
	return "" // non-literal — skip
}
