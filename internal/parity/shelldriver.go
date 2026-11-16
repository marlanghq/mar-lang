package parity

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// RunShell boots an HTML shell's own scripts under node, in document order,
// against a DOM fake small enough to read in one sitting, and returns what the
// driver printed.
//
// It exists for the part of the page that runs BEFORE runtime.js, and when
// runtime.js does not run at all: the boot failure screen. FakeBrowser cannot
// cover that, because FakeBrowser is a place to run runtime.js in.
//
// The shell is taken as rendered (the real template, filled in), never as a
// copy of its scripts, so a test cannot pass against code the page no longer
// contains. `scenario` is JS evaluated first; it decides what the network and
// the runtime do:
//
//	scenario.fetch(url)            the page's fetch()
//	scenario.runtime(src, attempt) what loading a runtime <script> did:
//	                               'error' (onerror), 'garbage' (loaded, defined
//	                               nothing), or a function that installs the
//	                               runtime's globals (then onload)
//	scenario.after()               optional, runs once the page has settled
//
// The driver prints a JSON report: the text on screen, the failure screen's
// parts, console.error calls, location.reload calls.
func RunShell(html, scenario string) (string, error) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return "", ErrNoNode
	}
	steps, err := shellSteps(html)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "mar-shell")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	stepsJSON, err := json.Marshal(steps)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "steps.json"), stepsJSON, 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "driver.js"), []byte(shellFake+"\n"+scenario+"\n"+shellDriver), 0o644); err != nil {
		return "", err
	}
	cmd := exec.Command(nodePath, filepath.Join(dir, "driver.js"), filepath.Join(dir, "steps.json"))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", errors.New(err.Error() + "\n" + stderr.String())
	}
	return strings.TrimSpace(string(out)), nil
}

type shellStep struct {
	Kind string `json:"kind"` // inline | src | data
	Code string `json:"code,omitempty"`
	Src  string `json:"src,omitempty"`
	ID   string `json:"id,omitempty"`
}

var scriptTag = regexp.MustCompile(`(?s)<script([^>]*)>(.*?)</script>`)
var attr = regexp.MustCompile(`([a-z-]+)="([^"]*)"`)

// shellSteps lists the page's <script> elements in document order: inline
// code to run, external runtimes to "load", and JSON data blocks the code
// reads by id (the static build's inline program).
func shellSteps(html string) ([]shellStep, error) {
	var steps []shellStep
	for _, m := range scriptTag.FindAllStringSubmatch(html, -1) {
		attrs := map[string]string{}
		for _, a := range attr.FindAllStringSubmatch(m[1], -1) {
			attrs[a[1]] = a[2]
		}
		switch {
		case attrs["src"] != "":
			steps = append(steps, shellStep{Kind: "src", Src: attrs["src"]})
		case attrs["type"] == "application/json":
			steps = append(steps, shellStep{Kind: "data", ID: attrs["id"], Code: m[2]})
		default:
			steps = append(steps, shellStep{Kind: "inline", Code: m[2]})
		}
	}
	if len(steps) == 0 {
		return nil, errors.New("no <script> elements in the shell")
	}
	return steps, nil
}

