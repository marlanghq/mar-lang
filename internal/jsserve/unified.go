package jsserve

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	"mar/internal/runtime"
)

// maxBodyBytes is the per-request body cap applied to /api/* and
// /services/*. Loaded from mar.json["server"]["maxBodyBytes"] at
// startup via SetMaxBodyBytes. The default (1 MiB, project.DefaultMaxBodyBytes)
// is set here so the package is safe to use even if the CLI didn't
// install one (test paths). Atomic so we don't need a mutex on the
// read path, which is hit per-request.
//
// Secure-by-default: there is no "off" value — a missing or zero
// configured value falls back to the default. Validation in the
// project package guarantees user-configured values are in bounds
// before they ever reach here.
var maxBodyBytes atomic.Int64

func init() {
	// 1 MiB — same constant as project.DefaultMaxBodyBytes. Duplicated
	// here (rather than imported) to avoid a project → jsserve back-
	// reference; the values are pinned by tests in both packages.
	maxBodyBytes.Store(1 << 20)
}

// SetMaxBodyBytes installs the per-request body cap. Called once at
// boot from the CLI after mar.json is loaded. Values outside the
// validator-enforced range are pinned to the default — a defensive
// fallback if a caller ever bypasses Validate (e.g. test code).
func SetMaxBodyBytes(n int64) {
	if n < 1<<10 || n > 32<<20 { // [1 KiB, 32 MiB] — matches project bounds
		n = 1 << 20
	}
	maxBodyBytes.Store(n)
}

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
		// Cap the body. http.MaxBytesReader returns *http.MaxBytesError
		// once the limit is hit; we surface that as 413 and bail. Without
		// this cap, a single client sending Content-Length: 10GB would
		// exhaust server memory before the handler runs (and the cap
		// applies even when the client lies about Content-Length — the
		// reader counts actual bytes).
		req.Body = http.MaxBytesReader(w, req.Body, maxBodyBytes.Load())
		body, err := io.ReadAll(req.Body)
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "read request body", http.StatusBadRequest)
			return
		}
		reqVal := buildRequestValue(req, body, params)
		// If the route was produced by Auth.protected, attach the loaded
		// user (or Nothing) under the well-known `__user` field so the
		// service wrapper can curry it in. The wrapper short-circuits to
		// 401 if Nothing.
		if requiresUser(r) {
			reqVal = withUser(reqVal, loadUserForRequest(req))
		}
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

// requiresUser checks the route record's `requiresUser` flag (set by
// Auth.protected via runtime.ExposedServiceToRoute).
func requiresUser(r runtime.VRecord) bool {
	b, _ := r.Fields["requiresUser"].(runtime.VBool)
	return b.V
}

// loadUserForRequest reads the session token (cookie or Bearer header),
// validates the session, loads the user record, returns it as
// Just/Nothing. Anything missing or invalid → Nothing (the service
// wrapper turns that into 401).
func loadUserForRequest(req *http.Request) runtime.Value {
	cfg := runtime.CurrentAuth()
	secret := AuthSecret()
	if cfg == nil || secret == "" {
		return runtime.VCtor{Tag: "Nothing"}
	}
	tok := extractSessionToken(req)
	if tok == "" {
		return runtime.VCtor{Tag: "Nothing"}
	}
	db, err := dbHandle()
	if err != nil {
		return runtime.VCtor{Tag: "Nothing"}
	}
	uid, ok := sessionUserID(db, secret, tok)
	if !ok {
		return runtime.VCtor{Tag: "Nothing"}
	}
	user, err := runtime.LoadUserValue(*cfg, uid)
	if err != nil {
		return runtime.VCtor{Tag: "Nothing"}
	}
	return runtime.VCtor{Tag: "Just", Args: []runtime.Value{user}}
}

// withUser returns a copy of reqVal with `__user` added.
func withUser(reqVal runtime.VRecord, user runtime.Value) runtime.VRecord {
	fields := make(map[string]runtime.Value, len(reqVal.Fields)+1)
	for k, v := range reqVal.Fields {
		fields[k] = v
	}
	fields["__user"] = user
	order := append([]string(nil), reqVal.Order...)
	order = append(order, "__user")
	return runtime.VRecord{Fields: fields, Order: order}
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
