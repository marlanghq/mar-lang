// Package parser parses Mar (Elm-style) tokens into an AST.
//
// This is a recursive descent parser with explicit handling of operator
// precedence in expressions. It is layout-aware in a limited way: top-level
// declarations are recognized by being at column 1 (a definition starts a new
// declaration whenever a name token appears at column 1).
package parser

import (
	"fmt"
	"strconv"
	"unicode/utf8"

	"mar/internal/ast"
	"mar/internal/lexer"
)

// Error carries position and message.
type Error struct {
	Line    int
	Column  int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("parse error at %d:%d: %s", e.Line, e.Column, e.Message)
}

// Parse parses a complete module from source.
func Parse(src string) (*ast.Module, error) {
	toks, err := lexer.Lex(src)
	if err != nil {
		return nil, err
	}
	// Default boundary column is 1: top-level declarations start at column 1,
	// so any token at column 1 ends the current expression.
	p := &parser{tokens: toks, boundaryCol: 1}
	return p.parseModule()
}

// --- internals ---

type parser struct {
	tokens []lexer.Token
	pos    int

	// boundaryCol limits how far function application and field-chain parsing
	// extend on subsequent lines. A token at column <= boundaryCol on a new
	// line ends the current expression. Default is 1 (top-level decls).
	// Inside a case branch or let binding body, the boundary is the column
	// of the enclosing pattern/binding so that the next sibling stops the body.
	boundaryCol int
}

func (p *parser) peek() lexer.Token {
	return p.tokens[p.pos]
}

func (p *parser) peekAt(offset int) lexer.Token {
	idx := p.pos + offset
	if idx >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1] // EOF
	}
	return p.tokens[idx]
}

func (p *parser) advance() lexer.Token {
	t := p.tokens[p.pos]
	if t.Kind != lexer.KindEOF {
		p.pos++
	}
	return t
}

func (p *parser) expect(kind lexer.Kind) (lexer.Token, error) {
	t := p.peek()
	if t.Kind != kind {
		return t, p.errorf("expected %s, got %s", kind, t.Kind)
	}
	return p.advance(), nil
}

func (p *parser) accept(kind lexer.Kind) (lexer.Token, bool) {
	t := p.peek()
	if t.Kind == kind {
		p.advance()
		return t, true
	}
	return t, false
}

func (p *parser) errorf(format string, args ...any) *Error {
	t := p.peek()
	return &Error{
		Line:    t.Line,
		Column:  t.Column,
		Message: fmt.Sprintf(format, args...),
	}
}

func posOf(t lexer.Token) ast.Pos {
	return ast.Pos{Line: t.Line, Column: t.Column}
}

// prevEnd is the exclusive end position of the most recently consumed
// token (the cursor just past its last character). Used to give an
// expression its source-span end.
func (p *parser) prevEnd() ast.Pos {
	i := p.pos - 1
	if i < 0 {
		i = 0
	}
	t := p.tokens[i]
	return ast.Pos{Line: t.EndLine, Column: t.EndColumn}
}

// withEnd stamps e's source-span end with the current prevEnd and
// returns e, so expression-building functions can `return p.withEnd(e),
// nil`. Each enclosing combinator re-stamps the node it returns, so the
// end always reaches the last token actually consumed for that node.
func (p *parser) withEnd(e ast.Expr) ast.Expr {
	ast.SetEnd(e, p.prevEnd())
	return e
}

// --- Module ---

func (p *parser) parseModule() (*ast.Module, error) {
	mod := &ast.Module{}

	// module Foo[.Bar] exposing (...)
	header, err := p.expect(lexer.KindModule)
	if err != nil {
		return nil, err
	}
	mod.Pos = posOf(header)
	name, err := p.parseModuleName()
	if err != nil {
		return nil, err
	}
	mod.Name = name

	if _, err := p.expect(lexer.KindExposing); err != nil {
		return nil, err
	}
	exp, err := p.parseExposing()
	if err != nil {
		return nil, err
	}
	mod.Exposing = exp

	// imports
	for p.peek().Kind == lexer.KindImport {
		imp, err := p.parseImport()
		if err != nil {
			return nil, err
		}
		mod.Imports = append(mod.Imports, imp)
	}

	// declarations
	for p.peek().Kind != lexer.KindEOF {
		decl, err := p.parseDecl()
		if err != nil {
			return nil, err
		}
		mod.Decls = append(mod.Decls, decl)
	}

	return mod, nil
}

func (p *parser) parseModuleName() (ast.ModuleName, error) {
	t, err := p.expect(lexer.KindUpperName)
	if err != nil {
		return nil, err
	}
	parts := []string{t.Value}
	for p.peek().Kind == lexer.KindDot && p.peekAt(1).Kind == lexer.KindUpperName {
		p.advance() // .
		t = p.advance()
		parts = append(parts, t.Value)
	}
	return parts, nil
}

