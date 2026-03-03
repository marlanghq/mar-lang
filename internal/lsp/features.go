package lsp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type lspDocumentSymbol struct {
	Name           string              `json:"name"`
	Detail         string              `json:"detail,omitempty"`
	Kind           int                 `json:"kind"`
	Range          lspRange            `json:"range"`
	SelectionRange lspRange            `json:"selectionRange"`
	Children       []lspDocumentSymbol `json:"children,omitempty"`
}

type lspCodeAction struct {
	Title       string           `json:"title"`
	Kind        string           `json:"kind,omitempty"`
	IsPreferred bool             `json:"isPreferred,omitempty"`
	Edit        lspWorkspaceEdit `json:"edit,omitempty"`
}

var (
	appDeclRe                = regexp.MustCompile(`^\s*app\s+([A-Za-z][A-Za-z0-9_]*)\s*$`)
	unknownInputTypeErrorRe  = regexp.MustCompile(`action\s+[a-z][A-Za-z0-9_]*\s+references unknown input type\s+"([A-Za-z][A-Za-z0-9_]*)"$`)
	keywordHoverDescriptions = map[string]string{
		"app":         "Declares the app name. Example: `app BookStoreApi`.",
		"port":        "Sets the HTTP server port. Example: `port 4100`.",
		"database":    "Sets the SQLite file path. Example: `database \"./app.db\"`.",
		"entity":      "Declares an entity. Belm generates CRUD endpoints for it.",
		"auth":        "Configures email-code authentication for the app.",
		"rule":        "Adds validation logic for entity records.",
		"authorize":   "Adds authorization rules for CRUD actions.",
		"type":        "Used with `type alias` to define an action input record.",
		"alias":       "Used with `type alias` to define an action input record.",
		"transaction": "Starts a transaction block inside an action definition.",
		"insert":      "Inserts a record in a transaction step.",
		"optional":    "Marks a field as nullable.",
		"primary":     "Marks a field as primary key.",
		"auto":        "Marks a field as auto-generated.",
		"input":       "References the action input record (e.g. `input.userId`).",
		"isRole":      "Authorization helper: `isRole(\"admin\")`.",
		"len":         "Returns the string length.",
		"contains":    "Returns true when text contains a substring.",
		"startsWith":  "Returns true when text starts with a prefix.",
		"endsWith":    "Returns true when text ends with a suffix.",
		"matches":     "Returns true when text matches a regex pattern.",
	}
)

func (s *server) handleHover(id json.RawMessage, params textDocumentPositionParams) {
	text, ok := s.documentText(params.TextDocument.URI)
	if !ok {
		s.respond(id, nil)
		return
	}

	index := s.buildWorkspaceSymbolIndex()
	if symbol, found := index.symbolAt(params.TextDocument.URI, params.Position); found {
		content := hoverForSymbol(index, symbol)
		s.respond(id, map[string]any{
			"contents": map[string]any{
				"kind":  "markdown",
				"value": content,
			},
			"range": symbol.Range,
		})
		return
	}

	word, tokenRange, found := wordAtPosition(text, params.Position)
	if !found {
		s.respond(id, nil)
		return
	}
	doc, ok := keywordHoverDescriptions[word]
	if !ok {
		s.respond(id, nil)
		return
	}
	s.respond(id, map[string]any{
		"contents": map[string]any{
			"kind":  "markdown",
			"value": fmt.Sprintf("**%s**\n\n%s", word, doc),
		},
		"range": tokenRange,
	})
}

func (s *server) handleDocumentSymbols(id json.RawMessage, params documentURIParams) {
	text, ok := s.documentText(params.TextDocument.URI)
	if !ok {
		s.respond(id, []lspDocumentSymbol{})
		return
	}
	s.respond(id, buildDocumentSymbols(text))
}

func (s *server) handleCodeAction(id json.RawMessage, params codeActionParams) {
	text, ok := s.documentText(params.TextDocument.URI)
	if !ok {
		s.respond(id, []lspCodeAction{})
		return
	}
	s.respond(id, buildQuickFixCodeActions(params.TextDocument.URI, text, params.Context.Diagnostics))
}

func (s *server) documentText(uri string) (string, bool) {
	if text, ok := s.documents[uri]; ok {
		return text, true
	}
	loaded, err := loadURIFile(uri)
	if err != nil {
		return "", false
	}
	return loaded, true
}

