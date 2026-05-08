package runtime

import "fmt"

// VService is the typed RPC contract. `Service.declare` produces one
// with no handler; `Service.implement` and `Auth.protect` produce
// VExposedService values whose embedded Service has Handler set (and
// RequiresUser flagged in the auth case).
//
// OriginModule / OriginName are stamped by the project loader (see
// internal/project/project.go) when the contract is bound at the top
// level. Implementations inherit the contract's origin so the URL
// path stays tied to the contract — frontend's Service.call always
// resolves the same way regardless of where the impl is bound.
type VService struct {
	Handler      Value // nil on a bare contract; set by implement/protect
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
//	Service.declare   : Service req resp
//	Service.implement : Service req resp -> (req -> Effect String resp) -> ExposedService
//	Service.call      : Service req resp -> req -> (Result String resp -> msg) -> Effect e msg
//
// Service.declare is a singleton VService — each top-level binding
// gets its own stamped copy via the loader's value-semantics path.
// Service.call on the Go side errors out — the server dispatches
// locally; the JS runtime re-implements call with fetch.
func serviceBuiltins() map[string]Value {
	return map[string]Value{
		"serviceDeclare": VService{},

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

// ServicePath returns the URL where a service is mounted. Convention:
// `/services/<OriginModule>.<OriginName>`. Both ends (server mount,
// browser fetch) compute it the same way so they always agree.
func ServicePath(svc VService) string {
	if svc.OriginModule != "" {
		return "/services/" + svc.OriginModule + "." + svc.OriginName
	}
	return "/services/" + svc.OriginName
}

// ExposedServiceToRoute turns a service-list element (VExposedService)
// into the same Route record shape the dispatcher already consumes for
// HTTP endpoints. The wrapped handler decodes the request body as JSON,
// calls the user's typed handler, JSON-encodes the result.
//
// Method is POST (RPC-style) and the body carries the full input payload.
// Errors at any stage become 4xx/5xx without crashing the server.
//
// When the service was created via `Auth.protect`, the wrapped
// handler additionally pulls the current User from the dispatcher's
// per-request context (set up by jsserve before calling Apply) and
// curries it in as the second argument. A missing/expired session
// short-circuits with 401 before any user code runs.
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
					bodyV, ok := req.Fields["body"].(VString)
					if !ok {
						return makeResp(400, "missing request body"), nil
					}
					input, err := decodeJSON(bodyV.V)
					if err != nil {
						return makeResp(422, fmt.Sprintf("invalid JSON: %v", err)), nil
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
							return makeResp(500, err.Error()), nil
						}
						eff, err := Apply(partial, userVal)
						if err != nil {
							return makeResp(500, err.Error()), nil
						}
						return runHandlerEffect(eff)
					}
					eff, err := Apply(handler, input)
					if err != nil {
						return makeResp(500, err.Error()), nil
					}
					return runHandlerEffect(eff)
				},
			}, nil
		},
	}
	return VRecord{
		Fields: map[string]Value{
			"method":       VString{V: "POST"},
			"path":         VString{V: ServicePath(svc)},
			"handler":      wrapped,
			"requiresUser": VBool{V: svc.RequiresUser},
		},
		Order: []string{"method", "path", "handler", "requiresUser"},
	}
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
		return makeResp(500, fmt.Sprintf("role getter failed: %v", err)), false
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
		return makeResp(500, fmt.Sprintf("authorize loader failed: %v", err)), false
	}
	loadEff, err := Apply(loaderPartial, user)
	if err != nil {
		return makeResp(500, fmt.Sprintf("authorize loader failed: %v", err)), false
	}
	veff, ok := loadEff.(VEffect)
	if !ok {
		return makeResp(500, "authorize loader did not return an Effect"), false
	}
	loaded, err := veff.Run()
	if err != nil {
		return makeResp(500, fmt.Sprintf("authorize loader failed: %v", err)), false
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
		return makeResp(500, fmt.Sprintf("authorize policy failed: %v", err)), false
	}
	p2, err := Apply(p1, user)
	if err != nil {
		return makeResp(500, fmt.Sprintf("authorize policy failed: %v", err)), false
	}
	verdict, err := Apply(p2, resource)
	if err != nil {
		return makeResp(500, fmt.Sprintf("authorize policy failed: %v", err)), false
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
		return makeResp(500, fmt.Sprintf("Service handler did not produce an Effect (got %T)", eff)), nil
	}
	out, err := veff.Run()
	if err != nil {
		return makeResp(500, err.Error()), nil
	}
	body, err := encodeValue(out)
	if err != nil {
		return makeResp(500, err.Error()), nil
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
