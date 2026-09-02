package parity

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"mar/internal/ast"
	"mar/internal/parser"
	"mar/internal/typecheck"
)

// ErrNoNode says the machine cannot run the web half at all. Callers turn it
// into a skip; every other error is a real failure.
var ErrNoNode = errors.New("node is not installed")

// Compile turns one of the fixture sources into the program.json both runtimes
// consume. Same bytes to node and to the iOS bundle: the point of the
// exercise is that the two are given identical input.
//
// The serializer is passed in rather than imported: internal/jsserve owns it,
// and its own tests use this package, so importing it here would be a cycle.
func Compile(source string, serialize func(*ast.Module) map[string]any) ([]byte, error) {
	mod, err := parser.Parse(source)
	if err != nil {
		return nil, err
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"modules": []any{serialize(mod)},
		"entry":   "Main.main",
	})
}

// RunWeb runs `driver` under node with the real runtime.js and the given
// program, inside FakeBrowser, and returns its stdout.
//
// The caller supplies runtimeJS rather than this package reading it, so that
// internal/jsserve's own tests can use this without importing themselves.
func RunWeb(runtimeJS string, programJSON []byte, driver string) (string, error) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return "", ErrNoNode
	}
	dir, err := os.MkdirTemp("", "mar-parity")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	write := func(name, body string) error {
		return os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	if err := write("runtime.js", runtimeJS); err != nil {
		return "", err
	}
	if err := write("program.json", string(programJSON)); err != nil {
		return "", err
	}
	if err := write("driver.js", FakeBrowser+driver); err != nil {
		return "", err
	}

	cmd := exec.Command(nodePath, filepath.Join(dir, "driver.js"),
		filepath.Join(dir, "runtime.js"), filepath.Join(dir, "program.json"))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", errors.New(err.Error() + "\n" + stderr.String())
	}
	return strings.TrimSpace(string(out)), nil
}

