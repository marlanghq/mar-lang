package runtime

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// VService is the typed RPC contract. `Service.declare VERB "path"`
// produces one with no handler; `Service.implement` and `Auth.protect`
// produce VExposedService values whose embedded Service has Handler set
// (and RequiresUser flagged in the auth case).
//
// Verb / Path are the HTTP verb and URL pattern the contract was
// declared with. The server mounts the service there; the browser's
// Service.call hits the same verb and path. The path may carry typed
// `{name:Type}` params (parsed with the same machinery as frontend
// routes), which the handler receives merged into its request.
//
// OriginModule / OriginName are stamped by the project loader (see
// internal/project/project.go) when the contract is bound at the top
// level. They identify the contract for diagnostics; the URL comes from
// Path, not from the binding name.
type VService struct {
	Handler      Value // nil on a bare contract; set by implement/protect
	Verb         string
	Path         string
	OriginModule string
	OriginName   string

	// RequiresUser is set by Auth.protect. When true, the dispatcher
	// validates the session cookie and currying-applies the loaded User
	// as the second positional argument before invoking the handler. A
	// missing/expired session returns 401 without touching the handler.
	RequiresUser bool

	// Authorization gates set by the Auth.requireRole / Auth.authorize /
	// Auth.requireOwner decorators (see docs/authorization-proposal.md).
	// Each is nil when the corresponding decorator wasn't applied.
	//
	// RequireRole holds the value the user's role must equal. The
	// dispatcher fetches the user's role via CurrentAuth().Role and
	// compares structurally with equalValues. Mismatch → 403.
	//
	// LoadResource is `input -> User -> Effect String (Maybe resource)`.
	// The dispatcher runs it before invoking the handler. Nothing → 404;
	// Just resource → fed to Policy.
	//
	// Policy is `input -> User -> resource -> Bool`. False → 403.
	RequireRole  Value
	LoadResource Value
	Policy       Value
}

func (VService) isValue() {}
func (s VService) Display() string {
	if s.OriginName != "" {
		return "<service:" + s.OriginName + ">"
	}
	return "<service>"
}

// VExposedService is the type-erased form. Lists of services with
// different Req/Resp share this type.
type VExposedService struct {
	Service VService
}

func (VExposedService) isValue() {}
func (e VExposedService) Display() string {
	return "<exposed:" + e.Service.OriginName + ">"
}

// serviceBuiltins exposes the contract / implement / call surface.
//
//	Service.declare   : Method -> String -> Service req resp
//	Service.implement : Service req resp -> (req -> Effect resp) -> ExposedService
//	Service.call      : Service req resp -> req -> (Result Service.Error resp -> msg) -> Effect msg
//
// Service.declare records the verb and path on the contract. Service.call
// on the Go side errors out — the server dispatches locally; the JS
// runtime re-implements call with fetch.
func serviceBuiltins() map[string]Value {
	return map[string]Value{
		"serviceDeclare": nativeFn(2, func(args []Value) (Value, error) {
			verb, ok := args[0].(VCtor)
			if !ok {
				return nil, fmt.Errorf("Service.declare: expected an HTTP method (got %T)", args[0])
			}
			path, ok := args[1].(VString)
			if !ok {
				return nil, fmt.Errorf("Service.declare: expected a path String (got %T)", args[1])
			}
			return VService{Verb: verb.Tag, Path: path.V}, nil
		}),

		"serviceImplement": nativeFn(2, func(args []Value) (Value, error) {
			contract, ok := args[0].(VService)
			if !ok {
				return nil, fmt.Errorf("Service.implement: expected Service contract (got %T)", args[0])
			}
			handler := args[1]
			contract.Handler = handler
			return VExposedService{Service: contract}, nil
		}),

		"serviceCall": nativeFn(4, func(args []Value) (Value, error) {
			return VEffect{
				Tag: "serviceCall",
				Run: func() (Value, error) {
					return nil, fmt.Errorf("Service.call is only available in the browser runtime")
				},
			}, nil
		}),
	}
}

