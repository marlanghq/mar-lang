package parser

import (
	"strings"
	"testing"

	"mar/internal/model"
)

func TestParseValidAppDerivesEntityMetadata(t *testing.T) {
	src := `
app BookStoreApi
port 4100
database "./bookstore.db"

entity Book {
  title: String
  price: Float
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	if app.AppName != "BookStoreApi" {
		t.Fatalf("unexpected app name: %q", app.AppName)
	}
	if app.Port != 4100 {
		t.Fatalf("unexpected port: %d", app.Port)
	}
	if app.Database != "./bookstore.db" {
		t.Fatalf("unexpected database: %q", app.Database)
	}
	if len(app.Entities) != 2 {
		t.Fatalf("expected 2 entities (including built-in User), got %d", len(app.Entities))
	}

	var bookFound bool
	var bookEntityName string
	var book = app.Entities[0]
	for _, entity := range app.Entities {
		if entity.Name == "Book" {
			book = entity
			bookFound = true
			bookEntityName = entity.Name
			break
		}
	}
	if !bookFound || bookEntityName != "Book" {
		t.Fatal("expected Book entity to be present")
	}

	if book.Table != "books" {
		t.Fatalf("unexpected table: %q", book.Table)
	}
	if book.Resource != "/books" {
		t.Fatalf("unexpected resource: %q", book.Resource)
	}
	if book.PrimaryKey != "id" {
		t.Fatalf("expected derived primary key id, got %q", book.PrimaryKey)
	}
	if len(book.Fields) != 5 {
		t.Fatalf("expected 5 fields (including derived id and timestamps), got %d", len(book.Fields))
	}
	if book.Fields[0].Name != "id" || !book.Fields[0].Primary || !book.Fields[0].Auto {
		t.Fatalf("expected first field to be derived auto primary id, got %+v", book.Fields[0])
	}
	if book.Fields[len(book.Fields)-2].Name != "created_at" || book.Fields[len(book.Fields)-2].Type != "DateTime" || !book.Fields[len(book.Fields)-2].Auto {
		t.Fatalf("expected created_at timestamp field, got %+v", book.Fields[len(book.Fields)-2])
	}
	if book.Fields[len(book.Fields)-1].Name != "updated_at" || book.Fields[len(book.Fields)-1].Type != "DateTime" || !book.Fields[len(book.Fields)-1].Auto {
		t.Fatalf("expected updated_at timestamp field, got %+v", book.Fields[len(book.Fields)-1])
	}
}

func TestParseSupportsDoubleDashComments(t *testing.T) {
	src := `
-- application
app TodoApi
port 4100
database "./todo.db"

-- entity
entity Todo {
  title: String
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if app.AppName != "TodoApi" {
		t.Fatalf("unexpected app name: %q", app.AppName)
	}
}

func TestParseDoesNotWarnWhenBootstrapCanPromptForRequiredScalarFields(t *testing.T) {
	src := `
app TodoOwned

entity User {
  teste: String
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(app.Warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", app.Warnings)
	}
}

func TestParseDoesNotWarnWhenBootstrapCanPromptForMultipleRequiredScalarFields(t *testing.T) {
	src := `
app TodoOwned

entity User {
  name: String
  surname: String
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(app.Warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", app.Warnings)
	}
}

func TestParseWarnsWhenRequiredRelationBlocksFirstAdminBootstrap(t *testing.T) {
	src := `
app TodoOwned

entity Team {
  name: String
}

entity User {
  belongs_to Team
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(app.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d (%v)", len(app.Warnings), app.Warnings)
	}
	if !strings.Contains(app.Warnings[0], "required relation field without default") {
		t.Fatalf("expected singular relation wording in warning, got %q", app.Warnings[0])
	}
	if !strings.Contains(app.Warnings[0], "`team`") {
		t.Fatalf("expected warning to mention blocking field, got %q", app.Warnings[0])
	}
	if !strings.Contains(app.Warnings[0], "You can make this field optional") {
		t.Fatalf("expected optional hint in warning, got %q", app.Warnings[0])
	}
}

func TestParseDoesNotWarnWhenFirstAdminCanBeAutoCreated(t *testing.T) {
	src := `
app TodoOwned

entity User {
  displayName: String optional
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(app.Warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", app.Warnings)
	}
}

func TestParseUsesDefaultPortWhenPortIsOmitted(t *testing.T) {
	src := `
app TodoApi
database "./todo.db"

entity Todo {
  title: String
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if app.Port != 4200 {
		t.Fatalf("expected default port 4200, got %d", app.Port)
	}
}

func TestParseUsesDefaultDatabaseWhenDatabaseIsOmitted(t *testing.T) {
	src := `
app TodoApi

entity Todo {
  title: String
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if app.Database != "todo-api.db" {
		t.Fatalf("expected default database todo-api.db, got %q", app.Database)
	}
}

func TestParseKeepsExplicitDatabaseWhenProvided(t *testing.T) {
	src := `
app TodoApi
database "./custom.db"

entity Todo {
  title: String
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if app.Database != "./custom.db" {
		t.Fatalf("expected explicit database to be preserved, got %q", app.Database)
	}
}

func TestParseSupportsFieldDefaults(t *testing.T) {
	src := `
app TodoApi

entity Todo {
  title: String default "Untitled task"
  done: Bool default false
  points: Int default 0
  progress: Float default 0.5
  due_on: Date default 1742234567890
  due_at: DateTime default 1742203200000
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	var todo *model.Entity
	for i := range app.Entities {
		if app.Entities[i].Name == "Todo" {
			todo = &app.Entities[i]
			break
		}
	}
	if todo == nil {
		t.Fatal("expected Todo entity to be present")
	}

	assertFieldDefault := func(name string, expected any) {
		t.Helper()
		for _, field := range todo.Fields {
			if field.Name == name {
				if field.Default != expected {
					t.Fatalf("expected default for %s to be %#v, got %#v", name, expected, field.Default)
				}
				return
			}
		}
		t.Fatalf("expected field %s to be present", name)
	}

	assertFieldDefault("title", "Untitled task")
	assertFieldDefault("done", false)
	assertFieldDefault("points", int64(0))
	assertFieldDefault("progress", 0.5)
	assertFieldDefault("due_on", normalizeDateMillis(1742234567890))
	assertFieldDefault("due_at", int64(1742203200000))
}

func TestParseRejectsInvalidFieldDefaultType(t *testing.T) {
	src := `
app TodoApi

entity Todo {
  done: Bool default "nope"
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for invalid field default")
	}
	if !strings.Contains(err.Error(), "field default for Bool must be true or false") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsDefaultOnAutoPrimaryField(t *testing.T) {
	src := `
app TodoApi

entity Todo {
  id: Int primary auto default 1
  title: String
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for default on auto primary field")
	}
	if !strings.Contains(err.Error(), "cannot use default together with primary") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseSupportsDateEntityFields(t *testing.T) {
	src := `
app TodoApi

entity Todo {
  title: String
  due_on: Date optional
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	var todo *model.Entity
	for i := range app.Entities {
		if app.Entities[i].Name == "Todo" {
			todo = &app.Entities[i]
			break
		}
	}
	if todo == nil {
		t.Fatal("expected Todo entity to be present")
	}

	var dueOn *model.Field
	for i := range todo.Fields {
		if todo.Fields[i].Name == "due_on" {
			dueOn = &todo.Fields[i]
			break
		}
	}
	if dueOn == nil {
		t.Fatal("expected due_on field to be present")
	}
	if dueOn.Type != "Date" {
		t.Fatalf("expected due_on type Date, got %q", dueOn.Type)
	}
	if !dueOn.Optional {
		t.Fatal("expected due_on to be optional")
	}
}

func TestParseSupportsDateTimeAliasFields(t *testing.T) {
	src := `
app TodoApi

entity Todo {
  title: String
}

type alias ScheduleTodoInput =
  { due_at: DateTime
  }

action scheduleTodo {
  input: ScheduleTodoInput

  create Todo {
    title: "Scheduled"
  }
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(app.InputAliases) != 1 {
		t.Fatalf("expected 1 type alias, got %d", len(app.InputAliases))
	}
	field := app.InputAliases[0].Fields[0]
	if field.Name != "due_at" {
		t.Fatalf("expected alias field due_at, got %q", field.Name)
	}
	if field.Type != "DateTime" {
		t.Fatalf("expected alias field type DateTime, got %q", field.Type)
	}
}

func TestParseSupportsBelongsToDefaultName(t *testing.T) {
	src := `
app TodoApi

entity Todo {
  title: String
  belongs_to User
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	var todo *model.Entity
	for i := range app.Entities {
		if app.Entities[i].Name == "Todo" {
			todo = &app.Entities[i]
			break
		}
	}
	if todo == nil {
		t.Fatal("expected Todo entity to be present")
	}

	var userField *model.Field
	for i := range todo.Fields {
		if todo.Fields[i].Name == "user" {
			userField = &todo.Fields[i]
			break
		}
	}
	if userField == nil {
		t.Fatal("expected user belongs_to field to be present")
	}
	if userField.RelationEntity != "User" {
		t.Fatalf("expected relation entity User, got %q", userField.RelationEntity)
	}
	if userField.Type != "Int" {
		t.Fatalf("expected user belongs_to field to resolve to Int, got %q", userField.Type)
	}
}

func TestParseSupportsNamedOptionalBelongsTo(t *testing.T) {
	src := `
app BillingApi

entity Invoice {
  total: Float
  belongs_to customer: User
  belongs_to approver: User optional
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	var invoice *model.Entity
	for i := range app.Entities {
		if app.Entities[i].Name == "Invoice" {
			invoice = &app.Entities[i]
			break
		}
	}
	if invoice == nil {
		t.Fatal("expected Invoice entity to be present")
	}

	var customer *model.Field
	var approver *model.Field
	for i := range invoice.Fields {
		switch invoice.Fields[i].Name {
		case "customer":
			customer = &invoice.Fields[i]
		case "approver":
			approver = &invoice.Fields[i]
		}
	}
	if customer == nil || approver == nil {
		t.Fatalf("expected customer and approver belongs_to fields, got %+v", invoice.Fields)
	}
	if customer.RelationEntity != "User" || customer.Optional {
		t.Fatalf("unexpected customer relation field: %+v", *customer)
	}
	if approver.RelationEntity != "User" || !approver.Optional {
		t.Fatalf("unexpected approver relation field: %+v", *approver)
	}
}

func TestParseSupportsBelongsToCurrentUser(t *testing.T) {
	src := `
app PersonalTodo

entity Todo {
  title: String
  belongs_to current_user
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	var todo *model.Entity
	for i := range app.Entities {
		if app.Entities[i].Name == "Todo" {
			todo = &app.Entities[i]
			break
		}
	}
	if todo == nil {
		t.Fatal("expected Todo entity to be present")
	}

	var userField *model.Field
	for i := range todo.Fields {
		if todo.Fields[i].Name == "user" {
			userField = &todo.Fields[i]
			break
		}
	}
	if userField == nil {
		t.Fatal("expected user field to be present")
	}
	if userField.RelationEntity != "User" {
		t.Fatalf("expected relation entity User, got %q", userField.RelationEntity)
	}
	if !userField.CurrentUser {
		t.Fatalf("expected current_user relation flag, got %+v", *userField)
	}
}

func TestParseRejectsBelongsToCurrentUserWithModifiers(t *testing.T) {
	src := `
app PersonalTodo

entity Todo {
  title: String
  belongs_to current_user optional
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for belongs_to current_user modifiers")
	}
	if !strings.Contains(err.Error(), "belongs_to current_user does not support modifiers") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseSupportsNamedBelongsToCurrentUser(t *testing.T) {
	src := `
app PersonalTodo

entity Todo {
  title: String
  belongs_to reviewer: current_user
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	var todo *model.Entity
	for i := range app.Entities {
		if app.Entities[i].Name == "Todo" {
			todo = &app.Entities[i]
			break
		}
	}
	if todo == nil {
		t.Fatal("expected Todo entity to be present")
	}

	var reviewerField *model.Field
	for i := range todo.Fields {
		if todo.Fields[i].Name == "reviewer" {
			reviewerField = &todo.Fields[i]
			break
		}
	}
	if reviewerField == nil {
		t.Fatal("expected reviewer field to be present")
	}
	if reviewerField.RelationEntity != "User" {
		t.Fatalf("expected relation entity User, got %q", reviewerField.RelationEntity)
	}
	if !reviewerField.CurrentUser {
		t.Fatalf("expected current_user relation flag, got %+v", *reviewerField)
	}
}

func TestParseRejectsBelongsToCurrentUserOnUserEntity(t *testing.T) {
	src := `
app PersonalTodo

entity User {
  title: String
  belongs_to current_user
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for User entity using belongs_to current_user")
	}
	if !strings.Contains(err.Error(), "entity User field user cannot use belongs_to current_user") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsBelongsToUnknownEntity(t *testing.T) {
	src := `
app TodoApi

entity Todo {
  title: String
  belongs_to Project
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for unknown belongs_to target")
	}
	if !strings.Contains(err.Error(), "references unknown entity Project") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsHashComments(t *testing.T) {
	src := `
# application
app TodoApi

entity Todo {
  title: String
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for hash comment")
	}
	if !strings.Contains(err.Error(), "unknown statement") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseUnknownTopLevelStatementSuggestsClosestKeyword(t *testing.T) {
	src := `
app TodoApi

entiti Todo {
  title: String
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for unknown top-level statement")
	}
	if !strings.Contains(err.Error(), `Did you mean "entity"?`) {
		t.Fatalf("expected top-level Did you mean suggestion, got: %v", err)
	}
}

func TestParseAuthDefaults(t *testing.T) {
	src := `
app AuthApi

entity User {
  email: String
  role: String
}

auth {
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if app.Auth == nil {
		t.Fatal("expected auth block to be parsed")
	}

	if app.Auth.EmailField != "email" {
		t.Fatalf("unexpected default email_field: %q", app.Auth.EmailField)
	}
	if app.Auth.RoleField != "role" {
		t.Fatalf("unexpected default role_field: %q", app.Auth.RoleField)
	}
	if app.Auth.CodeTTLMinutes != 10 {
		t.Fatalf("unexpected default code_ttl_minutes: %d", app.Auth.CodeTTLMinutes)
	}
	if app.Auth.SessionTTLHours != 24 {
		t.Fatalf("unexpected default session_ttl_hours: %d", app.Auth.SessionTTLHours)
	}
}

func TestParseAuthCodeTTLRejectsOutOfRange(t *testing.T) {
	src := `
app AuthApi

entity User {
  email: String
  role: String
}

auth {
  code_ttl_minutes 0
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for out-of-range auth.code_ttl_minutes")
	}
	if !strings.Contains(err.Error(), "auth.code_ttl_minutes must be between") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseAuthSessionTTLRejectsOutOfRange(t *testing.T) {
	src := `
app AuthApi

entity User {
  email: String
  role: String
}

auth {
  session_ttl_hours 9999
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for out-of-range auth.session_ttl_hours")
	}
	if !strings.Contains(err.Error(), "auth.session_ttl_hours must be between") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseUnknownAuthStatementSuggestsClosestKeyword(t *testing.T) {
	src := `
app AuthApi

entity User {
  email: String
  role: String
}

auth {
  email_subjet "Login"
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for unknown auth statement")
	}
	if !strings.Contains(err.Error(), `Did you mean "email_subject"?`) {
		t.Fatalf("expected auth Did you mean suggestion, got: %v", err)
	}
}

func TestParseMisplacedAuthStatementInSystemShowsHint(t *testing.T) {
	src := `
app Demo

system {
  admin_ui_session_ttl_hours 2
}

entity Todo {
  title: String
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for misplaced auth statement")
	}
	if !strings.Contains(err.Error(), `unknown system statement "admin_ui_session_ttl_hours 2"`) {
		t.Fatalf("unexpected error message: %v", err)
	}
	if !strings.Contains(err.Error(), "Hint:\n  \"admin_ui_session_ttl_hours\" looks like an auth setting. Try moving it into auth { ... }.") {
		t.Fatalf("expected misplaced auth hint, got: %v", err)
	}
}

func TestParseAuthSMTPConfig(t *testing.T) {
	src := `
app AuthApi

auth {
  email_transport smtp
  email_from "no-reply@example.com"
  email_subject "Your login code"
  smtp_host "smtp.example.com"
  smtp_port 587
  smtp_username "resend"
  smtp_password_env "RESEND_API_KEY"
  smtp_starttls true
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if app.Auth == nil {
		t.Fatal("expected auth block to be parsed")
	}
	if app.Auth.EmailTransport != "smtp" {
		t.Fatalf("unexpected email transport: %q", app.Auth.EmailTransport)
	}
	if app.Auth.SMTPHost != "smtp.example.com" {
		t.Fatalf("unexpected smtp_host: %q", app.Auth.SMTPHost)
	}
	if app.Auth.SMTPPort != 587 {
		t.Fatalf("unexpected smtp_port: %d", app.Auth.SMTPPort)
	}
	if app.Auth.SMTPUsername != "resend" {
		t.Fatalf("unexpected smtp_username: %q", app.Auth.SMTPUsername)
	}
	if app.Auth.SMTPPasswordEnv != "RESEND_API_KEY" {
		t.Fatalf("unexpected smtp_password_env: %q", app.Auth.SMTPPasswordEnv)
	}
	if !app.Auth.SMTPStartTLS {
		t.Fatal("expected smtp_starttls true")
	}
}

func TestParseAuthSMTPRequiresHost(t *testing.T) {
	src := `
app AuthApi

auth {
  email_transport smtp
  smtp_username "resend"
  smtp_password_env "RESEND_API_KEY"
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for missing smtp_host")
	}
	if !strings.Contains(err.Error(), "auth.smtp_host is required") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseAuthSMTPRejectsConsoleWithSMTPKeys(t *testing.T) {
	src := `
app AuthApi

auth {
  email_transport console
  smtp_host "smtp.example.com"
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for smtp_host with console transport")
	}
	if !strings.Contains(err.Error(), "auth.smtp_host can only be used") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseAuthorizeAllExpandsToCrudOperations(t *testing.T) {
	src := `
app TodoApi

entity Todo {
  title: String
  authorize all when user_authenticated
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	var todo model.Entity
	var found bool
	for _, entity := range app.Entities {
		if entity.Name == "Todo" {
			todo = entity
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected Todo entity")
	}

	if len(todo.Authorizations) != 4 {
		t.Fatalf("expected 4 expanded authorization rules, got %d", len(todo.Authorizations))
	}

	expected := map[string]string{
		"read":   "user_authenticated",
		"create": "user_authenticated",
		"update": "user_authenticated",
		"delete": "user_authenticated",
	}
	for _, authz := range todo.Authorizations {
		if expected[authz.Action] != authz.Expression {
			t.Fatalf("unexpected authorization for %s: %q", authz.Action, authz.Expression)
		}
	}
}

func TestParseAuthorizeAllAllowsSpecificOverride(t *testing.T) {
	src := `
app TodoApi

entity Todo {
  title: String
  authorize all when user_authenticated
  authorize delete when user_role == "admin"
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	var todo model.Entity
	for _, entity := range app.Entities {
		if entity.Name == "Todo" {
			todo = entity
			break
		}
	}

	expected := map[string]string{
		"read":   "user_authenticated",
		"create": "user_authenticated",
		"update": "user_authenticated",
		"delete": `user_role == "admin"`,
	}
	for _, authz := range todo.Authorizations {
		if expected[authz.Action] != authz.Expression {
			t.Fatalf("unexpected authorization for %s: %q", authz.Action, authz.Expression)
		}
	}
}

func TestParseRuleExpectSyntax(t *testing.T) {
	src := `
app TodoApi

entity Todo {
  title: String
  rule "Title must have at least 3 chars" expect length title >= 3
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	var todo model.Entity
	var found bool
	for _, entity := range app.Entities {
		if entity.Name == "Todo" {
			todo = entity
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected Todo entity")
	}
	if len(todo.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(todo.Rules))
	}
	if todo.Rules[0].Message != "Title must have at least 3 chars" {
		t.Fatalf("unexpected rule message: %q", todo.Rules[0].Message)
	}
	if todo.Rules[0].Expression != "length title >= 3" {
		t.Fatalf("unexpected rule expression: %q", todo.Rules[0].Expression)
	}
}

func TestParseRuleErrorUsesOriginalRuleLine(t *testing.T) {
	src := `
app Demo

entity Student {
  fullName: String

  rule "Student code must have at least 4 chars" expect length externalCode >= 4
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for unknown identifier in rule")
	}
	if !strings.Contains(err.Error(), `line 7: invalid rule expression "length externalCode >= 4" (unknown identifier "externalCode")`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRuleRejectsIncompatibleComparisonTypes(t *testing.T) {
	src := `
app Demo

entity Student {
  name: String

  rule "Name must have between 3 and 100 chars" expect length name >= 3 and name <= 100
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for incompatible comparison types in rule")
	}
	if !strings.Contains(err.Error(), `operator <= expects comparable values, got String and Int`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRuleRejectsNonBooleanExpression(t *testing.T) {
	src := `
app Demo

entity Student {
  name: String

  rule "Name length" expect length name
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for non-boolean rule expression")
	}
	if !strings.Contains(err.Error(), `expression must evaluate to Bool, got Int`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseAuthorizeRejectsIncompatibleBuiltinComparison(t *testing.T) {
	src := `
app Demo

entity Student {
  name: String

  authorize read when user_role <= 10
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for incompatible authorization expression")
	}
	if !strings.Contains(err.Error(), `operator <= expects comparable values, got String and Int`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseUnknownPublicStatementSuggestsClosestKeyword(t *testing.T) {
	src := `
app Demo

public {
  mout "/"
  dir "./dist"
}

entity Todo {
  title: String
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for unknown public statement")
	}
	if !strings.Contains(err.Error(), `Did you mean "mount"?`) {
		t.Fatalf("expected public Did you mean suggestion, got: %v", err)
	}
}

func TestParseUnknownSystemStatementSuggestsClosestKeyword(t *testing.T) {
	src := `
app Demo

system {
  sqlite_buzy_timeout_ms 5000
}

entity Todo {
  title: String
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for unknown system statement")
	}
	if !strings.Contains(err.Error(), `Did you mean "sqlite_busy_timeout_ms"?`) {
		t.Fatalf("expected system Did you mean suggestion, got: %v", err)
	}
}

func TestParseMisplacedSystemStatementInAuthShowsHint(t *testing.T) {
	src := `
app AuthApi

entity User {
  email: String
  role: String
}

auth {
  request_logs_buffer 500
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for misplaced system statement")
	}
	if !strings.Contains(err.Error(), `unknown auth statement "request_logs_buffer 500"`) {
		t.Fatalf("unexpected error message: %v", err)
	}
	if !strings.Contains(err.Error(), "Hint:\n  \"request_logs_buffer\" looks like a system setting. Try moving it into system { ... }.") {
		t.Fatalf("expected misplaced system hint, got: %v", err)
	}
}

func TestParseAuthAdminUISessionTTL(t *testing.T) {
	src := `
app AuthApi

entity User {
  email: String
  role: String
}

auth {
  admin_ui_session_ttl_hours 6
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if app.Auth == nil {
		t.Fatal("expected auth block to be parsed")
	}
	if app.Auth.AdminUISessionTTLHours == nil {
		t.Fatal("expected admin_ui_session_ttl_hours to be parsed")
	}
	if *app.Auth.AdminUISessionTTLHours != 6 {
		t.Fatalf("unexpected admin_ui_session_ttl_hours: %d", *app.Auth.AdminUISessionTTLHours)
	}
}

func TestParseAuthAdminUISessionTTLRejectsOutOfRange(t *testing.T) {
	src := `
app AuthApi

entity User {
  email: String
  role: String
}

auth {
  admin_ui_session_ttl_hours 0
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for out-of-range auth.admin_ui_session_ttl_hours")
	}
	if !strings.Contains(err.Error(), "auth.admin_ui_session_ttl_hours must be between") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseAuthAdminUISessionTTLRejectsNonInteger(t *testing.T) {
	src := `
app AuthApi

entity User {
  email: String
  role: String
}

auth {
  admin_ui_session_ttl_hours 0.01
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for non-integer auth.admin_ui_session_ttl_hours")
	}
	if !strings.Contains(err.Error(), "auth.admin_ui_session_ttl_hours must be an integer between") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseActionTypeMismatchShowsFriendlyError(t *testing.T) {
	src := `
app Demo

entity Book {
  title: String
  price: Float
}

type alias CreateBookInput =
  { title: String
  , price: String
  }

action createBook {
  input: CreateBookInput

  create Book {
    title: input.title
    price: input.price
  }
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for incompatible action field type")
	}

	if !strings.Contains(err.Error(), "action createBook field Book.price expects Float but got String") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseActionUnknownEntityFieldSuggestsClosestName(t *testing.T) {
	src := `
app Demo

entity Todo {
  title: String
}

type alias CreateTodoInput =
  { title: String
  }

action createTodo {
  input: CreateTodoInput

  create Todo {
    titel: input.title
  }
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for unknown action field")
	}
	if !strings.Contains(err.Error(), "Did you mean \"title\"?") {
		t.Fatalf("expected Did you mean suggestion, got: %v", err)
	}
}

func TestParseActionUnknownInputFieldSuggestsClosestName(t *testing.T) {
	src := `
app Demo

entity Todo {
  title: String
}

type alias CreateTodoInput =
  { title: String
  }

action createTodo {
  input: CreateTodoInput

  create Todo {
    title: input.titel
  }
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for unknown action input field")
	}
	if !strings.Contains(err.Error(), "Did you mean \"input.title\"?") {
		t.Fatalf("expected Did you mean suggestion, got: %v", err)
	}
}

func TestParseActionSupportsAliasedLoadAndUpdate(t *testing.T) {
	src := `
app Demo

entity Todo {
  title: String
  done: Bool default false
}

type alias RenameTodoInput =
  { id: Int
  , title: String
  }

action renameTodo {
  input: RenameTodoInput

  todo = load Todo {
    id: input.id
  }

  updatedTodo = update Todo {
    id: todo.id
    title: input.title
  }
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(app.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(app.Actions))
	}
	if got := app.Actions[0].Steps[0].Alias; got != "todo" {
		t.Fatalf("expected first alias todo, got %q", got)
	}
	if got := app.Actions[0].Steps[0].Kind; got != "load" {
		t.Fatalf("expected first step kind load, got %q", got)
	}
	if got := app.Actions[0].Steps[1].Alias; got != "updatedTodo" {
		t.Fatalf("expected second alias updatedTodo, got %q", got)
	}
}

func TestParseActionLoadRequiresAlias(t *testing.T) {
	src := `
app Demo

entity Todo {
  title: String
}

type alias LoadTodoInput =
  { id: Int
  }

action loadTodo {
  input: LoadTodoInput

  load Todo {
    id: input.id
  }

  create Todo {
    title: "x"
  }
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for load without alias")
	}
	if !strings.Contains(err.Error(), "invalid action statement") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseActionSupportsUpdateAndDeleteSteps(t *testing.T) {
	src := `
app Demo

entity Todo {
  title: String
  done: Bool default false
}

type alias ChangeTodoInput =
  { id: Int
  , title: String
  }

action changeTodo {
  input: ChangeTodoInput

  update Todo {
    id: input.id
    title: input.title
  }

  delete Todo {
    id: input.id
  }
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(app.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(app.Actions))
	}
	if got := app.Actions[0].Steps[0].Kind; got != "update" {
		t.Fatalf("expected first step kind update, got %q", got)
	}
	if got := app.Actions[0].Steps[1].Kind; got != "delete" {
		t.Fatalf("expected second step kind delete, got %q", got)
	}
}

func TestParseActionUpdateRequiresPrimaryKey(t *testing.T) {
	src := `
app Demo

entity Todo {
  title: String
}

type alias UpdateTodoInput =
  { title: String
  }

action updateTodo {
  input: UpdateTodoInput

  update Todo {
    title: input.title
  }
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for update without primary key")
	}
	if !strings.Contains(err.Error(), "must include primary key field id") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseActionDeleteOnlyAllowsPrimaryKey(t *testing.T) {
	src := `
app Demo

entity Todo {
  title: String
}

type alias DeleteTodoInput =
  { id: Int
  }

action deleteTodo {
  input: DeleteTodoInput

  delete Todo {
    id: input.id
    title: "x"
  }
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for delete with extra fields")
	}
	if !strings.Contains(err.Error(), "must only include primary key field id") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParsePublicBlock(t *testing.T) {
	src := `
app FrontApi
port 4200
database "./front.db"

public {
  dir "./frontend/dist"
  mount "/"
  spa_fallback "index.html"
}

entity Todo {
  title: String
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if app.Public == nil {
		t.Fatal("expected public block to be parsed")
	}
	if app.Public.Dir != "./frontend/dist" {
		t.Fatalf("unexpected public dir: %q", app.Public.Dir)
	}
	if app.Public.Mount != "/" {
		t.Fatalf("unexpected public mount: %q", app.Public.Mount)
	}
	if app.Public.SPAFallback != "index.html" {
		t.Fatalf("unexpected spa fallback: %q", app.Public.SPAFallback)
	}
}

func TestParsePublicBlockRejectsAbsoluteFallback(t *testing.T) {
	src := `
app FrontApi
database "./front.db"

public {
  dir "./frontend/dist"
  spa_fallback "/index.html"
}

entity Todo {
  title: String
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for invalid public.spa_fallback")
	}
	if !strings.Contains(err.Error(), "public.spa_fallback must be a relative file path") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseSystemRequestLogsBuffer(t *testing.T) {
	src := `
app FrontApi
database "./front.db"

system {
  request_logs_buffer 512
}

entity Todo {
  title: String
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if app.System == nil {
		t.Fatal("expected system block to be parsed")
	}
	if app.System.RequestLogsBuffer != 512 {
		t.Fatalf("unexpected request_logs_buffer: %d", app.System.RequestLogsBuffer)
	}
}

func TestParseSystemRequestLogsBufferRejectsOutOfRange(t *testing.T) {
	src := `
app FrontApi
database "./front.db"

system {
  request_logs_buffer 999999
}

entity Todo {
  title: String
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for out-of-range request_logs_buffer")
	}
	if !strings.Contains(err.Error(), "system.request_logs_buffer must be between") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseSystemAndAuthSettings(t *testing.T) {
	src := `
app FrontApi
database "./front.db"

system {
  sqlite_journal_mode wal
  sqlite_synchronous normal
  sqlite_foreign_keys true
  sqlite_busy_timeout_ms 5000
  sqlite_wal_autocheckpoint 1000
  sqlite_journal_size_limit_mb 64
  sqlite_mmap_size_mb 128
  sqlite_cache_size_kb 2000
  http_max_request_body_mb 1
}

auth {
  auth_request_code_rate_limit_per_minute 5
  auth_login_rate_limit_per_minute 10
  security_frame_policy sameorigin
  security_referrer_policy strict-origin-when-cross-origin
  security_content_type_nosniff true
}

entity Todo {
  title: String
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if app.System == nil {
		t.Fatal("expected system block to be parsed")
	}
	if app.System.SQLiteJournalMode == nil || *app.System.SQLiteJournalMode != "wal" {
		t.Fatalf("unexpected sqlite_journal_mode: %+v", app.System.SQLiteJournalMode)
	}
	if app.System.SQLiteSynchronous == nil || *app.System.SQLiteSynchronous != "normal" {
		t.Fatalf("unexpected sqlite_synchronous: %+v", app.System.SQLiteSynchronous)
	}
	if app.System.SQLiteForeignKeys == nil || !*app.System.SQLiteForeignKeys {
		t.Fatalf("unexpected sqlite_foreign_keys: %+v", app.System.SQLiteForeignKeys)
	}
	if app.Auth.SecurityFramePolicy == nil || *app.Auth.SecurityFramePolicy != "sameorigin" {
		t.Fatalf("unexpected security_frame_policy: %+v", app.Auth.SecurityFramePolicy)
	}
	if app.Auth.SecurityReferrerPolicy == nil || *app.Auth.SecurityReferrerPolicy != "strict-origin-when-cross-origin" {
		t.Fatalf("unexpected security_referrer_policy: %+v", app.Auth.SecurityReferrerPolicy)
	}
	if app.Auth.SecurityContentNoSniff == nil || !*app.Auth.SecurityContentNoSniff {
		t.Fatalf("unexpected security_content_type_nosniff: %+v", app.Auth.SecurityContentNoSniff)
	}
	if app.Auth == nil || app.Auth.AuthRequestCodeRateLimit == nil || *app.Auth.AuthRequestCodeRateLimit != 5 {
		t.Fatalf("unexpected auth_request_code_rate_limit_per_minute: %+v", app.Auth)
	}
	if app.Auth.AuthLoginRateLimit == nil || *app.Auth.AuthLoginRateLimit != 10 {
		t.Fatalf("unexpected auth_login_rate_limit_per_minute: %+v", app.Auth.AuthLoginRateLimit)
	}
	if app.System.SQLiteBusyTimeoutMs == nil || *app.System.SQLiteBusyTimeoutMs != 5000 {
		t.Fatalf("unexpected sqlite_busy_timeout_ms: %+v", app.System.SQLiteBusyTimeoutMs)
	}
	if app.System.SQLiteWALAutoCheckpoint == nil || *app.System.SQLiteWALAutoCheckpoint != 1000 {
		t.Fatalf("unexpected sqlite_wal_autocheckpoint: %+v", app.System.SQLiteWALAutoCheckpoint)
	}
	if app.System.SQLiteJournalSizeLimitMB == nil || *app.System.SQLiteJournalSizeLimitMB != 64 {
		t.Fatalf("unexpected sqlite_journal_size_limit_mb: %+v", app.System.SQLiteJournalSizeLimitMB)
	}
	if app.System.SQLiteMmapSizeMB == nil || *app.System.SQLiteMmapSizeMB != 128 {
		t.Fatalf("unexpected sqlite_mmap_size_mb: %+v", app.System.SQLiteMmapSizeMB)
	}
	if app.System.SQLiteCacheSizeKB == nil || *app.System.SQLiteCacheSizeKB != 2000 {
		t.Fatalf("unexpected sqlite_cache_size_kb: %+v", app.System.SQLiteCacheSizeKB)
	}
	if app.System.HTTPMaxRequestBodyMB == nil || *app.System.HTTPMaxRequestBodyMB != 1 {
		t.Fatalf("unexpected http_max_request_body_mb: %+v", app.System.HTTPMaxRequestBodyMB)
	}
}

func TestParseSystemSQLiteBusyTimeoutRejectsOutOfRange(t *testing.T) {
	src := `
app FrontApi
database "./front.db"

system {
  sqlite_busy_timeout_ms 700000
}

entity Todo {
  title: String
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for out-of-range sqlite_busy_timeout_ms")
	}
	if !strings.Contains(err.Error(), "system.sqlite_busy_timeout_ms must be between") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseSystemSQLiteCacheSizeRejectsOutOfRange(t *testing.T) {
	src := `
app FrontApi
database "./front.db"

system {
  sqlite_cache_size_kb 9999999
}

entity Todo {
  title: String
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for out-of-range sqlite_cache_size_kb")
	}
	if !strings.Contains(err.Error(), "system.sqlite_cache_size_kb must be between") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseSystemHTTPMaxRequestBodyRejectsOutOfRange(t *testing.T) {
	src := `
app FrontApi
database "./front.db"

system {
  http_max_request_body_mb 0
}

entity Todo {
  title: String
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for out-of-range http_max_request_body_mb")
	}
	if !strings.Contains(err.Error(), "system.http_max_request_body_mb must be between") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseAuthRequestCodeRateLimitRejectsOutOfRange(t *testing.T) {
	src := `
app FrontApi
database "./front.db"

auth {
  auth_request_code_rate_limit_per_minute 0
}

entity Todo {
  title: String
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for out-of-range auth_request_code_rate_limit_per_minute")
	}
	if !strings.Contains(err.Error(), "auth.auth_request_code_rate_limit_per_minute must be between") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseAuthLoginRateLimitRejectsOutOfRange(t *testing.T) {
	src := `
app FrontApi
database "./front.db"

auth {
  auth_login_rate_limit_per_minute 0
}

entity Todo {
  title: String
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for out-of-range auth_login_rate_limit_per_minute")
	}
	if !strings.Contains(err.Error(), "auth.auth_login_rate_limit_per_minute must be between") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseAuthSecurityFramePolicyRejectsInvalidValue(t *testing.T) {
	src := `
app FrontApi
database "./front.db"

auth {
  security_frame_policy allow
}

entity Todo {
  title: String
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for invalid security_frame_policy")
	}
	if !strings.Contains(err.Error(), "auth.security_frame_policy must be one of") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseAuthSecurityReferrerPolicyRejectsInvalidValue(t *testing.T) {
	src := `
app FrontApi
database "./front.db"

auth {
  security_referrer_policy unsafe-url
}

entity Todo {
  title: String
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for invalid security_referrer_policy")
	}
	if !strings.Contains(err.Error(), "auth.security_referrer_policy must be one of") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseAuthSecurityContentTypeNoSniffRejectsInvalidValue(t *testing.T) {
	src := `
app FrontApi
database "./front.db"

auth {
  security_content_type_nosniff maybe
}

entity Todo {
  title: String
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for invalid security_content_type_nosniff")
	}
	if !strings.Contains(err.Error(), "auth.security_content_type_nosniff must be true or false") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
