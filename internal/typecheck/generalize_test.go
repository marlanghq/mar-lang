package typecheck

import (
	"strings"
	"testing"

	"mar/internal/parser"
)

func TestGeneralizeIdentity(t *testing.T) {
	src := `module M exposing (..)
identity x = x
`
	resetVarIDsForTesting()
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	res, err := CheckModule(mod)
	if err != nil {
		t.Fatal(err)
	}
	got := res.ValueTypes["identity"].String()
	t.Logf("identity : %s", got)
	if !strings.Contains(got, "forall") {
		t.Fatalf("expected forall in type, got %s", got)
	}
}

func TestGeneralizeUnbox(t *testing.T) {
	src := `module M exposing (..)
type Box a = Box a
unbox b = case b of Box x -> x
`
	resetVarIDsForTesting()
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	res, err := CheckModule(mod)
	if err != nil {
		t.Fatal(err)
	}
	got := res.ValueTypes["unbox"].String()
	t.Logf("unbox : %s", got)
	if !strings.Contains(got, "forall") {
		t.Fatalf("expected forall in type, got %s", got)
	}
}
