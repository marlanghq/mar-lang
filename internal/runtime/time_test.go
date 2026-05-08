package runtime

import (
	"testing"
	"time"
)

// helper — pull a builtin from the registry by name.
func timeBuiltin(t *testing.T, name string) Value {
	t.Helper()
	v, ok := timeBuiltins()[name]
	if !ok {
		t.Fatalf("builtin not registered: %s", name)
	}
	return v
}

// callN invokes a native VFn with the given args. Returns the result
// or fails the test on error.
func callN(t *testing.T, name string, args ...Value) Value {
	t.Helper()
	fn, ok := timeBuiltin(t, name).(VFn)
	if !ok {
		t.Fatalf("%s: not a function (got %T)", name, timeBuiltin(t, name))
	}
	if fn.Native == nil {
		t.Fatalf("%s: not a native function", name)
	}
	out, err := fn.Native(args)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

// toMillis convenience for tests — UTC midnight of the given Y-M-D.
func ymdMillis(y, m, d int) int64 {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC).UnixMilli()
}

// Time.fromYMD ----------------------------------------------------------

func TestTimeFromYMD_BasicDate(t *testing.T) {
	got := callN(t, "timeFromYMD", VInt{V: 2026}, VInt{V: 5}, VInt{V: 5})
	want := ymdMillis(2026, 5, 5)
	if got.(VTime).Millis != want {
		t.Errorf("fromYMD(2026, 5, 5) = %d, want %d", got.(VTime).Millis, want)
	}
}

func TestTimeFromYMD_Epoch(t *testing.T) {
	got := callN(t, "timeFromYMD", VInt{V: 1970}, VInt{V: 1}, VInt{V: 1})
	if got.(VTime).Millis != 0 {
		t.Errorf("fromYMD(1970, 1, 1) = %d, want 0", got.(VTime).Millis)
	}
}

func TestTimeFromYMD_Normalizes(t *testing.T) {
	// Out-of-range month rolls forward — same as Go's time.Date and
	// JavaScript's Date constructor. Document the behavior rather
	// than reject the input; users who want strict validation can
	// check with their own arithmetic before calling.
	got := callN(t, "timeFromYMD", VInt{V: 2026}, VInt{V: 13}, VInt{V: 1})
	want := ymdMillis(2027, 1, 1)
	if got.(VTime).Millis != want {
		t.Errorf("fromYMD(2026, 13, 1) = %d, want %d (normalized to 2027-01-01)", got.(VTime).Millis, want)
	}
}

// Time.addDays ----------------------------------------------------------

func TestTimeAddDays_Forward(t *testing.T) {
	base := VTime{Millis: ymdMillis(2026, 5, 5)}
	got := callN(t, "timeAddDays", base, VInt{V: 10})
	want := ymdMillis(2026, 5, 15)
	if got.(VTime).Millis != want {
		t.Errorf("addDays(2026-05-05, 10) = %d, want %d", got.(VTime).Millis, want)
	}
}

func TestTimeAddDays_Negative(t *testing.T) {
	base := VTime{Millis: ymdMillis(2026, 5, 5)}
	got := callN(t, "timeAddDays", base, VInt{V: -7})
	want := ymdMillis(2026, 4, 28)
	if got.(VTime).Millis != want {
		t.Errorf("addDays(2026-05-05, -7) = %d, want %d", got.(VTime).Millis, want)
	}
}

func TestTimeAddDays_AcrossYear(t *testing.T) {
	base := VTime{Millis: ymdMillis(2026, 12, 28)}
	got := callN(t, "timeAddDays", base, VInt{V: 7})
	want := ymdMillis(2027, 1, 4)
	if got.(VTime).Millis != want {
		t.Errorf("addDays across year = %d, want %d", got.(VTime).Millis, want)
	}
}

// Time.addMonths --------------------------------------------------------

func TestTimeAddMonths_Forward(t *testing.T) {
	base := VTime{Millis: ymdMillis(2026, 5, 5)}
	got := callN(t, "timeAddMonths", base, VInt{V: 3})
	want := ymdMillis(2026, 8, 5)
	if got.(VTime).Millis != want {
		t.Errorf("addMonths(2026-05-05, 3) = %d, want %d", got.(VTime).Millis, want)
	}
}

func TestTimeAddMonths_AcrossYear(t *testing.T) {
	base := VTime{Millis: ymdMillis(2026, 11, 15)}
	got := callN(t, "timeAddMonths", base, VInt{V: 4})
	want := ymdMillis(2027, 3, 15)
	if got.(VTime).Millis != want {
		t.Errorf("addMonths across year = %d, want %d", got.(VTime).Millis, want)
	}
}

