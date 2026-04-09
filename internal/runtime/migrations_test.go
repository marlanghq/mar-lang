package runtime

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/model"
	"mar/internal/parser"
	"mar/internal/sqlitecli"
)

func TestMigrationsCreateAndAddOptionalField(t *testing.T) {
	requireSQLite3(t)

	dbPath := filepath.Join(t.TempDir(), "migration-safe.db")

	appV1 := mustParseApp(t, `
app MigrationApi

entity Book {
  title: String
}
`)
	appV1.Database = dbPath

	if _, err := New(appV1); err != nil {
		t.Fatalf("runtime.New(v1) failed: %v", err)
	}

	appV2 := mustParseApp(t, `
app MigrationApi

entity Book {
  title: String
  notes: String optional
}
`)
	appV2.Database = dbPath

	if _, err := New(appV2); err != nil {
		t.Fatalf("runtime.New(v2) failed: %v", err)
	}

	db := sqlitecli.Open(dbPath)
	rows, err := db.QueryRows(`PRAGMA table_info("books")`)
	if err != nil {
		t.Fatalf("PRAGMA table_info failed: %v", err)
	}
	notes, ok := findColumn(rows, "notes")
	if !ok {
		t.Fatalf("expected notes column in books table, got rows: %+v", rows)
	}
	if got := strings.ToUpper(fmt.Sprintf("%v", notes["type"])); got != "TEXT" {
		t.Fatalf("unexpected notes column type: %s", got)
	}
	if got := int64Value(notes["notnull"]); got != 0 {
		t.Fatalf("expected notes to be nullable, got notnull=%d", got)
	}
}