// parseExposing parses "( .. )" or "( name1, name2, Foo(..) )".
// Caller should have already consumed the `exposing` keyword.
func (p *parser) parseExposing() (ast.Exposing, error) {
	openTok, err := p.expect(lexer.KindLParen)
	if err != nil {
		return ast.Exposing{}, err
	}
	exp := ast.Exposing{Pos: posOf(openTok)}

	// (..)
	if p.peek().Kind == lexer.KindDot && p.peekAt(1).Kind == lexer.KindDot {
		p.advance() // .
		p.advance() // .
		if _, err := p.expect(lexer.KindRParen); err != nil {
			return exp, err
		}
		exp.All = true
		return exp, nil
	}

	// list of names
	for {
		item, err := p.parseExposedItem()
		if err != nil {
			return exp, err
		}
		exp.Items = append(exp.Items, item)
		if _, ok := p.accept(lexer.KindComma); ok {
			continue
		}
		break
	}
	if _, err := p.expect(lexer.KindRParen); err != nil {
		return exp, err
	}
	return exp, nil
}

func (p *parser) parseExposedItem() (ast.ExposedItem, error) {
	t := p.peek()
	switch t.Kind {
	case lexer.KindLowerName:
		p.advance()
		return ast.ExposedItem{Pos: posOf(t), Name: t.Value}, nil
	case lexer.KindUpperName:
		p.advance()
		item := ast.ExposedItem{Pos: posOf(t), Name: t.Value}
		// optional (..) for opening constructors
		if p.peek().Kind == lexer.KindLParen {
			p.advance() // (
			if p.peek().Kind == lexer.KindDot && p.peekAt(1).Kind == lexer.KindDot {
				p.advance()
				p.advance()
				if _, err := p.expect(lexer.KindRParen); err != nil {
					return item, err
				}
				item.Open = true
			} else {
				return item, p.errorf("expected (..) after type name in exposing")
			}
		}
		return item, nil
	default:
		return ast.ExposedItem{}, p.errorf("expected exposed name, got %s", t.Kind)
	}
}

func (p *parser) parseImport() (ast.Import, error) {
	tok, err := p.expect(lexer.KindImport)
	if err != nil {
		return ast.Import{}, err
	}
	imp := ast.Import{Pos: posOf(tok)}
	name, err := p.parseModuleName()
	if err != nil {
		return imp, err
	}
	imp.Module = name
	if _, ok := p.accept(lexer.KindAs); ok {
		aliasTok, err := p.expect(lexer.KindUpperName)
		if err != nil {
			return imp, err
		}
		imp.Alias = aliasTok.Value
	}
	if _, ok := p.accept(lexer.KindExposing); ok {
		exp, err := p.parseExposing()
		if err != nil {
			return imp, err
		}
		imp.Exposing = exp
	}
	return imp, nil
}

// --- Declarations ---

// parseDecl parses one top-level declaration, recognized by what's at the
// start of a line (column 1).
func (p *parser) parseDecl() (ast.Decl, error) {
	t := p.peek()
	switch t.Kind {
	case lexer.KindType:
		// type alias ... or type ...
		return p.parseTypeDecl()
	case lexer.KindPort:
		return p.parsePortDecl()
	case lexer.KindLowerName:
		// either annotation (name : Type) or value definition (name args = body)
		next := p.peekAt(1)
		if next.Kind == lexer.KindColon {
			return p.parseAnnotationDecl()
		}
		return p.parseValueDecl()
	default:
		return nil, p.errorf("expected declaration, got %s", t.Kind)
	}
}

func (p *parser) parseTypeDecl() (ast.Decl, error) {
	typeTok, err := p.expect(lexer.KindType)
	if err != nil {
		return nil, err
	}
	if _, ok := p.accept(lexer.KindAlias); ok {
		return p.parseTypeAlias(typeTok)
	}
	return p.parseCustomType(typeTok)
}

func (p *parser) parseTypeAlias(typeTok lexer.Token) (ast.Decl, error) {
	nameTok, err := p.expect(lexer.KindUpperName)
	if err != nil {
		return nil, err
	}
	decl := &ast.TypeAliasDecl{Pos: posOf(typeTok), Name: nameTok.Value}
	for p.peek().Kind == lexer.KindLowerName {
		decl.Params = append(decl.Params, p.advance().Value)
	}
	if _, err := p.expect(lexer.KindEquals); err != nil {
		return nil, err
	}
	body, err := p.parseTypeExpr()
	if err != nil {
		return nil, err
	}
	decl.Body = body
	return decl, nil
}

func (p *parser) parseCustomType(typeTok lexer.Token) (ast.Decl, error) {
	nameTok, err := p.expect(lexer.KindUpperName)
	if err != nil {
		return nil, err
	}
	decl := &ast.CustomTypeDecl{Pos: posOf(typeTok), Name: nameTok.Value}
	for p.peek().Kind == lexer.KindLowerName {
		decl.Params = append(decl.Params, p.advance().Value)
	}
	if _, err := p.expect(lexer.KindEquals); err != nil {
		return nil, err
	}
	// Constructors separated by |
	for {
		ctor, err := p.parseConstructor()
		if err != nil {
			return nil, err
		}
		decl.Constructors = append(decl.Constructors, ctor)
		if _, ok := p.accept(lexer.KindPipe); !ok {
			break
		}
	}
	return decl, nil
}

