package lsp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"mar/internal/expr"
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
		"app":                      "Declares the app name. Example: `app BookStoreApi`.",
		"port":                     "Sets the HTTP server port. Example: `port 4100`.",
		"database":                 "Sets the SQLite file path. Example: `database \"app.db\"`. Relative paths use the current working directory.",
		"system":                   "Declares system runtime options.",
		"request_logs_buffer":      "Sets in-memory request log capacity. Default `200`, range `10..5000`.",
		"http_max_request_body_mb": "Sets max HTTP request body size in megabytes. Default `1`, range `1..1024`.",
		"auth_request_code_rate_limit_per_minute": "Sets per-minute rate limit for `POST /auth/request-code`. Default `5`, range `1..10000`.",
		"auth_login_rate_limit_per_minute":        "Sets per-minute rate limit for `POST /auth/login`. Default `10`, range `1..10000`.",
		"security_frame_policy":                   "Sets `X-Frame-Options`. Options: `sameorigin` (default) or `deny`.",
		"security_referrer_policy":                "Sets `Referrer-Policy`. Options: `strict-origin-when-cross-origin` (default) or `no-referrer`.",
		"security_content_type_nosniff":           "Controls `X-Content-Type-Options: nosniff`. Default `true`.",
		"sqlite_journal_mode":                     "Sets SQLite journal mode. Example: `sqlite_journal_mode wal`.",
		"sqlite_synchronous":                      "Sets SQLite synchronous mode. Example: `sqlite_synchronous normal`.",
		"sqlite_foreign_keys":                     "Enables or disables SQLite foreign key checks. Example: `sqlite_foreign_keys true`.",
		"sqlite_busy_timeout_ms":                  "Sets SQLite busy timeout in milliseconds. Default `5000`, range `0..600000`.",
		"sqlite_wal_autocheckpoint":               "Sets SQLite WAL auto-checkpoint size in pages. Default `1000`, range `0..1000000`.",
		"sqlite_journal_size_limit_mb":            "Sets SQLite journal size limit in megabytes. Default `64`, range `-1..4096` (`-1` unlimited).",
		"sqlite_mmap_size_mb":                     "Sets SQLite mmap size in megabytes. Default `128`, range `0..16384`.",
		"sqlite_cache_size_kb":                    "Sets SQLite cache size in KiB. Default `2000`, range `0..1048576`.",
		"public":                                  "Declares embedded static frontend files and optional SPA fallback.",
		"dir":                                     "Sets the source directory for embedded static files. Example: `dir \"./frontend/dist\"`.",
		"mount":                                   "Sets where static files are served. Example: `mount \"/\"` or `mount \"/app\"`.",
		"spa_fallback":                            "Sets a fallback file for SPA routes when no static file matches.",
		"entity":                                  "Declares an entity. Mar generates CRUD endpoints for it.",
		"auth":                                    "Configures built-in email-code authentication for the app. Mar always includes a built-in `User` entity.",
		"code_ttl_minutes":                        "Sets login code lifetime in minutes. Default `10`, range `1..1440`.",
		"session_ttl_hours":                       "Sets session lifetime in hours. Default `24`, range `1..8760`.",
		"admin_ui_session_ttl_hours":              "Sets the generated admin UI session lifetime in hours. Falls back to `auth.session_ttl_hours` when omitted.",
		"email_transport":                         "Sets email delivery transport. Options: `console` or `smtp`.",
		"email_from":                              "Sets the sender email used by auth code messages.",
		"email_subject":                           "Sets the subject used by auth code messages.",
		"smtp_host":                               "Sets the SMTP server host when `email_transport smtp` is used.",
		"smtp_port":                               "Sets the SMTP server port when `email_transport smtp` is used.",
		"smtp_username":                           "Sets the SMTP username when `email_transport smtp` is used.",
		"smtp_password_env":                       "Sets the environment variable name that holds the SMTP password.",
		"smtp_starttls":                           "Controls whether Mar upgrades the SMTP connection with STARTTLS.",
		"rule":                                    "Adds validation logic for entity records.",
		"expect":                                  "Used in `rule` clauses to declare the condition that must be true.",
		"authorize":                               "Adds authorization rules for CRUD actions. Use `authorize all when ...` to set a default rule for read, create, update, and delete, and override specific operations when needed.",
		"read":                                    "Used in authorize clauses to control single-record reads and which rows appear in list responses.",
		"belongs_to":                              "Declares a singular relationship to another entity. Example: `belongs_to User`, `belongs_to customer: User optional`, `belongs_to current_user`, or `belongs_to reviewer: current_user`.",
		"current_user":                            "Used in `belongs_to current_user` or `belongs_to reviewer: current_user` to create a required relationship to the authenticated built-in `User` automatically.",
		"type":                                    "Used with `type alias` to define an action input record.",
		"alias":                                   "Used with `type alias` to define an action input record.",
		"action":                                  "Declares an action endpoint with `input` and one or more `load`, `create`, `update`, or `delete` steps. Steps may bind aliases like `todo = load Todo { ... }`.",
		"load":                                    "Loads one row inside an action block. `load` must bind to an alias and must select by primary key.",
		"create":                                  "Adds one create step inside an action block. Steps may bind aliases like `order = create Order { ... }`.",
		"update":                                  "Adds one update step inside an action block. Include the entity primary key plus the fields to change. Steps may bind aliases like `updatedOrder = update Order { ... }`.",
		"delete":                                  "Adds one delete step inside an action block. Include the entity primary key to select the row to remove. Steps may bind aliases like `deletedOrder = delete Order { ... }`.",
		"optional":                                "Marks a field as nullable.",
		"default":                                 "Sets a literal default value for a field. New required fields with defaults can be added automatically during migration.",
		"primary":                                 "Marks a field as primary key.",
		"auto":                                    "Marks a field as auto-generated.",
		"input":                                   "References the action input record (for example `input.userId`). Action step aliases may also be referenced like `todo.id`.",
		"length":                                  "Returns the string length. Example: `length title >= 3`.",
		"contains":                                "Returns true when text contains a substring. Example: `contains \"@\" email`.",
		"starts_with":                             "Returns true when text starts with a prefix. Example: `starts_with \"SKU-\" code`.",
		"ends_with":                               "Returns true when text ends with a suffix. Example: `ends_with \"@company.com\" email`.",
		"matches":                                 "Returns true when text matches a regex pattern. Example: `matches \"^[^@]+$\" email`.",
		"Int":                                     "Mar primitive type for whole numbers.",
		"String":                                  "Mar primitive type for text values.",
		"Bool":                                    "Mar primitive type for booleans (`true`/`false`).",
		"Float":                                   "Mar primitive type for decimal numbers.",
		"Posix":                                   "Mar primitive type for timestamps in Unix milliseconds, aligned with Elm `Time.Posix`.",
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
	if !ok && expr.IsBuiltinValueName(word) {
		switch word {
		case "user_authenticated":
			doc = "Returns true when the current request is authenticated."
		case "user_email":
			doc = "Returns the authenticated user's email for the current request."
		case "user_id":
			doc = "Returns the authenticated user's id for the current request."
		case "user_role":
			doc = "Returns the authenticated user's role for the current request."
		}
		ok = doc != ""
	}
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

		if match := actionDeclRe.FindStringSubmatchIndex(line); match != nil {
			action, next := parseActionDocumentSymbol(lines, lineNo, match)
			out = append(out, action)
			lineNo = next
		}
	}

	return out
}

