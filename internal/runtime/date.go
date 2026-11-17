package runtime

import (
	"fmt"
)

// A Date is a day of the calendar, with no time of day and no time zone: the
// number of days from 1970-01-01. Its range is the days of a Time, 0000-01-01
// to 9999-12-31 in the proleptic Gregorian calendar, so Date.toTime and
// Date.fromTime are total. Every way to build a Date goes through dateOf,
// and a Date that would leave the range is refused with dateOutOfRange, the
// same sentence on all three runtimes (runtime.js and MarCivil.swift).
//
// A Date is a label, read the same everywhere: 2026-11-17 is 2026-11-17 on
// the server, in the browser and on the phone, whatever their time zones.
// The device's time zone enters a program in one place, Date.today, and the
// server has no Date.today: it has no person whose day it would be.

func dateOutOfRange(source string) error {
	return fmt.Errorf("Date out of range: %s is outside the range of a Date (0000-01-01 to 9999-12-31)", source)
}

// dateOf is the one constructor of a Date: days, or the refusal naming
// source.
func dateOf(source string, days int64) (VDate, error) {
	if days < minTimeDay || days > maxTimeDay {
		return VDate{}, dateOutOfRange(source)
	}
	return VDate{Days: days}, nil
}

// dateFromYMD is the day y-m-d, with the month and the day carried over as
// Time.fromYMD carries them (month 13 is January of the next year, day 0 the
// last day of the month before).
func dateFromYMD(y, m, d int64) (VDate, error) {
	source := fmt.Sprintf("Date.fromYMD %d %d %d", y, m, d)
	yy := y + floorDiv(m-1, 12)
	mm := floorMod(m-1, 12) + 1
	return dateOf(source, daysFromCivil(yy, mm, 1)+(d-1))
}

// dateAddMonths moves a Date by n calendar months, landing on the last day
// of a shorter month (January 31 plus one month is the last day of
// February).
func dateAddMonths(dt VDate, n int64, source string) (VDate, error) {
	y, m, d := civilFromDays(dt.Days)
	total := y*12 + (m - 1) + n
	ny, nm := floorDiv(total, 12), floorMod(total, 12)+1
	if ny < 0 || ny > 9999 {
		return VDate{}, dateOutOfRange(source)
	}
	return dateOf(source, daysFromCivil(ny, nm, min(d, daysInMonth(ny, nm))))
}

// formatDateISO writes a Date as `2026-11-17`. Every Date is in range, so the
// year has four digits.
func formatDateISO(dt VDate) string {
	y, m, d := civilFromDays(dt.Days)
	return fmt.Sprintf("%04d-%02d-%02d", y, m, d)
}

// parseDateISO reads the one text form of a Date the three runtimes accept,
// `YYYY-MM-DD` and nothing else: four digits of year, two of month and two of
// day, the day checked against its month. Four digits of year keep every
// such text inside the range.
func parseDateISO(s string) (days int64, ok bool) {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return 0, false
	}
	num := func(from, to int) (int64, bool) {
		var v int64
		for i := from; i < to; i++ {
			if s[i] < '0' || s[i] > '9' {
				return 0, false
			}
			v = v*10 + int64(s[i]-'0')
		}
		return v, true
	}
	y, okY := num(0, 4)
	m, okM := num(5, 7)
	d, okD := num(8, 10)
	if !okY || !okM || !okD || m < 1 || m > 12 || d < 1 || d > daysInMonth(y, m) {
		return 0, false
	}
	return daysFromCivil(y, m, d), true
}

// dateWeekday is 1 for Monday through 7 for Sunday, as ISO 8601 counts them.
// 1970-01-01 was a Thursday.
func dateWeekday(dt VDate) int64 { return floorMod(dt.Days+3, 7) + 1 }

