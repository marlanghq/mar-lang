package runtime

import "testing"

// Deleting `Auth.config` from the source and saving used to change nothing:
// the registry survived the reload, so `mar dev` went on protecting routes
// with a configuration the program no longer contained. It took a restart to
// notice. This was found when two examples that declare no auth at all were
// seen, by a boot check, as having it.
func TestReloadDoesNotInheritTheAuthOfThePreviousProgram(t *testing.T) {
	t.Cleanup(ResetAuth)

	RegisterAuth(VAuth{SignInPath: "/sign-in", EmailSubject: "first program"})
	if CurrentAuth() == nil {
		t.Fatal("the first program registered auth")
	}

	// A hot reload: the next program declares no Auth.config, so nothing
	// re-registers.
	ResetForReload()

	if got := CurrentAuth(); got != nil {
		t.Fatalf("a program with no `Auth.config` inherited one: %+v", *got)
	}
}

func TestReloadKeepsTheAuthOfAProgramThatStillDeclaresIt(t *testing.T) {
	t.Cleanup(ResetAuth)

	RegisterAuth(VAuth{SignInPath: "/old"})
	ResetForReload()
	// Re-evaluating Main runs Auth.config again — the only way in.
	RegisterAuth(VAuth{SignInPath: "/new"})

	got := CurrentAuth()
	if got == nil || got.SignInPath != "/new" {
		t.Fatalf("the reloaded source decides, got %+v", got)
	}
}