func (p *parser) parseConstructor() (ast.Constructor, error) {
	nameTok, err := p.expect(lexer.KindUpperName)
	if err != nil {
		return ast.Constructor{}, err
	}
	ctor := ast.Constructor{Pos: posOf(nameTok), Name: nameTok.Value}
	// Constructor args: parse atomic type exprs until we hit something that
	// isn't a type atom OR breaks the boundary column (next decl starting
	// at col 1, etc.).
	for isTypeAtomStart(p.peek()) && p.peek().Column > p.boundaryCol {
		arg, err := p.parseTypeAtom()
		if err != nil {
			return ctor, err
		}
		ctor.Args = append(ctor.Args, arg)
	}
	return ctor, nil
}

func (p *parser) parseAnnotationDecl() (ast.Decl, error) {
	nameTok := p.advance() // lowerName
	if _, err := p.expect(lexer.KindColon); err != nil {
		return nil, err
	}
	body, err := p.parseTypeExpr()
	if err != nil {
		return nil, err
	}
	return &ast.AnnotationDecl{Pos: posOf(nameTok), Name: nameTok.Value, Type: body}, nil
}

func (p *parser) parseValueDecl() (ast.Decl, error) {
	nameTok := p.advance() // lowerName
	decl := &ast.ValueDecl{Pos: posOf(nameTok), Name: nameTok.Value}
	for p.peek().Kind != lexer.KindEquals && p.peek().Column > p.boundaryCol {
		pat, err := p.parsePatternAtom()
		if err != nil {
			return nil, err
		}
		decl.Params = append(decl.Params, pat)
	}
	if _, err := p.expect(lexer.KindEquals); err != nil {
		return nil, err
	}
	body, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	decl.Body = body
	return decl, nil
}

func (p *parser) parsePortDecl() (ast.Decl, error) {
	tok, err := p.expect(lexer.KindPort)
	if err != nil {
		return nil, err
	}
	nameTok, err := p.expect(lexer.KindLowerName)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.KindColon); err != nil {
		return nil, err
	}
	body, err := p.parseTypeExpr()
	if err != nil {
		return nil, err
	}
	return &ast.PortDecl{Pos: posOf(tok), Name: nameTok.Value, Type: body}, nil
}

// --- Type expressions ---

// parseTypeExpr parses a full type, including arrows. Right-associative.
//
//	t  ::=  app  ( "->" t )?
func (p *parser) parseTypeExpr() (ast.TypeExpr, error) {
	left, err := p.parseTypeApp()
	if err != nil {
		return nil, err
	}
	if p.peek().Kind == lexer.KindArrow {
		arrowTok := p.advance()
		right, err := p.parseTypeExpr()
		if err != nil {
			return nil, err
		}
		return &ast.TypeArrow{Pos: posOf(arrowTok), From: left, To: right}, nil
	}
	return left, nil
}

// parseTypeApp parses applications: List a, Result e a.
//
//	app ::= atom (atom)*
func (p *parser) parseTypeApp() (ast.TypeExpr, error) {
	first, err := p.parseTypeAtom()
	if err != nil {
		return nil, err
	}
	// Only TypeCons can be applied
	tc, isCon := first.(*ast.TypeCon)
	if !isCon {
		return first, nil
	}
	for isTypeAtomStart(p.peek()) && p.peek().Column > p.boundaryCol {
		arg, err := p.parseTypeAtom()
		if err != nil {
			return nil, err
		}
		tc.Args = append(tc.Args, arg)
	}
	return tc, nil
}

func (p *parser) parseTypeAtom() (ast.TypeExpr, error) {
	t := p.peek()
	switch t.Kind {
	case lexer.KindLowerName:
		p.advance()
		return &ast.TypeVar{Pos: posOf(t), Name: t.Value}, nil
	case lexer.KindUpperName:
		// possibly qualified: Module.Type or Module.Submodule.Type
		mod, name, err := p.parseQualifiedUpper(t)
		if err != nil {
			return nil, err
		}
		return &ast.TypeCon{Pos: posOf(t), Module: mod, Name: name}, nil
	case lexer.KindLParen:
		return p.parseTypeParens()
	case lexer.KindLBrace:
		return p.parseTypeRecord()
	default:
		return nil, p.errorf("expected type, got %s", t.Kind)
	}
}

// parseQualifiedUpper consumes a UpperName and optional .UpperName chain,
// returning (modulePath, finalName).
func (p *parser) parseQualifiedUpper(first lexer.Token) (ast.ModuleName, string, error) {
	p.advance() // consume the first UpperName
	parts := []string{first.Value}
	for p.peek().Kind == lexer.KindDot && p.peekAt(1).Kind == lexer.KindUpperName {
		p.advance() // .
		parts = append(parts, p.advance().Value)
	}
	if len(parts) == 1 {
		return nil, parts[0], nil
	}
	return ast.ModuleName(parts[:len(parts)-1]), parts[len(parts)-1], nil
}