func dateBuiltins() map[string]Value {
	return map[string]Value{
		"dateFromYMD": nativeFn(3, func(args []Value) (Value, error) {
			y, ok1 := args[0].(VInt)
			m, ok2 := args[1].(VInt)
			d, ok3 := args[2].(VInt)
			if !ok1 || !ok2 || !ok3 {
				return nil, fmt.Errorf("Date.fromYMD: expected (Int, Int, Int) (got %T, %T, %T)", args[0], args[1], args[2])
			}
			return dateFromYMD(y.V, m.V, d.V)
		}),

		"dateYear":    nativeFn(1, makeDateField("Date.year", 0)),
		"dateMonth":   nativeFn(1, makeDateField("Date.month", 1)),
		"dateDay":     nativeFn(1, makeDateField("Date.day", 2)),
		"dateWeekday": nativeFn(1, makeDateField("Date.weekday", 3)),

		"dateAddDays": nativeFn(2, makeDateShift("Date.addDays", func(dt VDate, n int64) (VDate, error) {
			return dateOf(fmt.Sprintf("Date.addDays %d", n), dt.Days+n)
		})),
		"dateAddMonths": nativeFn(2, makeDateShift("Date.addMonths", func(dt VDate, n int64) (VDate, error) {
			return dateAddMonths(dt, n, fmt.Sprintf("Date.addMonths %d", n))
		})),
		"dateAddYears": nativeFn(2, makeDateShift("Date.addYears", func(dt VDate, n int64) (VDate, error) {
			return dateAddMonths(dt, n*12, fmt.Sprintf("Date.addYears %d", n))
		})),

		// Date.diffDays a b is the number of days FROM a TO b: negative
		// when a is the later day.
		"dateDiffDays": nativeFn(2, func(args []Value) (Value, error) {
			a, b, err := twoDates("Date.diffDays", args)
			if err != nil {
				return nil, err
			}
			return VInt{V: b.Days - a.Days}, nil
		}),
		"dateBefore": nativeFn(2, func(args []Value) (Value, error) {
			a, b, err := twoDates("Date.before", args)
			if err != nil {
				return nil, err
			}
			return VBool{V: a.Days < b.Days}, nil
		}),
		"dateAfter": nativeFn(2, func(args []Value) (Value, error) {
			a, b, err := twoDates("Date.after", args)
			if err != nil {
				return nil, err
			}
			return VBool{V: a.Days > b.Days}, nil
		}),

		"dateToIso": nativeFn(1, func(args []Value) (Value, error) {
			dt, ok := args[0].(VDate)
			if !ok {
				return nil, fmt.Errorf("Date.toIso: expected Date (got %T)", args[0])
			}
			return VString{V: formatDateISO(dt)}, nil
		}),
		// Date.fromIso reads the text form parseDateISO accepts, and gives
		// Nothing for anything else.
		"dateFromIso": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("Date.fromIso: expected String (got %T)", args[0])
			}
			days, ok := parseDateISO(s.V)
			if !ok {
				return VCtor{Tag: "Nothing"}, nil
			}
			dt, err := dateOf(s.V, days)
			if err != nil {
				return VCtor{Tag: "Nothing"}, nil
			}
			return VCtor{Tag: "Just", Args: []Value{dt}}, nil
		}),

		// Date.fromTime is the day a Time falls on in UTC, the day
		// Time.year, Time.month and Time.day read. Date.toTime is midnight
		// UTC of a Date, the Time Time.fromYMD gives for it.
		"dateFromTime": nativeFn(1, func(args []Value) (Value, error) {
			t, ok := args[0].(VTime)
			if !ok {
				return nil, fmt.Errorf("Date.fromTime: expected Time (got %T)", args[0])
			}
			return dateOf("Date.fromTime", floorDiv(t.Millis, msPerDay))
		}),
		"dateToTime": nativeFn(1, func(args []Value) (Value, error) {
			dt, ok := args[0].(VDate)
			if !ok {
				return nil, fmt.Errorf("Date.toTime: expected Date (got %T)", args[0])
			}
			return timeOf("Date.toTime", dt.Days*msPerDay)
		}),

		// Date.today is the device's date, read in the device's time zone.
		// The type checker keeps it out of backend code; this binding exists
		// so the name resolves on the server like every other builtin.
		"dateToday": VEffect{
			Tag: "dateToday",
			Run: func() (Value, error) {
				return nil, fmt.Errorf("Date.today is only available on the device: the server has no time zone to read a date in")
			},
		},
	}
}

// makeDateField reads field i of a Date: year, month, day, weekday.
func makeDateField(name string, i int) func([]Value) (Value, error) {
	return func(args []Value) (Value, error) {
		dt, ok := args[0].(VDate)
		if !ok {
			return nil, fmt.Errorf("%s: expected Date (got %T)", name, args[0])
		}
		y, m, d := civilFromDays(dt.Days)
		return VInt{V: [4]int64{y, m, d, dateWeekday(dt)}[i]}, nil
	}
}

// makeDateShift checks the arguments of a Date -> Int -> Date shift and hands
// them to shift.
func makeDateShift(name string, shift func(VDate, int64) (VDate, error)) func([]Value) (Value, error) {
	return func(args []Value) (Value, error) {
		dt, ok1 := args[0].(VDate)
		n, ok2 := args[1].(VInt)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("%s: expected (Date, Int) (got %T, %T)", name, args[0], args[1])
		}
		return shift(dt, n.V)
	}
}

func twoDates(name string, args []Value) (VDate, VDate, error) {
	a, ok1 := args[0].(VDate)
	b, ok2 := args[1].(VDate)
	if !ok1 || !ok2 {
		return VDate{}, VDate{}, fmt.Errorf("%s: expected two Dates (got %T, %T)", name, args[0], args[1])
	}
	return a, b, nil
}
