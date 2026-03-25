package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	marversion "mar"
	"mar/internal/formatter"
	"mar/internal/lsp"
	"mar/internal/model"
	"mar/internal/parser"
)

var (
	cliCommit    = ""
	cliBuildTime = ""

	parseErrorQuotedTokenRe   = regexp.MustCompile(`"[^"\n]*"`)
	parseErrorUnknownInputRe  = regexp.MustCompile(`unknown input field ("[^"\n]*")`)
	parseErrorActionRe        = regexp.MustCompile(`\baction\s+([A-Za-z_][A-Za-z0-9_]*)`)
	parseErrorFieldRe         = regexp.MustCompile(`\bfield\s+([A-Za-z_][A-Za-z0-9_.]*)`)
	parseErrorDeclWordRe      = regexp.MustCompile(`\b(app|auth|public|system)\s+declaration\b`)
	parseErrorConfigPathRe    = regexp.MustCompile(`\b(auth|system|public)\.([A-Za-z_][A-Za-z0-9_.]*)\b`)
	parseErrorAuthTransportRe = regexp.MustCompile(`\b(email_transport)\s+(smtp|console)\b`)
	appWarningFieldRe         = regexp.MustCompile("`[^`\\n]+`")
)

type styledCLIError string

func (e styledCLIError) Error() string {
	return string(e)
}

func (e styledCLIError) StyledCLI() string {
	return string(e)
}

// Run dispatches Mar CLI subcommands.
func Run(binaryName string, args []string) error {
	if binaryName == "" {
		binaryName = "mar"
	}
	if len(args) == 0 {
		printUsage(binaryName)
		return nil
	}

	switch args[0] {
	case "init":
		return runInit(binaryName, args[1:])
	case "edit":
		return runEdit(binaryName, args[1:])
	case "compile":
		if len(args) != 2 && len(args) != 3 {
			return fmt.Errorf("usage: %s compile <app.mar> [output-name]", binaryName)
		}
		app, err := parseMarFile(args[1])
		if err != nil {
			return err
		}
		printAppWarnings(app)
		buildRoot, binaryOutputName := defaultBuildLayout(args[1], "")
		if len(args) == 3 {
			buildRoot, binaryOutputName = defaultBuildLayout(args[1], args[2])
		}
		return compileExecutablesWithOptions(app, buildRoot, binaryOutputName, buildOptions{
			PrintSummary: true,
			SourcePath:   args[1],
		})
	case "dev":
		if len(args) != 2 && len(args) != 3 {
			return fmt.Errorf("usage: %s dev <app.mar> [output-name]", binaryName)
		}
		outputPath := defaultOutputPath(args[1], "")
		if len(args) == 3 {
			outputPath = defaultOutputPath(args[1], args[2])
		}
		return runDev(binaryName, args[1], outputPath)
	case "fly":
		return runFly(binaryName, args[1:])
	case "completion":
		return runCompletion(binaryName, args[1:])
	case "format":
		return runFormat(binaryName, args[1:])
	case "lsp":
		// Accept optional extra args (e.g. --stdio) for editor/client compatibility.
		return lsp.RunStdio()
	case "version":
		if len(args) != 1 {
			return fmt.Errorf("usage: %s version", binaryName)
		}
		return printVersion(binaryName)
	default:
		return unknownCommandError(binaryName, args[0])
	}
}

type formatOptions struct {
	Check bool
	Stdin bool
	Files []string
}

func runFormat(binaryName string, args []string) error {
	opts, err := parseFormatArgs(binaryName, args)
	if err != nil {
		return err
	}
	if opts.Stdin {
		return runFormatStdin(opts.Check)
	}
	return runFormatFiles(binaryName, opts.Files, opts.Check)
}

func parseFormatArgs(binaryName string, args []string) (*formatOptions, error) {
	opts := &formatOptions{}
	for _, arg := range args {
		switch arg {
		case "--check":
			opts.Check = true
		case "--stdin":
			opts.Stdin = true
		default:
			if strings.HasPrefix(arg, "-") {
				return nil, fmt.Errorf("unknown flag %q\n\nusage: %s format [--check] [--stdin] [files...]", arg, binaryName)
			}
			opts.Files = append(opts.Files, arg)
		}
	}

	if opts.Stdin && len(opts.Files) > 0 {
		return nil, fmt.Errorf("usage: %s format [--check] [--stdin] [files...]\n\nwhen --stdin is set, do not pass file paths", binaryName)
	}
	if !opts.Stdin && len(opts.Files) == 0 {
		return nil, fmt.Errorf("usage: %s format [--check] [--stdin] [files...]\n\npass one or more .mar files, or use --stdin", binaryName)
	}
	return opts, nil
}

