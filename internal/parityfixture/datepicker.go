package parityfixture

import (
	"fmt"
	"time"
)

// DatePickerDays are days picked in a date picker in each of
// DatePickerZones, by both clients' tests (internal/jsserve and
// internal/iosbundle datepicker_test.go). Each comes back as the day that was
// picked, as the same Time on both clients, and in UTC as the midnight the
// server computes for it. They cover the first and the last day the pickers
// offer, a daylight-saving gap at midnight (São Paulo 2018-11-04), a midnight
// that happens twice (the Azores 2024-10-27), offsets that change later in
// the day (the Azores 1916 and 1945), a daylight-saving period of ten days
// stepped through day by day (Vienna, April 1945), and a leap day.
var DatePickerDays = []string{
	"1582-10-15", "1916-06-17", "1945-04-01", "1945-04-02", "1945-04-03", "1945-04-04", "1945-04-21",
	"2018-11-04", "2024-02-29", "2024-10-27", "9999-12-31",
}

// DatePickerRefused are days neither picker hands back as a Time: days before
// 1582-10-15 (among them the ten the Julian calendar skips and a leap day only
// the Julian calendar has), a day no calendar has, and a year past 9999.
var DatePickerRefused = []string{
	"0001-01-01", "1500-02-29", "1582-10-04", "1582-10-10", "1582-10-14", "2023-02-29", "10000-01-01",
}

var DatePickerZones = []string{
	"UTC", "America/Sao_Paulo", "Asia/Tokyo", "Atlantic/Azores", "Pacific/Kiritimati", "America/Los_Angeles",
	"Europe/Vienna",
}

// DatePickerUnshown are Times on a local day the pickers do not offer. The
// browser shows each as an empty field; the phone's DatePicker, which has no
// empty state, shows the nearer end of the range, Phone.
var DatePickerUnshown = []struct {
	Zone   string
	Millis int64
	Phone  string
}{
	{"UTC", -62167219200000, "1582-10-15"},
	{"UTC", -12219336000000, "1582-10-15"},
	{"Asia/Tokyo", 253402286400000, "9999-12-31"},
}

// LocalMidnight is the Time, in Unix ms, a picker hands back for day
// (YYYY-MM-DD) in zone: the earliest instant whose wall clock reads 00:00 on
// that day, or, when a daylight-saving gap skips 00:00, the first instant of
// the day. It is worked out from Go's time package over every offset the zone
// has from 18 hours before the day's UTC midnight to 18 hours after, so the
// clients' two probes are checked against all of them.
func LocalMidnight(zone, day string) (int64, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return 0, err
	}
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		return 0, err
	}
	midnight := d.Unix()
	type stretch struct{ from, to, offset int64 }
	var stretches []stretch
	for t, end := midnight-18*3600, midnight+18*3600; t < end; {
		_, offset := time.Unix(t, 0).In(loc).Zone()
		_, next := time.Unix(t, 0).In(loc).ZoneBounds()
		to := end
		if !next.IsZero() && next.Unix() < end {
			to = next.Unix()
		}
		stretches = append(stretches, stretch{t, to, int64(offset)})
		t = to
	}
	for _, s := range stretches {
		if at := midnight - s.offset; at >= s.from && at < s.to {
			return at * 1000, nil
		}
	}
	for _, s := range stretches {
		if s.from+s.offset >= midnight {
			return s.from * 1000, nil
		}
	}
	return 0, fmt.Errorf("%s has no instant on %s", zone, day)
}
