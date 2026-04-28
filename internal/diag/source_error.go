// Package diag provides user-facing error formatting with source context.
//
// The compiler's error types (lexer.Error, parser.Error, typecheck.InferError)
// each carry a position (line, column). At the boundary where errors meet
// the user — the CLI today, an LSP server later — we want to render those
// positions as a code snippet with a caret pointing at the bad token,
// Rust-style.
package diag

import (
	"fmt"
	"strings"

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
	return formatContext(e.Filename, e.Source, line, col, baseMessage(e.Inner))
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
	}
	return 0, 0, false
}

// baseMessage extracts the "what went wrong" part of a positioned error,
// stripping the leading "kind error at L:C:" prefix the type's Error
// method adds. Lets us format the position ourselves below the snippet.
func baseMessage(err error) string {
	switch e := err.(type) {
	case *lexer.Error:
		return "lex error: " + e.Message
	case *parser.Error:
		return "parse error: " + e.Message
	case *typecheck.InferError:
		return "type error: " + e.Message
	}
	return err.Error()
}

// formatContext returns a multi-line error display:
//
//	error: <message>
//	  --> <filename>:<line>:<col>
//	   |
//	 5 |     n.author
//	   |       ^
func formatContext(filename, source string, line, col int, msg string) string {
	lines := strings.Split(source, "\n")
	if line < 1 || line > len(lines) {
		return fmt.Sprintf("%s:%d:%d: %s", filename, line, col, msg)
	}
	var sb strings.Builder
	sb.WriteString(msg)
	sb.WriteString("\n  --> ")
	sb.WriteString(filename)
	sb.WriteString(fmt.Sprintf(":%d:%d\n", line, col))
	gutterW := len(fmt.Sprintf("%d", line))
	if gutterW < 2 {
		gutterW = 2
	}
	gutter := strings.Repeat(" ", gutterW)
	sb.WriteString(gutter)
	sb.WriteString(" |\n")
	sb.WriteString(fmt.Sprintf("%*d | %s\n", gutterW, line, lines[line-1]))
	sb.WriteString(gutter)
	sb.WriteString(" | ")
	// Indent caret to match the column. Tabs in the source are preserved
	// so a tab in the snippet maps to a tab in the caret line, keeping
	// alignment when the terminal renders the tab.
	if col >= 1 {
		runes := []rune(lines[line-1])
		for i := 0; i < col-1 && i < len(runes); i++ {
			if runes[i] == '\t' {
				sb.WriteRune('\t')
			} else {
				sb.WriteRune(' ')
			}
		}
	}
	sb.WriteString("^\n")
	return sb.String()
}
