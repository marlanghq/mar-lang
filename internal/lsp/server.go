// Package lsp implements a minimal Language Server Protocol server for
// mar. Today it provides:
//
//   - Diagnostics on open / change / save (parse + typecheck errors).
//   - Single-file analysis. Multi-module projects fall back to whatever
//     CheckModule can resolve from the file alone (stdlib-aware,
//     unresolved imports show up as type errors).
//
// Hover / completion / go-to-definition are intentionally out of scope
// for this MVP. The protocol surface is small enough that they can be
// added incrementally.
package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"mar/internal/ast"
	"mar/internal/formatter"
	"mar/internal/lexer"
	"mar/internal/parser"
	"mar/internal/project"
	"mar/internal/typecheck"
)

// RunStdio reads JSON-RPC over stdin and writes responses to stdout
// (the LSP convention for editor-launched servers).
func RunStdio() error {
	s := &Server{
		in:   bufio.NewReaderSize(os.Stdin, 1<<16),
		out:  os.Stdout,
		docs: map[string]string{},
		idx:  map[string]*DocIndex{},
	}
	return s.loop()
}

// Server holds the per-session state of one LSP connection.
type Server struct {
	in   *bufio.Reader
	out  io.Writer
	mu   sync.Mutex
	docs map[string]string    // uri -> latest contents
	idx  map[string]*DocIndex // uri -> last successful analysis (symbols, types)
}

// --- JSON-RPC framing ---

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// readMessage reads one Content-Length-framed JSON-RPC message.
func (s *Server) readMessage() (*message, error) {
	contentLen := 0
	for {
		line, err := s.in.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "Content-Length:") {
			contentLen, _ = strconv.Atoi(strings.TrimSpace(line[len("Content-Length:"):]))
		}
	}
	if contentLen <= 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}
	body := make([]byte, contentLen)
	if _, err := io.ReadFull(s.in, body); err != nil {
		return nil, err
	}
	var msg message
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	return &msg, nil
}

func (s *Server) writeRaw(payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = s.out.Write(body)
	return err
}

func (s *Server) respond(id json.RawMessage, result any) {
	resBytes, _ := json.Marshal(result)
	_ = s.writeRaw(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(id),
		"result":  json.RawMessage(resBytes),
	})
}

func (s *Server) notify(method string, params any) {
	pBytes, _ := json.Marshal(params)
	_ = s.writeRaw(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  json.RawMessage(pBytes),
	})
}

// --- Top-level loop ---

func (s *Server) loop() error {
	for {
		msg, err := s.readMessage()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			// Skip malformed frames; the editor will retry.
			continue
		}
		s.handle(msg)
	}
}

func (s *Server) handle(msg *message) {
	switch msg.Method {
	case "initialize":
		s.handleInitialize(msg)
	case "initialized":
		// Notification — no response.
	case "textDocument/didOpen":
		s.handleDidOpen(msg)
	case "textDocument/didChange":
		s.handleDidChange(msg)
	case "textDocument/didSave":
		// MVP: re-analyze on save. didChange already covers it; this is
		// belt-and-suspenders.
		s.handleDidSave(msg)
	case "textDocument/didClose":
		s.handleDidClose(msg)
	case "textDocument/hover":
		s.handleHover(msg)
	case "textDocument/definition":
		s.handleDefinition(msg)
	case "textDocument/documentSymbol":
		s.handleDocumentSymbol(msg)
	case "textDocument/references":
		s.handleReferences(msg)
	case "textDocument/rename":
		s.handleRename(msg)
	case "workspace/symbol":
		s.handleWorkspaceSymbol(msg)
	case "textDocument/completion":
		s.handleCompletion(msg)
	case "textDocument/inlayHint":
		s.handleInlayHint(msg)
	case "textDocument/codeAction":
		s.handleCodeAction(msg)
	case "textDocument/formatting":
		s.handleFormatting(msg)
	case "shutdown":
		s.respond(msg.ID, nil)
	case "exit":
		os.Exit(0)
	default:
		// Unknown method: if it has an ID it's a request and we should
		// reply with a method-not-found error. Notifications go silent.
		if len(msg.ID) > 0 {
			_ = s.writeRaw(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(msg.ID),
				"error":   rpcError{Code: -32601, Message: "method not found: " + msg.Method},
			})
		}
	}
}

// --- Initialize ---

type initializeParams struct {
	RootURI          string `json:"rootUri"`
	WorkspaceFolders []struct {
		URI  string `json:"uri"`
		Name string `json:"name"`
	} `json:"workspaceFolders"`
}

