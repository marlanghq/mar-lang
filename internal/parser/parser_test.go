package parser

import (
	"strings"
	"testing"

	"mar/internal/ast"
)

func mustParse(t *testing.T, src string) *ast.Module {
	t.Helper()
	mod, err := Parse(src)
	if err != nil {
		t.Fatalf("parse error: %v\nsource:\n%s", err, src)
	}
	return mod
}

func mustParseErr(t *testing.T, src string) error {
	t.Helper()
	_, err := Parse(src)
	if err == nil {
		t.Fatalf("expected parse error, got success\nsource:\n%s", src)
	}
	return err
}

// --- Module header ---

func TestModuleHeaderExposingAll(t *testing.T) {
	mod := mustParse(t, "module Foo exposing (..)\n")
	if len(mod.Name) != 1 || mod.Name[0] != "Foo" {
		t.Fatalf("module name = %v", mod.Name)
	}
	if !mod.Exposing.All {
		t.Fatalf("expected exposing (..)")
	}
}

func TestModuleHeaderDottedName(t *testing.T) {
	mod := mustParse(t, "module Foo.Bar.Baz exposing (..)\n")
	if strings.Join(mod.Name, ".") != "Foo.Bar.Baz" {
		t.Fatalf("module name = %v", mod.Name)
	}
}

func TestModuleHeaderExposingList(t *testing.T) {
	mod := mustParse(t, "module Foo exposing (a, B, C(..))\n")
	if mod.Exposing.All {
		t.Fatal("should not be exposing all")
	}
	if len(mod.Exposing.Items) != 3 {
		t.Fatalf("want 3 items, got %d", len(mod.Exposing.Items))
	}
	items := mod.Exposing.Items
	if items[0].Name != "a" || items[0].Open {
		t.Fatalf("item[0] = %+v", items[0])
	}
	if items[1].Name != "B" || items[1].Open {
		t.Fatalf("item[1] = %+v", items[1])
	}
	if items[2].Name != "C" || !items[2].Open {
		t.Fatalf("item[2] = %+v", items[2])
	}
}

// --- Imports ---

func TestImportSimple(t *testing.T) {
	mod := mustParse(t, "module Foo exposing (..)\nimport Bar\n")
	if len(mod.Imports) != 1 {
		t.Fatalf("want 1 import, got %d", len(mod.Imports))
	}
	if mod.Imports[0].Module[0] != "Bar" {
		t.Fatalf("import = %+v", mod.Imports[0])
	}
}

func TestImportWithAlias(t *testing.T) {
	mod := mustParse(t, "module Foo exposing (..)\nimport Bar.Baz as B\n")
	if mod.Imports[0].Alias != "B" {
		t.Fatalf("alias = %q", mod.Imports[0].Alias)
	}
}

func TestImportExposing(t *testing.T) {
	mod := mustParse(t, "module Foo exposing (..)\nimport Bar exposing (x, Y(..))\n")
	exp := mod.Imports[0].Exposing
	if len(exp.Items) != 2 {
		t.Fatalf("want 2 items, got %d", len(exp.Items))
	}
}

// --- Type alias ---

func TestTypeAliasSimple(t *testing.T) {
	mod := mustParse(t, "module Foo exposing (..)\ntype alias Name = String\n")
	d, ok := mod.Decls[0].(*ast.TypeAliasDecl)
	if !ok {
		t.Fatalf("want TypeAliasDecl, got %T", mod.Decls[0])
	}
	if d.Name != "Name" {
		t.Fatalf("name = %q", d.Name)
	}
}

func TestTypeAliasRecord(t *testing.T) {
	src := `module Foo exposing (..)
type alias Person = { name : String, age : Int }
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.TypeAliasDecl)
	rec, ok := d.Body.(*ast.TypeRecord)
	if !ok {
		t.Fatalf("body should be record, got %T", d.Body)
	}
	if len(rec.Fields) != 2 {
		t.Fatalf("want 2 fields, got %d", len(rec.Fields))
	}
}

func TestTypeAliasRecordRowPoly(t *testing.T) {
	src := `module Foo exposing (..)
type alias Named a = { a | name : String }
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.TypeAliasDecl)
	rec := d.Body.(*ast.TypeRecord)
	if rec.Extends != "a" {
		t.Fatalf("extends = %q", rec.Extends)
	}
}

func TestTypeAliasArrow(t *testing.T) {
	src := `module Foo exposing (..)
type alias Handler = Int -> String
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.TypeAliasDecl)
	if _, ok := d.Body.(*ast.TypeArrow); !ok {
		t.Fatalf("body should be arrow, got %T", d.Body)
	}
}

func TestTypeAliasParameterized(t *testing.T) {
	src := `module Foo exposing (..)
type alias Box a = { value : a }
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.TypeAliasDecl)
	if len(d.Params) != 1 || d.Params[0] != "a" {
		t.Fatalf("params = %v", d.Params)
	}
}

// --- Custom type ---

