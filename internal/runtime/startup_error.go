package runtime

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var (
	typeChangeRe   = regexp.MustCompile(`^migration blocked for ([^.]+)\.([^:]+): type changed from ([^ ]+) to ([^ ]+) in table ([^ ]+)$`)
	pkChangeRe     = regexp.MustCompile(`^migration blocked for ([^.]+)\.([^:]+): primary key shape changed in table ([^ ]+)$`)
	nullChangeRe   = regexp.MustCompile(`^migration blocked for ([^.]+)\.([^:]+): nullability changed in table ([^ ]+)$`)
	addRequiredRe  = regexp.MustCompile(`^migration blocked for entity ([^:]+): cannot auto-add required field "([^"]+)" to existing table ([^ ]+)$`)
	addPrimaryRe   = regexp.MustCompile(`^migration blocked for entity ([^:]+): cannot auto-add primary/auto field "([^"]+)" to existing table ([^ ]+)$`)
	internalStrict = regexp.MustCompile(`^migration blocked for internal table ([^:]+): cannot auto-add strict column "([^"]+)"$`)
)

type migrationBlockedKind string

const (
	blockedTypeChange   migrationBlockedKind = "type_change"
	blockedPKChange     migrationBlockedKind = "primary_key_change"
	blockedNullChange   migrationBlockedKind = "nullability_change"
	blockedAddRequired  migrationBlockedKind = "add_required_field"
	blockedAddPrimary   migrationBlockedKind = "add_primary_auto_field"
	blockedInternalRule migrationBlockedKind = "internal_strict_column"
)

type migrationBlockedInfo struct {
	Kind         migrationBlockedKind
	Entity       string
	Field        string
	Table        string
	CurrentType  string
	ExpectedType string
}

// PrintStartupError formats startup errors with friendlier diagnostics for migration blocks.
func PrintStartupError(err error, _ string) {
	if err == nil {
		return
	}
	msg := strings.TrimSpace(err.Error())
	info, ok := parseMigrationBlocked(msg)
	if !ok {
		fmt.Fprintln(os.Stderr, "error:", err)
		return
	}

	useColor := supportsANSIOn(os.Stderr)
	red := "\033[1;31m"
	yellow := "\033[1;33m"
	cyan := "\033[1;36m"

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, colorize(useColor, red, "MIGRATION BLOCKED"))
	fmt.Fprintln(os.Stderr, "I cannot start this app because this schema change is unsafe to apply automatically.")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s %s\n", colorize(useColor, cyan, "Entity:"), info.Entity)
	if info.Field != "" {
		fmt.Fprintf(os.Stderr, "  %s %s\n", colorize(useColor, cyan, "Field:"), info.Field)
	}
	if info.Table != "" {
		fmt.Fprintf(os.Stderr, "  %s %s\n", colorize(useColor, cyan, "Table:"), info.Table)
	}
	if info.Kind == blockedTypeChange {
		fmt.Fprintf(os.Stderr, "  %s %s\n", colorize(useColor, cyan, "Database type:"), info.CurrentType)
		fmt.Fprintf(os.Stderr, "  %s %s\n", colorize(useColor, cyan, "Belm type:"), info.ExpectedType)
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, colorize(useColor, yellow, "Hint:"))
	fmt.Fprintln(os.Stderr, "  Run a manual SQL migration, or update your Belm schema to match the current database.")
	fmt.Fprintln(os.Stderr)
}

func parseMigrationBlocked(msg string) (migrationBlockedInfo, bool) {
	if m := typeChangeRe.FindStringSubmatch(msg); len(m) == 6 {
		return migrationBlockedInfo{
			Kind:         blockedTypeChange,
			Entity:       m[1],
			Field:        m[2],
			CurrentType:  m[3],
			ExpectedType: m[4],
			Table:        m[5],
		}, true
	}
	if m := pkChangeRe.FindStringSubmatch(msg); len(m) == 4 {
		return migrationBlockedInfo{
			Kind:   blockedPKChange,
			Entity: m[1],
			Field:  m[2],
			Table:  m[3],
		}, true
	}
	if m := nullChangeRe.FindStringSubmatch(msg); len(m) == 4 {
		return migrationBlockedInfo{
			Kind:   blockedNullChange,
			Entity: m[1],
			Field:  m[2],
			Table:  m[3],
		}, true
	}
	if m := addRequiredRe.FindStringSubmatch(msg); len(m) == 4 {
		return migrationBlockedInfo{
			Kind:   blockedAddRequired,
			Entity: m[1],
			Field:  m[2],
			Table:  m[3],
		}, true
	}
	if m := addPrimaryRe.FindStringSubmatch(msg); len(m) == 4 {
		return migrationBlockedInfo{
			Kind:   blockedAddPrimary,
			Entity: m[1],
			Field:  m[2],
			Table:  m[3],
		}, true
	}
	if m := internalStrict.FindStringSubmatch(msg); len(m) == 3 {
		return migrationBlockedInfo{
			Kind:   blockedInternalRule,
			Entity: "internal auth",
			Field:  m[2],
			Table:  m[1],
		}, true
	}
	return migrationBlockedInfo{}, false
}

func supportsANSIOn(stream *os.File) bool {
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
