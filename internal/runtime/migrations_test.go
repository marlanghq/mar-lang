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

func TestMigrationsBlockAddingRequiredField(t *testing.T) {
	requireSQLite3(t)

	dbPath := filepath.Join(t.TempDir(), "migration-required-block.db")

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
	_, err := New(appV2)
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
	if _, err := r1.DB.Exec(`INSERT INTO books (title) VALUES (?)`, "First book"); err != nil {
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

	_, err = r.DB.Exec(`INSERT INTO users (email, role) VALUES (?, ?)`, "user@example.com", "user")
	if err != nil {
		t.Fatalf("first insert failed: %v", err)
	}
	_, err = r.DB.Exec(`INSERT INTO users (email, role) VALUES (?, ?)`, "USER@example.com", "admin")
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

	_, err = r.DB.Exec(`INSERT INTO users (email, role) VALUES (?, ?)`, "user@example.com", "user")
	if err != nil {
		t.Fatalf("first insert failed: %v", err)
	}
	_, err = r.DB.Exec(`INSERT INTO users (email, role) VALUES (?, ?)`, "USER@example.com", "admin")
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
