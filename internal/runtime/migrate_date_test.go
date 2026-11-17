package runtime

import (
	"database/sql"
	"strings"
	"testing"
)

// A Date is stored as its day count and a Time as milliseconds, both INTEGER.
// A Time at midnight UTC and the Date of that day are the same day
// (Date.toTime, Date.fromTime), so a column moves between the two when no row
// would lose anything, and blocks when one would.

func dayEntity(sqlType string, notNull bool) VEntity {
	return VEntity{Table: "days", Fields: []EntityField{
		{Name: "id", SQLType: "INTEGER", NotNull: true, Serial: true},
		{Name: "day", SQLType: sqlType, NotNull: notNull},
	}}
}

func storedDays(t *testing.T, db *sql.DB) []sql.NullInt64 {
	t.Helper()
	rows, err := db.Query(`SELECT day FROM days ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []sql.NullInt64
	for rows.Next() {
		var n sql.NullInt64
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func TestATimeColumnOfMidnightsBecomesADateColumn(t *testing.T) {
	_, db := migrateProbe(t, dayEntity("TIMESTAMP", false))
	// 2025-11-17, 1969-12-31 and the first day of a Time, all at midnight
	// UTC, and a row with no day at all.
	if _, err := db.Exec(`INSERT INTO days (day) VALUES (1763337600000), (-86400000), (-62167219200000), (NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := rerun(t, db, dayEntity("DATE", false)); err != nil {
		t.Fatalf("every row is a midnight, so the move is exact: %v", err)
	}
	got := storedDays(t, db)
	want := []int64{20409, -1, -719528}
	for i, w := range want {
		if !got[i].Valid || got[i].Int64 != w {
			t.Fatalf("row %d: want the day %d, got %v", i, w, got)
		}
	}
	if got[3].Valid {
		t.Fatalf("a row with no day keeps none, got %v", got[3])
	}
	v, err := decodeColumn("days", dayEntity("DATE", false).Fields[1], got[0].Int64)
	if err != nil || v != (VDate{Days: 20409}) {
		t.Fatalf("the row reads back as the Date 2025-11-17, got %v, %v", v, err)
	}
}

func TestATimeColumnWithATimeOfDayDoesNotBecomeADate(t *testing.T) {
	_, db := migrateProbe(t, dayEntity("TIMESTAMP", true))
	// Midnight, and 2025-11-17T22:30Z: which day that is depends on where
	// its person was.
	if _, err := db.Exec(`INSERT INTO days (day) VALUES (1763337600000), (1763418600000)`); err != nil {
		t.Fatal(err)
	}
	err := rerun(t, db, dayEntity("DATE", true))
	if err == nil {
		t.Fatal("a Time with a time of day has no one day to become")
	}
	for _, want := range []string{"days.day", "Time", "Date", "1 row has a time of day", "UPDATE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message must mention %q, got: %v", want, err)
		}
	}
	got := storedDays(t, db)
	if got[0].Int64 != 1763337600000 || got[1].Int64 != 1763418600000 {
		t.Fatalf("a blocked migration changed the data: %v", got)
	}
}

func TestADateColumnBecomesTheTimeAtItsMidnight(t *testing.T) {
	_, db := migrateProbe(t, dayEntity("DATE", true))
	if _, err := db.Exec(`INSERT INTO days (day) VALUES (20409), (-1)`); err != nil {
		t.Fatal(err)
	}
	if err := rerun(t, db, dayEntity("TIMESTAMP", true)); err != nil {
		t.Fatalf("every day has a midnight: %v", err)
	}
	got := storedDays(t, db)
	if got[0].Int64 != 1763337600000 || got[1].Int64 != -86400000 {
		t.Fatalf("want the midnights 1763337600000 and -86400000, got %v", got)
	}
}

func TestADateColumnHoldsOnlyDatesInRange(t *testing.T) {
	f := dayEntity("DATE", true).Fields[1]
	if _, err := decodeColumn("days", f, int64(2932897)); err == nil ||
		!strings.Contains(err.Error(), "Date out of range: 2932897 days from the database") {
		t.Fatalf("10000-01-01 is past the last Date, got %v", err)
	}
	if v, err := marValueToSQLForField(&f, VDate{Days: 20409}); err != nil || v != int64(20409) {
		t.Fatalf("a Date is stored as its day count, got %v, %v", v, err)
	}
	if _, err := marValueToSQLForField(&f, VTime{Millis: 1763337600000}); err == nil {
		t.Fatal("a Time written to a DATE column would be read back as some other day")
	}
}
