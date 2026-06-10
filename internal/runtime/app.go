package runtime

import (
	"fmt"
)

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
	Path      string
	InitFn    Value
	UpdateFn  Value
	ViewFn    Value
	Title     string // empty = no override (host's HTML <title> stays)
	Redirect  string // non-empty = Page.protected; redirect target if Auth.me returns Nothing
	IsDynamic bool   // true → Path is a pattern with `{name:Type}` segments;
	//                  // runtime parses the URL and threads a Params record
	//                  // through init/update/view as an extra leading arg.
	IsProtected bool // duplicates `Redirect != ""` for clarity; future
	//                  // protected variants without a redirect (eg. native
	//                  // sheets) will still set this true.
	IsAdmin bool // Page.adminProtected — gated by the framework admin
	//             // session (mar.json["admins"]) instead of the app's user
	//             // auth. Implies IsProtected.
	PathPattern []PathSegment // populated for dynamic pages; parsed once at
	//                          // builder time so the bundle emit + matchers
	//                          // don't re-parse the source string.
	OriginModule string
	OriginName   string
}

func (VPage) isValue() {}
func (p VPage) Display() string {
	return fmt.Sprintf("<page:%s>", p.Path)
}

// readPageRecord pulls the common { path, init, update, view, title? } shape
// out of a record argument. Used by Page.dynamic and Page.dynamicProtected
// — both share the same surface as Page.create, the only difference being
// which flags get flipped on the resulting VPage. The caller is expected to
// set IsDynamic / IsProtected / Redirect as appropriate.
func readPageRecord(arg Value, name string) (VPage, error) {
	rec, ok := arg.(VRecord)
	if !ok {
		return VPage{}, fmt.Errorf("%s: expected record argument (got %T)", name, arg)
	}
	pathV, ok := rec.Fields["path"].(VString)
	if !ok {
		return VPage{}, fmt.Errorf("%s: missing or non-String `path` field", name)
	}
	initFn, ok := rec.Fields["init"]
	if !ok {
		return VPage{}, fmt.Errorf("%s: missing `init` field", name)
	}
	updateFn, ok := rec.Fields["update"]
	if !ok {
		return VPage{}, fmt.Errorf("%s: missing `update` field", name)
	}
	viewFn, ok := rec.Fields["view"]
	if !ok {
		return VPage{}, fmt.Errorf("%s: missing `view` field", name)
	}
	title := ""
	if t, ok := rec.Fields["title"].(VString); ok {
		title = t.V
	}
	return VPage{
		Path:     pathV.V,
		InitFn:   initFn,
		UpdateFn: updateFn,
		ViewFn:   viewFn,
		Title:    title,
	}, nil
}

