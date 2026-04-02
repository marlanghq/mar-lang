package expr

var BuiltinValueNames = []string{
	"anonymous",
	"user_authenticated",
	"user_email",
	"user_id",
	"user_role",
}

var builtinValueSet = func() map[string]struct{} {
	out := make(map[string]struct{}, len(BuiltinValueNames))
	for _, name := range BuiltinValueNames {
		out[name] = struct{}{}
	}
	return out
}()

func IsBuiltinValueName(name string) bool {
	_, ok := builtinValueSet[name]
	return ok
}

func AllowedVariablesWithBuiltins(extra map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(builtinValueSet)+len(extra))
	for name := range builtinValueSet {
		out[name] = struct{}{}
	}
	for name := range extra {
		out[name] = struct{}{}
	}
	return out
}