// FakeBrowser is the smallest browser the page runtime will boot against.
//
// Two deliberate omissions do real work. There is no `startViewTransition`, so
// the runtime takes its synchronous swap path and every assertion below reads a
// settled DOM. And `history.back()` fires popstate inline rather than on a
// later task, which a real browser would not do but which makes the test
// deterministic: the runtime's popstate handler is a plain render() call, so
// nothing about the behavior under test depends on the delay.
const FakeBrowser = `
// nextSibling / previousSibling, kept in step with every mutation.
//
// Not decoration: moveGrabbed picks its insertBefore anchor with
// rows[target].nextSibling, so without these the row was appended to the END
// on every arrow press instead of moving one place — the keyboard reorder
// "worked" and reported the wrong thing, which is worse than not working.
// detach removes a node from whatever parent it currently has, so a re-insert
// moves it instead of cloning it into two places at once.
let attrRead = 0;
// Every name the runtime uses as an ATTRIBUTE. Attributes are stored as plain
// properties here, so reading one that was never set is indistinguishable from
// reading a DOM member the fake lacks — and only the second is worth
// reporting. Collected rather than listed, so it cannot go stale.
const attrNames = new Set();

function detach(c) {
  const p = c && c.parentNode;
  if (!p || !p.children) return;
  const i = p.children.indexOf(c);
  if (i < 0) return;
  p.children.splice(i, 1);
  if (p.childNodes) {
    const j = p.childNodes.indexOf(c);
    if (j >= 0) p.childNodes.splice(j, 1);
  }
  p.firstChild = p.children[0] || null;
  c.parentNode = null;
  relink(p);
}

function relink(parent) {
  const kids = parent.children || [];
  for (let i = 0; i < kids.length; i++) {
    kids[i].nextSibling = kids[i + 1] || null;
    kids[i].previousSibling = kids[i - 1] || null;
  }
}

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
    // appendChild and insertBefore MOVE a node that already has a parent, as
    // the DOM does. The fake used to splice it in a second time, so any code
    // that reorders by re-inserting an existing child DUPLICATED it — the row
    // appeared twice, the index the reorder reported was computed over a list
    // that does not exist in a browser, and nothing said a word.
    appendChild(c) { detach(c); this.children.push(c); this.childNodes.push(c);
      this.firstChild = this.children[0]; c.parentNode = this; relink(this); return c; },
    insertBefore(c, ref) {
      if (ref === c) return c;
      detach(c);
      const i = ref ? this.children.indexOf(ref) : -1;
      if (i < 0) return this.appendChild(c);
      this.children.splice(i, 0, c); this.childNodes.splice(i, 0, c);
      this.firstChild = this.children[0]; c.parentNode = this; relink(this); return c;
    },
    removeChild(c) { const i = this.children.indexOf(c);
      if (i >= 0) { this.children.splice(i, 1); this.childNodes.splice(i, 1); }
      this.firstChild = this.children[0] || null; relink(this); return c; },
    replaceChild(n, o) { const i = this.children.indexOf(o);
      if (i >= 0) { this.children[i] = n; this.childNodes[i] = n; n.parentNode = this; relink(this); }
      this.firstChild = this.children[0] || null; return o; },
    setAttribute(k, v) { attrNames.add(k); if (k === 'class') { cls = String(v); } else { this[k] = v; } },
    getAttribute(k) {
      attrNames.add(k);
      if (k === 'class') return cls;
      // Attributes live as plain properties here, so reading one that was
      // never set looks the same as reading a DOM member the fake lacks. Only
      // the second is worth reporting, so the gap recorder stands down while
      // an attribute is being read.
      attrRead++;
      try { return this[k] === undefined ? null : this[k]; } finally { attrRead--; }
    },
    removeAttribute(k) { attrNames.add(k); delete this[k]; },
    hasAttribute(k) {
      attrNames.add(k);
      attrRead++;
      try { return this[k] !== undefined; } finally { attrRead--; }
    },
    addEventListener(t, f) { (this._on[t] = this._on[t] || []).push(f); },
    removeEventListener(t, f) {
      if (this._on[t]) this._on[t] = this._on[t].filter((x) => x !== f); },
    // Events BUBBLE, because the runtime relies on it: the link interceptor
    // is one delegated listener on the host covering every descendant anchor
    // ("one listener per host covers every descendant anchor", says the code).
    // A dispatch that stopped at the target made that listener unreachable —
    // the click did nothing, the fake reported no error, and the matcher
    // behind it was exercised by no test.
    //
    // stopPropagation is honoured, since a handler that calls it is saying
    // exactly this should not happen.
    dispatch(t, ev) {
      ev = ev || {};
      if (ev.target === undefined) ev.target = this;
      let stopped = false;
      const userStop = ev.stopPropagation;
      ev.stopPropagation = function () {
        stopped = true;
        if (typeof userStop === 'function') userStop.call(ev);
      };
      let cur = this;
      while (cur) {
        (cur._on[t] || []).forEach((f) => f(ev));
        if (stopped) return;
        cur = cur.parentNode;
      }
    },
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
    // closest used to answer null always, and that made the fake LIE in the
    // way this file warns about: the runtime's link interceptor starts with
    // ev.target.closest('a[href^="/"]'), so with a null answer NO in-app link
    // click was ever routed here — the runtime ran, did nothing, and the
    // matcher behind it (matchesAnyPage) was reached by no test at all.
    //
    // Two selector shapes, both taken from the runtime:
    //   a[href^="/"]           a tag, optionally filtered on one attribute
    //   .mar-section-body > *  the ancestor that is a direct child of a class
    //
    // Anything else THROWS. Returning null for a selector this does not
    // understand is how the first version of this hid a whole listener: a
    // fake that cannot answer has to say so, not guess "no".
    closest(sel) {
      const raw = String(sel || '').trim();
      const child = /^\.([A-Za-z0-9_-]+)\s*>\s*\*$/.exec(raw);
      if (child) {
        const parentClass = child[1];
        let cur = this;
        while (cur && cur.parentNode) {
          if (cur.parentNode.classList && cur.parentNode.classList.contains(parentClass)) return cur;
          cur = cur.parentNode;
        }
        return null;
      }
      const simple = /^([a-zA-Z]+)(?:\[([a-zA-Z-]+)(?:\^?=\"([^\"]*)\")?\])?$/.exec(raw);
      if (simple) {
        const [, tag, attr, prefix] = simple;
        let cur = this;
        while (cur) {
          const tagOk = cur.tagName === tag.toUpperCase();
          let attrOk = true;
          if (attr) {
            const v = cur.getAttribute ? cur.getAttribute(attr) : null;
            attrOk = v !== null && v !== undefined &&
                     (prefix === undefined || String(v).startsWith(prefix));
          }
          if (tagOk && attrOk) return cur;
          cur = cur.parentNode;
        }
        return null;
      }
      throw new Error('FakeBrowser.closest does not understand the selector ' +
        JSON.stringify(raw) + ' — teach it that shape rather than letting it answer "no"');
    },
    contains() { return false; },
    focus() {}, blur() {}, scrollIntoView() {}, remove() {},
    click() { this.dispatch('click', { preventDefault() {}, stopPropagation() {}, target: this }); },
    getBoundingClientRect() { return { top: 0, left: 0, right: 100, bottom: 100, width: 100, height: 100 }; },
    getContext() { return null; },
    // Real elements have this; code that prunes detached nodes reads it, and a
    // fake that leaves it undefined makes every element look detached.
    get isConnected() {
      let n = this;
      while (n.parentNode) n = n.parentNode;
      return n === marRoot || n === global.document.body || n === global.document.documentElement;
    },
  };
  Object.defineProperty(el, 'className', { get: () => cls, set: (v) => { cls = String(v); }, enumerable: true });
  // Structure + classes + text: enough of a fingerprint for "did this
  // subtree change?", which is all the runtime asks it.
  Object.defineProperty(el, 'outerHTML', { get() {
    const kids = el.children.map((c) => (c.outerHTML === undefined ? String(c.textContent || '') : c.outerHTML)).join('');
    const t = el.tagName.toLowerCase();
    return '<' + t + ' class="' + cls + '">' + String(el.textContent || '') + kids + '</' + t + '>';
  } });
  // Every element is handed out wrapped, so a member the runtime reads and
  // this fake does not have gets RECORDED instead of coming back undefined.
  //
  // Four times in one afternoon a missing member made the fake lie rather than
  // fail: closest answered null, nextSibling was absent (and a || null
  // swallowed it), insertBefore cloned instead of moving, and events did not
  // bubble. Each one hid a whole listener from every test, and each was found
  // by hand, hours apart. __marFakeGaps is so the fake says it first.
  //
  // Reads only, and only of names that look like DOM members: the runtime also
  // parks its own bookkeeping on nodes (__marView and friends), and a read
  // of one that is not there yet is ordinary.
  const proxied = new Proxy(el, {
    get(target, prop, recv) {
      if (attrRead === 0 && typeof prop === 'string' && !(prop in target) &&
          !attrNames.has(prop) &&
          !prop.startsWith('__') && !prop.startsWith('_') &&
          prop !== 'then' && /^[a-z]/.test(prop)) {
        (globalThis.__marFakeGaps || (globalThis.__marFakeGaps = new Set())).add(prop);
      }
      return Reflect.get(target, prop, recv);
    },
  });
  el.self = proxied;
  return proxied;
}

const marRoot = mkEl('div');
marRoot.id = 'mar-root';

global.document = {
  // <html lang> carries the app's locale: the shell writes it from mar.json,
  // and App.locale reads it back. Without it here every web test ran on the
  // "en" fallback, so the locale path was never the one under test. The value
  // is the one the shell emits for a project that declares none.
  documentElement: (() => { const h = mkEl('html'); h.lang = 'en'; return h; })(),
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
// Window events a test needs to raise by hand (orientationchange, say).
global.fireWindow = fire;

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
global.MutationObserver = noopObserver;

// ResizeObserver records instead of ignoring, so a test can say "the box
// changed" the way a browser would. Nothing fires on its own: observe() only
// registers, and global.fireResize() is what delivers. A noop here would make
// the canvas size mirror look dead, which reads as a bug in the code under
// test rather than a hole in the fake.
const observers = [];
global.ResizeObserver = class {
  constructor(cb) { this.cb = cb; }
  observe(el) { observers.push({ cb: this.cb, el }); }
  unobserve(el) { for (let i = observers.length - 1; i >= 0; i--) if (observers[i].el === el) observers.splice(i, 1); }
  disconnect() { for (let i = observers.length - 1; i >= 0; i--) if (observers[i].cb === this.cb) observers.splice(i, 1); }
  takeRecords() { return []; }
};
global.fireResize = () => observers.forEach((o) => o.cb([{ target: o.el }], o));

// env(safe-area-inset-*) is resolved by the CSS engine, which a fake DOM does
// not have. So the fake resolves it from one table a test can write, and every
// other property answers '0px'. Global as well as on window: the runtime calls
// getComputedStyle bare.
global.safeArea = { top: 0, right: 0, bottom: 0, left: 0 };
const computedStyle = (el) => {
  const css = (el && el.style && el.style.cssText) || '';
  const env = (side, prop) =>
    css.indexOf(prop + ':env(safe-area-inset-' + side + ')') >= 0
      ? global.safeArea[side] + 'px'
      : '0px';
  return {
    getPropertyValue: () => '',
    paddingTop: env('top', 'padding-top'), paddingRight: env('right', 'padding-right'),
    paddingBottom: env('bottom', 'padding-bottom'), paddingLeft: env('left', 'padding-left'),
  };
};
global.getComputedStyle = computedStyle;

global.window = {
  document: global.document, history: global.history, location: global.location,
  innerWidth: 900, innerHeight: 700, scrollX: 0, scrollY: 0, devicePixelRatio: 1,
  addEventListener(t, f) { (winOn[t] = winOn[t] || []).push(f); },
  removeEventListener(t, f) { if (winOn[t]) winOn[t] = winOn[t].filter((x) => x !== f); },
  scrollTo() {}, scrollBy() {}, requestAnimationFrame: (f) => setTimeout(() => f(0), 0),
  cancelAnimationFrame() {}, getComputedStyle: computedStyle,
  matchMedia: () => ({ matches: false, media: '', addEventListener() {}, removeEventListener() {},
    addListener() {}, removeListener() {} }),
  IntersectionObserver: noopObserver, ResizeObserver: global.ResizeObserver,
  screen: { orientation: { addEventListener() {}, removeEventListener() {} } },
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

// SurfaceDriver reports what a single-page fixture shows, and what it shows
// after one tap: the same two questions AppViewModel.routeSmoke answers on
// iOS, printed in the same shape so the two can be compared line for line.
//
// Both halves deliberately read a rendered artefact rather than the model: the
// text comes out of the DOM the renderer built, and the shapes come off the
// canvas element the renderer wired. A comparison of models would agree even
// if one renderer dropped everything on the floor.
const SurfaceDriver = `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));

