// Package appbundle packages a mar project as a payload appended to a
// runtime stub executable. The resulting file is self-extracting: when
// run, the binary reads its own bytes, locates the trailing footer
// (magic + offsets), and unpacks the embedded ZIP that contains the
// project source + manifest + frontend assets.
//
// Layout of a built executable:
//
//	[ runtime stub bytes      ]   ← cmd/mar-runtime cross-compiled
//	[ ZIP payload bytes       ]   ← project files + mar.json + assets
//	[ footer (magic, offsets) ]   ← MARBNDL1 + stubSize + payloadSize
package appbundle

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// Filenames inside the ZIP payload.
	manifestFile = "mar.json"
	sourceDir    = "src/"

	// Footer marker so we can detect a stamped binary at startup.
	footerMagic = "MARBNDL2"
	footerSize  = len(footerMagic) + 8 + 8 // magic + stubSize + payloadSize
)

// fixedTimestamp keeps ZIP outputs reproducible across builds.
var fixedTimestamp = time.Unix(0, 0).UTC()

// Bundle is the parsed view of an embedded app: project source files
// (path -> bytes) keyed by their original path relative to the project
// root, plus mar.json bytes for runtime config.
type Bundle struct {
	ManifestJSON []byte
	Sources      map[string][]byte // relative path -> .mar source bytes
}

// BuildInput collects everything BuildPayload needs to produce the ZIP.
type BuildInput struct {
	ManifestJSON []byte            // raw mar.json bytes
	Sources      map[string][]byte // relative path -> .mar source bytes
}

// BuildPayload zips a project source tree + mar.json into the byte
// stream that gets appended to the runtime stub.
func BuildPayload(input BuildInput) ([]byte, error) {
	if len(input.Sources) == 0 {
		return nil, errors.New("appbundle: no source files to bundle")
	}
	if len(input.ManifestJSON) == 0 {
		return nil, errors.New("appbundle: manifest is empty")
	}

	buf := &bytes.Buffer{}
	w := zip.NewWriter(buf)

	if err := addZipFile(w, manifestFile, input.ManifestJSON); err != nil {
		return nil, err
	}
	// Write sources in a stable order so identical inputs produce
	// identical ZIPs (helpful for cache + reproducible builds).
	names := make([]string, 0, len(input.Sources))
	for name := range input.Sources {
		names = append(names, name)
	}
	sortStrings(names)
	for _, name := range names {
		if err := addZipFile(w, sourceDir+name, input.Sources[name]); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WriteExecutable concatenates a runtime stub, a payload, and a footer
// into a single self-extracting file at outputPath, with executable
// permissions.
func WriteExecutable(stub, payload []byte, outputPath string) error {
	if len(stub) == 0 {
		return errors.New("appbundle: stub is empty")
	}
	if len(payload) == 0 {
		return errors.New("appbundle: payload is empty")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(stub); err != nil {
		return err
	}
	if _, err := f.Write(payload); err != nil {
		return err
	}
	footer := make([]byte, footerSize)
	copy(footer, []byte(footerMagic))
	binary.BigEndian.PutUint64(footer[len(footerMagic):len(footerMagic)+8], uint64(len(stub)))
	binary.BigEndian.PutUint64(footer[len(footerMagic)+8:], uint64(len(payload)))
	if _, err := f.Write(footer); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(outputPath, 0o755)
}

// LoadExecutable opens the file at path, looks for the trailing footer,
// and extracts the embedded Bundle. Returns ErrNoBundle if the binary
// wasn't stamped (so callers can fall through to "no embedded program"
// behaviour when running mar-runtime stand-alone).
func LoadExecutable(path string) (*Bundle, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return LoadReaderAt(f, info.Size())
}

// LoadReaderAt is the lower-level entry point. Reads the footer from the
// last footerSize bytes, validates magic + offsets, then extracts the
// payload and parses it as a ZIP.
func LoadReaderAt(r io.ReaderAt, size int64) (*Bundle, error) {
	if size < int64(footerSize) {
		return nil, ErrNoBundle
	}
	footer := make([]byte, footerSize)
	if _, err := r.ReadAt(footer, size-int64(footerSize)); err != nil {
		return nil, err
	}
	if string(footer[:len(footerMagic)]) != footerMagic {
		return nil, ErrNoBundle
	}
	stubSize := int64(binary.BigEndian.Uint64(footer[len(footerMagic) : len(footerMagic)+8]))
	payloadSize := int64(binary.BigEndian.Uint64(footer[len(footerMagic)+8:]))
	if stubSize < 0 || payloadSize <= 0 {
		return nil, errors.New("appbundle: invalid footer offsets")
	}
	if stubSize+payloadSize+int64(footerSize) != size {
		return nil, errors.New("appbundle: footer size mismatch — corrupted binary?")
	}

	payload := make([]byte, payloadSize)
	if _, err := r.ReadAt(payload, stubSize); err != nil {
		return nil, err
	}
	return parsePayload(payload)
}

// parsePayload decompresses the ZIP and assembles a Bundle.
func parsePayload(payload []byte) (*Bundle, error) {
	zr, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return nil, fmt.Errorf("appbundle: open zip: %w", err)
	}
	bundle := &Bundle{Sources: map[string][]byte{}}
	for _, file := range zr.File {
		data, err := readZipEntry(file)
		if err != nil {
			return nil, fmt.Errorf("appbundle: read %s: %w", file.Name, err)
		}
		switch {
		case file.Name == manifestFile:
			bundle.ManifestJSON = data
		case strings.HasPrefix(file.Name, sourceDir):
			rel := strings.TrimPrefix(file.Name, sourceDir)
			bundle.Sources[rel] = data
		}
	}
	if len(bundle.Sources) == 0 {
		return nil, errors.New("appbundle: payload contains no source files")
	}
	return bundle, nil
}

// ErrNoBundle is returned by Load* when the binary doesn't carry a
// stamped payload (e.g. running cmd/mar-runtime directly during dev).
var ErrNoBundle = errors.New("appbundle: no embedded payload (run-as-CLI mode)")

// CollectSources walks projectDir looking for .mar files (recursive)
// and returns them keyed by their path relative to projectDir.
func CollectSources(projectDir string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.WalkDir(projectDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".mar") {
			return nil
		}
		rel, err := filepath.Rel(projectDir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("appbundle: no .mar files under %s", projectDir)
	}
	return out, nil
}

// ExtractToDir writes a Bundle's sources + manifest into destDir as
// regular files. Used by mar-runtime to materialize the project on disk
// so the existing project loader (which reads from filesystem) can take
// over without further changes.
func ExtractToDir(b *Bundle, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(destDir, manifestFile), b.ManifestJSON, 0o644); err != nil {
		return err
	}
	for rel, data := range b.Sources {
		dest := filepath.Join(destDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// addZipFile writes a single file into the ZIP with a fixed timestamp
// (so identical inputs produce byte-identical ZIPs).
func addZipFile(w *zip.Writer, name string, data []byte) error {
	hdr := &zip.FileHeader{
		Name:     name,
		Method:   zip.Deflate,
		Modified: fixedTimestamp,
	}
	wr, err := w.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = wr.Write(data)
	return err
}

func readZipEntry(file *zip.File) ([]byte, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}
