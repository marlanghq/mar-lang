package jsserve

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mar/internal/parityfixture"
)

// The browser's date input: a picked day comes back as local midnight of that
// day, in every zone, and shows as the day that was picked; a day the input
// does not offer is refused, and a Time on such a day shows as an empty field.
func TestTheBrowserDateInputKeepsThePickedDay(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	rt, err := os.ReadFile("runtime.js")
	if err != nil {
		t.Fatal(err)
	}
	// One process per zone, the days picked in order, as a person steps
	// through them: V8 answers Date's local getters from a cache that an
	// earlier pick can leave stale.
	driver := `
const fs = require('fs');
globalThis.window = globalThis; globalThis.document = undefined;
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const input = globalThis.__marTestDateInput;
const out = [];
for (const day of JSON.parse(process.argv[3])) {
  const ms = input.parse(day);
  out.push(day + ' ' + ms + ' ' + (ms === null ? '-' : input.value(ms)));
}
for (const ms of JSON.parse(process.argv[4])) out.push('show ' + ms + ' [' + input.value(ms) + ']');
process.stdout.write(out.join('\n'));
`
	for name, body := range map[string]string{"runtime.js": string(rt), "driver.js": driver} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	refused := map[string]bool{}
	for _, d := range parityfixture.DatePickerRefused {
		refused[d] = true
	}
	days, _ := json.Marshal(append(append([]string{}, parityfixture.DatePickerDays...), parityfixture.DatePickerRefused...))
	for _, zone := range parityfixture.DatePickerZones {
		var unshown []int64
		for _, u := range parityfixture.DatePickerUnshown {
			if u.Zone == zone {
				unshown = append(unshown, u.Millis)
			}
		}
		shows, _ := json.Marshal(append([]int64{}, unshown...))
		cmd := exec.Command(node, filepath.Join(dir, "driver.js"), filepath.Join(dir, "runtime.js"), string(days), string(shows))
		cmd.Env = append(os.Environ(), "TZ="+zone)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: node: %v\n%s", zone, err, out)
		}
		seen := 0
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			f := strings.Fields(line)
			seen++
			switch {
			case len(f) == 3 && f[0] == "show":
				if f[2] != "[]" {
					t.Errorf("%s: the Time %s is on a day the input does not offer, shown as %s", zone, f[1], f[2])
				}
			case len(f) == 3 && refused[f[0]]:
				if f[1] != "null" {
					t.Errorf("%s: %s is not a day the input offers, picked as %s", zone, f[0], f[1])
				}
			case len(f) == 3 && f[2] == f[0]:
				want, err := parityfixture.LocalMidnight(zone, f[0])
				if err != nil {
					t.Fatal(err)
				}
				if f[1] != strconv.FormatInt(want, 10) {
					t.Errorf("%s: %s is %s, local midnight is %d", zone, f[0], f[1], want)
				}
			default:
				t.Errorf("%s: %q", zone, line)
			}
		}
		if want := len(parityfixture.DatePickerDays) + len(parityfixture.DatePickerRefused) + len(unshown); seen != want {
			t.Errorf("%s: %d lines, want %d:\n%s", zone, seen, want, out)
		}
	}
}
