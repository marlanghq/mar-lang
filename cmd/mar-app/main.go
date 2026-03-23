package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"mar/internal/appbundle"
	"mar/internal/model"
	marruntime "mar/internal/runtime"
)

const backupKeepLast = 20

func main() {
	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: resolve executable:", err)
		os.Exit(1)
	}

	bundle, err := appbundle.LoadExecutable(exePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: load embedded app bundle:", err)
		os.Exit(1)
	}

	app := *bundle.App
	app.Database = marruntime.ResolveDatabasePath(app.Database)

	switch {
	case len(os.Args) == 1:
		printAppUsage(os.Args[0])
		os.Exit(0)
	case len(os.Args) == 2 && os.Args[1] == "serve":
		if err := runServe(&app, bundle); err != nil {
			marruntime.PrintStartupError(err, os.Args[0])
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
	fmt.Printf("%s\n", appColorize(useColor, "\033[1m", "Available commands"))
	fmt.Printf("  %s  %s\n", appColorize(useColor, "\033[1;32m", binaryName+" serve "), "Start the API server and show the Mar Admin URL.")
	fmt.Printf("  %s  %s\n", appColorize(useColor, "\033[1;32m", binaryName+" backup"), "Create a SQLite backup in ./backups.")
	fmt.Printf(
		"\n%s Use %s to start the API server and show the Mar Admin URL.\n",
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
	fmt.Fprintf(os.Stderr, "%s %q\n\n", appColorize(useColor, "\033[1;31m", "unknown command"), provided)
	fmt.Fprintf(os.Stderr, "%s\n", appColorize(useColor, "\033[1;36m", "Available commands:"))
	fmt.Fprintf(os.Stderr, "  %s\n", binaryName+" serve")
	fmt.Fprintf(os.Stderr, "  %s\n", binaryName+" backup")
	fmt.Fprintf(
		os.Stderr,
		"\n%s Use %s to start the API server and show the Mar Admin URL.\n",
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

func runServe(app *model.App, bundle *appbundle.Bundle) error {
	adminFS, err := fs.Sub(bundle.Archive, "admin")
	if err != nil {
		return errors.New("admin panel is not embedded in this executable")
	}

	r, err := marruntime.New(app)
	if err != nil {
		return err
	}
	defer r.Close()
	r.SetAdminFiles(adminFS)
	if app.Public != nil {
		publicFS, err := fs.Sub(bundle.Archive, "public")
		if err != nil {
			return fmt.Errorf("public assets are not embedded in this executable: %w", err)
		}
		r.SetPublicFiles(publicFS)
	}
	r.SetVersionInfo(marruntime.VersionInfo{
		MarVersion:   bundle.Metadata.MarVersion,
		MarCommit:    bundle.Metadata.MarCommit,
		MarBuildTime: bundle.Metadata.MarBuildTime,
		AppBuildTime: bundle.Metadata.AppBuildTime,
		ManifestHash: bundle.Metadata.ManifestHash,
	})
	if err := r.ValidateStartup(); err != nil {
		return err
	}

	useColor := appSupportsANSI(os.Stdout)
	adminURL := fmt.Sprintf("http://127.0.0.1:%d/_mar/admin", app.Port)
	fmt.Printf(
		"\n%s %s\n",
		appColorize(useColor, "\033[1;36m", "Admin panel:"),
		appColorize(useColor, "\033[1;34m", adminURL),
	)
	return r.Serve(context.Background())
}

func runBackup(app *model.App) error {
	result, err := marruntime.CreateSQLiteBackup(app.Database, marruntime.SQLiteConfigForApp(app), backupKeepLast)
	if err != nil {
		return err
	}

	useColor := appSupportsANSI(os.Stdout)
	fmt.Println()
	fmt.Printf("%s\n", appColorize(useColor, "\033[1m", "SQLite backup created"))
	fmt.Printf("  %s %s\n", appColorize(useColor, "\033[1;36m", "SQLite database:"), result.Database)
	fmt.Printf("  %s %s\n", appColorize(useColor, "\033[1;32m", "Backup file:"), result.Path)
	if len(result.Removed) > 0 {
		fmt.Printf("\n%s\n", appColorize(useColor, "\033[1;33m", "Removed old backups"))
		for _, path := range result.Removed {
			fmt.Printf("  %s\n", path)
		}
	}
	fmt.Println()
	return nil
}
