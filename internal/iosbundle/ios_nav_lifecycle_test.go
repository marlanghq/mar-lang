package iosbundle

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"mar/internal/jsserve"
	"mar/internal/parity"
)

// The iOS half of the navigation-lifecycle check.
//
// internal/jsserve/nav_lifecycle_test.go runs parity.NavSource through
// runtime.js and asserts what each step leaves on screen. These run the SAME
// program through the Swift runtime and assert the SAME strings. Two runtimes,
// one program, one set of expectations, which is what "no drift between web
// and iOS" has to mean if it means anything.
//
// It costs an Xcode build and a simulator, so it skips unless the machine has
// them and skips under -short. That is a real limitation and worth stating
// plainly: on a machine without Xcode, ADR-0009 is verified on the web and
// unverified here.
//
// Why it cannot be cheaper: on iOS the navigation lifecycle is not the
// runtime's bookkeeping, it is SwiftUI's view identity. Each NavigationStack
// entry builds its own MarPageHost whose @State gives it its own PageRuntime;
// popping destroys the pushed host while the one underneath was never torn
// down. A headless harness would have to stand in for SwiftUI, and would then
// be testing the stand-in. So the app really launches, and the driver inside
// it (AppViewModel.navLifecycle) only pokes and reads.

// The expectations, verbatim from the web test. Kept as literals rather than
// imported, because a shared constant would let both sides move together and
// the point is that they cannot.
var iosNavScripts = []struct {
	name   string
	script string
	want   string
	hint   string
}{
	{
		name:   "PushReinitsAndBackRestores",
		script: "push",
		want:   "mount:A=0 bumped:A=2 push-b:B=0 back-a:A=2 push-a:A=0",
		hint: "back-a:A=0  → Back re-initialized instead of restoring.\n" +
			"push-a:A=2  → a forward push reused the old model (the ADR-0009 bug).\n" +
			"push-b:A=2  → the push never happened; the shell is not driven by navPath.",
	},
	{
		name:   "ReplaceReinits",
		script: "replace",
		want:   "A=2 B=0",
		hint: "B=2 → Nav.replace carried the previous page's model into the new one, " +
			"which is how a logout lands you on a screen still holding the old session.",
	},
	{
		name:   "SheetPresentsOverTheCoveredPage",
		script: "sheet",
		want: "start:root=A=2,sheet=- presented:root=A=2,sheet=S=0 " +
			"dismissed:root=A=2,sheet=- reopened:root=A=2,sheet=S=0",
		hint: "presented:root=S=0  → the route replaced the page instead of covering it.\n" +
			"presented:root=A=0  → covering it wiped the covered page's model.\n" +
			"dismissed:sheet=S=0 → the overlay outlived its route.\n" +
			"reopened:sheet=S=2  → re-opening resumed the abandoned task instead of " +
			"starting a fresh one.",
	},
	{
		// A presented route opened cold, a shared link, a relaunch, arrives
		// with the screen it belongs to underneath it. It used to render as a
		// bare full screen, and Nav.dismiss is a no-op on the first entry, so
		// its own Done button did nothing.
		name:   "SheetOpenedColdPresentsOverTheAppRoot",
		script: "sheet-cold",
		want:   "cold:root=A=0,sheet=S=0",
		hint: "cold:sheet=-  → it rendered full screen, and Done is a dead end.\n" +
			"cold:root=-   → it presented over nothing, the same dead end with a backdrop.",
	},
	{
		// The real shape: the presented route nests in the url under the
		// screen it covers, so the parent resolves by path prefix with its
		// params already filled in.
		name:   "SheetOpenedColdPresentsOverItsParentRoute",
		script: "sheet-cold-nested",
		want:   "cold:root=A=0,sheet=N=0",
		hint: "cold:root=A=0,sheet=- → the prefix did not resolve, so it fell back " +
			"to a full screen instead of presenting over /a.",
	},
}

func TestNavigationLifecycleOnIOS(t *testing.T) {
	if testing.Short() {
		t.Skip("builds an iOS app and drives a simulator")
	}
	host := buildNavHost(t)

	for _, tc := range iosNavScripts {
		t.Run(tc.name, func(t *testing.T) {
			got := runNavScript(t, host, tc.script)
			if got != tc.want {
				t.Fatalf("navigation lifecycle differs from the web.\n got: %s\nwant: %s\n\n%s",
					got, tc.want, tc.hint)
			}
		})
	}
}

