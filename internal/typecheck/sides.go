package typecheck

import "strings"

// Which side of a Mar app a builtin runs on.
//
// The side cannot be read off the type: `Time.now` and `Repo.all` both produce
// a `Task`, and they sit on different sides. It cannot be read off the module
// name either, because `Auth` and `Service` are single ideas with parts on
// different sides. So it is recorded per name, here, next to nothing else: one
// table, consulted by the side check (side_check.go), by the reference
// generator, and by the runtime-coverage exemption below.
//
// See docs/proposals/side-checking.md for the reasoning, and
// docs/proposals/where-code-runs.md for the measurements behind `Task` and
// `Http`.
type Side uint8

const (
	// SideBoth runs anywhere. The majority of the standard library, and the
	// default for a name with no dot (the bare globals).
	SideBoth Side = iota

	// SideFrontend runs in the browser AND on iOS. Never one without the
	// other: a name earns this only when both clients implement it, so a
	// page that compiles is a page that runs on the phone too.
	//
	// Before adding a `SideFrontend` name, check it against ADR-0023: a
	// function that answers a question about the machine: viewport size,
	// pointers, held keys, colour scheme, must be a `Sub`, never a getter
	// that returns the value. A bare getter is reachable from a service
	// handler, typechecks there, and fails at runtime on a server with no
	// window. `Device.touchOnly` is a pure `Device -> Bool` and is safe only
	// because the `Device` it needs comes from `Device.watch`.
	//
	// This is the moment that rule gets enforced: TestEveryBuiltinHasASide
	// fails the build until a new builtin is classified, and classifying it
	// is when the shape becomes visible.
	SideFrontend

	// SideBackend runs on the server.
	SideBackend

	// SideEntry is read at build time to split the program, and runs on no
	// side. `App.fullstack` is not server code; it is the seam that says
	// which pages and which services exist.
	SideEntry
)

func (s Side) String() string {
	switch s {
	case SideFrontend:
		return "frontend"
	case SideBackend:
		return "backend"
	case SideEntry:
		return "entry point"
	default:
		return "both"
	}
}

// sideByName holds the names whose side disagrees with their module's. These
// are the two modules that are one idea with parts on different sides; keeping
// them whole and splitting them here was the decision (see the proposal's
// "Not a rename").
var sideByName = map[string]Side{
	// Auth: the client asks for a code and verifies it; the server
	// configures the feature and guards routes.
	"Auth.requestCode":    SideFrontend,
	"Auth.verifyCode":     SideFrontend,
	"Auth.me":             SideFrontend,
	"Auth.logout":         SideFrontend,
	"Auth.completeSignIn": SideFrontend,
	"Auth.config":         SideBackend,
	"Auth.protect":        SideBackend,
	"Auth.authorize":      SideBackend,
	"Auth.requireRole":    SideBackend,
	"Auth.requireOwner":   SideBackend,

	// Service: one declaration, two halves. `declare` is deliberately
	// shared, that is the whole point of it, which is why this module
	// cannot be cut in two without either duplicating `declare` or giving
	// it a dishonest home.
	"Service.declare":   SideBoth,
	"Service.call":      SideFrontend,
	"Service.implement": SideBackend,

	// App: the module is the build-time seam (App.fullstack and friends split
	// the program in two), but App.shared is not part of that seam: it builds
	// a client-side store and is called from an ordinary frontend module,
	// beside the Service.call that fills it. Filing it as build-time would
	// document it as something it isn't.
	"App.shared": SideFrontend,
}

// sideByModule covers every other qualified builtin, by its module. Every
// module in BaseEnv appears here; TestEveryBuiltinHasASide fails the build if
// one is added without a side, which is what keeps this table from drifting the
// way its hand-maintained predecessor did.
var sideByModule = map[string]Side{
	// Pure data and computation.
	"List":    SideBoth,
	"String":  SideBoth,
	"Char":    SideBoth,
	"Maybe":   SideBoth,
	"Result":  SideBoth,
	"Tuple":   SideBoth,
	"Dict":    SideBoth,
	"Set":     SideBoth,
	"Decimal": SideBoth,
	"Math":    SideBoth,
	"Time":    SideBoth,
	"Random":  SideBoth,
	"JSON":    SideBoth,
	"Task":    SideBoth, // measured both-sided; see the proposal

	// Screens, input, sound, and the client's own effects.
	"Page":     SideFrontend,
	"Nav":      SideFrontend,
	"UI":       SideFrontend,
	"Canvas":   SideFrontend,
	"Sound":    SideFrontend,
	"Keyboard": SideFrontend,
	"Gamepad":  SideFrontend,
	"Device":   SideFrontend,
	"Cmd":      SideFrontend,
	"Sub":      SideFrontend,
	"Http":     SideFrontend, // decided; a Mar backend makes no outbound calls

	// Persistence, schema, and the admin panel's own services.
	"Repo":      SideBackend,
	"Entity":    SideBackend,
	"Db":        SideBackend,
	"Server":    SideBackend,
	"Mar.Admin": SideBackend,

	// Split by name above.
	"Auth":    SideBoth,
	"Service": SideBoth,

	// The build-time seam.
	"App": SideEntry,
}

// SideOf reports which side `name` runs on. The second result is false for a
// name this table does not know, which for a qualified name means the module is
// missing from `sideByModule`: a bug the staleness test catches.
//
// A name with no dot is a bare global (`not`, `always`, `max`, `clamp`, …).
// Those are all pure and all run anywhere.
func SideOf(name string) (Side, bool) {
	if s, ok := sideByName[name]; ok {
		return s, true
	}
	i := strings.LastIndex(name, ".")
	if i < 0 {
		return SideBoth, true
	}
	s, ok := sideByModule[name[:i]]
	return s, ok
}

// SideOfModule reports the side of a module as a whole: where its functions run
// unless a name overrides it in `sideByName`. `Auth` and `Service` answer
// SideBoth here, which is the honest answer for them: they are the two modules
// with parts on each side, and the per-name table is what says which is which.
//
// False for a module this table does not know, including `Basics`, which is the
// reference's shelf for the bare globals rather than a real module.
func SideOfModule(mod string) (Side, bool) {
	s, ok := sideByModule[mod]
	return s, ok
}

// IsBackendOnlyBuiltin reports whether the client runtimes (JS and Swift) may
// skip implementing `name`.
//
// This is DERIVED, not a second list. It used to be maintained by hand next to
// the side question, and the two drifted: `App.frontend` ended up marked
// server-only because no client runtime implements it, which is true and yet
// says the wrong thing about a name the user writes in the `Main.mar` of a
// frontend app. `SideEntry` exists to keep those two facts separate while
// letting one follow from the other.
func IsBackendOnlyBuiltin(name string) bool {
	s, ok := SideOf(name)
	if !ok {
		return false
	}
	return s == SideBackend || s == SideEntry
}