func buildDocumentSymbols(text string) []lspDocumentSymbol {
	lines := splitNormalizedLines(text)
	out := make([]lspDocumentSymbol, 0, 16)

	for lineNo := 0; lineNo < len(lines); lineNo++ {
		line := lines[lineNo]
		trimmed := strings.TrimSpace(line)
		if isCommentOrBlankLSP(trimmed) {
			continue
		}

		if match := appDeclRe.FindStringSubmatchIndex(line); match != nil {
			name := line[match[2]:match[3]]
			out = append(out, lspDocumentSymbol{
				Name:           name,
				Detail:         "app",
				Kind:           2, // Module
				Range:          makeRange(lineNo, 0, len(line)),
				SelectionRange: makeRange(lineNo, match[2], match[3]),
			})
			continue
		}

		if match := entityDeclRe.FindStringSubmatchIndex(line); match != nil {
			entity, next := parseEntityDocumentSymbol(lines, lineNo, match)
			out = append(out, entity)
			lineNo = next
			continue
		}

		if match := typeAliasDeclRe.FindStringSubmatchIndex(line); match != nil {
			alias, next := parseAliasDocumentSymbol(lines, lineNo, match)
			out = append(out, alias)
			lineNo = next
			continue
		}

		if match := actionSigRe.FindStringSubmatchIndex(line); match != nil {
			name := line[match[2]:match[3]]
			inputAlias := line[match[4]:match[5]]
			out = append(out, lspDocumentSymbol{
				Name:           name,
				Detail:         "action: " + inputAlias + " -> Effect",
				Kind:           12, // Function
				Range:          makeRange(lineNo, 0, len(line)),
				SelectionRange: makeRange(lineNo, match[2], match[3]),
			})
		}
	}

	return out
}

func parseEntityDocumentSymbol(lines []string, startLine int, match []int) (lspDocumentSymbol, int) {
	name := lines[startLine][match[2]:match[3]]
	children := make([]lspDocumentSymbol, 0, 8)
	endLine := startLine

	for i := startLine + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "}" {
			endLine = i
			break
		}
		if field := fieldDeclRe.FindStringSubmatchIndex(line); field != nil {
			fieldName := line[field[2]:field[3]]
			children = append(children, lspDocumentSymbol{
				Name:           fieldName,
				Detail:         "field",
				Kind:           8, // Field
				Range:          makeRange(i, 0, len(line)),
				SelectionRange: makeRange(i, field[2], field[3]),
			})
		}
		endLine = i
	}

	return lspDocumentSymbol{
		Name:           name,
		Detail:         "entity",
		Kind:           23, // Struct
		Range:          lspRange{Start: lspPosition{Line: startLine, Character: 0}, End: lspPosition{Line: endLine, Character: len(lines[endLine])}},
		SelectionRange: makeRange(startLine, match[2], match[3]),
		Children:       children,
	}, endLine
}

func parseAliasDocumentSymbol(lines []string, startLine int, match []int) (lspDocumentSymbol, int) {
	name := lines[startLine][match[2]:match[3]]
	children := make([]lspDocumentSymbol, 0, 8)
	endLine := startLine

	for i := startLine; i < len(lines); i++ {
		line := lines[i]
		fields := aliasFieldDeclRe.FindAllStringSubmatchIndex(line, -1)
		for _, field := range fields {
			fieldName := line[field[2]:field[3]]
			children = append(children, lspDocumentSymbol{
				Name:           fieldName,
				Detail:         "field",
				Kind:           8, // Field
				Range:          makeRange(i, 0, len(line)),
				SelectionRange: makeRange(i, field[2], field[3]),
			})
		}
		if strings.Contains(line, "}") {
			endLine = i
			break
		}
		endLine = i
	}

	return lspDocumentSymbol{
		Name:           name,
		Detail:         "type alias",
		Kind:           11, // Interface
		Range:          lspRange{Start: lspPosition{Line: startLine, Character: 0}, End: lspPosition{Line: endLine, Character: len(lines[endLine])}},
		SelectionRange: makeRange(startLine, match[2], match[3]),
		Children:       children,
	}, endLine
}

