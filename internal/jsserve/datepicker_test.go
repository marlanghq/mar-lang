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
	"mar/internal/parser"
	"mar/internal/typecheck"
)

// The browser's date input on a device in each zone: a picked day comes back
// as the Date with that label and shows as the label; a text the input does
// not offer is refused; a Date before the first offered day shows as that
// day. Date.today is the day the device's wall clock shows, as Go's time
// package works it out.
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
	driver := `
const fs = require('fs');
globalThis.window = globalThis; globalThis.document = undefined;
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const input = globalThis.__marTestDateInput;
const [labels, early, instants] = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const out = [];
for (const label of labels) {
  const days = input.parse(label);
  out.push('pick [' + label + '] ' + days + ' ' + (days === null ? '-' : input.value(days)));
}
for (const days of early) out.push('show ' + days + ' ' + input.value(days));
for (const ms of instants) out.push('today ' + ms + ' ' + input.today(ms));
process.stdout.write(out.join('\n'));
`
	var early []int64
	for _, label := range parityfixture.DatePickerEarly {
		d, err := parityfixture.DayOf(label)
		if err != nil {
			t.Fatal(err)
		}
		early = append(early, d)
	}
	instants := parityfixture.TodayInstants()
	labels := append(append([]string{}, parityfixture.DatePickerDays...), parityfixture.DatePickerRefused...)
	input, _ := json.Marshal([]any{labels, early, instants})
	for name, body := range map[string]string{"runtime.js": string(rt), "driver.js": driver, "input.json": string(input)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	refused := map[string]bool{}
	for _, d := range parityfixture.DatePickerRefused {
		refused[d] = true
	}
	for _, zone := range parityfixture.DatePickerZones {
		cmd := exec.Command(node, filepath.Join(dir, "driver.js"), filepath.Join(dir, "runtime.js"), filepath.Join(dir, "input.json"))
		cmd.Env = append(os.Environ(), "TZ="+zone)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: node: %v\n%s", zone, err, out)
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if want := len(labels) + len(early) + len(instants); len(lines) != want {
			t.Fatalf("%s: %d lines, want %d:\n%s", zone, len(lines), want, out)
		}
		for _, line := range lines {
			f := strings.Fields(line)
			switch f[0] {
			case "pick":
				label := strings.TrimSuffix(strings.TrimPrefix(f[1], "["), "]")
				if refused[label] {
					if f[2] != "null" {
						t.Errorf("%s: %q is not a day the input offers, picked as %s", zone, label, f[2])
					}
					continue
				}
				want, err := parityfixture.DayOf(label)
				if err != nil {
					t.Fatal(err)
				}
				if f[2] != strconv.FormatInt(want, 10) || f[3] != label {
					t.Errorf("%s: picked %s, got the day %s shown as %s; want %d shown as %s", zone, label, f[2], f[3], want, label)
				}
			case "show":
				if f[2] != parityfixture.DatePickerFirst {
					t.Errorf("%s: the Date %s shows as %s, want %s", zone, f[1], f[2], parityfixture.DatePickerFirst)
				}
			case "today":
				ms, _ := strconv.ParseInt(f[1], 10, 64)
				want, err := parityfixture.TodayIn(zone, ms)
				if err != nil {
					t.Fatal(err)
				}
				if f[2] != strconv.FormatInt(want, 10) {
					t.Errorf("%s: at %s Date.today is the day %s, the device's clock shows %d", zone, f[1], f[2], want)
				}
			default:
				t.Errorf("%s: %q", zone, line)
			}
		}
	}
}

