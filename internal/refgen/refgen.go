// Package refgen derives the Mar stdlib function reference (categories, type
// signatures, descriptions, and worked examples) from the one source that owns
// the signatures: the typecheck package's BaseEnv. It mirrors internal/ctorgen
// — the signatures are NOT hand-written, they are read from the compiler and
// pretty-printed, so they can never drift from the language.
//
// The authored parts (which category a function belongs to, its prose
// description, and its examples) live in content.go for the pure-data core and
// content_app.go for everything an app reaches for. The examples are not just
// prose: examples_test.go compiles every one of them against the live compiler,
// and evaluates the ones that state an equality, requiring True. So a wrong
// example fails the build, exactly like a drifted signature would.
//
// The generator emits a Mar data module consumed by the website's /reference
// section. Staleness + coverage + example tests in this package lock it. To
// change the set: extend Modules or the maps in the content files, then run
//
//	go generate ./internal/refgen
//
//go:generate go run ./cmd
package refgen

import (
	"fmt"
	"strings"

	"mar/internal/typecheck"
)

// DataMarRelPath is where the generated Mar data module lives, relative to the
// repo root. The website's /reference pages import it.
const DataMarRelPath = "examples/mar-website/Frontend/Reference/Data.mar"

// Modules covered by the reference, in display order: the data core first, then
// what an app is built out of. Every export of a listed module has to be
// categorized and described, so a module is in here all of it or not at all.
// Mar.Admin is deliberately absent — it is the admin panel's own API, not one
// apps call.
var Modules = []string{"Basics", "List", "String", "Maybe", "Result", "Tuple", "Char", "Dict", "Set", "Decimal", "Math", "Time", "Random", "App", "Page", "Nav", "UI", "Canvas", "Sound", "Keyboard", "Gamepad", "Device", "Cmd", "Sub", "Task", "Service", "Entity", "Repo", "Auth", "Http", "JSON"}

// BasicsModule is the display name for the builtins that live in no module —
// the ones written bare, without a qualifier. There is no `import Basics` in
// Mar and no `Basics.not`: the name is a shelf for the reference to put them
// on, so they are not the only part of the stdlib the site cannot show.
const BasicsModule = "Basics"

// exportsOf returns the documented surface of a module. Basics is the one
// module whose members are not qualified, so its names come from the bare
// globals rather than from a Module. prefix scan.
func exportsOf(mod string) map[string]typecheck.Type {
	if mod == BasicsModule {
		return typecheck.BareGlobals()
	}
	return typecheck.BaseEnv().ExportsOf(mod)
}

// ModuleGroup is a named group of modules on the reference index, in display
// order.
type ModuleGroup struct {
	Title   string
	Modules []string
}

// CatGroup is a named group of functions inside a module, in display order.
type CatGroup struct {
	Name  string
	Funcs []string
}

// FnEntry is one documented stdlib function.
type FnEntry struct {
	Module    string   // "List"
	Name      string   // "map"
	Signature string   // the type after the colon: "(a -> b) -> List a -> List b"
	Side      string   // "frontend", "backend", "both", "entry point"
	Desc      string   // authored prose (may be multi-sentence, single line)
	Examples  []string // authored `expr == expected` lines, verified True (may be empty)
}

// Qualified is "List.map".
func (e FnEntry) Qualified() string { return e.Module + "." + e.Name }

// Section is a category's worth of entries within a module.
type Section struct {
	Category string
	Entries  []FnEntry
}

// entryOf builds the FnEntry for a qualified stdlib function, pulling its
// signature from the compiler and its prose/examples from content.go. ok is
// false when the name is not actually exported (a typo in the categories
// table); the coverage test turns that into a failure.
func entryOf(mod, name string, exps map[string]typecheck.Type) (FnEntry, bool) {
	t, ok := exps[name]
	if !ok {
		return FnEntry{}, false
	}
	q := mod + "." + name
	return FnEntry{
		Module:    mod,
		Name:      name,
		Signature: typecheck.Pretty(t),
		Side:      sideLabel(mod, name, q),
		Desc:      descriptions[q],
		Examples:  examples[q],
	}, true
}

