package iosbundle

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

// The phone's date picker: a picked day comes back as local midnight of that
// day, in every zone, the Time the browser hands back for it, and shows as the
// day that was picked; a day the picker does not offer is refused, and a Time
// on such a day shows as the nearer end of the range.
func TestThePhoneDatePickerKeepsThePickedDay(t *testing.T) {
	swiftc, err := exec.LookPath("swiftc")
	if err != nil {
		t.Skip("swiftc is not installed")
	}
	if testing.Short() {
		t.Skip("compiles the Swift core; not for -short")
	}
	dir := t.TempDir()
	if err := assembleSwiftCore(t, dir); err != nil {
		t.Fatalf("assembling the core: %v", err)
	}
	bin := filepath.Join(dir, "driver")
	build := exec.Command(swiftc, "-o", bin)
	sources, _ := filepath.Glob(filepath.Join(dir, "*.swift"))
	build.Args = append(build.Args, sources...)
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("swiftc: %v\n%s", err, out)
	}
	browser := browserPicks(t)
	for _, zone := range parityfixture.DatePickerZones {
		args := append([]string{"--picker", zone}, parityfixture.DatePickerDays...)
		args = append(args, parityfixture.DatePickerRefused...)
		out, err := exec.Command(bin, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", zone, err, out)
		}
		web := browser(zone)
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if want := len(parityfixture.DatePickerDays) + len(parityfixture.DatePickerRefused); len(lines) != want {
			t.Fatalf("%s: %d lines, want %d:\n%s", zone, len(lines), want, out)
		}
		for i, line := range lines {
			f := strings.Fields(line)
			if i >= len(parityfixture.DatePickerDays) {
				if len(f) != 3 || f[1] != "refused" {
					t.Errorf("%s: %s is not a day the picker offers, picked as %q", zone, parityfixture.DatePickerRefused[i-len(parityfixture.DatePickerDays)], line)
				}
				continue
			}
			if len(f) != 3 || f[2] != f[0] {
				t.Errorf("%s: picked %s, shown back as %q", zone, parityfixture.DatePickerDays[i], line)
				continue
			}
			if web != nil && i < len(web) && web[i] != f[1] {
				t.Errorf("%s: %s is %s on the phone and %s in the browser", zone, f[0], f[1], web[i])
			}
			want, err := parityfixture.LocalMidnight(zone, f[0])
			if err != nil {
				t.Fatal(err)
			}
			if f[1] != strconv.FormatInt(want, 10) {
				t.Errorf("%s: %s is %s, local midnight is %d", zone, f[0], f[1], want)
			}
		}
	}
	for _, u := range parityfixture.DatePickerUnshown {
		ms := strconv.FormatInt(u.Millis, 10)
		out, err := exec.Command(bin, "--picker-show", u.Zone, ms).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", u.Zone, err, out)
		}
		if got := strings.TrimSpace(string(out)); got != ms+" "+u.Phone {
			t.Errorf("%s: the Time %s shows as %q, want the nearer end of the range, %s", u.Zone, ms, got, u.Phone)
		}
	}
}

// browserPicks runs the browser's date input over the same days, in a node
// process per zone, and answers each day's Time; nil when node is missing.
func browserPicks(t *testing.T) func(zone string) []string {
	node, err := exec.LookPath("node")
	if err != nil {
		return func(string) []string { return nil }
	}
	dir := t.TempDir()
	driver := `
const fs = require('fs');
globalThis.window = globalThis; globalThis.document = undefined;
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
process.stdout.write(JSON.parse(process.argv[3]).map(d => String(globalThis.__marTestDateInput.parse(d))).join(' '));
`
	if err := os.WriteFile(filepath.Join(dir, "driver.js"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	days, _ := json.Marshal(parityfixture.DatePickerDays)
	runtimeJS, _ := filepath.Abs(filepath.Join("..", "jsserve", "runtime.js"))
	return func(zone string) []string {
		cmd := exec.Command(node, filepath.Join(dir, "driver.js"), runtimeJS, string(days))
		cmd.Env = append(os.Environ(), "TZ="+zone)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: node: %v", zone, err)
		}
		return strings.Fields(string(out))
	}
}
