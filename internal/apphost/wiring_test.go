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
	err := CheckWiring(nil, []runtime.VPage{protectedPage("/inbox", "/sign-in")}, nil)
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
	err := CheckWiring(nil, []runtime.VPage{protectedPage("/inbox", "")}, auth)
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
	if err := CheckWiring(nil, []runtime.VPage{page("/")}, auth); err != nil {
		t.Fatalf("no page redirects anywhere in this app: %v", err)
	}
}

func TestADynamicPageServesTheRedirectTarget(t *testing.T) {
	// A redirect target is a literal path; the page that answers it may be
	// declared as a pattern.
	auth := &runtime.VAuth{SignInPath: "/sign-in"}
	pages := []runtime.VPage{page("/{slug:String}"), protectedPage("/inbox", "")}
	if err := CheckWiring(nil, pages, auth); err != nil {
		t.Fatalf("`/{slug:String}` answers `/sign-in`: %v", err)
	}
}

func TestSignInPageThatExistsIsFine(t *testing.T) {
	auth := &runtime.VAuth{SignInPath: "/sign-in"}
	if err := CheckWiring(nil, []runtime.VPage{page("/sign-in"), protectedPage("/inbox", "/sign-in")}, auth); err != nil {
		t.Fatalf("this is the ordinary shape of every auth example: %v", err)
	}
}

func TestProtectedRedirectToNowhereRefusesTheBoot(t *testing.T) {
	auth := &runtime.VAuth{SignInPath: "/sign-in"}
	err := CheckWiring(nil, []runtime.VPage{
		page("/sign-in"),
		protectedPage("/inbox", "/login"), // not the configured sign-in page
	}, auth)
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
	err := CheckWiring([]runtime.VExposedService{gated}, []runtime.VPage{page("/sign-in")}, auth)
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
	if err := CheckWiring([]runtime.VExposedService{gated}, []runtime.VPage{page("/sign-in")}, auth); err != nil {
		t.Fatalf("a configured role is the whole point of requireRole: %v", err)
	}
}

func TestBackendOnlyAppIsUntouched(t *testing.T) {
	// No pages at all: nothing here is about pages, and a service-only app
	// must not be refused for lacking them.
	if err := CheckWiring([]runtime.VExposedService{svc("GET", "/api/x")}, nil, nil); err != nil {
		t.Fatalf("a backend-only app has no pages to wire: %v", err)
	}
}
