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
  email_transport console
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
	if !strings.Contains(sqlText, "AND") || !strings.Contains(sqlText, "OR") {
		t.Fatalf("expected SQL to preserve logical structure, got %q", sqlText)
	}
	if len(args) == 0 {
		t.Fatalf("expected translated SQL to include bound args, got none for %q", sqlText)
	}
}

func TestListReadAuthorizationWhereFallsBackForFunctionCalls(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "read-auth-sql-fallback.db"), `
app TodoReadFilter

auth {
  email_transport console
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