func (s *Server) handleInitialize(msg *message) {
	var p initializeParams
	_ = json.Unmarshal(msg.Params, &p)
	// Eagerly scan the workspace for .mar files so references / workspace
	// symbols can find them even before the user opens each file.
	go s.scanWorkspace(p)

	// Advertise: text sync (full content on each change), nothing else.
	// MVP — hover / completion / definition come later.
	s.respond(msg.ID, map[string]any{
		"capabilities": map[string]any{
			"textDocumentSync": map[string]any{
				"openClose": true,
				"change":    1, // 1 = Full
				"save":      true,
			},
			"hoverProvider":           true,
			"definitionProvider":      true,
			"documentSymbolProvider":  true,
			"referencesProvider":      true,
			"renameProvider":          true,
			"workspaceSymbolProvider": true,
			"completionProvider": map[string]any{
				"triggerCharacters": []string{"."},
			},
			"inlayHintProvider":         true,
			"codeActionProvider":        true,
			"documentFormattingProvider": true,
		},
		"serverInfo": map[string]any{
			"name":    "mar-lsp",
			"version": "0.1.0",
		},
	})
}

// scanWorkspace walks the workspace root for .mar files and seeds the
// document index. The user's open documents already get indexed via
// didOpen — this just covers files that haven't been opened yet so
// references / rename / workspace symbols can see them.
func (s *Server) scanWorkspace(p initializeParams) {
	roots := []string{}
	if p.RootURI != "" {
		if path, err := uriToPath(p.RootURI); err == nil && path != "" {
			roots = append(roots, path)
		}
	}
	for _, wf := range p.WorkspaceFolders {
		if path, err := uriToPath(wf.URI); err == nil && path != "" {
			roots = append(roots, path)
		}
	}
	for _, root := range roots {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".mar") {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			uri := "file://" + path
			s.mu.Lock()
			// Don't override an already-open (in-editor, possibly
			// edited) document with disk content.
			if _, alreadyIndexed := s.idx[uri]; !alreadyIndexed {
				s.docs[uri] = string(content)
				s.idx[uri] = BuildIndex(uri, string(content))
			}
			s.mu.Unlock()
			return nil
		})
	}
}

// --- Document lifecycle ---

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
	Text string `json:"text"`
}

type didCloseParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

func (s *Server) handleDidOpen(msg *message) {
	var p didOpenParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return
	}
	s.updateDoc(p.TextDocument.URI, p.TextDocument.Text)
	s.publishDiagnostics(p.TextDocument.URI, p.TextDocument.Text)
}

func (s *Server) handleDidChange(msg *message) {
	var p didChangeParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return
	}
	if len(p.ContentChanges) == 0 {
		return
	}
	text := p.ContentChanges[len(p.ContentChanges)-1].Text
	s.updateDoc(p.TextDocument.URI, text)
	s.publishDiagnostics(p.TextDocument.URI, text)
}

// updateDoc replaces the cached source + symbol index for one document.
// Called on every open / change so hover / definition / etc. see the
// latest state without re-running a full type-check on demand.
func (s *Server) updateDoc(uri, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[uri] = text
	s.idx[uri] = BuildIndex(uri, text)
}

func (s *Server) handleDidSave(msg *message) {
	var p didSaveParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return
	}
	if p.Text != "" {
		s.mu.Lock()
		s.docs[p.TextDocument.URI] = p.Text
		s.mu.Unlock()
	}
	s.mu.Lock()
	text := s.docs[p.TextDocument.URI]
	s.mu.Unlock()
	s.publishDiagnostics(p.TextDocument.URI, text)
}

func (s *Server) handleDidClose(msg *message) {
	var p didCloseParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return
	}
	s.mu.Lock()
	delete(s.docs, p.TextDocument.URI)
	delete(s.idx, p.TextDocument.URI)
	s.mu.Unlock()
	// Clear diagnostics for the closed document.
	s.notify("textDocument/publishDiagnostics", map[string]any{
		"uri":         p.TextDocument.URI,
		"diagnostics": []any{},
	})
}

// --- Hover / Definition / DocumentSymbol ---

type textDocPositionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position lspPosition `json:"position"`
}

type textDocOnlyParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

