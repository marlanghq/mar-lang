package runtime

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEntityCRUDSupportsBelongsToFields(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "belongs-to-crud.db"), `
app StoreApi

entity Book {
  title: String
  authorize read, create, update, delete when anonymous or user_authenticated
}

entity Review {
  body: String
  belongs_to Book
  authorize read, create, update, delete when anonymous or user_authenticated
}
`)

	bookRec := doRuntimeRequest(r, http.MethodPost, "/books", `{"title":"DDD"}`, "")
	if bookRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 when creating book, got %d body=%s", bookRec.Code, bookRec.Body.String())
	}

	reviewRec := doRuntimeRequest(r, http.MethodPost, "/reviews", `{"body":"Great","book":1}`, "")
	if reviewRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 when creating review, got %d body=%s", reviewRec.Code, reviewRec.Body.String())
	}

	var created map[string]any
	if err := json.Unmarshal(reviewRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response failed: %v body=%s", err, reviewRec.Body.String())
	}
	if created["book"] != float64(1) {
		t.Fatalf("expected review response to expose logical belongs_to field, got %#v", created["book"])
	}

	row, ok, err := queryRow(r.DB, `SELECT book_id FROM reviews WHERE id = 1`)
	if err != nil {
		t.Fatalf("query review row failed: %v", err)
	}
	if !ok {
		t.Fatal("expected review row to exist")
	}
	if row["book_id"] != int64(1) {
		t.Fatalf("expected stored foreign key column book_id=1, got %#v", row["book_id"])
	}
}

