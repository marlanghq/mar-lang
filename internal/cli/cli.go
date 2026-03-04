package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"belm/internal/formatter"
	"belm/internal/generator"
	"belm/internal/lsp"
	"belm/internal/model"
	"belm/internal/parser"
)

var (
	cliVersion   = "dev"
	cliCommit    = ""
	cliBuildTime = ""
)

// Run dispatches Belm CLI subcommands.
func Run(binaryName string, args []string) error {
	if binaryName == "" {
		binaryName = "belm"
	}
	if len(args) == 0 {
		printUsage(binaryName)
		return nil
	}

	switch args[0] {
	case "compile":
		if len(args) != 2 && len(args) != 3 {
			return fmt.Errorf("usage: %s compile <input.belm> [output-name]", binaryName)
		}
		app, err := parseBelmFile(args[1])
		if err != nil {
			return err
		}
		outputPath := defaultOutputPath(args[1], "")
		if len(args) == 3 {
			outputPath = defaultOutputPath(args[1], args[2])
		}
		return buildExecutableWithOptions(app, outputPath, buildOptions{
			PrintSummary: true,
			SourcePath:   args[1],
		})
	case "dev":
		if len(args) != 2 && len(args) != 3 {
			return fmt.Errorf("usage: %s dev <input.belm> [output-name]", binaryName)
		}
		outputPath := defaultOutputPath(args[1], "")
		if len(args) == 3 {
			outputPath = defaultOutputPath(args[1], args[2])
		}
		return runDev(binaryName, args[1], outputPath)
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
		return nil, fmt.Errorf("usage: %s format [--check] [--stdin] [files...]\n\npass one or more .belm files, or use --stdin", binaryName)
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
		if strings.ToLower(filepath.Ext(path)) != ".belm" {
			return fmt.Errorf("format only supports .belm files: %s", path)
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

func parseBelmFile(path string) (*model.App, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parser.Parse(string(content))
}

type buildOptions struct {
	PrintSummary bool
	SourcePath   string
}

func buildExecutable(app *model.App, outputPath string) error {
	return buildExecutableWithOptions(app, outputPath, buildOptions{PrintSummary: true})
}

func buildExecutableWithOptions(app *model.App, outputPath string, options buildOptions) error {
	if app == nil {
		return errors.New("nil app")
	}
	if _, err := os.Stat("go.mod"); err != nil {
		return errors.New("build command must run from the belm module root (go.mod not found)")
	}

	manifestJSON, err := json.Marshal(app)
	if err != nil {
		return err
	}
	manifestDigest := sha256.Sum256(manifestJSON)
	manifestHash := "sha256:" + hex.EncodeToString(manifestDigest[:])
	appBuildTime := time.Now().UTC().Format(time.RFC3339)
	compilerInfo := readVersionInfo("belm")

	workDir, err := os.MkdirTemp(".", ".belm-build-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workDir)

	if err := os.WriteFile(filepath.Join(workDir, "manifest.json"), manifestJSON, 0o644); err != nil {
		return err
	}

	sourceBaseDir := "."
	if trimmed := strings.TrimSpace(options.SourcePath); trimmed != "" {
		absSourcePath, err := filepath.Abs(trimmed)
		if err != nil {
			return err
		}
		sourceBaseDir = filepath.Dir(absSourcePath)
	}

	adminAssetsReady := false
	adminSourceDir := filepath.Join(".", "admin")
	if fileExists(filepath.Join(adminSourceDir, "index.html")) {
		if fileExists(filepath.Join(adminSourceDir, "dist", "app.js")) {
			if err := copyFile(filepath.Join(adminSourceDir, "index.html"), filepath.Join(workDir, "admin", "index.html")); err != nil {
				return err
			}
			if err := copyFile(filepath.Join(adminSourceDir, "favicon.svg"), filepath.Join(workDir, "admin", "favicon.svg")); err != nil {
				return err
			}
			if err := copyFile(filepath.Join(adminSourceDir, "dist", "app.js"), filepath.Join(workDir, "admin", "dist", "app.js")); err != nil {
				return err
			}
			adminAssetsReady = true
		} else {
			if err := ensureAdminBundle(".", adminSourceDir); err != nil {
				return fmt.Errorf("cannot compile executable without full admin assets: %w", err)
			}
			if err := copyFile(filepath.Join(adminSourceDir, "index.html"), filepath.Join(workDir, "admin", "index.html")); err != nil {
				return err
			}
			if err := copyFile(filepath.Join(adminSourceDir, "favicon.svg"), filepath.Join(workDir, "admin", "favicon.svg")); err != nil {
				return err
			}
			if err := copyFile(filepath.Join(adminSourceDir, "dist", "app.js"), filepath.Join(workDir, "admin", "dist", "app.js")); err != nil {
				return err
			}
			adminAssetsReady = true
		}
	} else {
		return errors.New("cannot compile executable without admin assets: missing ./admin/index.html")
	}

	publicAssetsReady := app.Public != nil
	if err := ensureEmbeddedPublicPlaceholder(workDir); err != nil {
		return err
	}
	if app.Public != nil {
		publicSourceDir := strings.TrimSpace(app.Public.Dir)
		if publicSourceDir == "" {
			return errors.New("public.dir cannot be empty")
		}
		if !filepath.IsAbs(publicSourceDir) {
			publicSourceDir = filepath.Join(sourceBaseDir, publicSourceDir)
		}
		publicSourceDir = filepath.Clean(publicSourceDir)
		info, err := os.Stat(publicSourceDir)
		if err != nil {
			return fmt.Errorf("public.dir not found: %s", publicSourceDir)
		}
		if !info.IsDir() {
			return fmt.Errorf("public.dir must be a directory: %s", publicSourceDir)
		}
		if err := copyDirContents(publicSourceDir, filepath.Join(workDir, "public")); err != nil {
			return err
		}
		if app.Public.SPAFallback != "" {
			fallback := filepath.Join(workDir, "public", filepath.FromSlash(app.Public.SPAFallback))
			if !fileExists(fallback) {
				return fmt.Errorf("public.spa_fallback not found in public.dir: %s", app.Public.SPAFallback)
			}
		}
	}

	mainSource := fmt.Sprintf(strings.TrimSpace(`
package main

import (
	"context"
	"embed"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"belm/internal/model"
	belmruntime "belm/internal/runtime"
)

//go:embed manifest.json admin/index.html admin/favicon.svg admin/dist/app.js all:public
var files embed.FS

const adminEnabled = %t
const publicEnabled = %t
const backupKeepLast = 20
const belmVersion = %q
const belmCommit = %q
const belmBuildTime = %q
const appBuildTime = %q
const appManifestHash = %q

func main() {
	var app model.App
	manifestData, err := files.ReadFile("manifest.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: read embedded manifest:", err)
		os.Exit(1)
	}
	if err := json.Unmarshal(manifestData, &app); err != nil {
		fmt.Fprintln(os.Stderr, "error: decode embedded manifest:", err)
		os.Exit(1)
	}

	switch {
	case len(os.Args) == 1:
		printAppUsage(os.Args[0])
		os.Exit(0)
	case len(os.Args) == 2 && os.Args[1] == "serve":
		if err := runServe(&app); err != nil {
			belmruntime.PrintStartupError(err, os.Args[0])
			os.Exit(1)
		}
	case len(os.Args) == 2 && os.Args[1] == "backup":
		if err := runBackup(&app); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	default:
		printAppUnknownCommand(os.Args[0], os.Args[1:])
		os.Exit(1)
	}
}

func printAppUsage(binaryName string) {
	useColor := appSupportsANSI(os.Stdout)
	fmt.Println()
	fmt.Printf("%%s\n", appColorize(useColor, "\033[1m", "Available commands"))
	fmt.Printf("  %%s  %%s\n", appColorize(useColor, "\033[1;32m", binaryName+" serve "), "Start the API server and open Belm Admin.")
	fmt.Printf("  %%s  %%s\n", appColorize(useColor, "\033[1;32m", binaryName+" backup"), "Create a SQLite backup in ./backups.")
	fmt.Printf(
		"\n%%s Use %%s to start the API server and open Belm Admin.\n",
		appColorize(useColor, "\033[1;33m", "Hint:"),
		appColorize(useColor, "\033[1;32m", binaryName+" serve"),
	)
	fmt.Println()
}

func printAppUnknownCommand(binaryName string, args []string) {
	useColor := appSupportsANSI(os.Stderr)
	provided := strings.TrimSpace(strings.Join(args, " "))
	if provided == "" {
		provided = "(empty)"
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "%%s %%q\n\n", appColorize(useColor, "\033[1;31m", "unknown command"), provided)
	fmt.Fprintf(os.Stderr, "%%s\n", appColorize(useColor, "\033[1;36m", "Available commands:"))
	fmt.Fprintf(os.Stderr, "  %%s\n", binaryName+" serve")
	fmt.Fprintf(os.Stderr, "  %%s\n", binaryName+" backup")
	fmt.Fprintf(
		os.Stderr,
		"\n%%s Use %%s to start the API server and open Belm Admin.\n",
		appColorize(useColor, "\033[1;33m", "Hint:"),
		appColorize(useColor, "\033[1;32m", binaryName+" serve"),
	)
	fmt.Fprintln(os.Stderr)
}

func appSupportsANSI(stream *os.File) bool {
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

func appColorize(enabled bool, colorCode, value string) string {
	if !enabled {
		return value
	}
	return colorCode + value + "\033[0m"
}

func runServe(app *model.App) error {
	if !adminEnabled {
		return errors.New("admin panel is not embedded in this executable")
	}
	r, err := belmruntime.New(app)
	if err != nil {
		return err
	}
	defer r.Close()
	if err := configurePublicFiles(r); err != nil {
		return err
	}
	if err := configureAdminFiles(r); err != nil {
		return err
	}
	r.SetVersionInfo(belmruntime.VersionInfo{
		BelmVersion:   belmVersion,
		BelmCommit:    belmCommit,
		BelmBuildTime: belmBuildTime,
		AppBuildTime:  appBuildTime,
		ManifestHash:  appManifestHash,
	})

	adminURL := fmt.Sprintf("http://127.0.0.1:%%d/_belm/admin", app.Port)
	fmt.Printf("\nAdmin panel: %%s\n", adminURL)
	if strings.TrimSpace(os.Getenv("BELM_ADMIN_NO_OPEN")) == "" {
		if err := openBrowser(adminURL); err != nil {
			fmt.Fprintln(os.Stderr, "warning: could not open browser:", err)
		}
	}
	return r.Serve(context.Background())
}

func runBackup(app *model.App) error {
	result, err := belmruntime.CreateSQLiteBackup(app.Database, backupKeepLast)
	if err != nil {
		return err
	}

	fmt.Printf("\nBackup created:\n  %%s\n", result.Path)
	if len(result.Removed) > 0 {
		fmt.Println("\nRemoved old backups:")
		for _, path := range result.Removed {
			fmt.Printf("  %%s\n", path)
		}
	}
	fmt.Printf("\nBackups directory:\n  %%s\n\n", result.BackupDir)
	return nil
}

func configurePublicFiles(r *belmruntime.Runtime) error {
	if !publicEnabled {
		return nil
	}
	publicSub, err := fs.Sub(files, "public")
	if err != nil {
		return err
	}
	r.SetPublicFiles(publicSub)
	return nil
}

func configureAdminFiles(r *belmruntime.Runtime) error {
	sub, err := fs.Sub(files, "admin")
	if err != nil {
		return err
	}
	r.SetAdminFiles(sub)
	return nil
}

func openBrowser(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}
`),
		adminAssetsReady,
		publicAssetsReady,
		compilerInfo.Version,
		compilerInfo.Commit,
		compilerInfo.BuildTime,
		appBuildTime,
		manifestHash,
	) + "\n"
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte(mainSource), 0o644); err != nil {
		return err
	}

	elmClient, err := generator.GenerateElmClient(app)
	if err != nil {
		return err
	}
	elmPath := generator.ClientOutputPath(outputPath, elmClient.FileName)
	if err := os.MkdirAll(filepath.Dir(elmPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(elmPath, elmClient.Source, 0o644); err != nil {
		return err
	}
	tsClient, err := generator.GenerateTSClient(app)
	if err != nil {
		return err
	}
	tsPath := generator.ClientOutputPath(outputPath, tsClient.FileName)
	if err := os.MkdirAll(filepath.Dir(tsPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tsPath, tsClient.Source, 0o644); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	cmd := exec.Command("go", "build", "-o", outputPath, workDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	if options.PrintSummary {
		printCompileSummary(outputPath, elmPath, tsPath)
	}
	return nil
}

func copyFile(srcPath, dstPath string) error {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	return dst.Close()
}

func ensureEmbeddedPublicPlaceholder(workDir string) error {
	publicDir := filepath.Join(workDir, "public")
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		return err
	}
	keepFile := filepath.Join(publicDir, ".keep")
	return os.WriteFile(keepFile, []byte("embedded public assets placeholder\n"), 0o644)
}

func copyDirContents(srcDir, dstDir string) error {
	return filepath.WalkDir(srcDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dstPath := filepath.Join(dstDir, rel)
		if entry.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return copyFile(path, dstPath)
	})
}

func ensureAdminBundle(projectRoot, adminDir string) error {
	srcMain := filepath.Join(adminDir, "src", "Main.elm")
	if !fileExists(srcMain) {
		return fmt.Errorf("admin source not found at %s", srcMain)
	}
	distApp := filepath.Join(adminDir, "dist", "app.js")
	if fileExists(distApp) {
		return nil
	}
	if _, err := exec.LookPath("elm"); err != nil {
		return errors.New("Elm compiler not found and admin bundle missing. Install Elm or prebuild admin/dist/app.js")
	}
	distDir := filepath.Join(adminDir, "dist")
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		return err
	}
	build := func(extraEnv []string) error {
		cmd := exec.Command("elm", "make", "src/Main.elm", "--output=dist/app.js")
		cmd.Dir = adminDir
		if len(extraEnv) > 0 {
			cmd.Env = append(os.Environ(), extraEnv...)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				return err
			}
			lines := strings.Split(msg, "\n")
			if len(lines) > 8 {
				msg = strings.Join(lines[:8], "\n")
			}
			return fmt.Errorf("%w: %s", err, msg)
		}
		return nil
	}
	if err := build(nil); err == nil {
		return nil
	}

	elmHome := filepath.Join(projectRoot, ".elm-home")
	if err := os.MkdirAll(elmHome, 0o755); err != nil {
		return err
	}
	if err := build([]string{"ELM_HOME=" + elmHome}); err != nil {
		return fmt.Errorf("failed to build admin panel: %w", err)
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func defaultOutputPath(inputPath, fallback string) string {
	name := ""
	override := strings.TrimSpace(fallback)
	if override != "" {
		name = filepath.Base(override)
		if name == "." {
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
	dirName := strings.TrimSuffix(name, filepath.Ext(name))
	if dirName == "" {
		dirName = name
	}
	binaryName := dirName
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(binaryName), ".exe") {
		binaryName += ".exe"
	}
	return filepath.Join("build", dirName, binaryName)
}

func printUsage(binaryName string) {
	useColor := cliSupportsANSIStream(os.Stdout)

	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1;36m", "Available commands"))
	fmt.Printf("  %-45s %s\n", fmt.Sprintf("%s compile <input.belm> [output-name]", binaryName), "Compile a .belm app into an executable and clients.")
	fmt.Printf("  %-45s %s\n", fmt.Sprintf("%s dev <input.belm> [output-name]", binaryName), "Run development mode with hot reload.")
	fmt.Printf("  %-45s %s\n", fmt.Sprintf("%s format [--check] [--stdin] [files...]", binaryName), "Format Belm source files (Elm-style).")
	fmt.Printf("  %-45s %s\n", fmt.Sprintf("%s lsp", binaryName), "Start the Belm Language Server (for editors).")
	fmt.Printf("  %-45s %s\n", fmt.Sprintf("%s version", binaryName), "Show version and build information.")
	fmt.Printf("\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
	fmt.Printf("  Build an app with: %s\n", colorizeCLI(useColor, "\033[1;32m", fmt.Sprintf("%s compile <input.belm>", binaryName)))
	fmt.Println()
}

func unknownCommandError(binaryName, provided string) error {
	useColor := cliSupportsANSIStream(os.Stderr)
	var b strings.Builder
	fmt.Fprintf(&b, "%s %q\n\n", colorizeCLI(useColor, "\033[1;31m", "unknown command"), provided)
	fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;36m", "Available commands:"))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s compile <input.belm> [output-name]", binaryName))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s dev <input.belm> [output-name]", binaryName))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s format [--check] [--stdin] [files...]", binaryName))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s lsp", binaryName))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s version", binaryName))
	if looksLikeBelmFile(provided) {
		fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
		fmt.Fprintf(&b, "  It looks like you passed a .belm file directly.\n")
		fmt.Fprintf(&b, "  Run: %s\n", colorizeCLI(useColor, "\033[1;32m", fmt.Sprintf("%s compile %s", binaryName, provided)))
	} else {
		fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
		fmt.Fprintf(&b, "  To compile an app, run: %s\n", colorizeCLI(useColor, "\033[1;32m", fmt.Sprintf("%s compile <input.belm>", binaryName)))
	}
	return errors.New(b.String())
}

func looksLikeBelmFile(value string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(value)), ".belm")
}