func (s *Server) handleHover(msg *message) {
	var p textDocPositionParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		s.respond(msg.ID, nil)
		return
	}
	s.mu.Lock()
	idx := s.idx[p.TextDocument.URI]
	s.mu.Unlock()
	if idx == nil {
		s.respond(msg.ID, nil)
		return
	}
	name := IdentifierAt(idx.Source, p.Position.Line, p.Position.Character)
	if name == "" {
		s.respond(msg.ID, nil)
		return
	}
	// Try the bare name; for qualified names (Foo.bar), also try the
	// final segment so hovering "Increment" in `View.button Increment`
	// works.
	sym, ok := idx.Symbols[name]
	if !ok {
		if dot := strings.LastIndex(name, "."); dot >= 0 {
			sym, ok = idx.Symbols[name[dot+1:]]
		}
	}
	if !ok {
		s.respond(msg.ID, nil)
		return
	}
	s.respond(msg.ID, map[string]any{
		"contents": map[string]any{
			"kind":  "markdown",
			"value": HoverMarkdown(sym),
		},
	})
}

func (s *Server) handleDefinition(msg *message) {
	var p textDocPositionParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		s.respond(msg.ID, nil)
		return
	}
	s.mu.Lock()
	idx := s.idx[p.TextDocument.URI]
	s.mu.Unlock()
	if idx == nil {
		s.respond(msg.ID, nil)
		return
	}
	name := IdentifierAt(idx.Source, p.Position.Line, p.Position.Character)
	if name == "" {
		s.respond(msg.ID, nil)
		return
	}
	sym, ok := idx.Symbols[name]
	if !ok {
		if dot := strings.LastIndex(name, "."); dot >= 0 {
			sym, ok = idx.Symbols[name[dot+1:]]
		}
	}
	if !ok {
		s.respond(msg.ID, nil)
		return
	}
	// Convert mar's 1-indexed Pos to LSP's 0-indexed range covering the
	// definition's name.
	startLine := sym.DefLine - 1
	startCol := sym.DefCol - 1
	if startLine < 0 {
		startLine = 0
	}
	if startCol < 0 {
		startCol = 0
	}
	endCol := startCol + len(sym.Name)
	s.respond(msg.ID, map[string]any{
		"uri": p.TextDocument.URI,
		"range": lspRange{
			Start: lspPosition{Line: startLine, Character: startCol},
			End:   lspPosition{Line: startLine, Character: endCol},
		},
	})
}

func (s *Server) handleDocumentSymbol(msg *message) {
	var p textDocOnlyParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		s.respond(msg.ID, nil)
		return
	}
	s.mu.Lock()
	idx := s.idx[p.TextDocument.URI]
	s.mu.Unlock()
	if idx == nil {
		s.respond(msg.ID, []any{})
		return
	}
	out := []any{}
	for _, sym := range idx.Symbols {
		startLine := sym.DefLine - 1
		startCol := sym.DefCol - 1
		if startLine < 0 {
			startLine = 0
		}
		if startCol < 0 {
			startCol = 0
		}
		endCol := startCol + len(sym.Name)
		r := lspRange{
			Start: lspPosition{Line: startLine, Character: startCol},
			End:   lspPosition{Line: startLine, Character: endCol},
		}
		out = append(out, map[string]any{
			"name":           sym.Name,
			"kind":           lspSymbolKind(sym.Kind),
			"range":          r,
			"selectionRange": r,
		})
	}
	s.respond(msg.ID, out)
}

// --- References / Rename / Workspace symbols ---

type referencesParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position lspPosition `json:"position"`
	Context  struct {
		IncludeDeclaration bool `json:"includeDeclaration"`
	} `json:"context"`
}

type renameParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position lspPosition `json:"position"`
	NewName  string      `json:"newName"`
}

type workspaceSymbolParams struct {
	Query string `json:"query"`
}

func (s *Server) handleReferences(msg *message) {
	var p referencesParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		s.respond(msg.ID, []any{})
		return
	}
	s.mu.Lock()
	idx := s.idx[p.TextDocument.URI]
	docs := copyDocs(s.docs)
	s.mu.Unlock()
	if idx == nil {
		s.respond(msg.ID, []any{})
		return
	}
	name := IdentifierAt(idx.Source, p.Position.Line, p.Position.Character)
	if name == "" {
		s.respond(msg.ID, []any{})
		return
	}
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		name = name[dot+1:]
	}
	out := []any{}
	for uri, src := range docs {
		for _, occ := range findIdentifierOccurrences(src, name) {
			// Skip the declaration site itself unless requested.
			if !p.Context.IncludeDeclaration && uri == p.TextDocument.URI {
				if sym, ok := idx.Symbols[name]; ok && sym.DefLine-1 == occ.line && sym.DefCol-1 == occ.col {
					continue
				}
			}
			out = append(out, map[string]any{
				"uri": uri,
				"range": lspRange{
					Start: lspPosition{Line: occ.line, Character: occ.col},
					End:   lspPosition{Line: occ.line, Character: occ.col + len(name)},
				},
			})
		}
	}
	s.respond(msg.ID, out)
}

