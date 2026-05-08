package lsp

import (
	"sort"
	"sync"

	"mar/internal/typecheck"
)

// Stdlib symbols are the framework-provided bindings (UI.button,
// Auth.config, Service.declare, etc.) sourced from typecheck.BaseEnv.
// They surface in completion, hover, and workspace-symbol so the
// editor knows about the language's built-ins in addition to the
// user's own declarations.
//
// They never appear in document indexes — go-to-definition has nowhere
// to navigate, since the implementation lives in Go. That's why hover
// renders them with a "_built-in_" footer and definition handlers
// return null for them.

var (
	stdlibOnce  sync.Once
	stdlibCache []Symbol
	stdlibIndex map[string]Symbol
)

// StdlibSymbols returns every framework-provided symbol the editor
// can act on, sorted alphabetically. Computed once on first call and
// cached for the lifetime of the LSP process.
func StdlibSymbols() []Symbol {
	stdlibOnce.Do(buildStdlib)
	return stdlibCache
}

// LookupStdlib returns the stdlib symbol matching `name` (qualified
// or bare custom-type), or false if absent.
func LookupStdlib(name string) (Symbol, bool) {
	stdlibOnce.Do(buildStdlib)
	s, ok := stdlibIndex[name]
	return s, ok
}

func buildStdlib() {
	bindings := typecheck.BaseQualifiedSymbols()
	customs := typecheck.BaseCustomTypes()

	// Allocate exactly: one Symbol per qualified value + one per
	// custom type + one per its constructor.
	estimate := len(bindings) + len(customs)*4
	stdlibCache = make([]Symbol, 0, estimate)
	stdlibIndex = make(map[string]Symbol, estimate)

	for name, t := range bindings {
		s := Symbol{
			Name:    name,
			Kind:    SymValue,
			Type:    typecheck.Pretty(t),
			Summary: stdlibBuiltinTag,
		}
		stdlibCache = append(stdlibCache, s)
		stdlibIndex[name] = s
	}
	for name, ct := range customs {
		s := Symbol{
			Name:    name,
			Kind:    SymCustomType,
			Summary: customSummaryFromBase(ct),
		}
		stdlibCache = append(stdlibCache, s)
		stdlibIndex[name] = s
		for _, ctorName := range ct.CtorOrder {
			cs := Symbol{
				Name:    ctorName,
				Kind:    SymConstructor,
				Type:    constructorTypeFromCustom(name, ct, ctorName),
				Summary: stdlibBuiltinTag,
			}
			stdlibCache = append(stdlibCache, cs)
			// First-write wins — value-env entries (Just/Nothing/Ok/Err)
			// in BaseQualifiedSymbols may have already inserted a
			// constructor symbol; don't shadow it with the customs
			// reconstruction.
			if _, exists := stdlibIndex[ctorName]; !exists {
				stdlibIndex[ctorName] = cs
			}
		}
	}
	sort.Slice(stdlibCache, func(i, j int) bool {
		return stdlibCache[i].Name < stdlibCache[j].Name
	})
}

// stdlibBuiltinTag flags Symbol.Summary so HoverMarkdown can render a
// "_built-in_" footer instead of a regular summary line.
const stdlibBuiltinTag = "__mar_stdlib__"

// customSummaryFromBase reconstructs a one-line summary mirroring
// `customSummary` for user-defined types — `type Maybe a = Just _ | Nothing`.
func customSummaryFromBase(ct typecheck.CustomType) string {
	out := "type " + ct.Name
	for _, p := range ct.Params {
		out += " " + p
	}
	out += " = "
	for i, name := range ct.CtorOrder {
		if i > 0 {
			out += " | "
		}
		out += name
		if c, ok := ct.Constructors[name]; ok {
			for range c.Args {
				out += " _"
			}
		}
	}
	return out
}

// constructorTypeFromCustom synthesizes a printable type for a
// stdlib constructor — e.g. `Just : a -> Maybe a`. Falls back to a
// bare name when the variant carries no args.
func constructorTypeFromCustom(typeName string, ct typecheck.CustomType, ctorName string) string {
	c, ok := ct.Constructors[ctorName]
	if !ok {
		return ""
	}
	if len(c.Args) == 0 {
		return typecheck.Pretty(ct.Constructors[ctorName].Result)
	}
	t := typecheck.Pretty(c.Result)
	for i := len(c.Args) - 1; i >= 0; i-- {
		t = typecheck.Pretty(c.Args[i]) + " -> " + t
	}
	return t
}
