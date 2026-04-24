package lsp

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"mar/internal/sexp"
)

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type lspTextEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

type lspWorkspaceEdit struct {
	Changes map[string][]lspTextEdit `json:"changes"`
}

type symbolKind string

const (
	symbolEntity      symbolKind = "entity"
	symbolEntityField symbolKind = "entity-field"
	symbolRecord      symbolKind = "record"
	symbolRecordField symbolKind = "record-field"
	symbolAction      symbolKind = "action"
)

type symbolOccurrence struct {
	URI         string
	Range       lspRange
	Key         string
	Name        string
	Kind        symbolKind
	Declaration bool
}

type workspaceSymbolIndex struct {
	occurrencesByURI  map[string][]symbolOccurrence
	occurrencesByKey  map[string][]symbolOccurrence
	declarationsByKey map[string]symbolOccurrence
}

type symbolCatalog struct {
	entities     map[string]string
	entityFields map[string]map[string]string
	records      map[string]string
	recordFields map[string]map[string]string
	actions      map[string]string
}

func (s *server) handleDefinition(id json.RawMessage, params textDocumentPositionParams) {
	index := s.buildWorkspaceSymbolIndex()
	symbol, ok := index.symbolAt(params.TextDocument.URI, params.Position)
	if !ok {
		s.respond(id, nil)
		return
	}
	location, ok := index.definition(symbol.Key)
	if !ok {
		s.respond(id, nil)
		return
	}
	s.respond(id, location)
}

func (s *server) handleReferences(id json.RawMessage, params referencesParams) {
	index := s.buildWorkspaceSymbolIndex()
	symbol, ok := index.symbolAt(params.TextDocument.URI, params.Position)
	if !ok {
		s.respond(id, []lspLocation{})
		return
	}
	s.respond(id, index.references(symbol.Key, params.Context.IncludeDeclaration))
}

func (s *server) handleRename(id json.RawMessage, params renameParams) {
	newName := strings.TrimSpace(params.NewName)
	if newName == "" {
		s.respondError(id, -32602, "New name cannot be empty.")
		return
	}

	index := s.buildWorkspaceSymbolIndex()
	symbol, ok := index.symbolAt(params.TextDocument.URI, params.Position)
	if !ok {
		s.respondError(id, -32602, "Cannot rename here. Place the cursor on an entity, field, record, or action name.")
		return
	}
	if err := validateRenameName(newName); err != nil {
		s.respondError(id, -32602, err.Error())
		return
	}

	edit := index.renameEdit(symbol.Key, newName)
	s.respond(id, edit)
}

func resolveWorkspaceRoots(params initializeParams) []string {
	roots := make([]string, 0, 4)
	seen := map[string]struct{}{}

	addPath := func(rawPath string) {
		path := strings.TrimSpace(rawPath)
		if path == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return
		}
		clean := filepath.Clean(abs)
		if _, ok := seen[clean]; ok {
			return
		}
		seen[clean] = struct{}{}
		roots = append(roots, clean)
	}

	for _, folder := range params.WorkspaceFolders {
		if path, err := fileURIToPath(folder.URI); err == nil {
			addPath(path)
		}
	}
	if len(roots) == 0 && strings.TrimSpace(params.RootURI) != "" {
		if path, err := fileURIToPath(params.RootURI); err == nil {
			addPath(path)
		}
	}
	if len(roots) == 0 && strings.TrimSpace(params.RootPath) != "" {
		addPath(params.RootPath)
	}
	return roots
}

func (s *server) buildWorkspaceSymbolIndex() *workspaceSymbolIndex {
	return buildWorkspaceSymbolIndex(s.collectWorkspaceDocuments())
}