func (s *Server) handleRename(msg *message) {
	var p renameParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		s.respond(msg.ID, nil)
		return
	}
	s.mu.Lock()
	idx := s.idx[p.TextDocument.URI]
	docs := copyDocs(s.docs)
	s.mu.Unlock()
	if idx == nil {
		s.respond(msg.ID, nil)
		return
	}
	name := IdentifierAt(idx.Source, p.Position.Line, p.Position.Character)
	if name == "" || name == p.NewName {
		s.respond(msg.ID, nil)
		return
	}
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		name = name[dot+1:]
	}
	// Build a WorkspaceEdit: per-document list of TextEdits, one per
	// occurrence of the identifier as a whole word.
	changes := map[string][]map[string]any{}
	for uri, src := range docs {
		var edits []map[string]any
		for _, occ := range findIdentifierOccurrences(src, name) {
			edits = append(edits, map[string]any{
				"range": lspRange{
					Start: lspPosition{Line: occ.line, Character: occ.col},
					End:   lspPosition{Line: occ.line, Character: occ.col + len(name)},
				},
				"newText": p.NewName,
			})
		}
		if len(edits) > 0 {
			changes[uri] = edits
		}
	}
	s.respond(msg.ID, map[string]any{"changes": changes})
}

func (s *Server) handleWorkspaceSymbol(msg *message) {
	var p workspaceSymbolParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		s.respond(msg.ID, []any{})
		return
	}
	q := strings.ToLower(p.Query)
	s.mu.Lock()
	indexes := make([]*DocIndex, 0, len(s.idx))
	for _, idx := range s.idx {
		indexes = append(indexes, idx)
	}
	s.mu.Unlock()
	out := []any{}
	for _, idx := range indexes {
		for _, sym := range idx.Symbols {
			if q != "" && !strings.Contains(strings.ToLower(sym.Name), q) {
				continue
			}
			startLine := sym.DefLine - 1
			startCol := sym.DefCol - 1
			if startLine < 0 {
				startLine = 0
			}
			if startCol < 0 {
				startCol = 0
			}
			endCol := startCol + len(sym.Name)
			out = append(out, map[string]any{
				"name": sym.Name,
				"kind": lspSymbolKind(sym.Kind),
				"location": map[string]any{
					"uri": idx.URI,
					"range": lspRange{
						Start: lspPosition{Line: startLine, Character: startCol},
						End:   lspPosition{Line: startLine, Character: endCol},
					},
				},
			})
		}
	}
	s.respond(msg.ID, out)
}

// --- Completion ---

type completionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position lspPosition `json:"position"`
}

func (s *Server) handleCompletion(msg *message) {
	var p completionParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		s.respond(msg.ID, []any{})
		return
	}
	s.mu.Lock()
	idx := s.idx[p.TextDocument.URI]
	indexes := make([]*DocIndex, 0, len(s.idx))
	for _, i := range s.idx {
		indexes = append(indexes, i)
	}
	s.mu.Unlock()
	if idx == nil {
		s.respond(msg.ID, []any{})
		return
	}
	prefix := identifierPrefixAt(idx.Source, p.Position.Line, p.Position.Character)
	// If the prefix is "Module." (or "Module.partial"), filter on the
	// qualified prefix; otherwise match any symbol starting with the
	// prefix string.
	out := []any{}
	seen := map[string]bool{}
	addSymbol := func(sym Symbol, fromOtherFile bool) {
		key := sym.Name
		if seen[key] {
			return
		}
		seen[key] = true
		if prefix != "" && !strings.HasPrefix(strings.ToLower(sym.Name), strings.ToLower(prefix)) {
			return
		}
		detail := sym.Type
		if detail == "" {
			detail = sym.Summary
		}
		ci := map[string]any{
			"label":  sym.Name,
			"kind":   completionKind(sym.Kind),
			"detail": detail,
		}
		if fromOtherFile {
			ci["sortText"] = "z" + sym.Name // de-prioritize externals
		}
		out = append(out, ci)
	}
	for _, sym := range idx.Symbols {
		addSymbol(sym, false)
	}
	for _, i := range indexes {
		if i == idx {
			continue
		}
		for _, sym := range i.Symbols {
			addSymbol(sym, true)
		}
	}
	s.respond(msg.ID, out)
}

