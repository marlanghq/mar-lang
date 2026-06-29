package runtime

import (
	"fmt"
	"strconv"
	"strings"
)

// PathSegment is one piece of a parsed URL pattern. Either a literal
// segment (`PathLit{"notes"}`) that must match the URL byte-for-byte,
// or a typed parameter (`PathParam{"id", "Int"}`) that captures a
// URL segment and decodes it.
type PathSegment struct {
	IsParam bool
	Lit     string // when !IsParam
	Name    string // when IsParam
	Type    string // when IsParam: "String", "Int" — see ValidPathTypes
}

// ValidPathTypes is the closed set of BUILT-IN types accepted in
// `{name:Type}` segments. Custom enum types (zero-arg ctors) are
// also accepted but resolve through the EnumTypes registry, not
// this map.
var ValidPathTypes = map[string]bool{
	"String": true,
	"Int":    true,
}

// EnumTypes is the runtime registry for custom enum types used in
// path patterns. Maps "Role" -> ["Member", "Admin"] so the matcher
// can decode `/users/admin` into VCtor("Admin"), and BuildURL can
// emit `/users/admin` from VCtor("Admin").
//
// Populated by the project loader (project.loadIntoEnv) — and by
// the test-only LoadModule path — when a CustomTypeDecl with all
// zero-arg ctors is encountered. Lookups are case-sensitive on the
// type name
// (matches Mar's PascalCase convention) but URL ↔ ctor mapping
// uses lowercased ctor names so URLs read idiomatically.
//
// Concurrency: writes happen during module load (single-threaded);
// reads happen during request handling (potentially concurrent).
// The map is read-only after load, so no locking — but if hot-reload
// ever swaps modules at runtime we'd need to wrap with a sync.RWMutex.
var EnumTypes = map[string][]string{}

// RegisterEnumType records a custom type's ctors so path patterns
// can reference it. Only zero-arg ctors are registered — types with
// payload aren't valid in paths and the typechecker already rejects
// them, so this is a defensive filter.
func RegisterEnumType(typeName string, ctorNames []string, ctorArities map[string]int) {
	for _, n := range ctorNames {
		if ctorArities[n] != 0 {
			return // not a path-eligible enum type
		}
	}
	EnumTypes[typeName] = ctorNames
}

// ResetEnumTypes clears the registry. Called by the dev-server's
// compile() on every hot reload — without this, types renamed or
// removed between reloads keep ghost entries in the map. Companion
// to ResetRegisteredEntities + ResetMigrationCache.
func ResetEnumTypes() {
	EnumTypes = map[string][]string{}
}

// VPath is a parsed URL pattern with typed params. Carries both the
// raw source (for diagnostics) and the segment list (for matching +
// link-building). Produced at typecheck time by coercing a String
// literal; never constructed directly by user code.
type VPath struct {
	Source   string        // the original "/notes/{id:Int}" string, for display
	Segments []PathSegment // parsed once at compile time
}

func (VPath) isValue() {}
func (p VPath) Display() string {
	return fmt.Sprintf("<path:%s>", p.Source)
}

// ParsePathPattern splits a URL pattern into segments and validates
// `{name:Type}` syntax. Errors out on:
//   - bare ":id" (the old syntax — must be `{id:Type}` now)
//   - unclosed braces
//   - missing type (`{id}` — type is mandatory)
//   - unknown type (must be in ValidPathTypes)
//   - duplicate param names within the same path
//
// Empty segments from leading/trailing slashes are dropped, so "/" parses
// to zero segments and "/notes/" matches "/notes" (trailing slash is not
// significant).
func ParsePathPattern(src string) (VPath, error) {
	parts := strings.Split(src, "/")
	segs := make([]PathSegment, 0, len(parts))
	seen := map[string]bool{}
	for _, p := range parts {
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, ":") {
			// Old syntax — error with a helpful message pointing at
			// the new one. The runtime won't accept this, so we
			// surface it loudly rather than silently treating it
			// as a literal segment.
			return VPath{}, fmt.Errorf("path %q: bare `:%s` is not supported. Use `{%s:Type}` (e.g. `{%s:String}` or `{%s:Int}`)",
				src, p[1:], p[1:], p[1:], p[1:])
		}
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			inner := p[1 : len(p)-1]
			colon := strings.IndexByte(inner, ':')
			if colon < 0 {
				return VPath{}, fmt.Errorf("path %q: param `{%s}` requires a type. Use `{%s:String}` or `{%s:Int}`",
					src, inner, inner, inner)
			}
			name := strings.TrimSpace(inner[:colon])
			ty := strings.TrimSpace(inner[colon+1:])
			if name == "" {
				return VPath{}, fmt.Errorf("path %q: empty param name in `{%s}`", src, inner)
			}
			// Accept built-ins outright; everything else has to be
			// a registered enum type. We can't validate enum types
			// at parse time on cold paths (the registry might be
			// empty during early bootstrap), so the decoder/encoder
			// re-check at use time. A truly unknown type produces
			// a runtime decode failure (matcher returns nil) — the
			// typechecker should have caught it earlier anyway.
			if !ValidPathTypes[ty] {
				if _, isEnum := EnumTypes[ty]; !isEnum {
					return VPath{}, fmt.Errorf("path %q: unknown type `%s` for param `%s`. Allowed: String, Int, or a registered zero-arg enum type",
						src, ty, name)
				}
			}
			if seen[name] {
				return VPath{}, fmt.Errorf("path %q: duplicate param `%s`", src, name)
			}
			seen[name] = true
			segs = append(segs, PathSegment{IsParam: true, Name: name, Type: ty})
			continue
		}
		if strings.ContainsAny(p, "{}") {
			return VPath{}, fmt.Errorf("path %q: malformed segment `%s` (use `{name:Type}`)", src, p)
		}
		segs = append(segs, PathSegment{Lit: p})
	}
	return VPath{Source: src, Segments: segs}, nil
}

