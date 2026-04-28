package jsserve

import (
	"io"
	"net/http"
	"strings"

	"mar/internal/runtime"
)

// dispatchBackend mirrors the routing logic in runtime/server.go: matches
// by method + path (with :name params), invokes the handler, runs the
// resulting Effect, writes the Response.
//
// Used by ServeLive's /api/* handler. The routes slice is read from the
// LiveProgram on every request, so a hot-reload swap takes effect on the
// next call.
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
