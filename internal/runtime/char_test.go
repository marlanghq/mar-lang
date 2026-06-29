package runtime

import "testing"

func TestEvalCharLiteral(t *testing.T) {
	if got := runValue(t, `'a'`); got != "'a'" {
		t.Fatalf("ASCII: %s", got)
	}
	if got := runValue(t, `'\n'`); got != "'\n'" {
		t.Fatalf("newline: %s", got)
	}
	if got := runValue(t, `'😀'`); got != "'😀'" {
		t.Fatalf("emoji: %s", got)
	}
	if got := runValue(t, `'\u{1F642}'`); got != "'🙂'" {
		t.Fatalf("unicode escape: %s", got)
	}
}

func TestEvalCharComparison(t *testing.T) {
	if got := runValue(t, `'a' == 'a'`); got != "True" {
		t.Fatalf("eq: %s", got)
	}
	if got := runValue(t, `'a' == 'b'`); got != "False" {
		t.Fatalf("neq: %s", got)
	}
	if got := runValue(t, `'a' < 'b'`); got != "True" {
		t.Fatalf("lt: %s", got)
	}
	if got := runValue(t, `'b' < 'a'`); got != "False" {
		t.Fatalf("gt: %s", got)
	}
}

func TestEvalCharModule(t *testing.T) {
	if got := runValue(t, `Char.toCode 'A'`); got != "65" {
		t.Fatalf("toCode: %s", got)
	}
	if got := runValue(t, `Char.fromCode 97`); got != "'a'" {
		t.Fatalf("fromCode: %s", got)
	}
	// Out-of-range / surrogate → U+FFFD
	// 55296 = 0xD800, first surrogate code point
	if got := runValue(t, `Char.fromCode 55296`); got != "'�'" {
		t.Fatalf("surrogate sanitized: %s", got)
	}
	if got := runValue(t, `Char.isDigit '5'`); got != "True" {
		t.Fatalf("isDigit '5': %s", got)
	}
	if got := runValue(t, `Char.isAlpha 'a'`); got != "True" {
		t.Fatalf("isAlpha 'a': %s", got)
	}
	if got := runValue(t, `Char.toUpper 'a'`); got != "'A'" {
		t.Fatalf("toUpper: %s", got)
	}
}

func TestEvalStringCharBridges(t *testing.T) {
	// String.toList iterates code points — 🇧🇷 → 2 chars (the regional
	// indicator scalars), not 1. Documents the code-point model
	// explicitly.
	if got := runValue(t, `List.length (String.toList "abc")`); got != "3" {
		t.Fatalf("toList length: %s", got)
	}
	if got := runValue(t, `List.length (String.toList "🇧🇷")`); got != "2" {
		t.Fatalf("flag splits to 2 scalars: %s", got)
	}
	if got := runValue(t, `String.fromList ['h', 'i']`); got != `"hi"` {
		t.Fatalf("fromList: %s", got)
	}
	if got := runValue(t, `String.cons 'h' "i"`); got != `"hi"` {
		t.Fatalf("cons: %s", got)
	}
}

func TestEvalStringPadWithChar(t *testing.T) {
	if got := runValue(t, `String.padLeft 5 '0' "42"`); got != `"00042"` {
		t.Fatalf("padLeft: %s", got)
	}
	if got := runValue(t, `String.padRight 5 '.' "ab"`); got != `"ab..."` {
		t.Fatalf("padRight: %s", got)
	}
}

func TestEvalCharPatternMatch(t *testing.T) {
	src := `case 'b' of
    'a' -> 1
    'b' -> 2
    _ -> 0`
	if got := runValue(t, src); got != "2" {
		t.Fatalf("char pattern: %s", got)
	}
}

func TestEvalDictWithCharKeys(t *testing.T) {
	// Char is Comparable, so Dict/Set should accept it as a key.
	src := `Dict.get 'a' (Dict.fromList [('a', 1), ('b', 2)])`
	if got := runValue(t, src); got != "Just 1" {
		t.Fatalf("Dict Char key: %s", got)
	}
}

func TestEvalCharJSONRoundtrip(t *testing.T) {
	if got := runValue(t, `JSON.encode 'a'`); got != `"{\"__char\":\"a\"}"` {
		t.Fatalf("encode: %s", got)
	}
}

func TestEvalStringMap(t *testing.T) {
	if got := runValue(t, `String.map Char.toUpper "hello"`); got != `"HELLO"` {
		t.Fatalf("toUpper: %s", got)
	}
	if got := runValue(t, `String.map Char.toLower "WORLD"`); got != `"world"` {
		t.Fatalf("toLower: %s", got)
	}
	// Empty string stays empty.
	if got := runValue(t, `String.map Char.toUpper ""`); got != `""` {
		t.Fatalf("empty: %s", got)
	}
	// Multi-byte chars survive.
	if got := runValue(t, `String.map (\c -> c) "ação"`); got != `"ação"` {
		t.Fatalf("multibyte: %s", got)
	}
}

func TestEvalStringFilter(t *testing.T) {
	if got := runValue(t, `String.filter Char.isDigit "abc123xyz"`); got != `"123"` {
		t.Fatalf("filter digits: %s", got)
	}
	if got := runValue(t, `String.filter Char.isAlpha "abc123"`); got != `"abc"` {
		t.Fatalf("filter letters: %s", got)
	}
	if got := runValue(t, `String.filter (\c -> c == 'a') "banana"`); got != `"aaa"` {
		t.Fatalf("filter a: %s", got)
	}
}

func TestEvalStringFoldl(t *testing.T) {
	// Count chars
	if got := runValue(t, `String.foldl (\c acc -> acc + 1) 0 "hello"`); got != "5" {
		t.Fatalf("count: %s", got)
	}
	// Reverse via cons
	if got := runValue(t, `String.foldl String.cons "" "abc"`); got != `"cba"` {
		t.Fatalf("reverse: %s", got)
	}
}

func TestEvalStringAny(t *testing.T) {
	if got := runValue(t, `String.any Char.isDigit "abc"`); got != "False" {
		t.Fatalf("none: %s", got)
	}
	if got := runValue(t, `String.any Char.isDigit "abc1"`); got != "True" {
		t.Fatalf("one: %s", got)
	}
	// Empty short-circuits to False.
	if got := runValue(t, `String.any Char.isDigit ""`); got != "False" {
		t.Fatalf("empty: %s", got)
	}
}
