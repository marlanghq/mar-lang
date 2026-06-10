// Package diag provides user-facing error formatting with source context.
//
// The compiler's error types (lexer.Error, parser.Error, typecheck.InferError)
// each carry a position (line, column). At the boundary where errors meet
// the user — the CLI today, an LSP server later — we want to render those
// positions as a code snippet with a caret pointing at the bad token,
// Rust-style.
//
// Visual conventions follow docs/cli-style.md §1 (spacing) and §3 (color):
//
//   - Leading blank line so the block separates from preceding output
//     (`fprintError`'s leading-blank rule, applied to source errors too).
//   - The "Lex error:" / "Parse error:" / "Type error:" prefix is bold
//     red, matching the role color for "Error:".
//   - The file path is bold magenta (cli-style.md §3 "paths and config keys").
//   - The line gutter and pipes are dim.
//   - The caret is bold red so the eye lands on it.
//
// Colors auto-disable when stderr isn't a TTY and when NO_COLOR is set,
// matching the rules in internal/jsserve/banner.go and cmd/mar/color.go.
package diag

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"mar/internal/lexer"
	"mar/internal/parser"
	"mar/internal/typecheck"
)

// SourceError pairs a positioned error with the file it came from so we
// can render code context when the user sees it.
type SourceError struct {
	Filename string
	Source   string
	Inner    error
}

// Wrap returns a SourceError if inner has a known position type
// (lexer/parser/typecheck), otherwise returns inner unchanged.
func Wrap(filename, source string, inner error) error {
	if inner == nil {
		return nil
	}
	if _, _, ok := positionOf(inner); !ok {
		return inner
	}
	return &SourceError{Filename: filename, Source: source, Inner: inner}
}

// Error returns a one-line summary (no context) for the standard error
// interface. Use Format for the full multi-line render.
func (e *SourceError) Error() string {
	return fmt.Sprintf("%s: %v", e.Filename, e.Inner)
}

// Unwrap exposes the underlying typed error so callers can inspect it.
func (e *SourceError) Unwrap() error { return e.Inner }

// Format renders the error with a code snippet and a caret at the
// offending column. Falls back to Error() when the line is out of range
// or the source is empty.
func (e *SourceError) Format() string {
	line, col, ok := positionOf(e.Inner)
	if !ok {
		return e.Error()
	}
	endLine, endCol, hasEnd := endOf(e.Inner)
	return formatContext(e.Filename, e.Source, line, col, endLine, endCol, hasEnd, kindOf(e.Inner), bodyOf(e.Inner))
}

// Format formats any error: if it's a *SourceError, use its Format;
// otherwise stringify normally. Convenience for the CLI.
func Format(err error) string {
	if err == nil {
		return ""
	}
	if se, ok := err.(*SourceError); ok {
		return se.Format()
	}
	return err.Error()
}

// positionOf extracts (line, col) from a known positioned error.
func positionOf(err error) (line, col int, ok bool) {
	switch e := err.(type) {
	case *lexer.Error:
		return e.Line, e.Column, true
	case *parser.Error:
		return e.Line, e.Column, true
	case *typecheck.InferError:
		return e.Pos.Line, e.Pos.Column, true
	case *typecheck.ShapeIssue:
		return e.Pos.Line, e.Pos.Column, true
	}
	return 0, 0, false
}

// endOf extracts the exclusive end position of an error's source span,
// when the error carries one (only typecheck.InferError does today).
// The renderer underlines [start, end); without an end it falls back to
// a single caret. Returns ok=false when there's no span or it's empty.
func endOf(err error) (line, col int, ok bool) {
	if e, isInfer := err.(*typecheck.InferError); isInfer {
		if e.End != (e.Pos) && e.End.Line != 0 {
			return e.End.Line, e.End.Column, true
		}
	}
	return 0, 0, false
}

// kindOf returns the role label ("Lex error" / "Parse error" / "Type
// error" / "Shape error") for the prefix at the start of the rendered
// block. Kept separate from the body so the renderer can color it
// independently.
func kindOf(err error) string {
	switch err.(type) {
	case *lexer.Error:
		return "Lex error"
	case *parser.Error:
		return "Parse error"
	case *typecheck.InferError:
		return "Type error"
	case *typecheck.ShapeIssue:
		return "Shape error"
	}
	return "Error"
}

// bodyOf returns the "what went wrong" half of a positioned error
// (without the kind prefix or the position — those get rendered
// separately by formatContext).
func bodyOf(err error) string {
	switch e := err.(type) {
	case *lexer.Error:
		return e.Message
	case *parser.Error:
		return e.Message
	case *typecheck.InferError:
		return e.Message
	case *typecheck.ShapeIssue:
		return e.Message
	}
	return err.Error()
}

