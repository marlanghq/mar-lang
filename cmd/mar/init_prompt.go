package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"mar/internal/scaffold"
)

// promptInitKind asks the operator which scaffold to generate. Three
// choices: fullstack (default), frontend-only, backend-only. Always
// prompts — `mar init` is a developer command, not a CI command, so
// there's no non-interactive caller to accommodate. Bare Enter (or
// an unrecognised input) keeps the default.
//
// Visual matches the rest of the CLI: yellow "1.", "2.", "3."
// numbering, default highlighted with "(default)" in dim text, then
// a prompt that defaults to 1 on bare Enter.
func promptInitKind() scaffold.Kind {
	fmt.Println()
	fmt.Println("Mar will scaffold a starter project so you have something running")
	fmt.Println("right away. Everything is editable after. Pick a shape to begin:")
	fmt.Println()
	fmt.Printf("  %s fullstack with auth %s\n",
		colorYellow("1."),
		colorDim("(default)"))
	fmt.Printf("  %s fullstack\n", colorYellow("2."))
	fmt.Printf("  %s frontend-only\n", colorYellow("3."))
	fmt.Printf("  %s backend-only\n", colorYellow("4."))
	fmt.Printf("  %s minimum\n", colorYellow("5."))
	fmt.Println()
	fmt.Print("Choose [1]: ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	switch strings.TrimSpace(input) {
	case "2", "fullstack":
		return scaffold.KindFullstack
	case "3", "frontend", "frontend-only":
		return scaffold.KindFrontend
	case "4", "backend", "backend-only":
		return scaffold.KindBackend
	case "5", "minimum", "min":
		return scaffold.KindMinimum
	default:
		return scaffold.KindFullstackAuth
	}
}
