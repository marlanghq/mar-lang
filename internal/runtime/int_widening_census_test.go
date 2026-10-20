package runtime

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// intrange.go claims: "Everything that can produce or admit an Int goes
// through it, so there is one answer to 'is this a Mar Int'." That was prose,
// and two sites did not — both found by hand, both real:
//
//	Time.toSeconds     int64(d.Seconds) from a float64 that holds 5e21
//	Decimal.toCents    IsInt64(), which is the wrong bound by ten orders
//
// The shared shape is a value arriving from a REPRESENTATION WIDER THAN INT —
// a float64 duration, a big.Int coefficient — and being narrowed by a Go
// conversion instead of by Mar's bound. Nothing else in the runtime can be out
// of range: an Int that is already an Int stays one, a `len` is bounded by
// memory, and an index is bounded by the list it indexes.
//
// So this is the rule, and it is narrow on purpose: a VInt built out of a
// widening conversion has to be listed here with the reason it is safe. It
// catches the next one without asking anyone to keep a 46-row table honest.
func TestNoIntIsNarrowedByGoInsteadOfByMarsBound(t *testing.T) {
	// Each entry is the expression as it appears, and why it cannot leave the
	// range. Anything not here fails the test.
	safe := map[string]string{
		"int64(len(l.Elements))":                     "a list's length is bounded by memory, far under 2^53",
		"int64(len(d.Pairs))":                        "a dict's size is bounded by memory",
		"int64(len(s.Items))":                        "a set's size is bounded by memory",
		"int64(len([]rune(s.V)))":                    "a string's length is bounded by memory",
		"int64(i)":                                   "an index into a list already in memory",
		"int64(runesBefore)":                         "a count of scalar values in a string already in memory",
		"int64(c.V)":                                 "a Char is a scalar value, at most 0x10FFFF",
		"int64(status)":                              "an HTTP status code",
		"int64(ttlMinutes)":                          "a configured TTL in minutes, from the app's own source",
		"int64(state >> 32)":                         "the high half of a 64-bit seed: 32 bits",
		"int64(state & 0xFFFFFFFF)":                  "the low half of a 64-bit seed: 32 bits",
		"int64(get(time.UnixMilli(t.Millis).UTC()))": "a calendar field: year, month, day, hour, minute, second",
		"r.Coef.Int64()":                             "guarded by inIntRange on the line above; see decToInt",
	}

	widening := regexp.MustCompile(`VInt\{V?:? *((?:int64\(|[A-Za-z_.]*\.Int64\(\))[^}]*)\}`)

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var problems []string
	seen := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			m := widening.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			expr := strings.TrimSpace(m[1])
			seen[expr] = true
			if _, ok := safe[expr]; !ok {
				problems = append(problems, strings.Join([]string{
					f + ":" + strconv.Itoa(i+1) + ": VInt{" + expr + "} narrows with a Go conversion.",
					"    Mar's Int is 53 bits and Go's is 64: if the value can come from",
					"    anything wider, route it through inIntRange (see intFromSeconds).",
					"    If it cannot, add the expression here with the reason.",
				}, "\n"))
			}
		}
	}
	for expr := range safe {
		if !seen[expr] {
			problems = append(problems, "the census lists VInt{"+expr+"}, which no longer appears; drop the row")
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
	if !t.Failed() {
		t.Logf("%d widening conversions into Int, each bounded by construction", len(seen))
	}
}
