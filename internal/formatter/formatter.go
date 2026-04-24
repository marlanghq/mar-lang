package formatter

import (
	"mar/internal/parser"
	"mar/internal/sexp"
)

// Format rewrites Mar source into a canonical s-expression layout.
func Format(source string) (string, error) {
	nodes, err := sexp.Parse(source)
	if err != nil {
		return "", err
	}
	formatted := sexp.Format(nodes)
	if _, err := parser.Parse(formatted); err != nil {
		return "", err
	}
	return formatted, nil
}
