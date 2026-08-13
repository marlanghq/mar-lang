package jsserve

import (
	"errors"
	"testing"

	"mar/internal/parity"
)

// Page models live on the navigation stack (docs/adrs/0009): going somewhere
// new re-runs that page's `init`, going Back hands you the screen you left.
//
// This had no test, and it broke silently for months. `render()` re-initialized
// only protected and dynamic pages: static public pages init eagerly ONCE in
// buildPageEntry, and there was no `else` to run init again, so a static page
// kept its very first model for the life of the tab. It shipped because the
// guard *looked* right: it cleared the flags, and nothing downstream used them.
//
// So these tests drive the real runtime, not the flags. The program under test
// is parity.NavSource: a counter you can bump, a page to navigate to, and a
// sheet route, and a fake browser thin enough to be obviously honest: it
// renders into a fake DOM the test reads back, and its history is a list with
// an index. The assertions are on what the page SHOWS, which is the only thing
// a user can see and the only thing a stale model can lie about.
//
// The same fixture drives the same assertions against the Swift runtime on a
// simulator (internal/iosbundle/nav_lifecycle_ios_test.go). Sharing the source
// rather than copying it is what makes "the same program" literal.

// runNavDriver runs `driver` against the fixture through the real runtime.js.
// The fake browser and the node plumbing live in internal/parity so the iOS
// half of this check can reuse them without a second copy.
func runNavDriver(t *testing.T, driver string) string {
	t.Helper()
	program, err := parity.Compile(parity.NavSource, SerializeModule)
	if err != nil {
		t.Fatalf("compiling the fixture: %v", err)
	}
	out, err := parity.RunWeb(runtimeJS, program, driver)
	if errors.Is(err, parity.ErrNoNode) {
		t.Skip("node not installed")
	}
	if err != nil {
		t.Fatalf("node run: %v", err)
	}
	return out
}

// The whole rule in one trip: bump A, push away, come Back (A is as you left
// it), then push into A from elsewhere (A starts over).
//
// The last step is the one that was broken. `/a` is a static public page, so it
// was init'd once at mount and never again; arriving fresh from `/b` used to
// show the counter from the earlier visit. It is also the step that separates
// this rule from "never re-init": without it, a test that only checked Back
// would pass on a runtime that simply kept every model forever.
func TestNavPushReinitsAndBackRestores(t *testing.T) {
	got := runNavDriver(t, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
window.marRun(program);

const seen = [];
const note = (label) => seen.push(label + ':' + (screenText().match(/[AB]=\d+/) || ['?'])[0]);

note('mount');
bump(); bump();
note('bumped');

globalThis.__marNav.push('/b');
note('push-b');

history.back();
note('back-a');

globalThis.__marNav.push('/b');
globalThis.__marNav.push('/a');
note('push-a');

process.stdout.write(seen.join(' '));
`)
	want := "mount:A=0 bumped:A=2 push-b:B=0 back-a:A=2 push-a:A=0"
	if got != want {
		t.Fatalf("navigation lifecycle is wrong.\n got: %s\nwant: %s\n\n"+
			"back-a:A=0  → Back re-initialized instead of restoring.\n"+
			"push-a:A=2  → a forward push reused the old model (the ADR-0009 bug).", got, want)
	}
}

// Nav.replace is a context change, not a stack movement: same depth, so the
// depth delta alone reads it as "no navigation at all". It must still re-init,
// or logging out would drop you on a screen still holding the previous
// session's model. This is why navKind() checks the verb before the depth.
func TestNavReplaceReinits(t *testing.T) {
	got := runNavDriver(t, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
window.marRun(program);

const shown = () => (screenText().match(/[AB]=\d+/) || ['?'])[0];
bump(); bump();
const before = shown();
globalThis.__marNav.replace('/b');
process.stdout.write(before + ' ' + shown());
`)
	want := "A=2 B=0"
	if got != want {
		t.Fatalf("Nav.replace did not re-init the destination.\n got: %s\nwant: %s", got, want)
	}
}

