package jsserve

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"mar/internal/ast"
)

//go:embed runtime.js
var runtimeJS string

// HTML page template. Loads the runtime, then the AST, then runs `main`.
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
<script src="/__runtime.js"></script>
<script>
window.addEventListener('DOMContentLoaded', function () {
  fetch('/__program.json').then(function (r) { return r.json(); }).then(function (p) {
    try { marRun(p); }
    catch (e) {
      var root = document.getElementById('mar-root');
      root.innerHTML = '<pre style="color:#b00">' + (e.message || e) + '</pre>';
      console.error(e);
    }
  });
});
</script>
</body>
</html>`

// Serve serves the embedded runtime, the program AST, and the host page on
// the given port. Blocks until the server stops.
func Serve(port int, mod *ast.Module, entry string) error {
	progJSON, err := json.Marshal(map[string]any{
		"module": SerializeModule(mod),
		"entry":  entry,
	})
	if err != nil {
		return err
	}

	title := "mar app"
	if len(mod.Name) > 0 {
		title = mod.Name[len(mod.Name)-1]
	}
	indexHTML := fmt.Sprintf(pageHTML, title)

	mux := http.NewServeMux()
	mux.HandleFunc("/__runtime.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = io.WriteString(w, runtimeJS)
	})
	mux.HandleFunc("/__program.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(progJSON)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, indexHTML)
	})

	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("[mar] Browser app on http://localhost%s\n", addr)
	return http.ListenAndServe(addr, mux)
}