// parseTypeParens handles (), (T), or (T, U, ...) tuple
func (p *parser) parseTypeParens() (ast.TypeExpr, error) {
	openTok, _ := p.expect(lexer.KindLParen)
	if _, ok := p.accept(lexer.KindRParen); ok {
		return &ast.TypeUnit{Pos: posOf(openTok)}, nil
	}
	first, err := p.parseTypeExpr()
	if err != nil {
		return nil, err
	}
	if _, ok := p.accept(lexer.KindRParen); ok {
		return first, nil // grouped
	}
	// tuple
	members := []ast.TypeExpr{first}
	for {
		if _, ok := p.accept(lexer.KindComma); !ok {
			break
		}
		next, err := p.parseTypeExpr()
		if err != nil {
			return nil, err
		}
		members = append(members, next)
	}
	if _, err := p.expect(lexer.KindRParen); err != nil {
		return nil, err
	}
	return &ast.TypeTuple{Pos: posOf(openTok), Members: members}, nil
}

// parseTypeRecord handles { f : T, ... } or { r | f : T, ... }
func (p *parser) parseTypeRecord() (ast.TypeExpr, error) {
	openTok, _ := p.expect(lexer.KindLBrace)
	rec := &ast.TypeRecord{Pos: posOf(openTok)}
	if _, ok := p.accept(lexer.KindRBrace); ok {
		return rec, nil
	}
	// peek for row extension: { lowerName | ... }
	if p.peek().Kind == lexer.KindLowerName && p.peekAt(1).Kind == lexer.KindPipe {
		rec.Extends = p.advance().Value
		p.advance() // |
	}
	for {
		nameTok, err := p.expect(lexer.KindLowerName)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.KindColon); err != nil {
			return nil, err
		}
		ftype, err := p.parseTypeExpr()
		if err != nil {
			return nil, err
		}
		rec.Fields = append(rec.Fields, ast.TypeRecField{
			Pos: posOf(nameTok), Name: nameTok.Value, Type: ftype,
		})
		if _, ok := p.accept(lexer.KindComma); !ok {
			break
		}
	}
	if _, err := p.expect(lexer.KindRBrace); err != nil {
		return nil, err
	}
	return rec, nil
}

func isTypeAtomStart(t lexer.Token) bool {
	switch t.Kind {
	case lexer.KindLowerName, lexer.KindUpperName, lexer.KindLParen, lexer.KindLBrace:
		return true
	}
	return false
}

// --- Patterns ---

// parsePatternAtom parses a non-applied pattern.
// Constructor patterns with args (Just x) need parens here.
func (p *parser) parsePatternAtom() (ast.Pattern, error) {
	t := p.peek()
	switch t.Kind {
	case lexer.KindLowerName:
		p.advance()
		return &ast.PVar{Pos: posOf(t), Name: t.Value}, nil
	case lexer.KindUnderscore:
		p.advance()
		return &ast.PWildcard{Pos: posOf(t)}, nil
	case lexer.KindUpperName:
		// constructor with no args (atomic)
		mod, name, err := p.parseQualifiedUpper(t)
		if err != nil {
			return nil, err
		}
		return &ast.PCtor{Pos: posOf(t), Module: mod, Name: name}, nil
	case lexer.KindInt:
		p.advance()
		v, err := strconv.ParseInt(t.Value, 10, 64)
		if err != nil {
			return nil, &Error{Line: t.Line, Column: t.Column, Message: fmt.Sprintf("integer literal %s is out of range for Int (64-bit signed)", t.Value)}
		}
		return &ast.PInt{Pos: posOf(t), Value: v}, nil
	case lexer.KindString:
		p.advance()
		return &ast.PString{Pos: posOf(t), Value: t.Value}, nil
	case lexer.KindChar:
		p.advance()
		r, _ := utf8.DecodeRuneInString(t.Value)
		return &ast.PChar{Pos: posOf(t), Value: r}, nil
	case lexer.KindLParen:
		return p.parsePatternParens()
	case lexer.KindLBrace:
		return p.parsePatternRecord()
	case lexer.KindLBracket:
		return p.parsePatternList()
	default:
		return nil, p.errorf("expected pattern, got %s", t.Kind)
	}
}

func (p *parser) parsePatternList() (ast.Pattern, error) {
	openTok, _ := p.expect(lexer.KindLBracket)
	if _, ok := p.accept(lexer.KindRBracket); ok {
		return &ast.PList{Pos: posOf(openTok)}, nil
	}
	var elems []ast.Pattern
	for {
		e, err := p.parsePattern()
		if err != nil {
			return nil, err
		}
		elems = append(elems, e)
		if _, ok := p.accept(lexer.KindComma); !ok {
			break
		}
	}
	if _, err := p.expect(lexer.KindRBracket); err != nil {
		return nil, err
	}
	return &ast.PList{Pos: posOf(openTok), Elements: elems}, nil
}

