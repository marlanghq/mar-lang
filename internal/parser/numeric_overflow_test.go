package parser

import (
	"mar/internal/ast"
	"strings"
	"testing"
)

// An integer literal wider than int64 must be a clear, positioned error rather
// than a silently-truncated wrong value.
func TestIntLiteralOutOfRangeErrors(t *testing.T) {
	// 20 nines > max int64 (9223372036854775807).
	err := mustParseErr(t, "module M exposing (..)\nx = 99999999999999999999\n")
	if !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("int literal: got %v, want 'out of range'", err)
	}
}

// Same rule on the pattern side (the other affected parse path).
func TestIntPatternOutOfRangeErrors(t *testing.T) {
	src := `module M exposing (..)
f x =
    case x of
        99999999999999999999 -> 1
        _ -> 0
`
	err := mustParseErr(t, src)
	if !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("int pattern: got %v, want 'out of range'", err)
	}
}

// Mar has no Float: any decimal literal is a clear parse error, not a
// silently-accepted value. (The lexer still tokenizes the float so the
// message can name it precisely; the parser is what rejects it.)
func TestDecimalLiteralParses(t *testing.T) {
	mod := mustParse(t, "module M exposing (..)\nx = 3.14\ny = 0.05\nz = 12.50\nw = 7 // 2\n")
	// x = 3.14 -> coef "314", scale 2
	decls := map[string]*ast.EDecimal{}
	var slashSlash bool
	for _, d := range mod.Decls {
		vd, ok := d.(*ast.ValueDecl)
		if !ok {
			continue
		}
		switch e := vd.Body.(type) {
		case *ast.EDecimal:
			decls[vd.Name] = e
		case *ast.EBinop:
			if vd.Name == "w" && e.Op == "//" {
				slashSlash = true
			}
		}
	}
	want := map[string]struct {
		coef  string
		scale int
	}{
		"x": {"314", 2},
		"y": {"5", 2},
		"z": {"1250", 2},
	}
	for name, w := range want {
		got, ok := decls[name]
		if !ok {
			t.Fatalf("%s: expected an EDecimal body", name)
		}
		if got.Coef != w.coef || got.Scale != w.scale {
			t.Errorf("%s: got coef=%q scale=%d, want coef=%q scale=%d", name, got.Coef, got.Scale, w.coef, w.scale)
		}
	}
	if !slashSlash {
		t.Errorf("w = 7 // 2: expected a %q binop body", "//")
	}
}

func TestDecimalLiteralTooWide(t *testing.T) {
	err := mustParseErr(t, "module M exposing (..)\nx = 12345678901234567890123456789012345.0\n")
	if !strings.Contains(err.Error(), "34 significant digits") {
		t.Fatalf("oversized decimal: got %v, want 34-digit message", err)
	}
}
