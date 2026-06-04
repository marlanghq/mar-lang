package runtime

import (
	"fmt"
	"os"
)

// VEndpoint is the runtime representation of a typed HTTP endpoint
// declaration (the low-level form, paired with Endpoint.implement).
//
// The same value is referenced both by the backend (via Endpoint.implement)
// and by the frontend (via Endpoint.call), keeping method + path consistent
// across the two without manual repetition.
type VEndpoint struct {
	Method string
	Path   string
}

func (VEndpoint) isValue() {}
func (e VEndpoint) Display() string {
	return fmt.Sprintf("<endpoint:%s %s>", e.Method, e.Path)
}

func endpointBuiltins() map[string]Value {
	return map[string]Value{
		// Endpoint.get / .post : String -> Endpoint
		// Low-level: the resulting Endpoint is paired with Endpoint.implement
		// to produce a Route. Use for custom paths or non-CRUD shapes.
		"endpointGet":  nativeFn(1, makeEndpoint("GET")),
		"endpointPost": nativeFn(1, makeEndpoint("POST")),

		// Endpoint.implement : (Request -> Effect e Response) -> Endpoint -> Route
		// Argument order chosen so `endpoint |> Endpoint.implement handler` reads naturally.
		"endpointImplement": nativeFn(2, func(args []Value) (Value, error) {
			handler := args[0]
			ep, ok := args[1].(VEndpoint)
			if !ok {
				return nil, fmt.Errorf("Endpoint.implement: expected Endpoint as second arg")
			}
			return makeRouteRecord(ep.Method, ep.Path, handler), nil
		}),

		// Endpoint.call : Endpoint -> String -> (Result String String -> msg) -> Effect e msg
		// On the Go side, this is a stub — the JS runtime re-implements it
		// using Http.get/post. Calling endpoints from server-side code is
		// not supported.
		"endpointCall": nativeFn(4, func(args []Value) (Value, error) {
			return VEffect{
				Tag: "endpointCall",
				Run: func() (Value, error) {
					return nil, fmt.Errorf("Endpoint.call is only available in the browser runtime")
				},
			}, nil
		}),

		// REST sugar. Each function takes (path, handler) and produces a Route
		// whose embedded handler does the path-param parsing, body decode,
		// status code, and JSON encoding so the user-supplied handler is just
		// the actual logic.
		//
		// All five share the same wrapping pattern: build a closure that on
		// every request extracts what it needs from the request value, calls
		// the user handler, runs the resulting Effect, and shapes a Response
		// record. Errors at any stage become 4xx/5xx without crashing.

		// Endpoint.list : String -> Effect String (List a) -> Route
		"endpointList": nativeFn(2, func(args []Value) (Value, error) {
			path, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("Endpoint.list: expected String path")
			}
			userEffect := args[1]
			handler := wrapNoArgsHandler(userEffect, 200)
			return makeRouteRecord("GET", path.V, handler), nil
		}),

		// Endpoint.show : String -> (Int -> Effect String (Maybe a)) -> Route
		"endpointShow": nativeFn(2, func(args []Value) (Value, error) {
			path, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("Endpoint.show: expected String path")
			}
			userHandler := args[1]
			handler := wrapIDHandler(userHandler, 200, true)
			return makeRouteRecord("GET", path.V, handler), nil
		}),

		// Endpoint.create : String -> (b -> Effect String a) -> Route
		"endpointCreate": nativeFn(2, func(args []Value) (Value, error) {
			path, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("Endpoint.create: expected String path")
			}
			userHandler := args[1]
			handler := wrapBodyHandler(userHandler, 201, false)
			return makeRouteRecord("POST", path.V, handler), nil
		}),

		// Endpoint.update : String -> (Int -> b -> Effect String (Maybe a)) -> Route
		"endpointUpdate": nativeFn(2, func(args []Value) (Value, error) {
			path, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("Endpoint.update: expected String path")
			}
			userHandler := args[1]
			handler := wrapIDBodyHandler(userHandler, 200, true)
			return makeRouteRecord("PATCH", path.V, handler), nil
		}),

		// Endpoint.delete : String -> (Int -> Effect String ()) -> Route
		"endpointDelete": nativeFn(2, func(args []Value) (Value, error) {
			path, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("Endpoint.delete: expected String path")
			}
			userHandler := args[1]
			handler := wrapDeleteHandler(userHandler)
			return makeRouteRecord("DELETE", path.V, handler), nil
		}),
	}
}

func makeEndpoint(method string) func([]Value) (Value, error) {
	return func(args []Value) (Value, error) {
		path, ok := args[0].(VString)
		if !ok {
			return nil, fmt.Errorf("Endpoint.%s: expected String path", method)
		}
		return VEndpoint{Method: method, Path: path.V}, nil
	}
}

// makeRouteRecord builds the runtime representation of a Route — a record
// the dispatcher reads to match HTTP requests against and to invoke the
// handler. Handler is `Request -> Effect String Response`.
func makeRouteRecord(method, path string, handler Value) Value {
	return VRecord{
		Fields: map[string]Value{
			"method":  VString{V: method},
			"path":    VString{V: path},
			"handler": handler,
		},
		Order: []string{"method", "path", "handler"},
	}
}

