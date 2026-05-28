// Package lexer tokenizes Mar source code (Elm-style syntax).
//
// The lexer produces a stream of tokens with position information. It does
// not perform layout-sensitive insertion of virtual braces/semicolons; that
// responsibility is left to the parser, which has access to line/column
// info on each token.
package lexer

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Kind classifies a token.
type Kind string

const (
	// Literals
	KindInt    Kind = "int"
	KindFloat  Kind = "float"
	KindString Kind = "string"
	// KindChar — single-quoted Unicode code point literal, e.g. 'a',
	// '\n', '\u{1F600}'. The Value field carries the decoded
	// single-character string (UTF-8 of the rune), so a parser can do
	// utf8.DecodeRuneInString(tok.Value) to get the int code point.
	KindChar Kind = "char"

	// Identifiers
	KindLowerName Kind = "lowerName" // lowercase: values, fields
	KindUpperName Kind = "upperName" // uppercase: types, constructors, modules
	KindFieldDot  Kind = "fieldDot"  // .fieldName accessor

	// Keywords
	KindModule   Kind = "module"
	KindExposing Kind = "exposing"
	KindImport   Kind = "import"
	KindAs       Kind = "as"
	KindType     Kind = "type"
	KindAlias    Kind = "alias"
	KindIf       Kind = "if"
	KindThen     Kind = "then"
	KindElse     Kind = "else"
	KindCase     Kind = "case"
	KindOf       Kind = "of"
	KindLet      Kind = "let"
	KindIn       Kind = "in"
	KindWhere    Kind = "where"
	KindPort     Kind = "port"

	// Punctuation
	KindLParen     Kind = "("
	KindRParen     Kind = ")"
	KindLBracket   Kind = "["
	KindRBracket   Kind = "]"
	KindLBrace     Kind = "{"
	KindRBrace     Kind = "}"
	KindComma      Kind = ","
	KindSemicolon  Kind = ";"
	KindUnderscore Kind = "_"
	KindBackslash  Kind = "\\"

	// Operators
	KindEquals    Kind = "="
	KindArrow     Kind = "->"
	KindPipeRight Kind = "|>"
	KindPipeLeft  Kind = "<|"
	KindPipe      Kind = "|"
	KindColon     Kind = ":"
	KindDoubleCol Kind = "::"
	KindBindArrow Kind = "<-"
	KindEqualsEq  Kind = "=="
	KindNotEq     Kind = "/="
	KindLT        Kind = "<"
	KindGT        Kind = ">"
	KindLTE       Kind = "<="
	KindGTE       Kind = ">="
	KindAnd       Kind = "&&"
	KindOr        Kind = "||"
	KindPlus      Kind = "+"
	KindMinus     Kind = "-"
	KindStar      Kind = "*"
	KindSlash     Kind = "/"
	KindAppend    Kind = "++"
	KindDot       Kind = "."

	// Layout / structural
	KindNewline Kind = "newline" // emitted only when meaningful for layout; not yet used
	KindEOF     Kind = "eof"
)

// Token is a lexed unit of source.
type Token struct {
	Kind   Kind
	Value  string // raw lexeme (for literals/identifiers); for keywords/punct, equal to Kind
	Line   int    // 1-indexed
	Column int    // 1-indexed (rune column)
}

func (t Token) String() string {
	if t.Value == "" || t.Value == string(t.Kind) {
		return fmt.Sprintf("%s @%d:%d", t.Kind, t.Line, t.Column)
	}
	return fmt.Sprintf("%s(%q) @%d:%d", t.Kind, t.Value, t.Line, t.Column)
}

// Error carries position info alongside a message.
type Error struct {
	Line    int
	Column  int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("lex error at %d:%d: %s", e.Line, e.Column, e.Message)
}

// Lex tokenizes the entire input. Returns the token stream plus any error
// encountered. On error the partial token list up to the failure point is
// returned alongside.
func Lex(src string) ([]Token, error) {
	l := newLexer(src)
	// At start of input, treat as preceded by whitespace.
	l.hadWhitespace = true
	for {
		before := l.pos
		l.skipTrivia()
		if l.pos > before {
			l.hadWhitespace = true
		}
		if l.eof() {
			break
		}
		if err := l.readToken(); err != nil {
			return l.tokens, err
		}
		l.hadWhitespace = false
	}
	l.emit(KindEOF, "")
	return l.tokens, nil
}

