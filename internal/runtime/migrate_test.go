package runtime

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// openTempDB returns a fresh SQLite handle at a per-test temp path.
// Caller closes via t.Cleanup. The temp file is removed when the test
// suite ends.
func openTempDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("db.Ping: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// Wire SetDBPath so currentDBPath() / displayPath in error
	// messages works during tests.
	SetDBPath(path)
	t.Cleanup(func() { SetDBPath("") })
	return db
}

// notesEntity returns a fresh Entity declaration for the test "notes"
// table. Defining a helper keeps each test focused on what it
// actually exercises.
func notesEntity() VEntity {
	return VEntity{
		Table: "notes",
		Fields: []EntityField{
			{Name: "id", SQLType: "INTEGER", NotNull: true, Serial: true},
			{Name: "body", SQLType: "TEXT", NotNull: true},
		},
	}
}

func notesWithArchived() VEntity {
	return VEntity{
		Table: "notes",
		Fields: []EntityField{
			{Name: "id", SQLType: "INTEGER", NotNull: true, Serial: true},
			{Name: "body", SQLType: "TEXT", NotNull: true},
			{Name: "archived", SQLType: "BOOLEAN"}, // optional → NULL allowed
		},
	}
}

// ----- Scenarios from docs/migrations.md "Testing plan" -----

// (1) Fresh install: empty DB → boot → all entities created → audit
// table has N create_table rows.
func TestMigrate_FreshInstall(t *testing.T) {
	db := openTempDB(t)
	m := NewMigrator(db, []VEntity{notesEntity()})
	summary, err := m.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !summary.HasChanges() {
		t.Fatalf("expected at least 1 applied step on fresh DB")
	}
	if got, want := len(summary.Applied), 1; got != want {
		t.Errorf("applied steps: got %d, want %d", got, want)
	}
	if summary.Applied[0].Kind != StepCreateTable {
		t.Errorf("first step kind: got %v, want StepCreateTable", summary.Applied[0].Kind)
	}
	// Audit table must exist and contain one create_table row.
	var auditCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM _mar_schema_migrations WHERE migration_kind = 'create_table'`,
	).Scan(&auditCount); err != nil {
		t.Fatalf("audit query: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("audit rows: got %d, want 1", auditCount)
	}
}

// (2) No-op restart: boot again → no new audit rows → fast.
func TestMigrate_NoOpRestart(t *testing.T) {
	db := openTempDB(t)
	m := NewMigrator(db, []VEntity{notesEntity()})
	if _, err := m.Run(); err != nil {
		t.Fatalf("first run: %v", err)
	}
	summary, err := m.Run()
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if summary.HasChanges() {
		t.Errorf("expected zero changes on re-run, got %d", len(summary.Applied))
	}
	var auditCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM _mar_schema_migrations`).Scan(&auditCount); err != nil {
		t.Fatalf("audit query: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("audit row count drifted: got %d, want 1", auditCount)
	}
}

