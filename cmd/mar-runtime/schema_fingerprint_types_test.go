package main

import (
	"database/sql"
	"path/filepath"
	"testing"

	"mar/internal/admin"
	"mar/internal/runtime"
)

// sqlite_master is the STORAGE view, and the storage type erases the one thing
// a restore most needs to compare. Measured before this: two databases
// differing only in `Entity.decimal 2` against `Entity.decimal 3` fingerprinted
// IDENTICALLY (201761987fe79c0e both), because `amount INTEGER` is the SQL
// either way.
//
// So a bundle taken at scale 2 restored into a binary running scale 3 while
// restore-db printed "Schema: matches the bundle", and every stored
// coefficient came to mean a different amount: R$ 12,34 read back as
// R$ 1,234. That is the same defect the migrator's declared-type catalog
// closes, arriving by the one path that does not go through the migrator.

func fingerprintOfDecimalScale(t *testing.T, scale int) string {
	t.Helper()
	runtime.ResetForReload()
	runtime.SetDBPath(filepath.Join(t.TempDir(), "p.db"))
	t.Cleanup(func() { runtime.SetDBPath("") })
	db, err := runtime.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	ent := runtime.VEntity{Table: "items", Fields: []runtime.EntityField{
		{Name: "id", SQLType: "INTEGER", NotNull: true, Serial: true},
		{Name: "amount", SQLType: "DECIMAL", NotNull: true, DecimalScale: scale},
	}}
	if _, err := runtime.NewMigrator(db, []runtime.VEntity{ent}).Run(); err != nil {
		t.Fatal(err)
	}
	fp, err := admin.SchemaFingerprint(db)
	if err != nil {
		t.Fatal(err)
	}
	return fp
}

func TestTheFingerprintSeesADeclaredTypeTheSQLHides(t *testing.T) {
	if fingerprintOfDecimalScale(t, 2) == fingerprintOfDecimalScale(t, 3) {
		t.Fatal("scale 2 and scale 3 are the same INTEGER column and different money")
	}
}

func TestTheFingerprintIsStableForTheSameDeclaration(t *testing.T) {
	// The other half: a fingerprint that changed on its own would refuse
	// every restore.
	// Named rather than compared inline: two identical calls either side of a
	// `!=` is the shape of a real bug, so a reader (and staticcheck's SA4000)
	// cannot tell this deliberate determinism check from one. The names say
	// which is which, and a failure can print both.
	first := fingerprintOfDecimalScale(t, 2)
	second := fingerprintOfDecimalScale(t, 2)
	if first != second {
		t.Fatalf("the same declaration must fingerprint the same, twice: %v then %v", first, second)
	}
}

func TestADatabaseWithNoCatalogStillFingerprints(t *testing.T) {
	// One written before the catalog existed. It contributes nothing rather
	// than failing, so an old bundle keeps the fingerprint it always had.
	runtime.ResetForReload()
	runtime.SetDBPath(filepath.Join(t.TempDir(), "old.db"))
	t.Cleanup(func() { runtime.SetDBPath("") })
	db, err := runtime.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	ent := runtime.VEntity{Table: "items", Fields: []runtime.EntityField{
		{Name: "id", SQLType: "INTEGER", NotNull: true, Serial: true},
	}}
	if _, err := runtime.NewMigrator(db, []runtime.VEntity{ent}).Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE _mar_column_types`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.SchemaFingerprint(db); err != nil {
		t.Fatalf("a database older than the catalog must still fingerprint: %v", err)
	}
}

func TestTheCatalogNameIsTheSameOnBothSides(t *testing.T) {
	// The table name is duplicated in internal/admin rather than imported,
	// because of the package graph. This is what holds the two together.
	runtime.ResetForReload()
	runtime.SetDBPath(filepath.Join(t.TempDir(), "p.db"))
	t.Cleanup(func() { runtime.SetDBPath("") })
	db, err := runtime.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	ent := runtime.VEntity{Table: "items", Fields: []runtime.EntityField{
		{Name: "id", SQLType: "INTEGER", NotNull: true, Serial: true},
	}}
	if _, err := runtime.NewMigrator(db, []runtime.VEntity{ent}).Run(); err != nil {
		t.Fatal(err)
	}
	var n int
	err = db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name = ?`,
		admin.DeclaredTypeCatalogName(),
	).Scan(&n)
	if err != nil || n != 1 {
		t.Fatalf("internal/admin looks for %q and the migrator writes no such table",
			admin.DeclaredTypeCatalogName())
	}
	_ = sql.ErrNoRows
}
