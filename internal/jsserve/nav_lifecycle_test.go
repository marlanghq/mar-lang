package jsserve

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/parser"
	"mar/internal/typecheck"
)

// Page models live on the navigation stack (docs/adrs/0009): going somewhere
// new re-runs that page's `init`, going Back hands you the screen you left.
//
// This had no test, and it broke silently for months. `render()` re-initialized
// only protected and dynamic pages — static public pages init eagerly ONCE in
// buildPageEntry, and there was no `else` to run init again — so a static page
// kept its very first model for the life of the tab. It shipped because the
// guard *looked* right: it cleared the flags, and nothing downstream used them.
//
// So this test drives the real runtime, not the flags. Two pages, a counter you
// can bump, and a fake browser thin enough to be obviously honest: it renders
// into a fake DOM the test reads back, and its history is a list with an index.
// The assertions are on what the page SHOWS, which is the only thing a user can
// see and the only thing a stale model can lie about.
const navLifecycleSrc = `module Main exposing (main)


import UI exposing (vstack, text, button, navigationStack, navigationTitle)


type alias Model =
    { n : Int }


type Msg
    = Bump


init : (Model, Cmd Msg)
init = ( { n = 0 }, Cmd.none )


update : Msg -> Model -> (Model, Cmd Msg)
update _ model = ( { model | n = model.n + 1 }, Cmd.none )


-- The label makes each page's text self-identifying, so a wrong-page render
-- fails loudly instead of looking like a wrong counter.
viewFor : String -> Model -> View Msg
viewFor label model =
    navigationStack [ navigationTitle label ]
        [ vstack []
            [ text [] (label ++ "=" ++ String.fromInt model.n)
            , button [] Bump "bump"
            ]
        ]


pageA : Page
pageA =
    Page.create
        { path = "/a"
        , title = "A"
        , init = init
        , update = update
        , view = viewFor "A"
        , subscriptions = always Sub.none
        }


pageB : Page
pageB =
    Page.create
        { path = "/b"
        , title = "B"
        , init = init
        , update = update
        , view = viewFor "B"
        , subscriptions = always Sub.none
        }


-- Same page in every respect except how it is shown: Page.sheet asks the
-- runtime to lay it OVER the screen it was reached from instead of replacing
-- it. Nothing else about the page changes, which is the claim under test.
pageS : Page
pageS =
    Page.sheet
        (Page.create
            { path = "/s"
            , title = "S"
            , init = init
            , update = update
            , view = viewFor "S"
            , subscriptions = always Sub.none
            }
        )


main : Cmd ()
main =
    App.frontend [ pageA, pageB, pageS ]
`

