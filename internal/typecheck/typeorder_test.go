package typecheck

import (
	"strings"
	"testing"

	"mar/internal/parser"
)

// The checker resolves an alias by substituting its body at each use, filling
// the table as it walks the declarations. Walking in source order made the
// type namespace order-dependent the same way the value namespace used to be
// (ADR 0015): a name declared below became an opaque TCon that never unified
// with what the alias actually is, and the error pointed at the innocent value
// rather than at the ordering. These tests are the ordering, from the outside.

func checks(t *testing.T, src string) error {
	t.Helper()
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = CheckModule(mod)
	return err
}

func mustCheck(t *testing.T, name, src string) {
	t.Helper()
	if err := checks(t, src); err != nil {
		t.Errorf("%s: should typecheck, got: %v", name, err)
	}
}

func TestAliasCanReferenceAliasDeclaredBelow(t *testing.T) {
	mustCheck(t, "alias -> alias", `module M exposing (..)

type alias Outer =
    { inner : Inner }


type alias Inner =
    { n : Int }


thing : Outer
thing =
    { inner = { n = 1 } }
`)
}

func TestConstructorCanReferenceAliasDeclaredBelow(t *testing.T) {
	mustCheck(t, "custom -> alias", `module M exposing (..)

type Wrapped
    = Wrap Point


type alias Point =
    { x : Int, y : Int }


thing : Wrapped
thing =
    Wrap { x = 1, y = 2 }
`)
}

func TestAliasCanReferenceCustomTypeDeclaredBelow(t *testing.T) {
	mustCheck(t, "alias -> custom", `module M exposing (..)

type alias Job =
    { state : State }


type State
    = Queued
    | Done


thing : Job
thing =
    { state = Queued }
`)
}

// The ordering must not break the shapes that were already fine. A custom
// type naming itself is the ordinary way to write a tree, and two of them
// naming each other is legal for the same reason: a custom type resolves to a
// TCon carrying only its name, so the definition need not exist yet.
func TestRecursiveCustomTypesStillCheck(t *testing.T) {
	mustCheck(t, "self-recursive", `module M exposing (..)

type Tree
    = Leaf
    | Node Tree Tree


thing : Tree
thing =
    Node Leaf Leaf
`)
	mustCheck(t, "mutually recursive", `module M exposing (..)

type Expr
    = Lit Int
    | Block Stmt


type Stmt
    = Return Expr


thing : Expr
thing =
    Block (Return (Lit 1))
`)
}

// A chain longer than one hop, declared backwards, to prove this is a real
// topological order and not a single look-ahead.
func TestAliasChainDeclaredBackwards(t *testing.T) {
	mustCheck(t, "three deep", `module M exposing (..)

type alias A =
    { b : B }


type alias B =
    { c : C }


type alias C =
    { n : Int }


thing : A
thing =
    { b = { c = { n = 1 } } }
`)
}

// Ordering is a fix, not a licence: a name that is not declared anywhere still
// has to be reported rather than quietly becoming an opaque type.
func TestUnknownTypeNameStillFails(t *testing.T) {
	err := checks(t, `module M exposing (..)

type alias Outer =
    { inner : Missing }


thing : Outer
thing =
    { inner = { n = 1 } }
`)
	if err == nil {
		t.Fatal("a record literal against an undeclared type name should not check")
	}
	if !strings.Contains(err.Error(), "Missing") {
		t.Errorf("the error should name the undeclared type, got: %v", err)
	}
}
