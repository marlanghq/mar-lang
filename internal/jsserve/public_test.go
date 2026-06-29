package jsserve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReservedPublicPath(t *testing.T) {
	cases := []struct {
		rel      string
		reserved bool
	}{
		// Generated bundle files — would overwrite the real ones.
		{"index.html", true},
		{"runtime.js", true},
		{"program.json", true},
		{"_headers", true},
		// Server route prefixes — owned by the runtime.
		{"api/notes.json", true},
		{"_mar/x", true},
		{"_auth/logo.png", true},
		{"services/y", true},
		{filepath.Join("api", "deep", "z.txt"), true},
		// Fine — ordinary assets.
		{"logo.png", false},
		{"showcase-sample.png", false},
		{filepath.Join("img", "a.jpg"), false},
		{"apiary.png", false},  // not the "api" segment
		{"index.htmlx", false}, // not exactly index.html
		{"_headersfoo", false}, // not exactly _headers
	}
	for _, tc := range cases {
		got := ReservedPublicPath(tc.rel) != ""
		if got != tc.reserved {
			t.Errorf("ReservedPublicPath(%q) reserved=%v, want %v", tc.rel, got, tc.reserved)
		}
	}
}

// ValidatePublicDir must reject the same collisions `mar build` rejects, so
// `mar dev` fails fast instead of silently shadowing the asset.
func TestValidatePublicDirRejectsReserved(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "api", "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := ValidatePublicDir(dir)
	if err == nil {
		t.Fatal("expected an error for public/api/x.txt, got nil")
	}
	if !strings.Contains(err.Error(), "api") {
		t.Fatalf("error should name the offending path: %v", err)
	}
}

func TestValidatePublicDirAcceptsCleanTreeAndMissingDir(t *testing.T) {
	// A clean tree with a dotfile passes (dotfiles are skipped).
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "img"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"logo.png", filepath.Join("img", "a.jpg"), ".DS_Store"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidatePublicDir(dir); err != nil {
		t.Fatalf("clean tree rejected: %v", err)
	}
	// A missing folder (or "") is not an error.
	if err := ValidatePublicDir(filepath.Join(dir, "does-not-exist")); err != nil {
		t.Fatalf("missing dir should be ok: %v", err)
	}
	if err := ValidatePublicDir(""); err != nil {
		t.Fatalf("empty dir should be ok: %v", err)
	}
}
