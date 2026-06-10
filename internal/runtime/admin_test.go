package runtime

import "testing"

// adminIdentityToMsg is a toMsg that returns the Result it's handed, so the
// performed effect yields the raw Ok/Err the dispatch produced — convenient
// for asserting on the outcome.
func adminIdentityToMsg() Value {
	return nativeFn(1, func(args []Value) (Value, error) { return args[0], nil })
}

// runAdminBuiltin applies all args to a Mar.Admin.* builtin (curried) and
// performs the resulting effect, returning its outcome.
func runAdminBuiltin(t *testing.T, name string, args ...Value) Value {
	t.Helper()
	cur := adminBuiltins()[name]
	for _, a := range args {
		v, err := apply(cur, a)
		if err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		cur = v
	}
	ve, ok := cur.(VEffect)
	if !ok {
		t.Fatalf("%s did not yield a VEffect, got %T", name, cur)
	}
	out, err := ve.Run()
	if err != nil {
		t.Fatalf("performing %s: %v", name, err)
	}
	return out
}

// The Mar.Admin.* builtins resolve in BaseEnv (so the LSP sees them and the
// type system can gate them), but their bodies are injected by the server at
// boot via RegisterAdminServices. Each is shaped like Service.call —
// AdminSession -> (Result String resp -> msg) -> Effect String msg — so this
// pins that contract: unregistered → toMsg (Err …); registered → toMsg (Ok …);
// and listEntityRows forwards its entity argument.
func TestAdminServicesInjection(t *testing.T) {
	defer RegisterAdminServices(nil)
	toMsg := adminIdentityToMsg()
	session := VString{V: "session"}

	// Unregistered: performing the effect resolves to Err through toMsg.
	RegisterAdminServices(nil)
	out := runAdminBuiltin(t, "marAdminServerInfo", session, toMsg)
	if c, ok := out.(VCtor); !ok || c.Tag != "Err" {
		t.Fatalf("unregistered serverInfo should resolve to Err, got %#v", out)
	}

	// Registered: the effect resolves to Ok carrying the injected value.
	RegisterAdminServices(&AdminServices{
		ServerInfo: func() (Value, error) { return VString{V: "info"}, nil },
	})
	got := runAdminBuiltin(t, "marAdminServerInfo", session, toMsg)
	if c, ok := got.(VCtor); !ok || c.Tag != "Ok" || len(c.Args) != 1 {
		t.Fatalf("registered serverInfo should resolve to Ok(value), got %#v", got)
	} else if s, ok := c.Args[0].(VString); !ok || s.V != "info" {
		t.Fatalf("expected Ok(VString \"info\"), got %#v", c.Args[0])
	}

	// listEntityRows is a 3-arg builtin (session, entity, toMsg): it must
	// forward the entity name to the injected body.
	var gotEntity string
	RegisterAdminServices(&AdminServices{
		ListEntityRows: func(entity string) (Value, error) {
			gotEntity = entity
			return VString{V: "rows"}, nil
		},
	})
	rows := runAdminBuiltin(t, "marAdminListEntityRows", session, VString{V: "users"}, toMsg)
	if gotEntity != "users" {
		t.Fatalf("expected entity \"users\" forwarded to body, got %q", gotEntity)
	}
	if c, ok := rows.(VCtor); !ok || c.Tag != "Ok" {
		t.Fatalf("listEntityRows should resolve to Ok, got %#v", rows)
	}
}
