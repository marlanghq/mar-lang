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
(define-entity book
    (fields
      ((title string))))

(define-app migration-api
  (entities book))
`)
	appV1.Database = dbPath

	if _, err := New(appV1); err != nil {
		t.Fatalf("runtime.New(v1) failed: %v", err)
	}

	appV2 := mustParseApp(t, `
(define-entity book
    (fields
      ((title string)
       (notes string optional))))

(define-app migration-api
  (entities book))
`)
	appV2.Database = dbPath

	if _, err := New(appV2); err != nil {
		t.Fatalf("runtime.New(v2) failed: %v", err)
	}

	db := sqlitecli.Open(dbPath)
	rows, err := db.QueryRows(`PRAGMA table_info("book")`)
	if err != nil {
		t.Fatalf("PRAGMA table_info failed: %v", err)
	}
	notes, ok := findColumn(rows, "notes")
	if !ok {
		t.Fatalf("expected notes column in book table, got rows: %+v", rows)
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
(define-entity book
    (fields
      ((title string)
       (price decimal))))

(define-app migration-api
  (entities book))
`)
	appV1.Database = dbPath
	if _, err := New(appV1); err != nil {
		t.Fatalf("runtime.New(v1) failed: %v", err)
	}

	appV2 := mustParseApp(t, `
(define-entity book
    (fields
      ((title string)
       (price string))))

(define-app migration-api
  (entities book))
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
(define-entity book
    (fields
      ((title string))))

(define-app migration-api
  (entities book))
`)
	appV1.Database = dbPath
	if _, err := New(appV1); err != nil {
		t.Fatalf("runtime.New(v1) failed: %v", err)
	}

	appV2 := mustParseApp(t, `
(define-entity book
    (fields
      ((title string)
       (stock int))))

(define-app migration-api
  (entities book))
`)
	appV2.Database = dbPath
	if _, err := New(appV2); err != nil {
		t.Fatalf("runtime.New(v2) failed: %v", err)
	}

	db := sqlitecli.Open(dbPath)
	rows, err := db.QueryRows(`PRAGMA table_info("book")`)
	if err != nil {
		t.Fatalf("PRAGMA table_info failed: %v", err)
	}
	stock, ok := findColumn(rows, "stock")
	if !ok {
		t.Fatalf("expected stock column in book table, got rows: %+v", rows)
	}
	if got := int64Value(stock["notnull"]); got != 1 {
		t.Fatalf("expected stock to be not null, got notnull=%d", got)
	}
}

func TestMigrationsBlockAddingRequiredFieldWhenTableHasRows(t *testing.T) {
	requireSQLite3(t)

	dbPath := filepath.Join(t.TempDir(), "migration-required-block.db")

	appV1 := mustParseApp(t, `
(define-entity book
    (fields
      ((title string))))

(define-app migration-api
  (entities book))
`)
	appV1.Database = dbPath
	r1, err := New(appV1)
	if err != nil {
		t.Fatalf("runtime.New(v1) failed: %v", err)
	}
	if _, err := r1.DB.Exec(`INSERT INTO book (title, created_at, updated_at) VALUES (?, ?, ?)`, "First book", int64(1), int64(1)); err != nil {
		t.Fatalf("seed insert failed: %v", err)
	}

	appV2 := mustParseApp(t, `
(define-entity book
    (fields
      ((title string)
       (stock int))))

(define-app migration-api
  (entities book))
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
(define-entity book
    (fields
      ((title string))))

(define-app migration-api
  (entities book))
`)
	appV1.Database = dbPath
	r1, err := New(appV1)
	if err != nil {
		t.Fatalf("runtime.New(v1) failed: %v", err)
	}
	if _, err := r1.DB.Exec(`INSERT INTO book (title, created_at, updated_at) VALUES (?, ?, ?)`, "First book", int64(1), int64(1)); err != nil {
		t.Fatalf("seed insert failed: %v", err)
	}

	appV2 := mustParseApp(t, `
(define-entity book
    (fields
      ((title string)
       (stock int)))
    (defaults
      ((stock 0))))

(define-app migration-api
  (entities book))
`)
	appV2.Database = dbPath

	if _, err := New(appV2); err != nil {
		t.Fatalf("runtime.New(v2) failed: %v", err)
	}

	db := sqlitecli.Open(dbPath)
	rows, err := db.QueryRows(`PRAGMA table_info("book")`)
	if err != nil {
		t.Fatalf("PRAGMA table_info failed: %v", err)
	}
	stock, ok := findColumn(rows, "stock")
	if !ok {
		t.Fatalf("expected stock column in book table, got rows: %+v", rows)
	}
	if got := int64Value(stock["notnull"]); got != 1 {
		t.Fatalf("expected stock to be not null, got notnull=%d", got)
	}
	if fmt.Sprintf("%v", stock["dflt_value"]) != "0" {
		t.Fatalf("expected stock default 0, got %#v", stock["dflt_value"])
	}

	row, ok, err := db.QueryRow(`SELECT title, stock FROM book WHERE title = ?`, "First book")
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

func TestMigrationsCreateEntityUniqueIndex(t *testing.T) {
	requireSQLite3(t)

	dbPath := filepath.Join(t.TempDir(), "migration-entity-unique.db")

	app := mustParseApp(t, `
(define-entity handle
    (fields
      ((realm string)
       (value string)))
    (unique
      ((realm value))))

(define-app twitterish
  (entities handle))
`)
	app.Database = dbPath

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}

	_, err = r.DB.Exec(`INSERT INTO handle (realm, value, created_at, updated_at) VALUES (?, ?, ?, ?)`, "user", "marcio", int64(1), int64(1))
	if err != nil {
		t.Fatalf("first insert failed: %v", err)
	}
	_, err = r.DB.Exec(`INSERT INTO handle (realm, value, created_at, updated_at) VALUES (?, ?, ?, ?)`, "user", "marcio", int64(2), int64(2))
	if err == nil {
		t.Fatal("expected duplicate composite unique insert to fail")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Fatalf("expected unique constraint error, got %v", err)
	}
}

