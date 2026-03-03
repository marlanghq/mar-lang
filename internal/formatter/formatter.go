package formatter

import (
	"fmt"
	"regexp"
	"strings"

	"belm/internal/parser"
)

var (
	appRe         = regexp.MustCompile(`^app\s+([A-Za-z][A-Za-z0-9_]*)$`)
	portRe        = regexp.MustCompile(`^port\s+([0-9]{1,5})$`)
	dbRe          = regexp.MustCompile(`^database\s+"([^"]+)"$`)
	authStartRe   = regexp.MustCompile(`^auth\s*\{$`)
	entityStartRe = regexp.MustCompile(`^entity\s+([A-Za-z][A-Za-z0-9_]*)\s*\{$`)
	typeAliasRe   = regexp.MustCompile(`^type\s+alias\s+([A-Za-z][A-Za-z0-9_]*)\s*=\s*(.*)$`)
	actionSigRe   = regexp.MustCompile(`^([a-z][A-Za-z0-9_]*)\s*:\s*([A-Za-z][A-Za-z0-9_]*)\s*->\s*(.+)$`)
	actionDefRe   = regexp.MustCompile(`^([a-z][A-Za-z0-9_]*)\s*=$`)

	entityFieldRe = regexp.MustCompile(`^([a-z][A-Za-z0-9_]*)\s*:\s*(Int|String|Bool|Float)(?:\s+(.*))?$`)
	ruleRe        = regexp.MustCompile(`^rule\s+"([^"]+)"\s+when\s+(.+)$`)
	authorizeRe   = regexp.MustCompile(`^authorize\s+(list|get|create|update|delete)\s+when\s+(.+)$`)
	authStmtRe    = regexp.MustCompile(`^(user_entity|email_field|role_field|code_ttl_minutes|session_ttl_hours|email_transport|dev_expose_code)\s+(.+)$`)
	authQuoteRe   = regexp.MustCompile(`^(email_from|email_subject|sendmail_path)\s+"([^"]+)"$`)

	aliasFieldRe = regexp.MustCompile(`^([a-z][A-Za-z0-9_]*)\s*:\s*(Int|String|Bool|Float)\s*$`)
	insertStepRe = regexp.MustCompile(`^insert\s+([A-Za-z][A-Za-z0-9_]*)\s*\{(.*)\}$`)
	assignRe     = regexp.MustCompile(`^([a-z][A-Za-z0-9_]*)\s*=\s*(.+)$`)
)

// Format rewrites Belm source into a canonical style and returns formatted text.
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
		if state.awaitingTxDef && strings.TrimSpace(line) == "transaction" {
			indentBefore = 1
		}
		if state.inTx {
			trimLine := strings.TrimSpace(line)
			if strings.HasPrefix(trimLine, "[") || trimLine == "]" || state.inTxList {
				indentBefore = 2
			}
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
	inEntity      bool
	inAuth        bool
	inTypeAlias   bool
	pendingAlias  bool
	inTx          bool
	inTxList      bool
	awaitingTxDef bool
}

func (s *formatState) update(line string) {
	trimLine := strings.TrimSpace(line)
	switch {
	case entityStartRe.MatchString(line):
		s.inEntity = true
	case authStartRe.MatchString(line):
		s.inAuth = true
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
	case actionDefRe.MatchString(line):
		s.awaitingTxDef = true
		s.inTx = false
		s.inTxList = false
	case trimLine == "transaction":
		if s.awaitingTxDef {
			s.awaitingTxDef = false
		}
		s.inTx = true
	case strings.HasPrefix(trimLine, "["):
		if s.inTx {
			s.inTxList = true
			if strings.Contains(trimLine, "]") {
				s.inTxList = false
				s.inTx = false
			}
		}
	case trimLine == "]":
		s.inTxList = false
		s.inTx = false
	case trimLine == "}":
		switch {
		case s.inEntity:
			s.inEntity = false
		case s.inAuth:
			s.inAuth = false
		case s.inTypeAlias:
			s.inTypeAlias = false
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
	if m := actionSigRe.FindStringSubmatch(trimmed); m != nil {
		return m[1] + " : " + m[2] + " -> " + collapseSpaces(m[3])
	}
	if m := actionDefRe.FindStringSubmatch(trimmed); m != nil {
		return m[1] + " ="
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
			return `rule "` + m[1] + `" when ` + strings.TrimSpace(m[2])
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

	if strings.TrimSpace(trimmed) == "transaction" {
		return "transaction"
	}
	if strings.TrimSpace(trimmed) == "[" || strings.TrimSpace(trimmed) == "]" {
		return strings.TrimSpace(trimmed)
	}
	if state.inTxList {
		prefix := ""
		token := strings.TrimSpace(trimmed)
		if strings.HasPrefix(token, ",") {
			prefix = ", "
			token = strings.TrimSpace(strings.TrimPrefix(token, ","))
		}
		return prefix + normalizeActionStep(token)
	}

	return collapseSpaces(trimmed)
}

func normalizeActionStep(token string) string {
	if m := insertStepRe.FindStringSubmatch(strings.TrimSpace(token)); m != nil {
		entity := m[1]
		values := strings.TrimSpace(m[2])
		parts := splitCSV(values)
		normalized := make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if mm := assignRe.FindStringSubmatch(part); mm != nil {
				normalized = append(normalized, mm[1]+" = "+strings.TrimSpace(mm[2]))
			} else {
				normalized = append(normalized, part)
			}
		}
		return "insert " + entity + " { " + strings.Join(normalized, ", ") + " }"
	}
	return collapseSpaces(strings.TrimSpace(token))
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}
	parts := make([]string, 0, 8)
	var b strings.Builder
	inString := false
	escaped := false
	for _, ch := range value {
		if escaped {
			b.WriteRune(ch)
			escaped = false
			continue
		}
		if inString && ch == '\\' {
			b.WriteRune(ch)
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			b.WriteRune(ch)
			continue
		}
		if ch == ',' && !inString {
			parts = append(parts, b.String())
			b.Reset()
			continue
		}
		b.WriteRune(ch)
	}
	parts = append(parts, b.String())
	return parts
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
	return strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "#")
}