// A sheet that opens another sheet, parity.SheetsSource, against the string
// TestDismissingASheetOverASheetKeepsTheFirst holds the web to. The web swaps
// the sheet's content and iOS presents the second sheet over the first; which
// model each screen has, and which Done closes what, has to be the same.
func TestSheetOverSheetOnIOS(t *testing.T) {
	if testing.Short() {
		t.Skip("builds an iOS app and drives a simulator")
	}
	buildSheetsHostOnce.Do(func() {
		buildSheetsHostOnce.host, buildSheetsHostOnce.skip, buildSheetsHostOnce.fail =
			makeHost(t, parity.SheetsSource, "SheetsCheck", sheetsBundleID)
	})
	if buildSheetsHostOnce.fail != "" {
		t.Fatal(buildSheetsHostOnce.fail)
	}
	if buildSheetsHostOnce.host == nil {
		t.Skip(buildSheetsHostOnce.skip)
	}
	got := runNavScript(t, buildSheetsHostOnce.host, "sheets")
	want := "start:root=A=0,sheet=- s1:root=A=0,sheet=S=1 s2:root=A=0,sheet=N=0 " +
		"back:root=A=0,sheet=S=1 bumped:root=A=0,sheet=S=2 closed:root=A=0,sheet=-"
	if got != want {
		t.Fatalf("a sheet over a sheet differs from the web.\n got: %s\nwant: %s\n\n"+
			"s2:sheet=-       → the second sheet never got a page of its own; the first stayed on screen.\n"+
			"s2:root=S=…      → the first sheet was pushed as a page under the second.\n"+
			"back:sheet=S=0   → closing the second sheet reset the first.\n"+
			"closed:sheet=S=… → the first sheet's Done did nothing.", got, want)
	}

	// No web counterpart: the web paints every presented route it has not
	// painted yet from scratch (paintPresented), so it cannot keep one
	// page's screen for another.
	swap := runNavScript(t, buildSheetsHostOnce.host, "sheets-swap")
	wantSwap := "s:root=A=0,sheet=S=1 swapped:root=A=0,sheet=N=0 bumped:root=A=0,sheet=N=1"
	if swap != wantSwap {
		t.Fatalf("a sheet whose route changed in place kept the page that was there.\n got: %s\nwant: %s", swap, wantSwap)
	}
}

// parity.SharedWrapSource against the string TestSheetAndSharedWrappersCompose
// holds the web to: every nesting of Page.sheet and Page.withShared presents
// the page, and a page that reads two stores is drawn and follows both.
func TestSharedWrappersOnIOS(t *testing.T) {
	if testing.Short() {
		t.Skip("builds an iOS app and drives a simulator")
	}
	buildWrapHostOnce.Do(func() {
		buildWrapHostOnce.host, buildWrapHostOnce.skip, buildWrapHostOnce.fail =
			makeHost(t, parity.SharedWrapSource, "WrapCheck", wrapBundleID)
	})
	if buildWrapHostOnce.fail != "" {
		t.Fatal(buildWrapHostOnce.fail)
	}
	if buildWrapHostOnce.host == nil {
		t.Skip(buildWrapHostOnce.skip)
	}
	got := runNavScript(t, buildWrapHostOnce.host, "shared-wrap")
	want := "start:root=A=0,sheet=- p:root=A=0,sheet=P=7 p-hit:root=A=0,sheet=P=8 " +
		"l:root=A=0,sheet=L=8 k:root=A=0,sheet=K=809 " +
		"t:root=T=809,sheet=- hit-a:root=T=909,sheet=- hit-b:root=T=910,sheet=-"
	if got != want {
		t.Fatalf("Page.sheet and Page.withShared compose differently from the web.\n got: %s\nwant: %s\n\n"+
			"p:root=P=7,sheet=- → Page.sheet around a Page.withShared was pushed, not presented.\n"+
			"t:root=-            → the page that reads two stores was dropped.", got, want)
	}

	subs := runNavScript(t, buildWrapHostOnce.host, "shared-subs")
	if want := "r:root=R=101"; subs != want {
		t.Fatalf("a store was handed the device a number of times other than once.\n got: %s\nwant: %s\n\n"+
			"R=2   → store A's messages went to store B.\n"+
			"R=201 → store A, read twice, got each message twice.", subs, want)
	}
}

