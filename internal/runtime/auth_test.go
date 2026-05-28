package runtime

import (
	"strings"
	"testing"
)

// helpers ------------------------------------------------------------

// stubExposed returns a minimal VExposedService backed by a no-op
// handler. The decorator tests don't actually invoke the handler;
// they only check that the decorator records its policy state.
func stubExposed() VExposedService {
	noop := nativeFn(2, func(args []Value) (Value, error) { return VUnit{}, nil })
	return VExposedService{
		Service: VService{
			Handler:      noop,
			OriginModule: "Test",
			OriginName:   "stub",
			RequiresUser: true,
		},
	}
}

// runEffect drives the dispatcher's wrapped Service handler with a
// synthetic request record. Returns the response record so tests
// can inspect status + body.
func runDispatch(t *testing.T, exposed VExposedService, body string, user Value) VRecord {
	t.Helper()
	route := ExposedServiceToRoute(exposed)
	rec, ok := route.(VRecord)
	if !ok {
		t.Fatalf("ExposedServiceToRoute didn't return a record: %T", route)
	}
	handler, ok := rec.Fields["handler"].(VFn)
	if !ok {
		t.Fatalf("route has no handler: %v", rec.Fields["handler"])
	}
	req := VRecord{
		Fields: map[string]Value{
			"body":   VString{V: body},
			"__user": user,
		},
		Order: []string{"body", "__user"},
	}
	effV, err := handler.Native([]Value{req})
	if err != nil {
		t.Fatalf("handler call: %v", err)
	}
	eff, ok := effV.(VEffect)
	if !ok {
		t.Fatalf("handler did not return an Effect: %T", effV)
	}
	out, err := eff.Run()
	if err != nil {
		t.Fatalf("effect run: %v", err)
	}
	resp, ok := out.(VRecord)
	if !ok {
		t.Fatalf("effect didn't return a Response record: %T", out)
	}
	return resp
}

func statusOf(t *testing.T, resp VRecord) int64 {
	t.Helper()
	v, ok := resp.Fields["status"].(VInt)
	if !ok {
		t.Fatalf("response missing status: %v", resp)
	}
	return v.V
}

func bodyOf(t *testing.T, resp VRecord) string {
	t.Helper()
	v, ok := resp.Fields["body"].(VString)
	if !ok {
		return ""
	}
	return v.V
}

// stubUser builds a minimal User record with `id` and `role`, plus
// any extra fields the test wants to inject.
func stubUser(id int64, role string, extras map[string]Value) VRecord {
	fields := map[string]Value{
		"id":   VInt{V: id},
		"role": VString{V: role},
	}
	order := []string{"id", "role"}
	for k, v := range extras {
		fields[k] = v
		order = append(order, k)
	}
	return VRecord{Fields: fields, Order: order}
}

// just/nothing helpers
func vJust(v Value) Value { return VCtor{Tag: "Just", Args: []Value{v}} }
func vNothing() Value     { return VCtor{Tag: "Nothing"} }

// TestAuthConfig_RejectsEmailFrom — the runtime parser must reject
// `email.from` in Auth.config. The From: address lives in mar.json's
// `mail.from` (the one verified with the SMTP provider); accepting
// it in Mar source too would invite typos that silently shadow the
// manifest value and break delivery at the provider. The reject
// must fire loudly at startup, not silently in prod.
func TestAuthConfig_RejectsEmailFrom(t *testing.T) {
	entity := VEntity{
		Table: "users",
		Fields: []EntityField{
			{Name: "id", SQLType: "INTEGER", NotNull: true, Serial: true},
			{Name: "email", SQLType: "TEXT", NotNull: true},
		},
	}
	identify := VFn{Arity: 1, Native: func(args []Value) (Value, error) { return VString{V: "x"}, nil }}
	signup := VFn{Arity: 1, Native: func(args []Value) (Value, error) { return VRecord{}, nil }}

	input := VRecord{
		Order: []string{"entity", "identify", "email", "signup"},
		Fields: map[string]Value{
			"entity":   entity,
			"identify": identify,
			"email": VRecord{
				Order: []string{"from", "subject"},
				Fields: map[string]Value{
					"from":    VString{V: "noreply@test.local"},
					"subject": VString{V: "Sign in"},
				},
			},
			"signup": signup,
		},
	}
	_, err := makeAuthConfig([]Value{input})
	if err == nil {
		t.Fatal("expected error for Auth.config with email.from; got nil")
	}
	if !strings.Contains(err.Error(), "email.from") || !strings.Contains(err.Error(), "mail.from") {
		t.Errorf("error should mention email.from and direct to mar.json's mail.from; got: %v", err)
	}
}