func (s *server) collectWorkspaceDocuments() map[string]string {
	out := make(map[string]string, len(s.documents))
	openPaths := map[string]struct{}{}

	for uri, text := range s.documents {
		out[uri] = text
		if path, err := fileURIToPath(uri); err == nil {
			openPaths[filepath.Clean(path)] = struct{}{}
		}
	}

	for _, root := range s.effectiveWorkspaceRoots() {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if shouldSkipWorkspaceDir(root, path, d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.ToLower(filepath.Ext(d.Name())) != ".mar" {
				return nil
			}

			cleanPath := filepath.Clean(path)
			if _, ok := openPaths[cleanPath]; ok {
				return nil
			}
			uri := filePathToURI(cleanPath)
			if _, ok := out[uri]; ok {
				return nil
			}

			raw, readErr := os.ReadFile(cleanPath)
			if readErr != nil {
				return nil
			}
			out[uri] = string(raw)
			return nil
		})
	}

	return out
}

func (s *server) effectiveWorkspaceRoots() []string {
	if len(s.workspaceRoots) > 0 {
		return s.workspaceRoots
	}
	return inferWorkspaceRootsFromDocuments(s.documents)
}

func inferWorkspaceRootsFromDocuments(documents map[string]string) []string {
	roots := make([]string, 0, len(documents))
	seen := map[string]struct{}{}

	addRoot := func(path string) {
		clean := filepath.Clean(path)
		if _, ok := seen[clean]; ok {
			return
		}
		seen[clean] = struct{}{}
		roots = append(roots, clean)
	}

	for uri := range documents {
		path, err := fileURIToPath(uri)
		if err != nil {
			continue
		}
		addRoot(filepath.Dir(path))
	}

	sort.Strings(roots)
	return roots
}

func shouldSkipWorkspaceDir(root, path, name string) bool {
	if filepath.Clean(path) == filepath.Clean(root) {
		return false
	}
	switch name {
	case ".git", "node_modules", "build", "dist", ".gocache", ".gomodcache", ".elm":
		return true
	}
	return strings.HasPrefix(name, ".")
}

func buildWorkspaceSymbolIndex(documents map[string]string) *workspaceSymbolIndex {
	index := &workspaceSymbolIndex{
		occurrencesByURI:  map[string][]symbolOccurrence{},
		occurrencesByKey:  map[string][]symbolOccurrence{},
		declarationsByKey: map[string]symbolOccurrence{},
	}
	catalog := &symbolCatalog{
		entities:     map[string]string{},
		entityFields: map[string]map[string]string{},
		records:      map[string]string{},
		recordFields: map[string]map[string]string{},
		actions:      map[string]string{},
	}

	uris := make([]string, 0, len(documents))
	for uri := range documents {
		uris = append(uris, uri)
	}
	sort.Strings(uris)

	parsed := map[string][]sexp.Node{}
	for _, uri := range uris {
		nodes, err := sexp.Parse(documents[uri])
		if err != nil {
			continue
		}
		parsed[uri] = nodes
		indexDeclarations(uri, nodes, catalog, index)
	}
	for _, uri := range uris {
		indexReferences(uri, parsed[uri], catalog, index)
	}

	return index
}

func indexDeclarations(uri string, nodes []sexp.Node, catalog *symbolCatalog, index *workspaceSymbolIndex) {
	for _, node := range nodes {
		if !isListNamed(node, "define") && !isListNamed(node, "define-record") {
			continue
		}

		if isListNamed(node, "define-record") {
			indexRecordDeclaration(uri, node, catalog, index)
			continue
		}

		nameNode, valueNode, ok := defineBinding(node)
		if !ok {
			continue
		}
		switch listName(valueNode) {
		case "entity":
			name := nameNode.Value
			index.add(symbolOccurrence{URI: uri, Range: nodeRange(nameNode), Key: catalog.entityKey(name), Name: name, Kind: symbolEntity, Declaration: true})
			indexEntityFieldDeclarations(uri, name, valueNode, catalog, index)
		case "action":
			name := nameNode.Value
			index.add(symbolOccurrence{URI: uri, Range: nodeRange(nameNode), Key: catalog.actionKey(name), Name: name, Kind: symbolAction, Declaration: true})
		}
	}
}

