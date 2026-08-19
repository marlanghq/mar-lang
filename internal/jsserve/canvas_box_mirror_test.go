package jsserve

import (
	"errors"
	"testing"

	"mar/internal/parity"
)

// Canvas.watchSize mirrors the whole box: size plus the four safe-area insets
// (docs/adrs/0034). The insets are the half nothing else can see -- they decide
// where a control may be drawn on a notched phone, and getting them wrong is
// invisible in a desktop browser, on a simulator held one way, and in any test
// that only watches the size.
//
// So these drive the real runtime.js against the fake browser and read what the
// PAGE PRINTS, not what the runtime computed. The fixture prints the last box
// it heard and counts the messages, so a missing message and a duplicate are
// both a number the test can name.
func runBoxDriver(t *testing.T, driver string) string {
	t.Helper()
	program, err := parity.Compile(parity.BoxSource, SerializeModule)
	if err != nil {
		t.Fatalf("compiling the fixture: %v", err)
	}
	out, err := parity.RunWeb(runtimeJS, program, boxHarness+driver)
	if errors.Is(err, parity.ErrNoNode) {
		t.Skip("node not installed")
	}
	if err != nil {
		t.Fatalf("running the driver: %v", err)
	}
	return out
}

// Boot the app, then hand the test the two knobs a browser owns: the element
// box and what the CSS engine resolves env(safe-area-inset-*) to.
const boxHarness = `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));

function findCanvas(node) {
  node = node || marRoot;
  if (node.tagName === 'CANVAS') return node;
  for (const c of (node.children || [])) { const f = findCanvas(c); if (f) return f; }
  return null;
}

// Set the element box and the insets, then let the browser notice. A resize
// notices both; an orientation change notices only what the window event says,
// which is exactly the case a 180-degree landscape turn produces.
function setBox(w, h) {
  const el = findCanvas();
  el.clientWidth = w; el.clientHeight = h;
  return el;
}
function insets(top, right, bottom, left) {
  global.safeArea = { top: top, right: right, bottom: bottom, left: left };
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function main() {
  window.marRun(program);
  await sleep(0);
`

// The seed: a canvas that just mounted must hear its real box, insets and all,
// without waiting for anything to change. Without it a game lays out its first
// frame against zeros.
func TestCanvasBoxMirrorSeedsSizeAndInsets(t *testing.T) {
	got := runBoxDriver(t, `
  setBox(844, 390);
  insets(0, 59, 21, 0);   // an iPhone Pro, landscape, notch on the right
  fireResize();
  await sleep(20);
  process.stdout.write(screenText());
}
main();
`)
	want := "box=844x390 insets=0,59,21,0 seen=1"
	if !contains(got, want) {
		t.Fatalf("the canvas did not hear its box on mount.\n got: %s\nwant a line containing: %s", got, want)
	}
}

// THE REGRESSION. Turning a phone 180 degrees in landscape leaves the box byte
// for byte identical and moves the notch from one side to the other. No
// ResizeObserver fires. A mirror that only re-reads on resize keeps reporting
// left/right swapped, so every control the app inset lands under the Dynamic
// Island -- wrong on one device held one way, which is the failure ADR 0034
// exists to prevent and the one no desktop browser can show you.
func TestCanvasBoxMirrorFollowsANotchThatMovedWithoutAResize(t *testing.T) {
	got := runBoxDriver(t, `
  setBox(844, 390);
  insets(0, 59, 21, 0);
  fireResize();
  await sleep(20);
  const seeded = screenText();

  // The turn: same box, notch now on the LEFT. Only the window event fires.
  insets(0, 0, 21, 59);
  fireWindow('orientationchange');
  await sleep(700);

  process.stdout.write(seeded + ' || ' + screenText());
}
main();
`)
	want := "box=844x390 insets=0,0,21,59 seen=2"
	if !contains(got, want) {
		t.Fatalf("the notch moved and the mirror did not follow.\n got: %s\nwant the second line to contain: %s\n"+
			"Left and right stay swapped, so an app insets the wrong edge on a phone turned one way.", got, want)
	}
}

// The re-read fires repeatedly (a rotation settles over several frames), and
// the window event can arrive with nothing actually changed. Either must cost
// exactly zero messages: an app that lays out on every box message would
// otherwise rebuild its HUD a handful of times per turn.
func TestCanvasBoxMirrorSaysNothingWhenNothingChanged(t *testing.T) {
	got := runBoxDriver(t, `
  setBox(844, 390);
  insets(0, 59, 21, 0);
  fireResize();
  await sleep(20);

  fireResize(); fireResize();
  fireWindow('orientationchange');
  await sleep(700);

  process.stdout.write(screenText());
}
main();
`)
	want := "box=844x390 insets=0,59,21,0 seen=1"
	if !contains(got, want) {
		t.Fatalf("an unchanged box was re-announced.\n got: %s\nwant a line containing: %s", got, want)
	}
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
