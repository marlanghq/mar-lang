package scaffold

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mar/internal/ast"
	"mar/internal/jsserve"
	"mar/internal/project"
	"mar/internal/runtime"
)

// Build compiles a mar project to a static dist/ that can be served
// by any HTTP server (CDN, S3, Cloudflare, GitHub Pages, etc.).
//
// Output layout:
//
//	dist/
//	  index.html            -- host page with no dev affordances
//	  runtime.js            -- the embedded JS interpreter
//	  program.json          -- serialized AST + entry name
//
// Backend / fullstack deployment isn't covered here yet — that needs
// a Go binary embedding mar runtime + manifest, which is a separate
// piece of work. For now `mar build` targets frontend-only apps that
// produce static output servable as files. If the project's main is
// App.fullstack, build emits a clear error.
func Build(projectDir, distDir string) error {
	mainFile := filepath.Join(projectDir, "Main.mar")
	if _, err := os.Stat(mainFile); err != nil {
		return fmt.Errorf("Main.mar not found in %s", projectDir)
	}

	// Same load + override pattern as `mar dev`, but the overrides
	// capture into a build context instead of a live server.
	bc := &buildCtx{}
	rEnv, _, err := project.LoadIntoEnvWithModulesAndHook(mainFile,
		func(env *runtime.Env, mods []*ast.Module) {
			fe := makeFrontendCapture(mods, bc)
			env.Define("appFrontend", fe)
			env.Define("App.frontend", fe)

			be := makeBackendCapture(bc)
			env.Define("appBackend", be)
			env.Define("App.backend", be)

			fs := makeFullstackCapture(mods, bc)
			env.Define("appFullstack", fs)
			env.Define("App.fullstack", fs)
		})
	if err != nil {
		return err
	}
	mainVal, ok := rEnv.Lookup("Main.main")
	if !ok {
		mainVal, ok = rEnv.Lookup("main")
	}
	if !ok {
		return fmt.Errorf("Main.mar must export `main`")
	}
	eff, ok := mainVal.(runtime.VEffect)
	if !ok {
		return fmt.Errorf("main is not an Effect (got %T)", mainVal)
	}
	if _, err := eff.Run(); err != nil {
		return err
	}

	// Refuse to build apps that need a backend — for those, `mar dev`
	// is currently the only deployable target. (Production backend
	// bundling is a separate work item.)
	if bc.kind == kindBackend {
		return fmt.Errorf("App.backend cannot be built to a static directory — backend apps need a live server. Run with `mar dev` for now.")
	}
	if bc.kind == kindFullstack {
		return fmt.Errorf("App.fullstack cannot be built to a static directory — the API needs a live server. Run with `mar dev`, or split the frontend into a separate App.frontend project that targets a static host.")
	}
	if bc.kind != kindFrontend {
		return fmt.Errorf("main didn't call any of App.frontend / App.backend / App.fullstack — nothing to build")
	}

	// Emit dist/.
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		return err
	}
	progJSON, err := makeProgramJSON(bc.frontMods, "main")
	if err != nil {
		return err
	}
	html := buildIndexHTML(distDir, bc.title)
	files := map[string][]byte{
		"index.html":   []byte(html),
		"runtime.js":   []byte(jsserve.RuntimeJS()),
		"program.json": progJSON,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(distDir, name), content, 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("[mar build] wrote %d files to %s\n", len(files), distDir)
	return nil
}

// --- internals: capture overrides ---

type buildKind int

const (
	kindUnset buildKind = iota
	kindFrontend
	kindBackend
	kindFullstack
)

// buildCtx is the build-time analog of jsserve.LiveProgram. The override
// builtins write into it; Build reads it after evaluating Main.main.
type buildCtx struct {
	kind      buildKind
	frontMods []*ast.Module
	title     string
}

func makeFrontendCapture(mods []*ast.Module, bc *buildCtx) runtime.Value {
	return runtime.VFn{
		Arity: 1,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			pageList, ok := args[0].(runtime.VList)
			if !ok {
				return nil, fmt.Errorf("App.frontend: expected List Page (got %T)", args[0])
			}
			roots := map[string]bool{}
			for i, pv := range pageList.Elements {
				page, ok := pv.(runtime.VPage)
				if !ok {
					return nil, fmt.Errorf("page %d is not a Page (got %T)", i, pv)
				}
				if page.OriginName == "" {
					return nil, fmt.Errorf("page %d has no provenance — pages must be top-level bindings", i)
				}
				roots[page.OriginModule] = true
			}
			merged := []*ast.Module{}
			seen := map[string]bool{}
			for root := range roots {
				for _, m := range reachableFrom(root, mods) {
					name := joinName(m.Name)
					if seen[name] {
						continue
					}
					seen[name] = true
					merged = append(merged, m)
				}
			}
			bc.kind = kindFrontend
			bc.frontMods = merged
			if len(merged) > 0 {
				nm := merged[len(merged)-1].Name
				if len(nm) > 0 {
					bc.title = nm[len(nm)-1]
				}
			}
			return runtime.VEffect{Tag: "appFrontend", Run: func() (runtime.Value, error) {
				return runtime.VUnit{}, nil
			}}, nil
		},
	}
}

