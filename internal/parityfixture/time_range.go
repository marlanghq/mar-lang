package parityfixture

// TimeRangeCase is an expression that builds a Time at or past an edge of
// its range (0000-01-01T00:00:00.000Z to 9999-12-31T23:59:59.999Z), with what
// every runtime answers: the Time's text, or the refusal. A Mar error cannot
// be caught, so the conformance corpus cannot hold these; each runtime runs
// TimeRangeCases to completion or failure (internal/project, internal/jsserve
// and internal/iosbundle time_range_test.go).
type TimeRangeCase struct {
	Expr    string // a String expression
	Says    string // the text of the Time, or the refusal up to TimeRangeTail
	Refused bool
}

// TimeRangeTail ends every refusal.
const TimeRangeTail = " is outside the range of a Time (0000-01-01T00:00:00.000Z to 9999-12-31T23:59:59.999Z)"

var TimeRangeCases = []TimeRangeCase{
	{"Time.toIso (Time.add (Time.fromYMD 9999 12 31) (Time.days 1))", "Time out of range: Time.add", true},
	{"Time.toIso (Time.sub (Time.fromYMD 0 1 1) (Time.millis 1))", "Time out of range: Time.sub", true},
	{"Time.toIso (Time.fromYMD 10000 1 1)", "Time out of range: Time.fromYMD 10000 1 1", true},
	{"Time.toIso (Time.fromYMD (-1) 12 31)", "Time out of range: Time.fromYMD -1 12 31", true},
	// Arguments large enough that a calendar library wraps them back
	// inside the range, on a wrong date.
	{"Time.toIso (Time.fromYMD 584554050 1 1)", "Time out of range: Time.fromYMD 584554050 1 1", true},
	{"Time.toIso (Time.addYears (Time.fromYMD 2000 1 1) 5450617530529111)", "Time out of range: Time.addYears 5450617530529111", true},
	{"Time.toIso (Time.addDays (Time.fromYMD 0 1 1) (-1))", "Time out of range: Time.addDays -1", true},
	{"Time.toIso (Time.addMonths (Time.fromYMD 9999 12 1) 1)", "Time out of range: Time.addMonths 1", true},
	{"Time.toIso (Time.addDays (Time.fromYMD 2000 1 1) 9007199254740991)", "Time out of range: Time.addDays 9007199254740991", true},
	// The edges themselves, and a carry that lands back inside.
	{"Time.toIso (Time.add (Time.fromYMD 9999 12 31) (Time.millis 86399999))", "9999-12-31T23:59:59.999Z", false},
	{"Time.toIso (Time.fromYMD 10000 0 1)", "9999-12-01T00:00:00.000Z", false},
	{"Time.toIso (Time.addYears (Time.fromYMD 9998 2 28) 1)", "9999-02-28T00:00:00.000Z", false},
}