const wrapBundleID = "dev.marlang.wrapcheck"

var buildWrapHostOnce struct {
	sync.Once
	host *navHost
	skip string
	fail string
}

const sheetsBundleID = "dev.marlang.sheetscheck"

var buildSheetsHostOnce struct {
	sync.Once
	host *navHost
	skip string
	fail string
}

const navBundleID = "dev.marlang.navcheck"

// navHost is a built app with the fixture's program baked in, reused across
// the scripts. One build, four launches: the build is the expensive part and
// nothing about it varies per script.
type navHost struct {
	appPath  string
	bundleID string
	udid     string
}

var buildNavHostOnce struct {
	sync.Once
	host *navHost
	skip string
	fail string
}

func buildNavHost(t *testing.T) *navHost {
	t.Helper()
	buildNavHostOnce.Do(func() {
		host, skip, fail := makeHost(t, parity.NavSource, "NavCheck", navBundleID)
		buildNavHostOnce.host = host
		buildNavHostOnce.skip = skip
		buildNavHostOnce.fail = fail
	})
	// Skip and fail are different answers and must not be collapsed. They
	// were, once: every setup problem returned a skip reason, so a Swift
	// template that did not compile printed "ok" and moved on. A check that
	// reports success when it could not run is worse than no check.
	if buildNavHostOnce.fail != "" {
		t.Fatal(buildNavHostOnce.fail)
	}
	if buildNavHostOnce.host == nil {
		t.Skip(buildNavHostOnce.skip)
	}
	return buildNavHostOnce.host
}

// makeHost scaffolds a project around a fixture, builds it for the
// simulator, and boots a device.
//
// Returns (host, skip, fail). Skip is for a machine that cannot run this at
// all, no Xcode, no simulator, because a missing toolchain is not a failing
// invariant and turning it into one would make the suite unusable off macOS.
// Fail is for everything the machine COULD have done and did not.
func makeHost(t *testing.T, source, appName, bundleID string) (host *navHost, skip string, fail string) {
	t.Helper()
	if _, err := exec.LookPath("xcrun"); err != nil {
		return nil, "xcrun not found (needs Xcode)", ""
	}
	udid, err := bootedSimulator()
	if err != nil {
		return nil, "no iOS simulator available: " + err.Error(), ""
	}

	// The program is compiled here and embedded, not fetched from a dev
	// server: the check is about navigation, and a network dependency would
	// turn an unrelated flake into a lifecycle failure. DefaultBaseURL points
	// at a closed port on purpose, so the background refresh fails fast and
	// the embedded snapshot is what runs.
	program, err := parity.Compile(source, jsserve.SerializeModule)
	if err != nil {
		t.Fatalf("compiling the fixture: %v", err)
	}

	dir := t.TempDir()
	projectDir, err := Generate(Spec{
		AppName:          appName,
		BundleID:         bundleID,
		DisplayName:      appName,
		MarketingVersion: "1.0.0",
		BuildNumber:      "1",
		DefaultBaseURL:   "http://127.0.0.1:1",
		EmbeddedProgram:  program,
	}, dir)
	if err != nil {
		return nil, "", "scaffolding the iOS project failed: " + err.Error()
	}

	build := exec.Command("xcodebuild",
		"-project", filepath.Join(projectDir, appName+".xcodeproj"),
		"-scheme", appName,
		"-sdk", "iphonesimulator",
		"-destination", "platform=iOS Simulator,id="+udid,
		"-derivedDataPath", filepath.Join(dir, "dd"),
		"build")
	if outBytes, err := build.CombinedOutput(); err != nil {
		return nil, "", "the iOS template does not compile:\n" + swiftErrors(string(outBytes))
	}

	app := filepath.Join(dir, "dd", "Build", "Products", "Debug-iphonesimulator", appName+".app")
	if _, err := os.Stat(app); err != nil {
		return nil, "", "the build produced no " + appName + ".app"
	}
	return &navHost{appPath: app, bundleID: bundleID, udid: udid}, "", ""
}

