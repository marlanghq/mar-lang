package formatter

import (
	"strings"
	"testing"
)

func TestFormatIsIdempotent(t *testing.T) {
	src := `
app   TodoApi
entity Todo{
title:String
done:Bool optional
}
`

	once, err := Format(src)
	if err != nil {
		t.Fatalf("first format failed: %v", err)
	}
	twice, err := Format(once)
	if err != nil {
		t.Fatalf("second format failed: %v", err)
	}

	if once != twice {
		t.Fatalf("formatter is not idempotent\n--- once ---\n%s\n--- twice ---\n%s", once, twice)
	}
}

func TestFormatCanonicalOutput(t *testing.T) {
	src := `
app TodoApi
entity Todo{
-- user-facing title
title:String
published_at:DateTime optional
done:Bool optional
}
`

	formatted, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}

	expected := "" +
		"app TodoApi\n" +
		"entity Todo {\n" +
		"  -- user-facing title\n" +
		"  title: String\n" +
		"  published_at: DateTime optional\n" +
		"  done: Bool optional\n" +
		"}\n"

	if formatted != expected {
		t.Fatalf("unexpected formatted output\n--- expected ---\n%s\n--- got ---\n%s", expected, formatted)
	}
}

func TestFormatKeepsStringDefaultLiteralsIntact(t *testing.T) {
	src := `
app TodoApi
entity Todo{
title:String default   "hello   world"
done:Bool default false
}
`

	formatted, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}

	expected := "" +
		"app TodoApi\n" +
		"entity Todo {\n" +
		"  title: String default \"hello   world\"\n" +
		"  done: Bool default false\n" +
		"}\n"

	if formatted != expected {
		t.Fatalf("unexpected formatted output\n--- expected ---\n%s\n--- got ---\n%s", expected, formatted)
	}
}

func TestFormatBelongsToCanonicalOutput(t *testing.T) {
	src := `
app BillingApi
entity Customer {
name:String
}
entity Invoice{
total:Float
belongs_to customer:Customer optional
belongs_to reviewer:current_user
belongs_to current_user
}
`

	formatted, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}

	expected := "" +
		"app BillingApi\n" +
		"entity Customer {\n" +
		"  name: String\n" +
		"}\n" +
		"entity Invoice {\n" +
		"  total: Float\n" +
		"  belongs_to customer: Customer optional\n" +
		"  belongs_to reviewer: current_user\n" +
		"  belongs_to current_user\n" +
		"}\n"

	if formatted != expected {
		t.Fatalf("unexpected formatted output\n--- expected ---\n%s\n--- got ---\n%s", expected, formatted)
	}
}

func TestFormatInvalidSourceReturnsParserError(t *testing.T) {
	src := `
app Broken
entity Todo {
  title String
}
`

	_, err := Format(src)
	if err == nil {
		t.Fatal("expected format to fail for invalid source")
	}
	if !strings.Contains(err.Error(), "invalid entity statement") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFormatActionBlockCanonicalOutput(t *testing.T) {
	src := `
app Demo
entity Todo{
title:String
}
type alias CreateTodoInput=
{title:Int}
action createTodo{
input:CreateTodoInput
create Todo{
title: input.title
}
}
`

	_, err := Format(src)
	if err == nil {
		t.Fatal("expected format to fail for invalid action payload type")
	}

	valid := `
app Demo
entity Todo{
title:String
}
type alias CreateTodoInput=
{title:String}
action createTodo{
input:CreateTodoInput
create Todo{
title:input.title
}
}
`

	formatted, err := Format(valid)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}

	expected := "" +
		"app Demo\n" +
		"entity Todo {\n" +
		"  title: String\n" +
		"}\n" +
		"type alias CreateTodoInput =\n" +
		"  {title:String}\n" +
		"action createTodo {\n" +
		"  input: CreateTodoInput\n" +
		"  create Todo {\n" +
		"    title: input.title\n" +
		"  }\n" +
		"}\n"

	if formatted != expected {
		t.Fatalf("unexpected formatted output\n--- expected ---\n%s\n--- got ---\n%s", expected, formatted)
	}
}