// ExposedServiceToRoute turns a service-list element (VExposedService)
// into the internal Route record the dispatcher consumes: the declared
// verb and path pattern, plus a handler that reconstructs the typed
// request from the URL path params, the query string (GET / DELETE) or
// the JSON body (POST / PUT / PATCH), calls the user's handler, and
// JSON-encodes the result.
//
// Errors at any stage become 4xx/5xx without crashing the server.
//
// When the service was created via `Auth.protect`, the wrapped handler
// additionally pulls the current User from the dispatcher's per-request
// context (set up by jsserve before calling Apply) and curries it in as
// the second argument. A missing/expired session short-circuits with 401
// before any user code runs.
func ExposedServiceToRoute(es VExposedService) Value {
	svc := es.Service
	wrapped := VFn{
		Arity: 1,
		Native: func(args []Value) (Value, error) {
			req, ok := args[0].(VRecord)
			if !ok {
				return nil, fmt.Errorf("Service handler: request is not a record")
			}
			return VEffect{
				Tag: "serviceHandler",
				Run: func() (Value, error) {
					reqPath := stringField(req, "path")
					query := stringField(req, "query")
					body := stringField(req, "body")
					input, err := assembleServiceInput(svc, reqPath, query, body)
					if err != nil {
						return makeResp(422, fmt.Sprintf("invalid request: %v", err)), nil
					}
					handler := svc.Handler
					if svc.RequiresUser {
						user, ok := req.Fields["__user"]
						if !ok || isNoneCtor(user) {
							return makeResp(401, "not authenticated"), nil
						}
						justUser, ok := user.(VCtor)
						if !ok || justUser.Tag != "Just" || len(justUser.Args) != 1 {
							return makeResp(401, "not authenticated"), nil
						}
						userVal := justUser.Args[0]

						// Role gate (Auth.requireRole). Mismatch → 403.
						if svc.RequireRole != nil {
							if resp, ok := checkRoleGate(svc.RequireRole, userVal); !ok {
								return resp, nil
							}
						}

						// ABAC gate (Auth.authorize / Auth.requireOwner).
						// Loader can return Nothing → 404; policy False → 403.
						if svc.LoadResource != nil {
							if resp, ok := checkABACGate(svc.LoadResource, svc.Policy, input, userVal); !ok {
								return resp, nil
							}
						}

						// Curry: handler(input)(user).
						partial, err := Apply(handler, input)
						if err != nil {
							return serverErrorResponse(err), nil
						}
						eff, err := Apply(partial, userVal)
						if err != nil {
							return serverErrorResponse(err), nil
						}
						return runHandlerEffect(eff)
					}
					eff, err := Apply(handler, input)
					if err != nil {
						return serverErrorResponse(err), nil
					}
					return runHandlerEffect(eff)
				},
			}, nil
		},
	}
	return VRecord{
		Fields: map[string]Value{
			"method":       VString{V: svc.Verb},
			"path":         VString{V: svc.Path},
			"handler":      wrapped,
			"requiresUser": VBool{V: svc.RequiresUser},
		},
		Order: []string{"method", "path", "handler", "requiresUser"},
	}
}

// stringField reads a String field off a request record, "" when absent.
func stringField(rec VRecord, name string) string {
	if s, ok := rec.Fields[name].(VString); ok {
		return s.V
	}
	return ""
}

// assembleServiceInput reconstructs a service handler's typed request
// value from the wire. Typed `{name:Type}` path params come from the URL
// path; the remaining fields come from the query string (`q` JSON param,
// for GET / DELETE) or the JSON body (POST / PUT / PATCH). An empty result
// is Unit, so a `Service () resp` handler receives `()`.
func assembleServiceInput(svc VService, reqPath, query, body string) (Value, error) {
	fields := map[string]Value{}
	order := []string{}
	merge := func(v Value) {
		rec, ok := v.(VRecord)
		if !ok {
			return
		}
		for _, k := range rec.Order {
			if _, seen := fields[k]; !seen {
				order = append(order, k)
			}
			fields[k] = rec.Fields[k]
		}
	}

	if strings.Contains(svc.Path, "{") {
		vpath, err := ParsePathPattern(svc.Path)
		if err != nil {
			return nil, err
		}
		matched := vpath.MatchURL(reqPath)
		if matched == nil {
			return nil, fmt.Errorf("path %q does not match %q", reqPath, svc.Path)
		}
		merge(matched)
	}

	switch svc.Verb {
	case "GET", "DELETE":
		if q := queryValue(query); q != "" {
			v, err := decodeJSON(q)
			if err != nil {
				return nil, err
			}
			merge(v)
		}
	default: // POST, PUT, PATCH
		trimmed := strings.TrimSpace(body)
		if trimmed != "" && trimmed != "null" {
			v, err := decodeJSON(body)
			if err != nil {
				return nil, err
			}
			if _, ok := v.(VRecord); ok {
				merge(v)
			} else if len(order) == 0 {
				// A non-record body with no path params is the request
				// value itself (rare; services normally use records or ()).
				return v, nil
			}
		}
	}

	if len(order) == 0 {
		return VUnit{}, nil
	}
	return VRecord{Fields: fields, Order: order}, nil
}

// queryValue pulls the single `q` parameter (a JSON blob of the non-path
// request fields) out of a raw query string.
func queryValue(raw string) string {
	if raw == "" {
		return ""
	}
	vals, err := url.ParseQuery(raw)
	if err != nil {
		return ""
	}
	return vals.Get("q")
}

