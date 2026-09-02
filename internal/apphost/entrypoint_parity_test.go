package apphost

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"mar/internal/jsserve"
	"mar/internal/runtime"
)

// Three times in one pass of this work, a check turned out to be live on one
// entry point and absent on another: the wire request guard (production yes,
// `mar dev` no), the boot checks (`mar dev` yes, `mar build --target ios` no),
// and the page checks (`App.fullstack` yes, `App.frontend` no). Each was found
// by accident.
//
// This is the matrix, asserted by behaviour rather than by reading: for each
// entry point, a program that violates a check it should run is refused, and
// one that violates a check that does not apply to it is not.

// runEntry drives one App.* builtin with a registered program and returns what
// it said.
func runEntry(t *testing.T, who string, pages []runtime.Value, services runtime.Value) error {
	t.Helper()
	runtime.ResetForReload()
	var notes bytes.Buffer
	routeNoteWriter = &notes
	t.Cleanup(func() { routeNoteWriter = os.Stderr })

	lp := &jsserve.LiveProgram{}
	var fn runtime.Value
	var arg runtime.Value
	switch who {
	case "App.frontend":
		fn = MakeFrontendBuiltin(nil, 0, lp)
		arg = runtime.VList{Elements: pages}
	case "App.backend":
		fn = MakeBackendBuiltin(0, lp)
		arg = runtime.VRecord{Fields: map[string]runtime.Value{"services": services}, Order: []string{"services"}}
	case "App.fullstack":
		fn = MakeFullstackBuiltin(runtime.NewEnv(), nil, 0, lp)
		arg = runtime.VRecord{
			Fields: map[string]runtime.Value{"pages": runtime.VList{Elements: pages}, "services": services},
			Order:  []string{"pages", "services"},
		}
	default:
		t.Fatalf("unknown entry point %q", who)
	}
	f, ok := fn.(runtime.VFn)
	if !ok || f.Native == nil {
		t.Fatalf("%s is not a native builtin", who)
	}
	_, err := f.Native([]runtime.Value{arg})
	return err
}

func pageValues(paths ...string) []runtime.Value {
	out := make([]runtime.Value, 0, len(paths))
	for _, p := range paths {
		out = append(out, page(p))
	}
	return out
}

func serviceList(exposed ...runtime.VExposedService) runtime.Value {
	out := make([]runtime.Value, 0, len(exposed))
	for _, es := range exposed {
		out = append(out, es)
	}
	return runtime.VList{Elements: out}
}

func TestEveryEntryPointThatServesPagesChecksThem(t *testing.T) {
	// Two pages on one path: the browser keeps the last, the phone keeps the
	// first, so the same program is two apps.
	dupes := pageValues("/", "/")
	for _, who := range []string{"App.frontend", "App.fullstack"} {
		err := runEntry(t, who, dupes, serviceList())
		if err == nil {
			t.Errorf("%s: two pages on `/` and it said nothing", who)
			continue
		}
		if !strings.Contains(err.Error(), "two pages are declared") {
			t.Errorf("%s: got %v", who, err)
		}
	}
}

func TestEveryEntryPointThatServesPagesNeedsSomewhereToLand(t *testing.T) {
	onlyDynamic := pageValues("/note/{id:Int}")
	for _, who := range []string{"App.frontend", "App.fullstack"} {
		err := runEntry(t, who, onlyDynamic, serviceList())
		if err == nil {
			t.Errorf("%s: `/` matches nothing here and it said nothing", who)
			continue
		}
		if !strings.Contains(err.Error(), "nowhere to land") {
			t.Errorf("%s: got %v", who, err)
		}
	}
}

func TestEveryEntryPointThatServesServicesChecksThem(t *testing.T) {
	// Two services on one verb and path: the first wins, silently.
	dupes := serviceList(svc("GET", "/api/x"), svc("GET", "/api/x"))
	for _, who := range []string{"App.backend", "App.fullstack"} {
		pages := []runtime.Value(nil)
		if who == "App.fullstack" {
			pages = pageValues("/")
		}
		err := runEntry(t, who, pages, dupes)
		if err == nil {
			t.Errorf("%s: two services on `GET /api/x` and it said nothing", who)
			continue
		}
		if !strings.Contains(err.Error(), "two services are declared") {
			t.Errorf("%s: got %v", who, err)
		}
	}
}

func TestAServiceUnderAFrameworkPathIsRefusedWhereverItIsRegistered(t *testing.T) {
	reserved := serviceList(svc("GET", "/_mar/x"))
	for _, who := range []string{"App.backend", "App.fullstack"} {
		pages := []runtime.Value(nil)
		if who == "App.fullstack" {
			pages = pageValues("/")
		}
		err := runEntry(t, who, pages, reserved)
		if err == nil {
			t.Errorf("%s: a service under `/_mar/` never receives a request", who)
			continue
		}
		if !strings.Contains(err.Error(), "the framework serves itself") {
			t.Errorf("%s: got %v", who, err)
		}
	}
}

func TestAnOrdinaryProgramPassesEveryCheckOnEveryEntryPoint(t *testing.T) {
	// The floor under the four above: none of them is refusing everything.
	//
	// It asserts what it can from here. A synthetic page has no provenance,
	// so the bundle step past the checks says so — that is the harness, not
	// a check, and the point is that none of the CHECKS spoke.
	checkMessages := []string{
		"two pages are declared", "two services are declared",
		"nowhere to land", "the framework serves itself",
		"Auth.config", "nothing mounts",
	}
	for _, who := range []string{"App.frontend", "App.backend", "App.fullstack"} {
		pages := pageValues("/")
		if who == "App.backend" {
			pages = nil
		}
		err := runEntry(t, who, pages, serviceList(svc("GET", "/api/x")))
		if err == nil {
			continue
		}
		for _, msg := range checkMessages {
			if strings.Contains(err.Error(), msg) {
				t.Errorf("%s refused an ordinary program: %v", who, err)
			}
		}
	}
}