func runFormatStdin(check bool) error {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	formatted, err := formatter.Format(string(input))
	if err != nil {
		return err
	}
	if check {
		if formatted != normalizeFormattedCompare(string(input)) {
			return errors.New("stdin is not formatted")
		}
		return nil
	}
	_, err = os.Stdout.Write([]byte(formatted))
	return err
}

func runFormatFiles(binaryName string, files []string, check bool) error {
	changed := make([]string, 0, len(files))
	for _, path := range files {
		if strings.ToLower(filepath.Ext(path)) != ".mar" {
			return fmt.Errorf("format only supports .mar files: %s", path)
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		original := string(raw)
		formatted, err := formatter.Format(original)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if normalizeFormattedCompare(original) == formatted {
			continue
		}
		changed = append(changed, path)
		if !check {
			if err := os.WriteFile(path, []byte(formatted), 0o644); err != nil {
				return err
			}
		}
	}

	if check && len(changed) > 0 {
		sort.Strings(changed)
		var b strings.Builder
		fmt.Fprintf(&b, "the following files are not formatted:\n")
		for _, file := range changed {
			fmt.Fprintf(&b, "  %s\n", file)
		}
		fmt.Fprintf(&b, "\nHint:\n  Run: %s format %s\n", binaryName, strings.Join(changed, " "))
		return errors.New(strings.TrimSpace(b.String()))
	}
	return nil
}

func normalizeFormattedCompare(source string) string {
	s := strings.ReplaceAll(source, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s
}

func parseMarFile(path string) (*model.App, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	app, err := parser.Parse(string(content))
	if err != nil {
		return nil, formatParseCLIError(err)
	}
	return app, nil
}

func printAppWarnings(app *model.App) {
	if app == nil || len(app.Warnings) == 0 {
		return
	}

	useColor := cliSupportsANSIStream(os.Stdout)
	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1;33m", "Warning"))
	for _, warning := range app.Warnings {
		warning = strings.TrimSpace(warning)
		if warning == "" {
			continue
		}
		for _, line := range strings.Split(warning, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			fmt.Printf("  %s\n", highlightAppWarning(useColor, line))
		}
	}
	fmt.Println()
}

func highlightAppWarning(useColor bool, message string) string {
	if !useColor {
		return strings.ReplaceAll(message, "`", "")
	}

	return appWarningFieldRe.ReplaceAllStringFunc(message, func(match string) string {
		trimmed := strings.Trim(match, "`")
		return colorizeCLI(true, "\033[1;36m", trimmed)
	})
}

func formatParseCLIError(err error) error {
	useColor := cliSupportsANSIStream(os.Stderr)
	message := strings.TrimSpace(err.Error())
	baseMessage, hint := splitSuggestionHint(message)
	if hint == "" {
		hint = parseCLIHintForMessage(baseMessage)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;31m", "Parse error"))
	fmt.Fprintf(&b, "  %s\n", highlightParseCLIMessage(useColor, baseMessage))
	if hint != "" {
		fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
		fmt.Fprintf(&b, "  %s\n", highlightParseCLIMessage(useColor, hint))
	}
	return styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
}

func parseCLIHintForMessage(message string) string {
	switch strings.TrimSpace(message) {
	case "missing app declaration":
		return "Add an app declaration near the top of the file. Example: app Todo"
	default:
		return ""
	}
}

func splitSuggestionHint(message string) (string, string) {
	marker := ". Did you mean "
	idx := strings.LastIndex(message, marker)
	if idx < 0 {
		return message, ""
	}
	base := strings.TrimSpace(message[:idx+1])
	hint := strings.TrimSpace(message[idx+2:])
	return base, hint
}

func highlightParseCLIMessage(useColor bool, message string) string {
	if !useColor {
		return message
	}

	const unknownInputPlaceholder = "__MAR_UNKNOWN_INPUT__"

	unknownInputToken := ""
	message = parseErrorUnknownInputRe.ReplaceAllStringFunc(message, func(match string) string {
		parts := parseErrorUnknownInputRe.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		unknownInputToken = parts[1]
		return "unknown input field " + unknownInputPlaceholder
	})
	message = parseErrorActionRe.ReplaceAllStringFunc(message, func(match string) string {
		parts := parseErrorActionRe.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		return "action " + colorizeCLI(true, "\033[1;36m", parts[1])
	})
	message = parseErrorDeclWordRe.ReplaceAllStringFunc(message, func(match string) string {
		parts := parseErrorDeclWordRe.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		return colorizeCLI(true, "\033[1m", parts[1]) + " declaration"
	})
	message = parseErrorConfigPathRe.ReplaceAllStringFunc(message, func(match string) string {
		return colorizeCLI(true, "\033[1;36m", match)
	})
	message = parseErrorAuthTransportRe.ReplaceAllStringFunc(message, func(match string) string {
		parts := parseErrorAuthTransportRe.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		return colorizeCLI(true, "\033[1m", parts[1]) + " " + colorizeCLI(true, "\033[1;32m", parts[2])
	})
	message = strings.ReplaceAll(message, "an app declaration", "an "+colorizeCLI(true, "\033[1m", "app")+" declaration")
	message = strings.ReplaceAll(
		message,
		"app Todo",
		colorizeCLI(true, "\033[1m", "app")+" "+colorizeCLI(true, "\033[1;36m", "Todo"),
	)
	message = parseErrorFieldRe.ReplaceAllStringFunc(message, func(match string) string {
		parts := parseErrorFieldRe.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		return "field " + colorizeCLI(true, "\033[1;36m", parts[1])
	})
	message = parseErrorQuotedTokenRe.ReplaceAllStringFunc(message, func(match string) string {
		return colorizeCLI(true, "\033[1;32m", match)
	})
	if unknownInputToken != "" {
		message = strings.Replace(message, unknownInputPlaceholder, colorizeCLI(true, "\033[1;31m", unknownInputToken), 1)
	}
	return message
}

type buildOptions struct {
	PrintSummary bool
	SourcePath   string
}

func defaultBuildLayout(inputPath, fallback string) (string, string) {
	name := ""
	override := strings.TrimSpace(fallback)
	if override != "" {
		name = filepath.Base(override)
		if name == "." || name == "" {
			name = ""
		}
	}
	if name == "" {
		base := filepath.Base(inputPath)
		ext := filepath.Ext(base)
		name = strings.TrimSuffix(base, ext)
		name = filepath.Base(name)
	}
	if name == "" {
		name = "app"
	}
	name = strings.TrimSuffix(name, filepath.Ext(name))
	dirName := name
	if dirName == "" {
		dirName = name
	}
	return filepath.Join("dist", dirName), dirName
}

func defaultOutputPath(inputPath, fallback string) string {
	buildRoot, binaryName := defaultBuildLayout(inputPath, fallback)
	target, err := hostRuntimeTarget()
	if err != nil {
		target = runtimeTarget{OS: runtime.GOOS, Arch: runtime.GOARCH}
	}
	return filepath.Join(buildRoot, targetBinaryName(binaryName, target))
}

func printUsage(binaryName string) {
	useColor := cliSupportsANSIStream(os.Stdout)

	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1;36m", "Available commands"))
	fmt.Printf("  %-45s %s\n", fmt.Sprintf("%s init [project-name]", binaryName), "Create a new Mar project with a starter app.")
	fmt.Printf("  %-45s %s\n", fmt.Sprintf("%s edit <app.mar>", binaryName), "Edit a Mar file directly in the terminal.")
	fmt.Printf("  %-45s %s\n", fmt.Sprintf("%s dev <app.mar> [output-name]", binaryName), "Run development mode with hot reload.")
	fmt.Printf("  %-45s %s\n", fmt.Sprintf("%s compile <app.mar> [output-name]", binaryName), "Compile a .mar app into executables for all supported platforms and generate its frontend clients.")
	fmt.Printf("  %-45s %s\n", fmt.Sprintf("%s fly init <app.mar>", binaryName), "Prepares Fly.io deployment files for your app.")
	fmt.Printf("  %-45s %s\n", fmt.Sprintf("%s fly deploy <app.mar>", binaryName), "Rebuild the Linux executable for Fly.io and run fly deploy.")
	fmt.Printf("  %-45s %s\n", fmt.Sprintf("%s completion <zsh|bash|fish>", binaryName), "Generate shell completion scripts.")
	fmt.Printf("  %-45s %s\n", fmt.Sprintf("%s format [--check] [--stdin] [files...]", binaryName), "Format Mar source files.")
	fmt.Printf("  %-45s %s\n", fmt.Sprintf("%s lsp", binaryName), "Start the Mar Language Server (for editors).")
	fmt.Printf("  %-45s %s\n", fmt.Sprintf("%s version", binaryName), "Show version and build information.")
	fmt.Printf("\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
	fmt.Printf("  Start in development mode with: %s\n", colorizeCLI(useColor, "\033[1;32m", fmt.Sprintf("%s dev <app.mar>", binaryName)))
	fmt.Println()
}

func unknownCommandError(binaryName, provided string) error {
	useColor := cliSupportsANSIStream(os.Stderr)
	var b strings.Builder
	fmt.Fprintf(&b, "%s %q\n\n", colorizeCLI(useColor, "\033[1;31m", "unknown command"), provided)
	fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;36m", "Available commands:"))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s init [project-name]", binaryName))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s edit <app.mar>", binaryName))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s dev <app.mar> [output-name]", binaryName))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s compile <app.mar> [output-name]", binaryName))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s fly init <app.mar>", binaryName))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s fly deploy <app.mar>", binaryName))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s completion <zsh|bash|fish>", binaryName))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s format [--check] [--stdin] [files...]", binaryName))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s lsp", binaryName))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s version", binaryName))
	if looksLikeMarFile(provided) {
		fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
		fmt.Fprintf(&b, "  Looks like you want to open this .mar app in development mode.\n")
		fmt.Fprintf(&b, "  Try: %s\n", colorizeCLI(useColor, "\033[1;32m", fmt.Sprintf("%s dev %s", binaryName, provided)))
	} else {
		fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
		fmt.Fprintf(&b, "  Start in development mode with: %s\n", colorizeCLI(useColor, "\033[1;32m", fmt.Sprintf("%s dev <app.mar>", binaryName)))
	}
	return errors.New(b.String())
}

