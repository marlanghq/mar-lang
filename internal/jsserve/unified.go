package jsserve

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"mar/internal/ast"
	"mar/internal/runtime"
)

// ServeUnified runs a single HTTP server that serves both the backend's
// routes (under /api) and the frontend's MVU bundle (at / and /_mar/*).
//
// backendRoutes:  the runtime list of routes (records with method/path/handler)
//                 already evaluated from the backend module(s).
// frontendMods:   the frontend modules whose AST will be sent to the browser.
//                 The JS runtime evaluates these and looks up the entry value.
// frontendEntry:  name of the value the JS runtime should run after loading.
//                 Conventionally "main" but for an app we look up the screens
//                 list and start App.serveScreens client-side.
func ServeUnified(port int, backendRoutes []runtime.Value, frontendMods []*ast.Module, frontendEntry string) error {
	merged := mergeModules(frontendMods)
	progJSON, err := json.Marshal(map[string]any{
		"module": SerializeModule(merged),
		"entry":  frontendEntry,
	})
	if err != nil {
		return err
	}
	title := "mar app"
	if len(merged.Name) > 0 {
		title = merged.Name[len(merged.Name)-1]
	}
	indexHTML := fmt.Sprintf(pageHTML, title)

	mux := http.NewServeMux()

	// /_mar/ — runtime.js and program.json
	mux.HandleFunc("/_mar/runtime.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = io.WriteString(w, runtimeJS)
	})
	mux.HandleFunc("/_mar/program.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(progJSON)
	})

	// /api/ — backend routes (delegate to the same dispatcher Server.serve uses)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		// strip /api prefix for matching against route paths
		stripped := strings.TrimPrefix(r.URL.Path, "/api")
		if stripped == "" {
			stripped = "/"
		}
		dispatchBackend(backendRoutes, stripped, w, r)
	})

	// /  — fall back to the SPA shell so the browser router can take over.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, indexHTML)
	})

	// Patch the embedded HTML page to load assets from /_mar/ instead of /.
	indexHTML = strings.Replace(indexHTML, `src="/__runtime.js"`, `src="/_mar/runtime.js"`, 1)
	indexHTML = strings.Replace(indexHTML, `'/__program.json'`, `'/_mar/program.json'`, 1)

	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("[mar] App on http://localhost%s\n", addr)
	fmt.Printf("       backend: /api/*    frontend: /    runtime: /_mar/*\n")
	return http.ListenAndServe(addr, mux)
}

// dispatchBackend mirrors the routing logic in runtime/server.go:
// matches by method + path (with :name params), invokes the handler, runs
// the resulting Effect, writes the Response.
func dispatchBackend(routes []runtime.Value, urlPath string, w http.ResponseWriter, req *http.Request) {
	reqSegs := splitPath(urlPath)
	for _, rv := range routes {
		r, ok := rv.(runtime.VRecord)
		if !ok {
			continue
		}
		method, _ := r.Fields["method"].(runtime.VString)
		path, _ := r.Fields["path"].(runtime.VString)
		if method.V != req.Method {
			continue
		}
		params, ok := matchPath(splitPath(path.V), reqSegs)
		if !ok {
			continue
		}
		body, _ := io.ReadAll(req.Body)
		reqVal := buildRequestValue(req, body, params)
		handler := r.Fields["handler"]
		effVal, err := runtime.Apply(handler, reqVal)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		eff, ok := effVal.(runtime.VEffect)
		if !ok {
			http.Error(w, "handler did not return an Effect", http.StatusInternalServerError)
			return
		}
		result, err := eff.Run()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp, ok := result.(runtime.VRecord)
		if !ok {
			http.Error(w, "handler effect did not produce a Response record", http.StatusInternalServerError)
			return
		}
		status, _ := resp.Fields["status"].(runtime.VInt)
		rbody, _ := resp.Fields["body"].(runtime.VString)
		w.WriteHeader(int(status.V))
		_, _ = io.WriteString(w, rbody.V)
		return
	}
	http.NotFound(w, req)
}

// splitPath / matchPath / buildRequestValue duplicate small bits from
// runtime/server.go because that package's helpers are unexported.
// (Future cleanup: move them to a shared package.)

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func matchPath(routeSegs, reqSegs []string) (map[string]string, bool) {
	if len(routeSegs) != len(reqSegs) {
		return nil, false
	}
	params := map[string]string{}
	for i, rs := range routeSegs {
		if strings.HasPrefix(rs, ":") {
			params[rs[1:]] = reqSegs[i]
			continue
		}
		if rs != reqSegs[i] {
			return nil, false
		}
	}
	return params, true
}

func buildRequestValue(req *http.Request, body []byte, params map[string]string) runtime.VRecord {
	paramsCopy := make(map[string]string, len(params))
	for k, v := range params {
		paramsCopy[k] = v
	}
	paramsLookup := runtime.VFn{
		Arity: 1,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			name, ok := args[0].(runtime.VString)
			if !ok {
				return runtime.VCtor{Tag: "Nothing"}, nil
			}
			if v, ok := paramsCopy[name.V]; ok {
				return runtime.VCtor{Tag: "Just", Args: []runtime.Value{runtime.VString{V: v}}}, nil
			}
			return runtime.VCtor{Tag: "Nothing"}, nil
		},
	}
	return runtime.VRecord{
		Fields: map[string]runtime.Value{
			"url":    runtime.VString{V: req.URL.String()},
			"method": runtime.VString{V: req.Method},
			"body":   runtime.VString{V: string(body)},
			"params": paramsLookup,
		},
		Order: []string{"url", "method", "body", "params"},
	}
}
