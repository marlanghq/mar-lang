package formatter

import (
	"fmt"
	"regexp"
	"strings"

	"mar/internal/parser"
)

var (
	appRe          = regexp.MustCompile(`^app\s+([A-Za-z][A-Za-z0-9_]*)$`)
	portRe         = regexp.MustCompile(`^port\s+([0-9]{1,5})$`)
	dbRe           = regexp.MustCompile(`^database\s+"([^"]+)"$`)
	systemStartRe  = regexp.MustCompile(`^system\s*\{$`)
	publicStartRe  = regexp.MustCompile(`^public\s*\{$`)
	authStartRe    = regexp.MustCompile(`^auth\s*\{$`)
	entityStartRe  = regexp.MustCompile(`^entity\s+([A-Za-z][A-Za-z0-9_]*)\s*\{$`)
	typeAliasRe    = regexp.MustCompile(`^type\s+alias\s+([A-Za-z][A-Za-z0-9_]*)\s*=\s*(.*)$`)
	actionStartRe  = regexp.MustCompile(`^action\s+([a-z][A-Za-z0-9_]*)\s*\{$`)
	actionInputRe  = regexp.MustCompile(`^input\s*:\s*([A-Za-z][A-Za-z0-9_]*)$`)
	actionCreateRe = regexp.MustCompile(`^create\s+([A-Za-z][A-Za-z0-9_]*)\s*\{$`)

	entityFieldRe = regexp.MustCompile(`^([a-z][A-Za-z0-9_]*)\s*:\s*(Int|String|Bool|Float|Posix)(?:\s+(.*))?$`)
	ruleRe        = regexp.MustCompile(`^rule\s+"([^"]+)"\s+expect\s+(.+)$`)
	authorizeRe   = regexp.MustCompile(`^authorize\s+(all|list|get|create|update|delete)\s+when\s+(.+)$`)
	systemIntRe   = regexp.MustCompile(`^(request_logs_buffer|sqlite_busy_timeout_ms|sqlite_wal_autocheckpoint|auth_request_code_rate_limit_per_minute|auth_login_rate_limit_per_minute|admin_ui_session_ttl_hours)\s+([0-9]{1,7})$`)
	systemModeRe  = regexp.MustCompile(`^(sqlite_journal_mode)\s+(wal|delete|truncate|persist|memory|off)$`)
	systemSyncRe  = regexp.MustCompile(`^(sqlite_synchronous)\s+(off|normal|full|extra)$`)
	systemBoolRe  = regexp.MustCompile(`^(sqlite_foreign_keys|security_content_type_nosniff)\s+(true|false)$`)
	systemFrameRe = regexp.MustCompile(`^(security_frame_policy)\s+(deny|sameorigin)$`)
	systemRefRe   = regexp.MustCompile(`^(security_referrer_policy)\s+(strict-origin-when-cross-origin|no-referrer)$`)
	systemLimitRe = regexp.MustCompile(`^(sqlite_journal_size_limit_mb)\s+(-?[0-9]{1,4})$`)
	systemMBRe    = regexp.MustCompile(`^(sqlite_mmap_size_mb|http_max_request_body_mb)\s+([0-9]{1,5})$`)
	systemKBRe    = regexp.MustCompile(`^(sqlite_cache_size_kb)\s+([0-9]{1,7})$`)
	publicQuoteRe = regexp.MustCompile(`^(dir|mount|spa_fallback)\s+"([^"]+)"$`)
	authStmtRe    = regexp.MustCompile(`^(code_ttl_minutes|session_ttl_hours|email_transport|smtp_port|smtp_starttls)\s+(.+)$`)
	authQuoteRe   = regexp.MustCompile(`^(email_from|email_subject|smtp_host|smtp_username|smtp_password_env)\s+"([^"]+)"$`)

	aliasFieldRe        = regexp.MustCompile(`^([a-z][A-Za-z0-9_]*)\s*:\s*(Int|String|Bool|Float|Posix)\s*$`)
	actionFieldAssignRe = regexp.MustCompile(`^([a-z][A-Za-z0-9_]*)\s*:\s*(.+)$`)
)

