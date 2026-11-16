package jsserve

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"mar/internal/parity"
)

// The browser's boot failure screen. A program.json that answers 500, comes
// back invalid, or never arrives, and a runtime.js that does not download or
// does not parse, each end on the screen rather than on "Loading…". The phone
// shows FailedView with a Retry for the same failures.
//
// These run the shell AS RENDERED (renderShell, the real template) under node,
// against parity.RunShell's small DOM fake. What they cannot prove is the
// paint: CSS, both color schemes, a phone width. That was checked in a real
// browser, by hand, and is not automatable here.

type shellReport struct {
	Failed       bool     `json:"failed"`
	Screen       string   `json:"screen"`
	Title        *string  `json:"title"`
	Line         *string  `json:"line"`
	Button       *string  `json:"button"`
	ButtonType   *string  `json:"buttonType"`
	Detail       *string  `json:"detail"`
	Role         *string  `json:"role"`
	Lang         *string  `json:"lang"`
	Errors       []string `json:"errors"`
	Reloads      int      `json:"reloads"`
	RootChildren int      `json:"rootChildren"`
	Extra        any      `json:"extra"`
}

func renderedShell(t *testing.T, dev bool) string {
	t.Helper()
	lp := &LiveProgram{}
	lp.SetDevMode(dev)
	rec := httptest.NewRecorder()
	renderShell(rec, lp)
	return rec.Body.String()
}

func runShell(t *testing.T, html, scenario string) shellReport {
	t.Helper()
	out, err := parity.RunShell(html, scenario)
	if errors.Is(err, parity.ErrNoNode) {
		t.Skip("node is not installed")
	}
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var r shellReport
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("report: %v\n%s", err, out)
	}
	return r
}

// The runtimes a scenario can hand the page. healthyRuntime stands in for
// runtime.js's marBootstrap and keeps only what the shell depends on: it reads
// the shell's promise and reports a rejection through __marBootFailed.
// TestMarBootstrapReportsThroughTheShell holds the real one to that.
const healthyRuntime = `() => {
  globalThis.extra = { boots: 0 };
  globalThis.marBootstrap = function () {
    globalThis.extra.boots++;
    window.__marProgramPromise.then(function (p) { globalThis.extra.program = p; },
      function (e) { window.__marBootFailed(e); });
  };
}`

const okProgram = `() => Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({ modules: [] }) })`

func scenario(fetch, runtime string, extra ...string) string {
	return "const scenario = { fetch: " + fetch + ", runtime: " + runtime + ", " + strings.Join(extra, ", ") + " };"
}

