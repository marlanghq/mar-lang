package project

import "testing"

// Every stdlib module is importable. Decimal was not.
//
// stdlibModules is derived from the qualified symbol table so that adding a
// builtin under a new module makes that module importable automatically --
// but it asked BaseQualifiedSymbols, which is built from a table of
// Qualified -> bare aliases and so never mentions a module whose names are
// already written qualified at the source. Decimal is the whole of one:
//
//	import Decimal      Main: cannot find imported module Decimal
//	Decimal.toString    works, with no import at all
//
// The set is the thing worth pinning rather than the one name, because the
// next module added the same way would fail the same way.
func TestEveryModuleWithQualifiedNamesIsImportable(t *testing.T) {
	set := stdlibModules()
	for _, m := range []string{
		"Decimal", "Keyboard", "Gamepad", "Canvas", "Sound", "Auth", "Service",
		"List", "String", "Dict", "Set", "Time", "Repo", "Entity", "UI", "View",
	} {
		if !set[m] {
			t.Errorf("`import %s` is rejected, and every other stdlib module is not", m)
		}
	}
}