func indexRecordDeclaration(uri string, node sexp.Node, catalog *symbolCatalog, index *workspaceSymbolIndex) {
	if len(node.Children) < 2 || node.Children[1].Kind != sexp.KindSymbol {
		return
	}
	nameNode := node.Children[1]
	name := nameNode.Value
	index.add(symbolOccurrence{URI: uri, Range: nodeRange(nameNode), Key: catalog.recordKey(name), Name: name, Kind: symbolRecord, Declaration: true})
	for _, fieldNode := range node.Children[2:] {
		children, ok := listSymbols(fieldNode)
		if !ok || len(children) < 1 {
			continue
		}
		field := children[0]
		index.add(symbolOccurrence{URI: uri, Range: nodeRange(field), Key: catalog.recordFieldKey(name, field.Value), Name: field.Value, Kind: symbolRecordField, Declaration: true})
	}
}

func indexEntityFieldDeclarations(uri, entityName string, entityNode sexp.Node, catalog *symbolCatalog, index *workspaceSymbolIndex) {
	for _, clause := range entityNode.Children[1:] {
		switch listName(clause) {
		case "fields":
			for _, fieldSpec := range secondListChildren(clause) {
				children, ok := listSymbols(fieldSpec)
				if !ok || len(children) < 1 {
					continue
				}
				field := children[0]
				index.add(symbolOccurrence{URI: uri, Range: nodeRange(field), Key: catalog.entityFieldKey(entityName, field.Value), Name: field.Value, Kind: symbolEntityField, Declaration: true})
			}
		case "belongs-to":
			for _, relationSpec := range secondListChildren(clause) {
				children, ok := listSymbols(relationSpec)
				if !ok || len(children) < 1 {
					continue
				}
				field := children[0]
				index.add(symbolOccurrence{URI: uri, Range: nodeRange(field), Key: catalog.entityFieldKey(entityName, field.Value), Name: field.Value, Kind: symbolEntityField, Declaration: true})
			}
		}
	}
}

func indexReferences(uri string, nodes []sexp.Node, catalog *symbolCatalog, index *workspaceSymbolIndex) {
	for _, node := range nodes {
		if !isListNamed(node, "define") && !isListNamed(node, "define-app") {
			continue
		}

		if isListNamed(node, "define-app") {
			indexDefappReferences(uri, node, catalog, index)
			continue
		}

		nameNode, valueNode, ok := defineBinding(node)
		if !ok {
			continue
		}
		switch listName(valueNode) {
		case "entity":
			indexEntityReferences(uri, nameNode.Value, valueNode, catalog, index)
		case "action":
			indexActionReferences(uri, valueNode, catalog, index)
		}
	}
}

func indexEntityReferences(uri string, entityName string, entityNode sexp.Node, catalog *symbolCatalog, index *workspaceSymbolIndex) {
	for _, clause := range entityNode.Children[1:] {
		switch listName(clause) {
		case "belongs-to":
			for _, relationSpec := range secondListChildren(clause) {
				children, ok := listSymbols(relationSpec)
				if !ok || len(children) < 1 {
					continue
				}
				target := children[0]
				if len(children) >= 2 {
					target = children[1]
				}
				if key, ok := catalog.entities[target.Value]; ok {
					index.add(symbolOccurrence{URI: uri, Range: nodeRange(target), Key: key, Name: target.Value, Kind: symbolEntity})
				}
			}
		case "validate", "authorize":
			if entityName != "" {
				indexFieldReferences(uri, clause, entityName, catalog, index)
			}
		}
	}
}