func TestBootFailureScreen(t *testing.T) {
	cases := []struct {
		name     string
		scenario string
		failed   bool
		detail   string // substring of the dev-only detail; "" when !failed
	}{
		{"healthy boot draws nothing",
			scenario(okProgram, `(src, n) => (`+healthyRuntime+`)`), false, ""},
		{"program.json answers 500",
			scenario(`() => Promise.resolve({ ok: false, status: 500, json: () => Promise.reject(new SyntaxError('Unexpected token i')) })`,
				`(src, n) => (`+healthyRuntime+`)`), true, "HTTP 500"},
		{"program.json is not JSON",
			scenario(`() => Promise.resolve({ ok: true, status: 200, json: () => Promise.reject(new SyntaxError('Unexpected end of JSON input')) })`,
				`(src, n) => (`+healthyRuntime+`)`), true, "Unexpected end of JSON input"},
		{"program.json never arrives",
			scenario(`() => Promise.reject(new TypeError('Failed to fetch'))`,
				`(src, n) => (`+healthyRuntime+`)`), true, "Failed to fetch"},
		{"runtime.js does not download, twice",
			scenario(okProgram, `(src, n) => 'error'`), true, "could not download /_mar/runtime.js"},
		{"runtime.js downloads but does not run, twice",
			scenario(okProgram, `(src, n) => 'garbage'`), true, "did not run"},
		{"runtime.js fails once, the fresh retry works",
			scenario(okProgram, `(src, n) => n === 1 ? 'error' : (`+healthyRuntime+`)`), false, ""},
		{"marBootstrap throws",
			scenario(okProgram, `(src, n) => () => { globalThis.marBootstrap = () => { throw new Error('boom in bootstrap'); }; }`), true, "boom in bootstrap"},
		{"program.json and runtime.js both fail: one screen, one log",
			scenario(`() => Promise.reject(new TypeError('Failed to fetch'))`, `(src, n) => 'error'`), true, "Failed to fetch"},
	}
	for _, dev := range []bool{true, false} {
		html := renderedShell(t, dev)
		for _, c := range cases {
			name := c.name
			if dev {
				name += " (mar dev)"
			}
			t.Run(name, func(t *testing.T) {
				r := runShell(t, html, c.scenario)
				if r.Failed != c.failed {
					t.Fatalf("failure screen shown = %v, want %v; screen: %q, console: %q", r.Failed, c.failed, r.Screen, r.Errors)
				}
				if !c.failed {
					if len(r.Errors) != 0 {
						t.Errorf("a boot that worked logged errors: %q", r.Errors)
					}
					if r.Screen != "Loading…" {
						t.Errorf("a boot that worked changed #mar-root before the app mounted: %q", r.Screen)
					}
					// Quiet is not enough: a shell that never starts the
					// runtime is quiet too. It has to have booted, once.
					extra, _ := r.Extra.(map[string]any)
					if extra == nil || extra["boots"] != float64(1) || extra["program"] == nil {
						t.Errorf("marBootstrap ran %v time(s) with program %v, want once with the fetched program", extra["boots"], extra["program"])
					}
					return
				}
				// The words, exactly: they are user-facing copy (no em-dashes,
				// no jargon), and a change to them is a decision, not a refactor.
				if r.Title == nil || *r.Title != "This app couldn't load." {
					t.Errorf("title = %v", r.Title)
				}
				if r.Line == nil || *r.Line != "Check your connection and try again." {
					t.Errorf("line = %v", r.Line)
				}
				if r.Button == nil || *r.Button != "Reload" || r.ButtonType == nil || *r.ButtonType != "button" {
					t.Errorf("button = %v type %v", r.Button, r.ButtonType)
				}
				if r.Role == nil || *r.Role != "alert" {
					t.Errorf("role = %v: a screen reader has to hear that the load failed", r.Role)
				}
				if r.Lang == nil || *r.Lang != "en" {
					t.Errorf("lang = %v: the copy is English whatever the app's locale", r.Lang)
				}
				if r.RootChildren != 1 || strings.Contains(r.Screen, "Loading") {
					t.Errorf("the failure must replace the loading placeholder, not sit beside it: %q", r.Screen)
				}
				if len(r.Errors) != 1 {
					t.Errorf("console.error calls = %d, want 1 (the cause, once): %q", len(r.Errors), r.Errors)
				}
				if dev {
					if r.Detail == nil || !strings.Contains(*r.Detail, c.detail) {
						t.Errorf("mar dev detail = %v, want it to mention %q", r.Detail, c.detail)
					}
				} else if r.Detail != nil {
					t.Errorf("a deployed app showed the technical detail %q: that belongs in the console", *r.Detail)
				}
			})
		}
	}
}

// Leaving the page aborts its requests, and Chrome delivers the rejection to
// the page during the pagehide dispatch (measured). That is not a failure of
// the app, so nothing may be drawn or logged.
func TestBootFailureIsQuietWhileLeaving(t *testing.T) {
	r := runShell(t, renderedShell(t, true), scenario(
		`() => new Promise((_, reject) => { globalThis.abortProgram = () => reject(new TypeError('Failed to fetch')); })`,
		`(src, n) => (`+healthyRuntime+`)`,
		`after: async () => { fireWindow('pagehide'); abortProgram(); }`))
	if r.Failed || len(r.Errors) != 0 {
		t.Fatalf("an abort on the way out drew the failure screen (%v) or logged %q", r.Failed, r.Errors)
	}
}