// runNavScript installs the host, launches it with the script selected, and
// returns the single line the in-app driver printed.
func runNavScript(t *testing.T, host *navHost, script string) string {
	t.Helper()
	_ = exec.Command("xcrun", "simctl", "terminate", host.udid, host.bundleID).Run()
	if out, err := exec.Command("xcrun", "simctl", "install", host.udid, host.appPath).CombinedOutput(); err != nil {
		t.Fatalf("simctl install: %v\n%s", err, out)
	}

	launch := exec.Command("xcrun", "simctl", "launch", "--console-pty", host.udid, host.bundleID)
	launch.Env = append(os.Environ(), "SIMCTL_CHILD_MAR_NAV_LIFECYCLE="+script)
	stdout, err := launch.StdoutPipe()
	if err != nil {
		t.Fatalf("wiring the app's stdout: %v", err)
	}
	launch.Stderr = launch.Stdout
	if err := launch.Start(); err != nil {
		t.Fatalf("simctl launch: %v", err)
	}
	defer func() {
		_ = exec.Command("xcrun", "simctl", "terminate", host.udid, host.bundleID).Run()
		_ = launch.Process.Kill()
		_, _ = launch.Process.Wait()
	}()

	// The app does not exit on its own, so read until the driver says it is
	// done. The deadline is generous because each step waits for SwiftUI to
	// settle; a timeout here means the driver never finished, and the log so
	// far is the most useful thing to show.
	type result struct {
		line string
		log  string
	}
	done := make(chan result, 1)
	go func() {
		var log strings.Builder
		buf := make([]byte, 4096)
		var line string
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				log.Write(buf[:n])
				for _, l := range strings.Split(log.String(), "\n") {
					if after, ok := strings.CutPrefix(strings.TrimSpace(l), "[mar] NAV "); ok {
						if after != "DONE" && !strings.HasPrefix(after, "UNKNOWN") {
							line = after
						}
						if after == "DONE" {
							done <- result{line, log.String()}
							return
						}
					}
				}
			}
			if err != nil {
				done <- result{line, log.String()}
				return
			}
		}
	}()

	select {
	case r := <-done:
		if r.line == "" {
			t.Fatalf("the app never reported a navigation result.\n%s", tailLines(r.log, 30))
		}
		return r.line
	case <-time.After(90 * time.Second):
		t.Fatal("timed out waiting for the navigation driver to finish")
		return ""
	}
}

// bootedSimulator returns an already-booted device, or boots the first
// available iPhone. Reusing a booted one keeps a local run fast; booting is
// for a clean machine.
//
// When it boots one, it says so, so the suite can put it back. See
// simulator_lifecycle_test.go: a device this package started is this
// package's to shut down, and one that was already running belongs to
// whoever was using it.
// runtimeVersion pulls the numbers out of a simulator runtime identifier:
// `com.apple.CoreSimulator.SimRuntime.iOS-26-5` gives [26, 5]. Anything
// unparseable sorts last, which is the right place for it.
func runtimeVersion(id string) []int {
	i := strings.LastIndex(id, "iOS-")
	if i < 0 {
		return nil
	}
	var out []int
	for _, part := range strings.Split(id[i+len("iOS-"):], "-") {
		n, err := strconv.Atoi(part)
		if err != nil {
			break
		}
		out = append(out, n)
	}
	return out
}

