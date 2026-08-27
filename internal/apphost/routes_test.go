package apphost

import (
	"strings"
	"testing"

	"mar/internal/runtime"
)

// A route table is decided entirely by the registered program, so every way it
// can be wrong belongs at boot. None of it was checked anywhere.

func svc(verb, path string) runtime.VExposedService {
	shape := runtime.WireShape{Kind: runtime.WireRecord}
	return runtime.VExposedService{
		Service: runtime.VService{Verb: verb, Path: path, InputShape: &shape},
	}
}

func page(path string) runtime.VPage { return runtime.VPage{Path: path} }

func TestTwoServicesOnOnePathRefuseTheBoot(t *testing.T) {
	// The first wins and the second is unreachable, silently. When the loser
	// is the Auth.protect'ed half of a pair, the reachable one is the open
	// half — a protected route served to anyone.
	found := CheckRouteTable([]runtime.VExposedService{
		svc("POST", "/api/notes"),
		svc("POST", "/api/notes"),
	}, nil)
	if len(found.Fatal) == 0 {
		t.Fatal("two services on one path cannot both run")
	}
	for _, want := range []string{"POST", "/api/notes"} {
		if !strings.Contains(found.Fatal[0].Error(), want) {
			t.Errorf("the message must name %q, got: %v", want, found.Fatal[0])
		}
	}
}

func TestSameVerbDifferentPathIsFine(t *testing.T) {
	found := CheckRouteTable([]runtime.VExposedService{
		svc("POST", "/api/notes"),
		svc("POST", "/api/tags"),
		svc("GET", "/api/notes"),
	}, nil)
	if len(found.Fatal) != 0 {
		t.Fatalf("these do not collide: %v", found.Fatal)
	}
}

func TestTwoPagesOnOnePathRefuseTheBoot(t *testing.T) {
	// The browser keeps the last and the phone keeps the first, so one
	// program is two different apps.
	found := CheckRouteTable(nil, []runtime.VPage{page("/settings"), page("/settings")})
	if len(found.Fatal) == 0 {
		t.Fatal("two pages on one path make the two runtimes disagree")
	}
	if !strings.Contains(found.Fatal[0].Error(), "/settings") {
		t.Errorf("the message must name the path, got: %v", found.Fatal[0])
	}
}

func TestReservedPathRefusesTheBoot(t *testing.T) {
	for _, p := range []string{"/_mar/admin", "/_auth/whoami"} {
		found := CheckRouteTable([]runtime.VExposedService{svc("GET", p)}, nil)
		if len(found.Fatal) == 0 {
			t.Errorf("%s is served by the framework; a service there never runs", p)
		}
	}
}

func TestShadowedRouteIsReportedNotRefused(t *testing.T) {
	// Live in examples/bigapp. The app around it works, so refusing would
	// turn a partly-working app into one that does not start — a bigger
	// hammer than the defect. The note makes the dead route visible.
	found := CheckRouteTable([]runtime.VExposedService{
		svc("GET", "/api/item/{id:String}"),
		svc("GET", "/api/item/new"),
	}, nil)
	if len(found.Fatal) != 0 {
		t.Fatalf("this must not refuse the boot: %v", found.Fatal)
	}
	if len(found.Notes) == 0 {
		t.Fatal("the dead route has to be named, or nobody ever notices it")
	}
	if !strings.Contains(found.Notes[0], "/api/item/new") {
		t.Errorf("the note must name the dead route, got: %v", found.Notes)
	}
}

func TestPageSwallowedByAServiceIsReported(t *testing.T) {
	found := CheckRouteTable([]runtime.VExposedService{svc("GET", "/reports")},
		[]runtime.VPage{page("/reports")})
	if len(found.Fatal) != 0 {
		t.Fatalf("three shipped examples have this; it must not refuse: %v", found.Fatal)
	}
	if len(found.Notes) == 0 || !strings.Contains(found.Notes[0], "/reports") {
		t.Fatalf("the page with no deep link has to be named, got: %v", found.Notes)
	}
}

func TestOrdinaryTableIsSilent(t *testing.T) {
	// The floor: a table with nothing wrong says nothing at all.
	found := CheckRouteTable([]runtime.VExposedService{
		svc("GET", "/api/notes"),
		svc("POST", "/api/notes"),
		svc("GET", "/api/notes/{id:Int}"),
	}, []runtime.VPage{page("/"), page("/notes/{id:Int}")})
	if len(found.Fatal) != 0 || len(found.Notes) != 0 {
		t.Fatalf("nothing is wrong here: fatal=%v notes=%v", found.Fatal, found.Notes)
	}
}
