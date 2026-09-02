package typecheck

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// noJSONForm is a list of names, and a list of names ages. This is what keeps
// it honest: the encoder's own `case` arms say which values it can write, and
// every framework value type WITHOUT an arm has to appear here.
//
// Derived, not remembered. Add a case to encodeValue and the corresponding
// entry has to go; add a value type the encoder cannot write and it has to
// arrive, or a call site passing one goes back to failing at runtime with the
// app already in front of somebody.
func TestTheNoJSONFormListMatchesTheEncodersOwnCases(t *testing.T) {
	src, err := os.ReadFile("../runtime/json.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	start := strings.Index(body, "func encodeValue(")
	if start < 0 {
		t.Fatal("encodeValue is not in json.go; this test is looking in the wrong place")
	}
	end := strings.Index(body[start:], "\n}\n")
	if end < 0 {
		t.Fatal("cannot find the end of encodeValue")
	}
	fn := body[start : start+end]

	// The arms encodeValue writes a value for. The VEffect/VFn arm is a
	// REFUSAL, not a case, so it is excluded by name.
	armRe := regexp.MustCompile(`case (V[A-Za-z]+(?:, V[A-Za-z]+)*):`)
	writes := map[string]bool{}
	for _, m := range armRe.FindAllStringSubmatch(fn, -1) {
		for _, name := range strings.Split(m[1], ", ") {
			writes[strings.TrimSpace(name)] = true
		}
	}
	if len(writes) < 12 {
		t.Fatalf("only %d encoder arms found; the scan is wrong, not the encoder", len(writes))
	}
	// The refusal arm names two types the encoder explicitly cannot write.
	delete(writes, "VEffect")
	delete(writes, "VFn")

	// Every value type the runtime has.
	all, err := os.ReadFile("../runtime/value.go")
	if err != nil {
		// The types are spread across files; scan the package instead.
		all = nil
	}
	_ = all
	// `Value` is the interface every value satisfies, not a value type.
	typeRe := regexp.MustCompile(`(?m)^type (V[A-Z][A-Za-z]*) `)
	valueTypes := map[string]bool{}
	entries, err := os.ReadDir("../runtime")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile("../runtime/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range typeRe.FindAllStringSubmatch(string(b), -1) {
			valueTypes[m[1]] = true
		}
	}
	if len(valueTypes) < 25 {
		t.Fatalf("only %d value types found; the scan is wrong", len(valueTypes))
	}

	// VDictPair is a Dict's internal pair, never a value on its own.
	delete(valueTypes, "VDictPair")

	// V<Name> -> the checker's type constructor name, for the ones that differ.
	marName := map[string]string{
		"VExposedService": "ExposedService",
		"VShared":         "App.Shared",
		// One runtime value, two Mar types: a Cmd on the client and a Task on
		// the server. Naming Cmd is enough for the scan; the lint names both.
		"VEffect": "Cmd",
	}
	var uncovered []string
	for v := range valueTypes {
		if writes[v] {
			continue
		}
		if v == "VFn" {
			continue // a function is a TArrow, handled without a name
		}
		name, ok := marName[v]
		if !ok {
			name = strings.TrimPrefix(v, "V")
		}
		if _, listed := noJSONForm[name]; !listed {
			uncovered = append(uncovered, v+" (would be "+name+")")
		}
	}
	sort.Strings(uncovered)
	if len(uncovered) > 0 {
		t.Errorf("the encoder cannot write %d value type(s) that noJSONForm does not name:\n  %s\n\n"+
			"A call site passing one goes back to failing at runtime, with the app already\n"+
			"in front of somebody. Add it to noJSONForm, or give encodeValue an arm.",
			len(uncovered), strings.Join(uncovered, "\n  "))
	}
	for name := range noJSONForm {
		v := "V" + name
		for k, mn := range marName {
			if mn == name {
				v = k
			}
		}
		if writes[v] {
			t.Errorf("noJSONForm names %q, and encodeValue now has a case for it; drop the row", name)
		}
	}
	if !t.Failed() {
		t.Logf("the encoder writes %d value types; %d framework types have no JSON form and are named",
			len(writes), len(noJSONForm))
	}
}
