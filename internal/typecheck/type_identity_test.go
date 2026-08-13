package typecheck

import (
	"strings"
	"testing"

	"mar/internal/parser"
)

// A type is identified by its module (ADR 0027). Before that, a user type
// became TCon{base name} with the module discarded, so two modules that each
// declared `Color` produced ONE type, and a `case` the checker had proven
// total fell through at runtime.
//
// These drive the real multi-module path, because that is the only place the
// defect lived: every one of them passes trivially inside a single module.
// The harness below is the checking pipeline's essentials (internal/project),
// small enough to be obviously honest.

type modSrc struct {
	name string
	src  string
}

// checkProject checks modules in the given order, threading each one's exports
// to the ones after it exactly as the project pipeline does: types keyed
// canonically (`A.T`), values and constructors under `Module.name`.
func checkProject(t *testing.T, mods []modSrc) error {
	t.Helper()
	valueEnv := BaseEnv()
	aliasesBy := map[string]map[string]TypeAlias{}
	customsBy := map[string]map[string]CustomType{}

	for _, m := range mods {
		parsed, err := parser.Parse(m.src)
		if err != nil {
			t.Fatalf("parse %s: %v", m.name, err)
		}
		importedAliases := map[string]TypeAlias{}
		importedCustoms := map[string]CustomType{}
		for _, imp := range parsed.Imports {
			impName := strings.Join(imp.Module, ".")
			for k, v := range aliasesBy[impName] {
				importedAliases[impName+"."+k] = v
			}
			for k, v := range customsBy[impName] {
				importedCustoms[impName+"."+k] = v
			}
		}
		res, err := CheckModuleWith(parsed, valueEnv, importedAliases, importedCustoms)
		if err != nil {
			return err
		}
		for vname, ty := range res.ValueTypes {
			valueEnv.Define(m.name+"."+vname, ty)
		}
		aliasesBy[m.name] = res.TypeAliases
		customsBy[m.name] = res.CustomTypes
	}
	return nil
}

func mustFail(t *testing.T, name string, mods []modSrc, want string) {
	t.Helper()
	err := checkProject(t, mods)
	if err == nil {
		t.Fatalf("%s: expected a compile error, got none", name)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("%s: wrong error.\n got: %v\nwant it to contain: %s", name, err, want)
	}
}

func mustPass(t *testing.T, name string, mods []modSrc) {
	t.Helper()
	if err := checkProject(t, mods); err != nil {
		t.Fatalf("%s: expected it to compile, got: %v", name, err)
	}
}

const modAColor = `module A exposing (Color(..), describe)
type Color = Red | Green
describe : Color -> String
describe c = case c of
    Red -> "red"
    Green -> "green"
`

const modBColor = `module B exposing (Color(..))
type Color = Blue | Yellow
`

// THE hole. `A.describe` is exhaustive over A.Color and the checker agrees;
// handing it a B.Color used to compile, and the program died at runtime with
// "no case branch matched". ADR 0022's guarantee did not survive a module
// boundary.
func TestCustomTypesFromDifferentModulesAreDifferentTypes(t *testing.T) {
	mustFail(t, "B.Color passed where A.Color is expected", []modSrc{
		{"A", modAColor},
		{"B", modBColor},
		{"Main", `module Main exposing (oops)
import A
import B
oops : String
oops = A.describe B.Blue
`},
	}, "A.Color")
}

// The same two modules, used correctly, must still compile: otherwise the
// test above would pass on a checker that simply refused everything.
func TestSameNamedCustomsDoNotBlockCorrectUse(t *testing.T) {
	mustPass(t, "A.Color used with A's constructors", []modSrc{
		{"A", modAColor},
		{"B", modBColor},
		{"Main", `module Main exposing (fine)
import A
import B
fine : String
fine = A.describe A.Red
`},
	})
}

const modAT = `module A exposing (T, mk)
type alias T = { a : Int }
mk : T
mk = { a = 1 }
`

const modBT = `module B exposing (T)
type alias T = { b : String }
`

// A qualified alias resolves to ITS module. This used to land on B's `T`: a
// module Main merely imported, and the answer flipped with the import order.
func TestQualifiedAliasResolvesToItsOwnModule(t *testing.T) {
	main := `module Main exposing (readIt)
import %s
import %s
readIt : A.T -> Int
readIt v = v.a
`
	mustPass(t, "A before B", []modSrc{
		{"A", modAT}, {"B", modBT},
		{"Main", strings.Replace(strings.Replace(main, "%s", "A", 1), "%s", "B", 1)},
	})
	// Same program, imports swapped. Order used to decide whether it compiled.
	mustPass(t, "B before A", []modSrc{
		{"A", modAT}, {"B", modBT},
		{"Main", strings.Replace(strings.Replace(main, "%s", "B", 1), "%s", "A", 1)},
	})
}

// A bare name two imports both claim is an error AT THE USE, naming both. It
// used to be resolved by import order, silently.
func TestBareNameClaimedByTwoImportsIsAmbiguous(t *testing.T) {
	mustFail(t, "T exposed by A and B", []modSrc{
		{"A", modAT}, {"B", modBT},
		{"Main", `module Main exposing (readIt)
import A exposing (T)
import B exposing (T)
readIt : T -> Int
readIt v = v.a
`},
	}, "ambiguous")
}

// A module's own declaration shadows an import of the same name. This is the
// framework's own idiom: every page declares `Msg`, and a page that reads
// shared state imports a module that has one too. Making that ambiguous would
// break `Msg` inside the page that declared it.
func TestLocalDeclarationShadowsAnImport(t *testing.T) {
	mustPass(t, "local Msg wins over the imported one", []modSrc{
		{"Global", `module Global exposing (Msg(..))
type Msg = Added
`},
		{"Page", `module Page exposing (handle)
import Global
type Msg = Clicked
handle : Msg -> Int
handle m = case m of
    Clicked -> 1
`},
	})
}

// A qualified name the module does not export is an error where it is written,
// with what the module DOES have. It used to become an opaque type, so the
// complaint surfaced wherever that type was first used instead.
func TestQualifiedNameThatDoesNotExistIsAnError(t *testing.T) {
	mustFail(t, "A.Colour typo", []modSrc{
		{"A", modAColor},
		{"Main", `module Main exposing (name)
import A
name : A.Colour -> String
name _ = "x"
`},
	}, "has no type `Colour`")
}