// parsePatternParens: (), (p), (p, q), or (Ctor a b)
func (p *parser) parsePatternParens() (ast.Pattern, error) {
	openTok, _ := p.expect(lexer.KindLParen)
	if _, ok := p.accept(lexer.KindRParen); ok {
		return &ast.PUnit{Pos: posOf(openTok)}, nil
	}
	first, err := p.parsePattern()
	if err != nil {
		return nil, err
	}
	if _, ok := p.accept(lexer.KindRParen); ok {
		return first, nil
	}
	members := []ast.Pattern{first}
	for {
		if _, ok := p.accept(lexer.KindComma); !ok {
			break
		}
		next, err := p.parsePattern()
		if err != nil {
			return nil, err
		}
		members = append(members, next)
	}
	if _, err := p.expect(lexer.KindRParen); err != nil {
		return nil, err
	}
	return &ast.PTuple{Pos: posOf(openTok), Members: members}, nil
}

// parsePattern parses a full pattern, allowing constructor application and
// the cons operator `head :: tail`.
//
//	p     ::= ctor pat* | cons
//	cons  ::= atom ("::" cons)?
func (p *parser) parsePattern() (ast.Pattern, error) {
	t := p.peek()
	if t.Kind == lexer.KindUpperName {
		mod, name, err := p.parseQualifiedUpper(t)
		if err != nil {
			return nil, err
		}
		ctor := &ast.PCtor{Pos: posOf(t), Module: mod, Name: name}
		for isPatternAtomStart(p.peek()) {
			arg, err := p.parsePatternAtom()
			if err != nil {
				return nil, err
			}
			ctor.Args = append(ctor.Args, arg)
		}
		// Allow `Just x :: rest` style — wrap ctor in cons if `::` follows.
		return p.parsePatternConsTail(ctor)
	}
	atom, err := p.parsePatternAtom()
	if err != nil {
		return nil, err
	}
	return p.parsePatternConsTail(atom)
}

// parsePatternConsTail handles optional `:: tail` after a pattern.
// Right-associative, so `a :: b :: rest` is `a :: (b :: rest)`.
func (p *parser) parsePatternConsTail(head ast.Pattern) (ast.Pattern, error) {
	if p.peek().Kind != lexer.KindDoubleCol {
		return head, nil
	}
	consTok := p.advance()
	tail, err := p.parsePattern()
	if err != nil {
		return nil, err
	}
	return &ast.PCons{Pos: posOf(consTok), Head: head, Tail: tail}, nil
}

func (p *parser) parsePatternRecord() (ast.Pattern, error) {
	openTok, _ := p.expect(lexer.KindLBrace)
	rec := &ast.PRecord{Pos: posOf(openTok)}
	if _, ok := p.accept(lexer.KindRBrace); ok {
		return rec, nil
	}
	for {
		nameTok, err := p.expect(lexer.KindLowerName)
		if err != nil {
			return nil, err
		}
		rec.Fields = append(rec.Fields, nameTok.Value)
		if _, ok := p.accept(lexer.KindComma); !ok {
			break
		}
	}
	if _, err := p.expect(lexer.KindRBrace); err != nil {
		return nil, err
	}
	return rec, nil
}

func isPatternAtomStart(t lexer.Token) bool {
	switch t.Kind {
	case lexer.KindLowerName, lexer.KindUpperName, lexer.KindUnderscore,
		lexer.KindInt, lexer.KindString, lexer.KindChar,
		lexer.KindLParen, lexer.KindLBrace, lexer.KindLBracket:
		return true
	}
	return false
}

// --- Expressions ---

// parseExpr parses a full expression. Operator precedence is handled in a
// separate pass via a Pratt-style loop on binary ops.
func (p *parser) parseExpr() (ast.Expr, error) {
	return p.parseBinop(0)
}

// Binary operator precedence (loosely Elm-aligned, simplified).
// Higher number binds tighter.
type opInfo struct {
	prec   int
	rAssoc bool
}

var opTable = map[string]opInfo{
	"|>": {1, false},
	"<|": {1, true},
	"||": {2, true},
	"&&": {3, true},
	"==": {4, false},
	"/=": {4, false},
	"<":  {4, false},
	">":  {4, false},
	"<=": {4, false},
	">=": {4, false},
	"++": {5, true},
	"::": {5, true},
	"+":  {6, false},
	"-":  {6, false},
	"*":  {7, false},
	"/":  {7, false},
}

func tokenToOp(t lexer.Token) (string, bool) {
	switch t.Kind {
	case lexer.KindPipeRight:
		return "|>", true
	case lexer.KindPipeLeft:
		return "<|", true
	case lexer.KindOr:
		return "||", true
	case lexer.KindAnd:
		return "&&", true
	case lexer.KindEqualsEq:
		return "==", true
	case lexer.KindNotEq:
		return "/=", true
	case lexer.KindLT:
		return "<", true
	case lexer.KindGT:
		return ">", true
	case lexer.KindLTE:
		return "<=", true
	case lexer.KindGTE:
		return ">=", true
	case lexer.KindAppend:
		return "++", true
	case lexer.KindDoubleCol:
		return "::", true
	case lexer.KindPlus:
		return "+", true
	case lexer.KindMinus:
		return "-", true
	case lexer.KindStar:
		return "*", true
	case lexer.KindSlash:
		return "/", true
	}
	return "", false
}

