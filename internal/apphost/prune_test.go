package apphost

import (
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/parser"
)

// parseMods parses each source and returns the modules, so a test can describe
// a small project the way a user would write it.
func parseMods(t *testing.T, sources ...string) []*ast.Module {
	t.Helper()
	var mods []*ast.Module
	for i, src := range sources {
		m, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("source %d: %v", i, err)
		}
		mods = append(mods, m)
	}
	return mods
}

func rootRef(module, name string) ast.Expr {
	return &ast.EQualified{Module: parseModulePath(module), Name: name}
}

// keptNames reports the value declarations that survived, module by module.
func keptNames(mods []*ast.Module) map[string][]string {
	out := map[string][]string{}
	for _, m := range mods {
		name := joinModuleName(m.Name)
		for _, d := range m.Decls {
			if vd, ok := d.(*ast.ValueDecl); ok {
				out[name] = append(out[name], vd.Name)
			}
		}
	}
	return out
}

func has(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// The case that sent a database schema to every browser: the page imports the
// backend module for a TYPE, which erases, and used to drag the entity value
// along with it.
func TestPruneDropsSchemaThePageOnlyImportsATypeFrom(t *testing.T) {
	mods := parseMods(t,
		`module Backend.Notes exposing (Note, notes)


type alias Note =
    { id : Int }


notes : Entity Note
notes =
    Entity.define { name = "notes", columns = { id = Entity.serial }, uniques = [] }
`,
		`module Frontend.Home exposing (page)


import Backend.Notes exposing (Note)


blank : Note
blank = { id = 0 }


page : Int
page = blank.id
`)

	pruned := pruneToPageReachable(mods, []ast.Expr{rootRef("Frontend.Home", "page")})
	kept := keptNames(pruned)

	if has(kept["Backend.Notes"], "notes") {
		t.Error("the entity survived pruning; the schema still ships to the browser")
	}
	if !has(kept["Frontend.Home"], "blank") {
		t.Error("a value the page uses was dropped")
	}
}

// The other half: a service implementation, and the Repo call inside it, is
// reachable only from the backend's service list, never from a page.
func TestPruneDropsServiceImplementations(t *testing.T) {
	mods := parseMods(t,
		`module Shared exposing (listNotes)


listNotes : Service {} (List Int)
listNotes =
    Service.declare GET "/api/notes"
`,
		`module Backend.Notes exposing (impl)


import Shared exposing (listNotes)


impl : ExposedService
impl =
    Service.implement listNotes (\_ -> Repo.all notes)


notes : Entity { id : Int }
notes =
    Entity.define { name = "notes", columns = { id = Entity.serial }, uniques = [] }
`,
		`module Frontend.Home exposing (page)


import Shared exposing (listNotes)


page : Cmd ()
page = Service.call listNotes {}
`)

	pruned := pruneToPageReachable(mods, []ast.Expr{rootRef("Frontend.Home", "page")})
	kept := keptNames(pruned)

	if !has(kept["Shared"], "listNotes") {
		t.Fatal("the service CONTRACT was dropped; the client needs its verb and path")
	}
	if has(kept["Backend.Notes"], "impl") {
		t.Error("the service implementation shipped to the browser")
	}
	if has(kept["Backend.Notes"], "notes") {
		t.Error("the entity shipped to the browser")
	}
	if leaks := findBackendOnlyLeaks(pruned); len(leaks) > 0 {
		t.Errorf("client bundle still calls server-only builtins: %+v", leaks)
	}
}

// The pruner is only safe if the kept set is CLOSED under reference, and the
// way that breaks is an expression kind the walker forgets to descend into.
// Each case here hides a reference to `needed` inside a different shape; a
// missing case in walk() drops it and this fails by name.
func TestPruneDescendsIntoEveryExpressionShape(t *testing.T) {
	shapes := map[string]string{
		"app":          `f needed`,
		"binop":        `needed + 1`,
		"lambda":       `(\_ -> needed)`,
		"if-cond":      `if needed > 0 then 1 else 2`,
		"if-then":      `if True then needed else 2`,
		"if-else":      `if True then 1 else needed`,
		"case-subject": `case needed of` + "\n        _ ->\n            1",
		"case-branch":  `case 0 of` + "\n        _ ->\n            needed",
		"let-binding":  `let x = needed in x`,
		"let-body":     `let x = 1 in needed + x`,
		"tuple":        `Tuple.first ( needed, 0 )`,
		"list":         `List.length [ needed ]`,
		"record":       `.a { a = needed }`,
		"record-update": `let r = { a = 0 } in
        .a { r | a = needed }`,
		"field-access": `.a { a = needed }`,
		"negate":       `0 - needed`,
	}

	for name, body := range shapes {
		t.Run(name, func(t *testing.T) {
			src := `module M exposing (root)


f : Int -> Int
f n = n


needed : Int
needed = 7


root : Int
root =
    ` + body + `
`
			mods := parseMods(t, src)
			pruned := pruneToPageReachable(mods, []ast.Expr{rootRef("M", "root")})
			if !has(keptNames(pruned)["M"], "needed") {
				t.Errorf("walk() does not descend into %s: a referenced declaration was dropped, "+
					"which is an \"unbound name\" crash in the browser", name)
			}
		})
	}
}

// Transitive reach: the page uses a helper that uses another helper.
func TestPruneKeepsTransitivelyReachedDeclarations(t *testing.T) {
	mods := parseMods(t,
		`module Util exposing (shout, upper)


upper : String -> String
upper s = String.toUpper s


shout : String -> String
shout s = upper s ++ "!"


unused : String
unused = "nobody calls me"
`,
		`module Frontend.Home exposing (page)


import Util exposing (shout)


page : String
page = shout "hi"
`)

	pruned := pruneToPageReachable(mods, []ast.Expr{rootRef("Frontend.Home", "page")})
	kept := keptNames(pruned)

	for _, want := range []string{"shout", "upper"} {
		if !has(kept["Util"], want) {
			t.Errorf("%s was dropped but is reachable from the page", want)
		}
	}
	if has(kept["Util"], "unused") {
		t.Error("an unreachable declaration survived pruning")
	}
}

// A bare name can come from the module itself or from any import that exposes
// it. Resolving it wrong would drop a declaration, so all candidates are kept.
func TestPruneKeepsEveryCandidateForAnAmbiguousBareName(t *testing.T) {
	mods := parseMods(t,
		`module A exposing (helper)


helper : Int
helper = 1
`,
		`module B exposing (helper)


helper : Int
helper = 2
`,
		`module Frontend.Home exposing (page)


import A exposing (helper)
import B exposing (helper)


page : Int
page = helper
`)

	pruned := pruneToPageReachable(mods, []ast.Expr{rootRef("Frontend.Home", "page")})
	kept := keptNames(pruned)
	for _, m := range []string{"A", "B"} {
		if !has(kept[m], "helper") {
			t.Errorf("%s.helper was dropped; an ambiguous bare name must keep every candidate", m)
		}
	}
}

// The backstop. After pruning, a server-only builtin in client code is not an
// accident of bundling: someone wrote it there.
func TestBackendOnlyLeakIsReportedWithItsDeclaration(t *testing.T) {
	mods := parseMods(t,
		`module Frontend.Home exposing (page)


notes : Entity { id : Int }
notes =
    Entity.define { name = "notes", columns = { id = Entity.serial }, uniques = [] }


page : Task (List { id : Int })
page = Repo.all notes
`)

	pruned := pruneToPageReachable(mods, []ast.Expr{rootRef("Frontend.Home", "page")})
	leaks := findBackendOnlyLeaks(pruned)
	if len(leaks) == 0 {
		t.Fatal("a page calling Repo.all was not reported")
	}
	var builtins []string
	for _, l := range leaks {
		if l.Module != "Frontend.Home" {
			t.Errorf("leak reported against the wrong module: %q", l.Module)
		}
		builtins = append(builtins, l.Builtin)
	}
	joined := strings.Join(builtins, ",")
	for _, want := range []string{"Repo.all", "Entity.define"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%s was not reported among the leaks (got %s)", want, joined)
		}
	}
}
