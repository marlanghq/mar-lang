package iosbundle

import (
	"sync"
	"testing"

	"mar/internal/parity"
)

// The iOS half of internal/jsserve/sub_seed_test.go: parity.SubsSource on a
// simulator, through the same steps, held to the same strings. Each count is
// the number of Device messages a page received; a page shown once must read
// 1.
//
// On the phone each shown page mounts its own runtime, and a mounted page's
// source hands it the device's state. Inside a single runtime the phone had
// the web's bug: a page that starts watching while its shared store already
// does joins a source that is running, and was handed nothing (L). A page
// that starts watching in the same turn as the source starts is handed it once
// (K).
//
// After Back, SwiftUI may or may not have unmounted the covered page. Either
// way a page shown again is handed the state only if it changed, so nothing
// here. The case where SwiftUI does unmount and mount it again (seen once for
// a root opened at launch, not reproducible on demand) has no script yet.
var iosSubsScripts = []struct {
	name   string
	script string
	want   string
}{
	{
		name:   "AWatchingPageGetsTheDeviceEachTimeItIsShown",
		script: "subs-push",
		want:   "start:N=0 push-a:A=1 push-b:B=1 back-a:A=1 push-b-again:B=1 replace-a:A=1",
	},
	{
		name:   "APageJoiningItsStoresRunningSourceGetsTheDevice",
		script: "subs-shared",
		want:   "start:N=0 push-k:K=1 replace-p:P=0 push-l:L=0 armed:L=1",
	},
}

const subsBundleID = "dev.marlang.subscheck"

var buildSubsHostOnce struct {
	sync.Once
	host *navHost
	skip string
	fail string
}

func TestStateSubscriptionsFollowThePageOnIOS(t *testing.T) {
	if testing.Short() {
		t.Skip("builds an iOS app and drives a simulator")
	}
	buildSubsHostOnce.Do(func() {
		buildSubsHostOnce.host, buildSubsHostOnce.skip, buildSubsHostOnce.fail =
			makeHost(t, parity.SubsSource, "SubsCheck", subsBundleID)
	})
	if buildSubsHostOnce.fail != "" {
		t.Fatal(buildSubsHostOnce.fail)
	}
	if buildSubsHostOnce.host == nil {
		t.Skip(buildSubsHostOnce.skip)
	}
	for _, tc := range iosSubsScripts {
		t.Run(tc.name, func(t *testing.T) {
			got := runNavScript(t, buildSubsHostOnce.host, tc.script)
			if got != tc.want {
				t.Fatalf("what a page was handed differs from the web.\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}
