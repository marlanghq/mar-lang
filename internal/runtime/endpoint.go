package runtime

import (
	"fmt"
)

// VEndpoint is the runtime representation of a typed HTTP endpoint
// declaration. The same value is referenced both by the backend (via
// Endpoint.implement) and by the frontend (via Endpoint.call), keeping
// method + path consistent across the two without manual repetition.
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
		// Endpoint.get / post / patch / delete : String -> Endpoint
		"endpointGet":    nativeFn(1, makeEndpoint("GET")),
		"endpointPost":   nativeFn(1, makeEndpoint("POST")),
		"endpointPatch":  nativeFn(1, makeEndpoint("PATCH")),
		"endpointDelete": nativeFn(1, makeEndpoint("DELETE")),

		// Endpoint.implement : (Request -> Effect e Response) -> Endpoint -> Route
		// Argument order chosen so `endpoint |> Endpoint.implement handler` reads naturally.
		"endpointImplement": nativeFn(2, func(args []Value) (Value, error) {
			handler := args[0]
			ep, ok := args[1].(VEndpoint)
			if !ok {
				return nil, fmt.Errorf("Endpoint.implement: expected Endpoint as second arg")
			}
			return VRecord{
				Fields: map[string]Value{
					"method":  VString{V: ep.Method},
					"path":    VString{V: ep.Path},
					"handler": handler,
				},
				Order: []string{"method", "path", "handler"},
			}, nil
		}),

		// Endpoint.call : Endpoint -> String -> (Result String String -> msg) -> Effect e msg
		// On the Go side, this is a stub — the JS runtime re-implements it
		// using Http.get/post. Server-side calling endpoints isn't supported
		// in this MVP.
		"endpointCall": nativeFn(4, func(args []Value) (Value, error) {
			return VEffect{
				Tag: "endpointCall",
				Run: func() (Value, error) {
					return nil, fmt.Errorf("Endpoint.call is only available in the browser runtime")
				},
			}, nil
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
