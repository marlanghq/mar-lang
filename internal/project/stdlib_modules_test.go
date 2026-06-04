package project

import "testing"

// isStdlib must recognise every built-in module — including the ones the old
// hand-maintained list silently omitted (Dict, Set, Time, Tuple, Char, Auth,
// Nav, Service, Server, Db), which made `import Dict` etc. fail as if the
// module were a missing local file.
func TestIsStdlibCoversBuiltinModules(t *testing.T) {
	for _, m := range []string{
		"List", "String", "Maybe", "Result", "Effect", "JSON",
		"UI", "View", "App", "Page", "Endpoint", "Http", "Entity", "Repo", "Response",
		"Dict", "Set", "Time", "Tuple", "Char", "Auth", "Nav", "Service",
	} {
		if !isStdlib(m) {
			t.Errorf("isStdlib(%q) = false, want true (real built-in module)", m)
		}
	}
}

// Names that are NOT built-in modules must be treated as project files —
// including IO/Screen, which a stale doc comment used to list as built-ins
// but which the language never actually provided.
func TestIsStdlibRejectsNonModules(t *testing.T) {
	for _, m := range []string{"Frontend", "Shared", "Backend", "IO", "Screen", "Nope"} {
		if isStdlib(m) {
			t.Errorf("isStdlib(%q) = true, want false (not a built-in module)", m)
		}
	}
}
