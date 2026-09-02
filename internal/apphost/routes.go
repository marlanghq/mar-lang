package apphost

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"mar/internal/runtime"
)

// A route table is decided entirely by the program: the services and pages
// `main` registers, and the paths they were declared with. Nothing about it
// depends on a request. So every way a route table can be wrong belongs at
// boot, and until now none of it was checked anywhere.
//
// What was measured, before this pass existed:
//
//   - two services at the same verb and path both mount; the first wins and
//     the second is unreachable, silently. When the losing one is the
//     `Auth.protect`ed half of a pair, a protected route is served to
//     unauthenticated callers.
//   - two pages at the same path both register, and the web keeps the LAST
//     while iOS keeps the FIRST — the same program, two different apps.
//   - a service declared on a framework path (`/_mar/...`, `/_auth/...`) is
//     silently shadowed by the framework's own handler.
//   - a route whose pattern is fully covered by an earlier one is dead for
//     every URL, which examples/bigapp has live in two screens.
//   - a page whose path a GET service swallows has no working deep link:
//     a reload returns JSON instead of the app.
//
// The first three REFUSE the boot: there is no reading of them where the
// program does what it says, and nothing in the examples relies on them.
//
// The last two REPORT instead. They are live in shipped examples, and the
// apps around them work — refusing would turn a partly-working app into one
// that does not start, which is a bigger hammer than the defect. A note names
// both routes so the dead one stops being invisible.

// RouteConflicts is what a boot check found: things that must stop the boot,
// and things the developer should be told about.
type RouteConflicts struct {
	Fatal []error
	Notes []string
}

// reservedPrefixes are the paths the framework serves itself. A service
// declared under one of them never receives a request.
var reservedPrefixes = []string{"/_mar/", "/_auth/"}

// CheckRouteTable compares every registered route with every other one.
func CheckRouteTable(services []runtime.VExposedService, pages []runtime.VPage) RouteConflicts {
	var out RouteConflicts

	// Exact duplicates among services, by verb and path.
	seen := map[string]runtime.VService{}
	for _, es := range services {
		svc := es.Service
		key := svc.Verb + " " + svc.Path
		if prev, dup := seen[key]; dup {
			out.Fatal = append(out.Fatal, fmt.Errorf(
				"two services are declared on `%s %s`, so only the first one can ever run.\n"+
					"  The second is unreachable — and if the two differ in whether they are "+
					"protected, the reachable one may be the open half of the pair.\n"+
					"  Give them different paths",
				svc.Verb, svc.Path))
			_ = prev
			continue
		}
		seen[key] = svc
		for _, prefix := range reservedPrefixes {
			if strings.HasPrefix(svc.Path, prefix) {
				out.Fatal = append(out.Fatal, fmt.Errorf(
					"the service on `%s %s` is declared under `%s`, which the framework serves itself.\n"+
						"  Requests to it never reach this handler.\n"+
						"  Move it out from under `%s`",
					svc.Verb, svc.Path, prefix, prefix))
			}
		}
	}

	// Exact duplicates among pages. The two client runtimes disagree about
	// which one survives, so the same program is two different apps.
	pageSeen := map[string]bool{}
	for _, p := range pages {
		path := p.Path
		if pageSeen[path] {
			out.Fatal = append(out.Fatal, fmt.Errorf(
				"two pages are declared on `%s`, and the two runtimes disagree about which one "+
					"survives: the browser keeps the last, the phone keeps the first.\n"+
					"  Give them different paths",
				path))
			continue
		}
		pageSeen[path] = true
	}

	out.Notes = append(out.Notes, shadowNotes(services, pages)...)
	sort.Strings(out.Notes)
	return out
}