func TestMigrationsAllowNullabilityChangeWhenTableIsEmpty(t *testing.T) {
	requireSQLite3(t)

	dbPath := filepath.Join(t.TempDir(), "migration-nullability-empty.db")

	appV1 := mustParseApp(t, `
(define-entity student
    (fields
      ((birth-date date))))

(define-app migration-api
  (entities student))
`)
	appV1.Database = dbPath
	if _, err := New(appV1); err != nil {
		t.Fatalf("runtime.New(v1) failed: %v", err)
	}

	appV2 := mustParseApp(t, `
(define-entity student
    (fields
      ((birth-date date optional))))

(define-app migration-api
  (entities student))
`)
	appV2.Database = dbPath
	if _, err := New(appV2); err != nil {
		t.Fatalf("runtime.New(v2) failed: %v", err)
	}

	db := sqlitecli.Open(dbPath)
	rows, err := db.QueryRows(`PRAGMA table_info("student")`)
	if err != nil {
		t.Fatalf("PRAGMA table_info failed: %v", err)
	}
	birthDate, ok := findColumn(rows, "birth_date")
	if !ok {
		t.Fatalf("expected birth_date column in student table, got rows: %+v", rows)
	}
	if got := int64Value(birthDate["notnull"]); got != 0 {
		t.Fatalf("expected birthDate to be nullable, got notnull=%d", got)
	}
}

func TestMigrationsBlockNullabilityChangeWhenTableHasRows(t *testing.T) {
	requireSQLite3(t)

	dbPath := filepath.Join(t.TempDir(), "migration-nullability-block.db")

	appV1 := mustParseApp(t, `
(define-entity student
    (fields
      ((birth-date date))))

(define-app migration-api
  (entities student))
`)
	appV1.Database = dbPath
	r1, err := New(appV1)
	if err != nil {
		t.Fatalf("runtime.New(v1) failed: %v", err)
	}
	if _, err := r1.DB.Exec(`INSERT INTO student (birth_date, created_at, updated_at) VALUES (?, ?, ?)`, int64(0), int64(1), int64(1)); err != nil {
		t.Fatalf("seed insert failed: %v", err)
	}

	appV2 := mustParseApp(t, `
(define-entity student
    (fields
      ((birth-date date optional))))

(define-app migration-api
  (entities student))
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
(define-entity todo
    (fields
      ((title string)))
    (belongs-to
      ((user))))

(define-app relation-create-api
  (entities todo))
`)
	app.Database = dbPath

	if _, err := New(app); err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}

	db := sqlitecli.Open(dbPath)
	rows, err := db.QueryRows(`PRAGMA foreign_key_list("todo")`)
	if err != nil {
		t.Fatalf("PRAGMA foreign_key_list failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one foreign key on todo, got %+v", rows)
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
(define-entity todo
    (fields
      ((title string))))

(define-app relation-block-api
  (entities todo))
`)
	appV1.Database = dbPath
	if _, err := New(appV1); err != nil {
		t.Fatalf("runtime.New(v1) failed: %v", err)
	}

	appV2 := mustParseApp(t, `
(define-entity todo
    (fields
      ((title string)))
    (belongs-to
      ((user))))

(define-app relation-block-api
  (entities todo))
`)
	appV2.Database = dbPath

	_, err := New(appV2)
	if err == nil {
		t.Fatal("expected relation migration to be blocked for existing table")
	}

	msg := err.Error()
	if !strings.Contains(msg, `table "todo" already exists`) {
		t.Fatalf("unexpected error message: %v", err)
	}
	if !strings.Contains(msg, `todo.user_id -> users.id`) {
		t.Fatalf("unexpected error message: %v", err)
	}
	if !strings.Contains(msg, `Migrate the table manually, then restart the app.`) {
		t.Fatalf("unexpected error message: %v", err)
	}
	if !strings.Contains(msg, `Suggested Manual Migration SQL:`) {
		t.Fatalf("unexpected error message: %v", err)
	}
	if !strings.Contains(msg, `CREATE TABLE todo_new (`) {
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
(define-entity todo
    (fields
      ((title string))))

(define-app internal-auth-api
  (entities todo))
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
(define app-auth ())

(define-entity user)

(define-app app-auth-api
  (auth app-auth)
  (entities user))
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