// Typing into the date input: Chrome fires a change on every keystroke, so a
// year being typed passes through texts that are not offered days. Those
// dispatch nothing and stay on screen while the field has focus; an offered
// day is dispatched; leaving the field puts back the day the model holds.
func TestTheBrowserDateInputLetsAYearBeTyped(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	day := func(label string) int64 {
		d, err := parityfixture.DayOf(label)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	cases := []struct {
		model, typed string
		sent         string // the day dispatched, "" for none
		afterBlur    string
	}{
		{"2026-11-17", "0001-11-17", "", "2026-11-17"},
		{"2026-11-17", "0019-11-17", "", "2026-11-17"},
		{"2026-11-17", "0199-11-17", "", "2026-11-17"},
		{"2026-11-17", "", "", "2026-11-17"},
		{"2026-11-17", "1582-10-14", "", "2026-11-17"},
		{"2026-11-17", "1999-11-17", "1999-11-17", "2026-11-17"},
		{"0000-01-01", "", "", parityfixture.DatePickerFirst},
	}
	type probe struct {
		Days  int64  `json:"days"`
		Typed string `json:"typed"`
	}
	var probes []probe
	for _, c := range cases {
		probes = append(probes, probe{day(c.model), c.typed})
	}
	dir := t.TempDir()
	rt, err := os.ReadFile("runtime.js")
	if err != nil {
		t.Fatal(err)
	}
	in, _ := json.Marshal(probes)
	driver := `
const fs = require('fs');
globalThis.window = globalThis; globalThis.document = undefined;
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const out = JSON.parse(process.argv[3]).map(p => globalThis.__marTestDateInput.type(p.days, p.typed));
process.stdout.write(JSON.stringify(out));
`
	for name, body := range map[string]string{"runtime.js": string(rt), "driver.js": driver} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := exec.Command(node, filepath.Join(dir, "driver.js"), filepath.Join(dir, "runtime.js"), string(in)).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var got []struct {
		Sent       []int64 `json:"sent"`
		TypedStays string  `json:"typedStays"`
		AfterBlur  string  `json:"afterBlur"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for i, c := range cases {
		g := got[i]
		var want []int64
		if c.sent != "" {
			want = []int64{day(c.sent)}
		}
		if len(g.Sent) != len(want) || (len(want) == 1 && g.Sent[0] != want[0]) {
			t.Errorf("model %s, typed %q: dispatched %v, want %v", c.model, c.typed, g.Sent, want)
		}
		if g.TypedStays != c.typed {
			t.Errorf("model %s, typed %q: the change rewrote the field to %q while it was being typed in", c.model, c.typed, g.TypedStays)
		}
		if g.AfterBlur != c.afterBlur {
			t.Errorf("model %s, typed %q: after leaving the field it shows %q, want %q", c.model, c.typed, g.AfterBlur, c.afterBlur)
		}
	}
}

// Date.today itself, run as the program runs it, on a device in each zone: the
// day the device's wall clock shows, as Go's time package works it out.
func TestTheBrowsersDateTodayIsTheDevicesDay(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	mod, err := parser.Parse("module Main exposing (..)\n\n\ntoday : Task Date\ntoday =\n    Date.today\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatal(err)
	}
	pj, err := json.Marshal(map[string]any{"modules": []any{SerializeModule(mod)}})
	if err != nil {
		t.Fatal(err)
	}
	rt, err := os.ReadFile("runtime.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	driver := `
const fs = require('fs');
globalThis.window = globalThis; globalThis.document = undefined;
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const before = Date.now();
const today = globalThis.__marEvalRaw(program, 'Main.today').run();
process.stdout.write(before + ' ' + today.days + ' ' + Date.now());
`
	for name, body := range map[string]string{"runtime.js": string(rt), "program.json": string(pj), "driver.js": driver} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, zone := range parityfixture.DatePickerZones {
		cmd := exec.Command(node, filepath.Join(dir, "driver.js"), filepath.Join(dir, "runtime.js"), filepath.Join(dir, "program.json"))
		cmd.Env = append(os.Environ(), "TZ="+zone)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: node: %v\n%s", zone, err, out)
		}
		f := strings.Fields(string(out))
		if len(f) != 3 {
			t.Fatalf("%s: %q", zone, out)
		}
		before, _ := strconv.ParseInt(f[0], 10, 64)
		after, _ := strconv.ParseInt(f[2], 10, 64)
		lo, err := parityfixture.TodayIn(zone, before)
		if err != nil {
			t.Fatal(err)
		}
		hi, _ := parityfixture.TodayIn(zone, after)
		if got := f[1]; got != strconv.FormatInt(lo, 10) && got != strconv.FormatInt(hi, 10) {
			t.Errorf("%s: Date.today is the day %s, the device's clock shows %d", zone, got, lo)
		}
	}
}