func parseActionDocumentSymbol(lines []string, startLine int, match []int) (lspDocumentSymbol, int) {
	name := lines[startLine][match[2]:match[3]]
	endLine := startLine
	inputAlias := ""
	depth := 1

	for i := startLine + 1; i < len(lines); i++ {
		line := lines[i]
		if m := actionInputRe.FindStringSubmatchIndex(line); m != nil && inputAlias == "" {
			inputAlias = line[m[2]:m[3]]
		}
		depth += strings.Count(line, "{")
		depth -= strings.Count(line, "}")
		endLine = i
		if depth <= 0 {
			break
		}
	}

	detail := "action"
	if inputAlias != "" {
		detail = "action input: " + inputAlias
	}
	return lspDocumentSymbol{
		Name:           name,
		Detail:         detail,
		Kind:           12, // Function
		Range:          lspRange{Start: lspPosition{Line: startLine, Character: 0}, End: lspPosition{Line: endLine, Character: len(lines[endLine])}},
		SelectionRange: makeRange(startLine, match[2], match[3]),
	}, endLine
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
		} else if fieldName, start, end, ok := belongsToFieldDeclaration(line); ok {
			children = append(children, lspDocumentSymbol{
				Name:           fieldName,
				Detail:         "field",
				Kind:           8, // Field
				Range:          makeRange(i, 0, len(line)),
				SelectionRange: makeRange(i, start, end),
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
		detail = "Executes typed `create`, `update`, and `delete` steps atomically and is exposed as `/actions/<name>`."
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
