package jsserve

import (
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"strings"

	"mar/internal/ast"
)

//go:embed runtime.js
var runtimeJS string

// RuntimeJS exposes the embedded JS runtime source — used by `mar build`
// to write runtime.js into a static dist/.
func RuntimeJS() string { return runtimeJS }

// HTML page template. Loads the runtime, then the AST, then runs `main`.
// Asset paths are stable (`/_mar/...`) so hot-reload's SSE channel sits
// alongside them without colliding with user routes.
const pageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
  /* Reasonable defaults so the bare view DSL renders looking like a
     real app, not raw browser stock. The DSL itself stays sparse —
     these styles target the HTML tags the runtime emits. */

  *, *::before, *::after { box-sizing: border-box; }

  :root {
    --fg: #1a1a1a;
    --fg-muted: #666;
    --bg: #fafafa;
    --surface: #fff;
    --border: #e2e2e2;
    --accent: #2563eb;
    --accent-fg: #fff;
    --radius: 6px;
    --gap: 0.5rem;
  }

  html, body { margin: 0; padding: 0; }
  body {
    /* Defaults are neutral — full viewport, content shrink, top-left.
       Apps that want a max-width "page" feel will opt in once the
       layout attributes (View.fill, View.center, etc.) land. */
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
    font-size: 15px;
    line-height: 1.4;
    color: var(--fg);
    background: var(--bg);
    padding: 1.5rem;
  }

  /* Typography (View.title / .subtitle / .text) */
  h1 { font-size: 1.75rem; font-weight: 700; margin: 0 0 0.5rem; }
  h2 { font-size: 1.1rem; font-weight: 600; margin: 1rem 0 0.4rem; color: var(--fg); }
  span { display: inline; }

  /* Buttons (View.button) */
  button {
    appearance: none;
    border: 1px solid var(--border);
    background: var(--surface);
    color: var(--fg);
    padding: 0.4rem 0.9rem;
    border-radius: var(--radius);
    font: inherit;
    cursor: pointer;
    transition: background 0.1s ease, border-color 0.1s ease;
  }
  button:hover { background: #f0f0f0; }
  button:active { background: #e7e7e7; }
  button:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }

  /* Inputs (View.input / View.textarea) */
  input[type="text"], textarea {
    border: 1px solid var(--border);
    background: var(--surface);
    color: var(--fg);
    padding: 0.45rem 0.6rem;
    border-radius: var(--radius);
    font: inherit;
    width: 100%%;
    max-width: 24rem;
  }
  input[type="text"]:focus, textarea:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px rgba(37, 99, 235, 0.15);
  }
  textarea { min-height: 4.5rem; resize: vertical; }

  /* Links (View.link) */
  a { color: var(--accent); text-decoration: none; }
  a:hover { text-decoration: underline; }

  /* Lists (View.list / View.keyedList) — drop the native bullets and
     give items breathing room without forcing a particular look. */
  ul {
    list-style: none;
    padding: 0;
    margin: 0;
  }
  li { padding: 0.35rem 0; }
  li + li { border-top: 1px solid var(--border); }

  /* Containers (View.section / .row / .column) get a touch of vertical
     rhythm. Children are content-sized (align-items: flex-start) — that
     lives in the runtime CSS so the DSL semantics stay shrink-by-default. */
  section { padding: 1rem 0; }

  /* Mount point: a flex column that fills the viewport height (minus
     body padding). This lets View.center / View.centerY actually center
     a top-level view on the page — without it, the column would have
     no inherent height to center within. */
  #mar-root {
    display: flex;
    flex-direction: column;
    min-height: calc(100vh - 3rem);
  }
</style>
</head>
<body>
<div id="mar-root"></div>
<script src="/_mar/runtime.js"></script>
<script>
window.addEventListener('DOMContentLoaded', function () {
  marBootstrap();
});
</script>
</body>
</html>`

// ServeLive runs the dev server backed by a LiveProgram (whose contents
// can change at runtime via hot-reload) and a ReloadHub (broadcasts
// "reload" events to connected browsers via SSE).
//
// hasAPI controls whether /api/* requests get dispatched to the backend
// routes inside lp. Browser-only mode (App.serve) sets it to false; the
// full-stack mode (App.fullstack) sets it to true. lp.hasAPI is also
// updated on every reload but the mux registration is fixed at startup,
// so this flag pins the routing topology.
func ServeLive(port int, lp *LiveProgram, hub *ReloadHub) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/_mar/runtime.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = io.WriteString(w, runtimeJS)
	})
	mux.HandleFunc("/_mar/program.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store") // dev mode — never cache
		_, _ = w.Write(lp.ProgramJSON())
	})
	mux.HandleFunc("/_mar/reload", hub.ServeReload)

	if lp.HasAPI() {
		mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
			stripped := strings.TrimPrefix(r.URL.Path, "/api")
			if stripped == "" {
				stripped = "/"
			}
			dispatchBackend(lp.Routes(), stripped, w, r)
		})
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, pageHTML, lp.Title())
	})

	addr := fmt.Sprintf(":%d", port)
	if lp.HasAPI() {
		fmt.Printf("[mar] App on http://localhost%s\n", addr)
		fmt.Printf("       backend: /api/*    frontend: /    runtime: /_mar/*\n")
	} else {
		fmt.Printf("[mar] Browser app on http://localhost%s\n", addr)
	}
	fmt.Printf("       hot reload: /_mar/reload (SSE)\n")
	return http.ListenAndServe(addr, mux)
}

// mergeModules concatenates the decls of multiple modules into a single
// virtual module. Names across modules are exposed as both "name" (bare) and
// "Module.name" (qualified) by the runtime's loader.
func mergeModules(mods []*ast.Module) *ast.Module {
	if len(mods) == 1 {
		return mods[0]
	}
	out := &ast.Module{}
	for _, m := range mods {
		out.Decls = append(out.Decls, m.Decls...)
		if len(out.Name) == 0 {
			out.Name = m.Name
		}
	}
	return out
}
