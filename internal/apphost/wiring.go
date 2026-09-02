package apphost

import (
	"fmt"
	"strings"

	"mar/internal/runtime"
)

// A declaration that names something the program never registers is decided
// entirely by the registered program: the pages, the services, and the auth
// configuration `main` installs. Nothing about it depends on a request. So it
// belongs at boot — and every one of these was discovered by a user instead.
//
// What was measured:
//
//   - `Page.protected` in an app with no `Auth.config` at all: the app boots,
//     serves, and the page hangs on "Loading…" forever. The diagnosis only
//     ever reaches the browser console.
//   - `Auth.config`'s `signInPage` is not one of the registered pages: every
//     protected page becomes a redirect loop, and the browser eventually dies
//     with "Maximum call stack size exceeded".
//   - `Auth.requireRole` on a service while `Auth.config` declares no role
//     field: 500 on every request to it, from a mistake that is pure
//     configuration.
//   - two `Auth.config` values in one program: the last one evaluated silently
//     becomes the app's auth, and the other is simply gone.
//
// Each of these has one reading and it is not the one the program states, so
// they refuse the boot rather than warn. Every example was booted with these
// on; none is refused.

// CheckWiring compares what the program REFERS to against what it REGISTERED.
// `noAdminPanel` is empty when this build serves `/_mar/admin`, and otherwise
// says WHY it does not — the reason differs, and so does the advice.
func CheckWiring(services []runtime.VExposedService, pages []runtime.VPage, auth *runtime.VAuth, noAdminPanel string) error {
	served := map[string]bool{}
	for _, p := range pages {
		served[p.Path] = true
	}

	// `Page.adminProtected` sets IsProtected AND IsAdmin, and it is gated by
	// the framework admin session rather than by `Auth.config`. Judging it as
	// an ordinary protected page told developers to add an `Auth.config`,
	// which would not have helped: the two gates are different surfaces.
	if admin := firstAdminPage(pages); admin != nil && noAdminPanel != "" && !served["/_mar/admin"] {
		return fmt.Errorf(
			"the page on `%s` is admin-protected, and this build has no admin panel: %s.\n"+
				"  An admin page is gated by the framework admin session, issued at "+
				"`/_mar/admin`, so nobody can ever pass the gate here.\n"+
				"  %s",
			admin.Path, noAdminPanel, adminAdvice(noAdminPanel))
	}

	protected := firstUserProtectedPage(pages)

	if protected != nil && auth == nil {
		return fmt.Errorf(
			"the page on `%s` is protected, but this app has no `Auth.config`.\n"+
				"  A protected page asks who is signed in before it renders, and with no auth "+
				"configured the answer never arrives: the page waits forever.\n"+
				"  Add an `Auth.config`, or use `Page.create` for a page anyone may see",
			protected.Path)
	}

	// Where a signed-out visitor is actually sent. `signInPage` alone proves
	// nothing: `Auth.config` fills it in from a page value, and an app whose
	// pages are all public never redirects anywhere, so an unserved path
	// there is unreachable rather than wrong. Only a page that redirects can
	// make it matter — which is why this walks the protected pages instead of
	// reading the configuration.
	for _, p := range pages {
		if !p.IsProtected {
			continue
		}
		target := p.Redirect
		from := "the protected page on `" + p.Path + "` redirects to"
		if target == "" && auth != nil {
			target = auth.SignInPath
			from = "`Auth.config` sends signed-out visitors from `" + p.Path + "` to"
		}
		if target == "" || servedBy(pages, target) {
			continue
		}
		return fmt.Errorf(
			"%s `%s`, and no page serves that path.\n"+
				"  A visitor who is not signed in is sent somewhere that does not exist, "+
				"so the redirect repeats until the browser stops with a stack overflow.\n"+
				"  Point it at a page this app registers",
			from, target)
	}

	if err := runtime.CheckAuthIdentity(auth); err != nil {
		return err
	}

	if err := runtime.CheckAuthRoles(auth, services); err != nil {
		return err
	}

	if err := runtime.CheckAuthEmailBody(auth); err != nil {
		return err
	}

	if err := CheckLandingPage(pages); err != nil {
		return err
	}

	if auth != nil && auth.Role == nil {
		for _, es := range services {
			if es.Service.RequireRole == nil {
				continue
			}
			return fmt.Errorf(
				"the service on `%s %s` requires a role, and `Auth.config` declares no `role` field.\n"+
					"  There is nothing to compare the requirement against, so every request to it "+
					"fails.\n"+
					"  Add `role` to `Auth.config`, or drop the requirement",
				es.Service.Verb, es.Service.Path)
		}
	}
	return nil
}

