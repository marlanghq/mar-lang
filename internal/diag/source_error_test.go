package diag

import (
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/typecheck"
)

// A type error that carries a source span (start + exclusive end)
// underlines the whole offending expression: endCol - startCol carets.
func TestFormat_UnderlinesSpan(t *testing.T) {
	src := "module M\nv = abcdef\n"
	inner := &typecheck.InferError{
		Pos:     ast.Pos{Line: 2, Column: 5}, // 'a'
		End:     ast.Pos{Line: 2, Column: 9}, // exclusive: covers cols 5-8 = "abcd"
		Message: "bad",
	}
	out := (&SourceError{Filename: "M.mar", Source: src, Inner: inner}).Format()
	if !strings.Contains(out, "^^^^") {
		t.Errorf("span [5,9) should render 4 carets; got:\n%s", out)
	}
	if strings.Contains(out, "^^^^^") {
		t.Errorf("span [5,9) should be exactly 4 carets, not 5+; got:\n%s", out)
	}
}

// Without an end position (lexer/parser errors, or a zero-width span)
// the renderer falls back to a single caret at the start column.
func TestFormat_SingleCaretWithoutSpan(t *testing.T) {
	inner := &typecheck.InferError{
		Pos:     ast.Pos{Line: 2, Column: 5},
		Message: "bad",
	}
	out := (&SourceError{Filename: "M.mar", Source: "module M\nv = abcdef\n", Inner: inner}).Format()
	if n := strings.Count(out, "^"); n != 1 {
		t.Errorf("no span should give exactly one caret, got %d; output:\n%s", n, out)
	}
}

// A span that runs onto later lines is clamped to the end of the start
// line (we underline only the snippet line we print).
func TestFormat_MultiLineSpanClampsToFirstLine(t *testing.T) {
	src := "module M\nv = abcdef\n  more\n"
	inner := &typecheck.InferError{
		Pos:     ast.Pos{Line: 2, Column: 5}, // 'a' on "v = abcdef" (10 cols)
		End:     ast.Pos{Line: 3, Column: 3}, // ends on the next line
		Message: "bad",
	}
	out := (&SourceError{Filename: "M.mar", Source: src, Inner: inner}).Format()
	// "v = abcdef" is 10 runes; from col 5 to end of line = 6 carets.
	if !strings.Contains(out, "^^^^^^") {
		t.Errorf("multi-line span should underline col 5 to end of line (6 carets); got:\n%s", out)
	}
}