// navFakeBrowser is the smallest browser the page runtime will boot against.
//
// Two deliberate omissions do real work. There is no `startViewTransition`, so
// the runtime takes its synchronous swap path and every assertion below reads a
// settled DOM. And `history.back()` fires popstate inline rather than on a
// later task, which a real browser would not do but which makes the test
// deterministic — the runtime's popstate handler is a plain render() call, so
// nothing about the behavior under test depends on the delay.
const navFakeBrowser = `
// A DOM element, faked only as far as the renderer actually reaches.
//
// Three of these are load-bearing rather than filler, and each was added
// because leaving it out made the fake LIE — the runtime ran without error and
// simply failed to update, which in a test reads as a bug in the code under
// test. Getting them right is what makes an assertion here mean anything:
//
//   querySelector(':scope > .cls') — how patchDOM finds a nav stack's body
//     wrapper. Returning null skips the in-place patch entirely, so the
//     screen freezes at its first render.
//   className / classList sharing one string — the renderer writes classes
//     both ways, and the selector above has to see either.
//   outerHTML — patchDOM's "did the nav bar actually change?" check. Left
//     undefined it compares undefined to undefined, always "same".
function mkEl(tag) {
  let cls = '';
  const classes = () => cls.split(/\s+/).filter(Boolean);
  const el = {
    tagName: String(tag).toUpperCase(), nodeType: 1,
    children: [], childNodes: [], firstChild: null, parentNode: null,
    textContent: '', value: '', id: '',
    style: {}, dataset: {}, _on: {},
    classList: {
      add(...c) { const s = new Set(classes()); c.forEach((x) => s.add(x)); cls = [...s].join(' '); },
      remove(...c) { const s = new Set(classes()); c.forEach((x) => s.delete(x)); cls = [...s].join(' '); },
      toggle(c, on) { on ? this.add(c) : this.remove(c); },
      contains(c) { return classes().indexOf(c) >= 0; },
    },
    appendChild(c) { this.children.push(c); this.childNodes.push(c);
      this.firstChild = this.children[0]; c.parentNode = this; return c; },
    insertBefore(c, ref) {
      const i = ref ? this.children.indexOf(ref) : -1;
      if (i < 0) return this.appendChild(c);
      this.children.splice(i, 0, c); this.childNodes.splice(i, 0, c);
      this.firstChild = this.children[0]; c.parentNode = this; return c;
    },
    removeChild(c) { const i = this.children.indexOf(c);
      if (i >= 0) { this.children.splice(i, 1); this.childNodes.splice(i, 1); }
      this.firstChild = this.children[0] || null; return c; },
    replaceChild(n, o) { const i = this.children.indexOf(o);
      if (i >= 0) { this.children[i] = n; this.childNodes[i] = n; n.parentNode = this; }
      this.firstChild = this.children[0] || null; return o; },
    setAttribute(k, v) { if (k === 'class') { cls = String(v); } else { this[k] = v; } },
    getAttribute(k) { if (k === 'class') return cls; return this[k] === undefined ? null : this[k]; },
    removeAttribute(k) { delete this[k]; },
    hasAttribute(k) { return this[k] !== undefined; },
    addEventListener(t, f) { (this._on[t] = this._on[t] || []).push(f); },
    removeEventListener(t, f) {
      if (this._on[t]) this._on[t] = this._on[t].filter((x) => x !== f); },
    dispatch(t, ev) { (this._on[t] || []).forEach((f) => f(ev)); },
    // Only the ':scope > .a > .b' shape the renderer uses: walk one class
    // step at a time through direct children. Anything else returns null
    // loudly rather than pretending to match.
    querySelector(sel) {
      const steps = String(sel).split('>').map((s) => s.trim()).filter((s) => s && s !== ':scope');
      let cur = this;
      for (const step of steps) {
        if (step[0] !== '.') return null;
        const want = step.slice(1);
        cur = (cur.children || []).find((c) => c.classList && c.classList.contains(want));
        if (!cur) return null;
      }
      return cur === this ? null : cur;
    },
    querySelectorAll() { return []; },
    closest() { return null; }, contains() { return false; },
    focus() {}, blur() {}, scrollIntoView() {}, remove() {},
    click() { this.dispatch('click', { preventDefault() {}, stopPropagation() {}, target: this }); },
    getBoundingClientRect() { return { top: 0, left: 0, right: 100, bottom: 100, width: 100, height: 100 }; },
    getContext() { return null; },
  };
  Object.defineProperty(el, 'className', { get: () => cls, set: (v) => { cls = String(v); }, enumerable: true });
  // Structure + classes + text: enough of a fingerprint for "did this
  // subtree change?", which is all the runtime asks it.
  Object.defineProperty(el, 'outerHTML', { get() {
    const kids = el.children.map((c) => (c.outerHTML === undefined ? String(c.textContent || '') : c.outerHTML)).join('');
    const t = el.tagName.toLowerCase();
    return '<' + t + ' class="' + cls + '">' + String(el.textContent || '') + kids + '</' + t + '>';
  } });
  return el;
}

const marRoot = mkEl('div');
marRoot.id = 'mar-root';

global.document = {
  documentElement: mkEl('html'),
  head: mkEl('head'),
  body: mkEl('body'),
  title: '',
  readyState: 'complete',
  createElement: mkEl,
  createTextNode: (t) => ({ nodeType: 3, textContent: String(t), children: [], childNodes: [] }),
  getElementById: (id) => (id === 'mar-root' ? marRoot : null),
  querySelector: () => null,
  querySelectorAll: () => [],
  addEventListener() {}, removeEventListener() {},
};

// history as a list plus a cursor — the same shape the runtime reasons about
// when it compares marDepth across renders.
let entries = [{ state: { marDepth: 0, prevTitle: '' }, path: '/a' }];
let cursor = 0;
const winOn = {};
const fire = (t, ev) => (winOn[t] || []).forEach((f) => f(ev || {}));

global.history = {
  scrollRestoration: 'auto',
  get state() { return entries[cursor].state; },
  get length() { return entries.length; },
  pushState(st, _t, p) {
    entries = entries.slice(0, cursor + 1);
    entries.push({ state: st, path: p });
    cursor++;
  },
  replaceState(st, _t, p) {
    entries[cursor] = { state: st, path: p === undefined || p === null ? entries[cursor].path : p };
  },
  back() { if (cursor > 0) { cursor--; fire('popstate', {}); } },
  forward() { if (cursor < entries.length - 1) { cursor++; fire('popstate', {}); } },
  go(n) { const t = cursor + n; if (t >= 0 && t < entries.length) { cursor = t; fire('popstate', {}); } },
};

global.location = {
  get pathname() { return entries[cursor].path; },
  search: '', hash: '', origin: 'http://test', protocol: 'http:', host: 'test',
  get href() { return 'http://test' + entries[cursor].path; },
};

const noopObserver = class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } };
global.IntersectionObserver = noopObserver;
global.ResizeObserver = noopObserver;
global.MutationObserver = noopObserver;

global.window = {
  document: global.document, history: global.history, location: global.location,
  innerWidth: 900, innerHeight: 700, scrollX: 0, scrollY: 0, devicePixelRatio: 1,
  addEventListener(t, f) { (winOn[t] = winOn[t] || []).push(f); },
  removeEventListener(t, f) { if (winOn[t]) winOn[t] = winOn[t].filter((x) => x !== f); },
  scrollTo() {}, scrollBy() {}, requestAnimationFrame: (f) => setTimeout(() => f(0), 0),
  cancelAnimationFrame() {}, getComputedStyle: () => ({ getPropertyValue: () => '' }),
  matchMedia: () => ({ matches: false, media: '', addEventListener() {}, removeEventListener() {},
    addListener() {}, removeListener() {} }),
  IntersectionObserver: noopObserver, ResizeObserver: noopObserver,
};
global.navigator = { userAgent: 'node', language: 'en' };
global.fetch = () => Promise.reject(new Error('no network in this test'));

// Everything the page rendered, flattened. The counter lives in a text node, so
// this is how the test sees the model.
function screenText(node, out) {
  out = out || [];
  node = node || marRoot;
  if (node.textContent) out.push(node.textContent);
  (node.children || []).forEach((c) => screenText(c, out));
  return out.join(' ');
}

// What the PRESENTED route shows, or '' when nothing is showing. The runtime
// mounts that overlay on document.body, outside #mar-root, so a test that only
// read the root would see the covered page and call it a pass.
//
// The open CLASS is the condition, not mere presence: closing drops
// mar-sheet-open first and detaches the node when the exit transition ends, so
// for one animation frame a closed sheet is still in the DOM. That class is what
// the CSS keys visibility off, so it is also what a user can see.
function sheetText() {
  const host = (global.document.body.children || []).find((c) =>
    c.classList && c.classList.contains('mar-page-sheet') && c.classList.contains('mar-sheet-open'));
  return host ? screenText(host) : '';
}

// The button carries the only click handler the page installs; finding it by
// its label keeps the test from depending on the renderer's element shape.
function bump(node) {
  node = node || marRoot;
  if (node.textContent === 'bump' && node._on && node._on.click) { node.click(); return true; }
  return (node.children || []).some((c) => bump(c));
}
`