func indexActionReferences(uri string, actionNode sexp.Node, catalog *symbolCatalog, index *workspaceSymbolIndex) {
	for _, clause := range actionNode.Children[1:] {
		head := listName(clause)
		switch head {
		case "load", "create", "update", "delete":
			if len(clause.Children) < 2 || clause.Children[1].Kind != sexp.KindSymbol {
				continue
			}
			entityNode := clause.Children[1]
			entityName := entityNode.Value
			if key, ok := catalog.entities[entityName]; ok {
				index.add(symbolOccurrence{URI: uri, Range: nodeRange(entityNode), Key: key, Name: entityName, Kind: symbolEntity})
			}
			valuesIndex := 2
			if head == "update" {
				valuesIndex = 3
			}
			if len(clause.Children) > valuesIndex {
				indexActionValueFields(uri, clause.Children[valuesIndex], entityName, catalog, index)
			}
		}
	}
}

func indexActionValueFields(uri string, valuesNode sexp.Node, entityName string, catalog *symbolCatalog, index *workspaceSymbolIndex) {
	for _, valueSpec := range valuesNode.Children {
		children, ok := listSymbols(valueSpec)
		if !ok || len(children) < 1 {
			continue
		}
		field := children[0]
		if key, ok := catalog.lookupEntityField(entityName, field.Value); ok {
			index.add(symbolOccurrence{URI: uri, Range: nodeRange(field), Key: key, Name: field.Value, Kind: symbolEntityField})
		}
	}
}

func indexDefappReferences(uri string, node sexp.Node, catalog *symbolCatalog, index *workspaceSymbolIndex) {
	var walk func(sexp.Node)
	walk = func(current sexp.Node) {
		if current.Kind == sexp.KindSymbol {
			if key, ok := catalog.entities[current.Value]; ok {
				index.add(symbolOccurrence{URI: uri, Range: nodeRange(current), Key: key, Name: current.Value, Kind: symbolEntity})
				return
			}
			if key, ok := catalog.actions[current.Value]; ok {
				index.add(symbolOccurrence{URI: uri, Range: nodeRange(current), Key: key, Name: current.Value, Kind: symbolAction})
				return
			}
		}
		for _, child := range current.Children {
			walk(child)
		}
	}
	for _, child := range node.Children[2:] {
		walk(child)
	}
}

func indexFieldReferences(uri string, node sexp.Node, entityName string, catalog *symbolCatalog, index *workspaceSymbolIndex) {
	if node.Kind == sexp.KindSymbol {
		if key, ok := catalog.lookupEntityField(entityName, node.Value); ok {
			index.add(symbolOccurrence{URI: uri, Range: nodeRange(node), Key: key, Name: node.Value, Kind: symbolEntityField})
		}
		return
	}
	for _, child := range node.Children {
		indexFieldReferences(uri, child, entityName, catalog, index)
	}
}

func defineBinding(node sexp.Node) (sexp.Node, sexp.Node, bool) {
	if len(node.Children) < 3 {
		return sexp.Node{}, sexp.Node{}, false
	}
	nameNode := node.Children[1]
	if nameNode.Kind == sexp.KindList && len(nameNode.Children) > 0 {
		nameNode = nameNode.Children[0]
	}
	if nameNode.Kind != sexp.KindSymbol {
		return sexp.Node{}, sexp.Node{}, false
	}
	return nameNode, node.Children[2], true
}

func listSymbols(node sexp.Node) ([]sexp.Node, bool) {
	if node.Kind != sexp.KindList {
		return nil, false
	}
	for _, child := range node.Children {
		if child.Kind != sexp.KindSymbol {
			return nil, false
		}
	}
	return node.Children, true
}

func secondListChildren(node sexp.Node) []sexp.Node {
	if len(node.Children) < 2 || node.Children[1].Kind != sexp.KindList {
		return nil
	}
	return node.Children[1].Children
}

func isListNamed(node sexp.Node, name string) bool {
	return listName(node) == name
}

func listName(node sexp.Node) string {
	if node.Kind != sexp.KindList || len(node.Children) == 0 || node.Children[0].Kind != sexp.KindSymbol {
		return ""
	}
	return node.Children[0].Value
}

