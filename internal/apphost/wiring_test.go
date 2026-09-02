package apphost

import (
	"strings"
	"testing"

	"mar/internal/runtime"
)

// A declaration that names something the program never registers is a fact
// about the registered program, not about any request. Each of these was
// found by a user watching a spinner.

func protectedPage(path, redirect string) runtime.VPage {
	return runtime.VPage{Path: path, Redirect: redirect, IsProtected: true}
}

func TestProtectedPageWithNoAuthRefusesTheBoot(t *testing.T) {
	// The app boots, serves, and the page waits forever: the question "who
	// is signed in?" has nobody to answer it. The diagnosis only ever
	// reached the browser console.
	err := CheckWiring(nil, []runtime.VPage{protectedPage("/inbox", "/sign-in")}, nil, "")
	if err == nil {
		t.Fatal("a protected page with no Auth.config can never finish loading")
	}
	for _, want := range []string{"/inbox", "Auth.config"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message must mention %q, got: %v", want, err)
		}
	}
}

func TestSignInPageThatNoPageServesRefusesTheBoot(t *testing.T) {
	// Redirect to a path with no page, which redirects again: the browser
	// dies with a stack overflow.
	auth := &runtime.VAuth{SignInPath: "/sign-in"}
	err := CheckWiring(nil, []runtime.VPage{protectedPage("/inbox", "")}, auth, "")
	if err == nil {
		t.Fatal("a sign-in path no page serves is an infinite redirect")
	}
	if !strings.Contains(err.Error(), "/sign-in") {
		t.Errorf("the message must name the path, got: %v", err)
	}
}

func TestAnAppWithNoProtectedPageIsNotJudgedOnItsSignInPath(t *testing.T) {
	// `Auth.config` fills SignInPath in from a page value, and examples with
	// auth on their SERVICES have no protected page at all. Nothing ever
	// redirects, so an unserved path there is unreachable, not wrong.
	// Two shipped examples were refused by the first version of this check.
	auth := &runtime.VAuth{SignInPath: "/sign-in"}
	if err := CheckWiring(nil, []runtime.VPage{page("/")}, auth, ""); err != nil {
		t.Fatalf("no page redirects anywhere in this app: %v", err)
	}
}

func TestADynamicPageServesTheRedirectTarget(t *testing.T) {
	// A redirect target is a literal path; the page that answers it may be
	// declared as a pattern.
	auth := &runtime.VAuth{SignInPath: "/sign-in"}
	pages := []runtime.VPage{page("/{slug:String}"), protectedPage("/inbox", "")}
	if err := CheckWiring(nil, pages, auth, ""); err != nil {
		t.Fatalf("`/{slug:String}` answers `/sign-in`: %v", err)
	}
}

func TestSignInPageThatExistsIsFine(t *testing.T) {
	auth := &runtime.VAuth{SignInPath: "/sign-in"}
	if err := CheckWiring(nil, []runtime.VPage{page("/sign-in"), protectedPage("/inbox", "/sign-in")}, auth, ""); err != nil {
		t.Fatalf("this is the ordinary shape of every auth example: %v", err)
	}
}

func TestProtectedRedirectToNowhereRefusesTheBoot(t *testing.T) {
	auth := &runtime.VAuth{SignInPath: "/sign-in"}
	err := CheckWiring(nil, []runtime.VPage{
		page("/sign-in"),
		protectedPage("/inbox", "/login"), // not the configured sign-in page
	}, auth, "")
	if err == nil {
		t.Fatal("a redirect target no page serves loops until the browser gives up")
	}
	if !strings.Contains(err.Error(), "/login") {
		t.Errorf("the message must name the target, got: %v", err)
	}
}

func TestRequireRoleWithNoRoleFieldRefusesTheBoot(t *testing.T) {
	// 500 on every request, from a mistake that is pure configuration.
	auth := &runtime.VAuth{SignInPath: "/sign-in"} // Role is nil
	gated := svc("POST", "/api/ban")
	gated.Service.RequireRole = runtime.VString{V: "admin"}
	err := CheckWiring([]runtime.VExposedService{gated}, []runtime.VPage{page("/sign-in")}, auth, "")
	if err == nil {
		t.Fatal("a role requirement with no role to compare against fails every request")
	}
	for _, want := range []string{"/api/ban", "role"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message must mention %q, got: %v", want, err)
		}
	}
}

func TestRequireRoleWithARoleFieldIsFine(t *testing.T) {
	auth := &runtime.VAuth{SignInPath: "/sign-in", Role: runtime.VString{V: "role"}}
	gated := svc("POST", "/api/ban")
	gated.Service.RequireRole = runtime.VString{V: "admin"}
	if err := CheckWiring([]runtime.VExposedService{gated}, []runtime.VPage{page("/sign-in")}, auth, ""); err != nil {
		t.Fatalf("a configured role is the whole point of requireRole: %v", err)
	}
}

