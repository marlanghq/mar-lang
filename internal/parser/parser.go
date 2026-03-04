package parser

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"belm/internal/expr"
	"belm/internal/model"
)

var (
	upperNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	fieldNameRe = regexp.MustCompile(`^[a-z][A-Za-z0-9_]*$`)
)

const (
	defaultRequestLogsBuffer = 200
	minRequestLogsBuffer     = 10
	maxRequestLogsBuffer     = 5000
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
		Database: "app.db",
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

		if trimmed == "system {" {
			if app.System != nil {
				return nil, fmt.Errorf("line %d: system block already declared", cur.number)
			}
			systemCfg, err := parseSystemBlock(lines, &idx)
			if err != nil {
				return nil, err
			}
			app.System = systemCfg
			continue
		}

		if trimmed == "public {" {
			if app.Public != nil {
				return nil, fmt.Errorf("line %d: public block already declared", cur.number)
			}
			publicCfg, err := parsePublicBlock(lines, &idx)
			if err != nil {
				return nil, err
			}
			app.Public = publicCfg
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

		if match(`^type\s+alias\s+([A-Za-z][A-Za-z0-9_]*)\s*=.*$`, trimmed) != nil {
			alias, err := parseTypeAlias(lines, &idx)
			if err != nil {
				return nil, err
			}
			app.InputAliases = append(app.InputAliases, *alias)
			continue
		}

		if m := match(`^action\s+([a-z][A-Za-z0-9_]*)\s*\{$`, trimmed); m != nil {
			action, err := parseActionBlock(lines, &idx, m[1])
			if err != nil {
				return nil, err
			}
			app.Actions = append(app.Actions, *action)
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
	if err := validateActions(app); err != nil {
		return nil, err
	}

	return app, nil
}

// parseAuthBlock parses the auth configuration block and applies defaults.
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

// parsePublicBlock parses static frontend embedding config.
func parsePublicBlock(lines []line, idx *int) (*model.PublicConfig, error) {
	publicCfg := &model.PublicConfig{
		Mount: "/",
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
			if strings.TrimSpace(publicCfg.Dir) == "" {
				return nil, fmt.Errorf("line %d: public.dir is required", ln.number)
			}

			publicCfg.Mount = normalizePublicMount(publicCfg.Mount)
			if !strings.HasPrefix(publicCfg.Mount, "/") {
				return nil, fmt.Errorf("line %d: public.mount must start with '/'", ln.number)
			}
			if publicCfg.SPAFallback != "" {
				if strings.HasPrefix(publicCfg.SPAFallback, "/") {
					return nil, fmt.Errorf("line %d: public.spa_fallback must be a relative file path", ln.number)
				}
				if strings.Contains(publicCfg.SPAFallback, "..") {
					return nil, fmt.Errorf("line %d: public.spa_fallback cannot contain '..'", ln.number)
				}
			}
			return publicCfg, nil
		}

		var matched bool
		if m := match(`^dir\s+"([^"]+)"$`, trimmed); m != nil {
			publicCfg.Dir = m[1]
			matched = true
		}
		if m := match(`^mount\s+"([^"]+)"$`, trimmed); m != nil {
			publicCfg.Mount = m[1]
			matched = true
		}
		if m := match(`^spa_fallback\s+"([^"]+)"$`, trimmed); m != nil {
			publicCfg.SPAFallback = m[1]
			matched = true
		}

		if !matched {
			return nil, fmt.Errorf("line %d: unknown public statement %q", ln.number, trimmed)
		}
		(*idx)++
	}

	return nil, fmt.Errorf("public block is missing closing }")
}

func normalizePublicMount(mount string) string {
	value := strings.TrimSpace(mount)
	if value == "" {
		return "/"
	}
	if value != "/" {
		value = strings.TrimSuffix(value, "/")
	}
	if value == "" {
		return "/"
	}
	return value
}

// parseSystemBlock parses system-level runtime options.
func parseSystemBlock(lines []line, idx *int) (*model.SystemConfig, error) {
	cfg := &model.SystemConfig{
		RequestLogsBuffer: defaultRequestLogsBuffer,
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
			return cfg, nil
		}

		if m := match(`^request_logs_buffer\s+([0-9]{1,6})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minRequestLogsBuffer || value > maxRequestLogsBuffer {
				return nil, fmt.Errorf(
					"line %d: system.request_logs_buffer must be between %d and %d",
					ln.number,
					minRequestLogsBuffer,
					maxRequestLogsBuffer,
				)
			}
			cfg.RequestLogsBuffer = value
			(*idx)++
			continue
		}

		return nil, fmt.Errorf("line %d: unknown system statement %q", ln.number, trimmed)
	}

	return nil, fmt.Errorf("system block is missing closing }")
}

// parseEntityBlock parses a single entity body including fields, rules, and authorize clauses.
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

// finalizeEntity resolves derived metadata and validates rule/authorization expressions.
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

func parseTypeAlias(lines []line, idx *int) (*model.TypeAlias, error) {
	start := lines[*idx]
	trimmed := strings.TrimSpace(start.text)
	m := match(`^type\s+alias\s+([A-Za-z][A-Za-z0-9_]*)\s*=\s*(.*)$`, trimmed)
	if m == nil {
		return nil, fmt.Errorf("line %d: invalid type alias declaration", start.number)
	}
	name := m[1]
	rest := strings.TrimSpace(m[2])
	alias := &model.TypeAlias{Name: name, Fields: []model.AliasField{}}
	seen := map[string]bool{}

	curLine := start.number
	if rest == "" {
		(*idx)++
		for *idx < len(lines) {
			curLine = lines[*idx].number
			rest = strings.TrimSpace(lines[*idx].text)
			if isCommentOrBlank(rest) {
				(*idx)++
				continue
			}
			break
		}
	}

	if !strings.HasPrefix(rest, "{") {
		return nil, fmt.Errorf("line %d: type alias %s must start with a record. Try: type alias %s = { field : String }", curLine, name, name)
	}
	rest = strings.TrimSpace(strings.TrimPrefix(rest, "{"))
	for {
		if rest == "" {
			(*idx)++
			if *idx >= len(lines) {
				return nil, fmt.Errorf("type alias %s is missing closing }", name)
			}
			curLine = lines[*idx].number
			rest = strings.TrimSpace(lines[*idx].text)
			if isCommentOrBlank(rest) {
				continue
			}
		}

		if strings.Contains(rest, "}") {
			before, after, _ := strings.Cut(rest, "}")
			before = strings.TrimSpace(before)
			if before != "" {
				if err := parseAliasFieldToken(alias, seen, before, curLine); err != nil {
					return nil, err
				}
			}
			if strings.TrimSpace(after) != "" {
				return nil, fmt.Errorf("line %d: unexpected tokens after type alias %s record", curLine, name)
			}
			(*idx)++
			if len(alias.Fields) == 0 {
				return nil, fmt.Errorf("line %d: type alias %s must declare at least one field", start.number, name)
			}
			return alias, nil
		}

		if err := parseAliasFieldToken(alias, seen, rest, curLine); err != nil {
			return nil, err
		}
		rest = ""
	}
}

func parseAliasFieldToken(alias *model.TypeAlias, seen map[string]bool, token string, lineNo int) error {
	token = strings.TrimSpace(strings.TrimPrefix(token, ","))
	token = strings.TrimSpace(strings.TrimSuffix(token, ","))
	if token == "" {
		return nil
	}
	m := match(`^([a-z][A-Za-z0-9_]*)\s*:\s*(Int|String|Bool|Float)$`, token)
	if m == nil {
		return fmt.Errorf("line %d: invalid field in type alias %s. Expected `name : Type` with Int/String/Bool/Float", lineNo, alias.Name)
	}
	name := m[1]
	if seen[name] {
		return fmt.Errorf("line %d: duplicate field %q in type alias %s", lineNo, name, alias.Name)
	}
	seen[name] = true
	alias.Fields = append(alias.Fields, model.AliasField{Name: name, Type: m[2]})
	return nil
}

func parseActionBlock(lines []line, idx *int, name string) (*model.Action, error) {
	action := &model.Action{Name: name, Steps: []model.ActionStep{}}
	hasInput := false

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
			if !hasInput {
				return nil, fmt.Errorf("line %d: action %s is missing `input: TypeAlias`", ln.number, name)
			}
			if len(action.Steps) == 0 {
				return nil, fmt.Errorf("line %d: action %s must contain at least one `create` block", ln.number, name)
			}
			return action, nil
		}

		if m := match(`^input\s*:\s*([A-Za-z][A-Za-z0-9_]*)$`, trimmed); m != nil {
			if hasInput {
				return nil, fmt.Errorf("line %d: action %s already declares input", ln.number, name)
			}
			action.InputAlias = m[1]
			hasInput = true
			(*idx)++
			continue
		}

		if m := match(`^create\s+([A-Za-z][A-Za-z0-9_]*)\s*\{$`, trimmed); m != nil {
			step, err := parseCreateBlock(lines, idx, name, m[1])
			if err != nil {
				return nil, err
			}
			action.Steps = append(action.Steps, *step)
			continue
		}

		return nil, fmt.Errorf("line %d: invalid action statement %q", ln.number, trimmed)
	}

	return nil, fmt.Errorf("action %s is missing closing }", name)
}

func parseCreateBlock(lines []line, idx *int, actionName, entityName string) (*model.ActionStep, error) {
	step := &model.ActionStep{Kind: "create", Entity: entityName, Values: []model.ActionFieldExpr{}}
	seen := map[string]bool{}

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
			if len(step.Values) == 0 {
				return nil, fmt.Errorf("line %d: create %s in action %s must define at least one field", ln.number, entityName, actionName)
			}
			return step, nil
		}

		assign := match(`^([a-z][A-Za-z0-9_]*)\s*:\s*(.+)$`, trimmed)
		if assign == nil {
			return nil, fmt.Errorf("line %d: invalid create field %q. Expected `field: value`", ln.number, trimmed)
		}
		field := assign[1]
		if seen[field] {
			return nil, fmt.Errorf("line %d: duplicate field %q in create %s", ln.number, field, entityName)
		}
		seen[field] = true

		expr, err := parseActionFieldExpr(strings.TrimSpace(assign[2]), ln.number)
		if err != nil {
			return nil, err
		}
		expr.Field = field
		step.Values = append(step.Values, *expr)
		(*idx)++
	}

	return nil, fmt.Errorf("create %s in action %s is missing closing }", entityName, actionName)
}

func parseActionFieldExpr(raw string, lineNo int) (*model.ActionFieldExpr, error) {
	if m := match(`^input\.([a-z][A-Za-z0-9_]*)$`, raw); m != nil {
		return &model.ActionFieldExpr{
			SourceKind: "input",
			InputField: m[1],
		}, nil
	}
	if raw == "true" || raw == "false" {
		return &model.ActionFieldExpr{
			SourceKind: "literal_bool",
			Literal:    raw == "true",
		}, nil
	}
	if raw == "null" {
		return &model.ActionFieldExpr{
			SourceKind: "literal_null",
			Literal:    nil,
		}, nil
	}
	if strings.HasPrefix(raw, "\"") && strings.HasSuffix(raw, "\"") {
		unquoted, err := strconv.Unquote(raw)
		if err != nil {
			return nil, fmt.Errorf("line %d: invalid string literal %q", lineNo, raw)
		}
		return &model.ActionFieldExpr{
			SourceKind: "literal_string",
			Literal:    unquoted,
		}, nil
	}
	if m := match(`^-?[0-9]+$`, raw); m != nil {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: invalid integer literal %q", lineNo, raw)
		}
		return &model.ActionFieldExpr{
			SourceKind: "literal_int",
			Literal:    n,
		}, nil
	}
	if m := match(`^-?[0-9]+\.[0-9]+$`, raw); m != nil {
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: invalid float literal %q", lineNo, raw)
		}
		return &model.ActionFieldExpr{
			SourceKind: "literal_float",
			Literal:    f,
		}, nil
	}
	return nil, fmt.Errorf("line %d: unsupported value %q. Use input.field, string, number, bool, or null", lineNo, raw)
}

func splitCSV(value string) ([]string, error) {
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
		if ch == '\\' && inString {
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
	if inString {
		return nil, fmt.Errorf("unterminated string")
	}
	parts = append(parts, b.String())
	return parts, nil
}

// validateAuthConfig ensures auth settings reference valid fields in the selected user entity.
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

func validateActions(app *model.App) error {
	aliasByName := map[string]*model.TypeAlias{}
	for i := range app.InputAliases {
		alias := &app.InputAliases[i]
		if _, exists := aliasByName[alias.Name]; exists {
			return fmt.Errorf("duplicate type alias %q", alias.Name)
		}
		aliasByName[alias.Name] = alias
	}

	entityByName := map[string]*model.Entity{}
	for i := range app.Entities {
		entityByName[app.Entities[i].Name] = &app.Entities[i]
	}

	seenActions := map[string]bool{}
	for _, action := range app.Actions {
		if seenActions[action.Name] {
			return fmt.Errorf("duplicate action %q", action.Name)
		}
		seenActions[action.Name] = true

		alias := aliasByName[action.InputAlias]
		if alias == nil {
			return fmt.Errorf("action %s references unknown input type %q", action.Name, action.InputAlias)
		}
		aliasFieldTypes := map[string]string{}
		for _, f := range alias.Fields {
			aliasFieldTypes[f.Name] = f.Type
		}

		if len(action.Steps) == 0 {
			return fmt.Errorf("action %s must have at least one create step", action.Name)
		}
		for _, step := range action.Steps {
			if step.Kind != "create" {
				return fmt.Errorf("action %s has unsupported step kind %q", action.Name, step.Kind)
			}
			entity := entityByName[step.Entity]
			if entity == nil {
				return fmt.Errorf("action %s references unknown entity %q", action.Name, step.Entity)
			}
			assignments := map[string]model.ActionFieldExpr{}
			for _, item := range step.Values {
				field := findEntityField(entity, item.Field)
				if field == nil {
					return fmt.Errorf("action %s assigns unknown field %s.%s", action.Name, entity.Name, item.Field)
				}
				if field.Primary && field.Auto {
					return fmt.Errorf("action %s cannot assign auto-generated field %s.%s", action.Name, entity.Name, item.Field)
				}
				assignments[item.Field] = item

				sourceType, err := resolveExprType(item, aliasFieldTypes)
				if err != nil {
					return fmt.Errorf("action %s field %s.%s: %w", action.Name, entity.Name, item.Field, err)
				}
				if sourceType == "Null" {
					if !field.Optional && !field.Primary {
						return fmt.Errorf("action %s field %s.%s: null is only allowed on optional fields", action.Name, entity.Name, item.Field)
					}
					continue
				}
				if !isTypeAssignable(field.Type, sourceType) {
					return fmt.Errorf("action %s field %s.%s expects %s but got %s", action.Name, entity.Name, item.Field, field.Type, sourceType)
				}
			}

			for _, field := range entity.Fields {
				if field.Primary && field.Auto {
					continue
				}
				if field.Optional {
					continue
				}
				if _, ok := assignments[field.Name]; !ok {
					return fmt.Errorf("action %s is missing required field %s.%s", action.Name, entity.Name, field.Name)
				}
			}
		}
	}
	return nil
}

func resolveExprType(expr model.ActionFieldExpr, aliasFieldTypes map[string]string) (string, error) {
	switch expr.SourceKind {
	case "input":
		t := aliasFieldTypes[expr.InputField]
		if t == "" {
			return "", fmt.Errorf("references unknown input field %q", expr.InputField)
		}
		return t, nil
	case "literal_string":
		return "String", nil
	case "literal_int":
		return "Int", nil
	case "literal_float":
		return "Float", nil
	case "literal_bool":
		return "Bool", nil
	case "literal_null":
		return "Null", nil
	default:
		return "", fmt.Errorf("unsupported source kind %q", expr.SourceKind)
	}
}

func isTypeAssignable(targetType, sourceType string) bool {
	if targetType == sourceType {
		return true
	}
	if targetType == "Float" && sourceType == "Int" {
		return true
	}
	return false
}

func findEntityField(entity *model.Entity, name string) *model.Field {
	for i := range entity.Fields {
		if entity.Fields[i].Name == name {
			return &entity.Fields[i]
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
