package cli

import (
	"errors"
	"io"
	"os"
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

func TestFormatParseCLIErrorAddsHintForMissingAppDeclaration(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	err := formatParseCLIError(errors.New("missing app declaration"))
	if err == nil {
		t.Fatal("expected formatted parse error")
	}

	msg := err.Error()
	if !strings.Contains(msg, "Parse error") {
		t.Fatalf("expected parse error title, got %q", msg)
	}
	if !strings.Contains(msg, "missing app declaration") {
		t.Fatalf("expected base parse message, got %q", msg)
	}
	if !strings.Contains(msg, "Hint:\n  Add an app declaration near the top of the file, for example: app Todo") {
		t.Fatalf("expected missing app declaration hint, got %q", msg)
	}
}

func TestHighlightParseCLIMessageColorsMissingAppDeclaration(t *testing.T) {
	msg := highlightParseCLIMessage(true, "missing app declaration")

	if !strings.Contains(msg, "missing \033[1mapp\033[0m declaration") {
		t.Fatalf("expected missing app declaration to highlight app, got %q", msg)
	}
}

func TestHighlightParseCLIMessageColorsOtherDeclarations(t *testing.T) {
	msg := highlightParseCLIMessage(true, "missing auth declaration and public declaration")

	if !strings.Contains(msg, "missing \033[1mauth\033[0m declaration") {
		t.Fatalf("expected missing auth declaration to highlight auth, got %q", msg)
	}
	if !strings.Contains(msg, "\033[1mpublic\033[0m declaration") {
		t.Fatalf("expected public declaration to highlight public, got %q", msg)
	}
}

func TestHighlightParseCLIMessageColorsAppDeclarationExample(t *testing.T) {
	msg := highlightParseCLIMessage(true, "Add an app declaration near the top of the file, for example: app Todo")

	if !strings.Contains(msg, "an \033[1mapp\033[0m declaration") {
		t.Fatalf("expected app keyword in prose to be bold, got %q", msg)
	}
	if !strings.Contains(msg, "\033[1mapp\033[0m \033[1;36mTodo\033[0m") {
		t.Fatalf("expected app declaration example to color app and Todo separately, got %q", msg)
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

func TestHighlightParseCLIMessageColorsAuthSmtpConfigReference(t *testing.T) {
	msg := highlightParseCLIMessage(true, "auth.smtp_host can only be used when email_transport smtp is selected")

	if !strings.Contains(msg, "\033[1;36mauth.smtp_host\033[0m") {
		t.Fatalf("expected auth.smtp_host to be cyan, got %q", msg)
	}
	if !strings.Contains(msg, "\033[1memail_transport\033[0m \033[1;32msmtp\033[0m") {
		t.Fatalf("expected email_transport smtp to be highlighted, got %q", msg)
	}
}

func TestFlyUsageErrorUsesStyledCLIFormat(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	err := flyUsageError("mar")
	if err == nil {
		t.Fatal("expected fly usage error")
	}

	msg := err.Error()
	if !strings.Contains(msg, "Fly usage") {
		t.Fatalf("expected fly usage title, got %q", msg)
	}
	if !strings.Contains(msg, "mar fly init <app.mar> [fly-app-name]") {
		t.Fatalf("expected fly usage command, got %q", msg)
	}
	if !strings.Contains(msg, "mar fly deploy <app.mar>") {
		t.Fatalf("expected fly deploy usage command, got %q", msg)
	}
	if !strings.Contains(msg, "Hint:\n  Prepare Fly.io deployment files with: mar fly init <app.mar>") {
		t.Fatalf("expected fly usage hint, got %q", msg)
	}
	if !strings.Contains(msg, "Deploy the current app with: mar fly deploy <app.mar>") {
		t.Fatalf("expected fly deploy hint, got %q", msg)
	}
	if !strings.HasSuffix(msg, "\n") {
		t.Fatalf("expected fly usage message to end with newline, got %q", msg)
	}
}

func TestRenderCompletionScriptSupportsZsh(t *testing.T) {
	script, err := renderCompletionScript("mar", "zsh")
	if err != nil {
		t.Fatalf("expected zsh completion script, got error: %v", err)
	}
	if !strings.Contains(script, "compdef _mar mar") {
		t.Fatalf("expected zsh compdef registration, got %q", script)
	}
	if !strings.Contains(script, "fly") || !strings.Contains(script, "completion") {
		t.Fatalf("expected script to include CLI commands, got %q", script)
	}
	if !strings.Contains(script, "fly_commands=(") || !strings.Contains(script, "_describe 'fly command' fly_commands") {
		t.Fatalf("expected zsh completion to describe fly subcommands via a named array, got %q", script)
	}
	if !strings.Contains(script, "shells=(") || !strings.Contains(script, "_describe 'shell' shells") {
		t.Fatalf("expected zsh completion to describe shells via a named array, got %q", script)
	}
	if !strings.Contains(script, "format_check_flags=(") {
		t.Fatalf("expected zsh completion to define format_check_flags, got %q", script)
	}
	if !strings.Contains(script, "_message 'output name'") {
		t.Fatalf("expected zsh completion to describe dev/compile output name, got %q", script)
	}
	if !strings.Contains(script, "--check:Check formatting without writing files") || !strings.Contains(script, "--stdin:Read Mar source from stdin") {
		t.Fatalf("expected zsh completion to include format flags, got %q", script)
	}
}

func TestRenderCompletionScriptRejectsUnsupportedShell(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	_, err := renderCompletionScript("mar", "pwsh")
	if err == nil {
		t.Fatal("expected unsupported shell error")
	}

	msg := err.Error()
	if !strings.Contains(msg, `Unsupported shell "pwsh"`) {
		t.Fatalf("expected unsupported shell message, got %q", msg)
	}
	if !strings.Contains(msg, "Use one of: zsh, bash, fish") {
		t.Fatalf("expected supported shells hint, got %q", msg)
	}
}

func TestRunCompletionPrintsBashScript(t *testing.T) {
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	runErr := runCompletion("mar", []string{"bash"})
	_ = w.Close()
	if runErr != nil {
		t.Fatalf("runCompletion returned error: %v", runErr)
	}

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "complete -F _mar_completion mar") {
		t.Fatalf("expected bash completion output, got %q", got)
	}
	if !strings.Contains(got, "--check --stdin") {
		t.Fatalf("expected bash completion output to include format flags, got %q", got)
	}
	if !strings.Contains(got, `if [[ ${COMP_CWORD} -eq 2 ]]; then`) {
		t.Fatalf("expected bash completion output to limit .mar suggestions to the first dev/compile argument, got %q", got)
	}
}

func TestRenderCompletionScriptSupportsFishFormatFlags(t *testing.T) {
	script, err := renderCompletionScript("mar", "fish")
	if err != nil {
		t.Fatalf("expected fish completion script, got error: %v", err)
	}
	if !strings.Contains(script, "-l check -d 'Check formatting without writing files'") {
		t.Fatalf("expected fish completion to include --check flag, got %q", script)
	}
	if !strings.Contains(script, "-l stdin -d 'Read Mar source from stdin'") {
		t.Fatalf("expected fish completion to include --stdin flag, got %q", script)
	}
}

func TestReadVersionInfoUsesEmbeddedVersion(t *testing.T) {
	originalCommit := cliCommit
	originalBuildTime := cliBuildTime
	t.Cleanup(func() {
		cliCommit = originalCommit
		cliBuildTime = originalBuildTime
	})

	cliCommit = ""
	cliBuildTime = ""

	info := readVersionInfo("mar")
	if info.Version != "0.0.1" {
		t.Fatalf("expected VERSION file version 0.0.1, got %q", info.Version)
	}
}
