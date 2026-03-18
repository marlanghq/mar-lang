package generator

import (
	"strings"
	"testing"

	"mar/internal/model"
)

func TestGenerateTSClientMapsPosixToNumber(t *testing.T) {
	app := &model.App{
		AppName:  "TodoApi",
		Port:     4200,
		Database: "todo.db",
		InputAliases: []model.TypeAlias{
			{
				Name: "ScheduleTodoInput",
				Fields: []model.AliasField{
					{Name: "due_at", Type: "Posix"},
				},
			},
		},
	}

	out, err := GenerateTSClient(app)
	if err != nil {
		t.Fatalf("GenerateTSClient returned error: %v", err)
	}

	source := string(out.Source)
	if !strings.Contains(source, "export interface ScheduleTodoInput {") {
		t.Fatalf("expected generated TypeScript client to include alias interface, source:\n%s", source)
	}
	if !strings.Contains(source, "due_at: number;") {
		t.Fatalf("expected Posix to map to number in TypeScript client, source:\n%s", source)
	}
}

func TestGenerateElmClientSchemaIncludesPosixFieldType(t *testing.T) {
	app := &model.App{
		AppName:  "TodoApi",
		Port:     4200,
		Database: "todo.db",
		Entities: []model.Entity{
			{
				Name:       "Todo",
				Table:      "todos",
				Resource:   "/todos",
				PrimaryKey: "id",
				Fields: []model.Field{
					{Name: "id", Type: "Int", Primary: true, Auto: true},
					{Name: "due_at", Type: "Posix"},
				},
			},
		},
	}

	out, err := GenerateElmClient(app)
	if err != nil {
		t.Fatalf("GenerateElmClient returned error: %v", err)
	}

	source := string(out.Source)
	if !strings.Contains(source, ", fieldType = \"Posix\"") {
		t.Fatalf("expected generated Elm client schema metadata to include Posix field type, source:\n%s", source)
	}
}

func TestGeneratedClientsIncludeFieldDefaultsInSchemaMetadata(t *testing.T) {
	app := &model.App{
		AppName:  "TodoApi",
		Port:     4200,
		Database: "todo.db",
		Entities: []model.Entity{
			{
				Name:       "Todo",
				Table:      "todos",
				Resource:   "/todos",
				PrimaryKey: "id",
				Fields: []model.Field{
					{Name: "id", Type: "Int", Primary: true, Auto: true},
					{Name: "title", Type: "String", Default: "Untitled"},
					{Name: "due_at", Type: "Posix", Default: int64(1742203200000)},
				},
			},
		},
	}

	tsOut, err := GenerateTSClient(app)
	if err != nil {
		t.Fatalf("GenerateTSClient returned error: %v", err)
	}
	tsSource := string(tsOut.Source)
	if !strings.Contains(tsSource, `defaultValue: "Untitled"`) {
		t.Fatalf("expected TS schema metadata to include string default, source:\n%s", tsSource)
	}
	if !strings.Contains(tsSource, `defaultValue: 1742203200000`) {
		t.Fatalf("expected TS schema metadata to include Posix default, source:\n%s", tsSource)
	}

	elmOut, err := GenerateElmClient(app)
	if err != nil {
		t.Fatalf("GenerateElmClient returned error: %v", err)
	}
	elmSource := string(elmOut.Source)
	if !strings.Contains(elmSource, `, defaultValue = Just (Encode.string "Untitled")`) {
		t.Fatalf("expected Elm schema metadata to include string default, source:\n%s", elmSource)
	}
	if !strings.Contains(elmSource, `, defaultValue = Just (Encode.float 1742203200000.0)`) {
		t.Fatalf("expected Elm schema metadata to include Posix default, source:\n%s", elmSource)
	}
}