// appBuiltins exposes the page / app builders.
//
//	Page.create  : { path, title, init, update, view } -> Page
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
		// Page.create takes a record { path, title?, init, update, view }.
		// `title` is optional — when omitted the browser-tab title is left
		// to whatever the host HTML set up.
		"pageCreate": nativeFn(1, func(args []Value) (Value, error) {
			rec, ok := args[0].(VRecord)
			if !ok {
				return nil, fmt.Errorf("Page.create: expected record argument (got %T)", args[0])
			}
			pathV, ok := rec.Fields["path"].(VString)
			if !ok {
				return nil, fmt.Errorf("Page.create: missing or non-String `path` field")
			}
			initFn, ok := rec.Fields["init"]
			if !ok {
				return nil, fmt.Errorf("Page.create: missing `init` field")
			}
			updateFn, ok := rec.Fields["update"]
			if !ok {
				return nil, fmt.Errorf("Page.create: missing `update` field")
			}
			viewFn, ok := rec.Fields["view"]
			if !ok {
				return nil, fmt.Errorf("Page.create: missing `view` field")
			}
			title := ""
			if t, ok := rec.Fields["title"].(VString); ok {
				title = t.V
			}
			return VPage{
				Path:     pathV.V,
				InitFn:   initFn,
				UpdateFn: updateFn,
				ViewFn:   viewFn,
				Title:    title,
			}, nil
		}),

		// Page.protected mirrors Page.create plus a `redirect` field
		// and User-aware handler signatures. Server-side we only need
		// the static metadata (path/title/redirect/origin) — the JS
		// runtime drives the Auth.me bootstrap and the User threading.
		"pageProtected": nativeFn(1, func(args []Value) (Value, error) {
			rec, ok := args[0].(VRecord)
			if !ok {
				return nil, fmt.Errorf("Page.protected: expected record argument (got %T)", args[0])
			}
			pathV, ok := rec.Fields["path"].(VString)
			if !ok {
				return nil, fmt.Errorf("Page.protected: missing or non-String `path` field")
			}
			initFn, ok := rec.Fields["init"]
			if !ok {
				return nil, fmt.Errorf("Page.protected: missing `init` field")
			}
			updateFn, ok := rec.Fields["update"]
			if !ok {
				return nil, fmt.Errorf("Page.protected: missing `update` field")
			}
			viewFn, ok := rec.Fields["view"]
			if !ok {
				return nil, fmt.Errorf("Page.protected: missing `view` field")
			}
			title := ""
			if t, ok := rec.Fields["title"].(VString); ok {
				title = t.V
			}
			// Marker — empty `Redirect` means "use Auth.config.signInPage".
			// The browser dispatcher resolves it at render time. The
			// non-empty case stays open as a future per-page override
			// (would need a `signInPage : Page` field here too).
			return VPage{
				Path:        pathV.V,
				InitFn:      initFn,
				UpdateFn:    updateFn,
				ViewFn:      viewFn,
				Title:       title,
				Redirect:    "",
				IsProtected: true,
			}, nil
		}),

		// Page.adminProtected mirrors Page.protected but is gated by the
		// framework admin session (mar.json["admins"]) rather than the
		// app's user auth. Server-side it's just metadata; the JS runtime
		// drives the admin-session bootstrap + redirect to the admin
		// sign-in page.
		"pageAdminProtected": nativeFn(1, func(args []Value) (Value, error) {
			page, err := readPageRecord(args[0], "Page.adminProtected")
			if err != nil {
				return nil, err
			}
			page.IsProtected = true
			page.IsAdmin = true
			return page, nil
		}),

		// Page.dynamic — pattern path with typed `{name:Type}` segments.
		// Same fields as Page.create; the path string gets parsed into
		// the typed segments the JS / iOS runtimes need to match URLs
		// and decode params. Server-side we only validate that the
		// pattern is well-formed — the parsed Pattern is shipped to
		// the client via the bundle JSON.
		"pageDynamic": nativeFn(1, func(args []Value) (Value, error) {
			page, err := readPageRecord(args[0], "Page.dynamic")
			if err != nil {
				return nil, err
			}
			parsed, err := ParsePathPattern(page.Path)
			if err != nil {
				return nil, fmt.Errorf("Page.dynamic: %w", err)
			}
			page.IsDynamic = true
			page.PathPattern = parsed.Segments
			return page, nil
		}),

		// Page.dynamicProtected — pattern path + auth gate. Combines
		// IsDynamic and IsProtected; client runtime threads BOTH
		// User and Params (in that order) through the handlers.
		"pageDynamicProtected": nativeFn(1, func(args []Value) (Value, error) {
			page, err := readPageRecord(args[0], "Page.dynamicProtected")
			if err != nil {
				return nil, err
			}
			parsed, err := ParsePathPattern(page.Path)
			if err != nil {
				return nil, fmt.Errorf("Page.dynamicProtected: %w", err)
			}
			page.IsDynamic = true
			page.IsProtected = true
			page.PathPattern = parsed.Segments
			return page, nil
		}),

		// Page.dynamicAdminProtected — pattern path + admin gate. Like
		// pageDynamicProtected but threads AdminSession (not User) + Params.
		"pageDynamicAdminProtected": nativeFn(1, func(args []Value) (Value, error) {
			page, err := readPageRecord(args[0], "Page.dynamicAdminProtected")
			if err != nil {
				return nil, err
			}
			parsed, err := ParsePathPattern(page.Path)
			if err != nil {
				return nil, fmt.Errorf("Page.dynamicAdminProtected: %w", err)
			}
			page.IsDynamic = true
			page.IsProtected = true
			page.IsAdmin = true
			page.PathPattern = parsed.Segments
			return page, nil
		}),

		// Nav.* are browser-only effects. Server-side they evaluate
		// but their Run errors out — same shape as Service.call.
		"navPush": nativeFn(1, func(args []Value) (Value, error) {
			return VEffect{
				Tag: "navPush",
				Run: func() (Value, error) {
					return nil, fmt.Errorf("Nav.push is only available in the browser runtime")
				},
			}, nil
		}),
		"navReplace": nativeFn(1, func(args []Value) (Value, error) {
			return VEffect{
				Tag: "navReplace",
				Run: func() (Value, error) {
					return nil, fmt.Errorf("Nav.replace is only available in the browser runtime")
				},
			}, nil
		}),

		// Auth.completeSignIn is the post-Auth.verifyCode helper that reads
		// the captured `?next=` and navigates back to the origin. Pure
		// browser-side concern — server evaluation shouldn't reach it.
		"authCompleteSignIn": VEffect{
			Tag: "authCompleteSignIn",
			Run: func() (Value, error) {
				return nil, fmt.Errorf("Auth.completeSignIn is only available in the browser runtime")
			},
		},

		// Nav.pushTo / Nav.replaceTo: typed alternatives that take a
		// `Path r` (string at runtime) and a record of params. The
		// effect is a no-op server-side; the browser runtime overrides
		// it with the actual history.pushState / replaceState wiring.
		// We pre-render the URL here so the runtime check runs on the
		// server too — a shape mismatch (missing field, wrong type)
		// fails fast in `mar build` rather than waiting for the click.
		"navPushTo": nativeFn(2, func(args []Value) (Value, error) {
			_, err := buildPathURL(args[0], args[1], "Nav.pushTo")
			if err != nil {
				return nil, err
			}
			return VEffect{
				Tag: "navPushTo",
				Run: func() (Value, error) {
					return nil, fmt.Errorf("Nav.pushTo is only available in the browser runtime")
				},
			}, nil
		}),
		"navReplaceTo": nativeFn(2, func(args []Value) (Value, error) {
			_, err := buildPathURL(args[0], args[1], "Nav.replaceTo")
			if err != nil {
				return nil, err
			}
			return VEffect{
				Tag: "navReplaceTo",
				Run: func() (Value, error) {
					return nil, fmt.Errorf("Nav.replaceTo is only available in the browser runtime")
				},
			}, nil
		}),

		// linkTo : Path r -> r -> String
		// Pure URL builder. Same shape as Nav.pushTo's argument
		// processing minus the Effect wrapping — meant for `href`
		// attributes on anchor tags. Server + browser + iOS all use
		// the same logic; no runtime override needed.
		"linkTo": nativeFn(2, func(args []Value) (Value, error) {
			url, err := buildPathURL(args[0], args[1], "linkTo")
			if err != nil {
				return nil, err
			}
			return VString{V: url}, nil
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
