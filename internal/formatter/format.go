// Package formatter reformats mar source code to a canonical style.
//
// Today this is intentionally conservative: it normalizes whitespace
// (tabs to spaces, trailing whitespace removed, single trailing
// newline) without reorganizing structure. A full AST-based pretty
// printer that re-flows long lines, sorts imports, and aligns type
// signatures is future work — it'll need comment preservation, which
// the lexer currently strips as trivia.
package formatter

import (
	"strings"
)

// Format takes mar source and returns the formatted version. Safe to
// run on any input — if something is unparseable, the formatter
// preserves it byte-for-byte (only whitespace is touched).
func Format(src string) string {
	// Normalize line endings to \n. Windows / Mac inconsistencies do
	// happen in checked-in code.
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")

	lines := strings.Split(src, "\n")
	for i, line := range lines {
		// Tabs -> 4 spaces. Mar's parser already accepts both, but
		// mixing causes surprises with the layout-sensitive parser.
		line = strings.ReplaceAll(line, "\t", "    ")
		// Strip trailing whitespace.
		line = strings.TrimRight(line, " \t")
		lines[i] = line
	}

	// Collapse runs of more than two blank lines into exactly two.
	// (Top-level decls are separated by two blank lines by convention.)
	out := make([]string, 0, len(lines))
	blanks := 0
	for _, line := range lines {
		if line == "" {
			blanks++
			if blanks > 2 {
				continue
			}
		} else {
			blanks = 0
		}
		out = append(out, line)
	}

	// Trim leading blank lines, ensure exactly one trailing newline.
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}
