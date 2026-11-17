package runtime

import (
	"fmt"
	"time"
)

// VDuration / VTime are the runtime representations of intervals and
// absolute moments. Defined in value.go. Constructed only via the
// unit-named smart constructors below so unit confusion (Int 30 →
// "is that days, seconds, hours?") is impossible at the call site.

func timeBuiltins() map[string]Value {
	return map[string]Value{
		// Duration constructors (unit-named so the call site documents
		// the unit). Each is n of its unit in milliseconds, exactly, or a
		// "Duration out of range" error when that is past Int.
		"timeMillis":  nativeFn(1, makeUnitConstructor("Time.millis", 1)),
		"timeSeconds": nativeFn(1, makeUnitConstructor("Time.seconds", 1000)),
		"timeMinutes": nativeFn(1, makeUnitConstructor("Time.minutes", 60*1000)),
		"timeHours":   nativeFn(1, makeUnitConstructor("Time.hours", 60*60*1000)),
		"timeDays":    nativeFn(1, makeUnitConstructor("Time.days", 24*60*60*1000)),
		"timeWeeks":   nativeFn(1, makeUnitConstructor("Time.weeks", 7*24*60*60*1000)),

		// Whole seconds, toward zero: 1.5s is 1 and -1.5s is -1.
		"timeToSeconds": nativeFn(1, func(args []Value) (Value, error) {
			d, ok := args[0].(VDuration)
			if !ok {
				return nil, fmt.Errorf("Time.toSeconds: expected Duration (got %T)", args[0])
			}
			return VInt{V: d.Millis / 1000}, nil
		}),

		// Absolute time. Time.now reads the wall clock: wrapped as
		// an Effect so it stays out of the pure path. Server-side
		// uses Go's time.Now(); browser/iOS override later if they
		// want their own clock source.
		"timeNow": VEffect{
			Tag: "timeNow",
			Run: func() (Value, error) {
				return VTime{Millis: time.Now().UnixMilli()}, nil
			},
		},

		"timeEvery": nativeFn(2, func(args []Value) (Value, error) {
			if _, ok := args[0].(VDuration); !ok {
				return nil, fmt.Errorf("Time.every: expected Duration (got %T)", args[0])
			}
			// Inert on the backend (no MVU loop). The frontend reconciles this into
			// a real timer that ticks every Duration, delivering the current Time.
			return VEffect{
				Tag: "timeEvery",
				Run: func() (Value, error) { return VUnit{}, nil },
			}, nil
		}),

		// Time.add / Time.sub shift a moment by a duration, both counted in
		// milliseconds, so the shift is exact.
		"timeAdd": nativeFn(2, func(args []Value) (Value, error) {
			t, ok1 := args[0].(VTime)
			d, ok2 := args[1].(VDuration)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("Time.add: expected (Time, Duration) (got %T, %T)", args[0], args[1])
			}
			return VTime{Millis: t.Millis + d.Millis}, nil
		}),
		"timeSub": nativeFn(2, func(args []Value) (Value, error) {
			t, ok1 := args[0].(VTime)
			d, ok2 := args[1].(VDuration)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("Time.sub: expected (Time, Duration) (got %T, %T)", args[0], args[1])
			}
			return VTime{Millis: t.Millis - d.Millis}, nil
		}),

		// Time.diff a b returns the Duration FROM a TO b, to the
		// millisecond. Negative if a is later than b.
		"timeDiff": nativeFn(2, func(args []Value) (Value, error) {
			a, ok1 := args[0].(VTime)
			b, ok2 := args[1].(VTime)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("Time.diff: expected (Time, Time) (got %T, %T)", args[0], args[1])
			}
			ms := b.Millis - a.Millis
			if !inIntRange(ms) {
				return nil, durationOutOfRange("Time.diff")
			}
			return VDuration{Millis: ms}, nil
		}),

		"timeBefore": nativeFn(2, timeCompare("Time.before", func(a, b int64) bool { return a < b })),
		"timeAfter":  nativeFn(2, timeCompare("Time.after", func(a, b int64) bool { return a > b })),

		// Time.toIso → "2026-05-05T13:45:30Z" (RFC 3339 UTC).
		"timeToIso": nativeFn(1, func(args []Value) (Value, error) {
			t, ok := args[0].(VTime)
			if !ok {
				return nil, fmt.Errorf("Time.toIso: expected Time (got %T)", args[0])
			}
			// Three decimal places, like the wire form and like the other
			// two runtimes. RFC3339 alone renders 1700000000123 as
			// "…:20Z", losing the milliseconds the value carries.
			return VString{V: time.UnixMilli(t.Millis).UTC().Format(isoMillis)}, nil
		}),

		// Time.fromIso parses RFC 3339 (with millisecond precision).
		// Returns Maybe Time so callers handle bad input explicitly.
		"timeFromIso": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("Time.fromIso: expected String (got %T)", args[0])
			}
			parsed, err := time.Parse(time.RFC3339, s.V)
			if err != nil {
				// Try with nanosecond precision (RFC 3339 allows it).
				parsed, err = time.Parse(time.RFC3339Nano, s.V)
				if err != nil {
					return VCtor{Tag: "Nothing"}, nil
				}
			}
			return VCtor{Tag: "Just", Args: []Value{VTime{Millis: parsed.UnixMilli()}}}, nil
		}),

		"timeToMillis": nativeFn(1, func(args []Value) (Value, error) {
			t, ok := args[0].(VTime)
			if !ok {
				return nil, fmt.Errorf("Time.toMillis: expected Time (got %T)", args[0])
			}
			return VInt{V: t.Millis}, nil
		}),

		// Time.fromYMD year month day: midnight UTC of that date.
		// Out-of-range components (month=13, day=32) normalize per
		// Go's time.Date convention: the value is *valid* but not
		// what the caller probably typed. We don't reject: same as
		// JavaScript's Date constructor.
		"timeFromYMD": nativeFn(3, func(args []Value) (Value, error) {
			y, ok1 := args[0].(VInt)
			m, ok2 := args[1].(VInt)
			d, ok3 := args[2].(VInt)
			if !ok1 || !ok2 || !ok3 {
				return nil, fmt.Errorf("Time.fromYMD: expected (Int, Int, Int) (got %T, %T, %T)", args[0], args[1], args[2])
			}
			t := time.Date(int(y.V), time.Month(m.V), int(d.V), 0, 0, 0, 0, time.UTC)
			return VTime{Millis: t.UnixMilli()}, nil
		}),

		// Calendar-aware arithmetic. Differs from Time.add because
		// months/years aren't fixed-length: addMonths(2026-01-31, 1)
		// lands on 2026-02-28, the last day of the shorter month, which
		// is what the reference promises.
		//
		// This comment used to say "or 2026-03-03 in Go's normalization",
		// as an aside. That normalization was the behaviour, and it was
		// the behaviour here and in the browser but not on the phone,
		// where Swift's Calendar clamps. See makeCalendarShift.
		//
		// addDays handles DST-free since we store UTC-only; switch to
		// Calendar-aware days if we ever add timezone support.
		"timeAddDays":   nativeFn(2, makeCalendarShift("Time.addDays", 0, 0, 1)),
		"timeAddMonths": nativeFn(2, makeCalendarShift("Time.addMonths", 0, 1, 0)),
		"timeAddYears":  nativeFn(2, makeCalendarShift("Time.addYears", 1, 0, 0)),

		// Component getters. All interpret the moment in UTC.
		// Month is 1-indexed (1 = January) to match human
		// convention; the Go time pkg's time.Month also starts at 1
		// so the conversion is direct.
		"timeYear":   nativeFn(1, makeComponent("Time.year", func(t time.Time) int { return t.Year() })),
		"timeMonth":  nativeFn(1, makeComponent("Time.month", func(t time.Time) int { return int(t.Month()) })),
		"timeDay":    nativeFn(1, makeComponent("Time.day", func(t time.Time) int { return t.Day() })),
		"timeHour":   nativeFn(1, makeComponent("Time.hour", func(t time.Time) int { return t.Hour() })),
		"timeMinute": nativeFn(1, makeComponent("Time.minute", func(t time.Time) int { return t.Minute() })),
		"timeSecond": nativeFn(1, makeComponent("Time.second", func(t time.Time) int { return t.Second() })),
	}
}

