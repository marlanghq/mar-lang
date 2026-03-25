package runtime

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

var (
	typeChangeRe   = regexp.MustCompile(`^migration blocked for ([^.]+)\.([^:]+): type changed from ([^ ]+) to ([^ ]+) in table ([^ ]+)$`)
	pkChangeRe     = regexp.MustCompile(`^migration blocked for ([^.]+)\.([^:]+): primary key shape changed in table ([^ ]+)$`)
	nullChangeRe   = regexp.MustCompile(`^migration blocked for ([^.]+)\.([^:]+): nullability changed in table ([^ ]+)$`)
	addRequiredRe  = regexp.MustCompile(`^migration blocked for entity ([^:]+): cannot auto-add required field "([^"]+)" \(([^)]+)\) to existing table ([^ ]+)$`)
	addPrimaryRe   = regexp.MustCompile(`^migration blocked for entity ([^:]+): cannot auto-add primary/auto field "([^"]+)" to existing table ([^ ]+)$`)
	internalStrict = regexp.MustCompile(`^migration blocked for internal table ([^:]+): cannot auto-add strict column "([^"]+)"$`)
	uniqueIndexRe  = regexp.MustCompile(`^migration blocked for ([^.]+)\.([^:]+): duplicate values prevent unique index creation in table ([^ ]+)$`)
)

type migrationBlockedKind string

const (
	blockedTypeChange   migrationBlockedKind = "type_change"
	blockedPKChange     migrationBlockedKind = "primary_key_change"
	blockedNullChange   migrationBlockedKind = "nullability_change"
	blockedAddRequired  migrationBlockedKind = "add_required_field"
	blockedAddPrimary   migrationBlockedKind = "add_primary_auto_field"
	blockedInternalRule migrationBlockedKind = "internal_strict_column"
	blockedUniqueIndex  migrationBlockedKind = "unique_index_create"
)

type migrationBlockedInfo struct {
	Kind         migrationBlockedKind
	Entity       string
	Field        string
	Table        string
	CurrentType  string
	ExpectedType string
}

type startupDetail struct {
	Label string
	Value string
}

type startupFriendlyError struct {
	Title   string
	Message string
	Details []startupDetail
	Hints   []string
}

func (e *startupFriendlyError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) == "" {
		return strings.TrimSpace(e.Title)
	}
	if strings.TrimSpace(e.Title) == "" {
		return strings.TrimSpace(e.Message)
	}
	return strings.TrimSpace(e.Title) + ": " + strings.TrimSpace(e.Message)
}

// PrintStartupError formats startup errors with friendlier diagnostics for migration blocks.
func PrintStartupError(err error, _ string) {
	if err == nil {
		return
	}
	var startupErr *startupFriendlyError
	if errors.As(err, &startupErr) {
		printFriendlyStartupError(startupErr)
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
		fmt.Fprintf(os.Stderr, "  %s %s\n", colorize(useColor, cyan, "Mar type:"), info.ExpectedType)
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, colorize(useColor, yellow, "Hint:"))
	if info.Kind == blockedUniqueIndex {
		fmt.Fprintln(os.Stderr, "  Remove duplicate values for this field in the current database.")
	} else if info.Kind == blockedAddRequired && info.Field != "" && info.ExpectedType != "" {
		optionalExample := colorizedFieldExample(useColor, info.Field, info.ExpectedType, "optional", "")
		defaultExample := colorizedFieldExample(useColor, info.Field, info.ExpectedType, "default", suggestedDefaultLiteral(info.ExpectedType))
		fmt.Fprintln(os.Stderr, "  You have a few options:")
		fmt.Fprintln(os.Stderr, "  1. Run a manual SQL migration to update the current database schema.")
		fmt.Fprintf(os.Stderr, "  2. Make the new field optional. Example: %s\n", optionalExample)
		fmt.Fprintf(os.Stderr, "  3. Keep the field required and give it a default. Example: %s\n", defaultExample)
	} else {
		fmt.Fprintln(os.Stderr, "  Run a manual SQL migration, or update your Mar schema to match the current database.")
	}
	fmt.Fprintln(os.Stderr)
}

func colorizedFieldExample(useColor bool, fieldName, fieldType, modifier, literal string) string {
	example := fieldName + ": " + fieldType + " " + modifier
	if literal != "" {
		example += " " + literal
	}
	if !useColor {
		return example
	}

	fieldColor := "\033[1;97m"
	typeColor := "\033[38;5;141m"
	modifierColor := "\033[38;5;110m"
	literalColor := marEditLiteralColor(literal)

	colored := colorize(useColor, fieldColor, fieldName+":") + " " + colorize(useColor, typeColor, fieldType) + " " + colorize(useColor, modifierColor, modifier)
	if literal != "" {
		colored += " " + colorize(useColor, literalColor, literal)
	}
	return colored
}

func suggestedDefaultLiteral(fieldType string) string {
	switch strings.TrimSpace(fieldType) {
	case "String":
		return `"Unknown"`
	case "Bool":
		return "false"
	case "Float":
		return "0.0"
	case "Int", "Posix":
		return "0"
	default:
		return "0"
	}
}

func marEditLiteralColor(literal string) string {
	trimmed := strings.TrimSpace(literal)
	if trimmed == "" {
		return "\033[38;5;110m"
	}
	if strings.HasPrefix(trimmed, `"`) && strings.HasSuffix(trimmed, `"`) {
		return "\033[38;5;114m"
	}
	return "\033[38;5;179m"
}

func printFriendlyStartupError(err *startupFriendlyError) {
	if err == nil {
		return
	}
	useColor := supportsANSIOn(os.Stderr)
	red := "\033[1;31m"
	yellow := "\033[1;33m"
	cyan := "\033[1;36m"

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, colorize(useColor, red, strings.TrimSpace(err.Title)))
	if msg := strings.TrimSpace(err.Message); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
	}
	if len(err.Details) > 0 {
		fmt.Fprintln(os.Stderr)
		for _, detail := range err.Details {
			if strings.TrimSpace(detail.Label) == "" || strings.TrimSpace(detail.Value) == "" {
				continue
			}
			fmt.Fprintf(os.Stderr, "  %s %s\n", colorize(useColor, cyan, detail.Label+":"), detail.Value)
		}
	}
	if len(err.Hints) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, colorize(useColor, yellow, "Hint:"))
		for _, hint := range err.Hints {
			if strings.TrimSpace(hint) == "" {
				continue
			}
			fmt.Fprintf(os.Stderr, "  %s\n", hint)
		}
	}
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
	if m := addRequiredRe.FindStringSubmatch(msg); len(m) == 5 {
		return migrationBlockedInfo{
			Kind:         blockedAddRequired,
			Entity:       m[1],
			Field:        m[2],
			ExpectedType: m[3],
			Table:        m[4],
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
	if m := uniqueIndexRe.FindStringSubmatch(msg); len(m) == 4 {
		return migrationBlockedInfo{
			Kind:   blockedUniqueIndex,
			Entity: m[1],
			Field:  m[2],
			Table:  m[3],
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