func validateRenameName(name string) error {
	if !isSourceIdentifier(name) {
		return fmt.Errorf("this symbol requires kebab-case. Example: %q", "order-item")
	}
	return nil
}

func isSourceIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		case r == '-' && i > 0 && i < len(name)-1:
		default:
			return false
		}
	}
	return true
}

func (index *workspaceSymbolIndex) add(occ symbolOccurrence) {
	index.occurrencesByURI[occ.URI] = append(index.occurrencesByURI[occ.URI], occ)
	index.occurrencesByKey[occ.Key] = append(index.occurrencesByKey[occ.Key], occ)
	if occ.Declaration {
		if _, ok := index.declarationsByKey[occ.Key]; !ok {
			index.declarationsByKey[occ.Key] = occ
		}
	}
}

func (index *workspaceSymbolIndex) symbolAt(uri string, pos lspPosition) (symbolOccurrence, bool) {
	occurrences := index.occurrencesByURI[uri]
	best := symbolOccurrence{}
	found := false
	bestSpan := 0

	for _, occ := range occurrences {
		if !rangeContainsPosition(occ.Range, pos) {
			continue
		}
		span := occ.Range.End.Character - occ.Range.Start.Character
		if !found || span < bestSpan || (span == bestSpan && prefersOccurrence(occ, best)) {
			best = occ
			found = true
			bestSpan = span
		}
	}

	return best, found
}

func prefersOccurrence(next symbolOccurrence, current symbolOccurrence) bool {
	if sameRange(next.Range, current.Range) &&
		current.Kind == symbolEntityField &&
		current.Declaration &&
		next.Kind == symbolEntity &&
		!next.Declaration {
		return true
	}
	return next.Declaration && !current.Declaration
}

func sameRange(a lspRange, b lspRange) bool {
	return a.Start.Line == b.Start.Line &&
		a.Start.Character == b.Start.Character &&
		a.End.Line == b.End.Line &&
		a.End.Character == b.End.Character
}

func (index *workspaceSymbolIndex) definition(key string) (lspLocation, bool) {
	decl, ok := index.declarationsByKey[key]
	if !ok {
		return lspLocation{}, false
	}
	return lspLocation{URI: decl.URI, Range: decl.Range}, true
}

func (index *workspaceSymbolIndex) references(key string, includeDeclaration bool) []lspLocation {
	occurrences := index.occurrencesByKey[key]
	out := make([]lspLocation, 0, len(occurrences))
	seen := map[string]struct{}{}

	for _, occ := range occurrences {
		if !includeDeclaration && occ.Declaration {
			continue
		}
		fingerprint := fmt.Sprintf("%s:%d:%d:%d", occ.URI, occ.Range.Start.Line, occ.Range.Start.Character, occ.Range.End.Character)
		if _, ok := seen[fingerprint]; ok {
			continue
		}
		seen[fingerprint] = struct{}{}
		out = append(out, lspLocation{URI: occ.URI, Range: occ.Range})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].URI != out[j].URI {
			return out[i].URI < out[j].URI
		}
		if out[i].Range.Start.Line != out[j].Range.Start.Line {
			return out[i].Range.Start.Line < out[j].Range.Start.Line
		}
		return out[i].Range.Start.Character < out[j].Range.Start.Character
	})
	return out
}

