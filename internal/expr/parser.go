package expr

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type ParserOptions struct {
	AllowedVariables map[string]struct{}
	AllowRoleFunc    bool
}

type tokenKind string

const (
	tokEOF    tokenKind = "eof"
	tokWord   tokenKind = "word"
	tokNumber tokenKind = "number"
	tokString tokenKind = "string"
	tokOp     tokenKind = "op"
)

type token struct {
	kind tokenKind
	text string
}

type parser struct {
	tokens []token
	idx    int
	opts   ParserOptions
}

// Parse parses an authorization or rule expression into an executable AST.
func Parse(input string, opts ParserOptions) (Expr, error) {
	tokens, err := tokenize(input)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens, opts: opts}
	expr, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tokEOF {
		return nil, fmt.Errorf("unexpected token %q", p.peek().text)
	}
	return expr, nil
}

func tokenize(input string) ([]token, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return nil, fmt.Errorf("expression is empty")
	}

	out := make([]token, 0, 32)
	for i := 0; i < len(s); {
		ch := rune(s[i])
		if unicode.IsSpace(ch) {
			i++
			continue
		}

		if i+1 < len(s) {
			two := s[i : i+2]
			if two == "<=" || two == ">=" || two == "==" || two == "!=" {
				out = append(out, token{kind: tokOp, text: two})
				i += 2
				continue
			}
		}

		if strings.ContainsRune("(),+-*/<>", ch) {
			out = append(out, token{kind: tokOp, text: string(ch)})
			i++
			continue
		}

		if ch == '"' {
			j := i + 1
			escaped := false
			for ; j < len(s); j++ {
				if escaped {
					escaped = false
					continue
				}
				if s[j] == '\\' {
					escaped = true
					continue
				}
				if s[j] == '"' {
					break
				}
			}
			if j >= len(s) || s[j] != '"' {
				return nil, fmt.Errorf("unterminated string literal")
			}
			raw := s[i : j+1]
			decoded, err := strconv.Unquote(raw)
			if err != nil {
				return nil, fmt.Errorf("invalid string literal %s", raw)
			}
			out = append(out, token{kind: tokString, text: decoded})
			i = j + 1
			continue
		}

		if isDigit(ch) {
			j := i + 1
			hasDot := false
			for ; j < len(s); j++ {
				r := rune(s[j])
				if r == '.' && !hasDot {
					hasDot = true
					continue
				}
				if !isDigit(r) {
					break
				}
			}
			out = append(out, token{kind: tokNumber, text: s[i:j]})
			i = j
			continue
		}

		if isIdentStart(ch) {
			j := i + 1
			for ; j < len(s); j++ {
				r := rune(s[j])
				if !isIdentPart(r) {
					break
				}
			}
			out = append(out, token{kind: tokWord, text: s[i:j]})
			i = j
			continue
		}

		return nil, fmt.Errorf("invalid character %q", ch)
	}

	out = append(out, token{kind: tokEOF})
	return out, nil
}

func (p *parser) parseExpression() (Expr, error) {
	return p.parseOr()
}

func (p *parser) parseOr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peekWord("or") {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = Binary{Op: "or", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (Expr, error) {
	left, err := p.parseEquality()
	if err != nil {
		return nil, err
	}
	for p.peekWord("and") {
		p.next()
		right, err := p.parseEquality()
		if err != nil {
			return nil, err
		}
		left = Binary{Op: "and", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseEquality() (Expr, error) {
	left, err := p.parseComparison()
	if err != nil {
		return nil, err
	}
	for p.peekOp("==") || p.peekOp("!=") {
		op := p.next().text
		right, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		left = Binary{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseComparison() (Expr, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for p.peekOp(">") || p.peekOp(">=") || p.peekOp("<") || p.peekOp("<=") {
		op := p.next().text
		right, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		left = Binary{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseTerm() (Expr, error) {
	left, err := p.parseFactor()
	if err != nil {
		return nil, err
	}
	for p.peekOp("+") || p.peekOp("-") {
		op := p.next().text
		right, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		left = Binary{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseFactor() (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.peekOp("*") || p.peekOp("/") {
		op := p.next().text
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = Binary{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseUnary() (Expr, error) {
	if p.peekWord("not") {
		p.next()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return Unary{Op: "not", Right: right}, nil
	}
	if p.peekOp("-") {
		p.next()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return Unary{Op: "-", Right: right}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Expr, error) {
	tok := p.peek()
	switch tok.kind {
	case tokNumber:
		p.next()
		if strings.Contains(tok.text, ".") {
			f, err := strconv.ParseFloat(tok.text, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid number %q", tok.text)
			}
			return Literal{Value: f}, nil
		}
		i, err := strconv.ParseInt(tok.text, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q", tok.text)
		}
		return Literal{Value: float64(i)}, nil
	case tokString:
		p.next()
		return Literal{Value: tok.text}, nil
	case tokWord:
		p.next()
		if tok.text == "true" {
			return Literal{Value: true}, nil
		}
		if tok.text == "false" {
			return Literal{Value: false}, nil
		}
		if tok.text == "null" {
			return Literal{Value: nil}, nil
		}
		if p.peekOp("(") {
			return p.parseCall(tok.text)
		}
		if p.opts.AllowedVariables != nil {
			if _, ok := p.opts.AllowedVariables[tok.text]; !ok {
				return nil, fmt.Errorf("unknown identifier %q", tok.text)
			}
		}
		return Variable{Name: tok.text}, nil
	case tokOp:
		if tok.text == "(" {
			p.next()
			inner, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			if !p.peekOp(")") {
				return nil, fmt.Errorf("expected )")
			}
			p.next()
			return inner, nil
		}
	}
	return nil, fmt.Errorf("unexpected token %q", tok.text)
}

func (p *parser) parseCall(name string) (Expr, error) {
	if !p.peekOp("(") {
		return nil, fmt.Errorf("expected (")
	}
	p.next()
	args := make([]Expr, 0, 2)
	if !p.peekOp(")") {
		for {
			arg, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			args = append(args, arg)
			if p.peekOp(")") {
				break
			}
			if !p.peekOp(",") {
				return nil, fmt.Errorf("expected ,")
			}
			p.next()
		}
	}
	p.next()

	switch name {
	case "contains", "startsWith", "endsWith", "len", "matches":
		return Call{Name: name, Args: args}, nil
	case "isRole":
		if !p.opts.AllowRoleFunc {
			return nil, fmt.Errorf("function isRole is only allowed in authorize expressions")
		}
		return Call{Name: name, Args: args}, nil
	default:
		return nil, fmt.Errorf("unknown function %q", name)
	}
}

func (p *parser) peek() token {
	if p.idx >= len(p.tokens) {
		return token{kind: tokEOF}
	}
	return p.tokens[p.idx]
}

func (p *parser) next() token {
	t := p.peek()
	if p.idx < len(p.tokens) {
		p.idx++
	}
	return t
}

func (p *parser) peekOp(op string) bool {
	t := p.peek()
	return t.kind == tokOp && t.text == op
}

func (p *parser) peekWord(word string) bool {
	t := p.peek()
	return t.kind == tokWord && t.text == word
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentPart(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