// The same normalization iOS uses: a SET of trimmed strings, sorted. The DOM
// nests differently from SwiftUI and repeats the title in the nav bar, so only
// the set of words is comparable — the tree shape is not.
function wordsIn(node, out) {
  out = out || new Set();
  node = node || marRoot;
  if (!node) return out;
  const push = (s) => {
    if (typeof s !== 'string') return;
    const t = s.trim();
    if (t) out.add(t);
  };
  // A leaf's textContent is its own words; a container's is its children
  // concatenated, which would add a string no one sees as a unit.
  if (!node.children || node.children.length === 0) push(node.textContent);
  // An <input> carries its words in properties, not in a text node: the
  // placeholder and whatever is typed. Both are on screen, and leaving them
  // out made a text field look empty next to the same field on iOS — the
  // collector under-reading, reported as a drift that was not there.
  push(node.placeholder);
  push(node.value);
  (node.children || []).forEach((c) => wordsIn(c, out));
  return out;
}
// Named apart from the nav driver's screenText, which lives in FakeBrowser
// and collects by a different rule for a different question.
const surfaceWords = () => Array.from(wordsIn()).sort().join(' ¦ ');

// tag(arg,arg) for constructors, [a,b] for lists, literals bare — character
// for character what dumpValue does in AppViewModel.swift.
function dumpValue(v) {
  if (!v || typeof v !== 'object') return '?';
  switch (v.k) {
    case 'I': return String(v.n);
    case 'S': return '"' + v.s + '"';
    case 'B': return v.b ? 'true' : 'false';
    case 'U': return '()';
    case 'L': return '[' + (v.xs || []).map(dumpValue).join(',') + ']';
    case 'T': return '(' + (v.xs || []).map(dumpValue).join(',') + ')';
    // An Angle prints its deci-degrees, the one number both runtimes agree
    // on, so a rotation that came out a tenth of a degree apart is a diff.
    case 'A': return v.deci + 'ddeg';
    case 'C': {
      const bare = canonicalTag(v.tag);
      if (!v.args || v.args.length === 0) return bare;
      return bare + '(' + v.args.map(dumpValue).join(',') + ')';
    }
    default: return '?' + v.k;
  }
}