func makeBackendCapture(bc *buildCtx) runtime.Value {
	return runtime.VFn{
		Arity: 1,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			bc.kind = kindBackend
			return runtime.VEffect{Tag: "appBackend", Run: func() (runtime.Value, error) {
				return runtime.VUnit{}, nil
			}}, nil
		},
	}
}

func makeFullstackCapture(mods []*ast.Module, bc *buildCtx) runtime.Value {
	return runtime.VFn{
		Arity: 1,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			bc.kind = kindFullstack
			return runtime.VEffect{Tag: "appFullstack", Run: func() (runtime.Value, error) {
				return runtime.VUnit{}, nil
			}}, nil
		},
	}
}

// reachableFrom + joinName are tiny duplicates of helpers in cmd/mar
// and project — kept here to avoid circular imports.
func reachableFrom(startModule string, mods []*ast.Module) []*ast.Module {
	byName := map[string]*ast.Module{}
	for _, m := range mods {
		byName[joinName(m.Name)] = m
	}
	visited := map[string]bool{}
	var order []*ast.Module
	var visit func(name string)
	visit = func(name string) {
		if visited[name] {
			return
		}
		visited[name] = true
		mod, ok := byName[name]
		if !ok {
			return
		}
		for _, imp := range mod.Imports {
			visit(joinName(imp.Module))
		}
		order = append(order, mod)
	}
	visit(startModule)
	return order
}

func joinName(parts []string) string {
	return strings.Join(parts, ".")
}

// makeProgramJSON serializes the merged frontend modules as the
// browser bundle.
func makeProgramJSON(mods []*ast.Module, entry string) ([]byte, error) {
	merged := mergeModules(mods)
	return json.Marshal(map[string]any{
		"module": jsserve.SerializeModule(merged),
		"entry":  entry,
	})
}

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

// buildIndexHTML produces the production HTML page. Differences from
// the dev version: no SSE reload connection, no dev banner — just a
// clean page that boots marRun on DOMContentLoaded.
func buildIndexHTML(distDir, title string) string {
	if title == "" {
		title = "mar app"
	}
	return fmt.Sprintf(productionPageHTML, title)
}

const productionPageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
  *, *::before, *::after { box-sizing: border-box; }
  :root {
    --fg: #1a1a1a; --bg: #fafafa; --surface: #fff; --border: #e2e2e2;
    --accent: #2563eb; --radius: 6px;
  }
  html, body { margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
    font-size: 15px; line-height: 1.4;
    color: var(--fg); background: var(--bg);
    padding: 1.5rem;
  }
  h1 { font-size: 1.75rem; font-weight: 700; margin: 0 0 0.5rem; }
  h2 { font-size: 1.1rem; font-weight: 600; margin: 1rem 0 0.4rem; }
  button {
    appearance: none; border: 1px solid var(--border);
    background: var(--surface); color: var(--fg);
    padding: 0.4rem 0.9rem; border-radius: var(--radius);
    font: inherit; cursor: pointer;
  }
  button:hover { background: #f0f0f0; }
  input[type="text"], textarea {
    border: 1px solid var(--border); background: var(--surface);
    padding: 0.45rem 0.6rem; border-radius: var(--radius);
    font: inherit; width: 100%%; max-width: 24rem;
  }
  textarea { min-height: 4.5rem; resize: vertical; }
  a { color: var(--accent); text-decoration: none; }
  a:hover { text-decoration: underline; }
  ul { list-style: none; padding: 0; margin: 0; }
  li { padding: 0.35rem 0; }
  li + li { border-top: 1px solid var(--border); }
  section { padding: 1rem 0; }
  #mar-root {
    display: flex; flex-direction: column;
    min-height: calc(100vh - 3rem);
  }
</style>
</head>
<body>
<div id="mar-root"></div>
<script src="./runtime.js"></script>
<script>
window.addEventListener('DOMContentLoaded', function () {
  fetch('./program.json').then(function (r) { return r.json(); }).then(function (p) {
    try { marRun(p); }
    catch (e) {
      var root = document.getElementById('mar-root');
      var pre = document.createElement('pre');
      pre.style.color = '#b00';
      pre.textContent = String(e && e.message || e);
      root.appendChild(pre);
    }
  });
});
</script>
</body>
</html>`
