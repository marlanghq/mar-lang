package apphost

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// The database boundary, censused the way the wire boundary is.
//
// Every column has to have a checked path in BOTH directions: `decodeColumn`
// produces the declared type or fails, and `storableAs` writes the declared
// type or refuses. Both were transcriptions once — the read turned 3.7 into 3
// on a column whose type says Int, and the write took whatever it was handed
// and let the READ fail later, at a point with nothing to connect it to.
//
// The census is a test rather than a note because a new column kind is the
// way this comes undone: a `case` neither switch has falls through, and only
// a program using that kind would ever notice.
func TestEveryColumnKindInTheExamplesIsCheckedBothWays(t *testing.T) {
	if testing.Short() {
		t.Skip("boots every example; skipped under -short")
	}
	kinds := map[string]int{}
	var unchecked []string
	for _, ex := range loadEveryExample(t) {
		for _, c := range ex.columns {
			kinds[c.kind]++
			if !c.checked {
				unchecked = append(unchecked, c.where)
			}
		}
	}

	if len(kinds) < 3 {
		t.Fatalf("only %d column kinds were reached; the census is not exercising them", len(kinds))
	}
	sort.Strings(unchecked)
	if len(unchecked) > 0 {
		t.Fatalf("%d column(s) have a path that checks nothing:\n  %s",
			len(unchecked), strings.Join(unchecked, "\n  "))
	}
	var summary []string
	for k, n := range kinds {
		summary = append(summary, fmt.Sprintf("%s×%d", k, n))
	}
	sort.Strings(summary)
	t.Logf("every column is checked on the way in and on the way out: %s",
		strings.Join(summary, " "))
}