// --- internals ---

type lexer struct {
	src    []rune
	pos    int
	line   int
	column int

	tokens []Token

	// position recorded at the start of the current token
	startLine   int
	startColumn int

	// hadWhitespace is true when whitespace or a comment was skipped just
	// before the current position. Used by field-accessor lexing to
	// distinguish `.field` (after whitespace = accessor function) from
	// `expr.field` (no whitespace = field access on expr).
	hadWhitespace bool
}

func newLexer(src string) *lexer {
	return &lexer{
		src:    []rune(normalizeNewlines(src)),
		line:   1,
		column: 1,
	}
}

func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

func (l *lexer) eof() bool {
	return l.pos >= len(l.src)
}

func (l *lexer) peek() rune {
	if l.eof() {
		return 0
	}
	return l.src[l.pos]
}

func (l *lexer) peekAt(offset int) rune {
	if l.pos+offset >= len(l.src) {
		return 0
	}
	return l.src[l.pos+offset]
}

func (l *lexer) advance() rune {
	if l.eof() {
		return 0
	}
	r := l.src[l.pos]
	l.pos++
	if r == '\n' {
		l.line++
		l.column = 1
	} else {
		l.column++
	}
	return r
}

func (l *lexer) emit(kind Kind, value string) {
	l.tokens = append(l.tokens, Token{
		Kind:   kind,
		Value:  value,
		Line:   l.startLine,
		Column: l.startColumn,
	})
}

func (l *lexer) errorf(format string, args ...any) *Error {
	return &Error{
		Line:    l.line,
		Column:  l.column,
		Message: fmt.Sprintf(format, args...),
	}
}

// skipTrivia skips whitespace and comments. Newlines are skipped here for now
// (layout will be handled later if needed).
func (l *lexer) skipTrivia() {
	for !l.eof() {
		r := l.peek()
		switch {
		case r == ' ' || r == '\t' || r == '\n':
			l.advance()
		case r == '-' && l.peekAt(1) == '-':
			// line comment
			for !l.eof() && l.peek() != '\n' {
				l.advance()
			}
		case r == '{' && l.peekAt(1) == '-':
			// block comment, supports nesting
			l.advance() // {
			l.advance() // -
			depth := 1
			for !l.eof() && depth > 0 {
				if l.peek() == '{' && l.peekAt(1) == '-' {
					l.advance()
					l.advance()
					depth++
				} else if l.peek() == '-' && l.peekAt(1) == '}' {
					l.advance()
					l.advance()
					depth--
				} else {
					l.advance()
				}
			}
		default:
			return
		}
	}
}

func (l *lexer) markStart() {
	l.startLine = l.line
	l.startColumn = l.column
}

func (l *lexer) readToken() error {
	l.markStart()
	r := l.peek()

	switch {
	case isDigit(r):
		return l.readNumber()
	case r == '"':
		return l.readString()
	case r == '\'':
		return l.readChar()
	case r == '.' && isLower(l.peekAt(1)) && l.hadWhitespace:
		// .field with preceding whitespace = accessor function
		return l.readFieldAccessor()
	case isLower(r):
		l.readLowerName()
		return nil
	case isUpper(r):
		l.readUpperName()
		return nil
	case r == '_' && isIdentTail(l.peekAt(1)):
		// _foo: identifier starting with underscore (treated as lowerName)
		l.readLowerName()
		return nil
	case r == '_':
		l.advance()
		l.emit(KindUnderscore, "_")
		return nil
	case r == '\\':
		l.advance()
		l.emit(KindBackslash, "\\")
		return nil
	default:
		return l.readPunctOrOperator()
	}
}

// readNumber: integer or float (no scientific notation yet)
func (l *lexer) readNumber() error {
	start := l.pos
	for !l.eof() && isDigit(l.peek()) {
		l.advance()
	}
	isFloat := false
	if l.peek() == '.' && isDigit(l.peekAt(1)) {
		isFloat = true
		l.advance() // .
		for !l.eof() && isDigit(l.peek()) {
			l.advance()
		}
	}
	value := string(l.src[start:l.pos])
	if isFloat {
		l.emit(KindFloat, value)
	} else {
		l.emit(KindInt, value)
	}
	return nil
}

