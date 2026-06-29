// Tests for the appbundle ZIP loader, with particular focus on the
// zip-slip defense. The threat model is a tampered payload appended
// to a `mar build` binary: an attacker swaps the trailing ZIP for
// one whose entry names try to escape the temp extraction directory.
// parsePayload must reject these at load time, and ExtractToDir
// must refuse to write a path that escapes destDir even if a Bundle
// is constructed programmatically with bad data (defense in depth).

package appbundle

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildPayloadWithEntries packages a custom set of (name, body)
// entries as a ZIP — bypassing BuildPayload's shape constraints
// (which only ever writes mar.json + src/*). Used by the zip-slip
// tests to simulate a tampered bundle.
func buildPayloadWithEntries(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	w := zip.NewWriter(buf)
	for name, data := range entries {
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		wr, err := w.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := wr.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestSafeBundleEntryName_Rejects covers the unit-level validation.
// Each case is something parsePayload must reject hard.
func TestSafeBundleEntryName_Rejects(t *testing.T) {
	bad := []struct {
		name string
		why  string
	}{
		{"", "empty"},
		{"src/../../etc/passwd", "parent-segment traversal under src/"},
		{"src/../sibling", "single parent traversal"},
		{"../etc/passwd", "leading parent traversal"},
		{"/etc/passwd", "absolute path"},
		{"src\\..\\..\\evil", "backslash separator"},
		{"src/.", "dot segment"},
		{"src//foo", "empty segment"},
		{"src/./foo", "dot segment in middle"},
		{"src/foo/../bar", "embedded parent"},
		{"src/", "missing rel"},
		{"random.bin", "neither manifest nor src/"},
		{"./mar.json", "dot prefix"},
	}
	for _, c := range bad {
		t.Run(c.why, func(t *testing.T) {
			if err := safeBundleEntryName(c.name); err == nil {
				t.Errorf("name %q should have been rejected (%s)", c.name, c.why)
			}
		})
	}
}

// TestSafeBundleEntryName_Accepts — the legitimate shapes must pass.
func TestSafeBundleEntryName_Accepts(t *testing.T) {
	good := []string{
		"mar.json",
		"src/Main.mar",
		"src/Frontend/SignIn.mar",
		"src/deep/nested/Module.mar",
	}
	for _, name := range good {
		t.Run(name, func(t *testing.T) {
			if err := safeBundleEntryName(name); err != nil {
				t.Errorf("name %q should be accepted, got %v", name, err)
			}
		})
	}
}

// TestParsePayload_RejectsZipSlip — end-to-end check that a ZIP
// with a traversal entry is refused at load time. This is the
// primary defense — even if ExtractToDir is bypassed (e.g. a future
// caller iterates Bundle.Sources directly and writes wherever), the
// Bundle never gets constructed in the first place.
func TestParsePayload_RejectsZipSlip(t *testing.T) {
	payload := buildPayloadWithEntries(t, map[string][]byte{
		"mar.json":              []byte(`{"name":"x"}`),
		"src/../../../evil.txt": []byte("owned"),
	})
	_, err := parsePayload(payload)
	if err == nil {
		t.Fatal("parsePayload should reject ZIP with traversal entry")
	}
	if !strings.Contains(err.Error(), "rejected entry") {
		t.Errorf("error %q should mention 'rejected entry'", err)
	}
}

// TestParsePayload_RejectsAbsolutePath — another zip-slip vector
// (entry name starts with /).
func TestParsePayload_RejectsAbsolutePath(t *testing.T) {
	payload := buildPayloadWithEntries(t, map[string][]byte{
		"mar.json":      []byte(`{"name":"x"}`),
		"/etc/cron.d/x": []byte("owned"),
	})
	_, err := parsePayload(payload)
	if err == nil {
		t.Fatal("parsePayload should reject ZIP with absolute-path entry")
	}
}

// TestParsePayload_RejectsForeignTopLevel — entries that aren't
// mar.json and don't live under src/ are tampering signals, not
// silently ignored.
func TestParsePayload_RejectsForeignTopLevel(t *testing.T) {
	payload := buildPayloadWithEntries(t, map[string][]byte{
		"mar.json":     []byte(`{"name":"x"}`),
		"src/Main.mar": []byte("main = 1"),
		"README":       []byte("hi"),
	})
	_, err := parsePayload(payload)
	if err == nil {
		t.Fatal("parsePayload should reject foreign top-level entry")
	}
}

// TestParsePayload_AcceptsLegitimateBundle — sanity that the fix
// didn't break valid bundles. We exercise the full BuildPayload →
// parsePayload roundtrip used by `mar build` / `mar-runtime`.
func TestParsePayload_AcceptsLegitimateBundle(t *testing.T) {
	payload, err := BuildPayload(BuildInput{
		ManifestJSON: []byte(`{"name":"hello"}`),
		Sources: map[string][]byte{
			"Main.mar":            []byte("main = 1"),
			"Frontend/SignIn.mar": []byte("page = 2"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := parsePayload(payload)
	if err != nil {
		t.Fatalf("legitimate bundle rejected: %v", err)
	}
	if len(b.Sources) != 2 {
		t.Errorf("expected 2 sources, got %d", len(b.Sources))
	}
	if string(b.ManifestJSON) != `{"name":"hello"}` {
		t.Errorf("manifest contents wrong: %q", b.ManifestJSON)
	}
}

// TestExtractToDir_DefenseInDepth — even if a caller hand-builds a
// Bundle skipping parsePayload (tests, future programmatic uses),
// ExtractToDir must still refuse to write outside destDir. This
// guards against the safeguards being shifted around in the future
// in a way that drops parsePayload's rejection.
func TestExtractToDir_DefenseInDepth(t *testing.T) {
	destDir := t.TempDir()
	// "..\/canary" would resolve to <parent-of-destDir>/canary.
	bundle := &Bundle{
		ManifestJSON: []byte(`{"name":"x"}`),
		Sources: map[string][]byte{
			"../canary": []byte("written outside destDir"),
		},
	}
	err := ExtractToDir(bundle, destDir)
	if err == nil {
		t.Fatal("ExtractToDir should refuse to write outside destDir")
	}
	// And nothing should have been written outside.
	canary := filepath.Join(filepath.Dir(destDir), "canary")
	if _, statErr := os.Stat(canary); statErr == nil {
		t.Errorf("file %s exists — extraction escaped destDir despite the error return", canary)
		_ = os.Remove(canary)
	}
}

// TestExtractToDir_Legit — happy path: a normal Bundle materializes
// the expected files in destDir.
func TestExtractToDir_Legit(t *testing.T) {
	destDir := t.TempDir()
	bundle := &Bundle{
		ManifestJSON: []byte(`{"name":"app"}`),
		Sources: map[string][]byte{
			"Main.mar":          []byte("main = 1"),
			"Backend/Notes.mar": []byte("notes = []"),
			"Frontend/Home.mar": []byte("home = ()"),
		},
	}
	if err := ExtractToDir(bundle, destDir); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"mar.json",
		"Main.mar",
		filepath.Join("Backend", "Notes.mar"),
		filepath.Join("Frontend", "Home.mar"),
	} {
		full := filepath.Join(destDir, want)
		if _, err := os.Stat(full); err != nil {
			t.Errorf("expected %s after extract, got %v", full, err)
		}
	}
}

// TestLoadReaderAt_FullRoundtrip — sanity that WriteExecutable + the
// trailing-footer probe still work end-to-end. This protects against
// the fix accidentally breaking the production code path that
// mar-runtime walks on startup.
func TestLoadReaderAt_FullRoundtrip(t *testing.T) {
	stub := []byte("stub-bytes-placeholder")
	payload, err := BuildPayload(BuildInput{
		ManifestJSON: []byte(`{"name":"rt"}`),
		Sources:      map[string][]byte{"Main.mar": []byte("main = 1")},
	})
	if err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(t.TempDir(), "fake-binary")
	if err := WriteExecutable(stub, payload, outPath); err != nil {
		t.Fatal(err)
	}
	b, err := LoadExecutable(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(b.ManifestJSON) != `{"name":"rt"}` {
		t.Errorf("manifest mismatch after roundtrip: %q", b.ManifestJSON)
	}
	if string(b.Sources["Main.mar"]) != "main = 1" {
		t.Errorf("source mismatch after roundtrip: %q", b.Sources["Main.mar"])
	}
}
