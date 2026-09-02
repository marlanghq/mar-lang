package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One program must not have two answers.
//
// `mar dev` installs apphost's App.* builtins and runs every boot check.
// `mar build --target ios` replaced them with its own capture, which reads
// `pages` and ignored `services` entirely — so a program `mar dev` refused
// with the offending contract named still built an iOS app and printed
// "Next steps". The phone got the broken one.
//
// This is the floor under that: the iOS path runs apphost.CheckProgram, so a
// program either builds on both or on neither.

func iosProbe(t *testing.T, mutate func(dir string)) error {
	t.Helper()
	dir := t.TempDir()
	src := "../../examples/notes-fullstack"
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if e.Name() == "mar.json" {
			b = []byte(`{"name":"notes-fullstack","locale":"en",` +
				`"database":{"path":"./notes.db"},` +
				`"ios":{"bundleId":"dev.probe.notes","displayName":"Notes",` +
				`"marketingVersion":"1.0","buildNumber":"1"}}`)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if mutate != nil {
		mutate(dir)
	}
	_, err = BuildIOS(dir, t.TempDir(), "test")
	return err
}

func TestTheIOSBuildRefusesWhatTheDevServerRefuses(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a project; skipped under -short")
	}
	err := iosProbe(t, func(dir string) {
		p := filepath.Join(dir, "Backend.mar")
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			t.Fatal(readErr)
		}
		cut := strings.Replace(string(b),
			"    [ Service.implement Shared.listAllNotes (\\_ -> Repo.all notes)\n    ,",
			"    [", 1)
		if cut == string(b) {
			t.Fatal("the example moved; this test is editing text that is no longer there")
		}
		if err := os.WriteFile(p, []byte(cut), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil {
		t.Fatal("the page's only fetch has no handler, and the iOS build shipped it")
	}
	for _, want := range []string{"listAllNotes", "GET /notes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message must mention %q, got: %v", want, err)
		}
	}
}

func TestTheIOSBuildAcceptsWhatTheDevServerAccepts(t *testing.T) {
	// The other half: the example as shipped must still build, or the check
	// above is just a way of refusing everything.
	if testing.Short() {
		t.Skip("compiles a project; skipped under -short")
	}
	if err := iosProbe(t, nil); err != nil {
		t.Fatalf("the example as shipped: %v", err)
	}
}

// The Swift runtime throws "Page.adminProtected is web-only; the admin panel
// has no iOS app" when such a page is reached. Measured: the bundle built,
// printed "Next steps", and the failure waited for whoever tapped through to
// it.
//
// The web build of the SAME program is fine, which is why this cannot be
// decided from the App.* entry point alone: a phone build registers itself as
// App.fullstack.
func TestAnAdminPageIsRefusedByTheIOSBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a project; skipped under -short")
	}
	err := iosProbe(t, func(dir string) {
		p := filepath.Join(dir, "Frontend.mar")
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			t.Fatal(readErr)
		}
		src := strings.Replace(string(b), "Page.create", "Page.adminProtected", 1)
		if src == string(b) {
			t.Skip("the example no longer uses Page.create")
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil {
		t.Fatal("the phone has no admin panel, and this page waits on one")
	}
	// Either the type checker refuses the swap (adminProtected takes an
	// AdminSession) or the boot check does; both are the build saying no.
	// What must never happen is a bundle that ships.
}