// decorators ---------------------------------------------------------

func TestAuthRequireRole_Attaches(t *testing.T) {
	out, err := makeAuthRequireRole([]Value{VString{V: "admin"}, stubExposed()})
	if err != nil {
		t.Fatalf("makeAuthRequireRole: %v", err)
	}
	exposed := out.(VExposedService)
	if exposed.Service.RequireRole == nil {
		t.Fatal("RequireRole was not set")
	}
	if v, ok := exposed.Service.RequireRole.(VString); !ok || v.V != "admin" {
		t.Fatalf("RequireRole = %v, want VString(admin)", exposed.Service.RequireRole)
	}
}

func TestAuthAuthorize_Attaches(t *testing.T) {
	loader := nativeFn(2, func(args []Value) (Value, error) { return VUnit{}, nil })
	policy := nativeFn(3, func(args []Value) (Value, error) { return VBool{V: true}, nil })
	out, err := makeAuthAuthorize([]Value{loader, policy, stubExposed()})
	if err != nil {
		t.Fatalf("makeAuthAuthorize: %v", err)
	}
	exposed := out.(VExposedService)
	if exposed.Service.LoadResource == nil {
		t.Fatal("LoadResource was not set")
	}
	if exposed.Service.Policy == nil {
		t.Fatal("Policy was not set")
	}
}

func TestAuthRequireOwner_SynthesizesPolicy(t *testing.T) {
	loader := nativeFn(2, func(args []Value) (Value, error) { return VUnit{}, nil })
	selector := nativeFn(1, func(args []Value) (Value, error) {
		// resource.userId
		return projectField(args[0], "userId")
	})
	out, err := makeAuthRequireOwner([]Value{loader, selector, stubExposed()})
	if err != nil {
		t.Fatalf("makeAuthRequireOwner: %v", err)
	}
	exposed := out.(VExposedService)
	if exposed.Service.LoadResource == nil || exposed.Service.Policy == nil {
		t.Fatal("requireOwner did not set both LoadResource and Policy")
	}

	// Simulate dispatcher invoking the synthesized policy.
	user := stubUser(42, "member", nil)
	resourceMatch := VRecord{Fields: map[string]Value{"userId": VInt{V: 42}}, Order: []string{"userId"}}
	resourceMiss := VRecord{Fields: map[string]Value{"userId": VInt{V: 99}}, Order: []string{"userId"}}

	// policy(input)(user)(resource)
	step1, _ := Apply(exposed.Service.Policy, VUnit{})
	step2, _ := Apply(step1, user)
	verdictMatch, err := Apply(step2, resourceMatch)
	if err != nil {
		t.Fatalf("policy on match: %v", err)
	}
	if !verdictMatch.(VBool).V {
		t.Fatal("policy should accept when selector matches user.id")
	}

	step1b, _ := Apply(exposed.Service.Policy, VUnit{})
	step2b, _ := Apply(step1b, user)
	verdictMiss, err := Apply(step2b, resourceMiss)
	if err != nil {
		t.Fatalf("policy on miss: %v", err)
	}
	if verdictMiss.(VBool).V {
		t.Fatal("policy should reject when selector mismatches user.id")
	}
}

// dispatcher gates ---------------------------------------------------

func TestDispatcher_Anonymous_401(t *testing.T) {
	exposed := stubExposed()
	resp := runDispatch(t, exposed, `{}`, vNothing())
	if got := statusOf(t, resp); got != 401 {
		t.Fatalf("anonymous request: status=%d, want 401 (body=%q)", got, bodyOf(t, resp))
	}
}

func TestDispatcher_RequireRole_Allow(t *testing.T) {
	// Register an Auth.config with a role getter for the gate to use.
	roleGetter := nativeFn(1, func(args []Value) (Value, error) {
		return projectField(args[0], "role")
	})
	RegisterAuth(VAuth{Role: roleGetter})

	exposed := stubExposed()
	// Plain handler that ignores input and curries fine with user.
	exposed.Service.Handler = nativeFn(2, func(args []Value) (Value, error) {
		return VEffect{Tag: "ok", Run: func() (Value, error) { return VString{V: "ok"}, nil }}, nil
	})
	exposed.Service.RequireRole = VString{V: "admin"}

	user := stubUser(1, "admin", nil)
	resp := runDispatch(t, exposed, `{}`, vJust(user))
	if got := statusOf(t, resp); got != 200 {
		t.Fatalf("admin request: status=%d, body=%q", got, bodyOf(t, resp))
	}
}