// (3) Add nullable field: edit entity, restart → add_column row,
// table has new column, existing rows have NULL.
func TestMigrate_AddNullableColumn(t *testing.T) {
	db := openTempDB(t)
	if _, err := NewMigrator(db, []VEntity{notesEntity()}).Run(); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO notes (body) VALUES ('hello')`); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	summary, err := NewMigrator(db, []VEntity{notesWithArchived()}).Run()
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if got, want := len(summary.Applied), 1; got != want {
		t.Fatalf("applied: got %d, want %d", got, want)
	}
	if summary.Applied[0].Kind != StepAddColumn {
		t.Errorf("step kind: got %v, want StepAddColumn", summary.Applied[0].Kind)
	}
	// Existing row should still be there with archived = NULL.
	var body string
	var archived sql.NullBool
	if err := db.QueryRow(`SELECT body, archived FROM notes`).Scan(&body, &archived); err != nil {
		t.Fatalf("post-migration query: %v", err)
	}
	if body != "hello" {
		t.Errorf("body lost: got %q, want %q", body, "hello")
	}
	if archived.Valid {
		t.Errorf("archived should be NULL, got %v", archived.Bool)
	}
}

// (4) Type change: Int → String → BLOCKED with type-changed error.
func TestMigrate_TypeChange_Blocked(t *testing.T) {
	db := openTempDB(t)
	// Initial: id INTEGER, body INTEGER (yes, weird, but we're
	// testing the diff logic; the TYPE change is what matters).
	original := VEntity{
		Table: "notes",
		Fields: []EntityField{
			{Name: "id", SQLType: "INTEGER", NotNull: true, Serial: true},
			{Name: "body", SQLType: "INTEGER", NotNull: true},
		},
	}
	if _, err := NewMigrator(db, []VEntity{original}).Run(); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	// Now declare body as TEXT.
	changed := notesEntity() // body: TEXT NOT NULL
	_, err := NewMigrator(db, []VEntity{changed}).Run()
	if err == nil {
		t.Fatalf("expected blocked-error on type change; got nil")
	}
	if !strings.Contains(err.Error(), "column type changed") {
		t.Errorf("error doesn't look like a type-change block:\n%s", err.Error())
	}
}

// (5) Drift on extra column: manually add a column to the live
// table; restart with an entity that doesn't declare it. Boot should
// succeed.
func TestMigrate_ExtraColumn_KeptWithWarning(t *testing.T) {
	db := openTempDB(t)
	if _, err := NewMigrator(db, []VEntity{notesEntity()}).Run(); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	// External tool added a column the framework doesn't know about.
	if _, err := db.Exec(`ALTER TABLE notes ADD COLUMN secret TEXT`); err != nil {
		t.Fatalf("manual ALTER: %v", err)
	}
	summary, err := NewMigrator(db, []VEntity{notesEntity()}).Run()
	if err != nil {
		t.Fatalf("re-migrate after manual ALTER: %v", err)
	}
	if summary.HasChanges() {
		t.Errorf("unexpected applied changes: %v", summary.Applied)
	}
	// The orphan-column case isn't surfaced today (we only warn
	// about whole-table orphans). This is documented as known
	// scope in the spec.
}

// (10) History audit: SELECT * FROM _mar_schema_migrations ORDER BY
// id returns every applied step in order.
func TestMigrate_AuditHistory(t *testing.T) {
	db := openTempDB(t)
	if _, err := NewMigrator(db, []VEntity{notesEntity()}).Run(); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if _, err := NewMigrator(db, []VEntity{notesWithArchived()}).Run(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	rows, err := db.Query(`SELECT table_name, migration_kind FROM _mar_schema_migrations ORDER BY id`)
	if err != nil {
		t.Fatalf("audit query: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var table, kind string
		if err := rows.Scan(&table, &kind); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, table+"."+kind)
	}
	want := []string{"notes.create_table", "notes.add_column_archived"}
	if len(got) != len(want) {
		t.Fatalf("audit history: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("audit[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

// (11) CI plan command: Plan() against a DB with a blocked-migration
// scenario → blocked step is in the result.
func TestMigrate_PlanShowsBlockedSteps(t *testing.T) {
	db := openTempDB(t)
	if _, err := NewMigrator(db, []VEntity{notesEntity()}).Run(); err != nil {
		t.Fatalf("seed migrate: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO notes (body) VALUES ('row')`); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	// Add a NOT NULL column on a non-empty table — should plan as
	// Blocked.
	withRequired := VEntity{
		Table: "notes",
		Fields: []EntityField{
			{Name: "id", SQLType: "INTEGER", NotNull: true, Serial: true},
			{Name: "body", SQLType: "TEXT", NotNull: true},
			{Name: "category", SQLType: "TEXT", NotNull: true}, // required, no default
		},
	}
	plan, err := NewMigrator(db, []VEntity{withRequired}).Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var foundBlocked bool
	for _, s := range plan {
		if s.Kind == StepBlocked && s.Column == "category" {
			foundBlocked = true
			break
		}
	}
	if !foundBlocked {
		t.Fatalf("expected blocked step for category; plan was:\n%+v", plan)
	}
}

