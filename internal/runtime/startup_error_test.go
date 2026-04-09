package runtime

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestPrintStartupErrorSuggestsMakingNewRequiredFieldOptional(t *testing.T) {
	original := os.Stderr
	reader, writer, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("pipe failed: %v", pipeErr)
	}
	os.Stderr = writer
	t.Cleanup(func() {
		os.Stderr = original
	})

	PrintStartupError(
		assertAsError(`migration blocked for entity Todo: cannot auto-add required field "publishedAt" (DateTime) to existing table todos`),
		"",
	)

	_ = writer.Close()
	var buf bytes.Buffer
	if _, copyErr := io.Copy(&buf, reader); copyErr != nil {
		t.Fatalf("copy failed: %v", copyErr)
	}

	output := buf.String()
	if !strings.Contains(output, "publishedAt: DateTime optional") {
		t.Fatalf("expected optional example in hint, got:\n%s", output)
	}
	if !strings.Contains(output, "publishedAt: DateTime default 0") {
		t.Fatalf("expected default example in hint, got:\n%s", output)
	}
}

func TestPrintStartupErrorSuggestsHumanStringDefault(t *testing.T) {
	original := os.Stderr
	reader, writer, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("pipe failed: %v", pipeErr)
	}
	os.Stderr = writer
	t.Cleanup(func() {
		os.Stderr = original
	})

	PrintStartupError(
		assertAsError(`migration blocked for entity User: cannot auto-add required field "name" (String) to existing table users`),
		"",
	)

	_ = writer.Close()
	var buf bytes.Buffer
	if _, copyErr := io.Copy(&buf, reader); copyErr != nil {
		t.Fatalf("copy failed: %v", copyErr)
	}

	output := buf.String()
	if !strings.Contains(output, `name: String default "Unknown"`) {
		t.Fatalf("expected human string default example in hint, got:\n%s", output)
	}
}

func TestPrintStartupErrorFormatsRelationMigrationBlock(t *testing.T) {
	original := os.Stderr
	reader, writer, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("pipe failed: %v", pipeErr)
	}
	os.Stderr = writer
	t.Cleanup(func() {
		os.Stderr = original
	})

	PrintStartupError(assertAsError(`migration blocked for entity VetVisit: table "vet_visits" already exists, and relation "clinic" requires a foreign key vet_visits.clinic_id -> clinics.id

SQLite cannot add this foreign key with ALTER TABLE, so Mar does not migrate it automatically.

Hint:
  Migrate the table manually, then restart the app.
  Suggested Manual Migration SQL:
    BEGIN TRANSACTION;

    CREATE TABLE vet_visits_new (
      "id" INTEGER PRIMARY KEY AUTOINCREMENT
    );

    COMMIT;`), "")

	_ = writer.Close()
	var buf bytes.Buffer
	if _, copyErr := io.Copy(&buf, reader); copyErr != nil {
		t.Fatalf("copy failed: %v", copyErr)
	}

	output := buf.String()
	if !strings.Contains(output, "Migration blocked") {
		t.Fatalf("expected friendly title, got:\n%s", output)
	}
	if !strings.Contains(output, "Foreign key: vet_visits.clinic_id -> clinics.id") {
		t.Fatalf("expected foreign key detail, got:\n%s", output)
	}
	if !strings.Contains(output, "Suggested manual migration SQL:") {
		t.Fatalf("expected sql hint title, got:\n%s", output)
	}
	if !strings.Contains(output, "BEGIN TRANSACTION;") {
		t.Fatalf("expected transactional sql hint, got:\n%s", output)
	}
	if !strings.Contains(output, "Migrate vet_visits manually, then restart the app.") {
		t.Fatalf("expected table-specific hint, got:\n%s", output)
	}
}

func TestPrintStartupErrorFormatsNullabilityMigrationBlock(t *testing.T) {
	original := os.Stderr
	reader, writer, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("pipe failed: %v", pipeErr)
	}
	os.Stderr = writer
	t.Cleanup(func() {
		os.Stderr = original
	})

	PrintStartupError(
		assertAsError(`migration blocked for Estudante.dataNascimento: nullability changed from required to optional in table estudantes`),
		"",
	)

	_ = writer.Close()
	var buf bytes.Buffer
	if _, copyErr := io.Copy(&buf, reader); copyErr != nil {
		t.Fatalf("copy failed: %v", copyErr)
	}

	output := buf.String()
	if !strings.Contains(output, "Database field: required") {
		t.Fatalf("expected database nullability detail, got:\n%s", output)
	}
	if !strings.Contains(output, "Mar field: optional") {
		t.Fatalf("expected mar nullability detail, got:\n%s", output)
	}
	if !strings.Contains(output, "Suggested manual migration SQL:") {
		t.Fatalf("expected sql hint title, got:\n%s", output)
	}
	if !strings.Contains(output, "BEGIN TRANSACTION;") {
		t.Fatalf("expected transactional sql hint, got:\n%s", output)
	}
	if !strings.Contains(output, "make dataNascimento optional") {
		t.Fatalf("expected field-specific nullability comment, got:\n%s", output)
	}
}

func assertAsError(message string) error {
	return startupErrorString(message)
}

type startupErrorString string

func (s startupErrorString) Error() string {
	return string(s)
}