// readString: simple double-quoted string with backslash escapes.
func (l *lexer) readString() error {
	l.advance() // opening "
	var sb strings.Builder
	for !l.eof() {
		r := l.peek()
		switch r {
		case '"':
			l.advance()
			l.emit(KindString, sb.String())
			return nil
		case '\\':
			l.advance()
			esc := l.peek()
			switch esc {
			case 'n':
				sb.WriteRune('\n')
			case 't':
				sb.WriteRune('\t')
			case 'r':
				sb.WriteRune('\r')
			case '"':
				sb.WriteRune('"')
			case '\\':
				sb.WriteRune('\\')
			default:
				return l.errorf("unknown string escape: \\%c", esc)
			}
			l.advance()
		case '\n':
			return l.errorf("unterminated string literal")
		default:
			sb.WriteRune(r)
			l.advance()
		}
	}
	return l.errorf("unterminated string literal")
}

// readChar: single-quoted character literal — 'a', '\n', '\u{1F600}'.
//
// One Unicode code point per literal (rune in Go, Unicode.Scalar in
// Swift, code point in JS). The contents are exactly ONE character —
// `'ab'` is a lex error, not a two-char "string". Use a String for
// that.
//
// Supported escapes mirror the string lexer plus `\u{HEX}` for
// arbitrary code points up to U+10FFFF; surrogates (D800-DFFF) are
// rejected here rather than letting an invalid value reach the
// runtime (would explode on Swift's Unicode.Scalar init).
func (l *lexer) readChar() error {
	l.advance() // opening '
	if l.eof() {
		return l.errorf("unterminated char literal")
	}
	var r rune
	switch l.peek() {
	case '\'':
		return l.errorf("empty char literal")
	case '\n':
		return l.errorf("unterminated char literal")
	case '\\':
		l.advance()
		esc := l.peek()
		switch esc {
		case 'n':
			r = '\n'
			l.advance()
		case 't':
			r = '\t'
			l.advance()
		case 'r':
			r = '\r'
			l.advance()
		case '\'':
			r = '\''
			l.advance()
		case '"':
			r = '"'
			l.advance()
		case '\\':
			r = '\\'
			l.advance()
		case 'u':
			// \u{HEX} — up to 6 hex digits, must be a valid scalar.
			l.advance() // u
			if l.peek() != '{' {
				return l.errorf("expected '{' after \\u")
			}
			l.advance() // {
			start := l.pos
			for !l.eof() && l.peek() != '}' {
				if !isHexDigit(l.peek()) {
					return l.errorf("invalid hex digit in \\u escape")
				}
				l.advance()
			}
			if l.eof() {
				return l.errorf("unterminated \\u escape")
			}
			hex := string(l.src[start:l.pos])
			if hex == "" {
				return l.errorf("empty \\u escape")
			}
			l.advance() // }
			n, err := strconv.ParseInt(hex, 16, 32)
			if err != nil {
				return l.errorf("invalid \\u escape: %v", err)
			}
			if n < 0 || n > 0x10FFFF {
				return l.errorf("\\u escape out of range: %x", n)
			}
			if n >= 0xD800 && n <= 0xDFFF {
				return l.errorf("\\u escape is a surrogate (invalid scalar): %x", n)
			}
			r = rune(n)
		default:
			return l.errorf("unknown char escape: \\%c", esc)
		}
	default:
		r = l.peek()
		l.advance()
	}
	if l.eof() || l.peek() != '\'' {
		return l.errorf("expected closing ' in char literal")
	}
	l.advance() // closing '
	l.emit(KindChar, string(r))
	return nil
}

func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// readFieldAccessor: .foo
func (l *lexer) readFieldAccessor() error {
	l.advance() // .
	start := l.pos
	if !isLower(l.peek()) {
		return l.errorf("expected identifier after '.'")
	}
	l.advance()
	for !l.eof() && isIdentTail(l.peek()) {
		l.advance()
	}
	name := string(l.src[start:l.pos])
	l.emit(KindFieldDot, name)
	return nil
}

func (l *lexer) readLowerName() {
	start := l.pos
	l.advance()
	for !l.eof() && isIdentTail(l.peek()) {
		l.advance()
	}
	name := string(l.src[start:l.pos])
	if k, ok := keywordKind(name); ok {
		l.emit(k, name)
		return
	}
	l.emit(KindLowerName, name)
}

