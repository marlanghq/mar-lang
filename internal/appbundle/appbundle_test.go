package appbundle

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildAndLoadExecutableRoundTrip(t *testing.T) {
	publicDir := filepath.Join(t.TempDir(), "public")
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(publicDir, "index.html"), []byte("public"), 0o644); err != nil {
		t.Fatal(err)
	}

	payload, err := BuildPayload(BuildInput{
		ManifestJSON: []byte(`{"appName":"TodoApi","port":4100,"database":"todo.db","entities":[]}`),
		Metadata: Metadata{
			MarVersion:   "v1.2.3",
			MarCommit:    "abc123",
			MarBuildTime: "2026-03-07T00:00:00Z",
			AppBuildTime: "2026-03-07T01:00:00Z",
			ManifestHash: "sha256:test-manifest-hash",
		},
		AppUIFiles: map[string][]byte{
			"index.html":  []byte("<html></html>"),
			"favicon.svg": []byte("<svg/>"),
			"dist/app.js": []byte("console.log('ok')"),
		},
		PublicDir: publicDir,
	})
	if err != nil {
		t.Fatalf("build payload failed: %v", err)
	}

	outputPath := filepath.Join(t.TempDir(), "todo")
	if err := WriteExecutable([]byte("stub-bytes"), payload, outputPath, true); err != nil {
		t.Fatalf("write executable failed: %v", err)
	}

	bundle, err := LoadExecutable(outputPath)
	if err != nil {
		t.Fatalf("load executable failed: %v", err)
	}

	if bundle.App.AppName != "TodoApi" {
		t.Fatalf("unexpected app name: %q", bundle.App.AppName)
	}
	if bundle.Metadata.ManifestHash != "sha256:test-manifest-hash" {
		t.Fatalf("unexpected manifest hash: %q", bundle.Metadata.ManifestHash)
	}

	appUIFS, err := fs.Sub(bundle.Archive, "ui")
	if err != nil {
		t.Fatalf("sub app ui fs failed: %v", err)
	}
	indexHTML, err := fs.ReadFile(appUIFS, "index.html")
	if err != nil {
		t.Fatalf("read app ui index failed: %v", err)
	}
	if string(indexHTML) != "<html></html>" {
		t.Fatalf("unexpected app ui index: %q", string(indexHTML))
	}
}