func TestMigrationsBlockTypeChange(t *testing.T) {
	requireSQLite3(t)

	dbPath := filepath.Join(t.TempDir(), "migration-type-block.db")

	appV1 := mustParseApp(t, `
app MigrationApi

entity Book {
  title: String
  price: Float
}
`)
	appV1.Database = dbPath
	if _, err := New(appV1); err != nil {
		t.Fatalf("runtime.New(v1) failed: %v", err)
	}

	appV2 := mustParseApp(t, `
app MigrationApi

entity Book {
  title: String
  price: String
}
`)
	appV2.Database = dbPath
	_, err := New(appV2)
	if err == nil {
		t.Fatal("expected migration to block type change")
	}

	msg := err.Error()
	if !strings.Contains(msg, "migration blocked for Book.price") {
		t.Fatalf("unexpected error message: %v", err)
	}
	if !strings.Contains(msg, "type changed") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestMigrationsAllowAddingRequiredFieldToEmptyTable(t *testing.T) {
	requireSQLite3(t)

	dbPath := filepath.Join(t.TempDir(), "migration-required-empty.db")

	appV1 := mustParseApp(t, `
app MigrationApi

entity Book {
  title: String
}
`)
	appV1.Database = dbPath
	if _, err := New(appV1); err != nil {
		t.Fatalf("runtime.New(v1) failed: %v", err)
	}

	appV2 := mustParseApp(t, `
app MigrationApi

entity Book {
  title: String
  stock: Int
}
`)
	appV2.Database = dbPath
	if _, err := New(appV2); err != nil {
		t.Fatalf("runtime.New(v2) failed: %v", err)
	}

	db := sqlitecli.Open(dbPath)
	rows, err := db.QueryRows(`PRAGMA table_info("books")`)
	if err != nil {
		t.Fatalf("PRAGMA table_info failed: %v", err)
	}
	stock, ok := findColumn(rows, "stock")
	if !ok {
		t.Fatalf("expected stock column in books table, got rows: %+v", rows)
	}
	if got := int64Value(stock["notnull"]); got != 1 {
		t.Fatalf("expected stock to be not null, got notnull=%d", got)
	}
}

func TestMigrationsBlockAddingRequiredFieldWhenTableHasRows(t *testing.T) {
	requireSQLite3(t)

	dbPath := filepath.Join(t.TempDir(), "migration-required-block.db")

	appV1 := mustParseApp(t, `
app MigrationApi

entity Book {
  title: String
}
`)
	appV1.Database = dbPath
	r1, err := New(appV1)
	if err != nil {
		t.Fatalf("runtime.New(v1) failed: %v", err)
	}
	if _, err := r1.DB.Exec(`INSERT INTO books (title, created_at, updated_at) VALUES (?, ?, ?)`, "First book", int64(1), int64(1)); err != nil {
		t.Fatalf("seed insert failed: %v", err)
	}

	appV2 := mustParseApp(t, `
app MigrationApi

entity Book {
  title: String
  stock: Int
}
`)
	appV2.Database = dbPath
	_, err = New(appV2)
	if err == nil {
		t.Fatal("expected migration to block required field addition")
	}

	msg := err.Error()
	if !strings.Contains(msg, `cannot auto-add required field "stock"`) {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestMigrationsAutoAddRequiredFieldWithDefault(t *testing.T) {
	requireSQLite3(t)

	dbPath := filepath.Join(t.TempDir(), "migration-required-default.db")

	appV1 := mustParseApp(t, `
app MigrationApi

entity Book {
  title: String
}
`)
	appV1.Database = dbPath
	r1, err := New(appV1)
	if err != nil {
		t.Fatalf("runtime.New(v1) failed: %v", err)
	}
	if _, err := r1.DB.Exec(`INSERT INTO books (title, created_at, updated_at) VALUES (?, ?, ?)`, "First book", int64(1), int64(1)); err != nil {
		t.Fatalf("seed insert failed: %v", err)
	}

	appV2 := mustParseApp(t, `
app MigrationApi

entity Book {
  title: String
  stock: Int default 0
}
`)
	appV2.Database = dbPath

	if _, err := New(appV2); err != nil {
		t.Fatalf("runtime.New(v2) failed: %v", err)
	}

	db := sqlitecli.Open(dbPath)
	rows, err := db.QueryRows(`PRAGMA table_info("books")`)
	if err != nil {
		t.Fatalf("PRAGMA table_info failed: %v", err)
	}
	stock, ok := findColumn(rows, "stock")
	if !ok {
		t.Fatalf("expected stock column in books table, got rows: %+v", rows)
	}
	if got := int64Value(stock["notnull"]); got != 1 {
		t.Fatalf("expected stock to be not null, got notnull=%d", got)
	}
	if fmt.Sprintf("%v", stock["dflt_value"]) != "0" {
		t.Fatalf("expected stock default 0, got %#v", stock["dflt_value"])
	}

	row, ok, err := db.QueryRow(`SELECT title, stock FROM books WHERE title = ?`, "First book")
	if err != nil {
		t.Fatalf("select migrated row failed: %v", err)
	}
	if !ok {
		t.Fatal("expected migrated row to exist")
	}
	if int64Value(row["stock"]) != 0 {
		t.Fatalf("expected migrated row stock default 0, got %#v", row["stock"])
	}
}

func TestMigrationsAllowNullabilityChangeWhenTableIsEmpty(t *testing.T) {
	requireSQLite3(t)

	dbPath := filepath.Join(t.TempDir(), "migration-nullability-empty.db")

	appV1 := mustParseApp(t, `
app MigrationApi

entity Student {
  birthDate: Date
}
`)
	appV1.Database = dbPath
	if _, err := New(appV1); err != nil {
		t.Fatalf("runtime.New(v1) failed: %v", err)
	}

	appV2 := mustParseApp(t, `
app MigrationApi

entity Student {
  birthDate: Date optional
}
`)
	appV2.Database = dbPath
	if _, err := New(appV2); err != nil {
		t.Fatalf("runtime.New(v2) failed: %v", err)
	}

	db := sqlitecli.Open(dbPath)
	rows, err := db.QueryRows(`PRAGMA table_info("students")`)
	if err != nil {
		t.Fatalf("PRAGMA table_info failed: %v", err)
	}
	birthDate, ok := findColumn(rows, "birthDate")
	if !ok {
		t.Fatalf("expected birthDate column in students table, got rows: %+v", rows)
	}
	if got := int64Value(birthDate["notnull"]); got != 0 {
		t.Fatalf("expected birthDate to be nullable, got notnull=%d", got)
	}
}

func TestMigrationsBlockNullabilityChangeWhenTableHasRows(t *testing.T) {
	requireSQLite3(t)

	dbPath := filepath.Join(t.TempDir(), "migration-nullability-block.db")

	appV1 := mustParseApp(t, `
app MigrationApi

entity Student {
  birthDate: Date
}
`)
	appV1.Database = dbPath
	r1, err := New(appV1)
	if err != nil {
		t.Fatalf("runtime.New(v1) failed: %v", err)
	}
	if _, err := r1.DB.Exec(`INSERT INTO students (birthDate, created_at, updated_at) VALUES (?, ?, ?)`, int64(0), int64(1), int64(1)); err != nil {
		t.Fatalf("seed insert failed: %v", err)
	}

	appV2 := mustParseApp(t, `
app MigrationApi

entity Student {
  birthDate: Date optional
}
`)
	appV2.Database = dbPath
	_, err = New(appV2)
	if err == nil {
		t.Fatal("expected migration to block nullability change")
	}

	msg := err.Error()
	if !strings.Contains(msg, "nullability changed from required to optional") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestMigrationsCreateForeignKeyForNewRelationTable(t *testing.T) {
	requireSQLite3(t)

	dbPath := filepath.Join(t.TempDir(), "migration-new-relation-fk.db")

	app := mustParseApp(t, `
app RelationCreateApi

entity User {
  email: String
}

entity Todo {
  title: String
  belongs_to User
}
`)
	app.Database = dbPath

	if _, err := New(app); err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}

	db := sqlitecli.Open(dbPath)
	rows, err := db.QueryRows(`PRAGMA foreign_key_list("todos")`)
	if err != nil {
		t.Fatalf("PRAGMA foreign_key_list failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one foreign key on todos, got %+v", rows)
	}
	fk := rows[0]
	if fmt.Sprintf("%v", fk["from"]) != "user_id" {
		t.Fatalf("expected foreign key from user_id, got %+v", fk)
	}
	if fmt.Sprintf("%v", fk["table"]) != "users" {
		t.Fatalf("expected foreign key target table users, got %+v", fk)
	}
	if fmt.Sprintf("%v", fk["to"]) != "id" {
		t.Fatalf("expected foreign key target column id, got %+v", fk)
	}
}

func TestMigrationsBlockAddingRelationToExistingTable(t *testing.T) {
	requireSQLite3(t)

	dbPath := filepath.Join(t.TempDir(), "migration-existing-relation-block.db")

	appV1 := mustParseApp(t, `
app RelationBlockApi

entity User {
  email: String
}

entity Todo {
  title: String
}
`)
	appV1.Database = dbPath
	if _, err := New(appV1); err != nil {
		t.Fatalf("runtime.New(v1) failed: %v", err)
	}

	appV2 := mustParseApp(t, `
app RelationBlockApi

entity User {
  email: String
}

entity Todo {
  title: String
  belongs_to User
}
`)
	appV2.Database = dbPath

	_, err := New(appV2)
	if err == nil {
		t.Fatal("expected relation migration to be blocked for existing table")
	}

	msg := err.Error()
	if !strings.Contains(msg, `table "todos" already exists`) {
		t.Fatalf("unexpected error message: %v", err)
	}
	if !strings.Contains(msg, `todos.user_id -> users.id`) {
		t.Fatalf("unexpected error message: %v", err)
	}
	if !strings.Contains(msg, `Migrate the table manually, then restart the app.`) {
		t.Fatalf("unexpected error message: %v", err)
	}
	if !strings.Contains(msg, `Suggested Manual Migration SQL:`) {
		t.Fatalf("unexpected error message: %v", err)
	}
	if !strings.Contains(msg, `CREATE TABLE todos_new (`) {
		t.Fatalf("unexpected error message: %v", err)
	}
	if !strings.Contains(msg, `/* replace NULL with a valid users.id value */ NULL AS user_id`) {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestMigrationsCreateAuthEmailUniqueIndexForInternalUsers(t *testing.T) {
	requireSQLite3(t)

	dbPath := filepath.Join(t.TempDir(), "migration-auth-index-internal.db")
	app := mustParseApp(t, `
app InternalAuthApi

entity Todo {
  title: String
}
`)
	app.Database = dbPath

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}

	_, err = r.DB.Exec(`INSERT INTO users (email, role, created_at, updated_at) VALUES (?, ?, ?, ?)`, "user@example.com", "user", int64(1), int64(1))
	if err != nil {
		t.Fatalf("first insert failed: %v", err)
	}
	_, err = r.DB.Exec(`INSERT INTO users (email, role, created_at, updated_at) VALUES (?, ?, ?, ?)`, "USER@example.com", "admin", int64(2), int64(2))
	if err == nil {
		t.Fatal("expected duplicate built-in auth email to fail")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Fatalf("expected unique constraint error, got %v", err)
	}
}

func TestMigrationsCreateAuthEmailUniqueIndexForAppUsers(t *testing.T) {
	requireSQLite3(t)

	dbPath := filepath.Join(t.TempDir(), "migration-auth-index-app.db")
	app := mustParseApp(t, `
app AppAuthApi

entity User {
  id: Int primary auto
  email: String
  role: String
}

auth {
}
`)
	app.Database = dbPath

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}

	_, err = r.DB.Exec(`INSERT INTO users (email, role, created_at, updated_at) VALUES (?, ?, ?, ?)`, "user@example.com", "user", int64(1), int64(1))
	if err != nil {
		t.Fatalf("first insert failed: %v", err)
	}
	_, err = r.DB.Exec(`INSERT INTO users (email, role, created_at, updated_at) VALUES (?, ?, ?, ?)`, "USER@example.com", "admin", int64(2), int64(2))
	if err == nil {
		t.Fatal("expected duplicate app auth email to fail")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Fatalf("expected unique constraint error, got %v", err)
	}
}

func requireSQLite3(t *testing.T) {
	t.Helper()
}

func mustParseApp(t *testing.T, src string) *model.App {
	t.Helper()
	app, err := parser.Parse(strings.TrimSpace(src) + "\n")
	if err != nil {
		t.Fatalf("failed to parse app source: %v", err)
	}
	return app
}

func findColumn(rows []map[string]any, name string) (map[string]any, bool) {
	for _, row := range rows {
		if strings.EqualFold(fmt.Sprintf("%v", row["name"]), name) {
			return row, true
		}
	}
	return nil, false
}

func int64Value(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	default:
		return 0
	}
}
