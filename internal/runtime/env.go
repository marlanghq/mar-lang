package runtime

import "strings"

// Env is a runtime environment: name -> value, lexically scoped via parent links.
type Env struct {
	bindings map[string]Value
	parent   *Env
}

// NewEnv returns a fresh empty environment.
func NewEnv() *Env {
	return &Env{bindings: map[string]Value{}}
}

// NewChildEnv returns a fresh empty environment whose parent is `parent`.
// Used by the project loader to give each module its own frame for bare
// names — values defined here shadow any same-named binding in `parent`
// without overwriting it. Cross-module references then reach `parent`
// (the shared rEnv) via the parent chain.
func NewChildEnv(parent *Env) *Env {
	return &Env{bindings: map[string]Value{}, parent: parent}
}

// Lookup finds name in this env or any parent. Returns the value and true if found.
func (e *Env) Lookup(name string) (Value, bool) {
	for cur := e; cur != nil; cur = cur.parent {
		if v, ok := cur.bindings[name]; ok {
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
	for i := len(frames) - 1; i >= 0; i-- {
		for name, v := range frames[i].bindings {
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			suffix := name[len(prefix):]
			if suffix == "" || strings.Contains(suffix, ".") {
				continue
			}
			out[suffix] = v
		}
	}
	return out
}

// Bind returns a new env extending this with name -> v.
func (e *Env) Bind(name string, v Value) *Env {
	frame := map[string]Value{name: v}
	return &Env{bindings: frame, parent: e}
}

// BindMany returns a new env extending this with all bindings in m.
func (e *Env) BindMany(m map[string]Value) *Env {
	if len(m) == 0 {
		return e
	}
	frame := make(map[string]Value, len(m))
	for k, v := range m {
		frame[k] = v
	}
	return &Env{bindings: frame, parent: e}
}

// Define mutates the env to add a binding. Used at module load time when we
// need to set up mutually recursive references.
func (e *Env) Define(name string, v Value) {
	e.bindings[name] = v
}
