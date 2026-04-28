package lsp

import (
	"fmt"
	"strings"

	"mar/internal/ast"
	"mar/internal/parser"
	"mar/internal/typecheck"
)

// SymbolKind classifies a top-level definition for LSP.
type SymbolKind int

const (
	SymValue SymbolKind = iota
	SymTypeAlias
	SymCustomType
	SymConstructor
)

// Symbol describes one top-level definition we know how to navigate to,
// hover over, and list.
type Symbol struct {
	Name string
	Kind SymbolKind
	// Where the name was *defined* (1-indexed, mar-style).
	DefLine int
	DefCol  int
	// For SymValue / SymConstructor — the inferred type, pretty-printed.
	Type string
	// For SymTypeAlias / SymCustomType — a one-line summary of the type's
	// shape (rhs).
	Summary string
}

// DocIndex is the per-document analysis cached by the LSP server: the
// parsed module, its source, the type-check result, and a symbol table
// keyed by name.
type DocIndex struct {
	URI     string
	Source  string
	Mod     *ast.Module
	Result  *typecheck.CheckResult
	Symbols map[string]Symbol
}

// BuildIndex parses + type-checks `source` and returns a DocIndex. If
// parse / type-check fails we still return a partial index (with whatever
// declarations we managed to read), so the editor can at least navigate
// to symbols defined before the broken line.
func BuildIndex(uri, source string) *DocIndex {
	idx := &DocIndex{
		URI:     uri,
		Source:  source,
		Symbols: map[string]Symbol{},
	}
	mod, perr := parser.Parse(source)
	if perr != nil || mod == nil {
		return idx
	}
	idx.Mod = mod
	res, _ := typecheck.CheckModule(mod) // ignore err — we still want to index decls
	idx.Result = res
	collectSymbols(idx)
	return idx
}

func collectSymbols(idx *DocIndex) {
	if idx.Mod == nil {
		return
	}
	for _, decl := range idx.Mod.Decls {
		switch d := decl.(type) {
		case *ast.ValueDecl:
			s := Symbol{
				Name:    d.Name,
				Kind:    SymValue,
				DefLine: d.Pos.Line,
				DefCol:  d.Pos.Column,
			}
			if idx.Result != nil {
				if t, ok := idx.Result.ValueTypes[d.Name]; ok {
					s.Type = typecheck.Pretty(t)
				}
			}
			idx.Symbols[d.Name] = s
		case *ast.TypeAliasDecl:
			s := Symbol{
				Name:    d.Name,
				Kind:    SymTypeAlias,
				DefLine: d.Pos.Line,
				DefCol:  d.Pos.Column,
				Summary: aliasSummary(d),
			}
			idx.Symbols[d.Name] = s
		case *ast.CustomTypeDecl:
			s := Symbol{
				Name:    d.Name,
				Kind:    SymCustomType,
				DefLine: d.Pos.Line,
				DefCol:  d.Pos.Column,
				Summary: customSummary(d),
			}
			idx.Symbols[d.Name] = s
			// Each constructor is also a symbol so users can hover /
			// jump to it from a usage site.
			for _, ctor := range d.Constructors {
				cs := Symbol{
					Name:    ctor.Name,
					Kind:    SymConstructor,
					DefLine: ctor.Pos.Line,
					DefCol:  ctor.Pos.Column,
				}
				if idx.Result != nil {
					if t, ok := idx.Result.ValueTypes[ctor.Name]; ok {
						cs.Type = typecheck.Pretty(t)
					}
				}
				idx.Symbols[ctor.Name] = cs
			}
		}
	}
}

func aliasSummary(d *ast.TypeAliasDecl) string {
	var sb strings.Builder
	sb.WriteString("type alias ")
	sb.WriteString(d.Name)
	for _, p := range d.Params {
		sb.WriteByte(' ')
		sb.WriteString(p)
	}
	return sb.String()
}

func customSummary(d *ast.CustomTypeDecl) string {
	var sb strings.Builder
	sb.WriteString("type ")
	sb.WriteString(d.Name)
	for _, p := range d.Params {
		sb.WriteByte(' ')
		sb.WriteString(p)
	}
	sb.WriteString(" = ")
	for i, c := range d.Constructors {
		if i > 0 {
			sb.WriteString(" | ")
		}
		sb.WriteString(c.Name)
		for range c.Args {
			sb.WriteString(" _")
		}
	}
	return sb.String()
}

// IdentifierAt returns the identifier (alphanumeric + underscore) at
// the given 0-indexed line/column in source. Empty string if none.
// Used by hover / go-to-definition handlers to figure out what the
// cursor is sitting on.
func IdentifierAt(source string, line, col int) string {
	lines := strings.Split(source, "\n")
	if line < 0 || line >= len(lines) {
		return ""
	}
	row := lines[line]
	if col < 0 || col > len(row) {
		return ""
	}
	isIdent := func(b byte) bool {
		return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
			(b >= '0' && b <= '9') || b == '_' || b == '.'
	}
	// Walk left and right from col to find token bounds.
	start := col
	for start > 0 && isIdent(row[start-1]) {
		start--
	}
	end := col
	for end < len(row) && isIdent(row[end]) {
		end++
	}
	if start == end {
		return ""
	}
	return row[start:end]
}

// HoverMarkdown produces the hover content for a symbol — Markdown with
// a code block containing the type signature.
func HoverMarkdown(s Symbol) string {
	switch s.Kind {
	case SymValue:
		if s.Type != "" {
			return fmt.Sprintf("```mar\n%s : %s\n```", s.Name, s.Type)
		}
		return fmt.Sprintf("```mar\n%s\n```", s.Name)
	case SymConstructor:
		if s.Type != "" {
			return fmt.Sprintf("```mar\n%s : %s\n```\n_constructor_", s.Name, s.Type)
		}
		return fmt.Sprintf("```mar\n%s\n```\n_constructor_", s.Name)
	case SymTypeAlias:
		return fmt.Sprintf("```mar\n%s\n```", s.Summary)
	case SymCustomType:
		return fmt.Sprintf("```mar\n%s\n```", s.Summary)
	}
	return s.Name
}
