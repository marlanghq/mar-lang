package appbundle

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mar/internal/model"
)

const (
	metadataFile = "metadata.json"
	manifestFile = "manifest.json"
	footerMagic  = "MARBNDL1"
	footerSize   = len(footerMagic) + 8 + 8
)

var zipTimestamp = time.Unix(0, 0).UTC()

type Metadata struct {
	MarVersion   string `json:"marVersion"`
	MarCommit    string `json:"marCommit"`
	MarBuildTime string `json:"marBuildTime"`
	AppBuildTime string `json:"appBuildTime"`
	ManifestHash string `json:"manifestHash"`
}

type BuildInput struct {
	ManifestJSON []byte
	Metadata     Metadata
	AdminFiles   map[string][]byte
	PublicDir    string
}

type Bundle struct {
	App      *model.App
	Metadata Metadata
	Archive  *zip.Reader
}

// BuildPayload packages the manifest, metadata, admin assets, and optional public
// files into the ZIP payload appended to a Mar executable.
func BuildPayload(input BuildInput) ([]byte, error) {
	if len(input.ManifestJSON) == 0 {
		return nil, errors.New("manifest payload is empty")
	}
	if len(input.AdminFiles) == 0 {
		return nil, errors.New("admin assets are required")
	}

	metadataJSON, err := json.Marshal(input.Metadata)
	if err != nil {
		return nil, err
	}

	buf := &bytes.Buffer{}
	writer := zip.NewWriter(buf)

	if err := addZipFile(writer, manifestFile, input.ManifestJSON); err != nil {
		return nil, err
	}
	if err := addZipFile(writer, metadataFile, metadataJSON); err != nil {
		return nil, err
	}
	for name, data := range input.AdminFiles {
		if err := addZipFile(writer, filepath.ToSlash(filepath.Join("admin", name)), data); err != nil {
			return nil, err
		}
	}
	if trimmed := strings.TrimSpace(input.PublicDir); trimmed != "" {
		if err := addZipDir(writer, trimmed, "public"); err != nil {
			return nil, err
		}
	}

	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WriteExecutable writes a runtime stub, app payload, and bundle footer to
// outputPath, optionally marking the result as executable.
func WriteExecutable(stub, payload []byte, outputPath string, executable bool) error {
	if len(stub) == 0 {
		return errors.New("runtime stub is empty")
	}
	if len(payload) == 0 {
		return errors.New("payload is empty")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}

	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := file.Write(stub); err != nil {
		return err
	}
	if _, err := file.Write(payload); err != nil {
		return err
	}

	footer := make([]byte, footerSize)
	copy(footer, []byte(footerMagic))
	binary.BigEndian.PutUint64(footer[len(footerMagic):len(footerMagic)+8], uint64(len(stub)))
	binary.BigEndian.PutUint64(footer[len(footerMagic)+8:], uint64(len(payload)))
	if _, err := file.Write(footer); err != nil {
		return err
	}

	if err := file.Close(); err != nil {
		return err
	}
	if executable {
		return os.Chmod(outputPath, 0o755)
	}
	return nil
}

// LoadExecutable opens path and loads the appended Mar app bundle from it.
func LoadExecutable(path string) (*Bundle, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	return LoadReaderAt(file, info.Size())
}

// LoadReaderAt reads a Mar app bundle from reader using the executable size to
// locate and validate the appended payload footer.
func LoadReaderAt(reader io.ReaderAt, executableSize int64) (*Bundle, error) {
	if executableSize < int64(footerSize) {
		return nil, errors.New("mar app bundle footer not found")
	}

	footer := make([]byte, footerSize)
	if _, err := reader.ReadAt(footer, executableSize-int64(footerSize)); err != nil {
		return nil, err
	}
	if string(footer[:len(footerMagic)]) != footerMagic {
		return nil, errors.New("mar app bundle footer not found")
	}

	offset := int64(binary.BigEndian.Uint64(footer[len(footerMagic) : len(footerMagic)+8]))
	payloadSize := int64(binary.BigEndian.Uint64(footer[len(footerMagic)+8:]))
	if offset < 0 || payloadSize <= 0 {
		return nil, errors.New("mar app bundle footer is invalid")
	}
	if offset+payloadSize+int64(footerSize) != executableSize {
		return nil, errors.New("mar app bundle size mismatch")
	}

	section := io.NewSectionReader(reader, offset, payloadSize)
	payload, err := io.ReadAll(section)
	if err != nil {
		return nil, err
	}
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return nil, err
	}

	manifestJSON, err := readZipFile(archive, manifestFile)
	if err != nil {
		return nil, err
	}
	metadataJSON, err := readZipFile(archive, metadataFile)
	if err != nil {
		return nil, err
	}

	var app model.App
	if err := json.Unmarshal(manifestJSON, &app); err != nil {
		return nil, err
	}

	var metadata Metadata
	if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
		return nil, err
	}

	return &Bundle{
		App:      &app,
		Metadata: metadata,
		Archive:  archive,
	}, nil
}

func addZipDir(writer *zip.Writer, sourceDir, targetRoot string) error {
	return filepath.WalkDir(sourceDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return addZipFile(writer, filepath.ToSlash(filepath.Join(targetRoot, rel)), data)
	})
}

func addZipFile(writer *zip.Writer, name string, data []byte) error {
	header := &zip.FileHeader{
		Name:     filepath.ToSlash(name),
		Method:   zip.Deflate,
		Modified: zipTimestamp,
	}
	header.SetMode(fs.FileMode(0o644))
	entry, err := writer.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = entry.Write(data)
	return err
}

func readZipFile(archive *zip.Reader, name string) ([]byte, error) {
	for _, file := range archive.File {
		if file.Name != name {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		return io.ReadAll(reader)
	}
	return nil, fs.ErrNotExist
}