// parseBinop is a Pratt-like precedence climbing loop.
func (p *parser) parseBinop(minPrec int) (ast.Expr, error) {
	left, err := p.parseApp()
	if err != nil {
		return nil, err
	}
	for {
		op, ok := tokenToOp(p.peek())
		if !ok {
			return p.withEnd(left), nil
		}
		info, known := opTable[op]
		if !known || info.prec < minPrec {
			return p.withEnd(left), nil
		}
		opTok := p.advance()
		nextMin := info.prec + 1
		if info.rAssoc {
			nextMin = info.prec
		}
		right, err := p.parseBinop(nextMin)
		if err != nil {
			return nil, err
		}
		left = &ast.EBinop{Pos: posOf(opTok), Op: op, Left: left, Right: right}
	}
}

// parseApp parses function application via juxtaposition. Field access binds
// tighter than application: `f x.y` parses as `f (x.y)`, not `(f x).y`.
//
//	atom_chain ::= atom ("." lowerName)*
//	app        ::= atom_chain (atom_chain)*
func (p *parser) parseApp() (ast.Expr, error) {
	// unary minus
	if p.peek().Kind == lexer.KindMinus {
		minusTok := p.advance()
		inner, err := p.parseApp()
		if err != nil {
			return nil, err
		}
		return p.withEnd(&ast.ENegate{Pos: posOf(minusTok), Inner: inner}), nil
	}
	first, err := p.parseAtomChain()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if !isExprAtomStart(t) || t.Column <= p.boundaryCol {
			break
		}
		arg, err := p.parseAtomChain()
		if err != nil {
			return nil, err
		}
		first = &ast.EApp{Pos: first.Position(), Fn: first, Arg: arg}
	}
	return p.withEnd(first), nil
}

// parseAtomChain parses an atom followed by zero or more `.field` accesses.
// This binds tighter than application.
func (p *parser) parseAtomChain() (ast.Expr, error) {
	atom, err := p.parseExprAtom()
	if err != nil {
		return nil, err
	}
	for p.peek().Kind == lexer.KindDot && p.peekAt(1).Kind == lexer.KindLowerName {
		p.advance() // .
		fieldTok := p.advance()
		atom = &ast.EFieldAccess{Pos: posOf(fieldTok), Record: atom, Field: fieldTok.Value}
	}
	return p.withEnd(atom), nil
}

// parseExprAtom parses a single non-application expression and stamps
// its source-span end. Field chains are handled in parseApp, not here.
func (p *parser) parseExprAtom() (ast.Expr, error) {
	e, err := p.parseExprAtomCore()
	if err != nil {
		return nil, err
	}
	return p.withEnd(e), nil
}

func (p *parser) parseExprAtomCore() (ast.Expr, error) {
	t := p.peek()
	switch t.Kind {
	case lexer.KindInt:
		p.advance()
		v, err := strconv.ParseInt(t.Value, 10, 64)
		if err != nil {
			return nil, &Error{Line: t.Line, Column: t.Column, Message: fmt.Sprintf("integer literal %s is out of range for Int (64-bit signed)", t.Value)}
		}
		return &ast.EInt{Pos: posOf(t), Value: v}, nil
	case lexer.KindFloat:
		p.advance()
		v, err := strconv.ParseFloat(t.Value, 64)
		if err != nil {
			return nil, &Error{Line: t.Line, Column: t.Column, Message: fmt.Sprintf("float literal %s is out of range for Float (64-bit)", t.Value)}
		}
		return &ast.EFloat{Pos: posOf(t), Value: v}, nil
	case lexer.KindString:
		p.advance()
		return &ast.EString{Pos: posOf(t), Value: t.Value}, nil
	case lexer.KindChar:
		p.advance()
		// Token.Value carries the decoded character as a UTF-8 string
		// (lexer.readChar emitted `string(r)`). Decode the single rune
		// back; invalid encoding shouldn't happen here since the lexer
		// validated.
		r, _ := utf8.DecodeRuneInString(t.Value)
		return &ast.EChar{Pos: posOf(t), Value: r}, nil
	case lexer.KindLowerName:
		p.advance()
		return &ast.EVar{Pos: posOf(t), Name: t.Value}, nil
	case lexer.KindUpperName:
		mod, name, err := p.parseQualifiedUpperOrValue(t)
		if err != nil {
			return nil, err
		}
		return p.makeQualifiedExpr(t, mod, name), nil
	case lexer.KindDot:
		// Bare `.name` reaches here only when the lexer didn't mark it as
		// a FieldDot (no preceding whitespace at start of expression).
		if p.peekAt(1).Kind != lexer.KindLowerName {
			return nil, p.errorf("expected field name after '.'")
		}
		p.advance() // .
		nameTok := p.advance()
		return &ast.EFieldAccessor{Pos: posOf(t), Field: nameTok.Value}, nil
	case lexer.KindFieldDot:
		p.advance()
		return &ast.EFieldAccessor{Pos: posOf(t), Field: t.Value}, nil
	case lexer.KindLParen:
		return p.parseExprParens()
	case lexer.KindLBracket:
		return p.parseExprList()
	case lexer.KindLBrace:
		return p.parseExprRecord()
	case lexer.KindBackslash:
		return p.parseLambda()
	case lexer.KindIf:
		return p.parseIf()
	case lexer.KindCase:
		return p.parseCase()
	case lexer.KindLet:
		return p.parseLet()
	default:
		return nil, p.errorf("expected expression, got %s", t.Kind)
	}
}

