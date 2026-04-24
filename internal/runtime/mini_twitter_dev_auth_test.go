package runtime

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/parser"
)

func TestMiniTwitterExamplePrintsLoginCodeToConsoleInDevMode(t *testing.T) {
	requireSQLite3(t)
	t.Setenv("MAR_DEV_MODE", "1")

	sourcePath := filepath.Join("..", "..", "examples", "mini-twitter.mar")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read mini-twitter example failed: %v", err)
	}

	app, err := parser.Parse(string(source))
	if err != nil {
		t.Fatalf("parse mini-twitter example failed: %v", err)
	}
	app.Database = filepath.Join(t.TempDir(), "mini-twitter.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}
	defer r.Close()

	originalStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe failed: %v", err)
	}
	os.Stdout = writer
	t.Cleanup(func() {
		os.Stdout = originalStdout
	})

	rec := doRuntimeRequest(r, http.MethodPost, "/auth/request-code", `{"email":"dev@example.com"}`, "")

	_ = writer.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, reader); err != nil {
		t.Fatalf("copy stdout failed: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from request-code, got %d body=%s", rec.Code, rec.Body.String())
	}

	output := buf.String()
	if !strings.Contains(output, "Auth logs") {
		t.Fatalf("expected auth log header in stdout, got:\n%s", output)
	}
	if !strings.Contains(output, "Transport") || !strings.Contains(output, "console") {
		t.Fatalf("expected console transport log in stdout, got:\n%s", output)
	}
	if !strings.Contains(output, "dev@example.com") {
		t.Fatalf("expected recipient email in stdout, got:\n%s", output)
	}
	if !strings.Contains(output, "Your login code is:") {
		t.Fatalf("expected login code body in stdout, got:\n%s", output)
	}
}
