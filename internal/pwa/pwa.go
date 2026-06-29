// Package pwa builds the Web App Manifest and app icons that make a
// Mar App.frontend installable as a PWA (iOS / Android / desktop).
//
// It's web-target infrastructure: the same Config drives both `mar dev`
// (which serves /_mar/manifest.json + /_mar/icon-*.png live) and
// `mar build` (which writes those files into dist/). No external
// dependencies — icon resizing uses a stdlib box-average downscale,
// which is plenty for the large→small ratios app icons need.
package pwa

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
)

// IconSizes are the PNG icon dimensions the manifest + apple-touch-icon
// reference. 180 = apple-touch-icon; 192 + 512 = Web App Manifest.
var IconSizes = []int{180, 192, 512}

// FaviconSize is the dimension served at /favicon.ico — small enough to
// be a crisp tab favicon, large enough for high-DPI tabs (browsers
// downscale to 16/32 as needed).
const FaviconSize = 48

// Config is the resolved PWA configuration (defaults already applied).
// IconPath is the absolute path to the user's master icon, or "" to
// generate a solid themeColor tile.
type Config struct {
	Name            string
	ShortName       string
	ThemeColor      string // #rrggbb
	BackgroundColor string // #rrggbb
	IconPath        string // absolute path, or "" → generated
}

// ManifestJSON builds the Web App Manifest bytes for c. start_url and
// scope are "/" (Mar apps are single-origin SPAs); display is
// standalone so the installed app opens without browser chrome.
func ManifestJSON(c Config) []byte {
	type icon struct {
		Src     string `json:"src"`
		Sizes   string `json:"sizes"`
		Type    string `json:"type"`
		Purpose string `json:"purpose,omitempty"`
	}
	icons := make([]icon, 0, len(IconSizes))
	for _, s := range IconSizes {
		icons = append(icons, icon{
			Src:     fmt.Sprintf("/_mar/icon-%d.png", s),
			Sizes:   fmt.Sprintf("%dx%d", s, s),
			Type:    "image/png",
			Purpose: "any",
		})
	}
	m := map[string]any{
		"name":             c.Name,
		"short_name":       c.ShortName,
		"start_url":        "/",
		"scope":            "/",
		"display":          "standalone",
		"theme_color":      c.ThemeColor,
		"background_color": c.BackgroundColor,
		"icons":            icons,
	}
	// Indented for human-readability when inspected in devtools / dist.
	b, _ := json.MarshalIndent(m, "", "  ")
	return b
}

// IconPNG returns the app icon at size×size as PNG bytes. When
// c.IconPath is set, the master image is loaded and downscaled;
// otherwise a solid themeColor square is generated.
func IconPNG(c Config, size int) ([]byte, error) {
	var src image.Image
	if c.IconPath != "" {
		f, err := os.Open(c.IconPath)
		if err != nil {
			return nil, fmt.Errorf("open pwa icon: %w", err)
		}
		defer f.Close()
		img, _, err := image.Decode(f)
		if err != nil {
			return nil, fmt.Errorf("decode pwa icon %s: %w", c.IconPath, err)
		}
		src = img
	} else {
		src = solidImage(512, mustColor(c.ThemeColor))
	}
	out := resizeBox(src, size, size)
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// solidImage returns a size×size opaque image filled with col.
func solidImage(size int, col color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.SetRGBA(x, y, col)
		}
	}
	return img
}

// resizeBox downscales (or copies) src into a w×h RGBA image by
// averaging every source pixel that maps into each destination pixel
// (box filter). Good quality for the large→small ratios icons use, and
// dependency-free. Averages in premultiplied-alpha space (what RGBA()
// returns), which is correct for compositing.
func resizeBox(src image.Image, w, h int) *image.RGBA {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if sw == 0 || sh == 0 {
		return dst
	}
	for dy := 0; dy < h; dy++ {
		sy0 := sb.Min.Y + dy*sh/h
		sy1 := sb.Min.Y + (dy+1)*sh/h
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for dx := 0; dx < w; dx++ {
			sx0 := sb.Min.X + dx*sw/w
			sx1 := sb.Min.X + (dx+1)*sw/w
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var rs, gs, bs, as, n uint64
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					r, g, b, a := src.At(sx, sy).RGBA() // 16-bit premultiplied
					rs += uint64(r)
					gs += uint64(g)
					bs += uint64(b)
					as += uint64(a)
					n++
				}
			}
			if n == 0 {
				n = 1
			}
			dst.SetRGBA64(dx, dy, color.RGBA64{
				R: uint16(rs / n), G: uint16(gs / n),
				B: uint16(bs / n), A: uint16(as / n),
			})
		}
	}
	return dst
}

// ParseHexColor parses "#rrggbb" (or "#rgb") into an opaque RGBA.
// Returns ok=false on malformed input so callers can validate.
func ParseHexColor(s string) (color.RGBA, bool) {
	if len(s) == 4 && s[0] == '#' { // #rgb → #rrggbb
		s = "#" + string([]byte{s[1], s[1], s[2], s[2], s[3], s[3]})
	}
	if len(s) != 7 || s[0] != '#' {
		return color.RGBA{}, false
	}
	var r, g, b uint8
	if _, err := fmt.Sscanf(s[1:], "%02x%02x%02x", &r, &g, &b); err != nil {
		return color.RGBA{}, false
	}
	return color.RGBA{R: r, G: g, B: b, A: 255}, true
}

// mustColor parses a hex color, defaulting to white on bad input
// (validation has already rejected bad colors before we render).
func mustColor(s string) color.RGBA {
	if c, ok := ParseHexColor(s); ok {
		return c
	}
	return color.RGBA{R: 255, G: 255, B: 255, A: 255}
}
