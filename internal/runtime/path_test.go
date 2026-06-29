package runtime

import (
	"strings"
	"testing"
)

func TestParsePathPattern_typedSegments(t *testing.T) {
	p, err := ParsePathPattern("/teams/{teamId:Int}/users/{slug:String}")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got := len(p.Segments); got != 4 {
		t.Fatalf("expected 4 segments, got %d", got)
	}
	if p.Segments[0].IsParam || p.Segments[0].Lit != "teams" {
		t.Errorf("seg 0: expected literal 'teams', got %+v", p.Segments[0])
	}
	if !p.Segments[1].IsParam || p.Segments[1].Name != "teamId" || p.Segments[1].Type != "Int" {
		t.Errorf("seg 1: expected param teamId:Int, got %+v", p.Segments[1])
	}
	if !p.Segments[3].IsParam || p.Segments[3].Name != "slug" || p.Segments[3].Type != "String" {
		t.Errorf("seg 3: expected param slug:String, got %+v", p.Segments[3])
	}
}

func TestParsePathPattern_rejectsBareColon(t *testing.T) {
	_, err := ParsePathPattern("/notes/:id")
	if err == nil {
		t.Fatal("expected error for bare :id, got nil")
	}
	if !strings.Contains(err.Error(), "{id:Type}") {
		t.Errorf("error should suggest {id:Type}, got: %v", err)
	}
}

func TestParsePathPattern_rejectsMissingType(t *testing.T) {
	_, err := ParsePathPattern("/notes/{id}")
	if err == nil {
		t.Fatal("expected error for {id} without type")
	}
	if !strings.Contains(err.Error(), "requires a type") {
		t.Errorf("error should mention missing type, got: %v", err)
	}
}

func TestParsePathPattern_rejectsUnknownType(t *testing.T) {
	_, err := ParsePathPattern("/notes/{id:UUID}")
	if err == nil {
		t.Fatal("expected error for unknown type UUID")
	}
}

func TestParsePathPattern_rejectsDuplicateParam(t *testing.T) {
	_, err := ParsePathPattern("/{a:Int}/{a:String}")
	if err == nil {
		t.Fatal("expected error for duplicate param 'a'")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error should mention duplicate, got: %v", err)
	}
}

func TestPathMatchURL_typedDecoders(t *testing.T) {
	p, err := ParsePathPattern("/notes/{id:Int}")
	if err != nil {
		t.Fatal(err)
	}
	// Successful match — Int gets decoded.
	v := p.MatchURL("/notes/42")
	rec, ok := v.(VRecord)
	if !ok {
		t.Fatalf("expected VRecord, got %T", v)
	}
	if got := rec.Fields["id"]; got != (VInt{V: 42}) {
		t.Errorf("expected id=VInt{42}, got %v", got)
	}
	// Type mismatch — non-numeric string against {id:Int} → miss.
	if v := p.MatchURL("/notes/abc"); v != nil {
		t.Errorf("expected nil for /notes/abc, got %v", v)
	}
	// Length mismatch — extra segment.
	if v := p.MatchURL("/notes/42/edit"); v != nil {
		t.Errorf("expected nil for length mismatch, got %v", v)
	}
}

func TestPathBuildURL_roundTrip(t *testing.T) {
	p, _ := ParsePathPattern("/teams/{teamId:Int}/users/{slug:String}")
	params := VRecord{
		Fields: map[string]Value{
			"teamId": VInt{V: 7},
			"slug":   VString{V: "marcio"},
		},
		Order: []string{"teamId", "slug"},
	}
	url, err := p.BuildURL(params)
	if err != nil {
		t.Fatal(err)
	}
	want := "/teams/7/users/marcio"
	if url != want {
		t.Errorf("expected %q, got %q", want, url)
	}
}

func TestPathBuildURL_missingFieldErrors(t *testing.T) {
	p, _ := ParsePathPattern("/notes/{id:Int}")
	_, err := p.BuildURL(VRecord{Fields: map[string]Value{}, Order: []string{}})
	if err == nil {
		t.Fatal("expected error for missing param")
	}
}

func TestPathBuildURL_wrongTypeErrors(t *testing.T) {
	p, _ := ParsePathPattern("/notes/{id:Int}")
	// Pass a string where Int is expected.
	_, err := p.BuildURL(VRecord{
		Fields: map[string]Value{"id": VString{V: "abc"}},
		Order:  []string{"id"},
	})
	if err == nil {
		t.Fatal("expected error for wrong-type param")
	}
}

// Custom enum types in path patterns: register a type with all
// zero-arg ctors, then verify URL → ctor (decode) and ctor → URL
// (encode) round-trip cleanly. URL canonical form is lowercase;
// ctor canonical form is PascalCase.
func TestPathEnumType_roundTrip(t *testing.T) {
	// Setup: register Role with two ctors.
	prev := EnumTypes["Role"]
	defer func() {
		if prev == nil {
			delete(EnumTypes, "Role")
		} else {
			EnumTypes["Role"] = prev
		}
	}()
	RegisterEnumType("Role", []string{"Member", "Admin"}, map[string]int{"Member": 0, "Admin": 0})

	p, err := ParsePathPattern("/users/{role:Role}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Decode: lowercased URL → PascalCase ctor.
	got := p.MatchURL("/users/admin")
	rec, ok := got.(VRecord)
	if !ok {
		t.Fatalf("expected VRecord, got %T", got)
	}
	c, ok := rec.Fields["role"].(VCtor)
	if !ok || c.Tag != "Admin" {
		t.Errorf("expected Admin, got %v", rec.Fields["role"])
	}

	// Decode: case-insensitive — `/users/MEMBER` should also match.
	if got := p.MatchURL("/users/MEMBER"); got == nil {
		t.Error("expected case-insensitive match")
	}

	// Decode failure: unknown ctor → matcher returns nil.
	if got := p.MatchURL("/users/superuser"); got != nil {
		t.Errorf("expected nil for unknown ctor, got %v", got)
	}

	// Encode: ctor → lowercase URL.
	url, err := p.BuildURL(VRecord{
		Fields: map[string]Value{"role": VCtor{Tag: "Admin"}},
		Order:  []string{"role"},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if want := "/users/admin"; url != want {
		t.Errorf("expected %q, got %q", want, url)
	}
}

func TestPathEnumType_rejectsPayloadCtor(t *testing.T) {
	// RegisterEnumType filters out non-zero-arg ctors silently —
	// nothing gets registered.
	delete(EnumTypes, "Maybe")
	RegisterEnumType("Maybe", []string{"Nothing", "Just"}, map[string]int{"Nothing": 0, "Just": 1})
	if _, registered := EnumTypes["Maybe"]; registered {
		t.Error("Maybe should not be registered (Just takes 1 arg)")
	}
}
