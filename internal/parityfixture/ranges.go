package parityfixture

// RangeCase is an expression that builds a Time or a Date at or past an edge
// of its range, with what every runtime answers: the value's text, or the
// refusal. A Mar error cannot be caught, so the conformance corpus cannot hold
// these; each runtime runs RangeCases to completion or failure
// (internal/project, internal/jsserve and internal/iosbundle range_test.go).
type RangeCase struct {
	Expr    string // a String expression
	Says    string // the text of the value, or the refusal up to Tail
	Refused bool
	Tail    string // what ends a refusal: TimeRangeTail or DateRangeTail
}

// TimeRangeTail ends every refusal of a Time, DateRangeTail every refusal of a
// Date.
const (
	TimeRangeTail = " is outside the range of a Time (0000-01-01T00:00:00.000Z to 9999-12-31T23:59:59.999Z)"
	DateRangeTail = " is outside the range of a Date (0000-01-01 to 9999-12-31)"
)

func timeCase(expr, says string, refused bool) RangeCase {
	return RangeCase{expr, says, refused, TimeRangeTail}
}

func dateCase(expr, says string, refused bool) RangeCase {
	return RangeCase{expr, says, refused, DateRangeTail}
}

var RangeCases = []RangeCase{
	timeCase("Time.toIso (Time.add (Time.fromYMD 9999 12 31) (Time.days 1))", "Time out of range: Time.add", true),
	timeCase("Time.toIso (Time.sub (Time.fromYMD 0 1 1) (Time.millis 1))", "Time out of range: Time.sub", true),
	timeCase("Time.toIso (Time.fromYMD 10000 1 1)", "Time out of range: Time.fromYMD 10000 1 1", true),
	timeCase("Time.toIso (Time.fromYMD (-1) 12 31)", "Time out of range: Time.fromYMD -1 12 31", true),
	// Arguments large enough that a calendar library wraps them back
	// inside the range, on a wrong date.
	timeCase("Time.toIso (Time.fromYMD 584554050 1 1)", "Time out of range: Time.fromYMD 584554050 1 1", true),
	timeCase("Time.toIso (Time.addYears (Time.fromYMD 2000 1 1) 5450617530529111)", "Time out of range: Time.addYears 5450617530529111", true),
	timeCase("Time.toIso (Time.addDays (Time.fromYMD 0 1 1) (-1))", "Time out of range: Time.addDays -1", true),
	timeCase("Time.toIso (Time.addMonths (Time.fromYMD 9999 12 1) 1)", "Time out of range: Time.addMonths 1", true),
	timeCase("Time.toIso (Time.addDays (Time.fromYMD 2000 1 1) 9007199254740991)", "Time out of range: Time.addDays 9007199254740991", true),
	// The edges themselves, and a carry that lands back inside.
	timeCase("Time.toIso (Time.add (Time.fromYMD 9999 12 31) (Time.millis 86399999))", "9999-12-31T23:59:59.999Z", false),
	timeCase("Time.toIso (Time.fromYMD 10000 0 1)", "9999-12-01T00:00:00.000Z", false),
	timeCase("Time.toIso (Time.addYears (Time.fromYMD 9998 2 28) 1)", "9999-02-28T00:00:00.000Z", false),

	dateCase("Date.toIso (Date.addDays (Date.fromYMD 9999 12 31) 1)", "Date out of range: Date.addDays 1", true),
	dateCase("Date.toIso (Date.addDays (Date.fromYMD 0 1 1) (-1))", "Date out of range: Date.addDays -1", true),
	dateCase("Date.toIso (Date.fromYMD 10000 1 1)", "Date out of range: Date.fromYMD 10000 1 1", true),
	dateCase("Date.toIso (Date.fromYMD (-1) 12 31)", "Date out of range: Date.fromYMD -1 12 31", true),
	dateCase("Date.toIso (Date.fromYMD 584554050 1 1)", "Date out of range: Date.fromYMD 584554050 1 1", true),
	dateCase("Date.toIso (Date.fromYMD 2000 1 9007199254740991)", "Date out of range: Date.fromYMD 2000 1 9007199254740991", true),
	dateCase("Date.toIso (Date.addYears (Date.fromYMD 2000 1 1) 5450617530529111)", "Date out of range: Date.addYears 5450617530529111", true),
	dateCase("Date.toIso (Date.addMonths (Date.fromYMD 9999 12 1) 1)", "Date out of range: Date.addMonths 1", true),
	dateCase("Date.toIso (Date.addMonths (Date.fromYMD 0 1 31) (-1))", "Date out of range: Date.addMonths -1", true),
	dateCase("Date.toIso (Date.addDays (Date.fromYMD 2000 1 1) 9007199254740991)", "Date out of range: Date.addDays 9007199254740991", true),
	dateCase("Date.toIso (Date.fromYMD 9999 12 31)", "9999-12-31", false),
	dateCase("Date.toIso (Date.fromYMD 10000 0 1)", "9999-12-01", false),
	dateCase("Date.toIso (Date.addYears (Date.fromYMD 9998 2 28) 1)", "9999-02-28", false),
	dateCase("Date.toIso (Date.addDays (Date.fromYMD 0 1 2) (-1))", "0000-01-01", false),
}