// makeQualifiedExpr returns ECtor (uppercase final segment) or EQualified
// (lowercase final segment, treated as a qualified value reference).
func (p *parser) makeQualifiedExpr(first lexer.Token, mod ast.ModuleName, name string) ast.Expr {
	if isUpperFirst(name) {
		return &ast.ECtor{Pos: posOf(first), Module: mod, Name: name}
	}
	return &ast.EQualified{Pos: posOf(first), Module: mod, Name: name}
}

func isUpperFirst(name string) bool {
	if name == "" {
		return false
	}
	c := name[0]
	return c >= 'A' && c <= 'Z'
}

// parseQualifiedUpperOrValue: parses a Module-prefixed name. The final
// segment may be UpperName (constructor or type) or LowerName (value).
//
//	Foo            -> mod=nil, name="Foo"
//	Foo.Bar        -> mod=[Foo], name="Bar"
//	Foo.Bar.value  -> mod=[Foo, Bar], name="value"
//	Foo.value      -> mod=[Foo], name="value"
func (p *parser) parseQualifiedUpperOrValue(first lexer.Token) (ast.ModuleName, string, error) {
	p.advance() // consume the first UpperName
	parts := []string{first.Value}
chain:
	for p.peek().Kind == lexer.KindDot {
		next := p.peekAt(1)
		switch next.Kind {
		case lexer.KindUpperName:
			p.advance() // .
			parts = append(parts, p.advance().Value)
		case lexer.KindLowerName:
			p.advance() // .
			finalName := p.advance().Value
			// All previous parts are the module path
			return ast.ModuleName(parts), finalName, nil
		default:
			// Not a qualified-value continuation; stop the chain.
			break chain
		}
	}
	if len(parts) == 1 {
		return nil, parts[0], nil
	}
	return ast.ModuleName(parts[:len(parts)-1]), parts[len(parts)-1], nil
}

func (p *parser) parseExprParens() (ast.Expr, error) {
	openTok, _ := p.expect(lexer.KindLParen)
	if _, ok := p.accept(lexer.KindRParen); ok {
		return &ast.EUnit{Pos: posOf(openTok)}, nil
	}
	first, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, ok := p.accept(lexer.KindRParen); ok {
		return first, nil // grouping
	}
	members := []ast.Expr{first}
	for {
		if _, ok := p.accept(lexer.KindComma); !ok {
			break
		}
		next, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		members = append(members, next)
	}
	if _, err := p.expect(lexer.KindRParen); err != nil {
		return nil, err
	}
	return &ast.ETuple{Pos: posOf(openTok), Members: members}, nil
}

func (p *parser) parseExprList() (ast.Expr, error) {
	openTok, _ := p.expect(lexer.KindLBracket)
	if _, ok := p.accept(lexer.KindRBracket); ok {
		return &ast.EList{Pos: posOf(openTok)}, nil
	}
	var elems []ast.Expr
	for {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		elems = append(elems, e)
		if _, ok := p.accept(lexer.KindComma); !ok {
			break
		}
	}
	if _, err := p.expect(lexer.KindRBracket); err != nil {
		return nil, err
	}
	return &ast.EList{Pos: posOf(openTok), Elements: elems}, nil
}

// parseExprRecord: { a = 1, b = 2 } or { r | a = 1, b = 2 }
func (p *parser) parseExprRecord() (ast.Expr, error) {
	openTok, _ := p.expect(lexer.KindLBrace)
	if _, ok := p.accept(lexer.KindRBrace); ok {
		return &ast.ERecord{Pos: posOf(openTok)}, nil
	}
	// Look ahead for record update: lowerName |
	if p.peek().Kind == lexer.KindLowerName && p.peekAt(1).Kind == lexer.KindPipe {
		base := p.advance() // record name
		baseExpr := &ast.EVar{Pos: posOf(base), Name: base.Value}
		p.advance() // |
		update := &ast.ERecordUpdate{Pos: posOf(openTok), Record: baseExpr}
		for {
			f, err := p.parseRecordField()
			if err != nil {
				return nil, err
			}
			update.Fields = append(update.Fields, f)
			if _, ok := p.accept(lexer.KindComma); !ok {
				break
			}
		}
		if _, err := p.expect(lexer.KindRBrace); err != nil {
			return nil, err
		}
		return update, nil
	}
	rec := &ast.ERecord{Pos: posOf(openTok)}
	for {
		f, err := p.parseRecordField()
		if err != nil {
			return nil, err
		}
		rec.Fields = append(rec.Fields, f)
		if _, ok := p.accept(lexer.KindComma); !ok {
			break
		}
	}
	if _, err := p.expect(lexer.KindRBrace); err != nil {
		return nil, err
	}
	return rec, nil
}

