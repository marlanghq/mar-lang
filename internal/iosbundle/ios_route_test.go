package iosbundle

import (
	"sync"
	"testing"

	"mar/internal/parity"
)

// The iOS half of the routing check in internal/jsserve/effect_routing_test.go
// (TestRouteSourceOnTheWeb): parity.RouteSource on a simulator, through the
// same steps, held to the same string. A result goes to the page that ran the
// effect, not to the page that mounted last, which is what the phone used to
// do: the value A asked for while pushing B went to B.
const routeBundleID = "dev.marlang.routecheck"

var buildRouteHostOnce struct {
	sync.Once
	host *navHost
	skip string
	fail string
}

func TestEffectResultsReachTheirPageOnIOS(t *testing.T) {
	if testing.Short() {
		t.Skip("builds an iOS app and drives a simulator")
	}
	buildRouteHostOnce.Do(func() {
		buildRouteHostOnce.host, buildRouteHostOnce.skip, buildRouteHostOnce.fail =
			makeHost(t, parity.RouteSource, "RouteCheck", routeBundleID)
	})
	if buildRouteHostOnce.fail != "" {
		t.Fatal(buildRouteHostOnce.fail)
	}
	if buildRouteHostOnce.host == nil {
		t.Skip(buildRouteHostOnce.skip)
	}
	got := runNavScript(t, buildRouteHostOnce.host, "route-batch")
	want := "start:A=0 after-go:B=0 back-a:A=5"
	if got != want {
		t.Fatalf("a result went to another page than the one that asked.\n got: %s\nwant: %s\n\n"+
			"after-go:B=1 → the value A asked for went to B, the page on screen.\n"+
			"back-a:A=0  → A never got it.\n"+
			"back-a:B=0  → A, covered, navigated when its value arrived.", got, want)
	}

	twice := runNavScript(t, buildRouteHostOnce.host, "route-twice")
	wantTwice := "on-t:T=0 after-twice:C=0 back:B=0"
	if twice != wantTwice {
		t.Fatalf("the page on screen lost an effect after its own push.\n got: %s\nwant: %s", twice, wantTwice)
	}

	sheet := runNavScript(t, buildRouteHostOnce.host, "route-sheet-subs")
	wantSheet := "on-r:R=0 covered:R=0 closed:R=1"
	if sheet != wantSheet {
		t.Fatalf("a covered page's subscriptions did not wait for it to be shown.\n got: %s\nwant: %s\n\n"+
			"covered:R=1 → R's timer ran behind the sheet.\n"+
			"closed:R=0  → the sheet closed and R's timer never started.\n"+
			"closed:A=0  → closing the sheet took R off the stack with it.", sheet, wantSheet)
	}
}
