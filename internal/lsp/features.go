package lsp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"mar/internal/expr"
	"mar/internal/sexp"
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

var keywordHoverDescriptions = map[string]string{
	"define-app":                   "Declares the root app and explicitly separates backend and frontend blocks.",
	"config":                       "Attaches a named config value to the app.",
	"database":                     "Sets the SQLite file path. Example: `database \"app.db\"`. Relative paths use the current working directory.",
	"ios":                          "Declares iOS project generation settings.",
	"bundle-identifier":            "Sets the required iOS bundle identifier. Example: `(bundle-identifier \"com.example.app\")`.",
	"display-name":                 "Sets the optional iOS Home Screen app name. Falls back to the Mar app name when omitted.",
	"server-url":                   "Sets the required server URL used by the generated iOS app. Example: `(server-url \"https://school.example.com\")`.",
	"system":                       "Declares system runtime options.",
	"request-logs-buffer":          "Sets in-memory request log capacity. Default `200`, range `10..5000`.",
	"http-max-request-body-mb":     "Sets max HTTP request body size in megabytes. Default `1`, range `1..1024`.",
	"sqlite-journal-mode":          "Sets SQLite journal mode. Example: `(sqlite-journal-mode wal)`.",
	"sqlite-synchronous":           "Sets SQLite synchronous mode. Example: `(sqlite-synchronous normal)`.",
	"sqlite-foreign-keys":          "Enables or disables SQLite foreign key checks. Example: `(sqlite-foreign-keys true)`.",
	"sqlite-busy-timeout-ms":       "Sets SQLite busy timeout in milliseconds. Default `5000`, range `0..600000`.",
	"sqlite-wal-autocheckpoint":    "Sets SQLite WAL auto-checkpoint size in pages. Default `1000`, range `0..1000000`.",
	"sqlite-journal-size-limit-mb": "Sets SQLite journal size limit in megabytes. Default `64`, range `-1..4096` (`-1` unlimited).",
	"sqlite-mmap-size-mb":          "Sets SQLite mmap size in megabytes. Default `128`, range `0..16384`.",
	"sqlite-cache-size-kb":         "Sets SQLite cache size in KiB. Default `2000`, range `0..1048576`.",
	"public":                       "Declares embedded static frontend files and optional SPA fallback.",
	"backend":                      "Groups backend declarations inside define-app, such as entities, queries, and actions.",
	"frontend":                     "Groups frontend declarations inside define-app, such as screens.",
	"dir":                          "Sets the source directory for embedded static files. Example: `dir \"./frontend/dist\"`.",
	"mount":                        "Sets where static files are served. Example: `mount \"/\"` or `mount \"/app\"`.",
	"spa-fallback":                 "Sets a fallback file for SPA routes when no static file matches.",
	"screens":                      "Declares an App UI navigation model made of screens, sections, lists, links, fields, and actions.",
	"queries":                      "Publishes named backend read models inside the define-app backend block.",
	"actions":                      "Publishes named backend mutation endpoints inside the define-app backend block.",
	"define-screen":                "Declares a screen. Parameterized screens use `(define-screen (post-detail post) ...)` and their parameters must be inferred from callers.",
	"screen":                       "Legacy wrapper name; new screen declarations should use top-level `define-screen` directly.",
	"section":                      "Declares a list-style section. Conditional UI should use normal expressions like `(if condition item (empty))`.",
	"list":                         "Displays rows for an entity in a screen.",
	"row":                          "Declares how each list row is rendered.",
	"field":                        "Displays a field from the current row of a screen, typically the first screen argument.",
	"title":                        "Sets a screen title or the title field for list rows.",
	"subtitle":                     "Sets the subtitle field for list rows.",
	"text":                         "Renders static text inside a `view` tree.",
	"button":                       "Renders a button that emits a screen message. Buttons are only valid on screens that define `update`.",
	"text-input":                   "Renders a text input bound to the screen model. Requires a screen with `update`.",
	"textarea":                     "Renders a multiline text input bound to the screen model. Requires a screen with `update`.",
	"toggle":                       "Renders a boolean input bound to the screen model. Requires a screen with `update`.",
	"select":                       "Renders a select input bound to the screen model. Requires a screen with `update`.",
	"define":                       "Defines a named value or function. Example: `(define app-config ...)` or `(define (my-posts) ...)`.",
	"define-record":                "Declares a record type with named fields.",
	"define-type":                  "Declares a tagged union type with named variants.",
	"entity":                       "Declares an entity. Mar generates CRUD endpoints for it.",
	"auth":                         "Configures built-in email-code authentication for the app. Mar always includes a built-in `User` entity.",
	"code-ttl-minutes":             "Sets login code lifetime in minutes. Default `10`, range `1..1440`.",
	"session-ttl-hours":            "Sets session lifetime in hours. Default `24`, range `1..8760` (up to 365 days).",
	"auth-request-code-rate-limit-per-minute": "Sets per-minute rate limit for `POST /auth/request-code`. Default `5`, range `1..10000`.",
	"auth-login-rate-limit-per-minute":        "Sets per-minute rate limit for `POST /auth/login`. Default `10`, range `1..10000`.",
	"admin-ui-session-ttl-hours":              "Sets the generated admin session lifetime in hours. Range `1..8760` (up to 365 days). Falls back to `auth.session-ttl-hours` when omitted.",
	"security-frame-policy":                   "Sets `X-Frame-Options`. Options: `sameorigin` (default) or `deny`.",
	"security-referrer-policy":                "Sets `Referrer-Policy`. Options: `strict-origin-when-cross-origin` (default) or `no-referrer`.",
	"security-content-type-nosniff":           "Controls `X-Content-Type-Options: nosniff`. Default `true`.",
	"from":                                    "Sets the sender email used by auth code messages.",
	"subject":                                 "Sets the subject used by auth code messages.",
	"smtp-host":                               "Sets the SMTP server host used outside `mar dev`.",
	"smtp-port":                               "Sets the SMTP server port used outside `mar dev`.",
	"smtp-username":                           "Sets the SMTP username used outside `mar dev`.",
	"smtp-password-env":                       "Sets the environment variable name that holds the SMTP password.",
	"smtp-starttls":                           "Controls whether Mar upgrades the SMTP connection with STARTTLS.",
	"validate":                                "Adds an entity validation expression. Return `false` for a default error or use `(error \"...\")` for a custom message.",
	"authorize":                               "Adds authorization expressions for CRUD actions. Return `false` for the default auth error or use `(error \"...\")` for a custom message.",
	"error":                                   "Aborts the current expression immediately with a custom error message.",
	"action":                                  "Declares a backend REST mutation. Define it like `(define complete-todo (action ...))`.",
	"input":                                   "Declares the request payload accepted by an action.",
	"command":                                 "Creates a managed backend effect inside a screen transition. Only valid inside the effects list returned by screen `init` or `update`.",
	"go":                                      "Navigation effect that pushes another screen. Only valid inside the effects list returned by screen `init` or `update`.",
	"back":                                    "Navigation effect that pops the current screen. Only valid inside the effects list returned by screen `init` or `update`.",
	"read":                                    "Used in authorize clauses to control single-record reads and which rows appear in list responses.",
	"belongs-to":                              "Declares a relationship to another entity. Example: `(belongs-to ((user)))` or `(belongs-to ((author user)))`.",
	"unique":                                  "Declares one or more unique constraints for an entity.",
	"load":                                    "Loads one row inside an action block by primary key. Example: `(load todo todo-id)`.",
	"create":                                  "Adds a create step inside an action block.",
	"update":                                  "Adds an update step inside an action block. Pass the entity, id expression, and changed fields.",
	"delete":                                  "Adds a delete step inside an action block. Pass the entity and id expression.",
	"optional":                                "Marks a field as nullable.",
	"default":                                 "Sets a literal default value for a field. New required fields with defaults can be added automatically during migration.",
	"primary":                                 "Marks a field as primary key.",
	"auto":                                    "Marks a field as auto-generated.",
	"length":                                  "Returns the string length. Example: `(length title)`.",
	"contains":                                "Returns true when text contains a substring. Example: `(contains email \"@\")`.",
	"starts-with":                             "Returns true when text starts with a prefix.",
	"ends-with":                               "Returns true when text ends with a suffix.",
	"matches":                                 "Returns true when text matches a regex pattern. Example: `matches \"^[^@]+$\" email`.",
	"int":                                     "Mar primitive type for whole numbers.",
	"string":                                  "Mar primitive type for text values.",
	"bool":                                    "Mar primitive type for booleans (`true`/`false`).",
	"decimal":                                 "Mar primitive type for exact decimal numbers.",
	"date":                                    "Mar primitive type for calendar dates.",
	"datetime":                                "Mar primitive type for timestamps.",
}

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
		case "current_user":
			doc = "Returns the current user state: `(authenticated id email role)` or `(anonymous)`."
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
	nodes, err := sexp.Parse(text)
	if err != nil {
		return []lspDocumentSymbol{}
	}
	out := make([]lspDocumentSymbol, 0, 16)

	for _, node := range nodes {
		if isListNamed(node, "define-app") && len(node.Children) >= 2 && node.Children[1].Kind == sexp.KindSymbol {
			out = append(out, documentSymbol(node.Children[1], "app", 2, nil))
			continue
		}

		if isListNamed(node, "define-record") && len(node.Children) >= 2 && node.Children[1].Kind == sexp.KindSymbol {
			out = append(out, recordDocumentSymbol(node))
			continue
		}

		nameNode, valueNode, ok := defineBinding(node)
		if !ok {
			continue
		}
		switch listName(valueNode) {
		case "entity":
			out = append(out, entityDocumentSymbol(nameNode, valueNode))
		case "action":
			out = append(out, documentSymbol(nameNode, "action", 12, nil))
		case "screen":
			out = append(out, documentSymbol(nameNode, "screen", 12, nil))
		}
	}

	return out
}

