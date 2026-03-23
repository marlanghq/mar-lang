//go:build windows

package cli

import (
	"fmt"
	"os"
	"strings"
)

type marEditor struct{}

func runEdit(binaryName string, args []string) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return editUsageError(binaryName)
	}
	return initCLIError(
		"Editor not supported on Windows",
		"mar edit is currently available only on macOS and Linux terminals.",
		"Edit your app.mar file in VSCode or another editor instead.",
	)
}

func editUsageError(binaryName string) error {
	useColor := cliSupportsANSIStream(os.Stderr)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;31m", "Edit usage"))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s edit <app.mar>", binaryName))
	fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
	fmt.Fprintf(&b, "  Open a Mar file in the terminal with: %s\n", colorizeCLI(useColor, "\033[1;32m", fmt.Sprintf("%s edit app.mar", binaryName)))
	return styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
}