// formatContext returns a multi-line error display following the
// cli-style.md §1 spacing rules and §3 color rules:
//
//	<blank>
//	Type error: <message>
//	  --> <filename>:<line>:<col>
//	   |
//	97 |     n.author
//	   |       ^
//
// Leading blank separates the block from preceding output. "Type
// error:" is bold red (same role as "Error:"). The file path is bold
// magenta. The gutter / pipes are dim. The caret is bold red.
func formatContext(filename, source string, line, col, endLine, endCol int, hasEnd bool, kind, msg string) string {
	c := colors()
	lines := strings.Split(source, "\n")
	if line < 1 || line > len(lines) {
		// Out-of-range line — drop the snippet but keep the styled
		// prefix + path so the block still reads as a mar error.
		return fmt.Sprintf("\n%s %s\n  --> %s:%d:%d\n",
			c.boldRed(kind+":"),
			msg,
			c.boldMagenta(filename),
			line, col,
		)
	}
	var sb strings.Builder
	// Leading blank — separates the block from preceding stderr output
	// (boot logs, file-watcher noise on recompile, etc.).
	sb.WriteString("\n")
	sb.WriteString(c.boldRed(kind + ":"))
	sb.WriteByte(' ')
	sb.WriteString(msg)
	sb.WriteString("\n  --> ")
	sb.WriteString(c.boldMagenta(filename))
	fmt.Fprintf(&sb, ":%d:%d\n", line, col)
	gutterW := len(fmt.Sprintf("%d", line))
	if gutterW < 2 {
		gutterW = 2
	}
	emptyGutter := strings.Repeat(" ", gutterW)
	pipe := c.dim("|")
	sb.WriteString(emptyGutter)
	sb.WriteByte(' ')
	sb.WriteString(pipe)
	sb.WriteByte('\n')
	sb.WriteString(c.dim(fmt.Sprintf("%*d", gutterW, line)))
	sb.WriteByte(' ')
	sb.WriteString(pipe)
	sb.WriteByte(' ')
	sb.WriteString(lines[line-1])
	sb.WriteByte('\n')
	sb.WriteString(emptyGutter)
	sb.WriteByte(' ')
	sb.WriteString(pipe)
	sb.WriteByte(' ')
	// Indent caret to match the column. Tabs in the source are preserved
	// so a tab in the snippet maps to a tab in the caret line, keeping
	// alignment when the terminal renders the tab.
	srcRunes := []rune(lines[line-1])
	if col >= 1 {
		for i := 0; i < col-1 && i < len(srcRunes); i++ {
			if srcRunes[i] == '\t' {
				sb.WriteRune('\t')
			} else {
				sb.WriteRune(' ')
			}
		}
	}
	// Underline the whole [start, end) span when an end is known
	// (endCol is exclusive). A same-line span gives endCol-col carets;
	// a span that runs onto later lines is clamped to the end of this
	// line. No end, or a zero-width span, falls back to a single caret.
	caretN := 1
	if hasEnd {
		switch {
		case endLine == line && endCol > col:
			caretN = endCol - col
		case endLine > line:
			caretN = len(srcRunes) - (col - 1)
		}
		if caretN < 1 {
			caretN = 1
		}
	}
	sb.WriteString(c.boldRed(strings.Repeat("^", caretN)))
	sb.WriteByte('\n')
	return sb.String()
}

// colorSet bundles the helpers formatContext uses. Either real ANSI
// wrappers (TTY + colors enabled) or identity functions (piped output,
// NO_COLOR set). Resolved once per process via colorsOnce.
type colorSet struct {
	boldRed     func(string) string
	boldMagenta func(string) string
	dim         func(string) string
}

var (
	colorsOnce sync.Once
	colorsVal  colorSet
)

func colors() colorSet {
	colorsOnce.Do(func() {
		if !stderrTTY() {
			id := func(s string) string { return s }
			colorsVal = colorSet{boldRed: id, boldMagenta: id, dim: id}
			return
		}
		// Same ANSI codes as cmd/mar/color.go — keep them in sync so
		// the snippet renders the same shade as `Error:` / `Hint:`
		// blocks from fprintError/fprintHint.
		colorsVal = colorSet{
			boldRed:     wrapANSI("\x1b[1;31m"),
			boldMagenta: wrapANSI("\x1b[1;35m"),
			// 256-color medium gray (xterm 245). Matches the dim
			// used in cmd/mar/color.go and internal/jsserve/banner.go.
			dim: wrapANSI("\x1b[38;5;245m"),
		}
	})
	return colorsVal
}

func wrapANSI(prefix string) func(string) string {
	return func(s string) string { return prefix + s + "\x1b[0m" }
}

// stderrTTY reports whether stderr is an interactive terminal AND the
// user hasn't opted out via NO_COLOR. Errors print to stderr, so the
// decision is on stderr (not stdout — those can have different
// redirections, e.g. `mar dev > out.log` leaves stderr a TTY).
func stderrTTY() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
