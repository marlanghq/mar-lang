package runtime

import (
	"fmt"
	"os"
	"strings"
)

const (
	ansiReset   = "\033[0m"
	ansiTitle   = "\033[1m"
	ansiLabel   = "\033[1;36m"
	ansiSection = "\033[1;34m"
	ansiHint    = "\033[1;33m"
	ansiCommand = "\033[1;32m"
)

// printStartupBanner prints a human-friendly runtime summary with optional ANSI colors.
func (r *Runtime) printStartupBanner() {
	useColor := supportsANSI()
	apiURL := fmt.Sprintf("http://localhost:%d", r.App.Port)

	fmt.Printf("\n%s %q\n", colorize(useColor, ansiTitle, "Belm app"), r.App.AppName)
	fmt.Printf("  %s %s\n", colorize(useColor, ansiLabel, "API"), apiURL)
	fmt.Printf("  %s %s\n", colorize(useColor, ansiLabel, "SQLite"), r.App.Database)

	if r.authEnabled() {
		fmt.Printf("\n%s\n", colorize(useColor, ansiSection, "Auth"))
		fmt.Printf("  %s %s\n", "POST", "/auth/request-code")
		fmt.Printf("  %s %s\n", "POST", "/auth/login")
		fmt.Printf("  %s %s\n", "POST", "/auth/logout")
		fmt.Printf("  %s %s\n", "GET ", "/auth/me")
		fmt.Printf("  %s %s\n", "POST", "/_belm/bootstrap-admin (optional first admin)")
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
	fmt.Printf("  %s %s\n", "GET ", "/_belm/admin")
	fmt.Printf("  %s %s\n", "GET ", "/_belm/schema")
	if r.authEnabled() {
		fmt.Printf("  %s %s\n", "GET ", "/_belm/perf (role admin)")
		fmt.Printf("  %s %s\n", "GET ", "/_belm/request-logs (role admin)")
		fmt.Printf("  %s %s\n", "POST", "/_belm/backups (role admin)")
		fmt.Printf("  %s %s\n", "GET ", "/_belm/backups (role admin)")
	}

	if shouldShowAdminHint() {
		fmt.Printf("\n%s run %s to open Belm Admin\n", colorize(useColor, ansiHint, "Hint:"), colorize(useColor, ansiCommand, os.Args[0]+" serve"))
	}
}

func shouldShowAdminHint() bool {
	return !(len(os.Args) > 1 && os.Args[1] == "serve")
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
