package parityfixture

import (
	"fmt"
	"time"
)

// DatePickerDays are days picked in a date picker on a device in each of
// DatePickerZones, by both clients' tests (internal/jsserve and
// internal/iosbundle datepicker_test.go). Each comes back as the Date whose
// label was picked, whatever the zone, and shows as that label. They cover
// the first and the last day the pickers offer, a midnight a zone's clock
// skipped (São Paulo 2018-11-04) or repeated (the Azores 2024-10-27), a short
// daylight-saving period (Vienna, April 1945), a day a zone skipped entirely
// (Pacific/Apia 2011-12-30), and a leap day.
var DatePickerDays = []string{
	"1582-10-15", "1916-06-17", "1945-04-02", "1945-04-03", "2011-12-30", "2018-11-04", "2024-02-29",
	"2024-10-27", "2026-11-17", "9999-12-31",
}

// DatePickerRefused are texts neither picker hands back as a Date: days before
// 1582-10-15 (among them the ten the Julian calendar skips and a leap day only
// the Julian calendar has), a day no calendar has, a year past 9999, and an
// emptied field.
var DatePickerRefused = []string{
	"0001-01-01", "1500-02-29", "1582-10-04", "1582-10-10", "1582-10-14", "2023-02-29", "10000-01-01", "",
}

// DatePickerZones are the device time zones the pickers and Date.today are
// run in. Pacific/Pago_Pago (UTC-11) and Pacific/Kiritimati (UTC+14) between
// them put some zone on a different day from UTC at every hour, so a
// Date.today that read the UTC day fails whenever the test runs.
var DatePickerZones = []string{
	"UTC", "America/Sao_Paulo", "Asia/Tokyo", "Atlantic/Azores", "Pacific/Kiritimati", "America/Los_Angeles",
	"Europe/Vienna", "Pacific/Apia", "Pacific/Pago_Pago",
}

// DatePickerEarly are Dates before the first day the pickers offer. Both show
// each as that first day, DatePickerFirst: the phone's DatePicker has no
// empty state, and the browser shows what the phone shows.
var DatePickerEarly = []string{"0000-01-01", "1500-03-01", "1582-10-14"}

const DatePickerFirst = "1582-10-15"

// DayOf is the day count from 1970-01-01 of a YYYY-MM-DD label: the Date a
// picker hands back for it.
func DayOf(label string) (int64, error) {
	d, err := time.Parse("2006-01-02", label)
	if err != nil {
		return 0, err
	}
	return floorDays(d.Unix()), nil
}

// TodayInstants are the instants, in Unix ms, at which both clients' tests
// read Date.today: every half hour and the millisecond before it, over three
// days around a midnight Pacific/Apia skipped, daylight-saving changes in São
// Paulo, the Azores and Vienna, and an ordinary day.
func TodayInstants() []int64 {
	var out []int64
	for _, anchor := range []string{"2011-12-29", "2018-11-03", "2024-10-26", "2026-03-28", "2026-11-17"} {
		a, err := time.Parse("2006-01-02", anchor)
		if err != nil {
			panic(err)
		}
		for step := int64(0); step <= 144; step++ {
			t := a.UnixMilli() + step*1800000
			out = append(out, t-1, t)
		}
	}
	return out
}

// TodayIn is the day count the device's wall clock shows at ms in zone, worked
// out by Go's time package: what Date.today answers there.
func TodayIn(zone string, ms int64) (int64, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return 0, err
	}
	y, m, d := time.UnixMilli(ms).In(loc).Date()
	if y < 1 || y > 9999 {
		return 0, fmt.Errorf("%d is outside the years TodayIn reads", ms)
	}
	return floorDays(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix()), nil
}

func floorDays(secs int64) int64 {
	q := secs / 86400
	if secs%86400 < 0 {
		q--
	}
	return q
}
