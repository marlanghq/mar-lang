package typecheck

import (
	"fmt"
	"strings"

	"mar/internal/ast"
)

// validateServicePath checks the `{name:Type}` params of a service's
// declared path against the request type. Each path param must name a
// field of req with a matching type. Returns nil for non-service decls,
// non-literal paths (can't check statically), and paths with no params.
func validateServicePath(v *ast.ValueDecl, annotType Type, tEnv *typeNameEnv, s *Subst) error {
	sc, ok := annotType.(TCon)
	if !ok || sc.Name != "Service" || len(sc.Args) != 2 {
		return nil
	}
	path, pos, ok := serviceDeclarePathLiteral(v.Body)
	if !ok {
		return nil
	}
	row, err := elaboratePathLiteral(path, tEnv)
	if err != nil {
		return errorf(pos, "%s: %v", v.Name, err)
	}
	rowRec := row.(TRecord)
	if len(rowRec.Order) == 0 {
		return nil
	}
	req := resolveAliasToRecord(s.Apply(sc.Args[0]), tEnv)
	reqRec, ok := req.(TRecord)
	if !ok {
		return errorf(pos, "%s: path %q has params %v, but the request type is not a record with those fields",
			v.Name, path, rowRec.Order)
	}
	for _, name := range rowRec.Order {
		field, has := reqRec.Fields[name]
		if !has {
			return errorf(pos, "%s: path param `{%s}` is not a field of the request type", v.Name, name)
		}
		if err := Unify(rowRec.Fields[name], field, s); err != nil {
			return errorf(pos, "%s: path param `{%s}` type does not match the request field `%s`: %v",
				v.Name, name, name, err)
		}
	}
	return nil
}

// serviceDeclarePathLiteral recognises `Service.declare METHOD "path"`
// and returns the path string literal. Anything else returns ok=false.
func serviceDeclarePathLiteral(e ast.Expr) (string, ast.Pos, bool) {
	head, args := flattenApp(e)
	if !isQualified(head, "Service", "declare") || len(args) != 2 {
		return "", ast.Pos{}, false
	}
	str, ok := args[1].(*ast.EString)
	if !ok {
		return "", ast.Pos{}, false
	}
	return str.Value, str.Pos, true
}

// resolveAliasToRecord expands a type alias to its body when the alias
// names a record, so a request type written as a named alias (e.g.
// `Service GetNote ...` with `type alias GetNote = { id : Int }`)
// validates the same as an inline record.
func resolveAliasToRecord(t Type, tEnv *typeNameEnv) Type {
	for range 8 { // bounded to avoid cycles
		c, ok := t.(TCon)
		if !ok {
			return t
		}
		alias, ok := tEnv.aliases[c.Name]
		if !ok {
			return t
		}
		t = alias.Body
	}
	return t
}

// pathRowOfAnnot returns the row component of `Path r` if `t` is a
// Path type. Used by the value-decl bidirectional check to decide
// whether a String-literal body should be coerced into a typed Path.
//
// We accept either the canonical `TCon{"Path", [r]}` or the same
// after substitution chasing — anything else returns ok=false and the
// caller falls back to plain String inference.
func pathRowOfAnnot(t Type) (Type, bool) {
	c, ok := t.(TCon)
	if !ok || c.Name != "Path" || len(c.Args) != 1 {
		return nil, false
	}
	return c.Args[0], true
}

// elaboratePathLiteral parses a "/notes/{id:Int}/..." pattern and
// returns the closed record type for its params. Static-only paths
// (e.g. "/about") yield an empty closed record. Errors echo the
// runtime parser's wording so users see the same message whether
// the failure is at compile time or while debugging a generated URL.
//
// `tEnv` is used to resolve custom enum types in `{name:Type}` (e.g.
// `{role:Role}` where `Role = Member | Admin`). Only types whose
// every ctor takes zero args are accepted — same restriction as
// Entity.enum, since both round-trip through a stringly serialized
// form.
func elaboratePathLiteral(src string, tEnv *typeNameEnv) (Type, error) {
	parts := strings.Split(src, "/")
	fields := map[string]Type{}
	order := []string{}
	seen := map[string]bool{}
	for _, p := range parts {
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, ":") {
			return nil, fmt.Errorf("path %q: bare `:%s` is not supported. Use `{%s:Type}` (e.g. `{%s:String}` or `{%s:Int}`)",
				src, p[1:], p[1:], p[1:], p[1:])
		}
		if !strings.HasPrefix(p, "{") {
			if strings.ContainsAny(p, "{}") {
				return nil, fmt.Errorf("path %q: malformed segment %q (use `{name:Type}`)", src, p)
			}
			continue // literal segment — no field contribution
		}
		if !strings.HasSuffix(p, "}") {
			return nil, fmt.Errorf("path %q: unclosed segment %q", src, p)
		}
		inner := p[1 : len(p)-1]
		colon := strings.IndexByte(inner, ':')
		if colon < 0 {
			return nil, fmt.Errorf("path %q: param `{%s}` requires a type. Use `{%s:String}` or `{%s:Int}`",
				src, inner, inner, inner)
		}
		name := strings.TrimSpace(inner[:colon])
		ty := strings.TrimSpace(inner[colon+1:])
		if name == "" {
			return nil, fmt.Errorf("path %q: empty param name in `{%s}`", src, inner)
		}
		if seen[name] {
			return nil, fmt.Errorf("path %q: duplicate param %q", src, name)
		}
		seen[name] = true
		fieldType, err := pathSegmentType(ty, tEnv)
		if err != nil {
			return nil, fmt.Errorf("path %q: param %q: %w", src, name, err)
		}
		fields[name] = fieldType
		order = append(order, name)
	}
	// Closed record — no Tail. The point of typing the path is to make
	// the params row exact: extra fields on the user side become a
	// compile error.
	return TRecord{Fields: fields, Order: order}, nil
}

// pathSegmentType maps `{name:Type}` type identifiers to Mar types.
// Built-ins (`String`, `Int`) resolve directly. Any other identifier
// is treated as a custom enum type — looked up in `tEnv.customs`,
// validated to have all zero-arg ctors, and returned as the
// corresponding TCon.
//
// Kept in lockstep with the runtime decoders in internal/runtime/path.go,
// the JS runtime, and the iOS Swift runtime. Adding a new built-in
// here means updating those three sites; adding a new custom enum
// just works (the custom-type registry is shared infrastructure).
func pathSegmentType(name string, tEnv *typeNameEnv) (Type, error) {
	switch name {
	case "String":
		return TString, nil
	case "Int":
		return TInt, nil
	}
	if tEnv != nil {
		if ct, ok := tEnv.customs[name]; ok {
			// Reject ctors with payload — the URL → ctor mapping
			// only makes sense for nullary ctors (mirrors
			// Entity.enum's restriction). Saying "no, you can't"
			// up front beats a confusing runtime decode error.
			for _, cname := range ct.CtorOrder {
				if len(ct.Constructors[cname].Args) > 0 {
					return nil, fmt.Errorf("type %q can't be used in a path: ctor %q takes %d arg(s), only zero-arg enum types are allowed",
						name, cname, len(ct.Constructors[cname].Args))
				}
			}
			if len(ct.Params) != 0 {
				// Generic custom types (e.g. `Maybe a`) don't fit
				// here — even with all-nullary ctors, the row
				// would have to carry an unresolved parameter.
				return nil, fmt.Errorf("type %q can't be used in a path: parameterized types aren't supported", name)
			}
			return TCon{Name: name}, nil
		}
	}
	return nil, fmt.Errorf("unknown type %q. Allowed: String, Int, or a zero-arg custom type", name)
}
