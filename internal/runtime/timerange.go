package runtime

import (
	"fmt"
	"time"
)

// A Time is a millisecond from 0000-01-01T00:00:00.000Z to
// 9999-12-31T23:59:59.999Z in the proleptic Gregorian calendar, UTC, on all
// three runtimes. In that range RFC 3339 writes the year in four digits, so
// the server, the browser and the phone write the same text for a Time and
// read each other's. Every way to build a Time goes through this file, and a
// Time that would leave the range is refused with timeOutOfRange, the same
// sentence on all three (runtime.js and MarCivil.swift).
//
// The calendar arithmetic is done here in int64 rather than with the time
// package: time.Date and AddDate wrap silently for a large argument, and a
// wrapped date can land back inside the range, wrong.
const (
	MinTimeMillis int64 = -62167219200000 // 0000-01-01T00:00:00.000Z
	MaxTimeMillis int64 = 253402300799999 // 9999-12-31T23:59:59.999Z

	msPerDay int64 = 86400000
)

func inTimeRange(ms int64) bool { return ms >= MinTimeMillis && ms <= MaxTimeMillis }

func timeOutOfRange(source string) error {
	return fmt.Errorf("Time out of range: %s is outside the range of a Time (0000-01-01T00:00:00.000Z to 9999-12-31T23:59:59.999Z)", source)
}

// timeOf is the one constructor of a Time: ms, or the refusal naming source.
func timeOf(source string, ms int64) (VTime, error) {
	if !inTimeRange(ms) {
		return VTime{}, timeOutOfRange(source)
	}
	return VTime{Millis: ms}, nil
}

// floorDiv and floorMod for a positive divisor.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b < 0 {
		q--
	}
	return q
}

func floorMod(a, b int64) int64 { return a - floorDiv(a, b)*b }

func isLeapYear(y int64) bool { return y%4 == 0 && (y%100 != 0 || y%400 == 0) }

func daysInMonth(y, m int64) int64 {
	switch m {
	case 2:
		if isLeapYear(y) {
			return 29
		}
		return 28
	case 4, 6, 9, 11:
		return 30
	}
	return 31
}

// daysFromCivil is the number of days from 1970-01-01 to y-m-1, plus d-1.
// m is 1..12; d is any offset from the 1st. Howard Hinnant's days_from_civil,
// exact in int64 for every Mar Int year (|y| < 2^53, so era*146097 < 3.3e18).
func daysFromCivil(y, m, d int64) int64 {
	if m <= 2 {
		y--
	}
	era := floorDiv(y, 400)
	yoe := y - era*400
	doy := (153*((m+9)%12) + 2) / 5
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468 + (d - 1)
}

// civilFromDays is the date of a day count from 1970-01-01.
func civilFromDays(days int64) (y, m, d int64) {
	z := days + 719468
	era := floorDiv(z, 146097)
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d = doy - (153*mp+2)/5 + 1
	if mp < 10 {
		m = mp + 3
	} else {
		m = mp - 9
	}
	y = yoe + era*400
	if m <= 2 {
		y++
	}
	return y, m, d
}

var (
	minTimeDay = floorDiv(MinTimeMillis, msPerDay)
	maxTimeDay = floorDiv(MaxTimeMillis, msPerDay)
)

// timeFromYMD is midnight UTC of y-m-d, with the month and the day carried
// over the way time.Date does it (month 13 is January of the next year, day 0
// the last day of the month before).
func timeFromYMD(y, m, d int64) (VTime, error) {
	source := fmt.Sprintf("Time.fromYMD %d %d %d", y, m, d)
	yy := y + floorDiv(m-1, 12)
	mm := floorMod(m-1, 12) + 1
	days := daysFromCivil(yy, mm, 1) + (d - 1)
	if days < minTimeDay || days > maxTimeDay {
		return VTime{}, timeOutOfRange(source)
	}
	return timeOf(source, days*msPerDay)
}

// timeAddDays moves a Time by n days of 86400000 ms (UTC has no daylight
// saving), keeping the time of day.
func timeAddDays(t VTime, n int64) (VTime, error) {
	source := fmt.Sprintf("Time.addDays %d", n)
	days := floorDiv(t.Millis, msPerDay) + n
	if days < minTimeDay || days > maxTimeDay {
		return VTime{}, timeOutOfRange(source)
	}
	return timeOf(source, days*msPerDay+floorMod(t.Millis, msPerDay))
}

