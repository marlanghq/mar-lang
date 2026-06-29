package lexer

import (
	"strings"
	"testing"
)

// helper: lex and return only kinds (drop EOF)
func kinds(t *testing.T, src string) []Kind {
	t.Helper()
	toks, err := Lex(src)
	if err != nil {
		t.Fatalf("lex error: %v", err)
	}
	out := make([]Kind, 0, len(toks))
	for _, tk := range toks {
		if tk.Kind == KindEOF {
			break
		}
		out = append(out, tk.Kind)
	}
	return out
}

func eqKinds(t *testing.T, src string, want ...Kind) {
	t.Helper()
	got := kinds(t, src)
	if len(got) != len(want) {
		t.Fatalf("kind count mismatch for %q: got %v, want %v", src, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("kind[%d] mismatch for %q: got %v, want %v", i, src, got, want)
		}
	}
}

func TestEmpty(t *testing.T) {
	toks, err := Lex("")
	if err != nil {
		t.Fatalf("lex error: %v", err)
	}
	if len(toks) != 1 || toks[0].Kind != KindEOF {
		t.Fatalf("expected only EOF, got %v", toks)
	}
}

func TestWhitespaceOnly(t *testing.T) {
	toks, err := Lex("   \n  \t  \n")
	if err != nil {
		t.Fatalf("lex error: %v", err)
	}
	if len(toks) != 1 || toks[0].Kind != KindEOF {
		t.Fatalf("expected only EOF, got %v", toks)
	}
}

func TestComments(t *testing.T) {
	src := `
-- this is a line comment
foo  -- trailing
{- block
   comment {- nested -} still in block
-}
bar
`
	eqKinds(t, src, KindLowerName, KindLowerName)
}

func TestIntegers(t *testing.T) {
	eqKinds(t, "42 0 1234567890", KindInt, KindInt, KindInt)
}

func TestFloats(t *testing.T) {
	toks, err := Lex("3.14 0.5 100.0")
	if err != nil {
		t.Fatalf("lex error: %v", err)
	}
	if len(toks) != 4 || toks[3].Kind != KindEOF {
		t.Fatalf("expected 3 floats + EOF, got %v", toks)
	}
	for i, want := range []string{"3.14", "0.5", "100.0"} {
		if toks[i].Kind != KindFloat || toks[i].Value != want {
			t.Fatalf("token %d: want float %q, got %v", i, want, toks[i])
		}
	}
}

func TestStrings(t *testing.T) {
	src := `"hello" "with \"quotes\"" "tabs\there"`
	toks, err := Lex(src)
	if err != nil {
		t.Fatalf("lex error: %v", err)
	}
	want := []string{"hello", "with \"quotes\"", "tabs\there"}
	for i, w := range want {
		if toks[i].Kind != KindString || toks[i].Value != w {
			t.Fatalf("token %d: want %q, got %v", i, w, toks[i])
		}
	}
}

func TestStringUnterminated(t *testing.T) {
	_, err := Lex(`"hello`)
	if err == nil {
		t.Fatal("expected error for unterminated string")
	}
	if !strings.Contains(err.Error(), "unterminated") {
		t.Fatalf("expected unterminated error, got: %v", err)
	}
}

func TestChars(t *testing.T) {
	cases := map[string]string{
		`'a'`:         "a",
		`'\n'`:        "\n",
		`'\t'`:        "\t",
		`'\\'`:        "\\",
		`'\''`:        "'",
		`'\"'`:        "\"",
		`'\u{41}'`:    "A",
		`'\u{1F600}'`: "😀",
		`'é'`:         "é",
	}
	for src, want := range cases {
		toks, err := Lex(src)
		if err != nil {
			t.Fatalf("lex error for %q: %v", src, err)
		}
		if toks[0].Kind != KindChar {
			t.Fatalf("%q: got kind %v, want char", src, toks[0].Kind)
		}
		if toks[0].Value != want {
			t.Fatalf("%q: got %q, want %q", src, toks[0].Value, want)
		}
	}
}

func TestCharErrors(t *testing.T) {
	bad := []string{
		`''`,           // empty
		`'ab'`,         // two chars
		`'\q'`,         // unknown escape
		`'\u{}'`,       // empty hex
		`'\u{D800}'`,   // surrogate
		`'\u{110000}'`, // out of range
		`'`,            // unterminated
	}
	for _, src := range bad {
		_, err := Lex(src)
		if err == nil {
			t.Fatalf("expected error for %q, got none", src)
		}
	}
}

func TestIdentifiers(t *testing.T) {
	src := "foo Foo foo_bar foo' Foo123"
	toks, err := Lex(src)
	if err != nil {
		t.Fatalf("lex error: %v", err)
	}
	wantKinds := []Kind{KindLowerName, KindUpperName, KindLowerName, KindLowerName, KindUpperName}
	wantValues := []string{"foo", "Foo", "foo_bar", "foo'", "Foo123"}
	for i, k := range wantKinds {
		if toks[i].Kind != k || toks[i].Value != wantValues[i] {
			t.Fatalf("token %d: want %v(%q), got %v", i, k, wantValues[i], toks[i])
		}
	}
}

