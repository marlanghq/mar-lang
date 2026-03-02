package parser

import (
	"fmt"
	"regexp"
	"strings"

	"belm/internal/expr"
	"belm/internal/model"
)

var (
	upperNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	fieldNameRe = regexp.MustCompile(`^[a-z][A-Za-z0-9_]*$`)
)

type line struct {
	number int
	text   string
}

// Parse reads Belm source and returns an App model.
func Parse(source string) (*model.App, error) {
	lines := splitLines(source)
	idx := 0

	app := &model.App{
		Port:     3000,
		Database: "./app.db",
	}

	next := func() *line {
		if idx >= len(lines) {
			return nil
		}
		return &lines[idx]
	}
	advance := func() {
		idx++
	}

	for {
		cur := next()
		if cur == nil {
			break
		}
		trimmed := strings.TrimSpace(cur.text)
		if isCommentOrBlank(trimmed) {
			advance()
			continue
		}

		if m := match(`^app\s+([A-Za-z][A-Za-z0-9_]*)$`, trimmed); m != nil {
			app.AppName = m[1]
			advance()
			continue
		}

		if m := match(`^port\s+([0-9]{1,5})$`, trimmed); m != nil {
			port := mustInt(m[1])
			if port < 1 || port > 65535 {
				return nil, fmt.Errorf("line %d: invalid port %d", cur.number, port)
			}
			app.Port = port
			advance()
			continue
		}

		if m := match(`^database\s+"([^"]+)"$`, trimmed); m != nil {
			app.Database = m[1]
			advance()
			continue
		}

		if trimmed == "auth {" {
			if app.Auth != nil {
				return nil, fmt.Errorf("line %d: auth block already declared", cur.number)
			}
			auth, err := parseAuthBlock(lines, &idx)
			if err != nil {
				return nil, err
			}
			app.Auth = auth
			continue
		}

		if m := match(`^entity\s+([A-Za-z][A-Za-z0-9_]*)\s*\{$`, trimmed); m != nil {
			entityName := m[1]
			entity, err := parseEntityBlock(lines, &idx, entityName)
			if err != nil {
				return nil, err
			}
			app.Entities = append(app.Entities, *entity)
			continue
		}

		return nil, fmt.Errorf("line %d: unknown statement %q", cur.number, trimmed)
	}

	if app.AppName == "" {
		return nil, fmt.Errorf("missing app declaration")
	}
	if len(app.Entities) == 0 {
		return nil, fmt.Errorf("at least one entity is required")
	}
	if err := validateAuthConfig(app); err != nil {
		return nil, err
	}

	return app, nil
}

func parseAuthBlock(lines []line, idx *int) (*model.AuthConfig, error) {
	auth := &model.AuthConfig{
		EmailField:      "email",
		RoleField:       "role",
		CodeTTLMinutes:  10,
		SessionTTLHours: 24,
		EmailTransport:  "console",
		EmailFrom:       "no-reply@belm.local",
		EmailSubject:    "Your Belm login code",
		SendmailPath:    "/usr/sbin/sendmail",
		DevExposeCode:   true,
	}

	(*idx)++
	for *idx < len(lines) {
		ln := lines[*idx]
		trimmed := strings.TrimSpace(ln.text)
		if isCommentOrBlank(trimmed) {
			(*idx)++
			continue
		}
		if trimmed == "}" {
			(*idx)++
			if auth.UserEntity == "" {
				return nil, fmt.Errorf("line %d: auth.user_entity is required", ln.number)
			}
			return auth, nil
		}

		var matched bool
		if m := match(`^user_entity\s+([A-Za-z][A-Za-z0-9_]*)$`, trimmed); m != nil {
			auth.UserEntity = m[1]
			matched = true
		}
		if m := match(`^email_field\s+([a-z][A-Za-z0-9_]*)$`, trimmed); m != nil {
			auth.EmailField = m[1]
			matched = true
		}
		if m := match(`^role_field\s+([a-z][A-Za-z0-9_]*)$`, trimmed); m != nil {
			auth.RoleField = m[1]
			matched = true
		}
		if m := match(`^code_ttl_minutes\s+([0-9]{1,4})$`, trimmed); m != nil {
			auth.CodeTTLMinutes = mustInt(m[1])
			matched = true
		}
		if m := match(`^session_ttl_hours\s+([0-9]{1,4})$`, trimmed); m != nil {
			auth.SessionTTLHours = mustInt(m[1])
			matched = true
		}
		if m := match(`^email_transport\s+(console|sendmail)$`, trimmed); m != nil {
			auth.EmailTransport = m[1]
			matched = true
		}
		if m := match(`^email_from\s+"([^"]+)"$`, trimmed); m != nil {
			auth.EmailFrom = m[1]
			matched = true
		}
		if m := match(`^email_subject\s+"([^"]+)"$`, trimmed); m != nil {
			auth.EmailSubject = m[1]
			matched = true
		}
		if m := match(`^sendmail_path\s+"([^"]+)"$`, trimmed); m != nil {
			auth.SendmailPath = m[1]
			matched = true
		}
		if m := match(`^dev_expose_code\s+(true|false)$`, trimmed); m != nil {
			auth.DevExposeCode = m[1] == "true"
			matched = true
		}

		if !matched {
			return nil, fmt.Errorf("line %d: unknown auth statement %q", ln.number, trimmed)
		}
		(*idx)++
	}

	return nil, fmt.Errorf("auth block is missing closing }")
}

