package typecheck

import (
	"strings"
	"testing"
)

// String literal coerces to Path r when the value declaration carries
// a `Path { ... }` annotation. The row's fields must match the
// `{name:Type}` segments exactly — no more, no less.
func TestCheckPathFromStringLiteral(t *testing.T) {
	src := `module M exposing (..)

notesDetail : Path { id : Int }
notesDetail = "/notes/{id:Int}"
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := res.ValueTypes["notesDetail"].String()
	if !strings.Contains(got, "Path") {
		t.Fatalf("expected Path in type, got: %s", got)
	}
	if !strings.Contains(got, "id : Int") {
		t.Fatalf("expected `id : Int` field in row, got: %s", got)
	}
}

func TestCheckPathFromStringLiteral_emptyParams(t *testing.T) {
	// Static-only path: no `{name:Type}` segments → empty closed row.
	src := `module M exposing (..)

home : Path {}
home = "/"
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestCheckPathRejectsMismatchedAnnotation(t *testing.T) {
	// Annotation says id:Int but the literal has no id segment.
	src := `module M exposing (..)

bad : Path { id : Int }
bad = "/notes"
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected error for mismatched annotation, got nil")
	}
}

func TestCheckPathRejectsBareColon(t *testing.T) {
	src := `module M exposing (..)

bad : Path { id : Int }
bad = "/notes/:id"
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected error for bare :id syntax")
	}
	if !strings.Contains(err.Error(), "{id:Type}") {
		t.Errorf("error should suggest {id:Type}, got: %v", err)
	}
}

// linkTo + Nav.pushTo / Nav.replaceTo unify against any Path r.
func TestCheckLinkTo_typedFromPath(t *testing.T) {
	src := `module M exposing (..)

notesDetail : Path { id : Int }
notesDetail = "/notes/{id:Int}"

href = linkTo notesDetail { id = 42 }
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got := res.ValueTypes["href"].String(); got != "String" {
		t.Fatalf("href: want String, got %s", got)
	}
}

func TestCheckLinkTo_rejectsWrongFieldType(t *testing.T) {
	// id is Int in the path but we pass a String here — should fail.
	src := `module M exposing (..)

notesDetail : Path { id : Int }
notesDetail = "/notes/{id:Int}"

href = linkTo notesDetail { id = "abc" }
`
	if _, err := checkSource(t, src); err == nil {
		t.Fatal("expected error for String passed where Int is required")
	}
}

func TestCheckLinkTo_rejectsExtraField(t *testing.T) {
	src := `module M exposing (..)

notesDetail : Path { id : Int }
notesDetail = "/notes/{id:Int}"

href = linkTo notesDetail { id = 42, slug = "extra" }
`
	if _, err := checkSource(t, src); err == nil {
		t.Fatal("expected error for extra field in params record")
	}
}

// Custom enum types are accepted in `{name:Type}` segments so user
// code can route on a closed sum type — same restriction as
// Entity.enum, only zero-arg ctors are eligible.
func TestCheckPath_acceptsCustomEnumType(t *testing.T) {
	src := `module M exposing (..)

type Role = Member | Admin

userByRole : Path { role : Role }
userByRole = "/users/{role:Role}"
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	got := res.ValueTypes["userByRole"].String()
	if !strings.Contains(got, "Path") || !strings.Contains(got, "Role") {
		t.Fatalf("expected Path with Role, got: %s", got)
	}
}

func TestCheckPath_rejectsCustomTypeWithPayload(t *testing.T) {
	// Maybe's `Just a` ctor takes a payload — not eligible for
	// path use because the URL ↔ ctor mapping doesn't cover args.
	src := `module M exposing (..)

bad : Path { x : Maybe }
bad = "/x/{x:Maybe}"
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected error for type with payload ctor")
	}
}

func TestCheckPath_rejectsUnknownType(t *testing.T) {
	src := `module M exposing (..)

bad : Path { x : Bogus }
bad = "/x/{x:Bogus}"
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
	if !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("error should say 'unknown type', got: %v", err)
	}
}

// linkTo with an enum-typed param: passing the wrong ctor type
// should be a compile error, just like with Int / String.
func TestCheckLinkTo_enumTypeMismatch(t *testing.T) {
	src := `module M exposing (..)

type Role = Member | Admin
type Color = Red | Blue

userByRole : Path { role : Role }
userByRole = "/users/{role:Role}"

href = linkTo userByRole { role = Red }
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected error: Color ctor passed where Role required")
	}
}

func TestCheckNavPushTo_typed(t *testing.T) {
	src := `module M exposing (..)

home : Path {}
home = "/"

go = Nav.pushTo home {}
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	got := res.ValueTypes["go"].String()
	if !strings.Contains(got, "Cmd") {
		t.Fatalf("go should be Cmd, got %s", got)
	}
}
