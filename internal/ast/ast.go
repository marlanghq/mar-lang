// Package ast defines the Mar (Elm-style) abstract syntax tree.
//
// Every node carries position info (line/column) from its first significant
// token. Nodes are immutable values produced by the parser.
package ast

// Pos identifies a source position. Line and Column are 1-indexed.
type Pos struct {
	Line   int
	Column int
}

// --- Module ---

// Module is the top-level unit. One module per source file.
type Module struct {
	Pos      Pos
	Name     ModuleName // e.g. ["Posts", "Backend"]
	Exposing Exposing
	Imports  []Import
	Decls    []Decl
}

// ModuleName is a dotted identifier path: ["Foo", "Bar"] for "Foo.Bar".
type ModuleName []string

// Exposing controls what a module exposes from itself or imports from another.
type Exposing struct {
	Pos   Pos
	All   bool          // exposing (..)
	Items []ExposedItem // exposing (a, B, C(..))
}

// ExposedItem is a single name in an exposing list.
type ExposedItem struct {
	Pos  Pos
	Name string // "foo" (value) or "Foo" (type)
	Open bool   // true if "Foo(..)" — exposes type and all constructors
}

// --- Imports ---

type Import struct {
	Pos      Pos
	Module   ModuleName
	Alias    string   // "" if no alias (import Foo as F)
	Exposing Exposing // empty if no exposing clause
}

// --- Top-level declarations ---

// Decl is one of TypeAliasDecl, CustomTypeDecl, AnnotationDecl, ValueDecl, PortDecl.
type Decl interface {
	declNode()
	Position() Pos
}

// TypeAliasDecl — type alias Foo a = ...
type TypeAliasDecl struct {
	Pos    Pos
	Name   string
	Params []string // type parameters: ["a", "b"] for `type alias Foo a b = ...`
	Body   TypeExpr
}

func (d *TypeAliasDecl) declNode()     {}
func (d *TypeAliasDecl) Position() Pos { return d.Pos }

// CustomTypeDecl — type Foo a = A | B Int | C String String
type CustomTypeDecl struct {
	Pos          Pos
	Name         string
	Params       []string
	Constructors []Constructor
}

func (d *CustomTypeDecl) declNode()     {}
func (d *CustomTypeDecl) Position() Pos { return d.Pos }

type Constructor struct {
	Pos  Pos
	Name string
	Args []TypeExpr // payload types; empty for tag-only
}

// AnnotationDecl — foo : Int -> String
// Stored separately from ValueDecl so the parser can pair them up.
type AnnotationDecl struct {
	Pos  Pos
	Name string
	Type TypeExpr
}

func (d *AnnotationDecl) declNode()     {}
func (d *AnnotationDecl) Position() Pos { return d.Pos }

// ValueDecl — foo x y = expr
// Function params are desugared into nested lambdas at parse time? Or kept here?
// We keep them explicit for better error messages.
type ValueDecl struct {
	Pos    Pos
	Name   string
	Params []Pattern
	Body   Expr
}

func (d *ValueDecl) declNode()     {}
func (d *ValueDecl) Position() Pos { return d.Pos }

// PortDecl — port foo : SomeType (rare; placeholder)
type PortDecl struct {
	Pos  Pos
	Name string
	Type TypeExpr
}

func (d *PortDecl) declNode()     {}
func (d *PortDecl) Position() Pos { return d.Pos }

// --- Type expressions ---

// TypeExpr is a type appearing in annotations, aliases, constructors.
type TypeExpr interface {
	typeNode()
	Position() Pos
}

// TypeVar — a, b, msg, etc. (lowercase)
type TypeVar struct {
	Pos  Pos
	Name string
}

func (t *TypeVar) typeNode()     {}
func (t *TypeVar) Position() Pos { return t.Pos }

// TypeCon — Int, String, MyType (uppercase, possibly qualified).
// May be applied to args: List a, Result e a.
type TypeCon struct {
	Pos    Pos
	Module ModuleName // empty if unqualified
	Name   string
	Args   []TypeExpr
}

func (t *TypeCon) typeNode()     {}
func (t *TypeCon) Position() Pos { return t.Pos }

// TypeArrow — a -> b
type TypeArrow struct {
	Pos  Pos
	From TypeExpr
	To   TypeExpr
}

func (t *TypeArrow) typeNode()     {}
func (t *TypeArrow) Position() Pos { return t.Pos }

