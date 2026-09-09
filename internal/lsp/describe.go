package lsp

import (
	"encoding/json"
	"sort"
	"strings"
)

// `mar/describe` answers "what is this name, exactly" with the reference's own
// prose and its worked examples.
//
// It exists for a reader the LSP did not have before: a language model writing
// Mar inside the editor. Mar is in no training data, so a model working here is
// guessing at a stdlib it has never seen, and it guesses from the languages it
// HAS seen -- List.foldr with Haskell's argument order, a Basics.not that does
// not compile, a Float that does not exist. Hover already answers this, but
// hover needs a cursor sitting on the name; a model has a name and no cursor.
//
// What comes back is not documentation in the usual sense. Every example here
// is evaluated by a test that requires it to be True, so a model asking this
// question receives a checked fact rather than a plausible sentence. That is
// the reason to expose it at all.
type describeParams struct {
	// Name is a qualified name (`Decimal.toCents`), a bare one (`not`), or a
	// prefix. A prefix answers with the matches rather than nothing, because
	// the caller that most needs this is the one that does not yet know the
	// spelling.
	Name string `json:"name"`
	// Limit caps a prefix answer. Zero means the default.
	Limit int `json:"limit"`
}

type describedSymbol struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Type     string   `json:"type,omitempty"`
	Doc      string   `json:"doc,omitempty"`
	Examples []string `json:"examples,omitempty"`
}

type describeResult struct {
	// Exact is the symbol whose name matched exactly, when one did.
	Exact *describedSymbol `json:"exact,omitempty"`
	// Matches are the other names that start with what was asked, so a
	// half-remembered name leads somewhere instead of nowhere.
	Matches []describedSymbol `json:"matches,omitempty"`
	// Total is how many matched before Limit cut the list, so the caller can
	// tell "these are all of them" from "these are the first few".
	Total int `json:"total"`
}

const describeDefaultLimit = 25

func (s *Server) handleDescribe(msg *message) {
	var p describeParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		s.respond(msg.ID, describeResult{})
		return
	}
	s.respond(msg.ID, Describe(p.Name, p.Limit))
}

// Describe is the answer itself, separated from the protocol so it can be
// tested without a server. It reads the same table hover reads.
func Describe(name string, limit int) describeResult {
	if limit <= 0 {
		limit = describeDefaultLimit
	}
	out := describeResult{}
	if sym, ok := LookupStdlib(name); ok {
		d := describedSymbol{
			Name:     sym.Name,
			Kind:     kindName(sym.Kind),
			Type:     sym.Type,
			Doc:      sym.Doc,
			Examples: sym.Examples,
		}
		// A custom type carries its shape where a value carries its type.
		if d.Type == "" && sym.Summary != stdlibBuiltinTag {
			d.Type = sym.Summary
		}
		out.Exact = &d
	}
	if name == "" {
		return out
	}
	lower := strings.ToLower(name)
	var matches []describedSymbol
	for _, sym := range StdlibSymbols() {
		if sym.Name == name || !strings.Contains(strings.ToLower(sym.Name), lower) {
			continue
		}
		matches = append(matches, describedSymbol{
			Name:     sym.Name,
			Kind:     kindName(sym.Kind),
			Type:     sym.Type,
			Doc:      sym.Doc,
			Examples: sym.Examples,
		})
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Name < matches[j].Name })
	out.Total = len(matches)
	if len(matches) > limit {
		matches = matches[:limit]
	}
	out.Matches = matches
	return out
}

func kindName(k SymbolKind) string {
	switch k {
	case SymValue:
		return "value"
	case SymConstructor:
		return "constructor"
	case SymCustomType:
		return "type"
	case SymTypeAlias:
		return "alias"
	}
	return "symbol"
}
