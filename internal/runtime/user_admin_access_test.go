package runtime

import (
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"mar/internal/parser"
)

func TestAdminHasBuiltInAccessToUserEntity(t *testing.T) {
	requireSQLite3(t)

	src := `
app UserAdminAccess

entity User {
  displayName: String optional
}

entity Todo {
  title: String

  authorize all when auth_authenticated
}

auth {
  email_transport console
}
`

	app, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	app.Database = filepath.Join(t.TempDir(), "user-admin-access.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer r.Close()

	adminCode := requestCodeAndUseKnownCode(t, r, "owner@example.com")
	adminToken := loginWithCodeAndReadToken(t, r, "owner@example.com", adminCode)

	userCode := requestCodeAndUseKnownCode(t, r, "member@example.com")
	userToken := loginWithCodeAndReadToken(t, r, "member@example.com", userCode)

	memberRow, found, err := r.loadAuthUserByEmail("", "member@example.com")
	if err != nil {
		t.Fatalf("load auth user failed: %v", err)
	}
	if !found {
		t.Fatal("expected member user to exist")
	}
	memberID := memberRow[r.authUser.PrimaryKey]

	listRec := doRuntimeRequest(r, http.MethodGet, "/users", "", adminToken)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected admin to list users, got %d body=%s", listRec.Code, listRec.Body.String())
	}

	getRec := doRuntimeRequest(r, http.MethodGet, "/users/"+fmt.Sprint(memberID), "", adminToken)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected admin to get user, got %d body=%s", getRec.Code, getRec.Body.String())
	}

	updateRec := doRuntimeRequest(r, http.MethodPatch, "/users/"+fmt.Sprint(memberID), `{"displayName":"Member name"}`, adminToken)
	if updateRec.Code != http.StatusForbidden {
		t.Fatalf("expected admin update to remain forbidden without explicit authorize, got %d body=%s", updateRec.Code, updateRec.Body.String())
	}

	memberListRec := doRuntimeRequest(r, http.MethodGet, "/users", "", userToken)
	if memberListRec.Code != http.StatusForbidden {
		t.Fatalf("expected regular user list to remain forbidden, got %d body=%s", memberListRec.Code, memberListRec.Body.String())
	}
}
