package runtime

import (
	"fmt"
)

// unwrapModelTuple takes a value returned by init/update — expected to be
// (Model, Effect _ Msg) — and returns just the Model. The Effect side is
// ignored at this layer (the JS runtime in the browser handles dispatch).
// If the value is not a tuple, it's returned as-is.
func unwrapModelTuple(v Value) Value {
	if t, ok := v.(VTuple); ok && len(t.Members) == 2 {
		return t.Members[0]
	}
	return v
}

// VPage packages a single MVU screen (path + init/update/view) into a
// runnable value. Pages are first-class so users can compose them into
// frontend / fullstack apps:
//
//	myPage : Page
//	myPage = Page.root init update view
//
//	main = App.frontend [myPage]
//
// OriginModule/OriginName are filled in by the project loader when this
// page is the result of a top-level binding (e.g. `page = Page.root ...`
// in module Frontend gives Origin{Frontend, page}). App.fullstack reads
// them to know which qualified name to use as the browser bundle entry.
type VPage struct {
	Path         string
	InitFn       Value
	UpdateFn     Value
	ViewFn       Value
	OriginModule string
	OriginName   string
}

func (VPage) isValue() {}
func (p VPage) Display() string {
	return fmt.Sprintf("<page:%s>", p.Path)
}

// appBuiltins exposes the page / app builders.
//
//	Page.create  : String -> init -> update -> view -> Page
//	Page.root    : init -> update -> view -> Page          (path = "/")
//	App.frontend : List Page -> Effect String ()
//	App.backend  : List Route -> Effect String ()
//	App.fullstack: { api, pages } -> Effect String ()
//
// The default builtins for the App.* server entry points error out when
// evaluated outside of `mar dev`, because they need access to the
// project's module ASTs (to ship as a browser bundle) and mar.json
// (for the listening port). The CLI installs project-aware overrides
// before evaluating Main.main — see cmd/mar/main.go runDev.
func appBuiltins() map[string]Value {
	return map[string]Value{
		"pageCreate": nativeFn(4, func(args []Value) (Value, error) {
			path, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("Page.create: expected String path (got %T)", args[0])
			}
			return VPage{
				Path:     path.V,
				InitFn:   args[1],
				UpdateFn: args[2],
				ViewFn:   args[3],
			}, nil
		}),
		"pageRoot": nativeFn(3, func(args []Value) (Value, error) {
			return VPage{
				Path:     "/",
				InitFn:   args[0],
				UpdateFn: args[1],
				ViewFn:   args[2],
			}, nil
		}),

		"appFrontend": nativeFn(1, func(args []Value) (Value, error) {
			return nil, fmt.Errorf("App.frontend: only available via `mar dev` (the CLI installs the project-aware version)")
		}),
		"appBackend": nativeFn(1, func(args []Value) (Value, error) {
			return nil, fmt.Errorf("App.backend: only available via `mar dev` (the CLI installs the project-aware version)")
		}),
		"appFullstack": nativeFn(1, func(args []Value) (Value, error) {
			return nil, fmt.Errorf("App.fullstack: only available via `mar dev` (the CLI installs the project-aware version)")
		}),
	}
}