// shadowNotes reports routes that are live but unreachable: a pattern fully
// covered by an earlier one, and a page whose path a GET service takes.
func shadowNotes(services []runtime.VExposedService, pages []runtime.VPage) []string {
	var notes []string
	for i, later := range services {
		for j := 0; j < i; j++ {
			earlier := services[j]
			if earlier.Service.Verb != later.Service.Verb {
				continue
			}
			if earlier.Service.Path == later.Service.Path {
				continue // an exact duplicate is fatal above
			}
			if covers(earlier.Service.Path, later.Service.Path) {
				notes = append(notes, fmt.Sprintf(
					"the service on `%s %s` can never be reached: `%s` is declared earlier and matches every URL it would",
					later.Service.Verb, later.Service.Path, earlier.Service.Path))
			}
		}
	}
	for _, p := range pages {
		for _, es := range services {
			if es.Service.Verb != "GET" {
				continue
			}
			if es.Service.Path != p.Path && !covers(es.Service.Path, p.Path) {
				continue
			}
			notes = append(notes, fmt.Sprintf(
				"the page on `%s` has no working deep link: the service on `GET %s` answers that URL first, so a reload returns JSON",
				p.Path, es.Service.Path))
		}
	}
	return notes
}

// covers reports whether every URL the second pattern matches is also matched
// by the first. A typed parameter matches anything a literal would, so
// `/item/{id:String}` covers `/item/new`, and the literal is dead.
func covers(earlier, later string) bool {
	a, errA := runtime.ParsePathPattern(earlier)
	b, errB := runtime.ParsePathPattern(later)
	if errA != nil || errB != nil {
		return false
	}
	if len(a.Segments) != len(b.Segments) {
		return false
	}
	for i, seg := range a.Segments {
		other := b.Segments[i]
		switch {
		case seg.IsParam && seg.Type == "String":
			// Matches any single segment.
		case seg.IsParam && seg.Type == "Int":
			// Matches a number, so it covers a numeric literal and any
			// other Int param, and nothing else.
			if other.IsParam && other.Type == "Int" {
				continue
			}
			if !other.IsParam && isAllDigits(other.Lit) {
				continue
			}
			return false
		case seg.IsParam:
			// A custom enum matches its own constructor names.
			if other.IsParam && other.Type == seg.Type {
				continue
			}
			return false
		default:
			if other.IsParam || other.Lit != seg.Lit {
				return false
			}
		}
	}
	return true
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// reportRouteTable runs the boot check and turns it into the two things a
// boot can do: refuse, or say something.
//
// The notes go to stderr rather than being swallowed, because a route that
// can never be reached is invisible by construction — nobody files a bug
// about a screen they have not noticed is missing.
// `App.frontend` has no server, so it serves no admin panel. Every other web
// entry point does.
func reportRouteTable(who string, services []runtime.VExposedService, pages []runtime.Value) error {
	noPanel := ""
	if who == "App.frontend" {
		noPanel = "`App.frontend` has no server to serve it"
	}
	return reportRouteTableOn(who, noPanel, services, pages)
}

// reportRouteTableOn is the same for a caller that knows something `who`
// cannot say. A phone build registers itself as `App.fullstack` and still has
// no admin panel: the panel has no iOS app at all.
func reportRouteTableOn(who, noAdminPanel string, services []runtime.VExposedService, pages []runtime.Value) error {
	var ps []runtime.VPage
	for _, pv := range pages {
		if p, ok := pv.(runtime.VPage); ok {
			ps = append(ps, p)
		}
	}
	if err := CheckWiring(services, ps, runtime.CurrentAuth(), noAdminPanel); err != nil {
		return fmt.Errorf("%s: %w", who, err)
	}
	found := CheckRouteTable(services, ps)
	for _, note := range found.Notes {
		fmt.Fprintf(routeNoteWriter, "mar: %s\n", note)
	}
	if len(found.Fatal) > 0 {
		return fmt.Errorf("%s: %w", who, found.Fatal[0])
	}
	return nil
}

// routeNoteWriter is a variable so a test can read what a boot would print.
var routeNoteWriter io.Writer = os.Stderr