// makeResp constructs a Response record `{ status, body }`. It's the one
// place Response values are built — Response.ok / .notFound / .status
// (server.go) and the endpoint/service handlers all go through it.
func makeResp(status int, body string) Value {
	return VRecord{
		Fields: map[string]Value{
			"status": VInt{V: int64(status)},
			"body":   VString{V: body},
		},
		Order: []string{"status", "body"},
	}
}

// serverErrorResponse maps a handler-side failure to a 500 Response without
// leaking server internals to the client.
//
// A user-authored Effect.fail value carries an intentional, app-level message
// (the `String` error channel of `Effect String resp`) and is surfaced as-is.
// Any other error — a type mismatch, an encode failure, a database/SQL error —
// is logged server-side and the client receives a generic body, so internal
// detail (SQL text, Go type names, file paths) never reaches the wire.
func serverErrorResponse(err error) Value {
	// effectError is propagated unwrapped through the effect chain, so a
	// direct type assertion catches a user-authored Effect.fail value; any
	// other (wrapped or internal) error falls through to the sanitized path.
	if _, ok := err.(effectError); ok {
		return makeResp(500, err.Error())
	}
	fmt.Fprintf(os.Stderr, "[mar] handler error: %v\n", err)
	return makeResp(500, "internal server error")
}

// jsonOfValue encodes a value as JSON for a response body. Returns the
// fallback message and a non-nil error if encoding failed — callers turn
// that into a 500 with a clear message.
func jsonOfValue(v Value) (string, error) {
	s, err := encodeValue(v)
	if err != nil {
		return "", fmt.Errorf("could not encode response value: %w", err)
	}
	return s, nil
}

// pathParamID extracts the `:id` parameter from the request value and parses
// it as Int. Returns ok=false with a Response if the param is missing or
// not a valid integer, so callers can short-circuit with a 400.
func pathParamID(req Value) (int64, Value, bool) {
	rec, ok := req.(VRecord)
	if !ok {
		return 0, makeResp(400, "internal error: request is not a record"), false
	}
	paramsFn, ok := rec.Fields["params"]
	if !ok {
		return 0, makeResp(400, "internal error: request has no params"), false
	}
	idVal, err := Apply(paramsFn, VString{V: "id"})
	if err != nil {
		return 0, makeResp(400, "could not look up :id"), false
	}
	ctor, ok := idVal.(VCtor)
	if !ok || ctor.Tag == "Nothing" {
		return 0, makeResp(400, "missing path param :id"), false
	}
	if ctor.Tag != "Just" || len(ctor.Args) != 1 {
		return 0, makeResp(400, "invalid path param :id"), false
	}
	idStr, ok := ctor.Args[0].(VString)
	if !ok {
		return 0, makeResp(400, "invalid path param :id"), false
	}
	var n int64
	for i := 0; i < len(idStr.V); i++ {
		c := idStr.V[i]
		if c < '0' || c > '9' {
			return 0, makeResp(400, fmt.Sprintf("path param :id must be Int (got %q)", idStr.V)), false
		}
		n = n*10 + int64(c-'0')
	}
	if len(idStr.V) == 0 {
		return 0, makeResp(400, "path param :id must be Int (empty)"), false
	}
	return n, nil, true
}

// decodeBody decodes the JSON body of the request as a Value. Returns a
// Response (422) if the body is not valid JSON.
func decodeBody(req Value) (Value, Value, bool) {
	rec, ok := req.(VRecord)
	if !ok {
		return nil, makeResp(400, "internal error: request is not a record"), false
	}
	bodyV, ok := rec.Fields["body"].(VString)
	if !ok {
		return nil, makeResp(400, "internal error: request body missing"), false
	}
	v, err := decodeJSON(bodyV.V)
	if err != nil {
		return nil, makeResp(422, fmt.Sprintf("invalid JSON body: %v", err)), false
	}
	return v, nil, true
}

// runUserEffect runs an Effect produced by the user handler. If the value
// isn't actually an Effect, returns a 500. If the effect fails, returns a
// 500 with the message.
func runUserEffect(v Value) (Value, Value, bool) {
	eff, ok := v.(VEffect)
	if !ok {
		return nil, serverErrorResponse(fmt.Errorf("handler did not produce an Effect (got %T)", v)), false
	}
	out, err := eff.Run()
	if err != nil {
		return nil, serverErrorResponse(err), false
	}
	return out, nil, true
}

// wrapNoArgsHandler wraps an `Effect String a` user handler — the request is
// ignored (no path params, no body), the effect is run on every request, and
// the result is JSON-encoded with the given success status.
func wrapNoArgsHandler(userEffect Value, successStatus int) Value {
	return VFn{
		Arity: 1,
		Native: func(args []Value) (Value, error) {
			return VEffect{
				Tag: "endpointHandler",
				Run: func() (Value, error) {
					out, errResp, ok := runUserEffect(userEffect)
					if !ok {
						return errResp, nil
					}
					body, err := jsonOfValue(out)
					if err != nil {
						return serverErrorResponse(err), nil
					}
					return makeResp(successStatus, body), nil
				},
			}, nil
		},
	}
}

