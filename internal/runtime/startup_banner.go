package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	ansiReset   = "\033[0m"
	ansiTitle   = "\033[1m"
	ansiLabel   = "\033[1;36m"
	ansiSection = "\033[1;38;5;61m"
	ansiHint    = "\033[1;33m"
	ansiCommand = "\033[1;32m"
)

// printStartupBanner prints a human-friendly runtime summary with optional ANSI colors.
func (r *Runtime) printStartupBanner() {
	useColor := supportsANSI()
	apiURL := fmt.Sprintf("http://localhost:%d", r.App.Port)

	fmt.Printf("\n%s %q\n", colorize(useColor, ansiTitle, "Mar app"), r.App.AppName)
	fmt.Printf("  %s %s\n", colorize(useColor, ansiLabel, "API"), apiURL)
	fmt.Printf("  %s %s\n", colorize(useColor, ansiLabel, "SQLite"), displayDatabasePath(r.App.Database))

	if r.authEnabled() {
		fmt.Printf("\n%s\n", colorize(useColor, ansiSection, "Auth"))
		fmt.Printf("  %s %s\n", "POST", "/auth/request-code")
		fmt.Printf("  %s %s\n", "POST", "/auth/login")
		fmt.Printf("  %s %s\n", "POST", "/auth/logout")
		fmt.Printf("  %s %s\n", "GET ", "/auth/me")
		fmt.Printf("  %s %s\n", "POST", "/_mar/admin/bootstrap (first admin setup)")
		if r.authUser != nil {
			fmt.Printf("  %s %s\n", "ALL ", r.authUser.Resource+" (auth users)")
		}
	}

	crudCount := 0
	for _, entity := range r.App.Entities {
		if r.authUser != nil && entity.Name == r.authUser.Name {
			continue
		}
		crudCount++
	}
	if crudCount > 0 {
		fmt.Printf("\n%s\n", colorize(useColor, ansiSection, "CRUD"))
		for _, entity := range r.App.Entities {
			if r.authUser != nil && entity.Name == r.authUser.Name {
				continue
			}
			fmt.Printf("  %s %s\n", "ALL ", entity.Resource)
		}
	}

	if len(r.App.Actions) > 0 {
		fmt.Printf("\n%s\n", colorize(useColor, ansiSection, "Actions"))
		for _, action := range r.App.Actions {
			fmt.Printf("  %s %s\n", "POST", "/actions/"+action.Name)
		}
	}

	if r.App.Public != nil {
		fmt.Printf("\n%s\n", colorize(useColor, ansiSection, "Public"))
		mount := normalizeMount(r.App.Public.Mount)
		fmt.Printf("  %s %s\n", "DIR ", r.App.Public.Dir+" (embedded)")
		fmt.Printf("  %s %s\n", "GET ", mount+"*")
		if r.App.Public.SPAFallback != "" {
			fmt.Printf("  %s %s\n", "SPA ", r.App.Public.SPAFallback)
		}
	}

	fmt.Printf("\n%s\n", colorize(useColor, ansiSection, "System"))
	fmt.Printf("  %s %s\n", "GET ", "/health")
	fmt.Printf("  %s %s\n", "GET ", "/_mar")
	fmt.Printf("  %s %s\n", "GET ", "/_mar/schema")
	fmt.Printf("  %s %s\n", "GET ", "/_mar/version")
	if r.authEnabled() {
		fmt.Printf("  %s %s\n", "GET ", "/_mar/admin/version (role admin)")
		fmt.Printf("  %s %s\n", "GET ", "/_mar/admin/perf (role admin)")
		fmt.Printf("  %s %s\n", "GET ", "/_mar/admin/request-logs (role admin)")
		fmt.Printf("  %s %s\n", "POST", "/_mar/admin/backups (role admin)")
		fmt.Printf("  %s %s\n", "GET ", "/_mar/admin/backups (role admin)")
	}

	if shouldShowAdminHint() {
		fmt.Printf("\n%s run %s to open the Mar App UI\n", colorize(useColor, ansiHint, "Hint:"), colorize(useColor, ansiCommand, os.Args[0]+" serve"))
	}
}

func shouldShowAdminHint() bool {
	return !(len(os.Args) > 1 && os.Args[1] == "serve")
}

func displayDatabasePath(databasePath string) string {
	processCWD, err := os.Getwd()
	if err != nil {
		processCWD = ""
	}
	return resolveDatabaseDisplayPath(
		databasePath,
		processCWD,
		strings.TrimSpace(os.Getenv("MAR_DEV_LAUNCH_CWD")),
	)
}

func resolveDatabaseDisplayPath(databasePath, processCWD, launchCWD string) string {
	trimmed := strings.TrimSpace(databasePath)
	if trimmed == "" {
		return databasePath
	}

	cleaned := filepath.Clean(trimmed)
	displayBase := strings.TrimSpace(launchCWD)
	if displayBase == "" {
		displayBase = strings.TrimSpace(processCWD)
	}

	if filepath.IsAbs(cleaned) {
		if displayBase == "" {
			return cleaned
		}
		rel, err := filepath.Rel(displayBase, cleaned)
		if err != nil {
			return cleaned
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return cleaned
		}
		return filepath.Clean(rel)
	}
	if strings.TrimSpace(processCWD) == "" {
		return cleaned
	}

	actualPath := filepath.Join(processCWD, cleaned)
	if displayBase == "" {
		return cleaned
	}

	rel, err := filepath.Rel(displayBase, actualPath)
	if err != nil {
		return actualPath
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return actualPath
	}
	return filepath.Clean(rel)
}

func colorize(enabled bool, colorCode, value string) string {
	if !enabled {
		return value
	}
	return colorCode + value + ansiReset
}

func supportsANSI() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	term := strings.ToLower(strings.TrimSpace(os.Getenv("TERM")))
	if term == "" || term == "dumb" {
		return false
	}
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