// makeResp builds a Response record { status, body }. Every place a
// response is constructed goes through it.
func makeResp(status int, body string) Value {
	return VRecord{
		Fields: map[string]Value{
			"status": VInt{V: int64(status)},
			"body":   VString{V: body},
		},
		Order: []string{"status", "body"},
	}
}

// serverErrorResponse maps a handler-side failure to a 500 Response
// without leaking server internals. A user-authored Effect.fail value
// carries an intentional app-level message and is surfaced as-is; any
// other error is logged server-side and the client gets a generic body,
// so SQL text, Go type names, and file paths never reach the wire.
func serverErrorResponse(err error) Value {
	if ee, ok := err.(effectError); ok {
		if s, ok := ee.value.(VString); ok {
			return makeResp(500, s.V)
		}
		return makeResp(500, ee.value.Display())
	}
	fmt.Fprintf(os.Stderr, "[mar] handler error: %v\n", err)
	return makeResp(500, "internal server error")
}

// checkRoleGate applies the registered role getter (from Auth.config)
// to the loaded user, then compares the result against the required
// role with structural equality. Returns (response, false) on
// rejection or misconfiguration; (nil, true) when the gate passes.
func checkRoleGate(required, user Value) (Value, bool) {
	cfg := CurrentAuth()
	if cfg == nil || cfg.Role == nil {
		return makeResp(500, "Auth.requireRole used but Auth.config has no `role` field"), false
	}
	userRole, err := Apply(cfg.Role, user)
	if err != nil {
		return serverErrorResponse(fmt.Errorf("role getter failed: %w", err)), false
	}
	if !equalValues(userRole, required) {
		return makeResp(403, "forbidden"), false
	}
	return nil, true
}

// checkABACGate runs the resource loader and policy. On Nothing the
// resource didn't exist (404). On Just resource, applies the policy;
// False rejects (403). True or no policy attached passes through.
func checkABACGate(loader, policy Value, input, user Value) (Value, bool) {
	loaderPartial, err := Apply(loader, input)
	if err != nil {
		return serverErrorResponse(fmt.Errorf("authorize loader failed: %w", err)), false
	}
	loadEff, err := Apply(loaderPartial, user)
	if err != nil {
		return serverErrorResponse(fmt.Errorf("authorize loader failed: %w", err)), false
	}
	veff, ok := loadEff.(VEffect)
	if !ok {
		return makeResp(500, "authorize loader did not return an Effect"), false
	}
	loaded, err := veff.Run()
	if err != nil {
		return serverErrorResponse(fmt.Errorf("authorize loader failed: %w", err)), false
	}
	maybeCtor, ok := loaded.(VCtor)
	if !ok {
		return makeResp(500, "authorize loader did not return a Maybe"), false
	}
	if maybeCtor.Tag == "Nothing" {
		return makeResp(404, "not found"), false
	}
	if maybeCtor.Tag != "Just" || len(maybeCtor.Args) != 1 {
		return makeResp(500, "authorize loader returned malformed Maybe"), false
	}
	resource := maybeCtor.Args[0]
	if policy == nil {
		return nil, true
	}
	// policy(input)(user)(resource)
	p1, err := Apply(policy, input)
	if err != nil {
		return serverErrorResponse(fmt.Errorf("authorize policy failed: %w", err)), false
	}
	p2, err := Apply(p1, user)
	if err != nil {
		return serverErrorResponse(fmt.Errorf("authorize policy failed: %w", err)), false
	}
	verdict, err := Apply(p2, resource)
	if err != nil {
		return serverErrorResponse(fmt.Errorf("authorize policy failed: %w", err)), false
	}
	pass, ok := verdict.(VBool)
	if !ok {
		return makeResp(500, "authorize policy did not return a Bool"), false
	}
	if !pass.V {
		return makeResp(403, "forbidden"), false
	}
	return nil, true
}

// runHandlerEffect runs the Effect returned by a Service handler and
// shapes a Response from its result. Pulled out so both the auth and
// non-auth paths share it.
func runHandlerEffect(eff Value) (Value, error) {
	veff, ok := eff.(VEffect)
	if !ok {
		return serverErrorResponse(fmt.Errorf("Service handler did not produce an Effect (got %T)", eff)), nil
	}
	out, err := veff.Run()
	if err != nil {
		return serverErrorResponse(err), nil
	}
	body, err := encodeValue(out)
	if err != nil {
		return serverErrorResponse(err), nil
	}
	return makeResp(200, body), nil
}

// isNoneCtor returns true if v is the Maybe constructor Nothing. Used
// by the auth wrapper to detect "no user attached" without importing
// the Maybe type from elsewhere.
func isNoneCtor(v Value) bool {
	c, ok := v.(VCtor)
	return ok && c.Tag == "Nothing"
}