func buildQuickFixCodeActions(uri string, text string, diagnostics []lspCodeActionDiagnostic) []lspCodeAction {
	out := make([]lspCodeAction, 0, 4)
	seen := map[string]struct{}{}
	eof := endPosition(text)

	add := func(action lspCodeAction) {
		if strings.TrimSpace(action.Title) == "" {
			return
		}
		if _, ok := seen[action.Title]; ok {
			return
		}
		seen[action.Title] = struct{}{}
		out = append(out, action)
	}

	for _, diag := range diagnostics {
		msg := strings.TrimSpace(diag.Message)
		switch {
		case msg == "missing app declaration":
			add(lspCodeAction{
				Title:       "Add app declaration",
				Kind:        "quickfix",
				IsPreferred: true,
				Edit: lspWorkspaceEdit{
					Changes: map[string][]lspTextEdit{
						uri: {{
							Range:   makeRange(0, 0, 0),
							NewText: "app Main\n\n",
						}},
					},
				},
			})

		case msg == "at least one entity is required":
			add(lspCodeAction{
				Title: "Add an entity skeleton",
				Kind:  "quickfix",
				Edit: lspWorkspaceEdit{
					Changes: map[string][]lspTextEdit{
						uri: {{
							Range:   lspRange{Start: eof, End: eof},
							NewText: ensurePrefixNewline(text, "entity Todo {\n  title: String\n}\n"),
						}},
					},
				},
			})

		case strings.Contains(msg, "is missing closing }"), strings.Contains(msg, "missing closing }"):
			add(lspCodeAction{
				Title: "Insert missing closing brace",
				Kind:  "quickfix",
				Edit: lspWorkspaceEdit{
					Changes: map[string][]lspTextEdit{
						uri: {{
							Range:   lspRange{Start: eof, End: eof},
							NewText: ensurePrefixNewline(text, "}\n"),
						}},
					},
				},
			})

		default:
			if m := unknownInputTypeErrorRe.FindStringSubmatch(msg); m != nil {
				aliasName := m[1]
				add(lspCodeAction{
					Title: "Create type alias " + aliasName,
					Kind:  "quickfix",
					Edit: lspWorkspaceEdit{
						Changes: map[string][]lspTextEdit{
							uri: {{
								Range:   lspRange{Start: eof, End: eof},
								NewText: ensurePrefixNewline(text, "type alias "+aliasName+" = {\n  value : String\n}\n"),
							}},
						},
					},
				})
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Title < out[j].Title
	})
	return out
}

func hoverForSymbol(index *workspaceSymbolIndex, symbol symbolOccurrence) string {
	header := ""
	detail := ""
	switch symbol.Kind {
	case symbolEntity:
		header = "Entity `" + symbol.Name + "`"
		detail = "Generated CRUD endpoints are based on this declaration."
	case symbolEntityField:
		entityName, fieldName := splitFieldKey(symbol.Key, "entity-field:")
		if fieldName == "" {
			fieldName = symbol.Name
		}
		header = "Field `" + fieldName + "`"
		if entityName != "" {
			detail = "Belongs to entity `" + entityName + "`."
		}
	case symbolAlias:
		header = "Type alias `" + symbol.Name + "`"
		detail = "Used as a typed input record for actions."
	case symbolAliasField:
		aliasName, fieldName := splitFieldKey(symbol.Key, "alias-field:")
		if fieldName == "" {
			fieldName = symbol.Name
		}
		header = "Input field `" + fieldName + "`"
		if aliasName != "" {
			detail = "Belongs to type alias `" + aliasName + "`."
		}
	case symbolAction:
		header = "Action `" + symbol.Name + "`"
		detail = "Executes a typed transaction (`transaction`) and is exposed as `/actions/<name>`."
	default:
		header = "Symbol `" + symbol.Name + "`"
	}

	defText := ""
	if def, ok := index.definition(symbol.Key); ok {
		defText = "Defined at `" + locationLabel(def.URI, def.Range.Start.Line) + "`."
	}

	refCount := len(index.references(symbol.Key, false))
	return "**" + header + "**\n\n" + strings.TrimSpace(strings.Join([]string{
		detail,
		defText,
		fmt.Sprintf("References: **%d**", refCount),
	}, "\n\n"))
}

func locationLabel(uri string, line int) string {
	path, err := fileURIToPath(uri)
	if err != nil {
		return uri
	}
	return filepath.Base(path) + ":" + fmt.Sprintf("%d", line+1)
}

func splitFieldKey(key, prefix string) (string, string) {
	rest := strings.TrimPrefix(key, prefix)
	if rest == key {
		return "", ""
	}
	left, right, ok := strings.Cut(rest, ".")
	if !ok {
		return "", ""
	}
	return left, right
}

func ensurePrefixNewline(text, suffix string) string {
	base := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	if base == "" {
		return suffix
	}
	if strings.HasSuffix(base, "\n\n") {
		return suffix
	}
	if strings.HasSuffix(base, "\n") {
		return "\n" + suffix
	}
	return "\n\n" + suffix
}

func wordAtPosition(text string, pos lspPosition) (string, lspRange, bool) {
	lines := splitNormalizedLines(text)
	if pos.Line < 0 || pos.Line >= len(lines) {
		return "", lspRange{}, false
	}
	line := lines[pos.Line]
	if len(line) == 0 {
		return "", lspRange{}, false
	}

	idx := pos.Character
	if idx < 0 {
		return "", lspRange{}, false
	}
	if idx >= len(line) {
		idx = len(line) - 1
	}
	if !isWordByte(line[idx]) {
		if idx > 0 && isWordByte(line[idx-1]) {
			idx--
		} else {
			return "", lspRange{}, false
		}
	}

	start := idx
	for start > 0 && isWordByte(line[start-1]) {
		start--
	}
	end := idx + 1
	for end < len(line) && isWordByte(line[end]) {
		end++
	}

	return line[start:end], makeRange(pos.Line, start, end), true
}

func isWordByte(value byte) bool {
	return (value >= 'a' && value <= 'z') ||
		(value >= 'A' && value <= 'Z') ||
		(value >= '0' && value <= '9') ||
		value == '_'
}