func TestEntityCRUDSupportsManyToManyViaJoinEntityBelongsTo(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "belongs-to-join.db"), `
app EnrollmentApi

entity Student {
  name: String
  authorize read, create, update, delete when anonymous or user_authenticated
}

entity Course {
  title: String
  authorize read, create, update, delete when anonymous or user_authenticated
}

entity Enrollment {
  belongs_to Student
  belongs_to Course
  authorize read, create, update, delete when anonymous or user_authenticated
}
`)

	if rec := doRuntimeRequest(r, http.MethodPost, "/students", `{"name":"Mia"}`, ""); rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 when creating student, got %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := doRuntimeRequest(r, http.MethodPost, "/courses", `{"title":"Math"}`, ""); rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 when creating course, got %d body=%s", rec.Code, rec.Body.String())
	}

	rec := doRuntimeRequest(r, http.MethodPost, "/enrollments", `{"student":1,"course":1}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 when creating enrollment, got %d body=%s", rec.Code, rec.Body.String())
	}

	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response failed: %v body=%s", err, rec.Body.String())
	}
	if created["student"] != float64(1) || created["course"] != float64(1) {
		t.Fatalf("expected join entity response to expose logical belongs_to fields, got %#v", created)
	}
}

func TestReadAuthorizationFiltersListAndProtectsGet(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "read-filter.db"), `
app TodoReadFilter

auth {
}

entity Todo {
  title: String
  belongs_to User

  authorize read when user_authenticated and (user == user_id or user_role == "admin")
  authorize create when user_authenticated and user == user_id
  authorize update when user_authenticated and (user == user_id or user_role == "admin")
  authorize delete when user_authenticated and (user == user_id or user_role == "admin")
}
`)

	adminCode := requestCodeAndUseKnownCode(t, r, "owner@example.com")
	adminToken := loginWithCodeAndReadToken(t, r, "owner@example.com", adminCode)

	memberCode := requestCodeAndUseKnownCode(t, r, "member@example.com")
	memberToken := loginWithCodeAndReadToken(t, r, "member@example.com", memberCode)

	adminRow, found, err := r.loadAuthUserByEmail("", "owner@example.com")
	if err != nil {
		t.Fatalf("load admin user failed: %v", err)
	}
	if !found {
		t.Fatal("expected admin user to exist")
	}
	memberRow, found, err := r.loadAuthUserByEmail("", "member@example.com")
	if err != nil {
		t.Fatalf("load member user failed: %v", err)
	}
	if !found {
		t.Fatal("expected member user to exist")
	}

	adminID := adminRow[r.authUser.PrimaryKey]
	memberID := memberRow[r.authUser.PrimaryKey]

	if rec := doRuntimeRequest(r, http.MethodPost, "/todos", fmt.Sprintf(`{"title":"Admin todo","user":%v}`, adminID), adminToken); rec.Code != http.StatusCreated {
		t.Fatalf("expected admin todo create to succeed, got %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := doRuntimeRequest(r, http.MethodPost, "/todos", fmt.Sprintf(`{"title":"Member todo","user":%v}`, memberID), memberToken); rec.Code != http.StatusCreated {
		t.Fatalf("expected member todo create to succeed, got %d body=%s", rec.Code, rec.Body.String())
	}

	memberListRec := doRuntimeRequest(r, http.MethodGet, "/todos", "", memberToken)
	if memberListRec.Code != http.StatusOK {
		t.Fatalf("expected member todo list to succeed, got %d body=%s", memberListRec.Code, memberListRec.Body.String())
	}
	var memberRows []map[string]any
	if err := json.Unmarshal(memberListRec.Body.Bytes(), &memberRows); err != nil {
		t.Fatalf("decode member list failed: %v body=%s", err, memberListRec.Body.String())
	}
	if len(memberRows) != 1 || memberRows[0]["title"] != "Member todo" {
		t.Fatalf("expected member list to be filtered to own row, got %#v", memberRows)
	}

	adminListRec := doRuntimeRequest(r, http.MethodGet, "/todos", "", adminToken)
	if adminListRec.Code != http.StatusOK {
		t.Fatalf("expected admin todo list to succeed, got %d body=%s", adminListRec.Code, adminListRec.Body.String())
	}
	var adminRows []map[string]any
	if err := json.Unmarshal(adminListRec.Body.Bytes(), &adminRows); err != nil {
		t.Fatalf("decode admin list failed: %v body=%s", err, adminListRec.Body.String())
	}
	if len(adminRows) != 2 {
		t.Fatalf("expected admin list to see all rows, got %#v", adminRows)
	}

	memberGetAdminRec := doRuntimeRequest(r, http.MethodGet, fmt.Sprintf("/todos/%v", adminRows[1]["id"]), "", memberToken)
	if memberGetAdminRec.Code != http.StatusForbidden {
		t.Fatalf("expected member get on foreign todo to be forbidden, got %d body=%s", memberGetAdminRec.Code, memberGetAdminRec.Body.String())
	}
}

func TestEntityCRUDSupportsBelongsToCurrentUser(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "belongs-to-current-user.db"), `
app PersonalTodo

auth {
}

entity Todo {
  title: String
  belongs_to current_user

  authorize read when user_authenticated and (user == user_id or user_role == "admin")
  authorize create when user_authenticated
  authorize update when user_authenticated and (user == user_id or user_role == "admin")
  authorize delete when user_authenticated and (user == user_id or user_role == "admin")
}
`)

	loginCode := requestCodeAndUseKnownCode(t, r, "owner@example.com")
	token := loginWithCodeAndReadToken(t, r, "owner@example.com", loginCode)

	userRow, found, err := r.loadAuthUserByEmail("", "owner@example.com")
	if err != nil {
		t.Fatalf("load auth user failed: %v", err)
	}
	if !found {
		t.Fatal("expected auth user to exist")
	}
	userID := userRow[r.authUser.PrimaryKey]
	userIDInt, ok := toInt64(userID)
	if !ok {
		t.Fatalf("expected auth user id to be Int, got %#v", userID)
	}

	rec := doRuntimeRequest(r, http.MethodPost, "/todos", `{"title":"Mine"}`, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 when creating todo, got %d body=%s", rec.Code, rec.Body.String())
	}

	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response failed: %v body=%s", err, rec.Body.String())
	}
	if created["user"] != float64(userIDInt) {
		t.Fatalf("expected created todo to belong to current user, got %#v", created["user"])
	}

	row, ok, err := queryRow(r.DB, `SELECT user_id FROM todos WHERE id = 1`)
	if err != nil {
		t.Fatalf("query todo row failed: %v", err)
	}
	if !ok {
		t.Fatal("expected todo row to exist")
	}
	if row["user_id"] != userIDInt {
		t.Fatalf("expected stored user_id=%#v, got %#v", userIDInt, row["user_id"])
	}
}

func TestEntityCRUDRejectsManualPayloadForBelongsToCurrentUser(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "belongs-to-current-user-reject.db"), `
app PersonalTodo

auth {
}

entity Todo {
  title: String
  belongs_to current_user

  authorize create when user_authenticated
  authorize read when user_authenticated and user == user_id
  authorize update when user_authenticated and user == user_id
  authorize delete when user_authenticated and user == user_id
}
`)

	loginCode := requestCodeAndUseKnownCode(t, r, "owner@example.com")
	token := loginWithCodeAndReadToken(t, r, "owner@example.com", loginCode)

	createRec := doRuntimeRequest(r, http.MethodPost, "/todos", `{"title":"Mine","user":999}`, token)
	if createRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when providing managed field on create, got %d body=%s", createRec.Code, createRec.Body.String())
	}
	if !strings.Contains(createRec.Body.String(), "managed automatically") {
		t.Fatalf("expected managed field error on create, got body=%s", createRec.Body.String())
	}

	validCreateRec := doRuntimeRequest(r, http.MethodPost, "/todos", `{"title":"Mine"}`, token)
	if validCreateRec.Code != http.StatusCreated {
		t.Fatalf("expected valid create to succeed, got %d body=%s", validCreateRec.Code, validCreateRec.Body.String())
	}

	updateRec := doRuntimeRequest(r, http.MethodPatch, "/todos/1", `{"user":999}`, token)
	if updateRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when providing managed field on update, got %d body=%s", updateRec.Code, updateRec.Body.String())
	}
	if !strings.Contains(updateRec.Body.String(), "managed automatically") {
		t.Fatalf("expected managed field error on update, got body=%s", updateRec.Body.String())
	}
}

func TestEntityCRUDSupportsNamedBelongsToCurrentUser(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "belongs-to-named-current-user.db"), `
app BookReviews

auth {
}

entity Review {
  rating: Int
  belongs_to reviewer: current_user

  authorize read when user_authenticated and reviewer == user_id
  authorize create when user_authenticated
  authorize update when user_authenticated and reviewer == user_id
  authorize delete when user_authenticated and reviewer == user_id
}
`)

	loginCode := requestCodeAndUseKnownCode(t, r, "reviewer@example.com")
	token := loginWithCodeAndReadToken(t, r, "reviewer@example.com", loginCode)

	userRow, found, err := r.loadAuthUserByEmail("", "reviewer@example.com")
	if err != nil {
		t.Fatalf("load auth user failed: %v", err)
	}
	if !found {
		t.Fatal("expected auth user to exist")
	}
	userID := userRow[r.authUser.PrimaryKey]
	userIDInt, ok := toInt64(userID)
	if !ok {
		t.Fatalf("expected auth user id to be Int, got %#v", userID)
	}

	createRec := doRuntimeRequest(r, http.MethodPost, "/reviews", `{"rating":5}`, token)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 when creating review, got %d body=%s", createRec.Code, createRec.Body.String())
	}

	var created map[string]any
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response failed: %v body=%s", err, createRec.Body.String())
	}
	if created["reviewer"] != float64(userIDInt) {
		t.Fatalf("expected created review to belong to current reviewer, got %#v", created["reviewer"])
	}

	row, ok, err := queryRow(r.DB, `SELECT reviewer_id FROM reviews WHERE id = 1`)
	if err != nil {
		t.Fatalf("query review row failed: %v", err)
	}
	if !ok {
		t.Fatal("expected review row to exist")
	}
	if row["reviewer_id"] != userIDInt {
		t.Fatalf("expected stored reviewer_id=%#v, got %#v", userIDInt, row["reviewer_id"])
	}

	updateRec := doRuntimeRequest(r, http.MethodPatch, "/reviews/1", `{"reviewer":999}`, token)
	if updateRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when providing managed named field on update, got %d body=%s", updateRec.Code, updateRec.Body.String())
	}
	if !strings.Contains(updateRec.Body.String(), "managed automatically") {
		t.Fatalf("expected managed field error on update, got body=%s", updateRec.Body.String())
	}
}

func TestEntityCRUDAutoTimestamps(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "timestamps.db"), `
app TimestampApi

entity Todo {
  title: String
  authorize read, create, update, delete when anonymous or user_authenticated
}
`)

	createRec := doRuntimeRequest(r, http.MethodPost, "/todos", `{"title":"First"}`, "")
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 when creating todo, got %d body=%s", createRec.Code, createRec.Body.String())
	}

	var created map[string]any
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response failed: %v body=%s", err, createRec.Body.String())
	}
	createdAt, createdAtOK := created["created_at"].(float64)
	updatedAt, updatedAtOK := created["updated_at"].(float64)
	if !createdAtOK || !updatedAtOK {
		t.Fatalf("expected create response to include timestamps, got %#v", created)
	}
	if createdAt <= 0 || updatedAt <= 0 {
		t.Fatalf("expected positive timestamps, got created_at=%v updated_at=%v", createdAt, updatedAt)
	}

	row, ok, err := queryRow(r.DB, `SELECT created_at, updated_at FROM todos WHERE id = 1`)
	if err != nil {
		t.Fatalf("query todo row failed: %v", err)
	}
	if !ok {
		t.Fatal("expected todo row to exist")
	}
	initialUpdatedAt := row["updated_at"]

	time.Sleep(2 * time.Millisecond)

	updateRec := doRuntimeRequest(r, http.MethodPatch, "/todos/1", `{"title":"Second"}`, "")
	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected 200 when updating todo, got %d body=%s", updateRec.Code, updateRec.Body.String())
	}

	updatedRow, ok, err := queryRow(r.DB, `SELECT created_at, updated_at FROM todos WHERE id = 1`)
	if err != nil {
		t.Fatalf("query updated todo row failed: %v", err)
	}
	if !ok {
		t.Fatal("expected updated todo row to exist")
	}
	if updatedRow["created_at"] != row["created_at"] {
		t.Fatalf("expected created_at to remain stable, got before=%#v after=%#v", row["created_at"], updatedRow["created_at"])
	}
	if updatedRow["updated_at"] == initialUpdatedAt {
		t.Fatalf("expected updated_at to change on update, got %#v", updatedRow["updated_at"])
	}
}
