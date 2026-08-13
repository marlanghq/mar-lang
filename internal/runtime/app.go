package runtime

import (
	"fmt"
	"sync"
)

// locale carries the app's declared language (mar.json `locale`) into
// the `App.locale` builtin. A package var rather than a parameter
// because the builtin env is built in a dozen places (dev, build, the
// admin panel, tests) and every one of them would have to thread a
// value it does not care about. Required in the manifest, so the
// fallback only covers callers that never set it, which are tests and
// a bare `mar dev` in a directory with no mar.json.
var (
	localeMu sync.RWMutex
	locale   = "en"
)

// SetLocale records the app's language tag. Called by `mar dev` and by
// the deployed runtime after reading mar.json, before Main.main is
// evaluated.
// An empty tag is ignored so `App.locale` is never the empty string:
// app code branching on it would see a value that matches nothing.
func SetLocale(tag string) {
	if tag == "" {
		return
	}
	localeMu.Lock()
	locale = tag
	localeMu.Unlock()
}

// Locale returns the tag SetLocale installed.
func Locale() string {
	localeMu.RLock()
	defer localeMu.RUnlock()
	return locale
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
	IsAdmin bool // Page.adminProtected: gated by the framework admin
	//             // session (mar.json["admins"]) instead of the app's user
	//             // auth. Implies IsProtected.
	IsSheet bool // Page.sheet: PRESENTED over the page you came from
	//             // instead of replacing it. Orthogonal to the four
	//             // dynamic × protected combinations: the route, the
	//             // history entry and the deep link are unchanged, only
	//             // the presentation differs. A cold load has nothing to
	//             // present over, so it renders full-screen.
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

// VShared is one app-wide client store: the model that outlives navigation,
// plus the update and subscriptions that drive it. Built by App.shared, read
// by Page.withShared, written by Cmd.toShared.
//
// The store itself only ever runs in the browser: this half exists because
// `mar dev` evaluates `main` server-side to learn the ROUTES, and a page built
// by Page.withShared cannot report its path without first being built, which
// needs a model. InitModel is that model, and only that: the init's Cmd is
// deliberately left unrun here, since it is a client command (typically the
// Service.call that fills the store) and running it on the server would fetch
// once per boot for nobody.
type VShared struct {
	InitModel Value
	InitCmd   Value
	UpdateFn  Value
	SubsFn    Value
}

func (VShared) isValue()        {}
func (VShared) Display() string { return "<shared>" }

// readPageRecord pulls the common { path, init, update, view, title? } shape
// out of a record argument. Used by Page.dynamic and Page.dynamicProtected
// : both share the same surface as Page.create, the only difference being
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
//	App.frontend : List Page -> Cmd ()
//	App.backend  : List Route -> Task ()
//	App.fullstack: { api, pages } -> Cmd ()
//
// The default builtins for the App.* server entry points error out when
// evaluated outside of `mar dev`, because they need access to the
// project's module ASTs (to ship as a browser bundle) and mar.json
// (for the listening port). The CLI installs project-aware overrides
// before evaluating Main.main: see cmd/mar/main.go runDev.
func appBuiltins() map[string]Value {
	return map[string]Value{
		// Page.create takes a record { path, title?, init, update, view }.
		// `title` is optional, when omitted the browser-tab title is left
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
		// the static metadata (path/title/redirect/origin): the JS
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
			// Marker: empty `Redirect` means "use Auth.config.signInPage".
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

		// Page.dynamic, pattern path with typed `{name:Type}` segments.
		// Same fields as Page.create; the path string gets parsed into
		// the typed segments the JS / iOS runtimes need to match URLs
		// and decode params. Server-side we only validate that the
		// pattern is well-formed: the parsed Pattern is shipped to
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

		// Page.dynamicProtected: pattern path + auth gate. Combines
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

		// Page.dynamicAdminProtected: pattern path + admin gate. Like
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

		// Page.sheet: presentation, not a new kind of page. Takes a
		// page built by any of the constructors above and flips one
		// flag: navigating to it lays it over the screen you came from
		// instead of replacing it.
		//
		// A decorator rather than a `sheet` field on every constructor's
		// record, because the record shapes are already the four
		// combinations of dynamic × protected; adding presentation there
		// would multiply them again. And a decorator rather than
		// `Page.sheetDynamicProtected`, for the same reason from the
		// other direction.
		"pageSheet": nativeFn(1, func(args []Value) (Value, error) {
			page, ok := args[0].(VPage)
			if !ok {
				return nil, fmt.Errorf("Page.sheet: expected a Page (got %T)", args[0])
			}
			page.IsSheet = true
			return page, nil
		}),

		// App.locale: the language the app declared in mar.json.
		//
		// A value, not a function: it is fixed for the life of the
		// program. Read through Locale() when the env is built, which
		// is after the CLI has installed the manifest's tag.
		"appLocale": VString{V: Locale()},

		// App.shared: the app-wide client store's definition.
		//
		// Server-side this is a carrier, not a store: nothing here runs an
		// update or a subscription. It exists because `mar dev` evaluates
		// `main` to learn the routes, and Page.withShared below needs a model
		// of the right shape to build its page with.
		"appShared": nativeFn(1, func(args []Value) (Value, error) {
			rec, ok := args[0].(VRecord)
			if !ok {
				return nil, fmt.Errorf("App.shared: expected record argument (got %T)", args[0])
			}
			initV, ok := rec.Fields["init"]
			if !ok {
				return nil, fmt.Errorf("App.shared: missing `init` field")
			}
			tup, ok := initV.(VTuple)
			if !ok || len(tup.Members) != 2 {
				return nil, fmt.Errorf("App.shared: `init` must be a (model, Cmd msg) tuple (got %T)", initV)
			}
			return VShared{
				InitModel: tup.Members[0],
				InitCmd:   tup.Members[1],
				UpdateFn:  rec.Fields["update"],
				SubsFn:    rec.Fields["subscriptions"],
			}, nil
		}),

		// Page.withShared: a wrapper over the six page constructors, not a
		// seventh flavor of each.
		//
		// The builder is applied ONCE here, with the store's initial model,
		// which is all the server needs: the page's path, so the route can be
		// registered and the bundle narrowed to the modules it reaches. In
		// the browser the same builder is re-applied on every shared change,
		// which is what makes a page render the live value rather than a
		// snapshot: see runtime.js.
		"pageWithShared": nativeFn(2, func(args []Value) (Value, error) {
			shared, ok := args[0].(VShared)
			if !ok {
				return nil, fmt.Errorf("Page.withShared: expected an App.Shared value (got %T)", args[0])
			}
			built, err := Apply(args[1], shared.InitModel)
			if err != nil {
				return nil, fmt.Errorf("Page.withShared: %w", err)
			}
			page, ok := built.(VPage)
			if !ok {
				return nil, fmt.Errorf("Page.withShared: the builder must return a Page (got %T)", built)
			}
			return page, nil
		}),

		// Cmd.toShared is a browser effect, like every other Cmd. Evaluating
		// it server-side is fine; running it is not.
		"cmdToShared": nativeFn(2, func(args []Value) (Value, error) {
			return VEffect{
				Tag: "cmdToShared",
				Run: func() (Value, error) {
					return nil, fmt.Errorf("Cmd.toShared is only available in the browser runtime")
				},
			}, nil
		}),

		// Nav.* are browser-only effects. Server-side they evaluate
		// but their Run errors out: same shape as Service.call.
		"navPush": nativeFn(1, func(args []Value) (Value, error) {
			return VEffect{
				Tag: "navPush",
				Run: func() (Value, error) {
					return nil, fmt.Errorf("Nav.push is only available in the browser runtime")
				},
			}, nil
		}),
		// Nav.dismiss is a VALUE, not a function: it takes nothing.
		"navDismiss": VEffect{
			Tag: "navDismiss",
			Run: func() (Value, error) {
				return nil, fmt.Errorf("Nav.dismiss is only available in the browser runtime")
			},
		},

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
		// browser-side concern: server evaluation shouldn't reach it.
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
		// server too: a shape mismatch (missing field, wrong type)
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
		// processing minus the Effect wrapping: meant for `href`
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
