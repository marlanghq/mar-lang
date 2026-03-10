package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestUnknownCommandErrorSuggestsDevForMarFile(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	err := unknownCommandError("mar", "examples/store.mar")
	if err == nil {
		t.Fatal("expected unknownCommandError to return an error")
	}

	msg := err.Error()
	if !strings.Contains(msg, `unknown command "examples/store.mar"`) {
		t.Fatalf("expected unknown command message, got %q", msg)
	}
	if !strings.Contains(msg, "Looks like you want to open this .mar app in development mode.") {
		t.Fatalf("expected friendly .mar hint, got %q", msg)
	}
	if !strings.Contains(msg, "Try: mar dev examples/store.mar") {
		t.Fatalf("expected dev command hint, got %q", msg)
	}
}

func TestFormatParseCLIErrorMovesSuggestionIntoHintBlock(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	err := formatParseCLIError(errors.New(`action placeBookOrder field OrderItem.unitPrice: references unknown input field "unitPreco". Did you mean "unitPrice"?`))
	if err == nil {
		t.Fatal("expected formatted parse error")
	}

	msg := err.Error()
	if !strings.Contains(msg, "Parse error") {
		t.Fatalf("expected parse error title, got %q", msg)
	}
	if !strings.Contains(msg, `references unknown input field "unitPreco".`) {
		t.Fatalf("expected base parse message, got %q", msg)
	}
	if !strings.Contains(msg, "Hint:\n  Did you mean \"unitPrice\"?") {
		t.Fatalf("expected hint block, got %q", msg)
	}
	if !strings.HasSuffix(msg, "\n") {
		t.Fatalf("expected parse error message to end with a single newline, got %q", msg)
	}
	if strings.HasSuffix(msg, "\n\n") {
		t.Fatalf("expected parse error message to avoid double trailing blank lines, got %q", msg)
	}
}

func TestHighlightParseCLIMessageColorsUnknownInputFieldInRed(t *testing.T) {
	msg := highlightParseCLIMessage(true, `action placeBookOrder field OrderItem.unitPrice: references unknown input field "unitPrico". Did you mean "unitPrice"?`)

	if !strings.Contains(msg, "\033[1;31m\"unitPrico\"\033[0m") {
		t.Fatalf("expected unknown input field token to be red, got %q", msg)
	}
	if !strings.Contains(msg, "\033[1;32m\"unitPrice\"\033[0m") {
		t.Fatalf("expected suggested token to remain green, got %q", msg)
	}
}