func makeComponent(name string, get func(time.Time) int) func([]Value) (Value, error) {
	return func(args []Value) (Value, error) {
		t, ok := args[0].(VTime)
		if !ok {
			return nil, fmt.Errorf("%s: expected Time (got %T)", name, args[0])
		}
		return VInt{V: int64(get(time.UnixMilli(t.Millis).UTC()))}, nil
	}
}

// makeCalendarShift returns a Time -> Int -> Time builder that adds
// `n * (years, months, days)` to the input Time. AddDate normalizes
// : e.g. Jan 31 + 1 month = Mar 3 (not Feb 31), matching Go's stdlib
// + JS Date conventions. If exact "last day of month" semantics ever
// matter, we'd add a separate addMonthsClamped.
func makeCalendarShift(name string, yearMul, monthMul, dayMul int) func([]Value) (Value, error) {
	return func(args []Value) (Value, error) {
		t, ok1 := args[0].(VTime)
		n, ok2 := args[1].(VInt)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("%s: expected (Time, Int) (got %T, %T)", name, args[0], args[1])
		}
		k := int(n.V)
		from := time.UnixMilli(t.Millis).UTC()
		shifted := from.AddDate(yearMul*k, monthMul*k, dayMul*k)
		// Go's AddDate OVERFLOWS a short month: Jan 31 plus one month is
		// March 2, and Feb 29 plus one year is March 1. The reference says
		// the opposite — "adding a month to the 31st lands on the last day
		// of a shorter month" — and the phone already did that, because
		// Swift's Calendar clamps. So the server and the browser said
		// March 2 while the phone said February 29, for a subscription
		// renewal or a due date. Two runtimes against the documented
		// contract and against the third.
		//
		// Only a month or year shift can overflow; adding DAYS is exact
		// and must keep rolling over, which is why the clamp is gated on
		// the day of the month moving.
		if (yearMul != 0 || monthMul != 0) && shifted.Day() != from.Day() {
			// Landed in the following month, so back up to the last day
			// of the one that was asked for.
			shifted = shifted.AddDate(0, 0, -shifted.Day())
		}
		return VTime{Millis: shifted.UnixMilli()}, nil
	}
}

// makeUnitConstructor returns a builder for one of the unit-named
// Duration constructors: n of the unit, unitMs milliseconds each.
func makeUnitConstructor(name string, unitMs int64) func([]Value) (Value, error) {
	return func(args []Value) (Value, error) {
		n, ok := args[0].(VInt)
		if !ok {
			return nil, fmt.Errorf("%s: expected Int (got %T)", name, args[0])
		}
		return durationOf(name, n.V, unitMs)
	}
}

func timeCompare(name string, op func(int64, int64) bool) func([]Value) (Value, error) {
	return func(args []Value) (Value, error) {
		a, ok1 := args[0].(VTime)
		b, ok2 := args[1].(VTime)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("%s: expected two Times (got %T, %T)", name, args[0], args[1])
		}
		return VBool{V: op(a.Millis, b.Millis)}, nil
	}
}
