// Package gosrc reads this repository's own Go source.
//
// Several tests here are censuses: they read the tree to count what it holds
// -- the boot passes, the AST nodes each runtime covers, the fields a config
// struct declares -- because a number that only moves when somebody changes
// the code is worth more than a memory of what the code contains.
//
// Every one of them opened a directory the same way, and every one used
// go/parser.ParseDir, which Go 1.25 deprecated. Five copies of one six-line
// loop is the shape this repository keeps closing elsewhere, so it is closed
// here too rather than migrated five times.
package gosrc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// ParseDir returns the parsed .go files of one directory, not recursing.
//
// It answers with FILES rather than with go/ast.Package, which is deprecated
// too and which every caller here immediately flattened anyway. A file knows
// its own package (`f.Name.Name`), so the callers that care -- the boot census
// skips the `_test` package -- still can.
func ParseDir(fset *token.FileSet, dir string, includeTests bool) ([]*ast.File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []*ast.File
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		if !includeTests && strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}