// The fake. What it has is exactly what the shell's scripts touch; a member
// they read that is missing here throws, rather than answering undefined and
// letting a test pass on a page that would fail.
const shellFake = `
'use strict';
const fs = require('fs');
const report = { errors: [], reloads: 0 };
const docOn = {}, winOn = {};
const dataById = {};

function mkEl(tag) {
  const el = {
    tagName: String(tag).toUpperCase(), children: [], parentNode: null,
    className: '', textContent: '', type: '', id: '', src: '', attrs: {}, _on: {},
    onload: null, onerror: null,
    get firstChild() { return this.children[0] || null; },
    appendChild(c) {
      if (c.parentNode) c.parentNode.removeChild(c);
      this.children.push(c); c.parentNode = this;
      if (c.tagName === 'SCRIPT' && this === document.head) loadScript(c, 2);
      return c;
    },
    removeChild(c) {
      const i = this.children.indexOf(c);
      if (i < 0) throw new Error('removeChild: not a child');
      this.children.splice(i, 1); c.parentNode = null; return c;
    },
    setAttribute(k, v) { this.attrs[k] = String(v); },
    getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; },
    addEventListener(t, f) { (this._on[t] = this._on[t] || []).push(f); },
    click() { (this._on.click || []).forEach((f) => f({ type: 'click' })); },
  };
  return new Proxy(el, { get(t, p) {
    if (typeof p === 'string' && !(p in t) && p !== 'then' && !p.startsWith('__')) {
      throw new Error('shell fake: element has no ' + p + ' (teach the fake, do not let it answer undefined)');
    }
    return t[p];
  } });
}

const root = mkEl('div');
root.id = 'mar-root';
// The loading placeholder the server shell ships inside #mar-root; the
// static shell ships an empty root. Either way the screen must replace it.
const placeholder = mkEl('div');
placeholder.className = 'mar-boot-loading';
placeholder.textContent = 'Loading…';

const document = {
  readyState: 'loading',
  head: null,
  getElementById(id) {
    if (id === 'mar-root') return root;
    if (id in dataById) return dataById[id];
    return null;
  },
  createElement: mkEl,
  addEventListener(t, f) { (docOn[t] = docOn[t] || []).push(f); },
};
document.head = mkEl('head');
globalThis.document = document;
globalThis.window = globalThis;
window.addEventListener = (t, f) => { (winOn[t] = winOn[t] || []).push(f); };
window.location = { reload() { report.reloads++; } };
console.error = (...a) => { report.errors.push(a.map((x) => (x && x.message) || String(x)).join(' ')); };
globalThis.fireWindow = (t) => (winOn[t] || []).forEach((f) => f({ type: t }));
// node has a real fetch; the page must only ever reach the scenario's.
globalThis.fetch = (url) => scenario.fetch(url);

function loadScript(s, attempt) {
  setTimeout(() => {
    const r = scenario.runtime(s.src, attempt);
    if (r === 'error') { if (s.onerror) s.onerror({ type: 'error' }); return; }
    if (typeof r === 'function') r();
    if (s.onload) s.onload({ type: 'load' });
  }, 0);
}

function words(n, out) {
  out = out || [];
  if (n.textContent && n.children.length === 0) out.push(n.textContent);
  n.children.forEach((c) => words(c, out));
  return out;
}
const tick = () => new Promise((r) => setTimeout(r, 0));
`

const shellDriver = `
(async () => {
  const steps = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
  if (steps.some((s) => s.kind === 'src' && s.src.indexOf('/_mar/') === 0)) root.appendChild(placeholder);
  for (const s of steps) {
    if (s.kind === 'data') { const d = mkEl('script'); d.textContent = s.code; dataById[s.id] = d; continue; }
    if (s.kind === 'inline') { (0, eval)(s.code); continue; }
    // A parser-blocking <script src>: it runs (or fails) before the parser
    // moves on, and a failure stops nothing but itself.
    const r = scenario.runtime(s.src, 1);
    if (typeof r === 'function') r();
  }
  await tick();
  if (scenario.beforeReady) scenario.beforeReady();
  document.readyState = 'interactive';
  (docOn.DOMContentLoaded || []).forEach((f) => f({ type: 'DOMContentLoaded' }));
  (winOn.DOMContentLoaded || []).forEach((f) => f({ type: 'DOMContentLoaded' }));
  document.readyState = 'complete';
  for (let i = 0; i < 5; i++) await tick();
  if (scenario.after) { await scenario.after(); for (let i = 0; i < 5; i++) await tick(); }
  const box = root.firstChild;
  const failed = !!(box && box.className === 'mar-boot-failed');
  const part = (cls) => failed ? (box.children.find((c) => c.className === cls) || null) : null;
  const btn = part('mar-boot-failed-reload');
  if (scenario.clickReload && btn) btn.click();
  report.failed = failed;
  report.screen = words(root).join(' | ');
  report.title = part('mar-boot-failed-title') ? part('mar-boot-failed-title').textContent : null;
  report.line = part('mar-boot-failed-line') ? part('mar-boot-failed-line').textContent : null;
  report.button = btn ? btn.textContent : null;
  report.buttonType = btn ? btn.type : null;
  report.detail = part('mar-boot-failed-detail') ? part('mar-boot-failed-detail').textContent : null;
  report.role = failed ? box.getAttribute('role') : null;
  report.lang = failed ? box.getAttribute('lang') : null;
  report.rootChildren = root.children.length;
  report.extra = globalThis.extra || null;
  process.stdout.write(JSON.stringify(report));
})().catch((e) => { process.stderr.write(String(e && e.stack || e)); process.exit(1); });
`