// MatchURL tries to align `urlPath` against the path pattern. On
// success returns a VRecord with one field per param (typed via the
// per-param decoder). On failure returns nil — the caller should
// move on to the next page.
func (p VPath) MatchURL(urlPath string) Value {
	parts := splitURLPath(urlPath)
	if len(parts) != len(p.Segments) {
		return nil
	}
	fields := map[string]Value{}
	order := make([]string, 0, len(p.Segments))
	for i, seg := range p.Segments {
		if !seg.IsParam {
			if seg.Lit != parts[i] {
				return nil
			}
			continue
		}
		decoded, ok := decodePathSegment(parts[i], seg.Type)
		if !ok {
			return nil
		}
		fields[seg.Name] = decoded
		order = append(order, seg.Name)
	}
	return VRecord{Fields: fields, Order: order}
}

// BuildURL renders a Path back to a URL string using the values from
// `params`. Any missing or wrong-type field surfaces as an error so
// link-builder bugs are loud rather than producing malformed URLs.
func (p VPath) BuildURL(params VRecord) (string, error) {
	var b strings.Builder
	for _, seg := range p.Segments {
		b.WriteByte('/')
		if !seg.IsParam {
			b.WriteString(seg.Lit)
			continue
		}
		v, ok := params.Fields[seg.Name]
		if !ok {
			return "", fmt.Errorf("linkTo %s: missing param `%s`", p.Source, seg.Name)
		}
		s, err := encodePathSegment(v, seg.Type)
		if err != nil {
			return "", fmt.Errorf("linkTo %s: param `%s`: %w", p.Source, seg.Name, err)
		}
		b.WriteString(s)
	}
	if b.Len() == 0 {
		return "/", nil
	}
	return b.String(), nil
}

// splitURLPath drops leading/trailing slashes + empty segments. Mirrors
// the parser's empty-segment handling so "/notes/" and "/notes" match
// the same pattern.
func splitURLPath(p string) []string {
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// decodePathSegment runs the type-specific decoder. Failure means the
// URL doesn't match this pattern — the matcher tries the next one.
func decodePathSegment(raw, ty string) (Value, bool) {
	switch ty {
	case "String":
		// Caller is responsible for percent-decoding; the matchers
		// above hand us pre-decoded segments.
		return VString{V: raw}, true
	case "Int":
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, false
		}
		return VInt{V: n}, true
	}
	// Custom enum type: lookup ctor by lowercased name. The URL
	// segment is canonical-lowercase ("admin", "member"); the
	// matched ctor is whatever the user declared (typically
	// PascalCase: "Admin", "Member").
	if ctors, ok := EnumTypes[ty]; ok {
		for _, c := range ctors {
			if strings.EqualFold(c, raw) {
				return VCtor{Tag: c}, true
			}
		}
	}
	return nil, false
}

// encodePathSegment is the reverse — it stringifies a typed param for
// linkTo / Nav.pushTo. Type mismatches (passing a String where Int is
// expected, etc.) surface as user-facing errors.
func encodePathSegment(v Value, ty string) (string, error) {
	switch ty {
	case "String":
		s, ok := v.(VString)
		if !ok {
			return "", fmt.Errorf("expected String, got %T", v)
		}
		return s.V, nil
	case "Int":
		n, ok := v.(VInt)
		if !ok {
			return "", fmt.Errorf("expected Int, got %T", v)
		}
		return strconv.FormatInt(n.V, 10), nil
	}
	if ctors, ok := EnumTypes[ty]; ok {
		c, isCtor := v.(VCtor)
		if !isCtor {
			return "", fmt.Errorf("expected %s, got %T", ty, v)
		}
		// Defensive: ensure the ctor we got is actually a member
		// of this enum (typechecker should already guarantee this,
		// but a stray runtime construction would otherwise produce
		// a URL with arbitrary ctor names).
		for _, cn := range ctors {
			if cn == c.Tag {
				return strings.ToLower(c.Tag), nil
			}
		}
		return "", fmt.Errorf("ctor %q is not a member of %s", c.Tag, ty)
	}
	return "", fmt.Errorf("unknown path-param type %q", ty)
}

// buildPathURL is the shared back-end for linkTo / Nav.pushTo /
// Nav.replaceTo. Takes the typed Path (a VString at runtime — the
// typechecker enforces the Path-shaped surface contract) and the
// params record, and returns the rendered URL.
//
// `caller` is just a label for error messages so users see whether
// the failure came from a `linkTo` or `Nav.pushTo` site.
func buildPathURL(pathV Value, paramsV Value, caller string) (string, error) {
	src, ok := pathV.(VString)
	if !ok {
		return "", fmt.Errorf("%s: expected Path (got %T)", caller, pathV)
	}
	parsed, err := ParsePathPattern(src.V)
	if err != nil {
		return "", fmt.Errorf("%s: %w", caller, err)
	}
	params, ok := paramsV.(VRecord)
	if !ok {
		return "", fmt.Errorf("%s: expected record of params (got %T)", caller, paramsV)
	}
	return parsed.BuildURL(params)
}