func TestFormatActionUpdateDeleteCanonicalOutput(t *testing.T) {
	valid := `
app Demo
entity Todo{
title:String
done:Bool default false
}
type alias ChangeTodoInput=
{id:Int}
action changeTodo{
input:ChangeTodoInput
update Todo{
id:input.id
done:true
}
delete Todo{
id:input.id
}
}
`

	formatted, err := Format(valid)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}

	expected := "" +
		"app Demo\n" +
		"entity Todo {\n" +
		"  title: String\n" +
		"  done: Bool default false\n" +
		"}\n" +
		"type alias ChangeTodoInput =\n" +
		"  {id:Int}\n" +
		"action changeTodo {\n" +
		"  input: ChangeTodoInput\n" +
		"  update Todo {\n" +
		"    id: input.id\n" +
		"    done: true\n" +
		"  }\n" +
		"  delete Todo {\n" +
		"    id: input.id\n" +
		"  }\n" +
		"}\n"

	if formatted != expected {
		t.Fatalf("unexpected formatted output\n--- expected ---\n%s\n--- got ---\n%s", expected, formatted)
	}
}

func TestFormatPublicBlockCanonicalOutput(t *testing.T) {
	src := `
app FrontApi
database "./front.db"
public {
dir    "./frontend/dist"
mount   "/"
spa_fallback   "index.html"
}
entity Todo{
title:String
}
`

	formatted, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}

	expected := "" +
		"app FrontApi\n" +
		"database \"./front.db\"\n" +
		"public {\n" +
		"  dir \"./frontend/dist\"\n" +
		"  mount \"/\"\n" +
		"  spa_fallback \"index.html\"\n" +
		"}\n" +
		"entity Todo {\n" +
		"  title: String\n" +
		"}\n"

	if formatted != expected {
		t.Fatalf("unexpected formatted output\n--- expected ---\n%s\n--- got ---\n%s", expected, formatted)
	}
}

func TestFormatSystemBlockCanonicalOutput(t *testing.T) {
	src := `
app FrontApi
database "./front.db"
system {
request_logs_buffer    500
security_frame_policy deny
security_referrer_policy no-referrer
security_content_type_nosniff false
sqlite_journal_mode   wal
sqlite_synchronous normal
sqlite_foreign_keys   true
sqlite_busy_timeout_ms  5000
sqlite_wal_autocheckpoint 1000
sqlite_journal_size_limit_mb 64
sqlite_mmap_size_mb  128
sqlite_cache_size_kb 2000
http_max_request_body_mb 1
}
entity Todo{
title:String
}
`

	formatted, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}

	expected := "" +
		"app FrontApi\n" +
		"database \"./front.db\"\n" +
		"system {\n" +
		"  request_logs_buffer 500\n" +
		"  security_frame_policy deny\n" +
		"  security_referrer_policy no-referrer\n" +
		"  security_content_type_nosniff false\n" +
		"  sqlite_journal_mode wal\n" +
		"  sqlite_synchronous normal\n" +
		"  sqlite_foreign_keys true\n" +
		"  sqlite_busy_timeout_ms 5000\n" +
		"  sqlite_wal_autocheckpoint 1000\n" +
		"  sqlite_journal_size_limit_mb 64\n" +
		"  sqlite_mmap_size_mb 128\n" +
		"  sqlite_cache_size_kb 2000\n" +
		"  http_max_request_body_mb 1\n" +
		"}\n" +
		"entity Todo {\n" +
		"  title: String\n" +
		"}\n"

	if formatted != expected {
		t.Fatalf("unexpected formatted output\n--- expected ---\n%s\n--- got ---\n%s", expected, formatted)
	}
}

func TestFormatPreservesRuleExpectSyntax(t *testing.T) {
	src := `
app TodoApi
entity Todo {
title:String
rule "Title must have at least 3 chars" expect length title >= 3
}
`

	formatted, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}

	expected := "" +
		"app TodoApi\n" +
		"entity Todo {\n" +
		"  title: String\n" +
		"  rule \"Title must have at least 3 chars\" expect length title >= 3\n" +
		"}\n"

	if formatted != expected {
		t.Fatalf("unexpected formatted output\n--- expected ---\n%s\n--- got ---\n%s", expected, formatted)
	}
}