// identifierPrefixAt returns the partial identifier the user is typing
// just before the cursor (left-of-col). Used for completion to filter
// candidates without requiring the LSP client to send context.
func identifierPrefixAt(src string, line, col int) string {
	lines := strings.Split(src, "\n")
	if line < 0 || line >= len(lines) {
		return ""
	}
	row := lines[line]
	if col < 0 || col > len(row) {
		return ""
	}
	isIdent := func(b byte) bool {
		return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
			(b >= '0' && b <= '9') || b == '_'
	}
	start := col
	for start > 0 && isIdent(row[start-1]) {
		start--
	}
	return row[start:col]
}

func completionKind(k SymbolKind) int {
	switch k {
	case SymValue:
		return 3 // Function
	case SymTypeAlias:
		return 7 // Class
	case SymCustomType:
		return 13 // Enum
	case SymConstructor:
		return 20 // EnumMember
	}
	return 6 // Variable
}

// --- Inlay hints ---

type inlayHintParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Range lspRange `json:"range"`
}

func (s *Server) handleInlayHint(msg *message) {
	var p inlayHintParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		s.respond(msg.ID, []any{})
		return
	}
	s.mu.Lock()
	idx := s.idx[p.TextDocument.URI]
	s.mu.Unlock()
	if idx == nil || idx.Mod == nil {
		s.respond(msg.ID, []any{})
		return
	}
	// For each top-level ValueDecl that doesn't have a corresponding
	// AnnotationDecl right above it, emit an inline ": Type" hint after
	// the name. Tells the user what type was inferred without forcing
	// them to write the annotation themselves.
	annotated := map[string]bool{}
	for _, d := range idx.Mod.Decls {
		if a, ok := d.(*ast.AnnotationDecl); ok {
			annotated[a.Name] = true
		}
	}
	out := []any{}
	for _, d := range idx.Mod.Decls {
		v, ok := d.(*ast.ValueDecl)
		if !ok || annotated[v.Name] {
			continue
		}
		sym, ok := idx.Symbols[v.Name]
		if !ok || sym.Type == "" {
			continue
		}
		// Position the hint after the name. Mar Pos is 1-indexed; LSP
		// positions are 0-indexed.
		line := v.Pos.Line - 1
		col := v.Pos.Column - 1 + len(v.Name)
		if line < 0 || col < 0 {
			continue
		}
		out = append(out, map[string]any{
			"position":     lspPosition{Line: line, Character: col},
			"label":        " : " + sym.Type,
			"kind":         1, // Type
			"paddingLeft":  false,
			"paddingRight": false,
		})
	}
	s.respond(msg.ID, out)
}

// --- Code actions (quick fixes + refactors) ---

type codeActionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Range   lspRange `json:"range"`
	Context struct {
		Diagnostics []map[string]any `json:"diagnostics"`
	} `json:"context"`
}

func (s *Server) handleCodeAction(msg *message) {
	var p codeActionParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		s.respond(msg.ID, []any{})
		return
	}
	s.mu.Lock()
	idx := s.idx[p.TextDocument.URI]
	s.mu.Unlock()
	if idx == nil {
		s.respond(msg.ID, []any{})
		return
	}

	out := []any{}

	// Quick fixes for the diagnostics the editor passes in. Match on
	// the message text (no error codes today, so this is fragile but
	// works for the cases we generate).
	for _, d := range p.Context.Diagnostics {
		msgText, _ := d["message"].(string)
		if act := didYouMeanFix(msgText, p.TextDocument.URI, p.Range, d, idx); act != nil {
			out = append(out, act)
		}
		if act := badFieldFix(msgText, p.TextDocument.URI, p.Range, d, idx); act != nil {
			out = append(out, act)
		}
	}

	// Refactor: add a type annotation for the top-level value at the
	// cursor when it doesn't have one.
	if act := addAnnotationRefactor(p.TextDocument.URI, p.Range, idx); act != nil {
		out = append(out, act)
	}

	s.respond(msg.ID, out)
}

