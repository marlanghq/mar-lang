package parser

import (
	"strings"
	"testing"
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
	if len(app.Entities) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(app.Entities))
	}

	book := app.Entities[0]
	if book.Table != "books" {
		t.Fatalf("unexpected table: %q", book.Table)
	}
	if book.Resource != "/books" {
		t.Fatalf("unexpected resource: %q", book.Resource)
	}
	if book.PrimaryKey != "id" {
		t.Fatalf("expected derived primary key id, got %q", book.PrimaryKey)
	}
	if len(book.Fields) != 3 {
		t.Fatalf("expected 3 fields (including derived id), got %d", len(book.Fields))
	}
	if book.Fields[0].Name != "id" || !book.Fields[0].Primary || !book.Fields[0].Auto {
		t.Fatalf("expected first field to be derived auto primary id, got %+v", book.Fields[0])
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

func TestParseAuthDefaults(t *testing.T) {
	src := `
app AuthApi

entity User {
  email: String
  role: String
}

auth {
  user_entity User
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
	if app.Auth.DevExposeCode {
		t.Fatalf("unexpected default dev_expose_code: %v", app.Auth.DevExposeCode)
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
  user_entity User
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
  user_entity User
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

func TestParseSystemAdminUISessionTTL(t *testing.T) {
	src := `
app AuthApi

system {
  admin_ui_session_ttl_hours 6
}

entity User {
  email: String
  role: String
}

auth {
  user_entity User
}
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if app.System == nil {
		t.Fatal("expected system block to be parsed")
	}
	if app.System.AdminUISessionTTLHours == nil {
		t.Fatal("expected admin_ui_session_ttl_hours to be parsed")
	}
	if *app.System.AdminUISessionTTLHours != 6 {
		t.Fatalf("unexpected admin_ui_session_ttl_hours: %d", *app.System.AdminUISessionTTLHours)
	}
}

func TestParseSystemAdminUISessionTTLRejectsOutOfRange(t *testing.T) {
	src := `
app AuthApi

system {
  admin_ui_session_ttl_hours 0
}

entity User {
  email: String
  role: String
}

auth {
  user_entity User
}
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error for out-of-range system.admin_ui_session_ttl_hours")
	}
	if !strings.Contains(err.Error(), "system.admin_ui_session_ttl_hours must be between") {
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
	if !strings.Contains(err.Error(), "Did you mean \"title\"?") {
		t.Fatalf("expected Did you mean suggestion, got: %v", err)
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

func TestParseSystemSQLiteSettings(t *testing.T) {
	src := `
app FrontApi
database "./front.db"

system {
  security_frame_policy sameorigin
  security_referrer_policy strict-origin-when-cross-origin
  security_content_type_nosniff true
  sqlite_journal_mode wal
  sqlite_synchronous normal
  sqlite_foreign_keys true
  auth_request_code_rate_limit_per_minute 5
  auth_login_rate_limit_per_minute 10
  sqlite_busy_timeout_ms 5000
  sqlite_wal_autocheckpoint 1000
  sqlite_journal_size_limit_mb 64
  sqlite_mmap_size_mb 128
  sqlite_cache_size_kb 2000
  http_max_request_body_mb 1
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
	if app.System.SecurityFramePolicy == nil || *app.System.SecurityFramePolicy != "sameorigin" {
		t.Fatalf("unexpected security_frame_policy: %+v", app.System.SecurityFramePolicy)
	}
	if app.System.SecurityReferrerPolicy == nil || *app.System.SecurityReferrerPolicy != "strict-origin-when-cross-origin" {
		t.Fatalf("unexpected security_referrer_policy: %+v", app.System.SecurityReferrerPolicy)
	}
	if app.System.SecurityContentNoSniff == nil || !*app.System.SecurityContentNoSniff {
		t.Fatalf("unexpected security_content_type_nosniff: %+v", app.System.SecurityContentNoSniff)
	}
	if app.System.AuthRequestCodeRateLimit == nil || *app.System.AuthRequestCodeRateLimit != 5 {
		t.Fatalf("unexpected auth_request_code_rate_limit_per_minute: %+v", app.System.AuthRequestCodeRateLimit)
	}
	if app.System.AuthLoginRateLimit == nil || *app.System.AuthLoginRateLimit != 10 {
		t.Fatalf("unexpected auth_login_rate_limit_per_minute: %+v", app.System.AuthLoginRateLimit)
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

func TestParseSystemAuthRequestCodeRateLimitRejectsOutOfRange(t *testing.T) {
	src := `
app FrontApi
database "./front.db"

system {
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
	if !strings.Contains(err.Error(), "system.auth_request_code_rate_limit_per_minute must be between") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseSystemAuthLoginRateLimitRejectsOutOfRange(t *testing.T) {
	src := `
app FrontApi
database "./front.db"

system {
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
	if !strings.Contains(err.Error(), "system.auth_login_rate_limit_per_minute must be between") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseSystemSecurityFramePolicyRejectsInvalidValue(t *testing.T) {
	src := `
app FrontApi
database "./front.db"

system {
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
	if !strings.Contains(err.Error(), "system.security_frame_policy must be one of") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseSystemSecurityReferrerPolicyRejectsInvalidValue(t *testing.T) {
	src := `
app FrontApi
database "./front.db"

system {
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
	if !strings.Contains(err.Error(), "system.security_referrer_policy must be one of") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestParseSystemSecurityContentTypeNoSniffRejectsInvalidValue(t *testing.T) {
	src := `
app FrontApi
database "./front.db"

system {
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
	if !strings.Contains(err.Error(), "system.security_content_type_nosniff must be true or false") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
