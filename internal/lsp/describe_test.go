package lsp_test

import (
	"strings"
	"testing"

	"mar/internal/lsp"
)

// `mar/describe` is the reference answered by NAME rather than by cursor
// position, for a caller that has one and not the other: a language model
// writing Mar in the editor.
//
// The reason it is worth a protocol extension rather than a docs link is that
// every example it returns is evaluated by a test that requires it to be True.
// A model asking about Decimal.toCents gets a checked fact, not a plausible
// sentence -- and Mar is in no training data, so plausible sentences are
// exactly what it would otherwise produce.
func TestDescribeAnswersWithTheReferencesOwnWords(t *testing.T) {
	t.Run("a qualified name", func(t *testing.T) {
		got := lsp.Describe("Decimal.toCents", 0)
		if got.Exact == nil {
			t.Fatal("Decimal.toCents has no exact answer")
		}
		if got.Exact.Type != "Decimal -> Int" {
			t.Errorf("type is %q", got.Exact.Type)
		}
		if !strings.Contains(got.Exact.Doc, "hundredths") {
			t.Errorf("doc is %q", got.Exact.Doc)
		}
		if len(got.Exact.Examples) == 0 {
			t.Error("no examples, which is the half that makes this worth asking")
		}
	})

	// Basics is the reference's name for the module-less names and does not
	// exist in the language: `Basics.not` does not compile. A caller asks for
	// what a program writes.
	t.Run("a bare global", func(t *testing.T) {
		got := lsp.Describe("not", 0)
		if got.Exact == nil {
			t.Fatal("`not` has no exact answer")
		}
		if got.Exact.Doc == "" {
			t.Error("`not` has no description")
		}
	})

	// The caller that most needs this is the one that does not know the
	// spelling, so a partial name leads somewhere.
	t.Run("a prefix answers with the matches", func(t *testing.T) {
		got := lsp.Describe("Decimal.to", 0)
		if len(got.Matches) < 2 {
			t.Fatalf("Decimal.to matched %d names", len(got.Matches))
		}
		for _, m := range got.Matches {
			if !strings.Contains(strings.ToLower(m.Name), "decimal.to") {
				t.Errorf("%s does not contain the query", m.Name)
			}
		}
	})

	// Total says whether the list is all of them, so a caller can tell a
	// complete answer from a truncated one instead of guessing.
	t.Run("a truncated list says so", func(t *testing.T) {
		got := lsp.Describe("List.", 3)
		if len(got.Matches) != 3 {
			t.Fatalf("limit 3 returned %d", len(got.Matches))
		}
		if got.Total <= 3 {
			t.Errorf("Total is %d, so nothing says the list was cut", got.Total)
		}
	})

	t.Run("a name that is not Mar's answers nothing rather than guessing", func(t *testing.T) {
		got := lsp.Describe("List.foldRight", 0)
		if got.Exact != nil {
			t.Errorf("invented an answer for a name Mar does not have: %+v", got.Exact)
		}
	})
}