// didYouMeanFix produces a quickfix for "unknown identifier: X" /
// "unknown qualified name: X" diagnostics — when there's a known
// symbol whose name is close (edit distance ≤ 2), suggest renaming X
// to it.
func didYouMeanFix(msgText, uri string, _ lspRange, diag map[string]any, idx *DocIndex) any {
	bad := extractQuotedName(msgText, "unknown identifier: ", "unknown qualified name: ")
	if bad == "" {
		return nil
	}
	if dot := strings.LastIndex(bad, "."); dot >= 0 {
		bad = bad[dot+1:]
	}
	candidates := []string{}
	for name := range idx.Symbols {
		if levenshtein(strings.ToLower(name), strings.ToLower(bad)) <= 2 && name != bad {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	// Pick the closest candidate (shortest edit distance, then alpha).
	sort.Slice(candidates, func(i, j int) bool {
		di := levenshtein(strings.ToLower(candidates[i]), strings.ToLower(bad))
		dj := levenshtein(strings.ToLower(candidates[j]), strings.ToLower(bad))
		if di != dj {
			return di < dj
		}
		return candidates[i] < candidates[j]
	})
	suggestion := candidates[0]

	// The diagnostic's range pinpoints the offending identifier. Reuse
	// it for the edit so we replace exactly what the user typed.
	rng, _ := diag["range"].(map[string]any)
	if rng == nil {
		return nil
	}
	return map[string]any{
		"title": fmt.Sprintf("Did you mean `%s`?", suggestion),
		"kind":  "quickfix",
		"diagnostics": []any{diag},
		"edit": map[string]any{
			"changes": map[string]any{
				uri: []map[string]any{{
					"range":   rng,
					"newText": suggestion,
				}},
			},
		},
		"isPreferred": true,
	}
}

// badFieldFix turns "record has no field 'X' (available: a, b, c)"
// into a quickfix that replaces X with the closest available field.
func badFieldFix(msgText, uri string, _ lspRange, diag map[string]any, idx *DocIndex) any {
	const prefix = "record has no field '"
	idxQuote := strings.Index(msgText, prefix)
	if idxQuote < 0 {
		return nil
	}
	rest := msgText[idxQuote+len(prefix):]
	end := strings.IndexByte(rest, '\'')
	if end < 0 {
		return nil
	}
	bad := rest[:end]
	availStart := strings.Index(rest, "(available: ")
	if availStart < 0 {
		return nil
	}
	availEnd := strings.LastIndex(rest, ")")
	if availEnd < 0 || availEnd < availStart {
		return nil
	}
	avail := rest[availStart+len("(available: ") : availEnd]
	candidates := []string{}
	for _, name := range strings.Split(avail, ", ") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if levenshtein(strings.ToLower(name), strings.ToLower(bad)) <= 3 {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		di := levenshtein(strings.ToLower(candidates[i]), strings.ToLower(bad))
		dj := levenshtein(strings.ToLower(candidates[j]), strings.ToLower(bad))
		if di != dj {
			return di < dj
		}
		return candidates[i] < candidates[j]
	})
	suggestion := candidates[0]
	rng, _ := diag["range"].(map[string]any)
	if rng == nil {
		return nil
	}
	return map[string]any{
		"title": fmt.Sprintf("Replace with `%s`", suggestion),
		"kind":  "quickfix",
		"diagnostics": []any{diag},
		"edit": map[string]any{
			"changes": map[string]any{
				uri: []map[string]any{{
					"range":   rng,
					"newText": suggestion,
				}},
			},
		},
		"isPreferred": true,
	}
}

// addAnnotationRefactor offers a refactor when the cursor is on a
// top-level value declaration that doesn't already have an annotation.
// Inserts `name : <inferred type>` on the line before the decl.
func addAnnotationRefactor(uri string, rng lspRange, idx *DocIndex) any {
	if idx.Mod == nil {
		return nil
	}
	annotated := map[string]bool{}
	for _, d := range idx.Mod.Decls {
		if a, ok := d.(*ast.AnnotationDecl); ok {
			annotated[a.Name] = true
		}
	}
	cursorLine := rng.Start.Line + 1 // back to 1-indexed for comparison
	for _, d := range idx.Mod.Decls {
		v, ok := d.(*ast.ValueDecl)
		if !ok {
			continue
		}
		if v.Pos.Line != cursorLine {
			continue
		}
		if annotated[v.Name] {
			return nil
		}
		sym, ok := idx.Symbols[v.Name]
		if !ok || sym.Type == "" {
			return nil
		}
		// Insert the annotation on the line above the decl. LSP
		// positions are 0-indexed.
		insertLine := v.Pos.Line - 1
		col := v.Pos.Column - 1
		if col < 0 {
			col = 0
		}
		indent := strings.Repeat(" ", col)
		return map[string]any{
			"title": fmt.Sprintf("Add type annotation `%s : %s`", v.Name, sym.Type),
			"kind":  "refactor",
			"edit": map[string]any{
				"changes": map[string]any{
					uri: []map[string]any{{
						"range": lspRange{
							Start: lspPosition{Line: insertLine, Character: col},
							End:   lspPosition{Line: insertLine, Character: col},
						},
						"newText": fmt.Sprintf("%s : %s\n%s", v.Name, sym.Type, indent),
					}},
				},
			},
		}
	}
	return nil
}

// extractQuotedName returns the suffix of msgText after the first
// matching prefix, trimming common surrounding punctuation. Returns
// "" if no prefix matches.
func extractQuotedName(msgText string, prefixes ...string) string {
	for _, p := range prefixes {
		if i := strings.Index(msgText, p); i >= 0 {
			rest := msgText[i+len(p):]
			// Stop at first whitespace or punctuation.
			end := len(rest)
			for j := 0; j < len(rest); j++ {
				b := rest[j]
				if b == ' ' || b == '\t' || b == '\n' || b == '"' || b == '\'' {
					end = j
					break
				}
			}
			return rest[:end]
		}
	}
	return ""
}

// levenshtein computes the edit distance between two strings.
// Standard DP, fine for the tiny strings we feed it.
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			min := prev[j] + 1
			if curr[j-1]+1 < min {
				min = curr[j-1] + 1
			}
			if prev[j-1]+cost < min {
				min = prev[j-1] + cost
			}
			curr[j] = min
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// --- Document formatting ---

type formattingParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

func (s *Server) handleFormatting(msg *message) {
	var p formattingParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		s.respond(msg.ID, []any{})
		return
	}
	s.mu.Lock()
	source := s.docs[p.TextDocument.URI]
	s.mu.Unlock()
	formatted := formatter.Format(source)
	if formatted == source {
		s.respond(msg.ID, []any{})
		return
	}
	// LSP wants TextEdit[] — return a single edit replacing the whole
	// document. End range covers a generous max-line/col so any valid
	// document is fully replaced.
	lines := strings.Split(source, "\n")
	endLine := len(lines)
	endCol := 0
	if endLine > 0 {
		endLine--
		endCol = len(lines[endLine])
	}
	s.respond(msg.ID, []any{map[string]any{
		"range": lspRange{
			Start: lspPosition{Line: 0, Character: 0},
			End:   lspPosition{Line: endLine, Character: endCol},
		},
		"newText": formatted,
	}})
}