func TestBackendOnlyAppIsUntouched(t *testing.T) {
	// No pages at all: nothing here is about pages, and a service-only app
	// must not be refused for lacking them.
	if err := CheckWiring([]runtime.VExposedService{svc("GET", "/api/x")}, nil, nil, ""); err != nil {
		t.Fatalf("a backend-only app has no pages to wire: %v", err)
	}
}

// The web resolves a URL by literal match, then by pattern, then by falling
// back to the first declared page. With no static page there is nothing safe
// to fall back to: it used to take the first DYNAMIC page and hand it
// `params = null`, and its `view` reads the params the URL was supposed to
// carry. Measured in a browser, on an app whose only page was
// `/note/{id:Int}`: every non-matching URL — `/` included — painted
//
//	view failed
//	Cannot read properties of null (reading 'k')
//
// iOS never had this. It refuses to fall back and shows "Page not found", so
// the same program was a crash screen in the browser and a tidy message on
// the phone.

func TestAnAppWhereEveryPageTakesAParameterIsRefused(t *testing.T) {
	err := CheckLandingPage([]runtime.VPage{page("/note/{id:Int}")})
	if err == nil {
		t.Fatal("`/` matches no page, and that is the app's own front door")
	}
	for _, want := range []string{"/note/{id:Int}", "`/`", "nowhere to land"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message must mention %q, got: %v", want, err)
		}
	}
}

func TestOneStaticPageIsEnoughToLandOn(t *testing.T) {
	// It does not have to be `/`: any fixed path is what the fallback takes.
	for _, static := range []string{"/", "/home", "/notes"} {
		pages := []runtime.VPage{page(static), page("/note/{id:Int}")}
		if err := CheckLandingPage(pages); err != nil {
			t.Errorf("`%s` is where a stray URL lands: %v", static, err)
		}
	}
}

func TestDeclarationOrderDoesNotMatter(t *testing.T) {
	// The dynamic page first is the ordinary way to write it.
	pages := []runtime.VPage{page("/note/{id:Int}"), page("/")}
	if err := CheckLandingPage(pages); err != nil {
		t.Fatalf("there is a static page: %v", err)
	}
}

func TestABackendOnlyAppHasNoPagesToJudge(t *testing.T) {
	if err := CheckLandingPage(nil); err != nil {
		t.Fatalf("a backend serves no pages: %v", err)
	}
}

// `Page.adminProtected` sets IsProtected AND IsAdmin, and it is gated by the
// framework admin session rather than by `Auth.config`. Judging it as an
// ordinary protected page told developers to add an `Auth.config`, which
// would not have helped: the two gates are different surfaces, and
// `App.frontend` has no server to serve either.

func adminPage(path string) runtime.VPage {
	return runtime.VPage{Path: path, IsProtected: true, IsAdmin: true}
}

func TestAnAdminPageWithNoPanelToPassIsRefused(t *testing.T) {
	err := CheckWiring(nil, []runtime.VPage{page("/"), adminPage("/panel")}, nil, "`App.frontend` has no server to serve it")
	if err == nil {
		t.Fatal("nobody can ever pass a gate this app does not mount")
	}
	for _, want := range []string{"/panel", "admin-protected", "/_mar/admin", "has no server"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message must mention %q, got: %v", want, err)
		}
	}
	// And it must not send them after an `Auth.config`, which is the other
	// gate entirely.
	if strings.Contains(err.Error(), "this app has no `Auth.config`") {
		t.Error("that is the advice for Page.protected, and it would not help here")
	}
}

func TestAnAdminPageIsFineWhereThePanelIsMounted(t *testing.T) {
	if err := CheckWiring(nil, []runtime.VPage{page("/"), adminPage("/panel")}, nil, ""); err != nil {
		t.Fatalf("a fullstack app serves /_mar/admin: %v", err)
	}
}

func TestAnAdminPageIsNotJudgedAgainstAuthConfig(t *testing.T) {
	// An app can have admin pages and no user auth at all. The old check
	// refused that, because Page.adminProtected sets IsProtected too.
	if err := CheckWiring(nil, []runtime.VPage{page("/"), adminPage("/panel")}, nil, ""); err != nil {
		t.Fatalf("admin pages do not need `Auth.config`: %v", err)
	}
}

func TestTheAdminAdviceFollowsTheReasonThereIsNoPanel(t *testing.T) {
	// A phone build registers itself as `App.fullstack` and still has no
	// admin panel: the panel has no iOS app at all. Telling that developer
	// to "use App.fullstack" is telling them to do what they already did.
	err := CheckWiring(nil, []runtime.VPage{page("/"), adminPage("/panel")}, nil,
		"the admin panel has no iOS app")
	if err == nil {
		t.Fatal("the phone can never show this page")
	}
	if !strings.Contains(err.Error(), "no iOS app") {
		t.Errorf("the message must give the real reason, got: %v", err)
	}
	if strings.Contains(err.Error(), "Use `App.fullstack`") {
		t.Error("that is the advice for App.frontend, and it is what they already did")
	}
	if !strings.Contains(err.Error(), "out of the phone build") {
		t.Errorf("the advice must be actionable here, got: %v", err)
	}
}
