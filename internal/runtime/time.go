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
				return timeOf("Time.now", time.Now().UnixMilli())
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
			return timeOf("Time.add", t.Millis+d.Millis)
		}),
		"timeSub": nativeFn(2, func(args []Value) (Value, error) {
			t, ok1 := args[0].(VTime)
			d, ok2 := args[1].(VDuration)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("Time.sub: expected (Time, Duration) (got %T, %T)", args[0], args[1])
			}
			return timeOf("Time.sub", t.Millis-d.Millis)
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
			return VString{V: formatTimeISO(t)}, nil
		}),

		// Time.fromIso reads the text form parseTimeISO accepts. Nothing
		// when the text is not in that form or names a moment outside the
		// range of a Time.
		"timeFromIso": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("Time.fromIso: expected String (got %T)", args[0])
			}
			ms, ok := parseTimeISO(s.V)
			if !ok {
				return VCtor{Tag: "Nothing"}, nil
			}
			t, err := timeOf(s.V, ms)
			if err != nil {
				return VCtor{Tag: "Nothing"}, nil
			}
			return VCtor{Tag: "Just", Args: []Value{t}}, nil
		}),

		"timeToMillis": nativeFn(1, func(args []Value) (Value, error) {
			t, ok := args[0].(VTime)
			if !ok {
				return nil, fmt.Errorf("Time.toMillis: expected Time (got %T)", args[0])
			}
			return VInt{V: t.Millis}, nil
		}),

		// Time.fromYMD year month day: midnight UTC of that date, with the
		// month and the day carried over (month 13 is January of the next
		// year, day 0 the last day of the month before).
		"timeFromYMD": nativeFn(3, func(args []Value) (Value, error) {
			y, ok1 := args[0].(VInt)
			m, ok2 := args[1].(VInt)
			d, ok3 := args[2].(VInt)
			if !ok1 || !ok2 || !ok3 {
				return nil, fmt.Errorf("Time.fromYMD: expected (Int, Int, Int) (got %T, %T, %T)", args[0], args[1], args[2])
			}
			return timeFromYMD(y.V, m.V, d.V)
		}),

		// Calendar arithmetic. Days are exact; months and years land on the
		// last day of a shorter month: addMonths(2026-01-31, 1) is
		// 2026-02-28 and addYears(2024-02-29, 1) is 2025-02-28.
		"timeAddDays": nativeFn(2, makeCalendarShift("Time.addDays", func(t VTime, n int64) (VTime, error) {
			return timeAddDays(t, n)
		})),
		"timeAddMonths": nativeFn(2, makeCalendarShift("Time.addMonths", func(t VTime, n int64) (VTime, error) {
			return timeAddMonths(t, n, fmt.Sprintf("Time.addMonths %d", n))
		})),
		"timeAddYears": nativeFn(2, makeCalendarShift("Time.addYears", func(t VTime, n int64) (VTime, error) {
			return timeAddMonths(t, n*12, fmt.Sprintf("Time.addYears %d", n))
		})),

		// Component getters, in UTC. Month is 1 for January.
		"timeYear":   nativeFn(1, makeComponent("Time.year", 0)),
		"timeMonth":  nativeFn(1, makeComponent("Time.month", 1)),
		"timeDay":    nativeFn(1, makeComponent("Time.day", 2)),
		"timeHour":   nativeFn(1, makeComponent("Time.hour", 3)),
		"timeMinute": nativeFn(1, makeComponent("Time.minute", 4)),
		"timeSecond": nativeFn(1, makeComponent("Time.second", 5)),
	}
}

// makeComponent reads field i of timeFields: year, month, day, hour, minute,
// second.
func makeComponent(name string, i int) func([]Value) (Value, error) {
	return func(args []Value) (Value, error) {
		t, ok := args[0].(VTime)
		if !ok {
			return nil, fmt.Errorf("%s: expected Time (got %T)", name, args[0])
		}
		y, m, d, hour, minute, second := timeFields(t)
		return VInt{V: [6]int64{y, m, d, hour, minute, second}[i]}, nil
	}
}

// makeCalendarShift checks the arguments of a Time -> Int -> Time shift and
// hands them to shift.
func makeCalendarShift(name string, shift func(VTime, int64) (VTime, error)) func([]Value) (Value, error) {
	return func(args []Value) (Value, error) {
		t, ok1 := args[0].(VTime)
		n, ok2 := args[1].(VInt)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("%s: expected (Time, Int) (got %T, %T)", name, args[0], args[1])
		}
		return shift(t, n.V)
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