// A failure that arrives while the document is still parsing (program.json
// refused before the body exists) is drawn once #mar-root is there, not lost.
func TestBootFailureBeforeTheBodyIsDrawnLater(t *testing.T) {
	r := runShell(t, renderedShell(t, false), scenario(
		`() => Promise.resolve({ ok: false, status: 503, json: () => Promise.reject(new Error('x')) })`,
		`(src, n) => 'error'`))
	if !r.Failed {
		t.Fatalf("not drawn: %q", r.Screen)
	}
}

func TestBootFailureReloadButtonReloads(t *testing.T) {
	r := runShell(t, renderedShell(t, false), scenario(
		`() => Promise.reject(new TypeError('Failed to fetch'))`,
		`(src, n) => (`+healthyRuntime+`)`,
		`clickReload: true`))
	if r.Reloads != 1 {
		t.Fatalf("Reload pressed, location.reload() called %d times", r.Reloads)
	}
}

// The admin panel is served from the same template with its own program URL,
// so it gets the same screen; it never shows the detail (it is framework UI,
// and it runs in production).
func TestAdminShellHasTheBootFailureScreen(t *testing.T) {
	rec := httptest.NewRecorder()
	handleAdminMarShell(rec, httptest.NewRequest("GET", "/_mar/admin", nil))
	html := rec.Body.String()
	if !strings.Contains(html, "fetch('/_mar/admin/program.json')") {
		t.Fatal("admin shell no longer fetches its own program")
	}
	r := runShell(t, html, scenario(
		`(url) => url === '/_mar/admin/program.json' ? Promise.resolve({ ok: false, status: 500, json: () => Promise.reject(new Error('x')) }) : Promise.reject(new Error('wrong url ' + url))`,
		`(src, n) => (`+healthyRuntime+`)`))
	if !r.Failed || r.Detail != nil {
		t.Fatalf("admin shell: failed=%v detail=%v screen=%q", r.Failed, r.Detail, r.Screen)
	}
}

// The real runtime.js keeps the contract the shell relies on: everything that
// stops a program from starting reaches window.__marBootFailed, and a program
// that starts never does.
func TestMarBootstrapReportsThroughTheShell(t *testing.T) {
	good, err := parity.Compile(`module Main exposing (main)


main : Cmd ()
main =
    Cmd.none
`, SerializeModule)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cases := []struct {
		name, promise, want string
	}{
		{"a rejected program.json", `Promise.reject(new Error('/_mar/program.json answered HTTP 500'))`, "HTTP 500"},
		{"a program the runtime refuses", `Promise.resolve({ modules: [{ decls: [{ kind: 'EFromTheFuture' }] }] })`, "unknown node kind"},
		{"a program without its entry", `Promise.resolve(Object.assign({}, program, { entry: 'Main.nowhere' }))`, "entry not found"},
		{"a program that starts", `Promise.resolve(program)`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := parity.RunWeb(runtimeJS, good, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const reported = [];
window.__marBootFailed = (e) => reported.push(String(e && e.message || e));
window.__marProgramPromise = `+c.promise+`;
window.marBootstrap();
setTimeout(() => process.stdout.write(JSON.stringify(reported)), 20);
`)
			if errors.Is(err, parity.ErrNoNode) {
				t.Skip("node not installed")
			}
			if err != nil {
				t.Fatalf("node: %v", err)
			}
			var reported []string
			if err := json.Unmarshal([]byte(out), &reported); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			if c.want == "" {
				if len(reported) != 0 {
					t.Fatalf("a program that started was reported as a failure: %q", reported)
				}
				return
			}
			if len(reported) != 1 || !strings.Contains(reported[0], c.want) {
				t.Fatalf("reported %q, want one report mentioning %q", reported, c.want)
			}
		})
	}
}
