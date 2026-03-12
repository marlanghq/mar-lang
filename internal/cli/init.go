package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var projectNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

func runInit(binaryName string, args []string) error {
	if len(args) > 1 {
		return initUsageError(binaryName)
	}

	projectName := ""
	if len(args) == 1 {
		projectName = strings.TrimSpace(args[0])
	} else {
		var err error
		projectName, err = promptProjectName()
		if err != nil {
			return err
		}
	}

	if err := validateProjectName(projectName); err != nil {
		return err
	}

	result, err := createInitProject(".", projectName)
	if err != nil {
		return err
	}

	printInitSummary(result)
	return nil
}

func initUsageError(binaryName string) error {
	useColor := cliSupportsANSIStream(os.Stderr)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;31m", "Init usage"))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s init [project-name]", binaryName))
	fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
	fmt.Fprintf(&b, "  Create a new app with: %s\n", colorizeCLI(useColor, "\033[1;32m", fmt.Sprintf("%s init my-app", binaryName)))
	return styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
}

func initCLIError(title, body, hint string) error {
	useColor := cliSupportsANSIStream(os.Stderr)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;31m", title))
	fmt.Fprintf(&b, "  %s\n", body)
	if strings.TrimSpace(hint) != "" {
		fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
		fmt.Fprintf(&b, "  %s\n", hint)
	}
	return styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
}

func promptProjectName() (string, error) {
	if !stdinIsTerminal() {
		useColor := cliSupportsANSIStream(os.Stderr)
		var b strings.Builder
		fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;31m", "Project name is required"))
		fmt.Fprintf(&b, "  I need a project name to create the new app.\n")
		fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
		fmt.Fprintf(&b, "  Run: %s\n", colorizeCLI(useColor, "\033[1;32m", "mar init my-app"))
		return "", styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
	}

	useColor := cliSupportsANSIStream(os.Stdout)
	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1m", "Project name"))
	fmt.Printf("  Use letters, numbers, hyphens, or underscores.\n")
	fmt.Printf("  This will be the folder name and the .mar file name.\n")
	fmt.Printf("  %s ", colorizeCLI(useColor, "\033[1;36m", "Project name?"))

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func validateProjectName(projectName string) error {
	trimmed := strings.TrimSpace(projectName)
	if trimmed == "" {
		useColor := cliSupportsANSIStream(os.Stderr)
		var b strings.Builder
		fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;31m", "Project name is required"))
		fmt.Fprintf(&b, "  Enter a project name before continuing.\n")
		fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
		fmt.Fprintf(&b, "  Use a name like %s or %s.\n",
			colorizeCLI(useColor, "\033[1;32m", "todo-app"),
			colorizeCLI(useColor, "\033[1;32m", "inventory"),
		)
		return styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
	}

	if strings.Contains(trimmed, string(filepath.Separator)) || strings.Contains(trimmed, "/") {
		return initCLIError(
			"Invalid project name",
			fmt.Sprintf("%q looks like a path. Use only a simple project name.", trimmed),
			"Use a name like todo-app or inventory.",
		)
	}

	if !projectNameRe.MatchString(trimmed) {
		return initCLIError(
			"Invalid project name",
			fmt.Sprintf("%q must start with a letter and use only letters, numbers, hyphens, or underscores.", trimmed),
			"Use a name like todo-app or inventory.",
		)
	}

	return nil
}

type initProjectResult struct {
	ProjectName string
	ProjectDir  string
	AppName     string
	MarFile     string
	GitIgnore   string
	Readme      string
}

func createInitProject(baseDir, projectName string) (*initProjectResult, error) {
	projectDir := filepath.Join(baseDir, projectName)
	if _, err := os.Stat(projectDir); err == nil {
		useColor := cliSupportsANSIStream(os.Stderr)
		return nil, initCLIError(
			"Project directory already exists",
			fmt.Sprintf(
				"The directory %s already exists.",
				colorizeCLI(useColor, "\033[1;36m", filepath.ToSlash(projectDir)),
			),
			"Choose another project name, or remove that folder and try again.",
		)
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		return nil, err
	}

	appName := projectNameToAppName(projectName)
	marFileName := projectName + ".mar"
	marFilePath := filepath.Join(projectDir, marFileName)
	gitIgnorePath := filepath.Join(projectDir, ".gitignore")
	readmePath := filepath.Join(projectDir, "README.md")

	if err := os.WriteFile(marFilePath, []byte(renderInitMar(projectName, appName)), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(gitIgnorePath, []byte(renderInitGitIgnore()), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(readmePath, []byte(renderInitReadme(projectName, appName, marFileName)), 0o644); err != nil {
		return nil, err
	}

	return &initProjectResult{
		ProjectName: projectName,
		ProjectDir:  projectDir,
		AppName:     appName,
		MarFile:     marFilePath,
		GitIgnore:   gitIgnorePath,
		Readme:      readmePath,
	}, nil
}

func projectNameToAppName(projectName string) string {
	parts := strings.FieldsFunc(strings.TrimSpace(projectName), func(r rune) bool {
		return !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
	if len(parts) == 0 {
		return "App"
	}

	var b strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(part)
		first := runes[0]
		if first >= 'a' && first <= 'z' {
			first = first - 'a' + 'A'
		}
		b.WriteRune(first)
		if len(runes) > 1 {
			b.WriteString(string(runes[1:]))
		}
	}

	value := b.String()
	if value == "" {
		return "App"
	}
	return value
}

func renderInitMar(projectName, appName string) string {
	return strings.TrimSpace(fmt.Sprintf(`
app %s
port 4100
database %q

entity Todo {
  id: Int primary auto
  title: String
  done: Bool

  rule "Title must have at least 3 chars" expect len(title) >= 3

  authorize all when auth_authenticated
}
`, appName, projectName+".db")) + "\n"
}

func renderInitGitIgnore() string {
	return strings.TrimSpace(`
dist/
*.db
*.db-shm
*.db-wal
.DS_Store
**/.DS_Store
`) + "\n"
}

func renderInitReadme(projectName, appName, marFileName string) string {
	return strings.TrimSpace(fmt.Sprintf(`
# %s

This project was created by %s.

## Run locally

%s

In development, Mar prints login codes to the terminal.

## Build executables

%s
`, appName, "`mar init`", "```bash\nmar dev "+marFileName+"\n```", "```bash\nmar compile "+marFileName+"\n```")) + "\n"
}

func printInitSummary(result *initProjectResult) {
	useColor := cliSupportsANSIStream(os.Stdout)
	projectDirLabel := filepath.ToSlash(result.ProjectDir)
	marFileName := filepath.Base(result.MarFile)

	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1;36m", "Project ready"))
	fmt.Printf("  %s\n", filepath.ToSlash(result.MarFile))
	fmt.Printf("  %s\n", filepath.ToSlash(result.GitIgnore))
	fmt.Printf("  %s\n", filepath.ToSlash(result.Readme))
	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1;33m", "Next steps"))
	fmt.Printf("  %s\n", colorizeCLI(useColor, "\033[1;32m", "cd "+projectDirLabel))
	fmt.Printf("  %s\n", colorizeCLI(useColor, "\033[1;32m", "mar dev "+marFileName))
	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
	fmt.Printf("  New to Mar? Start here: %s\n", colorizeCLI(useColor, "\033[1;34m", "https://mar-lang.dev/#getting-started"))
	fmt.Println()
}