func TestCustomTypeSimple(t *testing.T) {
	src := `module Foo exposing (..)
type Status = Active | Inactive | Archived
`
	mod := mustParse(t, src)
	d, ok := mod.Decls[0].(*ast.CustomTypeDecl)
	if !ok {
		t.Fatalf("want CustomTypeDecl, got %T", mod.Decls[0])
	}
	if len(d.Constructors) != 3 {
		t.Fatalf("want 3 ctors, got %d", len(d.Constructors))
	}
}

func TestCustomTypeWithPayload(t *testing.T) {
	src := `module Foo exposing (..)
type Maybe a = Nothing | Just a
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.CustomTypeDecl)
	if d.Constructors[1].Name != "Just" || len(d.Constructors[1].Args) != 1 {
		t.Fatalf("Just constructor = %+v", d.Constructors[1])
	}
}

func TestCustomTypeNominalWrap(t *testing.T) {
	src := `module Foo exposing (..)
type UserId = UserId Int
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.CustomTypeDecl)
	if len(d.Constructors) != 1 {
		t.Fatalf("want 1 ctor, got %d", len(d.Constructors))
	}
	c := d.Constructors[0]
	if c.Name != "UserId" || len(c.Args) != 1 {
		t.Fatalf("ctor = %+v", c)
	}
}

// --- Annotations and value decls ---

func TestAnnotationDecl(t *testing.T) {
	src := `module Foo exposing (..)
greet : String -> String
`
	mod := mustParse(t, src)
	d, ok := mod.Decls[0].(*ast.AnnotationDecl)
	if !ok {
		t.Fatalf("want AnnotationDecl, got %T", mod.Decls[0])
	}
	if d.Name != "greet" {
		t.Fatalf("name = %q", d.Name)
	}
}

func TestValueDeclSimple(t *testing.T) {
	src := `module Foo exposing (..)
greeting = "Hello"
`
	mod := mustParse(t, src)
	d, ok := mod.Decls[0].(*ast.ValueDecl)
	if !ok {
		t.Fatalf("want ValueDecl, got %T", mod.Decls[0])
	}
	if d.Name != "greeting" {
		t.Fatalf("name = %q", d.Name)
	}
	if _, ok := d.Body.(*ast.EString); !ok {
		t.Fatalf("body should be EString, got %T", d.Body)
	}
}

func TestValueDeclWithParams(t *testing.T) {
	src := `module Foo exposing (..)
add x y = x
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.ValueDecl)
	if len(d.Params) != 2 {
		t.Fatalf("want 2 params, got %d", len(d.Params))
	}
}

// --- Expression atoms ---

func TestExprArith(t *testing.T) {
	src := `module Foo exposing (..)
foo = 1 + 2 * 3
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.ValueDecl)
	binop, ok := d.Body.(*ast.EBinop)
	if !ok {
		t.Fatalf("want EBinop, got %T", d.Body)
	}
	if binop.Op != "+" {
		t.Fatalf("top op = %q, expected + (lower precedence first)", binop.Op)
	}
	right := binop.Right.(*ast.EBinop)
	if right.Op != "*" {
		t.Fatalf("right op = %q", right.Op)
	}
}

func TestExprPipeline(t *testing.T) {
	src := `module Foo exposing (..)
foo = x |> f |> g
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.ValueDecl)
	// |> is left-assoc, so should be ((x |> f) |> g)
	top := d.Body.(*ast.EBinop)
	if top.Op != "|>" {
		t.Fatalf("top op = %q", top.Op)
	}
	left := top.Left.(*ast.EBinop)
	if left.Op != "|>" {
		t.Fatalf("inner left op = %q", left.Op)
	}
}

func TestExprApplication(t *testing.T) {
	src := `module Foo exposing (..)
foo = f x y
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.ValueDecl)
	// f x y => (f x) y
	outer := d.Body.(*ast.EApp)
	if _, ok := outer.Arg.(*ast.EVar); !ok {
		t.Fatalf("outer arg should be y, got %T", outer.Arg)
	}
	inner := outer.Fn.(*ast.EApp)
	if v, ok := inner.Fn.(*ast.EVar); !ok || v.Name != "f" {
		t.Fatalf("inner fn = %+v", inner.Fn)
	}
}

func TestExprLambda(t *testing.T) {
	src := `module Foo exposing (..)
foo = \x -> x + 1
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.ValueDecl)
	if _, ok := d.Body.(*ast.ELambda); !ok {
		t.Fatalf("want ELambda, got %T", d.Body)
	}
}

func TestExprIf(t *testing.T) {
	src := `module Foo exposing (..)
foo = if True then 1 else 2
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.ValueDecl)
	if _, ok := d.Body.(*ast.EIf); !ok {
		t.Fatalf("want EIf, got %T", d.Body)
	}
}

