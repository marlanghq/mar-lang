package runtime

import (
	"strings"
	"testing"
)

// The read path refuses a stored value that does not match its column. The
// write path used to transcribe whatever it was handed, switching on the
// VALUE and never on the column — so an Int written into a TEXT column was
// stored happily and failed on the way back OUT, at a point with nothing to
// connect it to the write that caused it.
//
// These pin both directions: every column kind accepts the value it declares,
// and refuses the ones it does not.

func col(name, sqlType string) EntityField {
	return EntityField{Name: name, SQLType: sqlType, NotNull: true}
}

func mustStore(t *testing.T, f EntityField, v Value) any {
	t.Helper()
	out, err := marValueToSQLForField(&f, v)
	if err != nil {
		t.Fatalf("column %q must accept %s: %v", f.Name, describeValueKind(v), err)
	}
	return out
}

func refuses(t *testing.T, f EntityField, v Value, wants ...string) {
	t.Helper()
	_, err := marValueToSQLForField(&f, v)
	if err == nil {
		t.Fatalf("column %q (%s) accepted %s", f.Name, f.SQLType, describeValueKind(v))
	}
	for _, w := range wants {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("the message must mention %q, got: %v", w, err)
		}
	}
}

func TestEveryColumnAcceptsWhatItDeclares(t *testing.T) {
	if got := mustStore(t, col("age", "INTEGER"), VInt{V: 36}); got != int64(36) {
		t.Errorf("INTEGER stored %v", got)
	}
	if got := mustStore(t, col("name", "TEXT"), VString{V: "ada"}); got != "ada" {
		t.Errorf("TEXT stored %v", got)
	}
	if got := mustStore(t, col("done", "BOOLEAN"), VBool{V: true}); got != true {
		t.Errorf("BOOLEAN stored %v", got)
	}
	if got := mustStore(t, col("at", "TIMESTAMP"), VTime{Millis: 1700}); got != int64(1700) {
		t.Errorf("TIMESTAMP stored %v", got)
	}
}

func TestAValueOfTheWrongKindIsRefusedOnTheWayIn(t *testing.T) {
	refuses(t, col("age", "INTEGER"), VString{V: "old"}, "age", "whole number", `the text "old"`)
	refuses(t, col("name", "TEXT"), VInt{V: 7}, "name", "text", "whole number 7")
	refuses(t, col("done", "BOOLEAN"), VInt{V: 1}, "done", "true or false")
	refuses(t, col("at", "TIMESTAMP"), VInt{V: 1700}, "at", "expected Time")
}

func TestAnOptionalValueIsUnwrappedBeforeItIsJudged(t *testing.T) {
	// `Just "ada"` into a TEXT column is the string, not a constructor.
	if got := mustStore(t, col("name", "TEXT"), VCtor{Tag: "Just", Args: []Value{VString{V: "ada"}}}); got != "ada" {
		t.Errorf("stored %v", got)
	}
}

func TestAnEnumColumnStillJudgesTheConstructorSet(t *testing.T) {
	f := col("status", "TEXT")
	f.AcceptedCtors = []string{"Open", "Closed"}
	if got := mustStore(t, f, VCtor{Tag: "Open"}); got != "Open" {
		t.Errorf("stored %v", got)
	}
	refuses(t, f, VCtor{Tag: "Pending"}, "not in accepted set")
	// And the enum branch runs BEFORE the TEXT branch, so a bare string is
	// still refused for the reason that names the constructor set.
	refuses(t, f, VString{V: "Open"}, "expected a constructor")
}

func TestAFilterWithNoColumnStaysATranscription(t *testing.T) {
	// Some callers have no column in hand. There is nothing to check
	// against, and refusing on a guess would be worse than transcribing.
	out, err := marValueToSQLForField(nil, VString{V: "ada"})
	if err != nil || out != "ada" {
		t.Fatalf("got %v, %v", out, err)
	}
}