// firstUserProtectedPage skips the admin ones: they set IsProtected too, and
// the gate they wait on is not `Auth.config`.
func firstUserProtectedPage(pages []runtime.VPage) *runtime.VPage {
	for i := range pages {
		if pages[i].IsProtected && !pages[i].IsAdmin {
			return &pages[i]
		}
	}
	return nil
}

// adminAdvice says what to do about it, which depends on why there is no
// panel: a web app can gain one, a phone cannot.
func adminAdvice(reason string) string {
	if strings.Contains(reason, "iOS") {
		return "Keep the admin screens out of the phone build, or use `Page.protected` " +
			"with an `Auth.config` if this is the app's own sign-in"
	}
	return "Use `App.fullstack`, or `Page.protected` with an `Auth.config` if this is " +
		"the app's own sign-in"
}

func firstAdminPage(pages []runtime.VPage) *runtime.VPage {
	for i := range pages {
		if pages[i].IsAdmin {
			return &pages[i]
		}
	}
	return nil
}

// servedBy reports whether any registered page answers this path. A dynamic
// page answers a whole family of them, so a literal redirect target is served
// by the pattern that matches it.
func servedBy(pages []runtime.VPage, path string) bool {
	for _, p := range pages {
		if p.Path == path || covers(p.Path, path) {
			return true
		}
	}
	return false
}

// CheckLandingPage refuses an app where no URL that fails to match has
// anywhere to land.
//
// The web resolves a URL by literal match, then by dynamic pattern, then by
// falling back to the first declared page. With no STATIC page there is
// nothing safe to fall back to: the fallback used to take the first dynamic
// page and hand it `params = null`, and its `view` reads the params the URL
// was supposed to carry. Measured, on an app whose only page was
// `/note/{id:Int}`, every non-matching URL — `/` included, the app's own front
// door — painted "view failed: Cannot read properties of null".
//
// iOS never had this: it refuses to fall back and shows "Page not found". So
// the same program was a crash screen in the browser and a tidy message on the
// phone.
//
// Whether an app has a static page is decided by the program, so it is asked
// here rather than by whoever opens the wrong URL first.
func CheckLandingPage(pages []runtime.VPage) error {
	if len(pages) == 0 {
		return nil // a backend-only app serves no pages at all
	}
	var dynamic []string
	for _, p := range pages {
		pattern, err := runtime.ParsePathPattern(p.Path)
		if err != nil {
			return nil // not something to judge on
		}
		if !hasParam(pattern) {
			return nil // a static page: every stray URL lands here
		}
		dynamic = append(dynamic, "`"+p.Path+"`")
	}
	return fmt.Errorf(
		"every page in this app takes a parameter (%s), so a URL that matches none of them "+
			"has nowhere to land.\n"+
			"  `/` is one of those URLs: opening the app at its own address matches no page, and "+
			"the browser paints a failure screen instead of a screen.\n"+
			"  Add a page on a fixed path",
		strings.Join(dynamic, ", "))
}

func hasParam(p runtime.VPath) bool {
	for _, seg := range p.Segments {
		if seg.IsParam {
			return true
		}
	}
	return false
}
