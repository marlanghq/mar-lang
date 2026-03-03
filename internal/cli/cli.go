package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"belm/internal/generator"
	"belm/internal/model"
	"belm/internal/parser"
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
		return buildExecutable(app, outputPath)
	default:
		return unknownCommandError(binaryName, args[0])
	}
}

func parseBelmFile(path string) (*model.App, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parser.Parse(string(content))
}

func buildExecutable(app *model.App, outputPath string) error {
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

	workDir, err := os.MkdirTemp(".", ".belm-build-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workDir)

	if err := os.WriteFile(filepath.Join(workDir, "manifest.json"), manifestJSON, 0o644); err != nil {
		return err
	}

	adminAssetsReady := false
	adminSourceDir := filepath.Join(".", "admin")
	if fileExists(filepath.Join(adminSourceDir, "index.html")) {
		if fileExists(filepath.Join(adminSourceDir, "dist", "app.js")) {
			if err := copyFile(filepath.Join(adminSourceDir, "index.html"), filepath.Join(workDir, "admin", "index.html")); err != nil {
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
			if err := copyFile(filepath.Join(adminSourceDir, "dist", "app.js"), filepath.Join(workDir, "admin", "dist", "app.js")); err != nil {
				return err
			}
			adminAssetsReady = true
		}
	} else {
		return errors.New("cannot compile executable without admin assets: missing ./admin/index.html")
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
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"belm/internal/model"
	belmruntime "belm/internal/runtime"
)

//go:embed manifest.json admin/index.html admin/dist/app.js
var files embed.FS

const adminEnabled = %t

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
		if err := runServe(&app); err != nil {
			belmruntime.PrintStartupError(err, os.Args[0])
			os.Exit(1)
		}
	case len(os.Args) == 2 && os.Args[1] == "admin":
		if err := runAdmin(&app); err != nil {
			belmruntime.PrintStartupError(err, os.Args[0])
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "usage: %%s [admin]\n", os.Args[0])
		os.Exit(1)
	}
}

func runServe(app *model.App) error {
	r, err := belmruntime.New(app)
	if err != nil {
		return err
	}
	defer r.Close()
	return r.Serve(context.Background())
}

func runAdmin(app *model.App) error {
	if !adminEnabled {
		return errors.New("admin panel is not embedded in this executable")
	}
	r, err := belmruntime.New(app)
	if err != nil {
		return err
	}
	defer r.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 2)
	go func() {
		errCh <- r.Serve(ctx)
	}()
	ln, port, err := listenAdmin()
	if err != nil {
		return err
	}
	go func() {
		errCh <- serveAdminFiles(ctx, ln)
	}()

	adminURL := fmt.Sprintf("http://127.0.0.1:%%d/index.html?api=%%s", port, url.QueryEscape(fmt.Sprintf("http://localhost:%%d", app.Port)))
	fmt.Printf("\nAdmin panel: %%s\n", adminURL)
	if err := openBrowser(adminURL); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not open browser:", err)
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case <-sigCtx.Done():
		cancel()
		return nil
	case err := <-errCh:
		cancel()
		return err
	}
}

func serveAdminFiles(ctx context.Context, ln net.Listener) error {
	sub, err := fs.Sub(files, "admin")
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler:      http.FileServer(http.FS(sub)),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 20 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func listenAdmin() (net.Listener, int, error) {
	candidates := []string{"127.0.0.1:8080", "127.0.0.1:5173", "127.0.0.1:0"}
	for _, addr := range candidates {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			continue
		}
		port := ln.Addr().(*net.TCPAddr).Port
		return ln, port, nil
	}
	return nil, 0, errors.New("could not bind local port for admin panel")
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
`), adminAssetsReady) + "\n"
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte(mainSource), 0o644); err != nil {
		return err
	}

	elmClient, err := generator.GenerateElmClient(app)
	if err != nil {
		return err
	}
	elmPath := generator.ElmOutputPath(outputPath, elmClient.FileName)
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
	tsPath := generator.ElmOutputPath(outputPath, tsClient.FileName)
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
	printCompileSummary(outputPath, elmPath, tsPath)
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
		return errors.New("Elm compiler not found and admin bundle missing. Install Elm to use `belm admin` or prebuild admin/dist/app.js")
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
	fmt.Println(binaryName + " commands:")
	fmt.Printf("  %s compile <input.belm> [output-name]\n", binaryName)
}

func unknownCommandError(binaryName, provided string) error {
	useColor := cliSupportsANSIStream(os.Stderr)
	var b strings.Builder
	fmt.Fprintf(&b, "%s %q\n\n", colorizeCLI(useColor, "\033[1;31m", "unknown command"), provided)
	fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;36m", "Available commands:"))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s compile <input.belm> [output-name]", binaryName))
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
	fmt.Printf("    %s\n", colorizeCLI(useColor, "\033[1;32m", "./"+outputBin+" admin"))
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