// Blocked migrations must expose a *BlockedMigrationError so the CLI
// can split the message into the standard `Error:` (red headline) +
// `Hint:` (yellow body) blocks. The whole point of the structured
// type is to enable colored rendering — if the underlying error
// degrades to a generic fmt.Errorf, the CLI loses both the split and
// the colors and the message falls back to a plain raw print.
//
// Pins:
//   - errors.As succeeds against *BlockedMigrationError.
//   - Summary is the one-line headline (no embedded newlines).
//   - Hint is non-empty for the cases that have remediation guidance.
//   - The Error() method bundles Summary + "\n\n" + Hint so callers
//     that just stringify (tests, raw logs, SSE channel) still get
//     the full message.
func TestMigrate_BlockedErrorIsStructured(t *testing.T) {
	db := openTempDB(t)
	if _, err := NewMigrator(db, []VEntity{notesEntity()}).Run(); err != nil {
		t.Fatalf("seed migrate: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO notes (body) VALUES ('row')`); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	withRequired := VEntity{
		Table: "notes",
		Fields: []EntityField{
			{Name: "id", SQLType: "INTEGER", NotNull: true, Serial: true},
			{Name: "body", SQLType: "TEXT", NotNull: true},
			{Name: "category", SQLType: "TEXT", NotNull: true},
		},
	}
	_, err := NewMigrator(db, []VEntity{withRequired}).Run()
	if err == nil {
		t.Fatalf("expected blocked error, got nil")
	}

	var be *BlockedMigrationError
	if !errors.As(err, &be) {
		t.Fatalf("expected *BlockedMigrationError, got %T (%v)", err, err)
	}
	if be.Summary == "" {
		t.Fatal("Summary is empty")
	}
	if strings.Contains(be.Summary, "\n") {
		t.Errorf("Summary should be a one-liner, got:\n%s", be.Summary)
	}
	if !strings.Contains(be.Summary, "migration blocked for entity notes") {
		t.Errorf("Summary missing expected prefix: %q", be.Summary)
	}
	if !strings.Contains(be.Summary, `"category"`) {
		t.Errorf("Summary missing column name: %q", be.Summary)
	}
	if be.Hint == "" {
		t.Fatal("Hint is empty for a remediation-eligible case")
	}
	if !strings.Contains(be.Hint, "ALTER TABLE") {
		t.Errorf("Hint missing remediation SQL: %s", be.Hint)
	}
	// Migration errors must follow the established prefix grammar
	// (`Error:` / `Hint:` only, no ad-hoc labels) and must never
	// promise unreleased features — both rules came from operator
	// pushback. Catch the patterns, not the specific phrasings,
	// so future drift toward the same shape gets blocked.
	bannedSubstrings := []string{
		"Note:", // non-standard prefix label
		"Tip:",  // ditto
		"FYI:",  // ditto
	}
	bannedPatterns := []string{
		"a future ",           // "a future X will..."
		"will let you",        // "X will let you skip..."
		"once it lands",       // "once it lands, the migrator..."
		"once X lands",        // generic "once X lands"
		"coming soon",         // "coming soon"
		"not yet implemented", // "not yet implemented" — implies "but will be"
		"deferred to",         // "deferred to v1"
	}
	for _, s := range bannedSubstrings {
		if strings.Contains(be.Hint, s) {
			t.Errorf("Hint contains banned label %q:\n%s", s, be.Hint)
		}
	}
	lc := strings.ToLower(be.Hint)
	for _, p := range bannedPatterns {
		if strings.Contains(lc, p) {
			t.Errorf("Hint contains banned future-promise phrase %q:\n%s", p, be.Hint)
		}
	}
	// Error() must bundle the parts so callers that stringify the
	// error (rather than introspect the struct) get the full message.
	full := err.Error()
	if !strings.Contains(full, be.Summary) || !strings.Contains(full, be.Hint) {
		t.Errorf("Error() doesn't bundle Summary + Hint:\n%s", full)
	}
}

// Orphan table warning: declared entity disappears, table remains.
func TestMigrate_OrphanTable_NoteOnly(t *testing.T) {
	db := openTempDB(t)
	if _, err := NewMigrator(db, []VEntity{notesEntity()}).Run(); err != nil {
		t.Fatalf("seed migrate: %v", err)
	}
	// Now run with an empty entity list — notes is orphan.
	summary, err := NewMigrator(db, nil).Run()
	if err != nil {
		t.Fatalf("Run on empty entities: %v", err)
	}
	if summary.HasChanges() {
		t.Errorf("unexpected applied changes: %v", summary.Applied)
	}
	if len(summary.Notes) != 1 {
		t.Fatalf("expected 1 orphan note; got %d", len(summary.Notes))
	}
	if !strings.Contains(summary.Notes[0], `table "notes"`) ||
		!strings.Contains(summary.Notes[0], `DROP TABLE notes;`) {
		t.Errorf("orphan note format: %q", summary.Notes[0])
	}
}

// Empty-table nullability change: drop+recreate (data-less so safe).
func TestMigrate_NullabilityChange_EmptyTable_Recreates(t *testing.T) {
	db := openTempDB(t)
	// Initial: body NOT NULL.
	if _, err := NewMigrator(db, []VEntity{notesEntity()}).Run(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Switch body to optional. Table is empty → recreate.
	relaxed := VEntity{
		Table: "notes",
		Fields: []EntityField{
			{Name: "id", SQLType: "INTEGER", NotNull: true, Serial: true},
			{Name: "body", SQLType: "TEXT"}, // no NotNull
		},
	}
	summary, err := NewMigrator(db, []VEntity{relaxed}).Run()
	if err != nil {
		t.Fatalf("relax migrate: %v", err)
	}
	if got, want := len(summary.Applied), 1; got != want {
		t.Fatalf("applied: got %d, want %d", got, want)
	}
	if summary.Applied[0].Kind != StepRecreateEmpty {
		t.Errorf("kind: got %v, want StepRecreateEmpty", summary.Applied[0].Kind)
	}
}

// Nullability change on populated table → blocked.
func TestMigrate_NullabilityChange_NonEmpty_Blocked(t *testing.T) {
	db := openTempDB(t)
	if _, err := NewMigrator(db, []VEntity{notesEntity()}).Run(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO notes (body) VALUES ('x')`); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	relaxed := VEntity{
		Table: "notes",
		Fields: []EntityField{
			{Name: "id", SQLType: "INTEGER", NotNull: true, Serial: true},
			{Name: "body", SQLType: "TEXT"},
		},
	}
	_, err := NewMigrator(db, []VEntity{relaxed}).Run()
	if err == nil {
		t.Fatalf("expected blocked-error; got nil")
	}
	if !strings.Contains(err.Error(), "nullability changed") {
		t.Errorf("unexpected error message:\n%s", err.Error())
	}
}