// sideLabel is the badge text: which side of an app this function runs on,
// read from the compiler's own table so the page cannot contradict the check
// that enforces it. Same reason the signature is not hand-written either.
//
// `Basics` is the reference's name for the bare globals and is not a module you
// can import, so its entries are looked up unqualified — that is how they are
// spelled in `BaseEnv`, and how `SideOf` expects them.
func sideLabel(mod, name, qualified string) string {
	lookup := qualified
	if mod == "Basics" {
		lookup = name
	}
	s, ok := typecheck.SideOf(lookup)
	if !ok {
		// Only reachable if a module is missing from the side table, which
		// TestEveryBuiltinHasASide fails the build over. Say nothing rather
		// than guess: an empty badge renders as no badge.
		return ""
	}
	return s.String()
}

// ModuleSideLabel is the side of a whole module: what the module page states
// once, so the per-function rows only have to speak up when they disagree with
// it. `Basics` is the shelf for the bare globals, which are all pure.
func ModuleSideLabel(mod string) string {
	if mod == BasicsModule {
		return typecheck.SideBoth.String()
	}
	s, ok := typecheck.SideOfModule(mod)
	if !ok {
		return ""
	}
	return s.String()
}

// SectionsOf returns the category groups of a module, each populated with its
// entries in authored order. Empty groups are dropped.
func SectionsOf(mod string) []Section {
	exps := exportsOf(mod)
	var out []Section
	for _, cg := range categories[mod] {
		var es []FnEntry
		for _, name := range cg.Funcs {
			if e, ok := entryOf(mod, name, exps); ok {
				es = append(es, e)
			}
		}
		if len(es) > 0 {
			out = append(out, Section{Category: cg.Name, Entries: es})
		}
	}
	return out
}

// Entries returns every documented function across every module, flattened in
// category order.
func Entries() []FnEntry {
	var out []FnEntry
	for _, mod := range Modules {
		for _, s := range SectionsOf(mod) {
			out = append(out, s.Entries...)
		}
	}
	return out
}

// displayExamples turns each verifier-facing example into its display form: a
// list of LINES, one entry per authored example.
//
// An `expr == result` equality becomes `expr    -- result`: the page shows the
// result as a Mar comment, while the tests still evaluate the equality.
//
// A few examples cannot be a single expression at all. A Path only comes into
// being through an annotated binding, so the honest example for Nav.pushTo has
// to declare a route before using it. Those are authored as a declaration block
// and split here into one line each.
//
// The nesting is what keeps a multi-line example ONE example. Flattening every
// line into a single list made the page draw each line as its own card row, so
// a snippet's blank separator line rendered as an empty 22px row between two
// dividers, and the "Example"/"Examples" header counted lines instead of
// examples. Blank lines are dropped here rather than carried: the row boundary
// around each example now does the separating they were there for.
func displayExamples(raw []string) [][]string {
	var out [][]string
	for _, ex := range raw {
		var lines []string
		for _, line := range strings.Split(strings.Replace(ex, " == ", "    -- ", 1), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			lines = append(lines, line)
		}
		if len(lines) > 0 {
			out = append(out, lines)
		}
	}
	return out
}

// marList renders a Mar list literal of already-quoted string elements, or []
// when empty.
func marStringList(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	parts := make([]string, len(items))
	for i, s := range items {
		parts[i] = fmt.Sprintf("%q", s)
	}
	return "[ " + strings.Join(parts, ", ") + " ]"
}

// marNestedStringList renders `List (List String)` — one inner list per worked
// example, holding that example's lines.
func marNestedStringList(groups [][]string) string {
	if len(groups) == 0 {
		return "[]"
	}
	parts := make([]string, len(groups))
	for i, g := range groups {
		parts[i] = marStringList(g)
	}
	return "[ " + strings.Join(parts, ", ") + " ]"
}

