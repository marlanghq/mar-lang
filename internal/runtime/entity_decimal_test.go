package runtime

import (
	"math/big"
	"strings"
	"testing"
)

func TestEntityDecimalColumn(t *testing.T) {
	f := EntityField{Name: "amount", SQLType: "DECIMAL", DecimalScale: 2}

	// Storage form is the integer coefficient at the column scale.
	coef, err := decimalCoefficientAtScale(VDecimal{Coef: big.NewInt(1234), Scale: 2}, f.DecimalScale)
	if err != nil || coef != 1234 {
		t.Fatalf("12.34 at scale 2: got %d, %v", coef, err)
	}
	// Coarser values rescale exactly: 12.3 → 1230, 12 → 1200.
	coef, err = decimalCoefficientAtScale(VDecimal{Coef: big.NewInt(123), Scale: 1}, 2)
	if err != nil || coef != 1230 {
		t.Fatalf("12.3 at scale 2: got %d, %v", coef, err)
	}
	coef, err = decimalCoefficientAtScale(VDecimal{Coef: big.NewInt(12), Scale: 0}, 2)
	if err != nil || coef != 1200 {
		t.Fatalf("12 at scale 2: got %d, %v", coef, err)
	}
	// Trailing zeros at a finer scale are still exact: 12.340 → 1234.
	coef, err = decimalCoefficientAtScale(VDecimal{Coef: big.NewInt(12340), Scale: 3}, 2)
	if err != nil || coef != 1234 {
		t.Fatalf("12.340 at scale 2: got %d, %v", coef, err)
	}
	// A finer NONZERO digit aborts — no implicit rounding at the DB door.
	_, err = decimalCoefficientAtScale(VDecimal{Coef: big.NewInt(12345), Scale: 3}, 2)
	if err == nil || !strings.Contains(err.Error(), "round explicitly") {
		t.Fatalf("12.345 at scale 2 should error with explicit-rounding hint, got %v", err)
	}

	// Reads rehydrate to a Decimal at the column scale.
	v := mustDecodeColumn(t, f, int64(1234))
	d, ok := v.(VDecimal)
	if !ok || decString(d) != "12.34" {
		t.Fatalf("decode: got %#v", v)
	}

	// DDL stores the column as INTEGER.
	e := VEntity{Table: "expenses", Fields: []EntityField{
		{Name: "id", SQLType: "INTEGER", Serial: true, NotNull: true},
		f,
	}}
	sql := buildCreateTableSQL(e)
	if !strings.Contains(sql, "amount INTEGER") {
		t.Fatalf("DDL should store DECIMAL as INTEGER: %s", sql)
	}
}
