package runtime

import "strings"

// Env is a runtime environment: name -> value, lexically scoped via parent links.
//
// A frame has one of two shapes:
//
//   - a TABLE (bindings != nil): the root built by BaseEnv and each module's
//     scope. These are the only frames Define writes to.
//   - ONE BINDING (bindings == nil): a parameter, a `let`, a pattern variable.
//     Every call binds its parameters this way, so this is the shape the
//     evaluator allocates in its inner loop, and it carries its name and value
//     inline instead of in a Go map built to hold a single key.
type Env struct {
	bindings map[string]Value
	name     string
	val      Value
	parent   *Env
}

// NewEnv returns a fresh empty environment.
func NewEnv() *Env {
	return &Env{bindings: map[string]Value{}}
}

// NewChildEnv returns a fresh empty environment whose parent is `parent`.
// Used by the project loader to give each module its own frame for bare
// names: values defined here shadow any same-named binding in `parent`
// without overwriting it. Cross-module references then reach `parent`
// (the shared rEnv) via the parent chain.
func NewChildEnv(parent *Env) *Env {
	return &Env{bindings: map[string]Value{}, parent: parent}
}

// Lookup finds name in this env or any parent. Returns the value and true if found.
func (e *Env) Lookup(name string) (Value, bool) {
	for cur := e; cur != nil; cur = cur.parent {
		if cur.bindings == nil {
			if cur.name == name {
				return cur.val, true
			}
			continue
		}
		if v, ok := cur.bindings[name]; ok {
			return v, true
		}
	}
	return nil, false
}

// lookupQualified is Lookup for the dotted key `Module.name`, without building
// that key on the heap: a qualified reference is evaluated every time control
// passes through it, and joining its parts each time was two allocations per
// `List.map` in the program. The key is assembled in a stack buffer, and a map
// index or a comparison through string(bytes) does not copy it.
func (e *Env) lookupQualified(mod []string, name string) (Value, bool) {
	var stack [96]byte
	key := stack[:0]
	for _, part := range mod {
		key = append(key, part...)
		key = append(key, '.')
	}
	key = append(key, name...)
	for cur := e; cur != nil; cur = cur.parent {
		if cur.bindings == nil {
			if cur.name == string(key) {
				return cur.val, true
			}
			continue
		}
		if v, ok := cur.bindings[string(key)]; ok {
			return v, true
		}
	}
	return nil, false
}

// ExportsOf collects every binding that belongs to module `modName`:
// keys of the form `modName.suffix` where suffix has no further dot
// (so `Mar.Admin.x` exports from `Mar.Admin`, not from `Mar`). Powers
// `import M exposing (..)` at eval time, mirroring the typechecker's
// TypeEnv.ExportsOf. Walks frames outermost-first so inner bindings
// win, matching Lookup's shadowing order.
func (e *Env) ExportsOf(modName string) map[string]Value {
	prefix := modName + "."
	var frames []*Env
	for cur := e; cur != nil; cur = cur.parent {
		frames = append(frames, cur)
	}
	out := map[string]Value{}
	export := func(name string, v Value) {
		if !strings.HasPrefix(name, prefix) {
			return
		}
		suffix := name[len(prefix):]
		if suffix == "" || strings.Contains(suffix, ".") {
			return
		}
		out[suffix] = v
	}
	for i := len(frames) - 1; i >= 0; i-- {
		if frames[i].bindings == nil {
			export(frames[i].name, frames[i].val)
			continue
		}
		for name, v := range frames[i].bindings {
			export(name, v)
		}
	}
	return out
}

// Bind returns a new env extending this with name -> v.
func (e *Env) Bind(name string, v Value) *Env {
	return &Env{name: name, val: v, parent: e}
}

// Define mutates the env to add a binding. Used at module load time when we
// need to set up mutually recursive references.
//
// Every caller defines into a table (a NewEnv / NewChildEnv frame). A
// one-binding frame is promoted to a table first, keeping what it held, so
// Define stays total instead of depending on that survey remaining true.
func (e *Env) Define(name string, v Value) {
	if e.bindings == nil {
		e.bindings = map[string]Value{e.name: e.val}
		e.name, e.val = "", nil
	}
	e.bindings[name] = v
}
