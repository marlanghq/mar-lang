// Tests for Entity.define's unique-constraint support and the
// surrounding validation (name format, no-duplicate-registration).
//
// Three layers:
//
//  1. Direct calls to entityDefineImpl with crafted record specs.
//     Exercises the spec-parsing path: name validation, column
//     validation, uniques validation, and the duplicate-registration
//     safety net.
//
//  2. The migrator's plan + apply path. Asserts that uniques declared
//     in the spec translate to `CREATE UNIQUE INDEX IF NOT EXISTS`
//     statements with stable, idempotent names.
//
//  3. End-to-end: declare an entity with a composite unique, run
//     migrations, attempt a duplicate insert, observe the SQLite
//     UNIQUE constraint rejection. Proves the constraint is
//     enforced by the database, not just the type system.

package runtime

import (
	"strings"
	"testing"
)

// makeSpec builds a VRecord matching the new Entity.define signature
// shape — `{ name, columns, uniques }` — from raw pieces. Centralized
// so tests stay focused on what they're checking.
func makeSpec(name string, columns []EntityField, uniques [][]string) VRecord {
	colFields := map[string]Value{}
	colOrder := make([]string, 0, len(columns))
	for _, f := range columns {
		col := VColumn{
			SQLType:       f.SQLType,
			NotNull:       f.NotNull,
			Serial:        f.Serial,
			AcceptedCtors: f.AcceptedCtors,
		}
		colFields[f.Name] = col
		colOrder = append(colOrder, f.Name)
	}

	uniquesValue := buildUniquesList(uniques)
	return VRecord{
		Fields: map[string]Value{
			"name":    VString{V: name},
			"columns": VRecord{Fields: colFields, Order: colOrder},
			"uniques": uniquesValue,
		},
		Order: []string{"name", "columns", "uniques"},
	}
}

// buildUniquesList turns a [][]string into the VList-of-VList-of-VString
// shape the runtime sees.
func buildUniquesList(uniques [][]string) VList {
	outer := make([]Value, 0, len(uniques))
	for _, idx := range uniques {
		inner := make([]Value, len(idx))
		for i, c := range idx {
			inner[i] = VString{V: c}
		}
		outer = append(outer, VList{Elements: inner})
	}
	return VList{Elements: outer}
}

// sampleColumns returns a small set of EntityField suitable for
// unique-constraint tests — multiple non-PK columns we can name.
func sampleColumns() []EntityField {
	return []EntityField{
		{Name: "id", SQLType: "INTEGER", NotNull: true, Serial: true},
		{Name: "groupId", SQLType: "INTEGER", NotNull: true},
		{Name: "userId", SQLType: "INTEGER", NotNull: true},
		{Name: "dayKey", SQLType: "TEXT", NotNull: true},
	}
}

// callDefine invokes entityDefineImpl with the constructed spec and
// returns the result. Tests that want to inspect the returned entity
// (or its registration error) use this.
func callDefine(t *testing.T, spec VRecord) (VEntity, error) {
	t.Helper()
	out, err := entityDefineImpl([]Value{spec})
	if err != nil {
		return VEntity{}, err
	}
	ent, ok := out.(VEntity)
	if !ok {
		t.Fatalf("entityDefineImpl returned %T, want VEntity", out)
	}
	return ent, nil
}

// ---------- Uniques: builtin behavior ----------

func TestEntityDefine_AcceptsSingleColumnUnique(t *testing.T) {
	ResetRegisteredEntities()
	t.Cleanup(ResetRegisteredEntities)
	ent, err := callDefine(t, makeSpec("checkins", sampleColumns(), [][]string{{"dayKey"}}))
	if err != nil {
		t.Fatalf("Entity.define: %v", err)
	}
	if len(ent.UniqueIndexes) != 1 || len(ent.UniqueIndexes[0]) != 1 || ent.UniqueIndexes[0][0] != "dayKey" {
		t.Errorf("UniqueIndexes = %v, want [[dayKey]]", ent.UniqueIndexes)
	}
}