// TypeRecord — { a : Int, b : String } or { r | a : Int } (row poly)
type TypeRecord struct {
	Pos     Pos
	Extends string // "" if not extending; "r" for { r | ... }
	Fields  []TypeRecField
}

type TypeRecField struct {
	Pos  Pos
	Name string
	Type TypeExpr
}

func (t *TypeRecord) typeNode()     {}
func (t *TypeRecord) Position() Pos { return t.Pos }

// TypeTuple — (Int, String) — n >= 2
type TypeTuple struct {
	Pos     Pos
	Members []TypeExpr
}

func (t *TypeTuple) typeNode()     {}
func (t *TypeTuple) Position() Pos { return t.Pos }

// TypeUnit — () as a type
type TypeUnit struct {
	Pos Pos
}

func (t *TypeUnit) typeNode()     {}
func (t *TypeUnit) Position() Pos { return t.Pos }

// --- Patterns (for function params, case branches, let bindings) ---

type Pattern interface {
	patNode()
	Position() Pos
}

// PVar — x (binds variable)
type PVar struct {
	Pos  Pos
	Name string
}

func (p *PVar) patNode()      {}
func (p *PVar) Position() Pos { return p.Pos }

// PWildcard — _
type PWildcard struct {
	Pos Pos
}

func (p *PWildcard) patNode()      {}
func (p *PWildcard) Position() Pos { return p.Pos }

// PInt is a literal pattern (also covers PString / PFloat literals).
type PInt struct {
	Pos   Pos
	Value int64
}

func (p *PInt) patNode()      {}
func (p *PInt) Position() Pos { return p.Pos }

type PString struct {
	Pos   Pos
	Value string
}

func (p *PString) patNode()      {}
func (p *PString) Position() Pos { return p.Pos }

// PChar — char literal pattern: `case c of 'a' -> ...`. Compared by
// rune equality at match time.
type PChar struct {
	Pos   Pos
	Value rune
}

func (p *PChar) patNode()      {}
func (p *PChar) Position() Pos { return p.Pos }

// PCtor — Just x, Ok value, MyTag a b
type PCtor struct {
	Pos    Pos
	Module ModuleName
	Name   string
	Args   []Pattern
}

func (p *PCtor) patNode()      {}
func (p *PCtor) Position() Pos { return p.Pos }

// PTuple — (a, b)
type PTuple struct {
	Pos     Pos
	Members []Pattern
}

func (p *PTuple) patNode()      {}
func (p *PTuple) Position() Pos { return p.Pos }

// PRecord — { a, b, c } — destructures record fields
type PRecord struct {
	Pos    Pos
	Fields []string
}

func (p *PRecord) patNode()      {}
func (p *PRecord) Position() Pos { return p.Pos }

// PUnit — ()
type PUnit struct {
	Pos Pos
}

func (p *PUnit) patNode()      {}
func (p *PUnit) Position() Pos { return p.Pos }

// PList — explicit list pattern, e.g. [], [a], [a, b]
type PList struct {
	Pos      Pos
	Elements []Pattern
}

func (p *PList) patNode()      {}
func (p *PList) Position() Pos { return p.Pos }

// PCons — head :: tail
type PCons struct {
	Pos  Pos
	Head Pattern
	Tail Pattern
}

func (p *PCons) patNode()      {}
func (p *PCons) Position() Pos { return p.Pos }

// --- Expressions ---

type Expr interface {
	exprNode()
	Position() Pos
}

// EInt is a literal expression (also covers EFloat / EString literals).
type EInt struct {
	Pos   Pos
	Value int64
}

func (e *EInt) exprNode()     {}
func (e *EInt) Position() Pos { return e.Pos }

type EFloat struct {
	Pos   Pos
	Value float64
}

func (e *EFloat) exprNode()     {}
func (e *EFloat) Position() Pos { return e.Pos }

type EString struct {
	Pos   Pos
	Value string
}

func (e *EString) exprNode()     {}
func (e *EString) Position() Pos { return e.Pos }

// EChar — single Unicode code point literal: 'a', '\n', '\u{1F600}'.
// Value is the rune (int32). The lexer carries the decoded character
// as a UTF-8 string in Token.Value; the parser decodes the single
// rune. Used together with TChar in the typechecker and VChar (Go) /
// VChar (JS) / .char (Swift) at runtime.
type EChar struct {
	Pos   Pos
	Value rune
}

func (e *EChar) exprNode()     {}
func (e *EChar) Position() Pos { return e.Pos }

