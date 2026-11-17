package iosbundle

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mar/internal/parityfixture"
)

// The phone's date picker on a device in each zone: a picked day comes back
// as the Date with that label, the Date the browser hands back for it
// (internal/jsserve/datepicker_test.go holds the browser to the same days),
// and shows as the label; a day the picker does not offer is refused; a Date
// before the first offered day shows as that day. Date.today is the day the
// device's wall clock shows, as Go's time package works it out.
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
	runIn := func(env []string, args ...string) []string {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args[:2], err, out)
		}
		return strings.Split(strings.TrimSpace(string(out)), "\n")
	}
	run := func(args ...string) []string { return runIn(nil, args...) }
	var early []string
	for _, label := range parityfixture.DatePickerEarly {
		d, err := parityfixture.DayOf(label)
		if err != nil {
			t.Fatal(err)
		}
		early = append(early, strconv.FormatInt(d, 10))
	}
	var instants []string
	for _, ms := range parityfixture.TodayInstants() {
		instants = append(instants, strconv.FormatInt(ms, 10))
	}
	labels := append(append([]string{}, parityfixture.DatePickerDays...), parityfixture.DatePickerRefused...)
	for _, zone := range parityfixture.DatePickerZones {
		lines := run(append([]string{"--picker", zone}, labels...)...)
		if len(lines) != len(labels) {
			t.Fatalf("%s: %d lines, want %d:\n%s", zone, len(lines), len(labels), strings.Join(lines, "\n"))
		}
		for i, line := range lines {
			f := strings.Fields(line)
			if i >= len(parityfixture.DatePickerDays) {
				if len(f) < 2 || f[len(f)-2] != "refused" {
					t.Errorf("%s: %q is not a day the picker offers, picked as %q", zone, labels[i], line)
				}
				continue
			}
			want, err := parityfixture.DayOf(labels[i])
			if err != nil {
				t.Fatal(err)
			}
			if len(f) != 3 || f[0] != labels[i] || f[1] != strconv.FormatInt(want, 10) || f[2] != labels[i] {
				t.Errorf("%s: picked %s, got %q; want the day %d shown as %s", zone, labels[i], line, want, labels[i])
			}
		}
		for i, line := range run(append([]string{"--picker-show", zone}, early...)...) {
			if want := early[i] + " " + parityfixture.DatePickerFirst; line != want {
				t.Errorf("%s: the Date %s shows as %q, want %q", zone, early[i], line, want)
			}
		}
		// Date.today as a program runs it, on a device in this zone.
		now := strings.Fields(runIn([]string{"TZ=" + zone}, "--today-now")[0])
		if len(now) != 3 {
			t.Fatalf("%s: --today-now printed %q", zone, now)
		}
		before, _ := strconv.ParseInt(now[0], 10, 64)
		after, _ := strconv.ParseInt(now[2], 10, 64)
		lo, err := parityfixture.TodayIn(zone, before)
		if err != nil {
			t.Fatal(err)
		}
		hi, _ := parityfixture.TodayIn(zone, after)
		if now[1] != strconv.FormatInt(lo, 10) && now[1] != strconv.FormatInt(hi, 10) {
			t.Errorf("%s: Date.today is the day %s, the device's clock shows %d", zone, now[1], lo)
		}
		for _, line := range run(append([]string{"--today", zone}, instants...)...) {
			f := strings.Fields(line)
			ms, _ := strconv.ParseInt(f[0], 10, 64)
			want, err := parityfixture.TodayIn(zone, ms)
			if err != nil {
				t.Fatal(err)
			}
			if len(f) != 2 || f[1] != strconv.FormatInt(want, 10) {
				t.Errorf("%s: at %s Date.today is %q, the device's clock shows the day %d", zone, f[0], line, want)
			}
		}
	}

	// A phone set to another calendar or locale: the picker still reads its
	// labels in the Gregorian calendar, and starts the week where the locale
	// does.
	for _, locale := range []string{"ja_JP@calendar=japanese", "th_TH@calendar=buddhist"} {
		args := append([]string{"--picker", "Asia/Tokyo"}, parityfixture.DatePickerDays...)
		lines := run(append(args, "-AppleLocale", locale)...)
		for i, line := range lines {
			want, _ := parityfixture.DayOf(parityfixture.DatePickerDays[i])
			if f := strings.Fields(line); len(f) != 3 || f[1] != strconv.FormatInt(want, 10) || f[2] != f[0] {
				t.Errorf("%s: picked %s, got %q; want the day %d", locale, parityfixture.DatePickerDays[i], line, want)
			}
		}
	}
	for locale, want := range map[string]string{"en_US": "1", "de_DE": "2", "pt_BR": "1"} {
		if got := run("--picker-week", "UTC", "-AppleLocale", locale)[0]; got != want {
			t.Errorf("%s: the picker's week starts on day %s, the locale's on %s", locale, got, want)
		}
	}
}

// The SwiftUI view is not compiled by the Swift core tests (it imports
// SwiftUI), so what it wires is read from its source: the picker runs in
// MarCivil.pickerZone, hands both the calendar and the zone to the native
// DatePicker, and crosses the day through pickerDate and pickedDay, the
// functions the driver tests above.
func TestThePhoneDatePickerViewWiresTheTestedFunctions(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("template", "Sources", "MarRenderer.swift"))
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(src), "private struct MarUIDatePicker")
	if start < 0 {
		t.Fatal("MarUIDatePicker not found in MarRenderer.swift")
	}
	body := string(src[start:])
	if end := strings.Index(body[1:], "\nprivate "); end > 0 {
		body = body[:end+1]
	}
	for _, want := range []string{
		"let tz = MarCivil.pickerZone()",
		"MarCivil.pickerDate(days, tz)",
		"MarCivil.pickedDay(newDate, tz)",
		"in: MarCivil.pickerRange(tz)",
		`.environment(\.calendar, MarCivil.pickerCalendar(tz))`,
		`.environment(\.timeZone, tz)`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("MarUIDatePicker no longer has %q", want)
		}
	}
}