// wrapIDHandler wraps an `Int -> Effect String (Maybe a)` (or `Int -> Effect
// String a` if maybeMode is false) handler. Extracts :id from the URL,
// applies the user handler, runs the effect, and shapes the response.
//
// When maybeMode is true (show, update), a `Nothing` result becomes 404 with
// an empty body.
func wrapIDHandler(userHandler Value, successStatus int, maybeMode bool) Value {
	return VFn{
		Arity: 1,
		Native: func(args []Value) (Value, error) {
			req := args[0]
			return VEffect{
				Tag: "endpointHandler",
				Run: func() (Value, error) {
					id, errResp, ok := pathParamID(req)
					if !ok {
						return errResp, nil
					}
					eff, err := Apply(userHandler, VInt{V: id})
					if err != nil {
						return serverErrorResponse(err), nil
					}
					out, errResp2, ok := runUserEffect(eff)
					if !ok {
						return errResp2, nil
					}
					return shapeMaybeOrValueResponse(out, successStatus, maybeMode), nil
				},
			}, nil
		},
	}
}

// wrapBodyHandler wraps a `b -> Effect String a` handler. Decodes the JSON
// body, applies the user handler, runs the effect, JSON-encodes the result.
func wrapBodyHandler(userHandler Value, successStatus int, maybeMode bool) Value {
	return VFn{
		Arity: 1,
		Native: func(args []Value) (Value, error) {
			req := args[0]
			return VEffect{
				Tag: "endpointHandler",
				Run: func() (Value, error) {
					body, errResp, ok := decodeBody(req)
					if !ok {
						return errResp, nil
					}
					eff, err := Apply(userHandler, body)
					if err != nil {
						return serverErrorResponse(err), nil
					}
					out, errResp2, ok := runUserEffect(eff)
					if !ok {
						return errResp2, nil
					}
					return shapeMaybeOrValueResponse(out, successStatus, maybeMode), nil
				},
			}, nil
		},
	}
}

// wrapIDBodyHandler wraps `Int -> b -> Effect String (Maybe a)` (update).
func wrapIDBodyHandler(userHandler Value, successStatus int, maybeMode bool) Value {
	return VFn{
		Arity: 1,
		Native: func(args []Value) (Value, error) {
			req := args[0]
			return VEffect{
				Tag: "endpointHandler",
				Run: func() (Value, error) {
					id, errResp, ok := pathParamID(req)
					if !ok {
						return errResp, nil
					}
					body, errResp2, ok := decodeBody(req)
					if !ok {
						return errResp2, nil
					}
					partial, err := Apply(userHandler, VInt{V: id})
					if err != nil {
						return serverErrorResponse(err), nil
					}
					eff, err := Apply(partial, body)
					if err != nil {
						return serverErrorResponse(err), nil
					}
					out, errResp3, ok := runUserEffect(eff)
					if !ok {
						return errResp3, nil
					}
					return shapeMaybeOrValueResponse(out, successStatus, maybeMode), nil
				},
			}, nil
		},
	}
}

// wrapDeleteHandler wraps `Int -> Effect String ()`. Returns 204 with an
// empty body on success, regardless of the unit value.
func wrapDeleteHandler(userHandler Value) Value {
	return VFn{
		Arity: 1,
		Native: func(args []Value) (Value, error) {
			req := args[0]
			return VEffect{
				Tag: "endpointHandler",
				Run: func() (Value, error) {
					id, errResp, ok := pathParamID(req)
					if !ok {
						return errResp, nil
					}
					eff, err := Apply(userHandler, VInt{V: id})
					if err != nil {
						return serverErrorResponse(err), nil
					}
					_, errResp2, ok := runUserEffect(eff)
					if !ok {
						return errResp2, nil
					}
					return makeResp(204, ""), nil
				},
			}, nil
		},
	}
}

// shapeMaybeOrValueResponse converts the handler's return value to a Response.
// In maybeMode, `Just x` becomes a successStatus response with x's JSON, and
// `Nothing` becomes 404 with empty body. Otherwise the value itself is JSON
// encoded with the success status.
func shapeMaybeOrValueResponse(out Value, successStatus int, maybeMode bool) Value {
	if maybeMode {
		if ctor, ok := out.(VCtor); ok {
			switch ctor.Tag {
			case "Nothing":
				return makeResp(404, "")
			case "Just":
				if len(ctor.Args) == 1 {
					body, err := jsonOfValue(ctor.Args[0])
					if err != nil {
						return serverErrorResponse(err)
					}
					return makeResp(successStatus, body)
				}
			}
		}
		// Maybe-shaped output that doesn't match Just/Nothing: surface as 500.
		return serverErrorResponse(fmt.Errorf("expected Maybe, got %T", out))
	}
	body, err := jsonOfValue(out)
	if err != nil {
		return serverErrorResponse(err)
	}
	return makeResp(successStatus, body)
}
