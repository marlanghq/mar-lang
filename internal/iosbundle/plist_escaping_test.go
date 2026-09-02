package iosbundle

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Info.plist is XML, and the values written into it come from mar.json. An app
// called `Tom & Jerry` is an ordinary name; an unescaped `&` is a broken
// document.
//
// Measured before this, `plutil -lint` on the generated file said "Encountered
// unknown ampersand-escape sequence at line 8" — and the developer met that in
// Xcode, several steps away from the mar.json field that caused it.
//
// This is the same shape as unquoted SQL identifiers: a string the program
// controls, dropped into a host syntax that has its own meaning for some of
// its characters.
func TestAnAppNameWithMarkupStillProducesAValidPlist(t *testing.T) {
	spec := fullSpec()
	spec.DisplayName = `Tom & Jerry <b>"quoted"</b>`

	projectDir, err := Generate(spec, t.TempDir())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(projectDir, "Info.plist"))
	if err != nil {
		t.Fatal(err)
	}

	// Parsed by an XML reader, not by looking for the escapes: what matters is
	// that the document is well formed and the name survives it unchanged.
	var doc struct {
		Strings []string `xml:"dict>string"`
	}
	if err := xml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the generated Info.plist is not valid XML: %v\n%s", err, raw)
	}
	found := false
	for _, s := range doc.Strings {
		if s == spec.DisplayName {
			found = true
		}
	}
	if !found {
		t.Errorf("the display name did not survive the round trip; got %q", doc.Strings)
	}

	// The raw bytes must not carry the character that broke the parser.
	if strings.Contains(string(raw), "Tom & Jerry") {
		t.Error("the `&` reached the document unescaped")
	}
}

// An ordinary name is untouched: escaping has to be invisible when there is
// nothing to escape, or every existing project's plist changes for no reason.
func TestAnOrdinaryAppNameIsWrittenAsItIs(t *testing.T) {
	spec := fullSpec()
	spec.DisplayName = "Notes Fullstack"

	projectDir, err := Generate(spec, t.TempDir())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(projectDir, "Info.plist"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "<string>Notes Fullstack</string>") {
		t.Errorf("an ordinary name should appear as written:\n%s", raw)
	}
}
