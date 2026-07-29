package main

import (
	"path/filepath"
	"testing"

	"mar/internal/project"
	"mar/internal/scaffold"
)

// TestScaffoldKindsCompile guarantees every `mar init` kind scaffolds to a
// project that typechecks. Nothing else compiled the generated templates, so
// language/framework migrations (Effect→Task/Cmd, the required subscriptions
// page field, text taking attrs) silently rotted them and `mar init` shipped
// projects that failed on the first `mar check`. This test is the guard:
// project.Load runs the same multi-module typecheck the CLI does, so a
// template that stops compiling fails here.
func TestScaffoldKindsCompile(t *testing.T) {
	kinds := []scaffold.Kind{
		scaffold.KindMinimum,
		scaffold.KindFrontend,
		scaffold.KindBackend,
		scaffold.KindFullstack,
		scaffold.KindFullstackAuth,
	}
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "proj")
			if err := scaffold.Init(dir, kind); err != nil {
				t.Fatalf("scaffold.Init(%s): %v", kind, err)
			}
			if _, err := project.Load(dir); err != nil {
				t.Fatalf("generated %s project failed to typecheck:\n%v", kind, err)
			}
		})
	}
}
