package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/parityfixture"
	"mar/internal/runtime"
)

// The server's half of parityfixture.TimeRangeCases: each expression is a
// top-level value, computed when the program loads, so a refusal is the
// load's error.
func TestTheServerKeepsATimeInsideItsRange(t *testing.T) {
	for _, c := range parityfixture.TimeRangeCases {
		dir := t.TempDir()
		entry := filepath.Join(dir, "Main.mar")
		src := "module Main exposing (..)\n\n\nwrapped : String\nwrapped =\n    " + c.Expr + "\n"
		if err := os.WriteFile(entry, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		env, _, err := LoadIntoEnvForRuntime(entry, nil)
		if c.Refused {
			if err == nil || !strings.Contains(err.Error(), c.Says+parityfixture.TimeRangeTail) {
				t.Errorf("%s\n  want the refusal %q\n  got  %v", c.Expr, c.Says, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.Expr, err)
			continue
		}
		v, _ := env.Lookup("Main.wrapped")
		if s, ok := v.(runtime.VString); !ok || s.V != c.Says {
			t.Errorf("%s\n  want %q\n  got  %v", c.Expr, c.Says, v)
		}
	}
}