// MarModule renders the whole generated Frontend.Reference.Data module: a
// modules list, a grouped sectionsFor lookup (for the per-module pages), and a
// flat entriesFor derived from it (for search). Descriptions, signatures, and
// examples are quote/backslash free by construction, so %q yields valid Mar
// string literals.
func MarModule() string {
	var b strings.Builder
	b.WriteString("module Frontend.Reference.Data exposing (Entry, Section, Group, modules, moduleGroups, sectionsFor, entriesFor, blurbFor, sideFor)\n\n\n")
	b.WriteString("-- Code generated by internal/refgen; DO NOT EDIT.\n")
	b.WriteString("--\n")
	b.WriteString("-- The stdlib function reference behind the website's /reference section.\n")
	b.WriteString("-- Every signature is read straight from the Mar compiler (typecheck.BaseEnv)\n")
	b.WriteString("-- and pretty-printed, so it can never drift from the language. The categories,\n")
	b.WriteString("-- descriptions, and examples are authored in internal/refgen/content.go, and\n")
	b.WriteString("-- every example is compiled and run by the generator's tests. To change what\n")
	b.WriteString("-- appears here, edit the stdlib or that file, then run:\n")
	b.WriteString("--\n")
	b.WriteString("--     go generate ./internal/refgen\n\n\n")

	b.WriteString("type alias Entry =\n")
	b.WriteString("    { moduleName : String\n")
	b.WriteString("    , name : String\n")
	b.WriteString("    , signature : String\n")
	b.WriteString("    , side : String\n")
	b.WriteString("    , description : String\n")
	b.WriteString("    , examples : List (List String)\n")
	b.WriteString("    }\n\n\n")

	b.WriteString("type alias Group =\n")
	b.WriteString("    { title : String\n")
	b.WriteString("    , modules : List String\n")
	b.WriteString("    }\n\n\n")

	b.WriteString("type alias Section =\n")
	b.WriteString("    { category : String\n")
	b.WriteString("    , entries : List Entry\n")
	b.WriteString("    }\n\n\n")

	quoted := make([]string, len(Modules))
	for i, m := range Modules {
		quoted[i] = fmt.Sprintf("%q", m)
	}
	b.WriteString("modules : List String\n")
	b.WriteString("modules =\n")
	b.WriteString("    [ " + strings.Join(quoted, ", ") + " ]\n\n\n")

	b.WriteString("moduleGroups : List Group\n")
	b.WriteString("moduleGroups =\n")
	for i, g := range moduleGroups {
		lead := "    , "
		if i == 0 {
			lead = "    [ "
		}
		quotedMods := make([]string, len(g.Modules))
		for j, m := range g.Modules {
			quotedMods[j] = fmt.Sprintf("%q", m)
		}
		b.WriteString(fmt.Sprintf("%s{ title = %q, modules = [ %s ] }\n", lead, g.Title, strings.Join(quotedMods, ", ")))
	}
	b.WriteString("    ]\n\n\n")

	b.WriteString("sectionsFor : String -> List Section\n")
	b.WriteString("sectionsFor m =\n")
	b.WriteString("    case m of\n")
	for _, mod := range Modules {
		b.WriteString(fmt.Sprintf("        %q ->\n", mod))
		secs := SectionsOf(mod)
		if len(secs) == 0 {
			b.WriteString("            []\n\n")
			continue
		}
		for si, s := range secs {
			secLead := "            , "
			if si == 0 {
				secLead = "            [ "
			}
			b.WriteString(fmt.Sprintf("%s{ category = %q\n", secLead, s.Category))
			b.WriteString("              , entries =\n")
			for ei, e := range s.Entries {
				entLead := "                    , "
				if ei == 0 {
					entLead = "                    [ "
				}
				b.WriteString(fmt.Sprintf("%s{ moduleName = %q, name = %q, signature = %q, side = %q, description = %q, examples = %s }\n",
					entLead, e.Module, e.Name, e.Signature, e.Side, e.Desc, marNestedStringList(displayExamples(e.Examples))))
			}
			b.WriteString("                    ]\n")
			b.WriteString("              }\n")
		}
		b.WriteString("            ]\n\n")
	}
	b.WriteString("        _ ->\n")
	b.WriteString("            []\n\n\n")

	b.WriteString("blurbFor : String -> String\n")
	b.WriteString("blurbFor m =\n")
	b.WriteString("    case m of\n")
	for _, mod := range Modules {
		b.WriteString(fmt.Sprintf("        %q ->\n            %q\n\n", mod, blurbs[mod]))
	}
	b.WriteString("        _ ->\n")
	b.WriteString("            \"\"\n\n\n")

	b.WriteString("sideFor : String -> String\n")
	b.WriteString("sideFor m =\n")
	b.WriteString("    case m of\n")
	for _, mod := range Modules {
		b.WriteString(fmt.Sprintf("        %q ->\n            %q\n\n", mod, ModuleSideLabel(mod)))
	}
	b.WriteString("        _ ->\n")
	b.WriteString("            \"\"\n\n\n")

	b.WriteString("entriesFor : String -> List Entry\n")
	b.WriteString("entriesFor m =\n")
	b.WriteString("    List.concatMap (\\s -> s.entries) (sectionsFor m)\n")
	return b.String()
}