// runNavDriver compiles navLifecycleSrc, drops it next to the real runtime.js,
// and runs `driver` under node with both as argv. Returns the driver's stdout.
func runNavDriver(t *testing.T, driver string) string {
	t.Helper()
	nodePath, lookErr := exec.LookPath("node")
	if lookErr != nil {
		t.Skip("node not installed")
	}

	mod, err := parser.Parse(navLifecycleSrc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("typecheck: %v", err)
	}

	dir := t.TempDir()
	programJSON, err := json.Marshal(map[string]any{
		"modules": []any{SerializeModule(mod)},
		"entry":   "Main.main",
	})
	if err != nil {
		t.Fatalf("marshal program: %v", err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("runtime.js", runtimeJS)
	write("program.json", string(programJSON))
	write("driver.js", navFakeBrowser+driver)

	cmd := exec.Command(nodePath, filepath.Join(dir, "driver.js"),
		filepath.Join(dir, "runtime.js"), filepath.Join(dir, "program.json"))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node run: %v\n%s", err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// The whole rule in one trip: bump A, push away, come Back (A is as you left
// it), then push into A from elsewhere (A starts over).
//
// The last step is the one that was broken. `/a` is a static public page, so it
// was init'd once at mount and never again; arriving fresh from `/b` used to
// show the counter from the earlier visit. It is also the step that separates
// this rule from "never re-init" — without it, a test that only checked Back
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

// Nav.replace is a context change, not a stack movement — same depth, so the
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
// place a user would notice — the screen you came from is STILL THERE, with the
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

// Opened cold there is nothing to present over — no page came before it — so
// the route has to render as an ordinary full screen. Without this a shared
// link or a reload would land on a backdrop over nothing.
func TestSheetRouteOpenedColdRendersFullScreen(t *testing.T) {
	got := runNavDriver(t, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));

// Land directly on the presented route, as a deep link or reload would.
history.replaceState({ marDepth: 0, prevTitle: '' }, '', '/s');
window.marRun(program);

process.stdout.write(
  'root=' + ((screenText().match(/[ABS]=\d+/) || ['-'])[0]) +
  ',sheet=' + ((sheetText().match(/[ABS]=\d+/) || ['-'])[0]));
`)
	want := "root=S=0,sheet=-"
	if got != want {
		t.Fatalf("a cold-loaded sheet route did not fall back to full screen.\n got: %s\nwant: %s\n\n"+
			"sheet=S=0 → it presented over nothing, leaving no page underneath.", got, want)
	}
}
