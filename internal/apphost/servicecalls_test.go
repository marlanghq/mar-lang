package apphost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/jsserve"
	"mar/internal/project"
	"mar/internal/runtime"
)

// copyProject makes a writable copy of an example so a test can take one
// thing away from it and boot what is left.
func copyProject(t *testing.T, from string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.Walk(from, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		if strings.HasSuffix(rel, ".db") || strings.Contains(rel, string(filepath.Separator)) {
			return nil // sources live at the top level; databases are not sources
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatalf("copying %s: %v", from, err)
	}
	return dst
}

// boot runs a project as far as `main`, which is what registers the routes.
func boot(t *testing.T, dir string) error {
	t.Helper()
	runtime.ResetForReload()
	runtime.SetDBPath(filepath.Join(t.TempDir(), "probe.db"))
	lp := &jsserve.LiveProgram{}
	rEnv, mods, _, err := project.LoadIntoEnvWithModulesAndHook(filepath.Join(dir, "Main.mar"),
		func(env *runtime.Env, ms []*ast.Module) { Install(env, ms, 0, lp) })
	if err != nil {
		return err
	}
	mainV, ok := project.LookupMain(rEnv, mods)
	if !ok {
		t.Fatal("no main")
	}
	eff, ok := mainV.(runtime.VEffect)
	if !ok {
		t.Fatal("main is not an effect")
	}
	_, err = eff.Run()
	return err
}

// Measured on this exact project before the check existed: `mar check` passes,
// `GET /notes` answers 200 text/html — the app's own shell — and the page
// reports `decode failed: Unexpected token '<'`.
func TestCallingAContractNothingMountsIsRefusedAtBoot(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a project; skipped under -short")
	}
	dir := copyProject(t, "../../examples/notes-fullstack")

	backend := filepath.Join(dir, "Backend.mar")
	src, err := os.ReadFile(backend)
	if err != nil {
		t.Fatal(err)
	}
	// Take the handler away; the page still calls the contract.
	cut := strings.Replace(string(src),
		"    [ Service.implement Shared.listAllNotes (\\_ -> Repo.all notes)\n    ,",
		"    [", 1)
	if cut == string(src) {
		t.Fatal("the example moved; this test is editing text that is no longer there")
	}
	if err := os.WriteFile(backend, []byte(cut), 0o644); err != nil {
		t.Fatal(err)
	}

	err = boot(t, dir)
	if err == nil {
		t.Fatal("the page's only fetch has no handler, and the boot said nothing")
	}
	for _, want := range []string{"listAllNotes", "GET /notes", "Service.implement"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message must mention %q, got: %v", want, err)
		}
	}
}

func TestTheSameProjectUntouchedBoots(t *testing.T) {
	// The other half of the measurement: with the handler in place the same
	// project boots and the route answers JSON.
	if testing.Short() {
		t.Skip("boots a project; skipped under -short")
	}
	if err := boot(t, copyProject(t, "../../examples/notes-fullstack")); err != nil {
		t.Fatalf("the example as shipped: %v", err)
	}
}

func TestAContractThatIsNotNamedIsPassedOver(t *testing.T) {
	// `Service.call (pick model) ...` resolves to nothing here. The check
	// refuses what it can prove and stays quiet otherwise, because the
	// alternative is refusing a program on a guess.
	call := &ast.EApp{Fn: &ast.EApp{
		Fn:  &ast.EQualified{Module: ast.ModuleName{"Service"}, Name: "call"},
		Arg: &ast.EApp{Fn: &ast.EVar{Name: "pick"}, Arg: &ast.EVar{Name: "model"}},
	}, Arg: &ast.EUnit{}}
	mods := []*ast.Module{{
		Name:  ast.ModuleName{"Frontend"},
		Decls: []ast.Decl{&ast.ValueDecl{Name: "init", Body: call}},
	}}
	env := runtime.NewEnv()
	if err := CheckServiceCalls(mods, env, nil); err != nil {
		t.Fatalf("a computed contract is not something to refuse: %v", err)
	}
}

func TestACallOnAMountedContractIsAccepted(t *testing.T) {
	contract := runtime.VService{Verb: "GET", Path: "/notes"}
	env := runtime.NewEnv()
	env.Define("Shared.listAllNotes", contract)

	mods := []*ast.Module{{
		Name: ast.ModuleName{"Frontend"},
		Decls: []ast.Decl{&ast.ValueDecl{Name: "init", Body: &ast.EApp{
			Fn:  &ast.EQualified{Module: ast.ModuleName{"Service"}, Name: "call"},
			Arg: &ast.EQualified{Module: ast.ModuleName{"Shared"}, Name: "listAllNotes"},
		}}},
	}}
	mounted := []runtime.VExposedService{{Service: contract}}
	if err := CheckServiceCalls(mods, env, mounted); err != nil {
		t.Fatalf("this contract has a handler: %v", err)
	}
	if err := CheckServiceCalls(mods, env, nil); err == nil {
		t.Fatal("with nothing mounted, the same call has no handler")
	}
}