// Format rewrites Mar source into a canonical style and returns formatted text.
func Format(source string) (string, error) {
	normalized := normalizeNewlines(source)
	if _, err := parser.Parse(normalized); err != nil {
		return "", err
	}

	lines := strings.Split(normalized, "\n")
	out := make([]string, 0, len(lines))
	indent := 0
	state := &formatState{}

	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			if len(out) == 0 || out[len(out)-1] == "" {
				continue
			}
			out = append(out, "")
			continue
		}
		if isCommentLine(trimmed) {
			out = append(out, strings.Repeat(" ", indent*2)+trimmed)
			continue
		}

		line := normalizeLine(trimmed, state)
		indentBefore := indent - leadingCloseCount(line)
		if indentBefore < 0 {
			indentBefore = 0
		}
		if state.inTypeAlias || (state.pendingAlias && strings.HasPrefix(strings.TrimSpace(line), "{")) {
			indentBefore = 1
		}

		outLine := strings.Repeat(" ", indentBefore*2) + line
		out = append(out, outLine)

		indent += bracketDelta(line)
		if indent < 0 {
			indent = 0
		}
		state.update(line)
	}

	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	formatted := strings.Join(out, "\n") + "\n"

	if _, err := parser.Parse(formatted); err != nil {
		return "", fmt.Errorf("formatter produced invalid output: %w", err)
	}
	return formatted, nil
}

type formatState struct {
	inEntity       bool
	inSystem       bool
	inPublic       bool
	inAuth         bool
	inTypeAlias    bool
	pendingAlias   bool
	inAction       bool
	inActionCreate bool
}

func (s *formatState) update(line string) {
	trimLine := strings.TrimSpace(line)
	switch {
	case entityStartRe.MatchString(line):
		s.inEntity = true
	case authStartRe.MatchString(line):
		s.inAuth = true
	case systemStartRe.MatchString(line):
		s.inSystem = true
	case publicStartRe.MatchString(line):
		s.inPublic = true
	case typeAliasRe.MatchString(line):
		m := typeAliasRe.FindStringSubmatch(line)
		rest := strings.TrimSpace(m[2])
		if rest == "" {
			s.pendingAlias = true
		} else if strings.Contains(rest, "{") && !strings.Contains(rest, "}") {
			s.inTypeAlias = true
		}
	case s.pendingAlias && line == "{":
		s.pendingAlias = false
		s.inTypeAlias = true
	case actionStartRe.MatchString(line):
		s.inAction = true
	case s.inAction && actionCreateRe.MatchString(trimLine):
		s.inActionCreate = true
	case trimLine == "}":
		switch {
		case s.inActionCreate:
			s.inActionCreate = false
		case s.inEntity:
			s.inEntity = false
		case s.inSystem:
			s.inSystem = false
		case s.inPublic:
			s.inPublic = false
		case s.inAuth:
			s.inAuth = false
		case s.inTypeAlias:
			s.inTypeAlias = false
		case s.inAction:
			s.inAction = false
		}
	}
}