// A presented route (Page.sheet) is the same page machinery with one thing
// changed: where the view is painted. So the test asserts exactly that, in the
// place a user would notice: the screen you came from is STILL THERE, with the
// state you left on it, while the task sits over it.
//
// Reading only #mar-root would pass on a runtime that simply refused to
// navigate, which is why every step checks both surfaces: the root (the covered
// page) and the overlay (the presented one).
func TestSheetRoutePresentsOverTheCoveredPage(t *testing.T) {
	got := runNavDriver(t, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
window.marRun(program);

const seen = [];
const root  = () => (screenText().match(/[ABS]=\d+/) || ['-'])[0];
const sheet = () => (sheetText().match(/[ABS]=\d+/) || ['-'])[0];
const note = (label) => seen.push(label + ':root=' + root() + ',sheet=' + sheet());

bump(); bump();
note('start');

globalThis.__marNav.push('/s');
note('presented');

history.back();
note('dismissed');

// Re-opening is a fresh task, not the one you abandoned: same push semantics
// as any other route (ADR 0009).
globalThis.__marNav.push('/s');
note('reopened');

process.stdout.write(seen.join(' '));
`)
	want := "start:root=A=2,sheet=- presented:root=A=2,sheet=S=0 dismissed:root=A=2,sheet=- reopened:root=A=2,sheet=S=0"
	if got != want {
		t.Fatalf("presented-route lifecycle is wrong.\n got: %s\nwant: %s\n\n"+
			"presented:root=S=0     → the route replaced the page instead of covering it.\n"+
			"presented:root=A=0     → covering it wiped the covered page's model.\n"+
			"dismissed:sheet=S=0    → the overlay outlived its route.\n"+
			"dismissed:root=A=0     → revealing the covered page re-initialized it.", got, want)
	}
}

// Opened cold, a bookmark, a shared link, a reload, a presented route now
// arrives WITH the screen it belongs to underneath it.
//
// It used to render as a bare full screen, on the reasoning that a deep link
// has nothing behind it. That was worse than odd: Nav.dismiss is a no-op on
// the first history entry, so the sheet's own Done button did nothing and the
// screen was a dead end you could only leave with the browser's back button.
//
// The parent needs no new API because a presented route already nests in the
// url under the screen it covers, and the prefix of a concrete url is itself
// concrete, so /a/nested resolves /a with its params already filled in. A
// presented route with no such parent falls back to the app's first page,
// because a modal always has something behind it.
func TestSheetRouteOpenedColdPresentsOverItsParent(t *testing.T) {
	for _, tc := range []struct {
		name, url, want string
	}{
		{
			name: "nested under its parent route",
			url:  "/a/nested",
			want: "root=A=0,sheet=N=0",
		},
		{
			// /s is declared at the top level, so there is no prefix to
			// resolve: the app's first page goes underneath instead.
			name: "top-level, so the app's first page goes underneath",
			url:  "/s",
			want: "root=A=0,sheet=S=0",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runNavDriver(t, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));

// Land directly on the presented route, as a deep link or reload would.
history.replaceState({ marDepth: 0, prevTitle: '' }, '', '`+tc.url+`');
window.marRun(program);

process.stdout.write(
  'root=' + ((screenText().match(/[ABSN]=\d+/) || ['-'])[0]) +
  ',sheet=' + ((sheetText().match(/[ABSN]=\d+/) || ['-'])[0]));
`)
			if got != tc.want {
				t.Fatalf("a cold-loaded sheet route did not present over its parent.\n got: %s\nwant: %s\n\n"+
					"sheet=- with a root  → it rendered full screen, and Done is a dead end.\n"+
					"root=- with a sheet  → it presented over nothing, which is the same dead end "+
					"with a backdrop.", got, tc.want)
			}
		})
	}
}
