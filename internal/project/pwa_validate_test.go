package project

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePWAIcon(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "ok.png"), 512, 512)
	writePNG(t, filepath.Join(dir, "big.png"), 1000, 1000)
	writePNG(t, filepath.Join(dir, "small.png"), 256, 256)
	writePNG(t, filepath.Join(dir, "wide.png"), 600, 400)
	os.WriteFile(filepath.Join(dir, "fake.png"), []byte("not a png"), 0o644)

	cases := []struct {
		name    string
		icon    string
		wantErr bool
	}{
		{"512 square", "ok.png", false},
		{"1000 square", "big.png", false}, // floor, not whitelist
		{"too small", "small.png", true},
		{"non-square", "wide.png", true},
		{"not png", "fake.png", true},
		{"missing", "nope.png", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{Name: "X", PWA: &PWAConfig{Icon: tc.icon}}
			err := ValidatePWAIcon(dir, m)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidatePWAIcon(%s) err=%v, wantErr=%v", tc.icon, err, tc.wantErr)
			}
		})
	}

	// No PWA block / no icon → always fine (a tile is generated).
	if err := ValidatePWAIcon(dir, &Manifest{Name: "X"}); err != nil {
		t.Errorf("nil PWA block should validate: %v", err)
	}
	if err := ValidatePWAIcon(dir, nil); err != nil {
		t.Errorf("nil manifest should validate: %v", err)
	}
}

func TestValidatePWAColors(t *testing.T) {
	bad := &Manifest{Name: "X", PWA: &PWAConfig{ThemeColor: "blue"}}
	if err := Validate(bad, CompileTime); err == nil {
		t.Error("expected error for non-hex themeColor")
	}
	good := &Manifest{Name: "X", PWA: &PWAConfig{ThemeColor: "#0071e3", BackgroundColor: "#fff"}}
	if err := Validate(good, CompileTime); err != nil {
		t.Errorf("valid hex colors rejected: %v", err)
	}
}

func TestResolvePWADefaults(t *testing.T) {
	// No block → name-derived shortName, white colors, no icon.
	c := (&Manifest{Name: "Daily Checklist"}).ResolvePWA("/proj")
	if c.ShortName != "Daily Checklist" || c.ThemeColor != "#ffffff" || c.IconPath != "" {
		t.Errorf("defaults wrong: %+v", c)
	}
	// Relative icon path resolves against projectDir.
	c2 := (&Manifest{Name: "X", PWA: &PWAConfig{Icon: "icon.png", ShortName: "X!"}}).ResolvePWA("/proj")
	if c2.IconPath != filepath.Join("/proj", "icon.png") || c2.ShortName != "X!" {
		t.Errorf("resolve wrong: %+v", c2)
	}
}