func normalizeLine(trimmed string, state *formatState) string {
	if m := appRe.FindStringSubmatch(trimmed); m != nil {
		return "app " + m[1]
	}
	if m := portRe.FindStringSubmatch(trimmed); m != nil {
		return "port " + m[1]
	}
	if m := dbRe.FindStringSubmatch(trimmed); m != nil {
		return `database "` + m[1] + `"`
	}
	if systemStartRe.MatchString(trimmed) {
		return "system {"
	}
	if publicStartRe.MatchString(trimmed) {
		return "public {"
	}
	if authStartRe.MatchString(trimmed) {
		return "auth {"
	}
	if m := entityStartRe.FindStringSubmatch(trimmed); m != nil {
		return "entity " + m[1] + " {"
	}
	if m := typeAliasRe.FindStringSubmatch(trimmed); m != nil {
		name := m[1]
		rest := strings.TrimSpace(m[2])
		if rest == "" {
			return "type alias " + name + " ="
		}
		return "type alias " + name + " = " + rest
	}
	if m := actionStartRe.FindStringSubmatch(trimmed); m != nil {
		return "action " + m[1] + " {"
	}

	if state.inSystem {
		if m := systemIntRe.FindStringSubmatch(trimmed); m != nil {
			return m[1] + " " + m[2]
		}
		if m := systemModeRe.FindStringSubmatch(trimmed); m != nil {
			return m[1] + " " + m[2]
		}
		if m := systemSyncRe.FindStringSubmatch(trimmed); m != nil {
			return m[1] + " " + m[2]
		}
		if m := systemBoolRe.FindStringSubmatch(trimmed); m != nil {
			return m[1] + " " + m[2]
		}
		if m := systemFrameRe.FindStringSubmatch(trimmed); m != nil {
			return m[1] + " " + m[2]
		}
		if m := systemRefRe.FindStringSubmatch(trimmed); m != nil {
			return m[1] + " " + m[2]
		}
		if m := systemLimitRe.FindStringSubmatch(trimmed); m != nil {
			return m[1] + " " + m[2]
		}
		if m := systemMBRe.FindStringSubmatch(trimmed); m != nil {
			return m[1] + " " + m[2]
		}
		if m := systemKBRe.FindStringSubmatch(trimmed); m != nil {
			return m[1] + " " + m[2]
		}
	}

	if state.inPublic {
		if m := publicQuoteRe.FindStringSubmatch(trimmed); m != nil {
			return m[1] + ` "` + m[2] + `"`
		}
	}

	if state.inAuth {
		if m := authStmtRe.FindStringSubmatch(trimmed); m != nil {
			return m[1] + " " + strings.TrimSpace(m[2])
		}
		if m := authQuoteRe.FindStringSubmatch(trimmed); m != nil {
			return m[1] + ` "` + m[2] + `"`
		}
	}

	if state.inEntity {
		if m := entityFieldRe.FindStringSubmatch(trimmed); m != nil {
			attrs := strings.TrimSpace(m[3])
			if attrs == "" {
				return m[1] + ": " + m[2]
			}
			return m[1] + ": " + m[2] + " " + collapseSpaces(attrs)
		}
		if m := ruleRe.FindStringSubmatch(trimmed); m != nil {
			return `rule "` + m[1] + `" expect ` + strings.TrimSpace(m[2])
		}
		if m := authorizeRe.FindStringSubmatch(trimmed); m != nil {
			return "authorize " + m[1] + " when " + strings.TrimSpace(m[2])
		}
	}

	if state.inTypeAlias {
		if trimmed == "{" || trimmed == "}" {
			return trimmed
		}
		prefix := ""
		token := strings.TrimSpace(trimmed)
		if strings.HasPrefix(token, ",") {
			prefix = ", "
			token = strings.TrimSpace(strings.TrimPrefix(token, ","))
		}
		if m := aliasFieldRe.FindStringSubmatch(token); m != nil {
			return prefix + m[1] + " : " + m[2]
		}
	}

	if state.inAction {
		if m := actionInputRe.FindStringSubmatch(trimmed); m != nil {
			return "input: " + m[1]
		}
		if m := actionCreateRe.FindStringSubmatch(trimmed); m != nil {
			return "create " + m[1] + " {"
		}
		if state.inActionCreate {
			if m := actionFieldAssignRe.FindStringSubmatch(trimmed); m != nil {
				return m[1] + ": " + strings.TrimSpace(m[2])
			}
		}
	}

	return collapseSpaces(trimmed)
}

func normalizeNewlines(source string) string {
	s := strings.ReplaceAll(source, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

func leadingCloseCount(line string) int {
	trimmed := strings.TrimLeft(line, " \t")
	count := 0
	for _, ch := range trimmed {
		if ch == '}' || ch == ']' {
			count++
			continue
		}
		break
	}
	return count
}

func bracketDelta(line string) int {
	delta := 0
	inString := false
	escaped := false
	for i, ch := range line {
		if escaped {
			escaped = false
			continue
		}
		if inString && ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if !inString && i+1 < len(line) && line[i] == '-' && line[i+1] == '-' {
			break
		}
		if inString {
			continue
		}
		switch ch {
		case '{', '[':
			delta++
		case '}', ']':
			delta--
		}
	}
	return delta
}

func collapseSpaces(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func isCommentLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "--")
}
