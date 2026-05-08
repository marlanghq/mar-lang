package runtime

import "fmt"

// ioBuiltins returns runtime functions for stdlib effects available in
// server programs.
//
// IO.print / IO.println / IO.readLine were removed when mar narrowed
// to full-stack web — they don't fit the topologies (frontend / backend
// / fullstack) and the previous CLI sub-commands that exercised them
// (`mar run`) are gone too. If a future use-case needs them back,
// reintroduce as `Process.print` etc. with explicit semantics.
//
// What remains here is the stub for Http.get / Http.post on the Go
// side. Real HTTP requests run in the browser through the JS runtime;
// the Go effect just errors if invoked, so accidentally calling
// Http.* in server-evaluated code fails fast instead of silently
// doing nothing.
func ioBuiltins() map[string]Value {
	return map[string]Value{
		// Http.get / Http.post : implemented client-side by the JS runtime.
		// On the Go side they're stubs that error out — server-side code
		// that depends on Http is not supported.
		"httpGet": nativeFn(2, func(args []Value) (Value, error) {
			return VEffect{
				Tag: "httpGet",
				Run: func() (Value, error) {
					return nil, fmt.Errorf("Http.get is only available in the browser runtime")
				},
			}, nil
		}),
		"httpPost": nativeFn(3, func(args []Value) (Value, error) {
			return VEffect{
				Tag: "httpPost",
				Run: func() (Value, error) {
					return nil, fmt.Errorf("Http.post is only available in the browser runtime")
				},
			}, nil
		}),
	}
}