func (index *workspaceSymbolIndex) renameEdit(key string, newName string) lspWorkspaceEdit {
	changes := map[string][]lspTextEdit{}
	seen := map[string]struct{}{}

	for _, occ := range index.occurrencesByKey[key] {
		fingerprint := fmt.Sprintf("%s:%d:%d:%d", occ.URI, occ.Range.Start.Line, occ.Range.Start.Character, occ.Range.End.Character)
		if _, ok := seen[fingerprint]; ok {
			continue
		}
		seen[fingerprint] = struct{}{}
		changes[occ.URI] = append(changes[occ.URI], lspTextEdit{
			Range:   occ.Range,
			NewText: newName,
		})
	}

	for uri := range changes {
		edits := changes[uri]
		sort.Slice(edits, func(i, j int) bool {
			if edits[i].Range.Start.Line != edits[j].Range.Start.Line {
				return edits[i].Range.Start.Line > edits[j].Range.Start.Line
			}
			return edits[i].Range.Start.Character > edits[j].Range.Start.Character
		})
		changes[uri] = edits
	}

	return lspWorkspaceEdit{Changes: changes}
}

func (catalog *symbolCatalog) entityKey(name string) string {
	if key, ok := catalog.entities[name]; ok {
		return key
	}
	key := "entity:" + name
	catalog.entities[name] = key
	return key
}

func (catalog *symbolCatalog) entityFieldKey(entity, field string) string {
	if _, ok := catalog.entityFields[entity]; !ok {
		catalog.entityFields[entity] = map[string]string{}
	}
	if key, ok := catalog.entityFields[entity][field]; ok {
		return key
	}
	key := "entity-field:" + entity + "." + field
	catalog.entityFields[entity][field] = key
	return key
}

func (catalog *symbolCatalog) lookupEntityField(entity, field string) (string, bool) {
	entityMap, ok := catalog.entityFields[entity]
	if !ok {
		return "", false
	}
	key, ok := entityMap[field]
	return key, ok
}

func (catalog *symbolCatalog) recordKey(name string) string {
	if key, ok := catalog.records[name]; ok {
		return key
	}
	key := "record:" + name
	catalog.records[name] = key
	return key
}

func (catalog *symbolCatalog) recordFieldKey(record, field string) string {
	if _, ok := catalog.recordFields[record]; !ok {
		catalog.recordFields[record] = map[string]string{}
	}
	if key, ok := catalog.recordFields[record][field]; ok {
		return key
	}
	key := "record-field:" + record + "." + field
	catalog.recordFields[record][field] = key
	return key
}

func (catalog *symbolCatalog) actionKey(name string) string {
	if key, ok := catalog.actions[name]; ok {
		return key
	}
	key := "action:" + name
	catalog.actions[name] = key
	return key
}

func rangeContainsPosition(r lspRange, pos lspPosition) bool {
	if pos.Line < r.Start.Line || pos.Line > r.End.Line {
		return false
	}
	if r.Start.Line == r.End.Line {
		return pos.Character >= r.Start.Character && pos.Character < r.End.Character
	}
	if pos.Line == r.Start.Line {
		return pos.Character >= r.Start.Character
	}
	if pos.Line == r.End.Line {
		return pos.Character < r.End.Character
	}
	return true
}

func makeRange(line, start, end int) lspRange {
	return lspRange{
		Start: lspPosition{Line: line, Character: start},
		End:   lspPosition{Line: line, Character: end},
	}
}

func nodeRange(node sexp.Node) lspRange {
	start := node.Column - 1
	return makeRange(node.Line-1, start, start+len(node.Value))
}

func splitNormalizedLines(text string) []string {
	base := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	return strings.Split(base, "\n")
}

func fileURIToPath(rawURI string) (string, error) {
	parsed, err := url.Parse(rawURI)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" {
		return filepath.Clean(rawURI), nil
	}
	if parsed.Scheme != "file" {
		return "", fmt.Errorf("unsupported URI scheme %q", parsed.Scheme)
	}
	path, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" && strings.HasPrefix(path, "/") && len(path) >= 3 && path[2] == ':' {
		path = path[1:]
	}
	return filepath.Clean(path), nil
}

func filePathToURI(path string) string {
	clean := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		clean = filepath.ToSlash(clean)
		if !strings.HasPrefix(clean, "/") {
			clean = "/" + clean
		}
	}
	return (&url.URL{Scheme: "file", Path: clean}).String()
}
