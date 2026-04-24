package sexp

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type Kind string

const (
	KindList   Kind = "list"
	KindSymbol Kind = "symbol"
	KindString Kind = "string"
	KindNumber Kind = "number"
)

type Node struct {
	Kind     Kind
	Value    string
	Children []Node
	Line     int
	Column   int
}

type reader struct {
	src        []rune
	pos        int
	line       int
	column     int
	lastLine   int
	lastColumn int
}

func Parse(input string) ([]Node, error) {
	r := &reader{
		src:    []rune(normalizeNewlines(input)),
		line:   1,
		column: 1,
	}

	nodes := []Node{}
	for {
		r.skipTrivia()
		if r.eof() {
			return nodes, nil
		}
		node, err := r.readNode()
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
}

func ParseOne(input string) (Node, error) {
	nodes, err := Parse(input)
	if err != nil {
		return Node{}, err
	}
	if len(nodes) != 1 {
		return Node{}, fmt.Errorf("expected exactly one expression, got %d", len(nodes))
	}
	return nodes[0], nil
}

func Format(nodes []Node) string {
	parts := make([]string, 0, len(nodes))
	for _, node := range nodes {
		parts = append(parts, formatNode(node, 0))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n") + "\n"
}

func InlineString(node Node) string {
	return formatInline(node)
}

func normalizeNewlines(input string) string {
	return strings.ReplaceAll(strings.ReplaceAll(input, "\r\n", "\n"), "\r", "\n")
}

func (r *reader) readNode() (Node, error) {
	r.skipTrivia()
	if r.eof() {
		return Node{}, fmt.Errorf("unexpected end of input")
	}

	ch := r.peek()
	line, column := r.line, r.column
	switch ch {
	case '(':
		r.advance()
		children := []Node{}
		for {
			r.skipTrivia()
			if r.eof() {
				return Node{}, fmt.Errorf("line %d:%d: unterminated list", line, column)
			}
			if r.peek() == ')' {
				r.advance()
				return Node{Kind: KindList, Children: children, Line: line, Column: column}, nil
			}
			child, err := r.readNode()
			if err != nil {
				return Node{}, err
			}
			children = append(children, child)
		}
	case ')':
		return Node{}, fmt.Errorf("line %d:%d: unexpected )", line, column)
	case '"':
		return r.readString()
	default:
		return r.readAtom()
	}
}

func (r *reader) readString() (Node, error) {
	line, column := r.line, r.column
	r.advance() // opening quote
	var raw strings.Builder
	escaped := false
	for !r.eof() {
		ch := r.peek()
		r.advance()
		if escaped {
			raw.WriteRune('\\')
			raw.WriteRune(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			decoded, err := strconv.Unquote(`"` + raw.String() + `"`)
			if err != nil {
				return Node{}, fmt.Errorf("line %d:%d: invalid string literal", line, column)
			}
			return Node{Kind: KindString, Value: decoded, Line: line, Column: column}, nil
		}
		raw.WriteRune(ch)
	}
	return Node{}, fmt.Errorf("line %d:%d: unterminated string literal", line, column)
}

func (r *reader) readAtom() (Node, error) {
	line, column := r.line, r.column
	var b strings.Builder
	for !r.eof() {
		ch := r.peek()
		if unicode.IsSpace(ch) || ch == '(' || ch == ')' || ch == ';' {
			break
		}
		b.WriteRune(ch)
		r.advance()
	}
	value := b.String()
	if value == "" {
		return Node{}, fmt.Errorf("line %d:%d: expected symbol", line, column)
	}
	kind := KindSymbol
	if isNumberLiteral(value) {
		kind = KindNumber
	}
	return Node{Kind: kind, Value: value, Line: line, Column: column}, nil
}

func (r *reader) skipTrivia() {
	for !r.eof() {
		ch := r.peek()
		if unicode.IsSpace(ch) {
			r.advance()
			continue
		}
		if ch == ';' {
			for !r.eof() && r.peek() != '\n' {
				r.advance()
			}
			continue
		}
		return
	}
}

func (r *reader) eof() bool {
	return r.pos >= len(r.src)
}

func (r *reader) peek() rune {
	if r.eof() {
		return 0
	}
	return r.src[r.pos]
}

func (r *reader) advance() {
	if r.eof() {
		return
	}
	r.lastLine = r.line
	r.lastColumn = r.column
	ch := r.src[r.pos]
	r.pos++
	if ch == '\n' {
		r.line++
		r.column = 1
		return
	}
	r.column++
}

func isNumberLiteral(value string) bool {
	if value == "" {
		return false
	}
	if value[0] == '+' || value[0] == '-' {
		value = value[1:]
	}
	if value == "" {
		return false
	}
	hasDigits := false
	hasDot := false
	for _, ch := range value {
		switch {
		case ch >= '0' && ch <= '9':
			hasDigits = true
		case ch == '.' && !hasDot:
			hasDot = true
		default:
			return false
		}
	}
	return hasDigits
}

func formatNode(node Node, indent int) string {
	lines := formatNodeLines(node, indent)
	return strings.Join(lines, "\n")
}

func formatNodeLines(node Node, indent int) []string {
	inline := formatInline(node)
	if len(inline) <= 72 && !shouldForceMultiline(node) {
		return []string{strings.Repeat(" ", indent) + inline}
	}
	if node.Kind != KindList || len(node.Children) == 0 {
		return []string{strings.Repeat(" ", indent) + inline}
	}

	if node.Children[0].Kind == KindSymbol {
		return formatFormLines(node, indent)
	}
	return formatWrapperListLines(node, indent)
}

func formatFormLines(node Node, indent int) []string {
	firstLine := strings.Repeat(" ", indent) + "(" + formatInline(node.Children[0])
	start := 1
	for start < len(node.Children) && start <= inlineHeaderArgumentCount(node) {
		next := formatInline(node.Children[start])
		if len(firstLine)+1+len(next) > 72 {
			break
		}
		firstLine += " " + next
		start++
	}

	lines := []string{firstLine}
	for _, child := range node.Children[start:] {
		lines = append(lines, formatNodeLines(child, indent+2)...)
	}
	lines[len(lines)-1] += ")"
	return lines
}

func formatWrapperListLines(node Node, indent int) []string {
	firstChildLines := formatNodeLines(node.Children[0], indent+1)
	firstPrefix := strings.Repeat(" ", indent+1)
	lines := []string{
		strings.Repeat(" ", indent) + "(" + strings.TrimPrefix(firstChildLines[0], firstPrefix),
	}
	lines = append(lines, firstChildLines[1:]...)
	for _, child := range node.Children[1:] {
		lines = append(lines, formatNodeLines(child, indent+1)...)
	}
	lines[len(lines)-1] += ")"
	return lines
}

func shouldForceMultiline(node Node) bool {
	if node.Kind != KindList || len(node.Children) == 0 {
		return false
	}
	head := node.Children[0]
	if head.Kind != KindSymbol {
		return false
	}
	switch head.Value {
	case "entity", "screen":
		return len(node.Children) > 1
	case "define-app", "define-screen", "define", "fields", "belongs-to", "defaults", "validate", "authorize", "section":
		return len(node.Children) > 2
	case "entities", "queries", "actions", "screens":
		return len(node.Children) > 2
	default:
		return false
	}
}

func inlineHeaderArgumentCount(node Node) int {
	if node.Kind != KindList || len(node.Children) < 2 {
		return 0
	}
	head := node.Children[0]
	if head.Kind != KindSymbol {
		return 0
	}
	switch head.Value {
	case "update":
		return 2
	case "define-app", "define-screen", "define", "query", "list", "create", "edit", "link", "match", "view", "if", "go", "command":
		return 1
	default:
		return 0
	}
}

func formatInline(node Node) string {
	switch node.Kind {
	case KindString:
		return strconv.Quote(node.Value)
	case KindList:
		parts := make([]string, 0, len(node.Children))
		for _, child := range node.Children {
			parts = append(parts, formatInline(child))
		}
		return "(" + strings.Join(parts, " ") + ")"
	default:
		return node.Value
	}
}