func TestExprCase(t *testing.T) {
	src := `module Foo exposing (..)
foo x =
    case x of
        Just y -> y
        Nothing -> 0
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.ValueDecl)
	c, ok := d.Body.(*ast.ECase)
	if !ok {
		t.Fatalf("want ECase, got %T", d.Body)
	}
	if len(c.Branches) != 2 {
		t.Fatalf("want 2 branches, got %d", len(c.Branches))
	}
}

func TestExprLet(t *testing.T) {
	src := `module Foo exposing (..)
foo =
    let
        x = 1
        y = 2
    in
    x + y
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.ValueDecl)
	let, ok := d.Body.(*ast.ELet)
	if !ok {
		t.Fatalf("want ELet, got %T", d.Body)
	}
	if len(let.Bindings) != 2 {
		t.Fatalf("want 2 bindings, got %d", len(let.Bindings))
	}
}

func TestExprLetBindArrow(t *testing.T) {
	src := `module Foo exposing (..)
deletePost id =
    let
        post <- findOne id
    in
    delete post.id
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.ValueDecl)
	let := d.Body.(*ast.ELet)
	if !let.Bindings[0].IsBind {
		t.Fatalf("expected IsBind=true for <-")
	}
}

func TestExprList(t *testing.T) {
	src := `module Foo exposing (..)
foo = [1, 2, 3]
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.ValueDecl)
	lst, ok := d.Body.(*ast.EList)
	if !ok {
		t.Fatalf("want EList, got %T", d.Body)
	}
	if len(lst.Elements) != 3 {
		t.Fatalf("want 3 elements, got %d", len(lst.Elements))
	}
}

func TestExprRecord(t *testing.T) {
	src := `module Foo exposing (..)
foo = { name = "Alice", age = 30 }
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.ValueDecl)
	rec, ok := d.Body.(*ast.ERecord)
	if !ok {
		t.Fatalf("want ERecord, got %T", d.Body)
	}
	if len(rec.Fields) != 2 {
		t.Fatalf("want 2 fields, got %d", len(rec.Fields))
	}
}

func TestExprRecordUpdate(t *testing.T) {
	src := `module Foo exposing (..)
foo = { model | count = 1 }
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.ValueDecl)
	upd, ok := d.Body.(*ast.ERecordUpdate)
	if !ok {
		t.Fatalf("want ERecordUpdate, got %T", d.Body)
	}
	if len(upd.Fields) != 1 {
		t.Fatalf("want 1 field, got %d", len(upd.Fields))
	}
}

func TestExprFieldAccess(t *testing.T) {
	src := `module Foo exposing (..)
foo = user.name.first
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.ValueDecl)
	outer, ok := d.Body.(*ast.EFieldAccess)
	if !ok {
		t.Fatalf("want EFieldAccess, got %T", d.Body)
	}
	if outer.Field != "first" {
		t.Fatalf("outer field = %q", outer.Field)
	}
	inner := outer.Record.(*ast.EFieldAccess)
	if inner.Field != "name" {
		t.Fatalf("inner field = %q", inner.Field)
	}
}

func TestExprFieldAccessor(t *testing.T) {
	src := `module Foo exposing (..)
foo = .name
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.ValueDecl)
	if _, ok := d.Body.(*ast.EFieldAccessor); !ok {
		t.Fatalf("want EFieldAccessor, got %T", d.Body)
	}
}

func TestExprUnit(t *testing.T) {
	src := `module Foo exposing (..)
foo = ()
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.ValueDecl)
	if _, ok := d.Body.(*ast.EUnit); !ok {
		t.Fatalf("want EUnit, got %T", d.Body)
	}
}

func TestExprTuple(t *testing.T) {
	src := `module Foo exposing (..)
foo = (1, 2)
`
	mod := mustParse(t, src)
	d := mod.Decls[0].(*ast.ValueDecl)
	tup, ok := d.Body.(*ast.ETuple)
	if !ok {
		t.Fatalf("want ETuple, got %T", d.Body)
	}
	if len(tup.Members) != 2 {
		t.Fatalf("want 2 members, got %d", len(tup.Members))
	}
}

// --- Errors ---

func TestErrorNoModuleHeader(t *testing.T) {
	err := mustParseErr(t, "x = 1\n")
	if !strings.Contains(err.Error(), "module") {
		t.Fatalf("expected module error, got: %v", err)
	}
}

func TestErrorMissingExposing(t *testing.T) {
	err := mustParseErr(t, "module Foo\n")
	if !strings.Contains(err.Error(), "exposing") {
		t.Fatalf("expected exposing error, got: %v", err)
	}
}

// --- Larger smoke test ---

func TestSmokeRealisticModule(t *testing.T) {
	src := `module Posts exposing (..)

import Db
import Endpoint exposing (Endpoint)

type PostId = PostId Int

type alias Post =
    { id : PostId
    , author : UserId
    , body : String
    }

type alias PostInput = { body : String }

type PostField = Body

validatePost : PostInput -> Result (List String) PostInput
validatePost input =
    if String.length input.body == 0 then
        Err ["body cannot be empty"]
    else
        Ok input

create : Endpoint PostInput Post PostField
create =
    Endpoint.post "/posts"
`
	mod := mustParse(t, src)
	if len(mod.Decls) < 5 {
		t.Fatalf("expected several decls, got %d", len(mod.Decls))
	}
}