// findIdentifierOccurrences returns all 0-indexed (line, col) positions
// where `name` appears as a whole-word identifier. "Whole word" means
// the surrounding characters can't be identifier-continuation chars
// (alphanumeric / underscore / dot); strings and comments are scanned
// crudely — refine if false-positives become a real issue.
type occurrence struct{ line, col int }

func findIdentifierOccurrences(src, name string) []occurrence {
	if name == "" {
		return nil
	}
	var out []occurrence
	isIdent := func(b byte) bool {
		return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
			(b >= '0' && b <= '9') || b == '_' || b == '.'
	}
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		// Strip line comments — anything from "--" to end-of-line.
		commentIdx := strings.Index(line, "--")
		scan := line
		if commentIdx >= 0 {
			scan = line[:commentIdx]
		}
		for col := 0; col+len(name) <= len(scan); {
			if scan[col:col+len(name)] == name {
				before := byte(' ')
				if col > 0 {
					before = scan[col-1]
				}
				after := byte(' ')
				if col+len(name) < len(scan) {
					after = scan[col+len(name)]
				}
				if !isIdent(before) && !isIdent(after) {
					out = append(out, occurrence{line: i, col: col})
					col += len(name)
					continue
				}
			}
			col++
		}
	}
	return out
}

// copyDocs takes a snapshot of the docs map under the caller's lock so
// downstream scans don't fight the read mutex on big workspaces.
func copyDocs(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// lspSymbolKind maps our Symbol kinds to LSP's SymbolKind enum.
func lspSymbolKind(k SymbolKind) int {
	switch k {
	case SymValue:
		return 12 // Function
	case SymTypeAlias:
		return 26 // TypeParameter (closest match for an alias)
	case SymCustomType:
		return 23 // Enum
	case SymConstructor:
		return 22 // EnumMember
	}
	return 13 // Variable (fallback)
}

// --- Diagnostics ---

// publishDiagnostics analyzes the document and pushes any errors to the
// editor as squiggles. Tries the project loader first (so cross-module
// imports resolve); falls back to single-file analysis if the file isn't
// part of a discoverable project.
func (s *Server) publishDiagnostics(uri, content string) {
	path, _ := uriToPath(uri)
	diags := s.analyze(path, content)
	if diags == nil {
		// LSP requires an array — nil marshals as JSON null which some
		// clients treat as "no update" instead of "clear diagnostics".
		diags = []lspDiagnostic{}
	}
	s.notify("textDocument/publishDiagnostics", map[string]any{
		"uri":         uri,
		"diagnostics": diags,
	})
}

// lspDiagnostic mirrors the LSP Diagnostic shape.
type lspDiagnostic struct {
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

// analyze returns LSP diagnostics for a single file. We try the project
// loader first; if it discovers a multi-file project rooted at a
// directory containing this file, we use those errors. Otherwise we fall
// back to single-file Parse + CheckModule.
func (s *Server) analyze(path, content string) []lspDiagnostic {
	// Project mode: only when the file is on disk and the dir contains
	// other .mar files we can load. A standalone document open (no path)
	// always falls through to single-file.
	if path != "" {
		if diags, ok := s.projectAnalyze(path, content); ok {
			return diags
		}
	}
	return s.singleFileAnalyze(content)
}

func (s *Server) projectAnalyze(path, content string) ([]lspDiagnostic, bool) {
	dir := filepath.Dir(path)
	// Only run project analysis if there's more than one .mar file in
	// the dir — otherwise it's effectively a single-file edit and we
	// don't want to fail on disk version vs editor version drift.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, false
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".mar") {
			count++
			if count > 1 {
				break
			}
		}
	}
	if count <= 1 {
		return nil, false
	}
	// LoadForServe reads files from disk, so for in-memory edits we
	// might be checking stale content. Acceptable tradeoff: the typed
	// info is up-to-date for ALL OTHER files; the active file gets a
	// second pass via singleFileAnalyze and we merge below.
	_, err = project.LoadForServe(path)
	if err != nil {
		// project errors with a file path: extract and convert.
		if d, ok := errorToDiagnostic(err, path, content); ok {
			return []lspDiagnostic{d}, true
		}
		return nil, false
	}
	// Project compiles cleanly; still check the in-memory content of
	// THIS file (might be edited but unsaved).
	return s.singleFileAnalyze(content), true
}