func (l *lexer) readUpperName() {
	start := l.pos
	l.advance()
	for !l.eof() && isIdentTail(l.peek()) {
		l.advance()
	}
	name := string(l.src[start:l.pos])
	l.emit(KindUpperName, name)
}

// readPunctOrOperator handles the remaining punctuation and multi-char ops.
func (l *lexer) readPunctOrOperator() error {
	r := l.peek()
	r2 := l.peekAt(1)

	// Two-character operators first
	switch {
	case r == '-' && r2 == '>':
		l.advance()
		l.advance()
		l.emit(KindArrow, "->")
		return nil
	case r == '|' && r2 == '>':
		l.advance()
		l.advance()
		l.emit(KindPipeRight, "|>")
		return nil
	case r == '<' && r2 == '|':
		l.advance()
		l.advance()
		l.emit(KindPipeLeft, "<|")
		return nil
	case r == '<' && r2 == '-':
		l.advance()
		l.advance()
		l.emit(KindBindArrow, "<-")
		return nil
	case r == '=' && r2 == '=':
		l.advance()
		l.advance()
		l.emit(KindEqualsEq, "==")
		return nil
	case r == '/' && r2 == '=':
		l.advance()
		l.advance()
		l.emit(KindNotEq, "/=")
		return nil
	case r == '<' && r2 == '=':
		l.advance()
		l.advance()
		l.emit(KindLTE, "<=")
		return nil
	case r == '>' && r2 == '=':
		l.advance()
		l.advance()
		l.emit(KindGTE, ">=")
		return nil
	case r == '&' && r2 == '&':
		l.advance()
		l.advance()
		l.emit(KindAnd, "&&")
		return nil
	case r == '|' && r2 == '|':
		l.advance()
		l.advance()
		l.emit(KindOr, "||")
		return nil
	case r == '+' && r2 == '+':
		l.advance()
		l.advance()
		l.emit(KindAppend, "++")
		return nil
	case r == ':' && r2 == ':':
		l.advance()
		l.advance()
		l.emit(KindDoubleCol, "::")
		return nil
	}

	// Single-character punct/ops
	l.advance()
	switch r {
	case '(':
		l.emit(KindLParen, "(")
	case ')':
		l.emit(KindRParen, ")")
	case '[':
		l.emit(KindLBracket, "[")
	case ']':
		l.emit(KindRBracket, "]")
	case '{':
		l.emit(KindLBrace, "{")
	case '}':
		l.emit(KindRBrace, "}")
	case ',':
		l.emit(KindComma, ",")
	case ';':
		l.emit(KindSemicolon, ";")
	case '=':
		l.emit(KindEquals, "=")
	case '|':
		l.emit(KindPipe, "|")
	case ':':
		l.emit(KindColon, ":")
	case '<':
		l.emit(KindLT, "<")
	case '>':
		l.emit(KindGT, ">")
	case '+':
		l.emit(KindPlus, "+")
	case '-':
		l.emit(KindMinus, "-")
	case '*':
		l.emit(KindStar, "*")
	case '/':
		l.emit(KindSlash, "/")
	case '.':
		l.emit(KindDot, ".")
	default:
		return l.errorf("unexpected character: %q", r)
	}
	return nil
}

// --- helpers ---

func keywordKind(name string) (Kind, bool) {
	switch name {
	case "module":
		return KindModule, true
	case "exposing":
		return KindExposing, true
	case "import":
		return KindImport, true
	case "as":
		return KindAs, true
	case "type":
		return KindType, true
	case "alias":
		return KindAlias, true
	case "if":
		return KindIf, true
	case "then":
		return KindThen, true
	case "else":
		return KindElse, true
	case "case":
		return KindCase, true
	case "of":
		return KindOf, true
	case "let":
		return KindLet, true
	case "in":
		return KindIn, true
	case "where":
		return KindWhere, true
	case "port":
		return KindPort, true
	}
	return "", false
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

func isLower(r rune) bool {
	return r >= 'a' && r <= 'z'
}

func isUpper(r rune) bool {
	return r >= 'A' && r <= 'Z'
}

func isAlpha(r rune) bool {
	return isLower(r) || isUpper(r) || unicode.IsLetter(r)
}

func isIdentTail(r rune) bool {
	return isAlpha(r) || isDigit(r) || r == '_' || r == '\''
}