// EVar — foo, foo' (unqualified value)
type EVar struct {
	Pos  Pos
	Name string
}

func (e *EVar) exprNode()     {}
func (e *EVar) Position() Pos { return e.Pos }

// EQualified — Module.foo
type EQualified struct {
	Pos    Pos
	Module ModuleName
	Name   string
}

func (e *EQualified) exprNode()     {}
func (e *EQualified) Position() Pos { return e.Pos }

// ECtor — Just, Ok, MyTag (uppercase, may be qualified)
type ECtor struct {
	Pos    Pos
	Module ModuleName
	Name   string
}

func (e *ECtor) exprNode()     {}
func (e *ECtor) Position() Pos { return e.Pos }

// EFieldAccessor — .foo (a function: { r | foo : a } -> a)
type EFieldAccessor struct {
	Pos   Pos
	Field string
}

func (e *EFieldAccessor) exprNode()     {}
func (e *EFieldAccessor) Position() Pos { return e.Pos }

// EFieldAccess — expr.foo
type EFieldAccess struct {
	Pos    Pos
	Record Expr
	Field  string
}

func (e *EFieldAccess) exprNode()     {}
func (e *EFieldAccess) Position() Pos { return e.Pos }

// EApp — f x  (single application; chained calls become nested EApp)
type EApp struct {
	Pos Pos
	Fn  Expr
	Arg Expr
}

func (e *EApp) exprNode()     {}
func (e *EApp) Position() Pos { return e.Pos }

// EBinop — e1 OP e2 (kept as a binary node before precedence resolution)
type EBinop struct {
	Pos   Pos
	Op    string
	Left  Expr
	Right Expr
}

func (e *EBinop) exprNode()     {}
func (e *EBinop) Position() Pos { return e.Pos }

// ELambda — \x y -> body
type ELambda struct {
	Pos    Pos
	Params []Pattern
	Body   Expr
}

func (e *ELambda) exprNode()     {}
func (e *ELambda) Position() Pos { return e.Pos }

// EIf — if c then t else e
type EIf struct {
	Pos  Pos
	Cond Expr
	Then Expr
	Else Expr
}

func (e *EIf) exprNode()     {}
func (e *EIf) Position() Pos { return e.Pos }

// ECase — case e of branches
type ECase struct {
	Pos      Pos
	Subject  Expr
	Branches []CaseBranch
}

type CaseBranch struct {
	Pos     Pos
	Pattern Pattern
	Body    Expr
}

func (e *ECase) exprNode()     {}
func (e *ECase) Position() Pos { return e.Pos }

// ELet — let bindings in body
type ELet struct {
	Pos      Pos
	Bindings []LetBinding
	Body     Expr
}

// LetBinding is either a value binding (=) or an effect bind (<-).
type LetBinding struct {
	Pos        Pos
	Pattern    Pattern
	Annotation TypeExpr // optional; may be nil
	Body       Expr
	IsBind     bool // true for "x <- effect" (effect bind), false for "x = expr"
}

func (e *ELet) exprNode()     {}
func (e *ELet) Position() Pos { return e.Pos }

// ETuple — (a, b) when used as expression
type ETuple struct {
	Pos     Pos
	Members []Expr
}

func (e *ETuple) exprNode()     {}
func (e *ETuple) Position() Pos { return e.Pos }

// EList — [a, b, c]
type EList struct {
	Pos      Pos
	Elements []Expr
}

func (e *EList) exprNode()     {}
func (e *EList) Position() Pos { return e.Pos }

// ERecord — { a = 1, b = 2 }
type ERecord struct {
	Pos    Pos
	Fields []RecField
}

type RecField struct {
	Pos   Pos
	Name  string
	Value Expr
}

func (e *ERecord) exprNode()     {}
func (e *ERecord) Position() Pos { return e.Pos }

// ERecordUpdate — { record | field = newValue, other = ... }
type ERecordUpdate struct {
	Pos    Pos
	Record Expr // typically EVar; could be field access too
	Fields []RecField
}

func (e *ERecordUpdate) exprNode()     {}
func (e *ERecordUpdate) Position() Pos { return e.Pos }

// EUnit — ()
type EUnit struct {
	Pos Pos
}

func (e *EUnit) exprNode()     {}
func (e *EUnit) Position() Pos { return e.Pos }

// ENegate — -expr (unary minus)
type ENegate struct {
	Pos   Pos
	Inner Expr
}

func (e *ENegate) exprNode()     {}
func (e *ENegate) Position() Pos { return e.Pos }