// Two runtimes, two spellings for the same value, and neither is visible to a
// program: colours are one constructor with three or four arguments on iOS and
// two constructors here, and the canvas text shape is registered under a
// different name on each side. Both are internal bookkeeping — a Mar program
// cannot pattern-match a Shape — so they are folded together HERE rather than
// silently, and the same folding is spelled out in AppViewModel.swift.
//
// Anything else keeps its own tag, minus the module prefix (Add here,
// Canvas.Add there).
function canonicalTag(tag) {
  const bare = tag.indexOf('.') >= 0 ? tag.split('.').pop() : tag;
  if (bare === 'rgb' || bare === 'rgba' || bare === '__Color') return 'color';
  if (bare === 'canvasText') return 'text';
  return bare;
}

// The shapes hang off the canvas ELEMENT the renderer built (el.__marView),
// not off the model — so this reports what was actually handed to the painter.
function findCanvas(node) {
  node = node || marRoot;
  if (!node) return null;
  if (node.tagName === 'CANVAS' && node.__marView) return node.__marView;
  for (const c of node.children || []) {
    const found = findCanvas(c);
    if (found) return found;
  }
  return null;
}
const shapes = () => {
  const view = findCanvas();
  return view ? dumpValue({ k: 'L', xs: view.children || [] }) : null;
};

// The first BUTTON in document order, matching firstMessage() on iOS. Buttons
// specifically: a DOM node cannot say whether a tap sends a message or follows
// a link, so "anything clickable" would pick a different thing on each side
// and report a difference that is not one.
function firstButton(node) {
  node = node || marRoot;
  if (!node) return null;
  if (node.tagName === 'BUTTON' && node._on && node._on.click) return node;
  for (const c of node.children || []) {
    const found = firstButton(c);
    if (found) return found;
  }
  return null;
}

window.marRun(program);

const say = (label, suffix) => {
  console.log('WEBTEXT' + suffix + ' ' + surfaceWords());
  const s = shapes();
  if (s !== null) console.log('WEBSHAPES' + suffix + ' ' + s);
};

say('first', '');
const target = firstButton();
if (target) {
  target.click();
  say('after', '+1');
}
console.log('WEBSURFACE DONE');
// Leave on purpose: a page with a Time.every subscription arms a timer in the
// fake browser and node keeps the process alive while one is pending, so every
// canvas game would hang the harness. Everything to say has been printed.
process.exit(0);
`
