package parityfixture

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A number two runtimes both name is a number they both have to mean.
//
// Two of these were found by hand -- the recursion budget and the Decimal
// width -- and finding them by hand is the argument for the scan: a constant
// with the same name in two runtimes and a different value in each is a
// disagreement nobody wrote down and no test would notice, because each side
// is internally consistent.
//
// This is the structural axis, not the lexical one: it needs no comment
// claiming anything. It reads the constants and compares them.
func TestANumberTwoRuntimesNameIsANumberTheyBothMean(t *testing.T) {
	// Names that differ ON PURPOSE, each with the reason and the test that
	// owns it. Anything else that differs is a finding.
	deliberate := map[string]string{
		"maxcalldepth": "the recursion budget is per-platform on purpose: a goroutine " +
			"stack is not a V8 stack is not a phone's main thread. policy_numbers_test.go " +
			"pins the DIRECTION, which is the part that matters",
	}

	byRuntime := map[string]map[string]string{
		"server":  scanConstants(t, []string{"../runtime"}, `\b([A-Za-z][A-Za-z0-9]{3,})\s*(?:int|int64)?\s*=\s*([0-9][0-9_]*)\b`),
		"browser": scanConstants(t, []string{"../jsserve"}, `const ([A-Za-z_][A-Za-z0-9_]{3,})\s*=\s*([0-9][0-9_]*)\b`),
		"phone":   scanConstants(t, []string{"../iosbundle/template/Sources"}, `let ([A-Za-z][A-Za-z0-9]{3,})\s*(?::\s*[A-Za-z]+)?\s*=\s*([0-9][0-9_]*)\b`),
	}
	for who, m := range byRuntime {
		if len(m) < 5 {
			t.Fatalf("only %d constants found in the %s runtime; the scan is wrong, not the code",
				len(m), who)
		}
	}

	shared := map[string]map[string]string{}
	for who, m := range byRuntime {
		for name, val := range m {
			if shared[name] == nil {
				shared[name] = map[string]string{}
			}
			shared[name][who] = val
		}
	}

	var problems []string
	compared, used := 0, map[string]bool{}
	for name, vals := range shared {
		if len(vals) < 2 {
			continue
		}
		compared++
		distinct := map[string]bool{}
		for _, v := range vals {
			distinct[strings.ReplaceAll(v, "_", "")] = true
		}
		if len(distinct) == 1 {
			continue
		}
		if _, ok := deliberate[name]; ok {
			used[name] = true
			continue
		}
		var shown []string
		for who, v := range vals {
			shown = append(shown, who+"="+v)
		}
		sort.Strings(shown)
		problems = append(problems, name+": "+strings.Join(shown, " ")+"\n"+
			"    Two runtimes name the same number and mean different things by it.\n"+
			"    Neither side is internally inconsistent, so nothing else would notice.\n"+
			"    Make them agree, or say here why they must not.")
	}
	for name := range deliberate {
		if !used[name] {
			problems = append(problems, name+" is listed as deliberately different and no longer differs; drop the row")
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
	if compared < 8 {
		t.Errorf("only %d shared constants compared; the scan stopped seeing them", compared)
	}
	if !t.Failed() {
		t.Logf("%d numbers are named by more than one runtime, and %d of them agree",
			compared, compared-len(deliberate))
	}
}

func scanConstants(t *testing.T, dirs []string, pattern string) map[string]string {
	t.Helper()
	re := regexp.MustCompile(pattern)
	out := map[string]string{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			ext := filepath.Ext(e.Name())
			if ext != ".go" && ext != ".js" && ext != ".swift" {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				out[strings.ToLower(strings.ReplaceAll(m[1], "_", ""))] = m[2]
			}
		}
	}
	return out
}