func bootedSimulator() (string, error) {
	out, err := exec.Command("xcrun", "simctl", "list", "devices", "available", "-j").Output()
	if err != nil {
		return "", err
	}
	var listing struct {
		Devices map[string][]struct {
			UDID  string `json:"udid"`
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(out, &listing); err != nil {
		return "", err
	}
	// NEWEST runtime first, deliberately, and in a stable order.
	//
	// Ranging the map picked a different device on every run — the machine
	// here has three iOS runtimes with eleven devices each — so consecutive
	// runs booted different simulators and the suite left one behind. But
	// merely sorting the keys is worse than random: they are
	// `com.apple.CoreSimulator.SimRuntime.iOS-18-5` and friends, so
	// ascending order picks the OLDEST installed iOS, which is how this
	// package went from 190 seconds to a 600-second timeout. The version is
	// parsed rather than compared as text, because `iOS-9-0` sorts above
	// `iOS-10-0` in both directions otherwise.
	runtimeNames := make([]string, 0, len(listing.Devices))
	for name := range listing.Devices {
		runtimeNames = append(runtimeNames, name)
	}
	sort.Slice(runtimeNames, func(i, j int) bool {
		a, b := runtimeVersion(runtimeNames[i]), runtimeVersion(runtimeNames[j])
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] > b[k]
			}
		}
		if len(a) != len(b) {
			return len(a) > len(b)
		}
		return runtimeNames[i] > runtimeNames[j]
	})

	var candidate string
	for _, runtimeName := range runtimeNames {
		if !strings.Contains(runtimeName, "iOS") {
			continue
		}
		for _, d := range listing.Devices[runtimeName] {
			if d.State == "Booted" {
				return d.UDID, nil
			}
			if candidate == "" && strings.HasPrefix(d.Name, "iPhone") {
				candidate = d.UDID
			}
		}
	}
	if candidate == "" {
		return "", errNoSimulator
	}
	out, bootErr := exec.Command("xcrun", "simctl", "boot", candidate).CombinedOutput()
	alreadyRunning := strings.Contains(string(out), "current state: Booted")
	if bootErr != nil && !alreadyRunning {
		return "", bootErr
	}
	if !alreadyRunning {
		// We turned it on, so we owe it a shutdown.
		claimBootedSimulator(candidate)
	}
	// simctl boot returns before the device accepts installs.
	_ = exec.Command("xcrun", "simctl", "bootstatus", candidate).Run()
	return candidate, nil
}

type noSimulatorError struct{}

func (noSimulatorError) Error() string { return "no iPhone simulator is installed" }

var errNoSimulator = noSimulatorError{}

// swiftErrors pulls the compiler's own error lines out of an xcodebuild log,
// which is thousands of lines of frontend invocations around the two that say
// what is wrong. Falls back to the tail when nothing matches, so a failure
// mode this does not recognize still shows something.
func swiftErrors(log string) string {
	var found []string
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, ": error:") {
			found = append(found, strings.TrimSpace(line))
		}
	}
	if len(found) == 0 {
		return tailLines(log, 20)
	}
	if len(found) > 10 {
		found = found[:10]
	}
	return strings.Join(found, "\n")
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// parity.StoreLifeSource against the string TestAStoreLivesAsLongAsTheApp
// holds the web to: a store watching the device is handed it once whatever
// pages come and go, a store a page only writes to still runs its init Cmd, and
// a page a sheet covers stops listening.
func TestAStoreLivesAsLongAsTheAppOnIOS(t *testing.T) {
	if testing.Short() {
		t.Skip("builds an iOS app and drives a simulator")
	}
	buildLifeHostOnce.Do(func() {
		buildLifeHostOnce.host, buildLifeHostOnce.skip, buildLifeHostOnce.fail =
			makeHost(t, parity.StoreLifeSource, "LifeCheck", lifeBundleID)
	})
	if buildLifeHostOnce.fail != "" {
		t.Fatal(buildLifeHostOnce.fail)
	}
	if buildLifeHostOnce.host == nil {
		t.Skip(buildLifeHostOnce.skip)
	}
	got := runNavScript(t, buildLifeHostOnce.host, "store-life")
	want := "c:C=101 p:P=0 back-c:C=101 c-again:C=101 b:B=2 t-under-sheet:frozen"
	if got != want {
		t.Fatalf("a store or a covered page lives differently from the web.\n got: %s\nwant: %s\n\n"+
			"C=100           → the store a page only writes to never ran its init Cmd.\n"+
			"back-c:C=201    → the store stopped listening while a plain page was on top, and was handed the device again.\n"+
			"b:B=1           → a page watching the device through two taggers was handed it once.\n"+
			"t-under-sheet:ticking → the page under the sheet kept listening.", got, want)
	}
}

const lifeBundleID = "dev.marlang.lifecheck"

var buildLifeHostOnce struct {
	sync.Once
	host *navHost
	skip string
	fail string
}