func (s *Server) singleFileAnalyze(content string) []lspDiagnostic {
	mod, perr := parser.Parse(content)
	if perr != nil {
		if d, ok := errorToDiagnostic(perr, "", content); ok {
			return []lspDiagnostic{d}
		}
		return nil
	}
	_, terr := typecheck.CheckModule(mod)
	if terr != nil {
		if d, ok := errorToDiagnostic(terr, "", content); ok {
			return []lspDiagnostic{d}
		}
	}
	return nil
}

// errorToDiagnostic maps a positioned compiler error to LSP shape.
// Returns ok=false when the error has no usable position (e.g. an
// I/O error from the project loader).
func errorToDiagnostic(err error, _ /*path*/, content string) (lspDiagnostic, bool) {
	line, col, msg, ok := extractPositioned(err)
	if !ok {
		return lspDiagnostic{}, false
	}
	// LSP positions are 0-indexed.
	startLine := line - 1
	startCol := col - 1
	if startLine < 0 {
		startLine = 0
	}
	if startCol < 0 {
		startCol = 0
	}
	endCol := startCol + 1
	// If we can read the offending source line, extend the highlight to
	// the end of the line so the squiggle is more visible than a single
	// character.
	if content != "" {
		lines := strings.Split(content, "\n")
		if startLine < len(lines) {
			endCol = len(lines[startLine])
			if endCol < startCol+1 {
				endCol = startCol + 1
			}
		}
	}
	return lspDiagnostic{
		Range: lspRange{
			Start: lspPosition{Line: startLine, Character: startCol},
			End:   lspPosition{Line: startLine, Character: endCol},
		},
		Severity: 1, // 1 = Error
		Source:   "mar",
		Message:  msg,
	}, true
}

// extractPositioned pulls (line, col, message) from a positioned error.
// Mirrors diag's positionOf but is duplicated here to avoid an import
// cycle (diag depends on lexer/parser/typecheck; lsp uses them too).
func extractPositioned(err error) (line, col int, msg string, ok bool) {
	switch e := err.(type) {
	case *lexer.Error:
		return e.Line, e.Column, e.Message, true
	case *parser.Error:
		return e.Line, e.Column, e.Message, true
	case *typecheck.InferError:
		return e.Pos.Line, e.Pos.Column, e.Message, true
	}
	return 0, 0, "", false
}

// uriToPath converts file:// URIs to filesystem paths.
func uriToPath(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if u.Scheme != "file" {
		return "", nil
	}
	return u.Path, nil
}
