package jsserve

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/parser"
	"mar/internal/typecheck"
)

// An open AudioContext wired to the destination is what makes a browser put the
// "playing audio" speaker on a tab. The web runtime used to open one on the
// first click of EVERY app, to satisfy the autoplay policy before any sound was
// asked for — so a CRUD app that never makes a noise wore the speaker forever
// after the first click.
//
// Now the unlock listeners are armed only for programs that reference Sound,
// decided by walking the serialized AST for `module: ["Sound"]`. That shape is
// produced by the Go serializer and consumed by hand-written JS, with nothing
// between them: if the serializer ever spells module references differently,
// the predicate silently answers "no sound" for every program and every game
// loses its audio, with no error on either side. This pins the two together.
const soundProgramSrc = `module Chirp exposing (..)


beep : Sound
beep =
    Sound.tone Sound.Square 440 100
`

const silentProgramSrc = `module Quiet exposing (..)


greeting : String
greeting =
    "no sound here"
`

func TestSilentProgramsDoNotArmAudio(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; skipping runtime.js run")
	}

	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"program using Sound", soundProgramSrc, true},
		{"program with no sound", silentProgramSrc, false},
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "runtime.js"), []byte(runtimeJS), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
process.stdout.write(String(globalThis.__marReferencesSound(program.modules)));
`
	if err := os.WriteFile(filepath.Join(dir, "driver.js"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mod, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := typecheck.CheckModule(mod); err != nil {
				t.Fatalf("typecheck: %v", err)
			}
			programJSON, err := json.Marshal(map[string]any{
				"modules": []any{SerializeModule(mod)},
			})
			if err != nil {
				t.Fatalf("marshal program: %v", err)
			}
			path := filepath.Join(dir, "program.json")
			if err := os.WriteFile(path, programJSON, 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(nodePath, filepath.Join(dir, "driver.js"),
				filepath.Join(dir, "runtime.js"), path)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("node run: %v\n%s", err, stderr.String())
			}
			got := strings.TrimSpace(string(out)) == "true"
			if got != tc.want {
				t.Fatalf("referencesSound = %v, want %v.\nA false negative here means every game "+
					"loses its audio unlock; a false positive means silent apps get the tab's "+
					"speaker badge back.", got, tc.want)
			}
		})
	}
}