func TestEntityDefine_AcceptsCompositeUnique(t *testing.T) {
	ResetRegisteredEntities()
	t.Cleanup(ResetRegisteredEntities)
	ent, err := callDefine(t, makeSpec("checkins", sampleColumns(),
		[][]string{{"groupId", "userId", "dayKey"}}))
	if err != nil {
		t.Fatalf("Entity.define composite: %v", err)
	}
	want := []string{"groupId", "userId", "dayKey"}
	if !equalStringSlice(ent.UniqueIndexes[0], want) {
		t.Errorf("index cols = %v, want %v", ent.UniqueIndexes[0], want)
	}
}

func TestEntityDefine_AcceptsMultipleUniques(t *testing.T) {
	ResetRegisteredEntities()
	t.Cleanup(ResetRegisteredEntities)
	ent, err := callDefine(t, makeSpec("checkins", sampleColumns(),
		[][]string{{"groupId", "userId"}, {"dayKey"}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(ent.UniqueIndexes) != 2 {
		t.Fatalf("expected 2 indexes, got %d", len(ent.UniqueIndexes))
	}
}

func TestEntityDefine_AcceptsEmptyUniques(t *testing.T) {
	ResetRegisteredEntities()
	t.Cleanup(ResetRegisteredEntities)
	ent, err := callDefine(t, makeSpec("checkins", sampleColumns(), [][]string{}))
	if err != nil {
		t.Fatalf("empty uniques should be accepted: %v", err)
	}
	if len(ent.UniqueIndexes) != 0 {
		t.Errorf("expected no indexes, got %v", ent.UniqueIndexes)
	}
}

func TestEntityDefine_RejectsEmptyInnerUnique(t *testing.T) {
	ResetRegisteredEntities()
	t.Cleanup(ResetRegisteredEntities)
	_, err := callDefine(t, makeSpec("checkins", sampleColumns(), [][]string{{}}))
	if err == nil {
		t.Fatal("expected error for unique with zero columns")
	}
	if !strings.Contains(err.Error(), "empty") && !strings.Contains(err.Error(), "at least") {
		t.Errorf("error = %q, want mention of empty/at-least-one", err)
	}
}

func TestEntityDefine_RejectsUnknownColumnInUnique(t *testing.T) {
	ResetRegisteredEntities()
	t.Cleanup(ResetRegisteredEntities)
	_, err := callDefine(t, makeSpec("checkins", sampleColumns(),
		[][]string{{"groupId", "nope"}}))
	if err == nil {
		t.Fatal("expected error for unknown column")
	}
	if !strings.Contains(err.Error(), `"nope"`) {
		t.Errorf("error %q should name the offending column", err)
	}
	if !strings.Contains(err.Error(), "available:") {
		t.Errorf("error %q should list available columns", err)
	}
}

func TestEntityDefine_RejectsDuplicateColumnInSameIndex(t *testing.T) {
	ResetRegisteredEntities()
	t.Cleanup(ResetRegisteredEntities)
	_, err := callDefine(t, makeSpec("checkins", sampleColumns(),
		[][]string{{"groupId", "groupId"}}))
	if err == nil {
		t.Fatal("expected error for duplicate column in same index")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error = %q, want 'duplicate'", err)
	}
}

func TestEntityDefine_DedupsIdenticalUniques(t *testing.T) {
	// Two identical index specs in the same call collapse to one.
	// Mirrors what happens during hot-reload re-evaluation of the
	// same source.
	ResetRegisteredEntities()
	t.Cleanup(ResetRegisteredEntities)
	ent, err := callDefine(t, makeSpec("checkins", sampleColumns(),
		[][]string{{"groupId", "userId"}, {"groupId", "userId"}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(ent.UniqueIndexes) != 1 {
		t.Errorf("expected dedup → 1 index, got %d", len(ent.UniqueIndexes))
	}
}

// ---------- Name validation ----------

func TestEntityDefine_RejectsEmptyName(t *testing.T) {
	ResetRegisteredEntities()
	t.Cleanup(ResetRegisteredEntities)
	_, err := callDefine(t, makeSpec("", sampleColumns(), [][]string{}))
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestEntityDefine_RejectsSqlitePrefix(t *testing.T) {
	ResetRegisteredEntities()
	t.Cleanup(ResetRegisteredEntities)
	_, err := callDefine(t, makeSpec("sqlite_master", sampleColumns(), [][]string{}))
	if err == nil {
		t.Fatal("expected error for sqlite_ prefix")
	}
	if !strings.Contains(err.Error(), "sqlite_") {
		t.Errorf("error %q should mention the reserved prefix", err)
	}
}

func TestEntityDefine_RejectsMarPrefix(t *testing.T) {
	ResetRegisteredEntities()
	t.Cleanup(ResetRegisteredEntities)
	_, err := callDefine(t, makeSpec("_mar_admins", sampleColumns(), [][]string{}))
	if err == nil {
		t.Fatal("expected error for _mar_ prefix")
	}
	if !strings.Contains(err.Error(), "_mar_") {
		t.Errorf("error %q should mention the reserved prefix", err)
	}
}

func TestEntityDefine_RejectsLeadingDigit(t *testing.T) {
	ResetRegisteredEntities()
	t.Cleanup(ResetRegisteredEntities)
	_, err := callDefine(t, makeSpec("1users", sampleColumns(), [][]string{}))
	if err == nil {
		t.Fatal("expected error for name starting with a digit")
	}
}

func TestEntityDefine_RejectsInvalidCharacter(t *testing.T) {
	ResetRegisteredEntities()
	t.Cleanup(ResetRegisteredEntities)
	_, err := callDefine(t, makeSpec("user-table", sampleColumns(), [][]string{}))
	if err == nil {
		t.Fatal("expected error for hyphen in name")
	}
}

func TestEntityDefine_AcceptsValidNames(t *testing.T) {
	for _, name := range []string{
		"users", "Users", "user_table", "_internal_use", "u1", "Users123",
	} {
		t.Run(name, func(t *testing.T) {
			ResetRegisteredEntities()
			t.Cleanup(ResetRegisteredEntities)
			if _, err := callDefine(t, makeSpec(name, sampleColumns(), [][]string{})); err != nil {
				t.Errorf("name %q should be accepted, got %v", name, err)
			}
		})
	}
}

// ---------- Duplicate registration ----------

func TestEntityDefine_RejectsSecondRegistrationOfSameTable(t *testing.T) {
	// Two Entity.define calls with the same name in one program eval
	// = programmer bug. RegisterEntity flags it. (Hot-reload calls
	// ResetForReload before re-evaluating, so reloads still work —
	// that's the difference between "second call in same pass" and
	// "second call after reset".)
	ResetRegisteredEntities()
	t.Cleanup(ResetRegisteredEntities)
	if _, err := callDefine(t, makeSpec("checkins", sampleColumns(), [][]string{})); err != nil {
		t.Fatalf("first call: %v", err)
	}
	_, err := callDefine(t, makeSpec("checkins", sampleColumns(), [][]string{}))
	if err == nil {
		t.Fatal("second Entity.define with same name should error")
	}
	if !strings.Contains(err.Error(), "more than once") {
		t.Errorf("error = %q, want mention of 'more than once'", err)
	}
}

func TestEntityDefine_AllowsRegistrationAfterReset(t *testing.T) {
	// Reset emulates hot-reload. After reset, the same table name
	// can be registered again — this is what makes `mar dev` work.
	ResetRegisteredEntities()
	t.Cleanup(ResetRegisteredEntities)
	if _, err := callDefine(t, makeSpec("checkins", sampleColumns(), [][]string{})); err != nil {
		t.Fatal(err)
	}
	ResetRegisteredEntities()
	if _, err := callDefine(t, makeSpec("checkins", sampleColumns(), [][]string{})); err != nil {
		t.Errorf("second call after reset should succeed: %v", err)
	}
}

// ---------- Spec-record validation ----------

func TestEntityDefine_RejectsMissingNameField(t *testing.T) {
	ResetRegisteredEntities()
	t.Cleanup(ResetRegisteredEntities)
	spec := VRecord{
		Fields: map[string]Value{
			"columns": VRecord{Fields: map[string]Value{}, Order: []string{}},
			"uniques": VList{},
		},
		Order: []string{"columns", "uniques"},
	}
	_, err := entityDefineImpl([]Value{spec})
	if err == nil {
		t.Fatal("expected error when spec is missing `name`")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error %q should mention the missing field", err)
	}
}

func TestEntityDefine_RejectsMissingColumnsField(t *testing.T) {
	ResetRegisteredEntities()
	t.Cleanup(ResetRegisteredEntities)
	spec := VRecord{
		Fields: map[string]Value{
			"name":    VString{V: "x"},
			"uniques": VList{},
		},
		Order: []string{"name", "uniques"},
	}
	_, err := entityDefineImpl([]Value{spec})
	if err == nil {
		t.Fatal("expected error when spec is missing `columns`")
	}
}

func TestEntityDefine_RejectsMissingUniquesField(t *testing.T) {
	ResetRegisteredEntities()
	t.Cleanup(ResetRegisteredEntities)
	spec := VRecord{
		Fields: map[string]Value{
			"name":    VString{V: "x"},
			"columns": VRecord{Fields: map[string]Value{}, Order: []string{}},
		},
		Order: []string{"name", "columns"},
	}
	_, err := entityDefineImpl([]Value{spec})
	if err == nil {
		t.Fatal("expected error when spec is missing `uniques` (use [] for none)")
	}
}

// ---------- SQL emission (unchanged from previous unique work) ----------

func TestUniqueIndexName_StableFormat(t *testing.T) {
	got := uniqueIndexName("checkins", []string{"groupId", "userId", "dayKey"})
	want := "checkins_uq_groupId_userId_dayKey"
	if got != want {
		t.Errorf("index name = %q, want %q", got, want)
	}
}

func TestBuildCreateUniqueIndexSQL_Shape(t *testing.T) {
	sql := buildCreateUniqueIndexSQL("checkins",
		"checkins_uq_groupId_userId_dayKey",
		[]string{"groupId", "userId", "dayKey"})
	for _, want := range []string{
		"CREATE UNIQUE INDEX IF NOT EXISTS",
		`"checkins_uq_groupId_userId_dayKey"`,
		`"checkins"`,
		`"groupId", "userId", "dayKey"`,
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("CREATE UNIQUE INDEX missing %q.\nGot: %s", want, sql)
		}
	}
}

func TestPlanUniqueIndexes_EmitsOnePerDeclaredIndex(t *testing.T) {
	ent := VEntity{Table: "checkins", Fields: sampleColumns()}
	ent.UniqueIndexes = [][]string{
		{"groupId", "userId"},
		{"dayKey"},
	}
	steps := planUniqueIndexes(ent)
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}
	for i, s := range steps {
		if s.Kind != StepCreateUniqueIndex {
			t.Errorf("step[%d] kind = %v, want StepCreateUniqueIndex", i, s.Kind)
		}
		if s.Table != "checkins" {
			t.Errorf("step[%d] table = %q", i, s.Table)
		}
	}
	if steps[0].migrationKind() != "create_unique_index_groupId_userId" {
		t.Errorf("audit suffix = %q", steps[0].migrationKind())
	}
	if steps[1].migrationKind() != "create_unique_index_dayKey" {
		t.Errorf("audit suffix = %q", steps[1].migrationKind())
	}
}

// ---------- End-to-end migrator + SQLite enforcement ----------

func TestMigrate_CreatesUniqueIndex(t *testing.T) {
	db := openTempDB(t)
	ent := VEntity{Table: "checkins", Fields: sampleColumns()}
	ent.UniqueIndexes = [][]string{{"groupId", "userId", "dayKey"}}

	m := NewMigrator(db, []VEntity{ent})
	if _, err := m.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var name string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='index' AND name=?`,
		"checkins_uq_groupId_userId_dayKey",
	).Scan(&name)
	if err != nil {
		t.Fatalf("unique index not found: %v", err)
	}
}

func TestMigrate_UniqueIndex_Idempotent(t *testing.T) {
	db := openTempDB(t)
	ent := VEntity{Table: "checkins", Fields: sampleColumns()}
	ent.UniqueIndexes = [][]string{{"groupId", "userId"}}

	m := NewMigrator(db, []VEntity{ent})
	if _, err := m.Run(); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	if _, err := m.Run(); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`,
		"checkins_uq_groupId_userId",
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 unique index, got %d", count)
	}
}

func TestMigrate_UniqueIndex_EnforcedBySQLite(t *testing.T) {
	db := openTempDB(t)
	ent := VEntity{Table: "checkins", Fields: sampleColumns()}
	ent.UniqueIndexes = [][]string{{"groupId", "userId", "dayKey"}}

	m := NewMigrator(db, []VEntity{ent})
	if _, err := m.Run(); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(
		`INSERT INTO checkins (groupId, userId, dayKey) VALUES (?, ?, ?)`,
		1, 42, "2026-05-20",
	); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	if _, err := db.Exec(
		`INSERT INTO checkins (groupId, userId, dayKey) VALUES (?, ?, ?)`,
		1, 42, "2026-05-21",
	); err != nil {
		t.Fatalf("different-day insert should succeed: %v", err)
	}

	_, err := db.Exec(
		`INSERT INTO checkins (groupId, userId, dayKey) VALUES (?, ?, ?)`,
		1, 42, "2026-05-20",
	)
	if err == nil {
		t.Fatal("duplicate (groupId, userId, dayKey) insert should have been rejected by UNIQUE constraint")
	}
	if !strings.Contains(err.Error(), "UNIQUE") {
		t.Errorf("error doesn't mention UNIQUE: %v", err)
	}
}

func TestMigrate_UniqueIndex_OnExistingTable(t *testing.T) {
	db := openTempDB(t)

	// Boot 1: entity without unique.
	{
		ent := VEntity{Table: "checkins", Fields: sampleColumns()}
		if _, err := NewMigrator(db, []VEntity{ent}).Run(); err != nil {
			t.Fatalf("boot 1: %v", err)
		}
		if _, err := db.Exec(
			`INSERT INTO checkins (groupId, userId, dayKey) VALUES (1, 42, '2026-05-19')`,
		); err != nil {
			t.Fatal(err)
		}
	}

	// Boot 2: entity WITH unique. The index is added; the row survives.
	{
		ent := VEntity{Table: "checkins", Fields: sampleColumns()}
		ent.UniqueIndexes = [][]string{{"groupId", "userId", "dayKey"}}
		summary, err := NewMigrator(db, []VEntity{ent}).Run()
		if err != nil {
			t.Fatalf("boot 2: %v", err)
		}
		var sawIndex bool
		for _, s := range summary.Applied {
			if s.Kind == StepCreateUniqueIndex {
				sawIndex = true
				break
			}
		}
		if !sawIndex {
			t.Errorf("boot 2 should have applied a StepCreateUniqueIndex")
		}
	}

	var note string
	if err := db.QueryRow(
		`SELECT dayKey FROM checkins WHERE groupId = 1 AND userId = 42`,
	).Scan(&note); err != nil {
		t.Fatalf("seed row lost: %v", err)
	}
	if note != "2026-05-19" {
		t.Errorf("seed row corrupted: dayKey = %q", note)
	}
}
