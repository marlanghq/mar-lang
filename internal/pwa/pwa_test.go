package pwa

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"testing"
)

func TestManifestJSON(t *testing.T) {
	c := Config{Name: "Daily Checklist", ShortName: "Checklist", ThemeColor: "#0071e3", BackgroundColor: "#ffffff"}
	var m map[string]any
	if err := json.Unmarshal(ManifestJSON(c), &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if m["name"] != "Daily Checklist" || m["short_name"] != "Checklist" {
		t.Errorf("name/short_name wrong: %v / %v", m["name"], m["short_name"])
	}
	if m["display"] != "standalone" || m["start_url"] != "/" {
		t.Errorf("display/start_url wrong: %v / %v", m["display"], m["start_url"])
	}
	icons, ok := m["icons"].([]any)
	if !ok || len(icons) != len(IconSizes) {
		t.Fatalf("expected %d icons, got %v", len(IconSizes), m["icons"])
	}
}

func TestIconPNGGeneratedSize(t *testing.T) {
	c := Config{Name: "X", ShortName: "X", ThemeColor: "#0071e3", BackgroundColor: "#fff"}
	for _, size := range IconSizes {
		b, err := IconPNG(c, size)
		if err != nil {
			t.Fatalf("IconPNG(%d): %v", size, err)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("decode icon-%d: %v", size, err)
		}
		if cfg.Width != size || cfg.Height != size {
			t.Errorf("icon-%d is %dx%d", size, cfg.Width, cfg.Height)
		}
	}
}

func TestIconPNGResizesMaster(t *testing.T) {
	// A 600x600 master downscales to each target size.
	master := image.NewRGBA(image.Rect(0, 0, 600, 600))
	var buf bytes.Buffer
	if err := png.Encode(&buf, master); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := dir + "/icon.png"
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	c := Config{Name: "X", ShortName: "X", ThemeColor: "#000", BackgroundColor: "#fff", IconPath: path}
	b, err := IconPNG(c, 192)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := png.DecodeConfig(bytes.NewReader(b))
	if cfg.Width != 192 || cfg.Height != 192 {
		t.Errorf("resized icon is %dx%d, want 192x192", cfg.Width, cfg.Height)
	}
}

func TestParseHexColor(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"#0071e3", true}, {"#fff", true}, {"#FFFFFF", true},
		{"0071e3", false}, {"#12", false}, {"#gggggg", false}, {"", false},
	}
	for _, tc := range cases {
		_, ok := ParseHexColor(tc.in)
		if ok != tc.ok {
			t.Errorf("ParseHexColor(%q) ok=%v, want %v", tc.in, ok, tc.ok)
		}
	}
}
