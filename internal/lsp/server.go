package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"mar/internal/expr"
	"mar/internal/formatter"
	"mar/internal/parser"
)

type lspRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type lspSuccessResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

type lspErrorResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   *lspRespError   `json:"error"`
}

type lspRespError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type lspDiag struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity"`
	Source   string   `json:"source"`
	Message  string   `json:"message"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type didOpenParams struct {
	TextDocument struct {
		URI  string `json:"uri"`
		Text string `json:"text"`
	} `json:"textDocument"`
}

type didChangeParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

type didSaveParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Text *string `json:"text,omitempty"`
}

type didCloseParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

type completionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

type formattingParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

type initializeParams struct {
	RootURI          string `json:"rootUri"`
	RootPath         string `json:"rootPath"`
	WorkspaceFolders []struct {
		URI string `json:"uri"`
	} `json:"workspaceFolders"`
}

type textDocumentPositionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position lspPosition `json:"position"`
}

type referenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

type referencesParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position lspPosition      `json:"position"`
	Context  referenceContext `json:"context"`
}

type renameParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position lspPosition `json:"position"`
	NewName  string      `json:"newName"`
}

type documentURIParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

type lspCodeActionDiagnostic struct {
	Range   lspRange `json:"range"`
	Message string   `json:"message"`
}

type codeActionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Range   lspRange `json:"range"`
	Context struct {
		Diagnostics []lspCodeActionDiagnostic `json:"diagnostics"`
	} `json:"context"`
}

var lineErrRe = regexp.MustCompile(`(?i)^line\s+([0-9]+):\s*(.*)$`)

// RunStdio starts Mar Language Server over stdio using JSON-RPC/LSP framing.
func RunStdio() error {
	srv := &server{
		in:        bufio.NewReader(os.Stdin),
		out:       os.Stdout,
		documents: map[string]string{},
	}
	if cwd, err := os.Getwd(); err == nil && strings.TrimSpace(cwd) != "" {
		srv.workspaceRoots = []string{cwd}
	}
	return srv.loop()
}

type server struct {
	in             *bufio.Reader
	out            io.Writer
	documents      map[string]string
	workspaceRoots []string
	shutdownOK     bool
}

func (s *server) loop() error {
	for {
		payload, err := readLSPMessage(s.in)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		var req lspRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			continue
		}

		switch req.Method {
		case "initialize":
			var params initializeParams
			_ = json.Unmarshal(req.Params, &params)
			s.workspaceRoots = resolveWorkspaceRoots(params)
			caps := map[string]any{
				"capabilities": map[string]any{
					"textDocumentSync": map[string]any{
						"openClose": true,
						"change":    1,
						"save": map[string]any{
							"includeText": true,
						},
					},
					"completionProvider": map[string]any{
						"resolveProvider":   false,
						"triggerCharacters": []string{".", ":"},
					},
					"documentFormattingProvider": true,
					"definitionProvider":         true,
					"referencesProvider":         true,
					"renameProvider":             true,
					"hoverProvider":              true,
					"documentSymbolProvider":     true,
					"codeActionProvider": map[string]any{
						"codeActionKinds": []string{"quickfix"},
					},
				},
				"serverInfo": map[string]any{
					"name":    "mar",
					"version": "0.1.0",
				},
			}
			s.respond(req.ID, caps)

		case "initialized":
			// no-op

		case "shutdown":
			s.shutdownOK = true
			s.respond(req.ID, nil)

		case "exit":
			return nil

		case "textDocument/didOpen":
			var params didOpenParams
			if json.Unmarshal(req.Params, &params) == nil {
				s.documents[params.TextDocument.URI] = params.TextDocument.Text
				s.publishParseDiagnostics(params.TextDocument.URI, params.TextDocument.Text)
			}

		case "textDocument/didChange":
			var params didChangeParams
			if json.Unmarshal(req.Params, &params) == nil {
				if len(params.ContentChanges) > 0 {
					updated := params.ContentChanges[len(params.ContentChanges)-1].Text
					s.documents[params.TextDocument.URI] = updated
					s.publishParseDiagnostics(params.TextDocument.URI, updated)
				}
			}

		case "textDocument/didSave":
			var params didSaveParams
			if json.Unmarshal(req.Params, &params) == nil {
				if params.Text != nil {
					s.documents[params.TextDocument.URI] = *params.Text
					s.publishParseDiagnostics(params.TextDocument.URI, *params.Text)
				} else if text, ok := s.documents[params.TextDocument.URI]; ok {
					s.publishParseDiagnostics(params.TextDocument.URI, text)
				}
			}

		case "textDocument/didClose":
			var params didCloseParams
			if json.Unmarshal(req.Params, &params) == nil {
				delete(s.documents, params.TextDocument.URI)
				s.publishDiagnostics(params.TextDocument.URI, []lspDiag{})
			}

		case "textDocument/completion":
			_ = completionParams{}
			items := keywordCompletionItems()
			s.respond(req.ID, map[string]any{
				"isIncomplete": false,
				"items":        items,
			})

		case "textDocument/formatting":
			var params formattingParams
			if json.Unmarshal(req.Params, &params) == nil {
				uri := params.TextDocument.URI
				text, ok := s.documents[uri]
				if !ok {
					if loaded, err := loadURIFile(uri); err == nil {
						text = loaded
					}
				}
				if text == "" {
					s.respond(req.ID, []map[string]any{})
					continue
				}

				formatted, err := formatter.Format(text)
				if err != nil {
					s.respond(req.ID, []map[string]any{})
					continue
				}
				if formatted == normalizeForCompare(text) {
					s.respond(req.ID, []map[string]any{})
					continue
				}

				end := endPosition(text)
				edit := map[string]any{
					"range": map[string]any{
						"start": map[string]any{"line": 0, "character": 0},
						"end":   map[string]any{"line": end.Line, "character": end.Character},
					},
					"newText": formatted,
				}
				s.respond(req.ID, []map[string]any{edit})
			}

		case "textDocument/definition":
			var params textDocumentPositionParams
			if json.Unmarshal(req.Params, &params) == nil {
				s.handleDefinition(req.ID, params)
			} else {
				s.respond(req.ID, nil)
			}

		case "textDocument/references":
			var params referencesParams
			if json.Unmarshal(req.Params, &params) == nil {
				s.handleReferences(req.ID, params)
			} else {
				s.respond(req.ID, []lspLocation{})
			}

		case "textDocument/rename":
			var params renameParams
			if json.Unmarshal(req.Params, &params) == nil {
				s.handleRename(req.ID, params)
			} else {
				s.respondError(req.ID, -32602, "Invalid rename parameters.")
			}

		case "textDocument/hover":
			var params textDocumentPositionParams
			if json.Unmarshal(req.Params, &params) == nil {
				s.handleHover(req.ID, params)
			} else {
				s.respond(req.ID, nil)
			}

		case "textDocument/documentSymbol":
			var params documentURIParams
			if json.Unmarshal(req.Params, &params) == nil {
				s.handleDocumentSymbols(req.ID, params)
			} else {
				s.respond(req.ID, []map[string]any{})
			}

		case "textDocument/codeAction":
			var params codeActionParams
			if json.Unmarshal(req.Params, &params) == nil {
				s.handleCodeAction(req.ID, params)
			} else {
				s.respond(req.ID, []map[string]any{})
			}

		default:
			// Ignore unknown notifications. Reply with null on unknown requests.
			if len(req.ID) > 0 {
				s.respond(req.ID, nil)
			}
		}
	}
}

func loadURIFile(uri string) (string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "file" {
		return "", fmt.Errorf("unsupported uri")
	}
	path := strings.TrimSpace(parsed.Path)
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	path = filepath.Clean(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func endPosition(text string) lspPosition {
	normalized := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) == 0 {
		return lspPosition{Line: 0, Character: 0}
	}
	lastLine := len(lines) - 1
	return lspPosition{Line: lastLine, Character: len(lines[lastLine])}
}

func normalizeForCompare(source string) string {
	s := strings.ReplaceAll(source, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s
}

func keywordCompletionItems() []map[string]any {
	keywords := []string{
		"app",
		"port",
		"database",
		"system",
		"public",
		"entity",
		"auth",
		"type alias",
		"rule",
		"expect",
		"authorize",
		"all",
		"action",
		"input",
		"create",
		"default",
		"code_ttl_minutes",
		"session_ttl_hours",
		"admin_ui_session_ttl_hours",
		"email_transport",
		"email_from",
		"email_subject",
		"smtp_host",
		"smtp_port",
		"smtp_username",
		"smtp_password_env",
		"smtp_starttls",
		"dir",
		"mount",
		"spa_fallback",
		"request_logs_buffer",
		"http_max_request_body_mb",
		"auth_request_code_rate_limit_per_minute",
		"auth_login_rate_limit_per_minute",
		"security_frame_policy",
		"security_referrer_policy",
		"security_content_type_nosniff",
		"sqlite_journal_mode",
		"sqlite_synchronous",
		"sqlite_foreign_keys",
		"sqlite_busy_timeout_ms",
		"sqlite_wal_autocheckpoint",
		"sqlite_journal_size_limit_mb",
		"sqlite_mmap_size_mb",
		"sqlite_cache_size_kb",
	}
	types := []string{"Int", "String", "Bool", "Float", "Posix"}
	functions := []string{"length", "contains", "starts_with", "ends_with", "matches"}
	out := make([]map[string]any, 0, len(keywords)+len(types)+len(functions)+len(expr.BuiltinValueNames))
	for _, kw := range keywords {
		out = append(out, map[string]any{
			"label":  kw,
			"kind":   14, // CompletionItemKind.Keyword
			"detail": "Mar keyword",
		})
	}
	for _, typ := range types {
		out = append(out, map[string]any{
			"label":  typ,
			"kind":   25, // CompletionItemKind.TypeParameter
			"detail": "Mar type",
		})
	}
	for _, fn := range functions {
		out = append(out, map[string]any{
			"label":  fn,
			"kind":   3, // CompletionItemKind.Function
			"detail": "Mar built-in function",
		})
	}
	for _, value := range expr.BuiltinValueNames {
		out = append(out, map[string]any{
			"label":  value,
			"kind":   6, // CompletionItemKind.Variable
			"detail": "Mar built-in value",
		})
	}
	return out
}

func (s *server) publishParseDiagnostics(uri, text string) {
	_, err := parser.Parse(text)
	if err == nil {
		s.publishDiagnostics(uri, []lspDiag{})
		return
	}
	diag := parseErrorToDiagnostic(err, text)
	s.publishDiagnostics(uri, []lspDiag{diag})
}

func parseErrorToDiagnostic(err error, text string) lspDiag {
	message := strings.TrimSpace(err.Error())
	lineIdx := 0
	charEnd := 1

	if m := lineErrRe.FindStringSubmatch(message); m != nil {
		n, convErr := strconv.Atoi(m[1])
		if convErr == nil && n > 0 {
			lineIdx = n - 1
		}
		if strings.TrimSpace(m[2]) != "" {
			message = strings.TrimSpace(m[2])
		}
	}

	lines := strings.Split(strings.ReplaceAll(text, "\r", ""), "\n")
	if lineIdx >= 0 && lineIdx < len(lines) {
		charEnd = len(lines[lineIdx])
		if charEnd == 0 {
			charEnd = 1
		}
	}

	return lspDiag{
		Range: lspRange{
			Start: lspPosition{Line: lineIdx, Character: 0},
			End:   lspPosition{Line: lineIdx, Character: charEnd},
		},
		Severity: 1, // Error
		Source:   "mar-lsp",
		Message:  message,
	}
}

func (s *server) respond(id json.RawMessage, result any) {
	if len(id) == 0 {
		return
	}
	resp := lspSuccessResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	_ = writeLSPMessage(s.out, resp)
}

func (s *server) respondError(id json.RawMessage, code int, message string) {
	if len(id) == 0 {
		return
	}
	resp := lspErrorResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &lspRespError{
			Code:    code,
			Message: message,
		},
	}
	_ = writeLSPMessage(s.out, resp)
}

func (s *server) publishDiagnostics(uri string, diagnostics []lspDiag) {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/publishDiagnostics",
		"params": map[string]any{
			"uri":         uri,
			"diagnostics": diagnostics,
		},
	}
	_ = writeLSPMessage(s.out, payload)
}

func readLSPMessage(r *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			raw := strings.TrimSpace(line[len("content-length:"):])
			n, err := strconv.Atoi(raw)
			if err != nil {
				return nil, fmt.Errorf("invalid content length %q", raw)
			}
			contentLength = n
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing content length")
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

func writeLSPMessage(w io.Writer, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
