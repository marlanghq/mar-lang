package runtime

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestListReadAuthorizationWhereTranslatesSimpleOwnedReadRule(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "read-auth-sql.db"), `
app TodoReadFilter

auth {
}

entity Todo {
  title: String
  belongs_to User

  authorize read when user_authenticated and (user == user_id or user_role == "admin")
}
`)

	entity := r.entitiesByName["Todo"]
	if entity == nil {
		t.Fatal("expected Todo entity")
	}

	sqlText, args, ok := r.listReadAuthorizationWhere(entity, authSession{
		Authenticated: true,
		UserID:        int64(42),
		Role:          "member",
	})
	if !ok {
		t.Fatal("expected read authorization to translate to SQL")
	}
	if !strings.Contains(sqlText, `"user_id"`) {
		t.Fatalf("expected SQL to reference storage column user_id, got %q", sqlText)
	}
	if len(args) == 0 {
		t.Fatalf("expected translated SQL to include bound args, got none for %q", sqlText)
	}
	if strings.Contains(sqlText, "==") {
		t.Fatalf("expected SQL equality operator to use =, got %q", sqlText)
	}
	if strings.Contains(sqlText, "AND") || strings.Contains(sqlText, "OR") {
		t.Fatalf("expected SQL to simplify away constant logical branches, got %q", sqlText)
	}
	if strings.Contains(sqlText, "admin") {
		t.Fatalf("expected SQL to simplify away the admin comparison for non-admin users, got %q", sqlText)
	}
	if got, want := sqlText, `("user_id" = ?)`; got != want {
		t.Fatalf("expected simplified SQL %q, got %q", want, got)
	}
	if len(args) != 1 || args[0] != int64(42) {
		t.Fatalf("expected bound args [42], got %v", args)
	}
}

func TestListReadAuthorizationWhereFallsBackForFunctionCalls(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "read-auth-sql-fallback.db"), `
app TodoReadFilter

auth {
}

entity Todo {
  title: String

  authorize read when starts_with "Admin" title
}
`)

	entity := r.entitiesByName["Todo"]
	if entity == nil {
		t.Fatal("expected Todo entity")
	}

	if _, _, ok := r.listReadAuthorizationWhere(entity, authSession{Authenticated: true}); ok {
		t.Fatal("expected function-based authorization to fall back to in-memory filtering")
	}
}

func TestListReadAuthorizationWhereOmitsWhereForAlwaysTrueAdminRule(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "read-auth-sql-admin.db"), `
app TodoReadFilter

auth {
}

entity Todo {
  title: String
  belongs_to User

  authorize read when user_authenticated and (user == user_id or user_role == "admin")
}
`)

	entity := r.entitiesByName["Todo"]
	if entity == nil {
		t.Fatal("expected Todo entity")
	}

	if sqlText, args, ok := r.listReadAuthorizationWhere(entity, authSession{
		Authenticated: true,
		UserID:        int64(1),
		Role:          "admin",
	}); ok || sqlText != "" || len(args) != 0 {
		t.Fatalf("expected always-true admin rule to omit WHERE, got sql=%q args=%v ok=%v", sqlText, args, ok)
	}
}

func TestListReadAuthorizationWhereOmitsWhereForAnonymousOrAuthenticatedRule(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "read-auth-sql-public.db"), `
app TodoReadFilter

auth {
}

entity Todo {
  title: String

  authorize read when anonymous or user_authenticated
}
`)

	entity := r.entitiesByName["Todo"]
	if entity == nil {
		t.Fatal("expected Todo entity")
	}

	if sqlText, args, ok := r.listReadAuthorizationWhere(entity, authSession{}); ok || sqlText != "" || len(args) != 0 {
		t.Fatalf("expected anonymous or authenticated rule to omit WHERE for anonymous access, got sql=%q args=%v ok=%v", sqlText, args, ok)
	}

	if sqlText, args, ok := r.listReadAuthorizationWhere(entity, authSession{Authenticated: true, UserID: int64(1), Role: "member"}); ok || sqlText != "" || len(args) != 0 {
		t.Fatalf("expected anonymous or authenticated rule to omit WHERE for authenticated access, got sql=%q args=%v ok=%v", sqlText, args, ok)
	}
}