func (p *parser) parseRecordField() (ast.RecField, error) {
	nameTok, err := p.expect(lexer.KindLowerName)
	if err != nil {
		return ast.RecField{}, err
	}
	if _, err := p.expect(lexer.KindEquals); err != nil {
		return ast.RecField{}, err
	}
	val, err := p.parseExpr()
	if err != nil {
		return ast.RecField{}, err
	}
	return ast.RecField{Pos: posOf(nameTok), Name: nameTok.Value, Value: val}, nil
}

func (p *parser) parseLambda() (ast.Expr, error) {
	tok, _ := p.expect(lexer.KindBackslash)
	lam := &ast.ELambda{Pos: posOf(tok)}
	for p.peek().Kind != lexer.KindArrow {
		pat, err := p.parsePatternAtom()
		if err != nil {
			return nil, err
		}
		lam.Params = append(lam.Params, pat)
	}
	if _, err := p.expect(lexer.KindArrow); err != nil {
		return nil, err
	}
	body, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	lam.Body = body
	return lam, nil
}

func (p *parser) parseIf() (ast.Expr, error) {
	tok, _ := p.expect(lexer.KindIf)
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.KindThen); err != nil {
		return nil, err
	}
	thenE, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.KindElse); err != nil {
		return nil, err
	}
	elseE, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &ast.EIf{Pos: posOf(tok), Cond: cond, Then: thenE, Else: elseE}, nil
}

func (p *parser) parseCase() (ast.Expr, error) {
	tok, _ := p.expect(lexer.KindCase)
	subj, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.KindOf); err != nil {
		return nil, err
	}
	caseExpr := &ast.ECase{Pos: posOf(tok), Subject: subj}

	if !isPatternAtomStart(p.peek()) {
		return nil, p.errorf("expected case branch pattern, got %s", p.peek().Kind)
	}
	branchCol := p.peek().Column

	// Inside a branch body, parseApp/parseExpr should stop at any token whose
	// column <= branchCol — that's the next branch (or something outside).
	saved := p.boundaryCol
	p.boundaryCol = branchCol
	defer func() { p.boundaryCol = saved }()

	for isPatternAtomStart(p.peek()) && p.peek().Column == branchCol {
		branchTok := p.peek()
		pat, err := p.parsePattern()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.KindArrow); err != nil {
			return nil, err
		}
		body, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		caseExpr.Branches = append(caseExpr.Branches, ast.CaseBranch{
			Pos: posOf(branchTok), Pattern: pat, Body: body,
		})
	}
	return caseExpr, nil
}

// parseLet: let bindings in body
//
// Supports both = and <- bindings:
//
//	let
//	    x = 1
//	    y <- effect
//	in
//	    x + ...
func (p *parser) parseLet() (ast.Expr, error) {
	tok, _ := p.expect(lexer.KindLet)
	let := &ast.ELet{Pos: posOf(tok)}

	if p.peek().Kind == lexer.KindIn {
		return nil, p.errorf("let needs at least one binding")
	}
	bindCol := p.peek().Column

	saved := p.boundaryCol
	p.boundaryCol = bindCol
	for p.peek().Kind != lexer.KindIn && p.peek().Column == bindCol {
		b, err := p.parseLetBinding()
		if err != nil {
			p.boundaryCol = saved
			return nil, err
		}
		let.Bindings = append(let.Bindings, b)
	}
	p.boundaryCol = saved

	if _, err := p.expect(lexer.KindIn); err != nil {
		return nil, err
	}
	body, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	let.Body = body
	return let, nil
}

func (p *parser) parseLetBinding() (ast.LetBinding, error) {
	startTok := p.peek()
	pat, err := p.parsePattern()
	if err != nil {
		return ast.LetBinding{}, err
	}
	binding := ast.LetBinding{Pos: posOf(startTok), Pattern: pat}
	switch p.peek().Kind {
	case lexer.KindEquals:
		p.advance()
	case lexer.KindBindArrow:
		p.advance()
		binding.IsBind = true
	default:
		return binding, p.errorf("expected '=' or '<-' in let binding, got %s", p.peek().Kind)
	}
	body, err := p.parseExpr()
	if err != nil {
		return binding, err
	}
	binding.Body = body
	return binding, nil
}

// isExprAtomStart returns whether the token can begin an expression atom
// (used to decide if function application should continue).
func isExprAtomStart(t lexer.Token) bool {
	switch t.Kind {
	case lexer.KindInt, lexer.KindFloat, lexer.KindString, lexer.KindChar,
		lexer.KindLowerName, lexer.KindUpperName, lexer.KindFieldDot,
		lexer.KindLParen, lexer.KindLBracket, lexer.KindLBrace:
		return true
	}
	return false
}
