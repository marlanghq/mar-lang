package runtime

import "fmt"

// AdminServices supplies the runtime bodies for the Mar.Admin.* builtins.
//
// The internal/runtime package can't reach the live request counters, the DB
// handle, or the request-log ring buffer — those live up in the server layer
// (internal/jsserve). So the server injects an implementation at boot, the
// same dependency-inversion trick used by RegisterAuth and apphost.Install.
//
// Until something registers an implementation — and in `mar repl` — every
// Mar.Admin.* effect resolves to an Err through its toMsg. That's only defense
// in depth: the compile-time AdminSession capability (see Page.adminProtected)
// already guarantees normal app code can never reach these builtins, and the
// frontend panel performs them over an admin-authenticated HTTP transport.
//
// Each func returns a Mar Value whose shape MUST match the corresponding type
// scheme in typecheck/env.go (e.g. ServerInfo returns a VRecord carrying the
// marVersion / goVersion / bootedAtMs / … fields).
type AdminServices struct {
	ServerInfo     func() (Value, error)
	DBStats        func() (Value, error)
	RecentRequests func() (Value, error)
	ListEntities   func() (Value, error)
	ListEntityRows func(entity string) (Value, error)
	ListBackups    func() (Value, error)
}

// adminServices holds the injected implementation, or nil before boot.
var adminServices *AdminServices

// RegisterAdminServices wires the real Mar.Admin.* bodies. The server calls
// this once at startup. Passing nil resets to the unimplemented state, which
// tests rely on.
func RegisterAdminServices(s *AdminServices) { adminServices = s }

// adminBuiltins registers the privileged Mar.Admin.* server-introspection
// builtins. Each is shaped like Service.call —
//
//	AdminSession -> (Result String resp -> msg) -> Effect String msg
//
// — so the panel performs it as a frontend Cmd and receives the result through
// the toMsg. The AdminSession argument is the compile-time capability gate:
// only a Page.adminProtected page is handed one, so normal app code can't call
// these (caught at typecheck).
func adminBuiltins() map[string]Value {
	return map[string]Value{
		"marAdminListBackups": nativeFn(2, func(args []Value) (Value, error) {
			toMsg := args[1]
			return adminEffect("Mar.Admin.listBackups", func() (Value, error) {
				return adminDispatch(toMsg, func() (Value, error) {
					if adminServices == nil || adminServices.ListBackups == nil {
						return nil, adminUnavailable("Mar.Admin.listBackups")
					}
					return adminServices.ListBackups()
				})
			}), nil
		}),
		// Sign-in flow — frontend-transported (JS hits /_mar/admin/auth/*); the
		// Go runtime never performs these, so they're drift-coverage stubs.
		"marAdminRequestCode": nativeFn(2, func(_ []Value) (Value, error) {
			return adminEffect("Mar.Admin.requestCode", func() (Value, error) {
				return nil, adminUnavailable("Mar.Admin.requestCode")
			}), nil
		}),
		"marAdminVerifyCode": nativeFn(2, func(_ []Value) (Value, error) {
			return adminEffect("Mar.Admin.verifyCode", func() (Value, error) {
				return nil, adminUnavailable("Mar.Admin.verifyCode")
			}), nil
		}),
		"marAdminSignOut": nativeFn(1, func(_ []Value) (Value, error) {
			return adminEffect("Mar.Admin.signOut", func() (Value, error) {
				return nil, adminUnavailable("Mar.Admin.signOut")
			}), nil
		}),
		"marAdminServerInfo": nativeFn(2, func(args []Value) (Value, error) {
			toMsg := args[1]
			return adminEffect("Mar.Admin.serverInfo", func() (Value, error) {
				return adminDispatch(toMsg, func() (Value, error) {
					if adminServices == nil || adminServices.ServerInfo == nil {
						return nil, adminUnavailable("Mar.Admin.serverInfo")
					}
					return adminServices.ServerInfo()
				})
			}), nil
		}),
		"marAdminDbStats": nativeFn(2, func(args []Value) (Value, error) {
			toMsg := args[1]
			return adminEffect("Mar.Admin.dbStats", func() (Value, error) {
				return adminDispatch(toMsg, func() (Value, error) {
					if adminServices == nil || adminServices.DBStats == nil {
						return nil, adminUnavailable("Mar.Admin.dbStats")
					}
					return adminServices.DBStats()
				})
			}), nil
		}),
		"marAdminRecentRequests": nativeFn(2, func(args []Value) (Value, error) {
			toMsg := args[1]
			return adminEffect("Mar.Admin.recentRequests", func() (Value, error) {
				return adminDispatch(toMsg, func() (Value, error) {
					if adminServices == nil || adminServices.RecentRequests == nil {
						return nil, adminUnavailable("Mar.Admin.recentRequests")
					}
					return adminServices.RecentRequests()
				})
			}), nil
		}),
		"marAdminListEntities": nativeFn(2, func(args []Value) (Value, error) {
			toMsg := args[1]
			return adminEffect("Mar.Admin.listEntities", func() (Value, error) {
				return adminDispatch(toMsg, func() (Value, error) {
					if adminServices == nil || adminServices.ListEntities == nil {
						return nil, adminUnavailable("Mar.Admin.listEntities")
					}
					return adminServices.ListEntities()
				})
			}), nil
		}),
		"marAdminListEntityRows": nativeFn(3, func(args []Value) (Value, error) {
			// args[0] is the AdminSession (the compile-time capability, with
			// no runtime use here); args[1] is the entity name; args[2] the toMsg.
			entity, ok := args[1].(VString)
			if !ok {
				return nil, fmt.Errorf("Mar.Admin.listEntityRows: expected a String entity name, got %T", args[1])
			}
			toMsg := args[2]
			return adminEffect("Mar.Admin.listEntityRows", func() (Value, error) {
				return adminDispatch(toMsg, func() (Value, error) {
					if adminServices == nil || adminServices.ListEntityRows == nil {
						return nil, adminUnavailable("Mar.Admin.listEntityRows")
					}
					return adminServices.ListEntityRows(entity.V)
				})
			}), nil
		}),
	}
}

// adminDispatch runs the introspection body and threads its outcome through
// toMsg as a Result — toMsg (Ok value) on success, toMsg (Err message) on
// failure — mirroring how Service.call delivers its response.
func adminDispatch(toMsg Value, produce func() (Value, error)) (Value, error) {
	v, err := produce()
	if err != nil {
		return apply(toMsg, VCtor{Tag: "Err", Args: []Value{VString{V: err.Error()}}})
	}
	return apply(toMsg, VCtor{Tag: "Ok", Args: []Value{v}})
}

// adminEffect builds the deferred-effect wrapper every Mar.Admin.* builtin
// returns: the work runs when the Mar program performs the effect, not when
// the builtin is applied.
func adminEffect(tag string, run func() (Value, error)) VEffect {
	return VEffect{Tag: tag, Run: run}
}

func adminUnavailable(name string) error {
	return fmt.Errorf("%s is only available in the running Mar server", name)
}