func parseEntityBlock(lines []line, idx *int, name string) (*model.Entity, error) {
	if !upperNameRe.MatchString(name) {
		return nil, fmt.Errorf("entity name %q is invalid", name)
	}

	ent := &model.Entity{Name: name}
	rawRules := make([]model.Rule, 0, 4)
	rawAuthz := make([]model.Authorization, 0, 4)

	(*idx)++
	for *idx < len(lines) {
		ln := lines[*idx]
		trimmed := strings.TrimSpace(ln.text)
		if isCommentOrBlank(trimmed) {
			(*idx)++
			continue
		}
		if trimmed == "}" {
			(*idx)++
			if err := finalizeEntity(ent, rawRules, rawAuthz); err != nil {
				return nil, fmt.Errorf("line %d: %w", ln.number, err)
			}
			return ent, nil
		}

		if m := match(`^rule\s+"([^"]+)"\s+when\s+(.+)$`, trimmed); m != nil {
			rawRules = append(rawRules, model.Rule{Message: strings.TrimSpace(m[1]), Expression: strings.TrimSpace(m[2])})
			(*idx)++
			continue
		}

		if m := match(`^authorize\s+(list|get|create|update|delete)\s+when\s+(.+)$`, trimmed); m != nil {
			rawAuthz = append(rawAuthz, model.Authorization{Action: m[1], Expression: strings.TrimSpace(m[2])})
			(*idx)++
			continue
		}

		if m := match(`^([a-z][A-Za-z0-9_]*)\s*:\s*(Int|String|Bool|Float)(?:\s+(.*))?$`, trimmed); m != nil {
			field := model.Field{Name: m[1], Type: m[2]}
			attrs := strings.Fields(strings.TrimSpace(m[3]))
			for _, attr := range attrs {
				switch attr {
				case "primary":
					field.Primary = true
				case "auto":
					field.Auto = true
				case "optional":
					field.Optional = true
				case "":
					// no-op
				default:
					return nil, fmt.Errorf("line %d: unknown field attribute %q", ln.number, attr)
				}
			}
			ent.Fields = append(ent.Fields, field)
			(*idx)++
			continue
		}

		return nil, fmt.Errorf("line %d: invalid entity statement %q", ln.number, trimmed)
	}

	return nil, fmt.Errorf("entity %s is missing closing }", name)
}