func printCompileSummary(outputPath, elmPath, tsPath string) {
	useColor := cliSupportsANSIStream(os.Stdout)
	outputDir := filepath.Dir(outputPath)
	outputBin := filepath.Base(outputPath)

	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1m", "Build output"))
	fmt.Printf("  %s\n", colorizeCLI(useColor, "\033[1;32m", "Executable:"))
	fmt.Printf("    %s\n", outputPath)
	fmt.Printf("  %s\n", colorizeCLI(useColor, "\033[1;36m", "Clients:"))
	fmt.Printf("    %s\n", elmPath)
	fmt.Printf("    %s\n", tsPath)
	fmt.Printf("\n  %s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
	fmt.Printf("    %s\n", "To run this app and open Belm Admin:")
	fmt.Printf("    %s\n", colorizeCLI(useColor, "\033[1;32m", "cd "+outputDir))
	fmt.Printf("    %s\n", colorizeCLI(useColor, "\033[1;32m", "./"+outputBin+" serve"))
	fmt.Println()
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
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1m", "Belm version"))
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
		Version:   "dev",
		Commit:    "unknown",
		BuildTime: "unknown",
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
		Binary:    binaryName,
	}

	if exe, err := os.Executable(); err == nil && strings.TrimSpace(exe) != "" {
		info.Binary = exe
	}

	if strings.TrimSpace(cliVersion) != "" {
		info.Version = strings.TrimSpace(cliVersion)
	}
	if strings.TrimSpace(cliCommit) != "" {
		info.Commit = shortCommit(strings.TrimSpace(cliCommit))
	}
	if strings.TrimSpace(cliBuildTime) != "" {
		info.BuildTime = strings.TrimSpace(cliBuildTime)
	}

	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	if info.Version == "dev" && buildInfo.Main.Version != "" && buildInfo.Main.Version != "(devel)" {
		info.Version = buildInfo.Main.Version
	}

	for _, setting := range buildInfo.Settings {
		switch setting.Key {
		case "vcs.revision":
			if info.Commit == "unknown" && strings.TrimSpace(setting.Value) != "" {
				info.Commit = shortCommit(setting.Value)
			}
		case "vcs.time":
			if info.BuildTime == "unknown" && strings.TrimSpace(setting.Value) != "" {
				info.BuildTime = setting.Value
			}
		}
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