func TestDispatcher_RequireRole_Deny(t *testing.T) {
	roleGetter := nativeFn(1, func(args []Value) (Value, error) {
		return projectField(args[0], "role")
	})
	RegisterAuth(VAuth{Role: roleGetter})

	exposed := stubExposed()
	exposed.Service.RequireRole = VString{V: "admin"}

	user := stubUser(1, "member", nil)
	resp := runDispatch(t, exposed, `{}`, vJust(user))
	if got := statusOf(t, resp); got != 403 {
		t.Fatalf("member request: status=%d, want 403 (body=%q)", got, bodyOf(t, resp))
	}
	if !strings.Contains(bodyOf(t, resp), "forbidden") {
		t.Fatalf("403 body=%q, expected 'forbidden'", bodyOf(t, resp))
	}
}

func TestDispatcher_RequireRole_NoRoleGetterIs500(t *testing.T) {
	// Auth.config without a role getter — using requireRole is a misconfiguration.
	RegisterAuth(VAuth{Role: nil})

	exposed := stubExposed()
	exposed.Service.RequireRole = VString{V: "admin"}

	user := stubUser(1, "admin", nil)
	resp := runDispatch(t, exposed, `{}`, vJust(user))
	if got := statusOf(t, resp); got != 500 {
		t.Fatalf("misconfigured: status=%d, want 500 (body=%q)", got, bodyOf(t, resp))
	}
}

func TestDispatcher_Authorize_LoaderNothing_404(t *testing.T) {
	loader := nativeFn(2, func(args []Value) (Value, error) {
		return VEffect{Tag: "load", Run: func() (Value, error) {
			return vNothing(), nil
		}}, nil
	})
	policy := nativeFn(3, func(args []Value) (Value, error) { return VBool{V: true}, nil })

	exposed := stubExposed()
	exposed.Service.LoadResource = loader
	exposed.Service.Policy = policy

	user := stubUser(1, "x", nil)
	resp := runDispatch(t, exposed, `{}`, vJust(user))
	if got := statusOf(t, resp); got != 404 {
		t.Fatalf("missing resource: status=%d, want 404 (body=%q)", got, bodyOf(t, resp))
	}
}

func TestDispatcher_Authorize_PolicyDeny_403(t *testing.T) {
	loader := nativeFn(2, func(args []Value) (Value, error) {
		return VEffect{Tag: "load", Run: func() (Value, error) {
			return vJust(VRecord{Fields: map[string]Value{"x": VInt{V: 1}}, Order: []string{"x"}}), nil
		}}, nil
	})
	policy := nativeFn(3, func(args []Value) (Value, error) { return VBool{V: false}, nil })

	exposed := stubExposed()
	exposed.Service.LoadResource = loader
	exposed.Service.Policy = policy

	user := stubUser(1, "x", nil)
	resp := runDispatch(t, exposed, `{}`, vJust(user))
	if got := statusOf(t, resp); got != 403 {
		t.Fatalf("policy denial: status=%d, want 403 (body=%q)", got, bodyOf(t, resp))
	}
}

func TestDispatcher_Authorize_PolicyAllow_200(t *testing.T) {
	loader := nativeFn(2, func(args []Value) (Value, error) {
		return VEffect{Tag: "load", Run: func() (Value, error) {
			return vJust(VRecord{Fields: map[string]Value{"x": VInt{V: 1}}, Order: []string{"x"}}), nil
		}}, nil
	})
	policy := nativeFn(3, func(args []Value) (Value, error) { return VBool{V: true}, nil })

	exposed := stubExposed()
	exposed.Service.Handler = nativeFn(2, func(args []Value) (Value, error) {
		return VEffect{Tag: "ok", Run: func() (Value, error) { return VString{V: "yes"}, nil }}, nil
	})
	exposed.Service.LoadResource = loader
	exposed.Service.Policy = policy

	user := stubUser(1, "x", nil)
	resp := runDispatch(t, exposed, `{}`, vJust(user))
	if got := statusOf(t, resp); got != 200 {
		t.Fatalf("policy pass: status=%d, body=%q", got, bodyOf(t, resp))
	}
}