func entityDocumentSymbol(nameNode sexp.Node, entityNode sexp.Node) lspDocumentSymbol {
	children := make([]lspDocumentSymbol, 0, 8)
	for _, clause := range entityNode.Children[1:] {
		switch listName(clause) {
		case "fields", "belongs-to":
			for _, spec := range secondListChildren(clause) {
				symbols, ok := listSymbols(spec)
				if !ok || len(symbols) == 0 {
					continue
				}
				children = append(children, documentSymbol(symbols[0], "field", 8, nil))
			}
		}
	}
	return documentSymbol(nameNode, "entity", 23, children)
}

func recordDocumentSymbol(node sexp.Node) lspDocumentSymbol {
	nameNode := node.Children[1]
	children := make([]lspDocumentSymbol, 0, 8)
	for _, fieldNode := range node.Children[2:] {
		symbols, ok := listSymbols(fieldNode)
		if !ok || len(symbols) == 0 {
			continue
		}
		children = append(children, documentSymbol(symbols[0], "field", 8, nil))
	}
	return documentSymbol(nameNode, "record", 23, children)
}

func documentSymbol(nameNode sexp.Node, detail string, kind int, children []lspDocumentSymbol) lspDocumentSymbol {
	return lspDocumentSymbol{
		Name:           nameNode.Value,
		Detail:         detail,
		Kind:           kind,
		Range:          nodeRange(nameNode),
		SelectionRange: nodeRange(nameNode),
		Children:       children,
	}
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
		case msg == "missing define-app declaration":
			add(lspCodeAction{
				Title:       "Add define-app declaration",
				Kind:        "quickfix",
				IsPreferred: true,
				Edit: lspWorkspaceEdit{
					Changes: map[string][]lspTextEdit{
						uri: {{
							Range:   makeRange(0, 0, 0),
							NewText: "(define-app app)\n\n",
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
							NewText: ensurePrefixNewline(text, "(define todo\n  (entity\n    (fields\n      ((title string)))))\n"),
						}},
					},
				},
			})
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
	case symbolRecord:
		header = "Record `" + symbol.Name + "`"
		detail = "Closed data shape used by screens and helpers."
	case symbolRecordField:
		recordName, fieldName := splitFieldKey(symbol.Key, "record-field:")
		if fieldName == "" {
			fieldName = symbol.Name
		}
		header = "Field `" + fieldName + "`"
		if recordName != "" {
			detail = "Belongs to record `" + recordName + "`."
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
		value == '_' ||
		value == '-'
}
