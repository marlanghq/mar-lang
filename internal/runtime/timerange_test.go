package runtime

import (
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The hand-written calendar agrees with Go's time package, which is proleptic
// Gregorian in UTC, for every kind of moment in the range: both edges, the
// Gregorian cutover the phone's Foundation gets wrong, leap days, and a
// uniform sample over the ten thousand years.
func TestTheCalendarIsProlepticGregorianAcrossTheRange(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	samples := []int64{MinTimeMillis, MaxTimeMillis, 0, -1, -14826672000000, -12219292800001, -12219292800000, 951782400000}
	for i := 0; i < 200000; i++ {
		samples = append(samples, MinTimeMillis+rng.Int63n(MaxTimeMillis-MinTimeMillis+1))
	}
	for _, ms := range samples {
		ref := time.UnixMilli(ms).UTC()
		y, m, d, hour, minute, second := timeFields(VTime{Millis: ms})
		if y != int64(ref.Year()) || m != int64(ref.Month()) || d != int64(ref.Day()) ||
			hour != int64(ref.Hour()) || minute != int64(ref.Minute()) || second != int64(ref.Second()) {
			t.Fatalf("%d: fields %d-%d-%d %d:%d:%d, time package %s", ms, y, m, d, hour, minute, second, ref)
		}
		text := formatTimeISO(VTime{Millis: ms})
		back, ok := parseTimeISO(text)
		if !ok || back != ms {
			t.Fatalf("%d: %s reads back as %d (%v)", ms, text, back, ok)
		}
		days := floorDiv(ms, msPerDay)
		if got := daysFromCivil(y, m, d); got != days {
			t.Fatalf("%d: daysFromCivil(%d,%d,%d) = %d, want %d", ms, y, m, d, got, days)
		}
	}
}

// fromYMD carries the month and the day like time.Date for every argument
// time.Date handles without wrapping, and the shifts land where AddDate plus
// the last-day-of-the-month clamp lands.
func TestFromYMDAndTheShiftsMatchTheTimePackage(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < 50000; i++ {
		y := rng.Int63n(10002) - 1
		m := rng.Int63n(30) - 8
		d := rng.Int63n(80) - 20
		ref := time.Date(int(y), time.Month(m), int(d), 0, 0, 0, 0, time.UTC).UnixMilli()
		got, err := timeFromYMD(y, m, d)
		if inTimeRange(ref) != (err == nil) || (err == nil && got.Millis != ref) {
			t.Fatalf("fromYMD %d %d %d: got %v %v, time package %d", y, m, d, got, err, ref)
		}
	}
	for i := 0; i < 50000; i++ {
		from := MinTimeMillis + rng.Int63n(MaxTimeMillis-MinTimeMillis+1)
		n := rng.Int63n(2400) - 1200
		start := time.UnixMilli(from).UTC()
		ref := start.AddDate(0, int(n), 0)
		if ref.Day() != start.Day() {
			ref = ref.AddDate(0, 0, -ref.Day())
		}
		got, err := timeAddMonths(VTime{Millis: from}, n, "Time.addMonths")
		if inTimeRange(ref.UnixMilli()) != (err == nil) || (err == nil && got.Millis != ref.UnixMilli()) {
			t.Fatalf("addMonths %s %d: got %v %v, want %s", start, n, got, err, ref)
		}
		days, err := timeAddDays(VTime{Millis: from}, n*37)
		refDays := start.AddDate(0, 0, int(n*37)).UnixMilli()
		if inTimeRange(refDays) != (err == nil) || (err == nil && days.Millis != refDays) {
			t.Fatalf("addDays %s %d: got %v %v, want %d", start, n*37, days, err, refDays)
		}
	}
}

// Leaving the range is refused, including by arguments large enough that
// time.Date and AddDate wrap and land back inside it.
func TestATimeOutsideTheRangeIsRefused(t *testing.T) {
	refused := []struct {
		name string
		do   func() (VTime, error)
		says string
	}{
		{"one day past the last", func() (VTime, error) { return timeFromYMD(10000, 1, 1) }, "Time.fromYMD 10000 1 1"},
		{"one day before the first", func() (VTime, error) { return timeFromYMD(-1, 12, 31) }, "Time.fromYMD -1 12 31"},
		{"a year time.Date wraps into the range", func() (VTime, error) { return timeFromYMD(584554050, 1, 1) }, "Time.fromYMD 584554050"},
		{"a day count that wraps", func() (VTime, error) { return timeFromYMD(1970, 1, 9007199254740991) }, "Time.fromYMD 1970 1 9007199254740991"},
		{"addYears past 9999", func() (VTime, error) { return timeAddMonths(VTime{Millis: MaxTimeMillis}, 12, "Time.addYears 1") }, "Time.addYears 1"},
		{"addYears AddDate wraps", func() (VTime, error) {
			return timeAddMonths(VTime{}, 5450617530529111*12, "Time.addYears 5450617530529111")
		}, "Time.addYears 5450617530529111"},
		{"addDays past the first", func() (VTime, error) { return timeAddDays(VTime{Millis: MinTimeMillis}, -1) }, "Time.addDays -1"},
		{"addDays by the largest Int", func() (VTime, error) { return timeAddDays(VTime{}, 9007199254740991) }, "Time.addDays 9007199254740991"},
		{"one millisecond past the last", func() (VTime, error) { return timeOf("Time.add", MaxTimeMillis+1) }, "Time.add"},
	}
	for _, c := range refused {
		_, err := c.do()
		if err == nil || !strings.Contains(err.Error(), "Time out of range: "+c.says) ||
			!strings.Contains(err.Error(), "is outside the range of a Time (0000-01-01T00:00:00.000Z to 9999-12-31T23:59:59.999Z)") {
			t.Errorf("%s: want the range refusal naming %q, got %v", c.name, c.says, err)
		}
	}
	for _, edge := range []func() (VTime, error){
		func() (VTime, error) { return timeFromYMD(0, 1, 1) },
		func() (VTime, error) { return timeFromYMD(9999, 12, 31) },
		func() (VTime, error) { return timeFromYMD(10000, 0, 1) },
		func() (VTime, error) { return timeFromYMD(-1, 13, 1) },
		func() (VTime, error) { return timeAddDays(VTime{Millis: MaxTimeMillis - msPerDay}, 1) },
	} {
		if _, err := edge(); err != nil {
			t.Errorf("an edge inside the range was refused: %v", err)
		}
	}
}

// One text form, on all three runtimes: RFC 3339 with its optional parts and
// nothing else.
func TestTheTextFormOfATime(t *testing.T) {
	accepted := map[string]int64{
		"2023-11-14T22:13:20Z":           1700000000000,
		"2023-11-14T22:13:20.123Z":       1700000000123,
		"2023-11-14T22:13:20.123456789Z": 1700000000123,
		"2023-11-14T22:13:20.1Z":         1700000000100,
		"2023-11-14T19:13:20-03:00":      1700000000000,
		"2023-11-15T03:43:20+05:30":      1700000000000,
		"1969-12-31T23:59:59.9995Z":      -1,
		"0000-02-29T00:00:00Z":           -62162121600000,
		"0000-01-01T00:00:00Z":           MinTimeMillis,
		"9999-12-31T23:59:59.999Z":       MaxTimeMillis,
		"9999-12-31T23:59:59-01:00":      MaxTimeMillis + 3600001 - 1000,
	}
	for s, want := range accepted {
		got, ok := parseTimeISO(s)
		if !ok || got != want {
			t.Errorf("%s: got %d %v, want %d", s, got, ok, want)
		}
	}
	for _, s := range []string{
		"2023-11-14", "2023-11-14T22:13:20", "2023-11-14T22:13Z", "2023-11-14T1:13:20Z",
		"2023-11-14T22:13:20,5Z", "2023-11-14t22:13:20z", "2023-11-14T22:13:20.Z",
		"2023-02-29T00:00:00Z", "2023-11-31T00:00:00Z", "2023-11-14T24:00:00Z", "2023-11-14T22:60:00Z",
		"2023-11-14T22:13:60Z", "2023-11-14T22:13:20+24:00", "2023-11-14T22:13:20+0300",
		"+010000-01-01T00:00:00Z", "10000-01-01T00:00:00Z", "-0001-12-31T00:00:00Z",
		" 2023-11-14T22:13:20Z", "2023-11-14T22:13:20Z ", "２０２３-11-14T22:13:20Z",
	} {
		if _, ok := parseTimeISO(s); ok {
			t.Errorf("%q was accepted as a Time", s)
		}
	}
}

// Every Time in this package is built by timeOf in timerange.go, so none can
// skip the range: a Time literal anywhere else, keyed or not, is refused here.
var timeLiteral = regexp.MustCompile(`VTime\{[^}]`)

func TestEveryTimeIsBuiltInsideTheRange(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "timerange.go" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if m := timeLiteral.FindString(string(b)); m != "" {
			t.Errorf("%s builds a Time directly (%s); build it with timeOf so the range holds", f, m)
		}
	}
}
