package lsp

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
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
	symbolAlias       symbolKind = "type-alias"
	symbolAliasField  symbolKind = "alias-field"
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
	aliases      map[string]string
	aliasFields  map[string]map[string]string
	actions      map[string]string
	actionInputs map[string]string
}

var (
	entityDeclRe = regexp.MustCompile(`^\s*entity\s+([A-Za-z][A-Za-z0-9_]*)\s*\{`)
	fieldDeclRe  = regexp.MustCompile(`^\s*([a-z][A-Za-z0-9_]*)\s*:\s*(Int|String|Bool|Float|Posix)\b`)

	typeAliasDeclRe  = regexp.MustCompile(`^\s*type\s+alias\s+([A-Za-z][A-Za-z0-9_]*)\s*=\s*(.*)$`)
	aliasFieldDeclRe = regexp.MustCompile(`([a-z][A-Za-z0-9_]*)\s*:\s*(Int|String|Bool|Float|Posix)\b`)

	actionDeclRe        = regexp.MustCompile(`^\s*action\s+([a-z][A-Za-z0-9_]*)\s*\{\s*$`)
	actionInputRe       = regexp.MustCompile(`^\s*input\s*:\s*([A-Za-z][A-Za-z0-9_]*)\s*$`)
	createDeclRe        = regexp.MustCompile(`^\s*create\s+([A-Za-z][A-Za-z0-9_]*)\s*\{\s*$`)
	actionFieldAssignRe = regexp.MustCompile(`^\s*([a-z][A-Za-z0-9_]*)\s*:\s*(.+)$`)
	actionInputRefRe    = regexp.MustCompile(`\binput\.([a-z][A-Za-z0-9_]*)\b`)

	ruleLineRe      = regexp.MustCompile(`^\s*rule\s+"[^"]+"\s+expect\s+(.+)$`)
	authorizeLineRe = regexp.MustCompile(`^\s*authorize\s+(?:all|list|get|create|update|delete)\s+when\s+(.+)$`)
	wordRe          = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*\b`)

	authOpenRe = regexp.MustCompile(`^\s*auth\s*\{\s*$`)

	upperIdentifierRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	lowerIdentifierRe = regexp.MustCompile(`^[a-z][A-Za-z0-9_]*$`)
)

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
		s.respondError(id, -32602, "Cannot rename here. Place the cursor on an entity, field, type alias, or action name.")
		return
	}
	if err := validateRenameName(symbol.Kind, newName); err != nil {
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
	if len(roots) == 0 {
		if cwd, err := os.Getwd(); err == nil {
			addPath(cwd)
		}
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

	for _, root := range s.workspaceRoots {
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
		aliases:      map[string]string{},
		aliasFields:  map[string]map[string]string{},
		actions:      map[string]string{},
		actionInputs: map[string]string{},
	}

	uris := make([]string, 0, len(documents))
	for uri := range documents {
		uris = append(uris, uri)
	}
	sort.Strings(uris)

	for _, uri := range uris {
		indexDeclarations(uri, documents[uri], catalog, index)
	}
	for _, uri := range uris {
		indexReferences(uri, documents[uri], catalog, index)
	}

	return index
}

func indexDeclarations(uri, text string, catalog *symbolCatalog, index *workspaceSymbolIndex) {
	lines := splitNormalizedLines(text)
	currentEntity := ""
	currentAlias := ""
	currentAction := ""
	actionInCreate := false

	for lineNo, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isCommentOrBlankLSP(trimmed) {
			continue
		}

		if currentEntity != "" {
			if trimmed == "}" {
				currentEntity = ""
				continue
			}
			if match := fieldDeclRe.FindStringSubmatchIndex(line); match != nil {
				fieldName := line[match[2]:match[3]]
				key := catalog.entityFieldKey(currentEntity, fieldName)
				index.add(symbolOccurrence{
					URI:         uri,
					Range:       makeRange(lineNo, match[2], match[3]),
					Key:         key,
					Name:        fieldName,
					Kind:        symbolEntityField,
					Declaration: true,
				})
			}
			continue
		}

		if currentAlias != "" {
			indexAliasFieldDeclarations(uri, lineNo, line, currentAlias, catalog, index)
			if strings.Contains(line, "}") {
				currentAlias = ""
			}
			continue
		}

		if currentAction != "" {
			if actionInCreate {
				if trimmed == "}" {
					actionInCreate = false
				}
				continue
			}
			if match := actionInputRe.FindStringSubmatchIndex(line); match != nil {
				inputAlias := line[match[2]:match[3]]
				catalog.actionInputs[currentAction] = inputAlias
				continue
			}
			if createDeclRe.MatchString(line) {
				actionInCreate = true
				continue
			}
			if trimmed == "}" {
				currentAction = ""
				continue
			}
			continue
		}

		if match := entityDeclRe.FindStringSubmatchIndex(line); match != nil {
			entityName := line[match[2]:match[3]]
			key := catalog.entityKey(entityName)
			index.add(symbolOccurrence{
				URI:         uri,
				Range:       makeRange(lineNo, match[2], match[3]),
				Key:         key,
				Name:        entityName,
				Kind:        symbolEntity,
				Declaration: true,
			})
			currentEntity = entityName
			continue
		}

		if match := typeAliasDeclRe.FindStringSubmatchIndex(line); match != nil {
			aliasName := line[match[2]:match[3]]
			key := catalog.aliasKey(aliasName)
			index.add(symbolOccurrence{
				URI:         uri,
				Range:       makeRange(lineNo, match[2], match[3]),
				Key:         key,
				Name:        aliasName,
				Kind:        symbolAlias,
				Declaration: true,
			})
			currentAlias = aliasName
			indexAliasFieldDeclarations(uri, lineNo, line, currentAlias, catalog, index)
			if strings.Contains(line, "}") {
				currentAlias = ""
			}
			continue
		}

		if match := actionDeclRe.FindStringSubmatchIndex(line); match != nil {
			actionName := line[match[2]:match[3]]
			key := catalog.actionKey(actionName)
			index.add(symbolOccurrence{
				URI:         uri,
				Range:       makeRange(lineNo, match[2], match[3]),
				Key:         key,
				Name:        actionName,
				Kind:        symbolAction,
				Declaration: true,
			})
			currentAction = actionName
		}
	}
}

func indexAliasFieldDeclarations(uri string, lineNo int, line string, aliasName string, catalog *symbolCatalog, index *workspaceSymbolIndex) {
	matches := aliasFieldDeclRe.FindAllStringSubmatchIndex(line, -1)
	for _, match := range matches {
		fieldName := line[match[2]:match[3]]
		key := catalog.aliasFieldKey(aliasName, fieldName)
		index.add(symbolOccurrence{
			URI:         uri,
			Range:       makeRange(lineNo, match[2], match[3]),
			Key:         key,
			Name:        fieldName,
			Kind:        symbolAliasField,
			Declaration: true,
		})
	}
}

func indexReferences(uri, text string, catalog *symbolCatalog, index *workspaceSymbolIndex) {
	lines := splitNormalizedLines(text)
	currentEntity := ""
	currentAlias := ""
	inAuth := false
	activeAction := ""
	activeActionInputAlias := ""
	activeCreateEntity := ""
	inCreate := false

	for lineNo, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isCommentOrBlankLSP(trimmed) {
			continue
		}

		if currentEntity != "" {
			if trimmed == "}" {
				currentEntity = ""
				continue
			}
			if match := ruleLineRe.FindStringSubmatchIndex(line); match != nil {
				indexExpressionFieldReferences(uri, lineNo, line, match[2], match[3], currentEntity, catalog, index)
			}
			if match := authorizeLineRe.FindStringSubmatchIndex(line); match != nil {
				indexExpressionFieldReferences(uri, lineNo, line, match[2], match[3], currentEntity, catalog, index)
			}
			continue
		}

		if currentAlias != "" {
			if strings.Contains(line, "}") {
				currentAlias = ""
			}
			continue
		}

		if inAuth {
			if trimmed == "}" {
				inAuth = false
				continue
			}
		}

		if authOpenRe.MatchString(line) {
			inAuth = true
			continue
		}

		if match := entityDeclRe.FindStringSubmatchIndex(line); match != nil {
			currentEntity = line[match[2]:match[3]]
			continue
		}
		if match := typeAliasDeclRe.FindStringSubmatchIndex(line); match != nil {
			currentAlias = line[match[2]:match[3]]
			if strings.Contains(line, "}") {
				currentAlias = ""
			}
			continue
		}

		if match := actionDeclRe.FindStringSubmatchIndex(line); match != nil {
			actionName := line[match[2]:match[3]]
			activeAction = actionName
			activeActionInputAlias = catalog.actionInputs[actionName]
			activeCreateEntity = ""
			inCreate = false
			if key, ok := catalog.actions[actionName]; ok {
				index.add(symbolOccurrence{
					URI:   uri,
					Range: makeRange(lineNo, match[2], match[3]),
					Key:   key,
					Name:  actionName,
					Kind:  symbolAction,
				})
			}
			continue
		}

		if activeAction == "" {
			continue
		}

		if inCreate {
			if trimmed == "}" {
				inCreate = false
				activeCreateEntity = ""
				continue
			}

			if match := actionFieldAssignRe.FindStringSubmatchIndex(line); match != nil {
				fieldName := line[match[2]:match[3]]
				if key, ok := catalog.lookupEntityField(activeCreateEntity, fieldName); ok {
					index.add(symbolOccurrence{
						URI:   uri,
						Range: makeRange(lineNo, match[2], match[3]),
						Key:   key,
						Name:  fieldName,
						Kind:  symbolEntityField,
					})
				}
			}

			inputMatches := actionInputRefRe.FindAllStringSubmatchIndex(line, -1)
			for _, inputMatch := range inputMatches {
				fieldName := line[inputMatch[2]:inputMatch[3]]
				if key, ok := catalog.lookupAliasField(activeActionInputAlias, fieldName); ok {
					index.add(symbolOccurrence{
						URI:   uri,
						Range: makeRange(lineNo, inputMatch[2], inputMatch[3]),
						Key:   key,
						Name:  fieldName,
						Kind:  symbolAliasField,
					})
				}
			}
			continue
		}

		if match := actionInputRe.FindStringSubmatchIndex(line); match != nil {
			aliasName := line[match[2]:match[3]]
			activeActionInputAlias = aliasName
			if key, ok := catalog.aliases[aliasName]; ok {
				index.add(symbolOccurrence{
					URI:   uri,
					Range: makeRange(lineNo, match[2], match[3]),
					Key:   key,
					Name:  aliasName,
					Kind:  symbolAlias,
				})
			}
			continue
		}

		if match := createDeclRe.FindStringSubmatchIndex(line); match != nil {
			entityName := line[match[2]:match[3]]
			activeCreateEntity = entityName
			inCreate = true
			if key, ok := catalog.entities[entityName]; ok {
				index.add(symbolOccurrence{
					URI:   uri,
					Range: makeRange(lineNo, match[2], match[3]),
					Key:   key,
					Name:  entityName,
					Kind:  symbolEntity,
				})
			}
			continue
		}

		if trimmed == "}" {
			activeAction = ""
			activeActionInputAlias = ""
			activeCreateEntity = ""
			inCreate = false
		}
	}
}

func indexExpressionFieldReferences(uri string, lineNo int, line string, exprStart int, exprEnd int, entityName string, catalog *symbolCatalog, index *workspaceSymbolIndex) {
	if exprStart < 0 || exprEnd <= exprStart || exprEnd > len(line) {
		return
	}
	exprValue := line[exprStart:exprEnd]
	tokens := wordRe.FindAllStringSubmatchIndex(exprValue, -1)
	for _, token := range tokens {
		name := exprValue[token[0]:token[1]]
		key, ok := catalog.lookupEntityField(entityName, name)
		if !ok {
			continue
		}
		start := exprStart + token[0]
		end := exprStart + token[1]
		index.add(symbolOccurrence{
			URI:   uri,
			Range: makeRange(lineNo, start, end),
			Key:   key,
			Name:  name,
			Kind:  symbolEntityField,
		})
	}
}

func validateRenameName(kind symbolKind, name string) error {
	switch kind {
	case symbolEntity, symbolAlias:
		if !upperIdentifierRe.MatchString(name) {
			return fmt.Errorf("This symbol requires UpperCamelCase. Example: %q", "OrderItem")
		}
	case symbolAction, symbolEntityField, symbolAliasField:
		if !lowerIdentifierRe.MatchString(name) {
			return fmt.Errorf("This symbol requires lowerCamelCase. Example: %q", "orderItem")
		}
	}
	return nil
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
		if !found || span < bestSpan || (span == bestSpan && occ.Declaration && !best.Declaration) {
			best = occ
			found = true
			bestSpan = span
		}
	}

	return best, found
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

func (catalog *symbolCatalog) aliasKey(name string) string {
	if key, ok := catalog.aliases[name]; ok {
		return key
	}
	key := "alias:" + name
	catalog.aliases[name] = key
	return key
}

func (catalog *symbolCatalog) aliasFieldKey(alias, field string) string {
	if _, ok := catalog.aliasFields[alias]; !ok {
		catalog.aliasFields[alias] = map[string]string{}
	}
	if key, ok := catalog.aliasFields[alias][field]; ok {
		return key
	}
	key := "alias-field:" + alias + "." + field
	catalog.aliasFields[alias][field] = key
	return key
}

func (catalog *symbolCatalog) lookupAliasField(alias, field string) (string, bool) {
	aliasMap, ok := catalog.aliasFields[alias]
	if !ok {
		return "", false
	}
	key, ok := aliasMap[field]
	return key, ok
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

func splitNormalizedLines(text string) []string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	return strings.Split(normalized, "\n")
}

func isCommentOrBlankLSP(trimmed string) bool {
	return trimmed == "" || strings.HasPrefix(trimmed, "--")
}

func fileURIToPath(rawURI string) (string, error) {
	parsed, err := url.Parse(rawURI)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "file" {
		return "", fmt.Errorf("unsupported uri scheme %q", parsed.Scheme)
	}
	path := parsed.Path
	if path == "" {
		return "", fmt.Errorf("empty file uri")
	}
	if runtime.GOOS == "windows" && strings.HasPrefix(path, "/") && len(path) > 2 && path[2] == ':' {
		path = path[1:]
	}
	path = filepath.FromSlash(path)
	return filepath.Clean(path), nil
}

func filePathToURI(path string) string {
	absPath, err := filepath.Abs(path)
	if err == nil {
		path = absPath
	}
	slashPath := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String()
}