func TestKeywords(t *testing.T) {
	src := "module exposing import as type alias if then else case of let in where port"
	want := []Kind{
		KindModule, KindExposing, KindImport, KindAs, KindType, KindAlias,
		KindIf, KindThen, KindElse, KindCase, KindOf, KindLet, KindIn, KindWhere, KindPort,
	}
	eqKinds(t, src, want...)
}

func TestFieldAccessor(t *testing.T) {
	// `.foo` after whitespace = field accessor function (FieldDot token).
	toks, err := Lex(".foo .bar123")
	if err != nil {
		t.Fatalf("lex error: %v", err)
	}
	if toks[0].Kind != KindFieldDot || toks[0].Value != "foo" {
		t.Fatalf("want .foo as FieldDot, got %v", toks[0])
	}
	if toks[1].Kind != KindFieldDot || toks[1].Value != "bar123" {
		t.Fatalf("want .bar123 as FieldDot, got %v", toks[1])
	}
}

func TestDotFieldAccessNoWhitespace(t *testing.T) {
	// `r.x` (no whitespace) = LowerName + Dot + LowerName.
	src := "r.x"
	want := []Kind{KindLowerName, KindDot, KindLowerName}
	eqKinds(t, src, want...)
}

func TestUnderscore(t *testing.T) {
	eqKinds(t, "_", KindUnderscore)
	// _foo should be a lowerName, not underscore
	eqKinds(t, "_foo", KindLowerName)
}

func TestOperators(t *testing.T) {
	src := "= -> |> <| | : :: <- == /= < > <= >= && || + - * / ++ ."
	want := []Kind{
		KindEquals, KindArrow, KindPipeRight, KindPipeLeft, KindPipe,
		KindColon, KindDoubleCol, KindBindArrow,
		KindEqualsEq, KindNotEq, KindLT, KindGT, KindLTE, KindGTE,
		KindAnd, KindOr,
		KindPlus, KindMinus, KindStar, KindSlash, KindAppend, KindDot,
	}
	eqKinds(t, src, want...)
}

func TestPunctuation(t *testing.T) {
	src := "( ) [ ] { } , ; \\"
	want := []Kind{
		KindLParen, KindRParen, KindLBracket, KindRBracket,
		KindLBrace, KindRBrace, KindComma, KindSemicolon, KindBackslash,
	}
	eqKinds(t, src, want...)
}

func TestPositions(t *testing.T) {
	src := "foo\n  bar"
	toks, err := Lex(src)
	if err != nil {
		t.Fatalf("lex error: %v", err)
	}
	if toks[0].Line != 1 || toks[0].Column != 1 {
		t.Fatalf("foo position: want 1:1, got %d:%d", toks[0].Line, toks[0].Column)
	}
	if toks[1].Line != 2 || toks[1].Column != 3 {
		t.Fatalf("bar position: want 2:3, got %d:%d", toks[1].Line, toks[1].Column)
	}
}

func TestSimpleModule(t *testing.T) {
	src := `module Foo exposing (..)

import Bar

type alias Person =
    { name : String
    , age : Int
    }

greet : Person -> String
greet person =
    "Hello, " ++ person.name
`
	toks, err := Lex(src)
	if err != nil {
		t.Fatalf("lex error: %v", err)
	}
	if len(toks) == 0 || toks[len(toks)-1].Kind != KindEOF {
		t.Fatal("expected EOF at end")
	}
	// Spot check: starts with module, has KindString for "Hello, "
	if toks[0].Kind != KindModule {
		t.Fatalf("first token should be module, got %v", toks[0])
	}
	hasHello := false
	for _, tk := range toks {
		if tk.Kind == KindString && tk.Value == "Hello, " {
			hasHello = true
		}
	}
	if !hasHello {
		t.Fatal("expected to find string 'Hello, '")
	}
}

func TestNumberFollowedByDot(t *testing.T) {
	// "42." should be int 42 + dot, not float (no digit after .)
	toks, err := Lex("42.")
	if err != nil {
		t.Fatalf("lex error: %v", err)
	}
	if toks[0].Kind != KindInt || toks[0].Value != "42" {
		t.Fatalf("want int 42, got %v", toks[0])
	}
	if toks[1].Kind != KindDot {
		t.Fatalf("want dot, got %v", toks[1])
	}
}

func TestMinusVsArrow(t *testing.T) {
	// "->" is arrow, "-" alone is minus
	eqKinds(t, "-> -", KindArrow, KindMinus)
}

func TestBackslashLambda(t *testing.T) {
	// \x -> x
	src := `\x -> x`
	eqKinds(t, src, KindBackslash, KindLowerName, KindArrow, KindLowerName)
}
