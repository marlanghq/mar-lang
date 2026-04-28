package runtime

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// serverBuiltins returns runtime functions for an HTTP server.
//
// MVP API surface (keep minimal):
//
//	Server.serve : Int -> List Route -> Effect String ()
//	Server.get   : String -> (Request -> Effect String Response) -> Route
//	Server.post  : String -> (Request -> Effect String Response) -> Route
//	Response.ok  : String -> Response
//	Response.notFound : Response
//	Response.status : Int -> String -> Response
//
// Request and Response are records (not opaque types), exposed for the user
// to inspect and construct. Handlers receive a Request and return an Effect
// of Response.
func serverBuiltins() map[string]Value {
	return map[string]Value{
		"serverServe":  nativeFn(2, serverServeImpl),
		"serverGet":    nativeFn(2, makeRoute("GET")),
		"serverPost":   nativeFn(2, makeRoute("POST")),
		"serverPatch":  nativeFn(2, makeRoute("PATCH")),
		"serverDelete": nativeFn(2, makeRoute("DELETE")),

		"responseOk": nativeFn(1, func(args []Value) (Value, error) {
			body, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("Response.ok: expected String body")
			}
			return makeResponse(200, body.V), nil
		}),
		"responseNotFound": makeResponse(404, "not found"),
		"responseStatus": nativeFn(2, func(args []Value) (Value, error) {
			status, ok1 := args[0].(VInt)
			body, ok2 := args[1].(VString)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("Response.status: expected Int and String")
			}
			return makeResponse(int(status.V), body.V), nil
		}),
	}
}

// makeRoute returns a function that builds a Route VRecord with the given method.
func makeRoute(method string) func([]Value) (Value, error) {
	return func(args []Value) (Value, error) {
		path, ok := args[0].(VString)
		if !ok {
			return nil, fmt.Errorf("route: expected String path")
		}
		handler := args[1]
		return VRecord{
			Fields: map[string]Value{
				"method":  VString{V: method},
				"path":    VString{V: path.V},
				"handler": handler,
			},
			Order: []string{"method", "path", "handler"},
		}, nil
	}
}

// makeResponse builds a Response VRecord.
func makeResponse(status int, body string) Value {
	return VRecord{
		Fields: map[string]Value{
			"status": VInt{V: int64(status)},
			"body":   VString{V: body},
		},
		Order: []string{"status", "body"},
	}
}

// serverServeImpl is the actual HTTP server. Returns an Effect that, when
// run, starts listening on the given port. This is BLOCKING — the effect
// never returns naturally (only on shutdown).
func serverServeImpl(args []Value) (Value, error) {
	portV, ok1 := args[0].(VInt)
	routesV, ok2 := args[1].(VList)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("Server.serve: expected Int port and List routes")
	}
	port := int(portV.V)

	type compiledRoute struct {
		method  string
		path    string
		handler Value
	}
	var routes []compiledRoute
	for _, rv := range routesV.Elements {
		r, ok := rv.(VRecord)
		if !ok {
			return nil, fmt.Errorf("Server.serve: route is not a record")
		}
		method, _ := r.Fields["method"].(VString)
		path, _ := r.Fields["path"].(VString)
		handler := r.Fields["handler"]
		routes = append(routes, compiledRoute{
			method:  method.V,
			path:    path.V,
			handler: handler,
		})
	}

	return VEffect{
		Tag: "serve",
		Run: func() (Value, error) {
			mux := http.NewServeMux()
			for _, r := range routes {
				method, path, handler := r.method, r.path, r.handler
				mux.HandleFunc(path, func(w http.ResponseWriter, req *http.Request) {
					if req.Method != method {
						http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
						return
					}
					body, _ := io.ReadAll(req.Body)
					reqVal := VRecord{
						Fields: map[string]Value{
							"url":    VString{V: req.URL.String()},
							"method": VString{V: req.Method},
							"body":   VString{V: string(body)},
						},
						Order: []string{"url", "method", "body"},
					}
					// Apply handler to request, then run the resulting Effect.
					effVal, err := apply(handler, reqVal)
					if err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
					eff, ok := effVal.(VEffect)
					if !ok {
						http.Error(w, "handler did not return an Effect", http.StatusInternalServerError)
						return
					}
					result, err := eff.Run()
					if err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
					resp, ok := result.(VRecord)
					if !ok {
						http.Error(w, "handler effect did not produce a Response record", http.StatusInternalServerError)
						return
					}
					status, _ := resp.Fields["status"].(VInt)
					rbody, _ := resp.Fields["body"].(VString)
					w.WriteHeader(int(status.V))
					_, _ = io.WriteString(w, rbody.V)
				})
			}
			fmt.Printf("[mar] Listening on :%d\n", port)
			err := http.ListenAndServe(fmt.Sprintf(":%d", port), mux)
			return VUnit{}, err
		},
	}, nil
}

// noteUsedImports keeps strings imports happy if some helpers go unused.
var _ = strings.Repeat