// timeAddMonths moves a Time by n calendar months, keeping the time of day
// and landing on the last day of a shorter month (January 31 plus one month
// is the last day of February).
func timeAddMonths(t VTime, n int64, source string) (VTime, error) {
	days := floorDiv(t.Millis, msPerDay)
	y, m, d := civilFromDays(days)
	total := y*12 + (m - 1) + n
	ny, nm := floorDiv(total, 12), floorMod(total, 12)+1
	if ny < 0 || ny > 9999 {
		return VTime{}, timeOutOfRange(source)
	}
	nd := min(d, daysInMonth(ny, nm))
	return timeOf(source, daysFromCivil(ny, nm, nd)*msPerDay+floorMod(t.Millis, msPerDay))
}

// timeFields is a Time's calendar date and time of day, UTC.
func timeFields(t VTime) (y, m, d, hour, minute, second int64) {
	days := floorDiv(t.Millis, msPerDay)
	rem := t.Millis - days*msPerDay
	y, m, d = civilFromDays(days)
	return y, m, d, rem / 3600000, rem / 60000 % 60, rem / 1000 % 60
}

// formatTimeISO writes a Time as `2006-01-02T15:04:05.000Z`, the wire form
// and Time.toIso. Every Time is in range, so the year has four digits.
func formatTimeISO(t VTime) string {
	return time.UnixMilli(t.Millis).UTC().Format(isoMillis)
}

// parseTimeISO reads the one text form of a Time all three runtimes accept,
// RFC 3339 with its optional parts and nothing else:
//
//	YYYY-MM-DD 'T' HH:MM:SS [ '.' DIGIT+ ] ( 'Z' | ( '+' | '-' ) HH:MM )
//
// 'T' and 'Z' upper case; the day checked against its month; hour 00-23,
// minute and second 00-59, offset hour 00-23 and offset minute 00-59; the
// fraction truncated to milliseconds. ok is false when the text is not in
// that form; ms is then meaningless. A text in that form can still name a
// moment outside the range (9999-12-31T23:59:59-01:00); ms is that moment.
func parseTimeISO(s string) (ms int64, ok bool) {
	i := 0
	num := func(n int) (int64, bool) {
		if i+n > len(s) {
			return 0, false
		}
		var v int64
		for k := 0; k < n; k++ {
			c := s[i+k]
			if c < '0' || c > '9' {
				return 0, false
			}
			v = v*10 + int64(c-'0')
		}
		i += n
		return v, true
	}
	lit := func(c byte) bool {
		if i < len(s) && s[i] == c {
			i++
			return true
		}
		return false
	}
	y, ok1 := num(4)
	if !ok1 || !lit('-') {
		return 0, false
	}
	mo, ok2 := num(2)
	if !ok2 || !lit('-') {
		return 0, false
	}
	d, ok3 := num(2)
	if !ok3 || !lit('T') {
		return 0, false
	}
	h, ok4 := num(2)
	if !ok4 || !lit(':') {
		return 0, false
	}
	mi, ok5 := num(2)
	if !ok5 || !lit(':') {
		return 0, false
	}
	sec, ok6 := num(2)
	if !ok6 {
		return 0, false
	}
	var milli int64
	if lit('.') {
		k := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			if k < 3 {
				milli = milli*10 + int64(s[i]-'0')
			}
			k++
			i++
		}
		if k == 0 {
			return 0, false
		}
		for ; k < 3; k++ {
			milli *= 10
		}
	}
	var offset int64
	switch {
	case lit('Z'):
	case i < len(s) && (s[i] == '+' || s[i] == '-'):
		sign := int64(1)
		if s[i] == '-' {
			sign = -1
		}
		i++
		oh, okH := num(2)
		if !okH || !lit(':') {
			return 0, false
		}
		om, okM := num(2)
		if !okM || oh > 23 || om > 59 {
			return 0, false
		}
		offset = sign * (oh*3600 + om*60)
	default:
		return 0, false
	}
	if i != len(s) || mo < 1 || mo > 12 || d < 1 || d > daysInMonth(y, mo) || h > 23 || mi > 59 || sec > 59 {
		return 0, false
	}
	secs := daysFromCivil(y, mo, d)*86400 + h*3600 + mi*60 + sec - offset
	return secs*1000 + milli, true
}
