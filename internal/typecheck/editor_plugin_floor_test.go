package typecheck_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The plugin declares which VSCode it supports and, separately, which VSCode's
// API surface it compiles against. Those were different numbers:
//
//	engines.vscode          ^1.91.0    the floor it promises to run on
//	@types/vscode           ^1.91.0    a RANGE, resolved to 1.125.0
//
// So it type-checked against thirty-odd minor versions of API its own declared
// floor does not have. Nothing was broken -- the extension uses four ancient
// calls -- but nothing would have caught it either: reaching for an API added
// after 1.91 compiled, packaged, shipped, and failed on a user's machine.
//
// The types are pinned to the floor exactly now, so that is a compile error.
// Verified by reaching for McpServerDefinition, which arrived well after 1.91:
//
//	error TS2694: Namespace '"vscode"' has no exported member 'McpServerDefinition'
//
// The floor itself is not a free choice: vscode-languageclient 10 declares
// ^1.91.0, so the plugin cannot support less. Raising it drops users, which is
// a decision rather than a fix, and this test does not make it -- it only
// requires the two numbers to be the same one.
func TestThePluginCompilesAgainstTheOldestVSCodeItSupports(t *testing.T) {
	var pkg struct {
		Engines      map[string]string `json:"engines"`
		Dependencies map[string]string `json:"dependencies"`
		DevDeps      map[string]string `json:"devDependencies"`
	}
	b, err := os.ReadFile("../../vscode-mar/package.json")
	if err != nil {
		t.Fatalf("the plugin's manifest: %v", err)
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		t.Fatalf("the plugin's manifest is not valid JSON: %v", err)
	}

	floor := pkg.Engines["vscode"]
	if floor == "" {
		t.Fatal("the plugin declares no engines.vscode: nothing says which VSCode it runs on")
	}
	types := pkg.DevDeps["@types/vscode"]
	if types == "" {
		t.Fatal("the plugin has no @types/vscode: it compiles against no API surface at all")
	}

	// The types must be an exact version, not a range. A range is how the two
	// numbers drifted apart in the first place: ^1.91.0 quietly resolved to
	// whatever was newest at install time.
	if strings.ContainsAny(types, "^~*x><= ") {
		t.Errorf("@types/vscode is the range %q.\n"+
			"    A range resolves to whatever is newest at install time, so the plugin\n"+
			"    compiles against an API surface nobody chose. Pin it to the floor: %q",
			types, strings.TrimLeft(floor, "^~"))
	}
	if want := strings.TrimLeft(floor, "^~"); types != want {
		t.Errorf("the plugin promises to run on VSCode %s and compiles against %s.\n"+
			"    An API added between the two compiles here and is missing on a user's\n"+
			"    machine. Pin @types/vscode to %s, or raise engines.vscode on purpose.",
			floor, types, want)
	}

	// The floor is inherited from the language client, not chosen freely.
	if lc := pkg.Dependencies["vscode-languageclient"]; lc == "" {
		t.Error("the plugin no longer depends on vscode-languageclient; " +
			"the floor above came from it, so re-derive it")
	}
}
