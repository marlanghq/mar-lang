package typecheck

import (
	"strings"
	"testing"
)

// The whole point of the AdminSession capability: Mar.Admin.* requires an
// AdminSession as its first argument, and only Page.adminProtected ever hands
// one out. So normal code can't call Mar.Admin.* — caught at COMPILE time, not
// runtime. (The functions are shaped like Service.call:
// AdminSession -> (Result String resp -> msg) -> Effect String msg.)

// A plain top-level reference can't satisfy the AdminSession argument
// (there's no value of that type anywhere in user-reachable scope).
func TestAdminGateRejectsPlainCall(t *testing.T) {
	src := `module M exposing (..)
broken = Mar.Admin.serverInfo "not an admin session"
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected a type error calling Mar.Admin.serverInfo without an AdminSession; got clean check")
	}
	if !strings.Contains(err.Error(), "AdminSession") {
		t.Fatalf("error should name the AdminSession capability, got: %v", err)
	}
}

// A NORMAL page (Page.create) is never handed an AdminSession: its init is
// just the (model, effect) tuple, with no session binding in scope. The only
// way to call Mar.Admin.* from one is to fabricate the capability, and that
// is a compile error (AdminSession has no user-facing constructor, so
// nothing the user can write unifies with it).
func TestAdminGateRejectsInNormalPage(t *testing.T) {
	src := `module M exposing (..)
type Msg = Done
page =
    Page.create
        { path = "/"
        , init = ((), Mar.Admin.serverInfo "forged" (\_ -> Done))
        , update = \_ model -> (model, Cmd.none)
        , view = \model -> UI.text [] "x"
        }
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected a type error: a normal page has no AdminSession to pass to Mar.Admin.*; got clean check")
	}
	if !strings.Contains(err.Error(), "AdminSession") {
		t.Fatalf("error should name the AdminSession capability, got: %v", err)
	}
}

// An ADMIN page (Page.adminProtected) is handed an AdminSession in
// init/update/view, so it CAN call Mar.Admin.* — performing the result through
// a toMsg, exactly like Service.call.
func TestAdminGateAllowsInAdminPage(t *testing.T) {
	src := `module M exposing (..)
type Msg = Ignored
page =
    Page.adminProtected
        { path = "/_mar/admin"
        , title = "Admin"
        , init = \admin -> ((), Mar.Admin.serverInfo admin (\_ -> Ignored))
        , update = \_ _ model -> (model, Cmd.none)
        , view = \_ model -> UI.text [] "ok"
        }
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("an admin page should be able to call Mar.Admin.* with its AdminSession; got: %v", err)
	}
}