func looksLikeMarFile(value string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(value)), ".mar")
}

func cliSupportsANSIStream(stream *os.File) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	term := strings.ToLower(strings.TrimSpace(os.Getenv("TERM")))
	if term == "" || term == "dumb" {
		return false
	}
	info, err := stream.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func colorizeCLI(enabled bool, colorCode, value string) string {
	if !enabled {
		return value
	}
	return colorCode + value + "\033[0m"
}

func PrintError(err error) {
	fmt.Fprintln(os.Stderr)
	type styled interface {
		StyledCLI() string
	}
	if styledErr, ok := err.(styled); ok {
		fmt.Fprintln(os.Stderr, styledErr.StyledCLI())
		return
	}
	fmt.Fprintln(os.Stderr, "error:", err)
}

type versionInfo struct {
	Version   string
	Commit    string
	BuildTime string
	GoVersion string
	Platform  string
	Binary    string
}

func printVersion(binaryName string) error {
	useColor := cliSupportsANSIStream(os.Stdout)
	info := readVersionInfo(binaryName)

	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1m", "Mar version"))
	fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;36m", "Version:"), info.Version)
	fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;36m", "Commit:"), info.Commit)
	fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;36m", "Build time:"), info.BuildTime)
	fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;36m", "Go:"), info.GoVersion)
	fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;36m", "Platform:"), info.Platform)
	fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;36m", "Binary:"), info.Binary)
	fmt.Println()

	return nil
}

func readVersionInfo(binaryName string) versionInfo {
	info := versionInfo{
		Version:   marversion.Version(),
		Commit:    "unknown",
		BuildTime: "unknown",
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
		Binary:    binaryName,
	}

	if exe, err := os.Executable(); err == nil && strings.TrimSpace(exe) != "" {
		info.Binary = exe
	}

	if strings.TrimSpace(cliCommit) != "" {
		info.Commit = shortCommit(strings.TrimSpace(cliCommit))
	}
	if strings.TrimSpace(cliBuildTime) != "" {
		info.BuildTime = strings.TrimSpace(cliBuildTime)
	}

	return info
}

func shortCommit(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= 12 {
		return trimmed
	}
	return trimmed[:12]
}
