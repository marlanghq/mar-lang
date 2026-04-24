package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectWorkspaceDocumentsUsesOpenDocumentDirectoryWithoutWorkspaceRoots(t *testing.T) {
	tmpDir := t.TempDir()

	rootOnlyPath := filepath.Join(tmpDir, "root.mar")
	openPath := filepath.Join(tmpDir, "examples", "todo-owned.mar")
	siblingPath := filepath.Join(tmpDir, "examples", "shared.mar")

	if err := os.MkdirAll(filepath.Dir(openPath), 0o755); err != nil {
		t.Fatalf("mkdir examples: %v", err)
	}
	if err := os.WriteFile(rootOnlyPath, []byte("(define root (entity (fields ((title string)))))\n"), 0o644); err != nil {
		t.Fatalf("write root file: %v", err)
	}
	if err := os.WriteFile(openPath, []byte("(define todo (entity (fields ((title string)))))\n"), 0o644); err != nil {
		t.Fatalf("write open file: %v", err)
	}
	if err := os.WriteFile(siblingPath, []byte("(define shared (entity (fields ((title string)))))\n"), 0o644); err != nil {
		t.Fatalf("write sibling file: %v", err)
	}

	srv := &server{
		documents: map[string]string{
			filePathToURI(openPath): "(define todo (entity (fields ((title string)))))\n",
		},
	}

	documents := srv.collectWorkspaceDocuments()

	if _, ok := documents[filePathToURI(openPath)]; !ok {
		t.Fatalf("expected open document to be indexed")
	}
	if _, ok := documents[filePathToURI(siblingPath)]; !ok {
		t.Fatalf("expected sibling document in the same directory to be indexed")
	}
	if _, ok := documents[filePathToURI(rootOnlyPath)]; ok {
		t.Fatalf("did not expect files outside the open document directory to be indexed")
	}
}

func TestDefinitionOnBelongsToEntityReferenceResolvesToEntityDeclaration(t *testing.T) {
	uri := filePathToURI(filepath.Join(t.TempDir(), "todo-owned.mar"))
	text := strings.Join([]string{
		"(define user",
		"  (entity",
		"    (fields",
		"      ((email string)))))",
		"",
		"(define todo",
		"  (entity",
		"    (fields",
		"      ((title string)))",
		"    (belongs-to",
		"      ((owner user)))))",
		"",
	}, "\n")

	index := buildWorkspaceSymbolIndex(map[string]string{uri: text})
	line := 10
	character := strings.Index("      ((owner user)))", "user")
	if character < 0 {
		t.Fatalf("expected test fixture to contain user reference")
	}

	symbol, ok := index.symbolAt(uri, lspPosition{Line: line, Character: character})
	if !ok {
		t.Fatalf("expected symbol at belongs-to entity reference")
	}
	if symbol.Kind != symbolEntity {
		t.Fatalf("expected entity symbol, got %q", symbol.Kind)
	}
	if symbol.Name != "user" {
		t.Fatalf("expected symbol name user, got %q", symbol.Name)
	}

	def, ok := index.definition(symbol.Key)
	if !ok {
		t.Fatalf("expected definition for entity reference")
	}
	if def.URI != uri {
		t.Fatalf("expected definition in same document, got %q", def.URI)
	}
	if def.Range.Start.Line != 0 {
		t.Fatalf("expected definition on line 0, got %d", def.Range.Start.Line)
	}
}

func TestDefinitionOnShortBelongsToResolvesToEntityDeclaration(t *testing.T) {
	uri := filePathToURI(filepath.Join(t.TempDir(), "personal-todo.mar"))
	text := strings.Join([]string{
		"(define user",
		"  (entity",
		"    (fields",
		"      ((email string)))))",
		"",
		"(define personal-todo",
		"  (entity",
		"    (fields",
		"      ((title string)))",
		"    (belongs-to",
		"      ((user)))))",
		"",
	}, "\n")

	index := buildWorkspaceSymbolIndex(map[string]string{uri: text})
	line := 10
	character := strings.Index("      ((user)))", "user")
	if character < 0 {
		t.Fatalf("expected test fixture to contain user reference")
	}

	symbol, ok := index.symbolAt(uri, lspPosition{Line: line, Character: character})
	if !ok {
		t.Fatalf("expected symbol at belongs-to user reference")
	}
	if symbol.Kind != symbolEntity {
		t.Fatalf("expected entity symbol, got %q", symbol.Kind)
	}
	if symbol.Name != "user" {
		t.Fatalf("expected symbol name user, got %q", symbol.Name)
	}

	def, ok := index.definition(symbol.Key)
	if !ok {
		t.Fatalf("expected definition for user reference")
	}
	if def.URI != uri {
		t.Fatalf("expected definition in same document, got %q", def.URI)
	}
	if def.Range.Start.Line != 0 {
		t.Fatalf("expected definition on line 0, got %d", def.Range.Start.Line)
	}
}
