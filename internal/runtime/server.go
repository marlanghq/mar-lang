package runtime

import "fmt"

// responseBuiltins returns Response.* helper constructors.
//
// API surface (deliberately small):
//
//	Response.ok       : String -> Response
//	Response.notFound : Response
//	Response.status   : Int -> String -> Response
//
// Response is a record (not opaque), exposed so handlers can build it
// directly. The dev-server / built-server code is in internal/jsserve.
// mar apps host themselves through App.frontend / App.backend /
// App.fullstack — there is no lower-level `Server.serve` to drop down to.
func serverBuiltins() map[string]Value {
	return map[string]Value{
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
