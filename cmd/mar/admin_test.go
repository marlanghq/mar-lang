package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/project"
)

// TestPatchAdmins_AddToEmpty starts from a mar.json without an
// admins field and confirms the patch inserts it (after `name` for
// predictable layout).
func TestPatchAdmins_AddToEmpty(t *testing.T) {
	raw := []byte(`{
  "name": "my-app"
}
`)
	patched, changed, err := patchAdmins(raw, func(admins []string) []string {
		return append(admins, "me@example.com")
	})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	// Result should still be valid JSON, contain admins as an array
	// with the email in it.
	var roundTrip map[string]interface{}
	if err := json.Unmarshal(patched, &roundTrip); err != nil {
		t.Fatalf("patched output is not valid JSON: %v\noutput:\n%s", err, patched)
	}
	got, ok := roundTrip["admins"].([]interface{})
	if !ok {
		t.Fatalf("admins field is not an array: %v", roundTrip)
	}
	if len(got) != 1 || got[0] != "me@example.com" {
		t.Errorf("admins: got %v, want [me@example.com]", got)
	}
	// Predictable insertion: admins should appear after name.
	str := string(patched)
	if strings.Index(str, `"admins"`) < strings.Index(str, `"name"`) {
		t.Errorf("admins should be inserted after name; got:\n%s", str)
	}
}

// TestPatchAdmins_AppendPreservesOrder — adding to an existing list
// appends; doesn't sort. The user may have intentional ordering.
func TestPatchAdmins_AppendPreservesOrder(t *testing.T) {
	raw := []byte(`{
  "name": "my-app",
  "admins": [
    "z@x.com",
    "a@x.com"
  ]
}
`)
	patched, changed, err := patchAdmins(raw, func(admins []string) []string {
		return append(admins, "m@x.com")
	})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	var roundTrip map[string]interface{}
	if err := json.Unmarshal(patched, &roundTrip); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	got := roundTrip["admins"].([]interface{})
	want := []interface{}{"z@x.com", "a@x.com", "m@x.com"}
	if len(got) != len(want) {
		t.Fatalf("admins: got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("admins[%d]: got %v, want %v", i, got[i], want[i])
		}
	}
}

// TestPatchAdmins_NoChangeWhenIdempotent — running add for an email
// that's already present must report changed=false (so the CLI can
// avoid touching the file timestamp).
func TestPatchAdmins_NoChangeWhenIdempotent(t *testing.T) {
	raw := []byte(`{
  "name": "x",
  "admins": ["me@x.com"]
}
`)
	_, changed, err := patchAdmins(raw, func(admins []string) []string {
		// Caller's CLI logic dedupes; the transform here just simulates
		// the no-op case where admins is unchanged.
		return admins
	})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if changed {
		t.Errorf("expected no change")
	}
}

// TestPatchAdmins_RemoveAll — removing the last entry leaves
// `admins: []`. Subsequent runs are no-ops.
func TestPatchAdmins_RemoveAll(t *testing.T) {
	raw := []byte(`{
  "name": "x",
  "admins": ["me@x.com"]
}
`)
	patched, changed, err := patchAdmins(raw, func(admins []string) []string {
		return nil
	})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	var roundTrip map[string]interface{}
	if err := json.Unmarshal(patched, &roundTrip); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	admins, _ := roundTrip["admins"].([]interface{})
	if len(admins) != 0 {
		t.Errorf("admins should be empty; got %v", admins)
	}
}

// TestPatchAdmins_PreservesOtherFields — other top-level keys must
// survive the round-trip. Important because users will have `auth`,
// `mail`, `database`, `server` blocks that we don't want to clobber.
func TestPatchAdmins_PreservesOtherFields(t *testing.T) {
	raw := []byte(`{
  "name": "x",
  "auth": { "sessionSecret": "env:SESSION" },
  "mail": { "from": "noreply@x.com" }
}
`)
	patched, _, err := patchAdmins(raw, func(admins []string) []string {
		return append(admins, "me@x.com")
	})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	var roundTrip map[string]interface{}
	if err := json.Unmarshal(patched, &roundTrip); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := roundTrip["auth"]; !ok {
		t.Error("auth field disappeared")
	}
	if _, ok := roundTrip["mail"]; !ok {
		t.Error("mail field disappeared")
	}
	if roundTrip["name"] != "x" {
		t.Errorf("name field corrupted: %v", roundTrip["name"])
	}
}

// TestRunAdminAddRoundTrip — end-to-end: `mar admin add` from a temp
// project dir actually creates the right file. Uses chdir, since the
// CLI assumes cwd = project root (same convention as `mar build`).
func TestRunAdminAddRoundTrip(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(dir, "mar.json"), []byte(`{
  "name": "x"
}
`), 0o644))

	prevWD, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(prevWD) })
	must(os.Chdir(dir))

	if rc := runAdminAdd("me@example.com"); rc != 0 {
		t.Fatalf("rc=%d, want 0", rc)
	}

	got, err := os.ReadFile("mar.json")
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("file is not valid JSON: %v\nfile:\n%s", err, got)
	}
	admins, ok := parsed["admins"].([]interface{})
	if !ok || len(admins) != 1 || admins[0] != "me@example.com" {
		t.Errorf("admins: got %v", parsed["admins"])
	}
}

// TestRunAdminAddRejectsBadEmail — gibberish is rejected before any
// write happens.
func TestRunAdminAddRejectsBadEmail(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(dir, "mar.json"), []byte(`{"name":"x"}`), 0o644))
	prevWD, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(prevWD) })
	must(os.Chdir(dir))

	if rc := runAdminAdd("notanemail"); rc != 1 {
		t.Errorf("expected rc=1; got %d", rc)
	}

	// File should not have been modified.
	got, _ := os.ReadFile("mar.json")
	if strings.Contains(string(got), "admins") {
		t.Errorf("mar.json should not contain admins after invalid add: %s", got)
	}
}

// TestLoadAdminsFromManifest — canonicalization (lowercase, trim,
// dedupe) and sort. Important because boot sync relies on this.
func TestLoadAdminsFromManifest(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, []string{}},
		{"single", []string{"a@x.com"}, []string{"a@x.com"}},
		{"trim", []string{"  a@x.com  "}, []string{"a@x.com"}},
		{"lowercase", []string{"A@X.com"}, []string{"a@x.com"}},
		{"dedupe", []string{"a@x.com", "A@X.com"}, []string{"a@x.com"}},
		{"sort", []string{"z@x.com", "a@x.com"}, []string{"a@x.com", "z@x.com"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := LoadAdminsFromManifest(&project.Manifest{Admins: tc.in})
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d]: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