func finalizeEntity(ent *model.Entity, rawRules []model.Rule, rawAuthz []model.Authorization) error {
	if len(ent.Fields) == 0 {
		return fmt.Errorf("entity %s has no fields", ent.Name)
	}

	primaryCount := 0
	for _, f := range ent.Fields {
		if !fieldNameRe.MatchString(f.Name) {
			return fmt.Errorf("field name %q in %s is invalid", f.Name, ent.Name)
		}
		if f.Primary {
			primaryCount++
		}
	}
	if primaryCount > 1 {
		return fmt.Errorf("entity %s has multiple primary fields", ent.Name)
	}
	if primaryCount == 0 {
		ent.Fields = append([]model.Field{{
			Name:    "id",
			Type:    "Int",
			Primary: true,
			Auto:    true,
		}}, ent.Fields...)
	}
	for _, f := range ent.Fields {
		if f.Primary {
			ent.PrimaryKey = f.Name
			break
		}
	}
	if ent.PrimaryKey == "" {
		return fmt.Errorf("entity %s requires a primary key", ent.Name)
	}

	ent.Table = pluralize(toSnake(ent.Name))
	ent.Resource = "/" + ent.Table

	allowedVars := make(map[string]struct{}, len(ent.Fields))
	for _, f := range ent.Fields {
		allowedVars[f.Name] = struct{}{}
	}

	for _, rule := range rawRules {
		if strings.TrimSpace(rule.Message) == "" {
			return fmt.Errorf("rule message cannot be empty")
		}
		if strings.TrimSpace(rule.Expression) == "" {
			return fmt.Errorf("rule expression cannot be empty")
		}
		if _, err := expr.Parse(rule.Expression, expr.ParserOptions{AllowedVariables: allowedVars}); err != nil {
			return fmt.Errorf("invalid rule expression %q (%w)", rule.Expression, err)
		}
		ent.Rules = append(ent.Rules, rule)
	}

	seenAction := map[string]bool{}
	authVars := map[string]struct{}{
		"auth_authenticated": {},
		"auth_email":         {},
		"auth_user_id":       {},
		"auth_role":          {},
	}
	for name := range allowedVars {
		authVars[name] = struct{}{}
	}
	for _, authz := range rawAuthz {
		if seenAction[authz.Action] {
			return fmt.Errorf("duplicate authorize rule for %q", authz.Action)
		}
		seenAction[authz.Action] = true
		if _, err := expr.Parse(authz.Expression, expr.ParserOptions{AllowedVariables: authVars, AllowRoleFunc: true}); err != nil {
			return fmt.Errorf("invalid authorization expression %q (%w)", authz.Expression, err)
		}
		ent.Authorizations = append(ent.Authorizations, authz)
	}

	return nil
}

func validateAuthConfig(app *model.App) error {
	if app.Auth == nil {
		return nil
	}

	var userEntity *model.Entity
	for i := range app.Entities {
		if app.Entities[i].Name == app.Auth.UserEntity {
			userEntity = &app.Entities[i]
			break
		}
	}
	if userEntity == nil {
		return fmt.Errorf("auth.user_entity %q does not match any declared entity", app.Auth.UserEntity)
	}

	if !hasField(userEntity, app.Auth.EmailField, "String") {
		return fmt.Errorf("auth.email_field %q must exist in entity %s with type String", app.Auth.EmailField, userEntity.Name)
	}

	if app.Auth.RoleField != "" {
		if hasFieldName(userEntity, app.Auth.RoleField) && !hasField(userEntity, app.Auth.RoleField, "String") {
			return fmt.Errorf("auth.role_field %q must be String when present in entity %s", app.Auth.RoleField, userEntity.Name)
		}
	}

	return nil
}

func hasField(ent *model.Entity, name, typ string) bool {
	for _, f := range ent.Fields {
		if f.Name == name && f.Type == typ {
			return true
		}
	}
	return false
}

func hasFieldName(ent *model.Entity, name string) bool {
	for _, f := range ent.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

func isCommentOrBlank(s string) bool {
	return s == "" || strings.HasPrefix(s, "--") || strings.HasPrefix(s, "#")
}

func splitLines(source string) []line {
	raw := strings.Split(strings.ReplaceAll(source, "\r", ""), "\n")
	lines := make([]line, 0, len(raw))
	for i, text := range raw {
		lines = append(lines, line{number: i + 1, text: text})
	}
	return lines
}

func match(pattern, value string) []string {
	re := regexp.MustCompile(pattern)
	return re.FindStringSubmatch(value)
}

func mustInt(s string) int {
	n := 0
	for _, ch := range s {
		n = n*10 + int(ch-'0')
	}
	return n
}

func toSnake(v string) string {
	var b strings.Builder
	for i, ch := range v {
		if i > 0 && ch >= 'A' && ch <= 'Z' {
			b.WriteByte('_')
		}
		if ch >= 'A' && ch <= 'Z' {
			b.WriteByte(byte(ch + 32))
		} else {
			b.WriteRune(ch)
		}
	}
	return b.String()
}

func pluralize(v string) string {
	if strings.HasSuffix(v, "y") && len(v) > 1 {
		prev := v[len(v)-2]
		if !strings.ContainsRune("aeiou", rune(prev)) {
			return v[:len(v)-1] + "ies"
		}
	}
	if strings.HasSuffix(v, "s") {
		return v + "es"
	}
	return v + "s"
}
