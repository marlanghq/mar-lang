package generator

import (
	"strings"
	"testing"

	"mar/internal/model"
)

func TestGenerateTSClientMapsDateTimeToNumber(t *testing.T) {
	app := &model.App{
		AppName:  "TodoApi",
		Port:     4200,
		Database: "todo.db",
		InputAliases: []model.TypeAlias{
			{
				Name: "ScheduleTodoInput",
				Fields: []model.AliasField{
					{Name: "due_at", Type: "DateTime"},
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
		t.Fatalf("expected DateTime to map to number in TypeScript client, source:\n%s", source)
	}
}

func TestGenerateElmClientSchemaIncludesDateFieldType(t *testing.T) {
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
					{Name: "due_on", Type: "Date"},
				},
			},
		},
	}

	out, err := GenerateElmClient(app)
	if err != nil {
		t.Fatalf("GenerateElmClient returned error: %v", err)
	}

	source := string(out.Source)
	if !strings.Contains(source, ", fieldType = \"Date\"") {
		t.Fatalf("expected generated Elm client schema metadata to include Date field type, source:\n%s", source)
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
					{Name: "due_at", Type: "DateTime", Default: int64(1742203200000)},
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
		t.Fatalf("expected TS schema metadata to include DateTime default, source:\n%s", tsSource)
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
		t.Fatalf("expected Elm schema metadata to include DateTime default, source:\n%s", elmSource)
	}
}

func TestGeneratedClientsIncludeCurrentUserBelongsToMetadata(t *testing.T) {
	app := &model.App{
		AppName:  "PersonalTodo",
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
					{Name: "title", Type: "String"},
					{Name: "user", Type: "Int", RelationEntity: "User", CurrentUser: true},
				},
			},
		},
	}

	tsOut, err := GenerateTSClient(app)
	if err != nil {
		t.Fatalf("GenerateTSClient returned error: %v", err)
	}
	tsSource := string(tsOut.Source)
	if !strings.Contains(tsSource, `currentUser: true`) {
		t.Fatalf("expected TS schema metadata to include currentUser flag, source:\n%s", tsSource)
	}

	elmOut, err := GenerateElmClient(app)
	if err != nil {
		t.Fatalf("GenerateElmClient returned error: %v", err)
	}
	elmSource := string(elmOut.Source)
	if !strings.Contains(elmSource, `, currentUser = True`) {
		t.Fatalf("expected Elm schema metadata to include currentUser flag, source:\n%s", elmSource)
	}
}
