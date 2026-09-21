package conformance

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// ADR 0038 clause 1 says a Char is one Unicode scalar value, and clause 6 says
// case mapping is FULL and may change a string's length. Those two meet at
// Char.toUpper, whose type cannot hold a longer answer: the full uppercase of
// the sharp s is two letters. So a Char maps with Unicode's SIMPLE mapping and
// a String with the full one, which is the distinction Unicode draws and the
// reason it publishes both.
//
// Go gets the simple mapping from its own unicode package. The browser and the
// phone have no simple-mapping API at all, only a full one, so they carry a
// small table instead. A hand-written table is a table that rots, and this
// test is what keeps it honest: it derives the truth from Go's unicode data
// and reads the two source files back.
//
// The table stays small because of a property worth stating: wherever the full
// mapping is exactly one scalar it IS the simple mapping. That is asserted
// below over every scalar rather than believed, because the tables are only
// correct if it holds.

var (
	jsRuntimePath = "../jsserve/runtime.js"
	swiftCharPath = "../iosbundle/template/Sources/MarChar.swift"
)

func simpleCaseExceptions(t *testing.T) (upper, lower map[rune]rune) {
	t.Helper()
	up := cases.Upper(language.Und)
	lo := cases.Lower(language.Und)
	upper, lower = map[rune]rune{}, map[rune]rune{}
	var violations []string
	for r := rune(0); r <= 0x10FFFF; r++ {
		if !utf8.ValidRune(r) {
			continue
		}
		s := string(r)

		if full := []rune(up.String(s)); len(full) == 1 {
			if full[0] != unicode.ToUpper(r) {
				violations = append(violations, fmt.Sprintf("U+%04X upper: full=U+%04X simple=U+%04X", r, full[0], unicode.ToUpper(r)))
			}
		} else if m := unicode.ToUpper(r); m != r {
			upper[r] = m
		}

		if full := []rune(lo.String(s)); len(full) == 1 {
			if full[0] != unicode.ToLower(r) {
				violations = append(violations, fmt.Sprintf("U+%04X lower: full=U+%04X simple=U+%04X", r, full[0], unicode.ToLower(r)))
			}
		} else if m := unicode.ToLower(r); m != r {
			lower[r] = m
		}
	}
	if len(violations) > 0 {
		if len(violations) > 10 {
			violations = append(violations[:10], fmt.Sprintf("...and %d more", len(violations)-10))
		}
		t.Fatalf("the property the client tables are built on no longer holds: a full case mapping\n"+
			"of one scalar is supposed to BE the simple mapping, and %d scalars now disagree:\n  %s\n\n"+
			"The clients decide by length, so every one of these is now mapped wrongly there.\n"+
			"They need the full simple mapping, not an exception table.",
			len(violations), strings.Join(violations, "\n  "))
	}
	return upper, lower
}

// pairsIn reads `0xABCD: 0xEF01,` pairs out of the named table in a source
// file. Both clients spell the table the same way, which is why one reader
// serves both.
func pairsIn(t *testing.T, path, table string) map[rune]rune {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(raw)
	i := strings.Index(src, table)
	if i < 0 {
		t.Fatalf("%s no longer declares %s; it was renamed or removed", path, table)
	}
	rest := src[i:]
	// The two files close the table differently, and the phone's is indented
	// by whatever scope it sits in, so the terminator is matched rather than
	// spelled: the first line that is only a closing bracket.
	loc := regexp.MustCompile(`(?m)^\s*(\]|\};|\}\)|\})`).FindStringIndex(rest)
	if loc == nil {
		t.Fatalf("%s: could not find the end of %s", path, table)
	}
	end := loc[1]
	out := map[rune]rune{}
	for _, m := range regexp.MustCompile(`0x([0-9A-Fa-f]{4,6}):\s*0x([0-9A-Fa-f]{4,6})`).FindAllStringSubmatch(rest[:end], -1) {
		from, _ := strconv.ParseInt(m[1], 16, 32)
		to, _ := strconv.ParseInt(m[2], 16, 32)
		out[rune(from)] = rune(to)
	}
	return out
}

func diffTables(who string, want, got map[rune]rune) []string {
	var problems []string
	for r, w := range want {
		g, ok := got[r]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s is missing U+%04X -> U+%04X", who, r, w))
		} else if g != w {
			problems = append(problems, fmt.Sprintf("%s maps U+%04X to U+%04X; Unicode says U+%04X", who, r, g, w))
		}
	}
	for r, g := range got {
		if _, ok := want[r]; !ok {
			problems = append(problems, fmt.Sprintf("%s carries U+%04X -> U+%04X, which no longer needs an entry", who, r, g))
		}
	}
	sort.Strings(problems)
	return problems
}

func TestClientSimpleCaseTablesMatchUnicode(t *testing.T) {
	wantUpper, wantLower := simpleCaseExceptions(t)

	for _, c := range []struct {
		who   string
		path  string
		table string
		want  map[rune]rune
	}{
		{"the browser's SIMPLE_UPPER", jsRuntimePath, "SIMPLE_UPPER", wantUpper},
		{"the browser's SIMPLE_LOWER", jsRuntimePath, "SIMPLE_LOWER", wantLower},
		{"the phone's simpleUpper", swiftCharPath, "simpleUpper", wantUpper},
		{"the phone's simpleLower", swiftCharPath, "simpleLower", wantLower},
	} {
		problems := diffTables(c.who, c.want, pairsIn(t, c.path, c.table))
		if len(problems) > 0 {
			t.Errorf("%s no longer matches Go's unicode data (%d problem(s)):\n  %s\n\n"+
				"Go reads its simple mappings from the unicode package, so a Go toolchain update\n"+
				"moves the answer on the server and nowhere else. Update the table in %s.",
				c.who, len(problems), strings.Join(problems, "\n  "), c.path)
		}
	}
}