func TestTimeAddMonths_Jan31RollsForward(t *testing.T) {
	// Jan 31 + 1 month overflows February → normalizes to early
	// March (Mar 3 in non-leap years, Mar 2 in leap years) per Go's
	// time.AddDate convention. Documents the trade-off: caller who
	// needs "last day of month" semantics has to handle it
	// explicitly.
	base := VTime{Millis: ymdMillis(2026, 1, 31)}
	got := callN(t, "timeAddMonths", base, VInt{V: 1})
	// 2026 is non-leap, so Jan 31 + 1 month = Mar 3 (Feb 28 + 3 days
	// of overflow).
	want := ymdMillis(2026, 3, 3)
	if got.(VTime).Millis != want {
		t.Errorf("addMonths(Jan 31, 1) = %d, want Mar 3 (%d)", got.(VTime).Millis, want)
	}
}

// Time.addYears ---------------------------------------------------------

func TestTimeAddYears_Forward(t *testing.T) {
	base := VTime{Millis: ymdMillis(2026, 5, 5)}
	got := callN(t, "timeAddYears", base, VInt{V: 2})
	want := ymdMillis(2028, 5, 5)
	if got.(VTime).Millis != want {
		t.Errorf("addYears(2026-05-05, 2) = %d, want %d", got.(VTime).Millis, want)
	}
}

func TestTimeAddYears_LeapDay(t *testing.T) {
	// Feb 29 + 1 year normalizes to Mar 1 in non-leap years (same
	// rationale as Jan 31 + 1 month).
	base := VTime{Millis: ymdMillis(2024, 2, 29)} // 2024 is leap
	got := callN(t, "timeAddYears", base, VInt{V: 1})
	want := ymdMillis(2025, 3, 1)
	if got.(VTime).Millis != want {
		t.Errorf("addYears(Feb 29 of leap, 1) = %d, want Mar 1 (%d)", got.(VTime).Millis, want)
	}
}

// Type errors -----------------------------------------------------------

func TestTimeAddDays_RejectsNonTime(t *testing.T) {
	fn := timeBuiltin(t, "timeAddDays").(VFn)
	_, err := fn.Native([]Value{VString{V: "not a time"}, VInt{V: 1}})
	if err == nil {
		t.Fatal("addDays accepted a non-Time first arg")
	}
}

func TestTimeFromYMD_RejectsNonInt(t *testing.T) {
	fn := timeBuiltin(t, "timeFromYMD").(VFn)
	_, err := fn.Native([]Value{VString{V: "2026"}, VInt{V: 5}, VInt{V: 5}})
	if err == nil {
		t.Fatal("fromYMD accepted a non-Int year")
	}
}

// Component getters ----------------------------------------------------

func TestTimeComponents_DateOnly(t *testing.T) {
	// fromYMD(2026, 5, 5) → midnight UTC. Components extract back
	// the same Y/M/D, with H/M/S = 0.
	base := callN(t, "timeFromYMD", VInt{V: 2026}, VInt{V: 5}, VInt{V: 5}).(VTime)
	cases := []struct {
		name string
		want int64
	}{
		{"timeYear", 2026},
		{"timeMonth", 5},
		{"timeDay", 5},
		{"timeHour", 0},
		{"timeMinute", 0},
		{"timeSecond", 0},
	}
	for _, c := range cases {
		got := callN(t, c.name, base).(VInt).V
		if got != c.want {
			t.Errorf("%s = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestTimeComponents_FullDateTime(t *testing.T) {
	// 2026-05-05T13:45:30Z exact moment.
	target := time.Date(2026, 5, 5, 13, 45, 30, 0, time.UTC).UnixMilli()
	base := VTime{Millis: target}
	cases := []struct {
		name string
		want int64
	}{
		{"timeYear", 2026},
		{"timeMonth", 5},
		{"timeDay", 5},
		{"timeHour", 13},
		{"timeMinute", 45},
		{"timeSecond", 30},
	}
	for _, c := range cases {
		got := callN(t, c.name, base).(VInt).V
		if got != c.want {
			t.Errorf("%s = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestTimeMonth_OneIndexed(t *testing.T) {
	// January should be 1 (matching human convention), not 0
	// (JavaScript's quirk). Verify the boundary explicitly so a
	// regression in the JS port would be caught.
	jan := VTime{Millis: ymdMillis(2026, 1, 15)}
	dec := VTime{Millis: ymdMillis(2026, 12, 15)}
	if got := callN(t, "timeMonth", jan).(VInt).V; got != 1 {
		t.Errorf("January Time.month = %d, want 1", got)
	}
	if got := callN(t, "timeMonth", dec).(VInt).V; got != 12 {
		t.Errorf("December Time.month = %d, want 12", got)
	}
}

func TestTimeYear_RejectsNonTime(t *testing.T) {
	fn := timeBuiltin(t, "timeYear").(VFn)
	_, err := fn.Native([]Value{VInt{V: 12345}})
	if err == nil {
		t.Fatal("Time.year accepted a non-Time arg")
	}
}
