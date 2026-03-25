package runtime

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestPrintStartupErrorSuggestsMakingNewRequiredFieldOptional(t *testing.T) {
	original := os.Stderr
	reader, writer, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("pipe failed: %v", pipeErr)
	}
	os.Stderr = writer
	t.Cleanup(func() {
		os.Stderr = original
	})

	PrintStartupError(
		assertAsError(`migration blocked for entity Todo: cannot auto-add required field "posixTest" (Posix) to existing table todos`),
		"",
	)

	_ = writer.Close()
	var buf bytes.Buffer
	if _, copyErr := io.Copy(&buf, reader); copyErr != nil {
		t.Fatalf("copy failed: %v", copyErr)
	}

	output := buf.String()
	if !strings.Contains(output, "posixTest: Posix optional") {
		t.Fatalf("expected optional example in hint, got:\n%s", output)
	}
	if !strings.Contains(output, "posixTest: Posix default 0") {
		t.Fatalf("expected default example in hint, got:\n%s", output)
	}
}

func TestPrintStartupErrorSuggestsHumanStringDefault(t *testing.T) {
	original := os.Stderr
	reader, writer, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("pipe failed: %v", pipeErr)
	}
	os.Stderr = writer
	t.Cleanup(func() {
		os.Stderr = original
	})

	PrintStartupError(
		assertAsError(`migration blocked for entity User: cannot auto-add required field "name" (String) to existing table users`),
		"",
	)

	_ = writer.Close()
	var buf bytes.Buffer
	if _, copyErr := io.Copy(&buf, reader); copyErr != nil {
		t.Fatalf("copy failed: %v", copyErr)
	}

	output := buf.String()
	if !strings.Contains(output, `name: String default "Unknown"`) {
		t.Fatalf("expected human string default example in hint, got:\n%s", output)
	}
}

func assertAsError(message string) error {
	return startupErrorString(message)
}

type startupErrorString string

func (s startupErrorString) Error() string {
	return string(s)
}
