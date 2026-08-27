package apphost

import (
	"fmt"

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
func CheckWiring(services []runtime.VExposedService, pages []runtime.VPage, auth *runtime.VAuth) error {
	served := map[string]bool{}
	for _, p := range pages {
		served[p.Path] = true
	}

	protected := firstProtectedPage(pages)

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

func firstProtectedPage(pages []runtime.VPage) *runtime.VPage {
	for i := range pages {
		if pages[i].IsProtected {
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
