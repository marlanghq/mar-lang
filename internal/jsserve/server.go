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

// HTML page template. Loads the runtime, then the AST, then runs `main`.
// Asset paths are stable (`/_mar/...`) so hot-reload's SSE channel sits
// alongside them without colliding with user routes.
const pageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>%s</title>
<style>
  body { font-family: system-ui, sans-serif; margin: 2rem; }
  button { padding: 0.4rem 0.9rem; margin-right: 0.3rem; }
  input, textarea { padding: 0.4rem; }
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
