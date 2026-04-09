package parser

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"mar/internal/expr"
	"mar/internal/model"
	"mar/internal/suggest"
)

var (
	upperNameRe  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	fieldNameRe  = regexp.MustCompile(`^[a-z][A-Za-z0-9_]*$`)
	envVarNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

var frontendDisplayFieldNames = []string{"name", "title", "email", "label", "slug"}

const marTypeRefPattern = `[A-Za-z][A-Za-z0-9_]*`

var (
	topLevelStatementCandidates = []string{
		"app",
		"port",
		"database",
		"ios",
		"system",
		"public",
		"frontend",
		"auth",
		"entity",
		"type",
		"type alias",
		"action",
	}
	iosStatementCandidates = []string{
		"bundle_identifier",
		"display_name",
		"server_url",
	}
	authStatementCandidates = []string{
		"code_ttl_minutes",
		"session_ttl_hours",
		"auth_request_code_rate_limit_per_minute",
		"auth_login_rate_limit_per_minute",
		"admin_ui_session_ttl_hours",
		"security_frame_policy",
		"security_referrer_policy",
		"security_content_type_nosniff",
		"email_from",
		"email_subject",
		"smtp_host",
		"smtp_port",
		"smtp_username",
		"smtp_password_env",
		"smtp_starttls",
	}
	publicStatementCandidates = []string{
		"dir",
		"mount",
		"spa_fallback",
	}
	systemStatementCandidates = []string{
		"request_logs_buffer",
		"http_max_request_body_mb",
		"sqlite_journal_mode",
		"sqlite_synchronous",
		"sqlite_foreign_keys",
		"sqlite_busy_timeout_ms",
		"sqlite_wal_autocheckpoint",
		"sqlite_journal_size_limit_mb",
		"sqlite_mmap_size_mb",
		"sqlite_cache_size_kb",
	}
)

const (
	defaultRequestLogsBuffer = 200
	minRequestLogsBuffer     = 10
	maxRequestLogsBuffer     = 5000

	minHTTPMaxRequestBodyMB = 1
	maxHTTPMaxRequestBodyMB = 1024

	minAuthRateLimitPerMinute = 1
	maxAuthRateLimitPerMinute = 10000

	minCodeTTLMinutes = 1
	maxCodeTTLMinutes = 1440

	minSessionTTLHours = 1
	maxSessionTTLHours = 8760

	minSQLiteBusyTimeoutMs = 0
	maxSQLiteBusyTimeoutMs = 600000

	minSQLiteWALAutoCheckpoint = 0
	maxSQLiteWALAutoCheckpoint = 1000000

	minSQLiteJournalSizeLimitMB = -1
	maxSQLiteJournalSizeLimitMB = 4096

	minSQLiteMmapSizeMB = 0
	maxSQLiteMmapSizeMB = 16384

	minSQLiteCacheSizeKB = 0
	maxSQLiteCacheSizeKB = 1048576
)

type line struct {
	number int
	text   string
}

// Parse reads Mar source and returns an App model.
func Parse(source string) (*model.App, error) {
	lines := splitLines(source)
	idx := 0
	var userExtension *model.Entity
	seenEntities := map[string]bool{}
	explicitDatabase := false
	authEmailSubjectExplicit := false

	app := &model.App{
		Port: 4200,
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
			if !explicitDatabase {
				app.Database = defaultDatabaseName(app.AppName)
			}
			if app.Auth != nil && !authEmailSubjectExplicit {
				app.Auth.EmailSubject = defaultAuthEmailSubject(app.AppName)
			}
			advance()
			continue
		}

		if m := match(`^port\s+([0-9]{1,5})$`, trimmed); m != nil {
			port := mustInt(m[1])
			if port < 1 || port > 65535 {
				return nil, parserErrorf("line %d: invalid port %d", cur.number, port)
			}
			app.Port = port
			advance()
			continue
		}

		if m := match(`^database\s+"([^"]+)"$`, trimmed); m != nil {
			app.Database = m[1]
			explicitDatabase = true
			advance()
			continue
		}

		if trimmed == "system {" {
			if app.System != nil {
				return nil, parserErrorf("line %d: system block already declared", cur.number)
			}
			systemCfg, err := parseSystemBlock(lines, &idx)
			if err != nil {
				return nil, err
			}
			app.System = systemCfg
			continue
		}

		if trimmed == "ios {" {
			if app.IOS != nil {
				return nil, parserErrorf("line %d: ios block already declared", cur.number)
			}
			iosCfg, err := parseIOSBlock(lines, &idx, app.AppName)
			if err != nil {
				return nil, err
			}
			app.IOS = iosCfg
			continue
		}

		if trimmed == "public {" {
			if app.Public != nil {
				return nil, parserErrorf("line %d: public block already declared", cur.number)
			}
			publicCfg, err := parsePublicBlock(lines, &idx)
			if err != nil {
				return nil, err
			}
			app.Public = publicCfg
			continue
		}

		if trimmed == "frontend {" {
			if app.Frontend != nil {
				return nil, parserErrorf("line %d: frontend block already declared", cur.number)
			}
			frontend, err := parseFrontendBlock(lines, &idx)
			if err != nil {
				return nil, err
			}
			app.Frontend = frontend
			continue
		}

		if trimmed == "auth {" {
			if app.Auth != nil {
				return nil, parserErrorf("line %d: auth block already declared", cur.number)
			}
			auth, emailSubjectExplicit, err := parseAuthBlock(lines, &idx, app.AppName)
			if err != nil {
				return nil, err
			}
			app.Auth = auth
			authEmailSubjectExplicit = emailSubjectExplicit
			continue
		}

		if m := match(`^entity\s+([A-Za-z][A-Za-z0-9_]*)\s*\{$`, trimmed); m != nil {
			entityName := m[1]
			if entityName == "User" {
				if userExtension != nil {
					return nil, parserErrorf("line %d: entity User already declared", cur.number)
				}
				entity, err := parseUserExtensionBlock(lines, &idx)
				if err != nil {
					return nil, err
				}
				userExtension = entity
				continue
			}
			if seenEntities[entityName] {
				return nil, parserErrorf("line %d: entity %q already declared", cur.number, entityName)
			}
			entity, err := parseEntityBlock(lines, &idx, entityName)
			if err != nil {
				return nil, err
			}
			seenEntities[entityName] = true
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

		if match(`^type\s+([A-Za-z][A-Za-z0-9_]*)\s*\{.*$`, trimmed) != nil {
			enumType, err := parseEnumType(lines, &idx)
			if err != nil {
				return nil, err
			}
			app.Types = append(app.Types, *enumType)
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

		return nil, unknownStatementError(cur.number, "", trimmed, topLevelStatementCandidates)
	}

	if app.AppName == "" {
		return nil, parserErrorf("missing app declaration")
	}
	if app.Auth == nil {
		app.Auth = defaultAuthConfig(app.AppName)
	}
	if err := injectImplicitUserEntity(app, userExtension); err != nil {
		return nil, err
	}
	if err := validateDeclaredTypes(app); err != nil {
		return nil, err
	}
	if err := resolveEntityRelations(app); err != nil {
		return nil, err
	}
	if err := resolveAliasRelations(app); err != nil {
		return nil, err
	}
	if err := validateEntityPredicates(app); err != nil {
		return nil, err
	}
	if err := validateAuthConfig(app); err != nil {
		return nil, err
	}
	if err := validateActions(app); err != nil {
		return nil, err
	}
	if err := validateFrontend(app); err != nil {
		return nil, err
	}
	app.Warnings = append(app.Warnings, authBootstrapWarnings(app)...)

	return app, nil
}

func defaultAuthConfig(appName string) *model.AuthConfig {
	return &model.AuthConfig{
		UserEntity:      "User",
		EmailField:      "email",
		RoleField:       "role",
		CodeTTLMinutes:  10,
		SessionTTLHours: 24,
		EmailFrom:       "no-reply@mar.local",
		EmailSubject:    defaultAuthEmailSubject(appName),
		SMTPPort:        587,
		SMTPStartTLS:    true,
	}
}

func defaultAuthEmailSubject(appName string) string {
	humanName := model.HumanizeIdentifier(appName)
	if humanName == "" {
		humanName = "Mar"
	}
	return "Your " + humanName + " login code"
}

func parseIOSBlock(lines []line, idx *int, appName string) (*model.IOSConfig, error) {
	cfg := &model.IOSConfig{}

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
			if cfg.BundleIdentifier == "" {
				return nil, parserErrorf("line %d: ios.bundle_identifier is required\n\nHint:\n  %s", ln.number, iosConfigHint(appName))
			}
			if cfg.ServerURL == "" {
				return nil, parserErrorf("line %d: ios.server_url is required\n\nHint:\n  %s", ln.number, iosConfigHint(appName))
			}
			return cfg, nil
		}

		var matched bool
		if m := match(`^bundle_identifier\s+"([^"]*)"$`, trimmed); m != nil {
			value := strings.TrimSpace(m[1])
			if !isValidIOSBundleIdentifier(value) {
				return nil, parserErrorf("line %d: ios.bundle_identifier must be a reverse-DNS identifier like \"com.example.app\"", ln.number)
			}
			cfg.BundleIdentifier = value
			matched = true
		}
		if m := match(`^display_name\s+"([^"]*)"$`, trimmed); m != nil {
			value := strings.TrimSpace(m[1])
			if value == "" {
				return nil, parserErrorf("line %d: ios.display_name must not be empty", ln.number)
			}
			cfg.DisplayName = value
			matched = true
		}
		if m := match(`^server_url\s+"([^"]*)"$`, trimmed); m != nil {
			value := strings.TrimSpace(m[1])
			if !isValidIOSServerURL(value) {
				return nil, parserErrorf("line %d: ios.server_url must be a valid http or https URL", ln.number)
			}
			cfg.ServerURL = value
			matched = true
		}

		if !matched {
			return nil, unknownStatementError(ln.number, "ios", trimmed, iosStatementCandidates)
		}
		(*idx)++
	}

	return nil, parserErrorf("ios block is missing closing }")
}

func iosConfigHint(appName string) string {
	exampleName := iosExampleName(appName)
	return "Add an ios block like:\n  ios {\n    bundle_identifier \"com.example." + exampleName + "\"\n    server_url \"https://" + exampleName + ".example.com\"\n  }"
}

func iosExampleName(appName string) string {
	var b strings.Builder
	for _, r := range appName {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	if b.Len() == 0 {
		return "app"
	}
	return b.String()
}

func isValidIOSBundleIdentifier(value string) bool {
	if value == "" || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") || !strings.Contains(value, ".") {
		return false
	}
	for _, part := range strings.Split(value, ".") {
		if part == "" {
			return false
		}
		for _, r := range part {
			if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-') {
				return false
			}
		}
	}
	return true
}

func isValidIOSServerURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed == nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return false
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return true
}

// parseAuthBlock parses the auth configuration block and applies defaults.
func parseAuthBlock(lines []line, idx *int, appName string) (*model.AuthConfig, bool, error) {
	auth := defaultAuthConfig(appName)
	emailSubjectExplicit := false

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
			return auth, emailSubjectExplicit, nil
		}

		var matched bool
		if m := match(`^code_ttl_minutes\s+([0-9]{1,4})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minCodeTTLMinutes || value > maxCodeTTLMinutes {
				return nil, false, parserErrorf(
					"line %d: auth.code_ttl_minutes must be between %d and %d",
					ln.number,
					minCodeTTLMinutes,
					maxCodeTTLMinutes,
				)
			}
			auth.CodeTTLMinutes = value
			matched = true
		} else if m := match(`^code_ttl_minutes\s+(.+)$`, trimmed); m != nil {
			return nil, false, parserErrorf("line %d: auth.code_ttl_minutes must be an integer between %d and %d.", ln.number, minCodeTTLMinutes, maxCodeTTLMinutes)
		}
		if m := match(`^session_ttl_hours\s+([0-9]{1,4})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minSessionTTLHours || value > maxSessionTTLHours {
				return nil, false, parserErrorf(
					"line %d: auth.session_ttl_hours must be an integer number of hours between %d and %d (up to 365 days)",
					ln.number,
					minSessionTTLHours,
					maxSessionTTLHours,
				)
			}
			auth.SessionTTLHours = value
			matched = true
		} else if m := match(`^session_ttl_hours\s+(.+)$`, trimmed); m != nil {
			return nil, false, parserErrorf("line %d: auth.session_ttl_hours must be an integer number of hours between %d and %d (up to 365 days).", ln.number, minSessionTTLHours, maxSessionTTLHours)
		}
		if m := match(`^auth_request_code_rate_limit_per_minute\s+([0-9]{1,5})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minAuthRateLimitPerMinute || value > maxAuthRateLimitPerMinute {
				return nil, false, parserErrorf(
					"line %d: auth.auth_request_code_rate_limit_per_minute must be between %d and %d",
					ln.number,
					minAuthRateLimitPerMinute,
					maxAuthRateLimitPerMinute,
				)
			}
			auth.AuthRequestCodeRateLimit = intPtr(value)
			matched = true
		} else if m := match(`^auth_request_code_rate_limit_per_minute\s+(.+)$`, trimmed); m != nil {
			return nil, false, parserErrorf("line %d: auth.auth_request_code_rate_limit_per_minute must be an integer between %d and %d.", ln.number, minAuthRateLimitPerMinute, maxAuthRateLimitPerMinute)
		}
		if m := match(`^auth_login_rate_limit_per_minute\s+([0-9]{1,5})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minAuthRateLimitPerMinute || value > maxAuthRateLimitPerMinute {
				return nil, false, parserErrorf(
					"line %d: auth.auth_login_rate_limit_per_minute must be between %d and %d",
					ln.number,
					minAuthRateLimitPerMinute,
					maxAuthRateLimitPerMinute,
				)
			}
			auth.AuthLoginRateLimit = intPtr(value)
			matched = true
		} else if m := match(`^auth_login_rate_limit_per_minute\s+(.+)$`, trimmed); m != nil {
			return nil, false, parserErrorf("line %d: auth.auth_login_rate_limit_per_minute must be an integer between %d and %d.", ln.number, minAuthRateLimitPerMinute, maxAuthRateLimitPerMinute)
		}
		if m := match(`^admin_ui_session_ttl_hours\s+([0-9]{1,4})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minSessionTTLHours || value > maxSessionTTLHours {
				return nil, false, parserErrorf(
					"line %d: auth.admin_ui_session_ttl_hours must be an integer number of hours between %d and %d (up to 365 days)",
					ln.number,
					minSessionTTLHours,
					maxSessionTTLHours,
				)
			}
			auth.AdminUISessionTTLHours = intPtr(value)
			matched = true
		} else if m := match(`^admin_ui_session_ttl_hours\s+(.+)$`, trimmed); m != nil {
			return nil, false, parserErrorf("line %d: auth.admin_ui_session_ttl_hours must be an integer number of hours between %d and %d (up to 365 days).", ln.number, minSessionTTLHours, maxSessionTTLHours)
		}
		if m := match(`^security_frame_policy\s+(deny|sameorigin)$`, trimmed); m != nil {
			auth.SecurityFramePolicy = stringPtr(m[1])
			matched = true
		} else if m := match(`^security_frame_policy\s+(.+)$`, trimmed); m != nil {
			return nil, false, parserErrorf(
				"line %d: auth.security_frame_policy must be one of: deny, sameorigin",
				ln.number,
			)
		}
		if m := match(`^security_referrer_policy\s+(strict-origin-when-cross-origin|no-referrer)$`, trimmed); m != nil {
			auth.SecurityReferrerPolicy = stringPtr(m[1])
			matched = true
		} else if m := match(`^security_referrer_policy\s+(.+)$`, trimmed); m != nil {
			return nil, false, parserErrorf(
				"line %d: auth.security_referrer_policy must be one of: strict-origin-when-cross-origin, no-referrer",
				ln.number,
			)
		}
		if m := match(`^security_content_type_nosniff\s+(true|false)$`, trimmed); m != nil {
			auth.SecurityContentNoSniff = boolPtr(m[1] == "true")
			matched = true
		} else if m := match(`^security_content_type_nosniff\s+(.+)$`, trimmed); m != nil {
			return nil, false, parserErrorf(
				"line %d: auth.security_content_type_nosniff must be true or false",
				ln.number,
			)
		}
		if m := match(`^email_from\s+"([^"]+)"$`, trimmed); m != nil {
			auth.EmailFrom = m[1]
			matched = true
		}
		if m := match(`^email_subject\s+"([^"]+)"$`, trimmed); m != nil {
			auth.EmailSubject = m[1]
			emailSubjectExplicit = true
			matched = true
		}
		if m := match(`^smtp_host\s+"([^"]+)"$`, trimmed); m != nil {
			auth.SMTPHost = m[1]
			matched = true
		}
		if m := match(`^smtp_port\s+([0-9]{1,5})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < 1 || value > 65535 {
				return nil, false, parserErrorf("line %d: auth.smtp_port must be between 1 and 65535", ln.number)
			}
			auth.SMTPPort = value
			matched = true
		} else if m := match(`^smtp_port\s+(.+)$`, trimmed); m != nil {
			return nil, false, parserErrorf("line %d: auth.smtp_port must be an integer between 1 and 65535.", ln.number)
		}
		if m := match(`^smtp_username\s+"([^"]+)"$`, trimmed); m != nil {
			auth.SMTPUsername = m[1]
			matched = true
		}
		if m := match(`^smtp_password_env\s+"([^"]+)"$`, trimmed); m != nil {
			auth.SMTPPasswordEnv = m[1]
			matched = true
		}
		if m := match(`^smtp_starttls\s+(true|false)$`, trimmed); m != nil {
			auth.SMTPStartTLS = m[1] == "true"
			matched = true
		} else if m := match(`^smtp_starttls\s+(.+)$`, trimmed); m != nil {
			return nil, false, parserErrorf("line %d: auth.smtp_starttls must be true or false.", ln.number)
		}
		if !matched {
			return nil, false, unknownStatementError(ln.number, "auth", trimmed, authStatementCandidates)
		}
		(*idx)++
	}

	return nil, false, parserErrorf("auth block is missing closing }")
}

func parseUserExtensionBlock(lines []line, idx *int) (*model.Entity, error) {
	ent := &model.Entity{Name: "User"}
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
			ent.Rules = rawRules
			ent.Authorizations = rawAuthz
			return ent, nil
		}

		if m := match(`^rule\s+"([^"]+)"\s+expect\s+(.+)$`, trimmed); m != nil {
			rawRules = append(rawRules, model.Rule{
				Message:    strings.TrimSpace(m[1]),
				Expression: strings.TrimSpace(m[2]),
				LineNo:     ln.number,
			})
			(*idx)++
			continue
		}

		if authz, ok, err := parseAuthorizeClause(trimmed, ln.number); ok {
			if err != nil {
				return nil, err
			}
			rawAuthz = append(rawAuthz, authz...)
			(*idx)++
			continue
		}

		if m := match(`^([a-z][A-Za-z0-9_]*)\s*:\s*(`+marTypeRefPattern+`)(?:\s+(.*))?$`, trimmed); m != nil {
			fieldName := m[1]
			field := model.Field{Name: fieldName, Type: m[2]}
			if err := parseFieldAttributes(&field, strings.TrimSpace(m[3]), ln.number); err != nil {
				return nil, err
			}
			if isBuiltInUserField(fieldName) {
				if !matchesBuiltInUserField(field) {
					return nil, parserErrorf("line %d: entity User cannot redefine built-in field %q", ln.number, fieldName)
				}
			}
			ent.Fields = append(ent.Fields, field)
			(*idx)++
			continue
		}

		if field, ok, err := parseBelongsToStatement(trimmed, ln.number); ok {
			if err != nil {
				return nil, err
			}
			ent.Fields = append(ent.Fields, *field)
			(*idx)++
			continue
		}

		return nil, parserErrorf("line %d: invalid entity statement %q", ln.number, trimmed)
	}

	return nil, parserErrorf("entity User is missing closing }")
}

func parseAuthorizeClause(trimmed string, lineNo int) ([]model.Authorization, bool, error) {
	m := match(`^authorize\s+(.+?)\s+when\s+(.+)$`, trimmed)
	if m == nil {
		return nil, false, nil
	}

	actions, err := parseAuthorizeActions(strings.TrimSpace(m[1]))
	if err != nil {
		return nil, true, parserErrorf("line %d: %w", lineNo, err)
	}
	expression := strings.TrimSpace(m[2])
	if expression == "" {
		return nil, true, parserErrorf("line %d: authorize expression cannot be empty", lineNo)
	}

	out := make([]model.Authorization, 0, len(actions))
	for _, action := range actions {
		out = append(out, model.Authorization{
			Action:     action,
			Expression: expression,
			LineNo:     lineNo,
		})
	}
	return out, true, nil
}

func containsLiteralTrueExpr(node expr.Expr) bool {
	switch n := node.(type) {
	case expr.Unary:
		return containsLiteralTrueExpr(n.Right)
	case expr.Binary:
		return containsLiteralTrueExpr(n.Left) || containsLiteralTrueExpr(n.Right)
	case expr.Call:
		for _, arg := range n.Args {
			if containsLiteralTrueExpr(arg) {
				return true
			}
		}
		return false
	}
	lit, ok := node.(expr.Literal)
	if !ok {
		return false
	}
	value, ok := lit.Value.(bool)
	return ok && value
}

func parseAuthorizeActions(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, parserErrorf("authorize must list one or more operations before when")
	}

	parts := strings.Split(raw, ",")
	actions := make([]string, 0, len(parts))
	valid := map[string]bool{
		"read":   true,
		"create": true,
		"update": true,
		"delete": true,
	}

	for _, part := range parts {
		action := strings.TrimSpace(part)
		if action == "" {
			return nil, parserErrorf("authorize operations must be separated by commas")
		}
		if !valid[action] {
			return nil, parserErrorf(
				"unknown authorize operation %q. Expected one or more of: read, create, update, delete",
				action,
			)
		}
		actions = append(actions, action)
	}

	return actions, nil
}

func isBuiltInUserField(name string) bool {
	return name == "id" || name == "email" || name == "role"
}

func matchesBuiltInUserField(field model.Field) bool {
	switch field.Name {
	case "id":
		return field.Type == "Int" && field.Primary && field.Auto && !field.Optional && field.Default == nil
	case "email":
		return field.Type == "String" && !field.Primary && !field.Auto && !field.Optional && field.Default == nil
	case "role":
		return !field.Primary && !field.Auto && !field.Optional && field.Default == nil && field.RelationEntity == "" && !field.CurrentUser
	default:
		return false
	}
}

func injectImplicitUserEntity(app *model.App, extension *model.Entity) error {
	user := model.Entity{
		Name: "User",
		Fields: []model.Field{
			{Name: "id", Type: "Int", Primary: true, Auto: true},
			{Name: "email", Type: "String"},
			{Name: "role", Type: "String"},
		},
	}

	rawRules := []model.Rule{}
	rawAuthz := []model.Authorization{}
	if extension != nil {
		for _, field := range extension.Fields {
			replaced := false
			for i := range user.Fields {
				if user.Fields[i].Name == field.Name {
					user.Fields[i] = field
					replaced = true
					break
				}
			}
			if !replaced {
				user.Fields = append(user.Fields, field)
			}
		}
		rawRules = append(rawRules, extension.Rules...)
		rawAuthz = append(rawAuthz, extension.Authorizations...)
	}

	if err := finalizeEntity(&user, rawRules, rawAuthz); err != nil {
		return err
	}

	app.Entities = append([]model.Entity{user}, app.Entities...)
	return nil
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
				return nil, parserErrorf("line %d: public.dir is required", ln.number)
			}

			publicCfg.Mount = normalizePublicMount(publicCfg.Mount)
			if !strings.HasPrefix(publicCfg.Mount, "/") {
				return nil, parserErrorf("line %d: public.mount must start with '/'", ln.number)
			}
			if publicCfg.SPAFallback != "" {
				if strings.HasPrefix(publicCfg.SPAFallback, "/") {
					return nil, parserErrorf("line %d: public.spa_fallback must be a relative file path", ln.number)
				}
				if strings.Contains(publicCfg.SPAFallback, "..") {
					return nil, parserErrorf("line %d: public.spa_fallback cannot contain '..'", ln.number)
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
			return nil, unknownStatementError(ln.number, "public", trimmed, publicStatementCandidates)
		}
		(*idx)++
	}

	return nil, parserErrorf("public block is missing closing }")
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

func parseFrontendBlock(lines []line, idx *int) (*model.Frontend, error) {
	frontend := &model.Frontend{Screens: []model.FrontendScreen{}}

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
			if len(frontend.Screens) == 0 {
				return nil, parserErrorf("line %d: frontend must declare at least one screen", ln.number)
			}
			return frontend, nil
		}
		if m := match(`^screen\s+([A-Za-z][A-Za-z0-9_]*)(?:\s+for\s+([A-Za-z][A-Za-z0-9_]*))?\s*\{$`, trimmed); m != nil {
			screen, err := parseFrontendScreenBlock(lines, idx, m[1], m[2])
			if err != nil {
				return nil, err
			}
			frontend.Screens = append(frontend.Screens, *screen)
			continue
		}

		return nil, parserErrorf("line %d: invalid frontend statement %q", ln.number, trimmed)
	}

	return nil, parserErrorf("frontend block is missing closing }")
}

func parseFrontendScreenBlock(lines []line, idx *int, name, forEntity string) (*model.FrontendScreen, error) {
	screen := &model.FrontendScreen{
		Name:         name,
		ForEntity:    forEntity,
		ToolbarItems: []model.FrontendToolbarItem{},
		Sections:     []model.FrontendSection{},
		LineNo:       lines[*idx].number,
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
			return screen, nil
		}
		if m := match(`^title\s+"([^"]+)"$`, trimmed); m != nil {
			screen.Title = m[1]
			screen.TitleExpression = ""
			screen.TitleLineNo = ln.number
			(*idx)++
			continue
		}
		if m := match(`^title\s+(.+)$`, trimmed); m != nil {
			screen.Title = ""
			screen.TitleExpression = strings.TrimSpace(m[1])
			screen.TitleLineNo = ln.number
			(*idx)++
			continue
		}
		if m := match(`^section(?:\s+"([^"]+)")?(?:\s+when\s+(.+))?\s*\{$`, trimmed); m != nil {
			section, err := parseFrontendSectionBlock(lines, idx, m[1], strings.TrimSpace(m[2]))
			if err != nil {
				return nil, err
			}
			screen.Sections = append(screen.Sections, *section)
			continue
		}
		if trimmed == "toolbar {" {
			items, err := parseFrontendToolbarBlock(lines, idx)
			if err != nil {
				return nil, err
			}
			screen.ToolbarItems = append(screen.ToolbarItems, items...)
			continue
		}

		return nil, parserErrorf("line %d: invalid screen statement %q", ln.number, trimmed)
	}

	return nil, parserErrorf("screen %s is missing closing }", name)
}

func parseFrontendToolbarBlock(lines []line, idx *int) ([]model.FrontendToolbarItem, error) {
	items := []model.FrontendToolbarItem{}

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
			return items, nil
		}
		if m := match(`^(primary|trailing)\s+create\s+([A-Za-z][A-Za-z0-9_]*)$`, trimmed); m != nil {
			items = append(items, model.FrontendToolbarItem{
				Placement: m[1],
				LineNo:    ln.number,
				Item: model.FrontendItem{
					Kind:   "create",
					Entity: m[2],
					LineNo: ln.number,
				},
			})
			(*idx)++
			continue
		}
		if m := match(`^(primary|trailing)\s+edit\s+list\s+([A-Za-z][A-Za-z0-9_]*)$`, trimmed); m != nil {
			items = append(items, model.FrontendToolbarItem{
				Placement: m[1],
				LineNo:    ln.number,
				Item: model.FrontendItem{
					Kind:   "editList",
					Entity: m[2],
					LineNo: ln.number,
				},
			})
			(*idx)++
			continue
		}
		if m := match(`^(primary|trailing)\s+edit$`, trimmed); m != nil {
			items = append(items, model.FrontendToolbarItem{
				Placement: m[1],
				LineNo:    ln.number,
				Item: model.FrontendItem{
					Kind:   "edit",
					LineNo: ln.number,
				},
			})
			(*idx)++
			continue
		}

		return nil, parserErrorf("line %d: invalid toolbar statement %q", ln.number, trimmed)
	}

	return nil, parserErrorf("frontend toolbar is missing closing }")
}

func parseFrontendSectionBlock(lines []line, idx *int, title, when string) (*model.FrontendSection, error) {
	section := &model.FrontendSection{
		Title:      title,
		When:       when,
		Items:      []model.FrontendItem{},
		LineNo:     lines[*idx].number,
		WhenLineNo: lines[*idx].number,
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
			return section, nil
		}
		if m := match(`^link\s+"([^"]+)"\s+to\s+([A-Za-z][A-Za-z0-9_]*)(?:\s+when\s+(.+))?$`, trimmed); m != nil {
			item := model.FrontendItem{
				Kind:         "link",
				Label:        m[1],
				Target:       m[2],
				Filter:       strings.TrimSpace(m[3]),
				LineNo:       ln.number,
				FilterLineNo: ln.number,
			}
			section.Items = append(section.Items, item)
			(*idx)++
			continue
		}
		if m := match(`^field\s+([a-z][A-Za-z0-9_]*(?:\.[a-z][A-Za-z0-9_]*)?)$`, trimmed); m != nil {
			section.Items = append(section.Items, model.FrontendItem{Kind: "field", Field: m[1], LineNo: ln.number})
			(*idx)++
			continue
		}
		if trimmed == "edit" {
			section.Items = append(section.Items, model.FrontendItem{Kind: "edit", LineNo: ln.number})
			(*idx)++
			continue
		}
		if trimmed == "edit {" {
			item := &model.FrontendItem{Kind: "edit", FormFields: []model.FrontendFormField{}, LineNo: ln.number}
			if err := parseFrontendItemValues(lines, idx, item, "edit", "current"); err != nil {
				return nil, err
			}
			section.Items = append(section.Items, *item)
			continue
		}
		if trimmed == "delete" {
			section.Items = append(section.Items, model.FrontendItem{Kind: "delete", LineNo: ln.number})
			(*idx)++
			continue
		}
		if m := match(`^create\s+([A-Za-z][A-Za-z0-9_]*)\s*\{$`, trimmed); m != nil {
			item, err := parseFrontendCreateBlock(lines, idx, m[1])
			if err != nil {
				return nil, err
			}
			section.Items = append(section.Items, *item)
			continue
		}
		if m := match(`^create\s+([A-Za-z][A-Za-z0-9_]*)$`, trimmed); m != nil {
			section.Items = append(section.Items, model.FrontendItem{Kind: "create", Entity: m[1], LineNo: ln.number})
			(*idx)++
			continue
		}
		if m := match(`^list\s+([A-Za-z][A-Za-z0-9_]*)(?:\s+where\s+(.+))?\s*\{$`, trimmed); m != nil {
			item, err := parseFrontendListBlock(lines, idx, "list", m[1], "", strings.TrimSpace(m[2]))
			if err != nil {
				return nil, err
			}
			section.Items = append(section.Items, *item)
			continue
		}
		if m := match(`^children\s+([A-Za-z][A-Za-z0-9_]*)\s+by\s+([a-z][A-Za-z0-9_]*)(?:\s+where\s+(.+))?\s*\{$`, trimmed); m != nil {
			item, err := parseFrontendListBlock(lines, idx, "children", m[1], m[2], strings.TrimSpace(m[3]))
			if err != nil {
				return nil, err
			}
			section.Items = append(section.Items, *item)
			continue
		}
		if m := match(`^report\s+([A-Za-z][A-Za-z0-9_]*)(?:\s+where\s+(.+))?\s*\{$`, trimmed); m != nil {
			item, err := parseFrontendReportBlock(lines, idx, m[1], strings.TrimSpace(m[2]))
			if err != nil {
				return nil, err
			}
			section.Items = append(section.Items, *item)
			continue
		}
		if m := match(`^action\s+([a-z][A-Za-z0-9_]*)\s*\{$`, trimmed); m != nil {
			item, err := parseFrontendActionBlock(lines, idx, m[1])
			if err != nil {
				return nil, err
			}
			section.Items = append(section.Items, *item)
			continue
		}

		return nil, parserErrorf("line %d: invalid frontend section statement %q", ln.number, trimmed)
	}

	return nil, parserErrorf("frontend section is missing closing }")
}

func parseFrontendListBlock(lines []line, idx *int, kind, entity, relationField, filter string) (*model.FrontendItem, error) {
	item := &model.FrontendItem{
		Kind:          kind,
		Entity:        entity,
		RelationField: relationField,
		Filter:        filter,
		LineNo:        lines[*idx].number,
		FilterLineNo:  lines[*idx].number,
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
			return item, nil
		}
		if m := match(`^title\s+([a-z][A-Za-z0-9_]*)$`, trimmed); m != nil {
			item.TitleField = m[1]
			(*idx)++
			continue
		}
		if m := match(`^subtitle\s+([a-z][A-Za-z0-9_]*)$`, trimmed); m != nil {
			item.SubtitleField = m[1]
			(*idx)++
			continue
		}
		if m := match(`^destination\s+([A-Za-z][A-Za-z0-9_]*)$`, trimmed); m != nil {
			item.Destination = m[1]
			(*idx)++
			continue
		}

		return nil, parserErrorf("line %d: invalid frontend list statement %q", ln.number, trimmed)
	}

	return nil, parserErrorf("frontend %s %s is missing closing }", kind, entity)
}

func parseFrontendReportBlock(lines []line, idx *int, entity, filter string) (*model.FrontendItem, error) {
	item := &model.FrontendItem{
		Kind:          "report",
		Entity:        entity,
		Filter:        filter,
		ReportMetrics: []model.FrontendReportMetric{},
		LineNo:        lines[*idx].number,
		FilterLineNo:  lines[*idx].number,
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
			return item, nil
		}
		if m := match(`^group\s+by\s+(.+)$`, trimmed); m != nil {
			if item.ReportGroup != "" {
				return nil, parserErrorf("line %d: duplicate frontend report group", ln.number)
			}
			item.ReportGroup = strings.TrimSpace(m[1])
			(*idx)++
			continue
		}
		if m := match(`^metric\s+([a-z]+)\(([^)]*)\)(?:\s+label\s+"([^"]+)")?$`, trimmed); m != nil {
			item.ReportMetrics = append(item.ReportMetrics, model.FrontendReportMetric{
				Aggregate: strings.TrimSpace(m[1]),
				Field:     strings.TrimSpace(m[2]),
				Label:     strings.TrimSpace(m[3]),
				LineNo:    ln.number,
			})
			(*idx)++
			continue
		}

		return nil, parserErrorf("line %d: invalid frontend report statement %q", ln.number, trimmed)
	}

	return nil, parserErrorf("frontend report %s is missing closing }", entity)
}

func parseFrontendActionBlock(lines []line, idx *int, name string) (*model.FrontendItem, error) {
	item := &model.FrontendItem{Kind: "action", Action: name, Values: []model.FrontendActionValue{}, FormFields: []model.FrontendFormField{}, LineNo: lines[*idx].number}
	if err := parseFrontendItemValues(lines, idx, item, "action", name); err != nil {
		return nil, err
	}
	return item, nil
}

func parseFrontendCreateBlock(lines []line, idx *int, entity string) (*model.FrontendItem, error) {
	item := &model.FrontendItem{Kind: "create", Entity: entity, Values: []model.FrontendActionValue{}, FormFields: []model.FrontendFormField{}, LineNo: lines[*idx].number}
	if err := parseFrontendItemValues(lines, idx, item, "create", entity); err != nil {
		return nil, err
	}
	return item, nil
}

func parseFrontendItemValues(lines []line, idx *int, item *model.FrontendItem, kind, name string) error {
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
			return nil
		}
		if trimmed == "form {" {
			if len(item.FormFields) > 0 {
				return parserErrorf("line %d: frontend %s %s already declares form fields", ln.number, kind, name)
			}
			formFields, err := parseFrontendFormFields(lines, idx, kind, name)
			if err != nil {
				return err
			}
			item.FormFields = formFields
			continue
		}
		m := match(`^([a-z][A-Za-z0-9_]*)\s*=\s*(.+)$`, trimmed)
		if m == nil {
			return parserErrorf("line %d: invalid frontend %s value %q. Expected `field = expression` or `form { ... }`", ln.number, kind, trimmed)
		}
		field := m[1]
		if seen[field] {
			return parserErrorf("line %d: duplicate frontend %s value %q", ln.number, kind, field)
		}
		seen[field] = true
		item.Values = append(item.Values, model.FrontendActionValue{Field: field, Expression: strings.TrimSpace(m[2]), LineNo: ln.number})
		(*idx)++
	}

	return parserErrorf("frontend %s %s is missing closing }", kind, name)
}

func parseFrontendFormFields(lines []line, idx *int, kind, name string) ([]model.FrontendFormField, error) {
	fields := []model.FrontendFormField{}
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
			return fields, nil
		}
		m := match(`^field\s+([a-z][A-Za-z0-9_]*)(?:\s+where\s+(.+))?$`, trimmed)
		if m == nil {
			return nil, parserErrorf("line %d: invalid frontend %s form field %q. Expected `field name` or `field name where relation == form.otherField`", ln.number, kind, trimmed)
		}
		fieldName := m[1]
		if seen[fieldName] {
			return nil, parserErrorf("line %d: duplicate frontend %s form field %q", ln.number, kind, fieldName)
		}
		seen[fieldName] = true
		fields = append(fields, model.FrontendFormField{
			Field:        fieldName,
			Filter:       strings.TrimSpace(m[2]),
			LineNo:       ln.number,
			FilterLineNo: ln.number,
		})
		(*idx)++
	}

	return nil, parserErrorf("frontend %s %s form is missing closing }", kind, name)
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
				return nil, parserErrorf(
					"line %d: system.request_logs_buffer must be between %d and %d",
					ln.number,
					minRequestLogsBuffer,
					maxRequestLogsBuffer,
				)
			}
			cfg.RequestLogsBuffer = value
			(*idx)++
			continue
		} else if m := match(`^request_logs_buffer\s+(.+)$`, trimmed); m != nil {
			return nil, parserErrorf("line %d: system.request_logs_buffer must be an integer between %d and %d.", ln.number, minRequestLogsBuffer, maxRequestLogsBuffer)
		}
		if m := match(`^http_max_request_body_mb\s+([0-9]{1,4})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minHTTPMaxRequestBodyMB || value > maxHTTPMaxRequestBodyMB {
				return nil, parserErrorf(
					"line %d: system.http_max_request_body_mb must be between %d and %d",
					ln.number,
					minHTTPMaxRequestBodyMB,
					maxHTTPMaxRequestBodyMB,
				)
			}
			cfg.HTTPMaxRequestBodyMB = intPtr(value)
			(*idx)++
			continue
		} else if m := match(`^http_max_request_body_mb\s+(.+)$`, trimmed); m != nil {
			return nil, parserErrorf("line %d: system.http_max_request_body_mb must be an integer between %d and %d.", ln.number, minHTTPMaxRequestBodyMB, maxHTTPMaxRequestBodyMB)
		}
		if m := match(`^sqlite_journal_mode\s+(wal|delete|truncate|persist|memory|off)$`, trimmed); m != nil {
			cfg.SQLiteJournalMode = stringPtr(m[1])
			(*idx)++
			continue
		} else if m := match(`^sqlite_journal_mode\s+(.+)$`, trimmed); m != nil {
			return nil, parserErrorf("line %d: system.sqlite_journal_mode must be one of: wal, delete, truncate, persist, memory, off.", ln.number)
		}
		if m := match(`^sqlite_synchronous\s+(off|normal|full|extra)$`, trimmed); m != nil {
			cfg.SQLiteSynchronous = stringPtr(m[1])
			(*idx)++
			continue
		} else if m := match(`^sqlite_synchronous\s+(.+)$`, trimmed); m != nil {
			return nil, parserErrorf("line %d: system.sqlite_synchronous must be one of: off, normal, full, extra.", ln.number)
		}
		if m := match(`^sqlite_foreign_keys\s+(true|false)$`, trimmed); m != nil {
			cfg.SQLiteForeignKeys = boolPtr(m[1] == "true")
			(*idx)++
			continue
		} else if m := match(`^sqlite_foreign_keys\s+(.+)$`, trimmed); m != nil {
			return nil, parserErrorf("line %d: system.sqlite_foreign_keys must be true or false.", ln.number)
		}
		if m := match(`^sqlite_busy_timeout_ms\s+([0-9]{1,7})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minSQLiteBusyTimeoutMs || value > maxSQLiteBusyTimeoutMs {
				return nil, parserErrorf(
					"line %d: system.sqlite_busy_timeout_ms must be between %d and %d",
					ln.number,
					minSQLiteBusyTimeoutMs,
					maxSQLiteBusyTimeoutMs,
				)
			}
			cfg.SQLiteBusyTimeoutMs = intPtr(value)
			(*idx)++
			continue
		} else if m := match(`^sqlite_busy_timeout_ms\s+(.+)$`, trimmed); m != nil {
			return nil, parserErrorf("line %d: system.sqlite_busy_timeout_ms must be an integer between %d and %d.", ln.number, minSQLiteBusyTimeoutMs, maxSQLiteBusyTimeoutMs)
		}
		if m := match(`^sqlite_wal_autocheckpoint\s+([0-9]{1,7})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minSQLiteWALAutoCheckpoint || value > maxSQLiteWALAutoCheckpoint {
				return nil, parserErrorf(
					"line %d: system.sqlite_wal_autocheckpoint must be between %d and %d",
					ln.number,
					minSQLiteWALAutoCheckpoint,
					maxSQLiteWALAutoCheckpoint,
				)
			}
			cfg.SQLiteWALAutoCheckpoint = intPtr(value)
			(*idx)++
			continue
		} else if m := match(`^sqlite_wal_autocheckpoint\s+(.+)$`, trimmed); m != nil {
			return nil, parserErrorf("line %d: system.sqlite_wal_autocheckpoint must be an integer between %d and %d.", ln.number, minSQLiteWALAutoCheckpoint, maxSQLiteWALAutoCheckpoint)
		}
		if m := match(`^sqlite_journal_size_limit_mb\s+(-?[0-9]{1,4})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minSQLiteJournalSizeLimitMB || value > maxSQLiteJournalSizeLimitMB {
				return nil, parserErrorf(
					"line %d: system.sqlite_journal_size_limit_mb must be between %d and %d",
					ln.number,
					minSQLiteJournalSizeLimitMB,
					maxSQLiteJournalSizeLimitMB,
				)
			}
			cfg.SQLiteJournalSizeLimitMB = intPtr(value)
			(*idx)++
			continue
		} else if m := match(`^sqlite_journal_size_limit_mb\s+(.+)$`, trimmed); m != nil {
			return nil, parserErrorf("line %d: system.sqlite_journal_size_limit_mb must be an integer between %d and %d.", ln.number, minSQLiteJournalSizeLimitMB, maxSQLiteJournalSizeLimitMB)
		}
		if m := match(`^sqlite_mmap_size_mb\s+([0-9]{1,5})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minSQLiteMmapSizeMB || value > maxSQLiteMmapSizeMB {
				return nil, parserErrorf(
					"line %d: system.sqlite_mmap_size_mb must be between %d and %d",
					ln.number,
					minSQLiteMmapSizeMB,
					maxSQLiteMmapSizeMB,
				)
			}
			cfg.SQLiteMmapSizeMB = intPtr(value)
			(*idx)++
			continue
		} else if m := match(`^sqlite_mmap_size_mb\s+(.+)$`, trimmed); m != nil {
			return nil, parserErrorf("line %d: system.sqlite_mmap_size_mb must be an integer between %d and %d.", ln.number, minSQLiteMmapSizeMB, maxSQLiteMmapSizeMB)
		}
		if m := match(`^sqlite_cache_size_kb\s+([0-9]{1,7})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minSQLiteCacheSizeKB || value > maxSQLiteCacheSizeKB {
				return nil, parserErrorf(
					"line %d: system.sqlite_cache_size_kb must be between %d and %d",
					ln.number,
					minSQLiteCacheSizeKB,
					maxSQLiteCacheSizeKB,
				)
			}
			cfg.SQLiteCacheSizeKB = intPtr(value)
			(*idx)++
			continue
		} else if m := match(`^sqlite_cache_size_kb\s+(.+)$`, trimmed); m != nil {
			return nil, parserErrorf("line %d: system.sqlite_cache_size_kb must be an integer between %d and %d.", ln.number, minSQLiteCacheSizeKB, maxSQLiteCacheSizeKB)
		}

		return nil, unknownStatementError(ln.number, "system", trimmed, systemStatementCandidates)
	}

	return nil, parserErrorf("system block is missing closing }")
}

func stringPtr(v string) *string {
	return &v
}

func intPtr(v int) *int {
	return &v
}

func boolPtr(v bool) *bool {
	return &v
}

type punctuatedParserError struct {
	message string
	base    error
}

func (e punctuatedParserError) Error() string {
	return e.message
}

func (e punctuatedParserError) Unwrap() error {
	return e.base
}

func parserErrorf(format string, args ...any) error {
	base := fmt.Errorf(format, args...)
	message := finalizeParserErrorMessage(base.Error())
	if message == base.Error() {
		return base
	}
	return punctuatedParserError{
		message: message,
		base:    base,
	}
}

func parserErrorAtLinef(lineNo int, format string, args ...any) error {
	if lineNo > 0 {
		lineArgs := append([]any{lineNo}, args...)
		return parserErrorf("line %d: "+format, lineArgs...)
	}
	return parserErrorf(format, args...)
}

func finalizeParserErrorMessage(message string) string {
	if idx := strings.LastIndex(message, "\n\nHint:\n"); idx >= 0 {
		base := finalizeParserErrorMessage(message[:idx])
		return base + message[idx:]
	}

	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return message
	}
	last := trimmed[len(trimmed)-1]
	if last == '.' || last == '?' {
		return message
	}
	return message + "."
}

func unknownStatementError(lineNumber int, scope, trimmed string, candidates []string) error {
	label := "unknown statement"
	if strings.TrimSpace(scope) != "" {
		label = "unknown " + strings.TrimSpace(scope) + " statement"
	}
	key := statementSuggestionKey(trimmed)
	base := fmt.Sprintf("line %d: %s %q%s", lineNumber, label, trimmed, suggest.DidYouMeanSuffix(key, candidates))
	if hint := misplacedStatementHint(scope, key); hint != "" {
		base += "\n\nHint:\n  " + hint
	}
	return parserErrorf("%s", base)
}

func misplacedStatementHint(scope, key string) string {
	current := strings.TrimSpace(scope)
	if current == "" || key == "" {
		return ""
	}

	targetScope := ""
	switch {
	case candidateContains(authStatementCandidates, key):
		targetScope = "auth"
	case candidateContains(iosStatementCandidates, key):
		targetScope = "ios"
	case candidateContains(systemStatementCandidates, key):
		targetScope = "system"
	case candidateContains(publicStatementCandidates, key):
		targetScope = "public"
	}

	if targetScope == "" || targetScope == current {
		return ""
	}

	switch targetScope {
	case "auth":
		return fmt.Sprintf("%q looks like an auth setting. Try moving it into auth { ... }.", key)
	case "ios":
		return fmt.Sprintf("%q looks like an iOS setting. Try moving it into ios { ... }.", key)
	case "system":
		return fmt.Sprintf("%q looks like a system setting. Try moving it into system { ... }.", key)
	case "public":
		return fmt.Sprintf("%q looks like a public setting. Try moving it into public { ... }.", key)
	default:
		return ""
	}
}

func candidateContains(candidates []string, key string) bool {
	for _, candidate := range candidates {
		if candidate == key {
			return true
		}
	}
	return false
}

func hasLinePrefixedError(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "line ")
}

func statementSuggestionKey(trimmed string) string {
	parts := strings.Fields(strings.TrimSpace(trimmed))
	if len(parts) == 0 {
		return ""
	}
	if parts[0] == "type" && len(parts) == 1 {
		return parts[0]
	}
	if parts[0] == "type" && len(parts) > 1 {
		return parts[0] + " " + parts[1]
	}
	return parts[0]
}

// parseEntityBlock parses a single entity body including fields, rules, and authorize clauses.
func parseEntityBlock(lines []line, idx *int, name string) (*model.Entity, error) {
	if !upperNameRe.MatchString(name) {
		return nil, parserErrorf("entity name %q is invalid", name)
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
				if hasLinePrefixedError(err) {
					return nil, err
				}
				return nil, parserErrorf("line %d: %w", ln.number, err)
			}
			return ent, nil
		}

		if m := match(`^rule\s+"([^"]+)"\s+expect\s+(.+)$`, trimmed); m != nil {
			rawRules = append(rawRules, model.Rule{
				Message:    strings.TrimSpace(m[1]),
				Expression: strings.TrimSpace(m[2]),
				LineNo:     ln.number,
			})
			(*idx)++
			continue
		}

		if authz, ok, err := parseAuthorizeClause(trimmed, ln.number); ok {
			if err != nil {
				return nil, err
			}
			rawAuthz = append(rawAuthz, authz...)
			(*idx)++
			continue
		}

		if m := match(`^([a-z][A-Za-z0-9_]*)\s*:\s*(`+marTypeRefPattern+`)(?:\s+(.*))?$`, trimmed); m != nil {
			field := model.Field{Name: m[1], Type: m[2]}
			if err := parseFieldAttributes(&field, strings.TrimSpace(m[3]), ln.number); err != nil {
				return nil, err
			}
			ent.Fields = append(ent.Fields, field)
			(*idx)++
			continue
		}

		if field, ok, err := parseBelongsToStatement(trimmed, ln.number); ok {
			if err != nil {
				return nil, err
			}
			ent.Fields = append(ent.Fields, *field)
			(*idx)++
			continue
		}

		return nil, parserErrorf("line %d: invalid entity statement %q", ln.number, trimmed)
	}

	return nil, parserErrorf("entity %s is missing closing }", name)
}

// finalizeEntity resolves derived metadata and validates rule/authorization expressions.
func finalizeEntity(ent *model.Entity, rawRules []model.Rule, rawAuthz []model.Authorization) error {
	if len(ent.Fields) == 0 {
		return parserErrorf("entity %s has no fields", ent.Name)
	}
	ent.Fields = append(ent.Fields,
		model.Field{Name: "created_at", Type: "DateTime", Auto: true},
		model.Field{Name: "updated_at", Type: "DateTime", Auto: true},
	)

	primaryCount := 0
	seenFields := map[string]bool{}
	for _, f := range ent.Fields {
		if !fieldNameRe.MatchString(f.Name) {
			return parserErrorf("field name %q in %s is invalid", f.Name, ent.Name)
		}
		if seenFields[f.Name] {
			return parserErrorf("duplicate field %q in %s", f.Name, ent.Name)
		}
		seenFields[f.Name] = true
		if f.Default != nil && f.Primary {
			return parserErrorf("field %s in %s cannot use default together with primary", f.Name, ent.Name)
		}
		if f.Default != nil && f.Auto {
			return parserErrorf("field %s in %s cannot use default together with auto", f.Name, ent.Name)
		}
		if f.Primary {
			primaryCount++
		}
	}
	if primaryCount > 1 {
		return parserErrorf("entity %s has multiple primary fields", ent.Name)
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
		return parserErrorf("entity %s requires a primary key", ent.Name)
	}

	ent.Table = pluralize(toSnake(ent.Name))
	ent.Resource = "/" + ent.Table

	allowedVars := make(map[string]struct{}, len(ent.Fields))
	for _, f := range ent.Fields {
		allowedVars[f.Name] = struct{}{}
	}

	for _, rule := range rawRules {
		if strings.TrimSpace(rule.Message) == "" {
			if rule.LineNo > 0 {
				return parserErrorf("line %d: rule message cannot be empty", rule.LineNo)
			}
			return parserErrorf("rule message cannot be empty")
		}
		if strings.TrimSpace(rule.Expression) == "" {
			if rule.LineNo > 0 {
				return parserErrorf("line %d: rule expression cannot be empty", rule.LineNo)
			}
			return parserErrorf("rule expression cannot be empty")
		}
		ruleAllowedVars := allowedVariablesForRawExpression(rule.Expression, allowedVars, false)
		if _, err := expr.Parse(rule.Expression, expr.ParserOptions{AllowedVariables: ruleAllowedVars}); err != nil {
			if rule.LineNo > 0 {
				return parserErrorf("line %d: invalid rule expression %q (%w)", rule.LineNo, rule.Expression, err)
			}
			return parserErrorf("invalid rule expression %q (%w)", rule.Expression, err)
		}
		ent.Rules = append(ent.Rules, rule)
	}

	seenAction := map[string]bool{}
	for _, authz := range rawAuthz {
		if seenAction[authz.Action] {
			if authz.LineNo > 0 {
				return parserErrorf("line %d: duplicate authorize rule for %q", authz.LineNo, authz.Action)
			}
			return parserErrorf("duplicate authorize rule for %q", authz.Action)
		}
		seenAction[authz.Action] = true
		authAllowedVars := allowedVariablesForRawExpression(authz.Expression, allowedVars, true)
		parsed, err := expr.Parse(authz.Expression, expr.ParserOptions{AllowedVariables: authAllowedVars})
		if err != nil {
			if authz.LineNo > 0 {
				return parserErrorf("line %d: invalid authorization expression %q (%w)", authz.LineNo, authz.Expression, err)
			}
			return parserErrorf("invalid authorization expression %q (%w)", authz.Expression, err)
		}
		if containsLiteralTrueExpr(parsed) {
			if authz.LineNo > 0 {
				return parserErrorf("line %d: authorization expressions cannot use true. Use anonymous or user_authenticated instead", authz.LineNo)
			}
			return parserErrorf("authorization expressions cannot use true. Use anonymous or user_authenticated instead")
		}
		ent.Authorizations = append(ent.Authorizations, authz)
	}

	return nil
}

func validateEntityPredicates(app *model.App) error {
	if app == nil {
		return nil
	}
	enumLiteralTypes := appEnumLiteralTypes(app)
	for i := range app.Entities {
		ent := &app.Entities[i]
		variableTypes := make(map[string]string, len(ent.Fields))
		for _, field := range ent.Fields {
			variableTypes[field.Name] = field.Type
		}
		for _, rule := range ent.Rules {
			if err := validateBooleanExpr(rule.Expression, variableTypes, nil, false, enumLiteralTypes); err != nil {
				if rule.LineNo > 0 {
					return parserErrorf("line %d: invalid rule expression %q (%w)", rule.LineNo, rule.Expression, err)
				}
				return parserErrorf("invalid rule expression %q (%w)", rule.Expression, err)
			}
		}
		for _, authz := range ent.Authorizations {
			authBuiltins := authBuiltinTypes(app)
			if err := validateBooleanExpr(authz.Expression, variableTypes, authBuiltins, true, enumLiteralTypes); err != nil {
				if authz.LineNo > 0 {
					return parserErrorf("line %d: invalid authorization expression %q (%w)", authz.LineNo, authz.Expression, err)
				}
				return parserErrorf("invalid authorization expression %q (%w)", authz.Expression, err)
			}
		}
	}
	return nil
}

func validateDeclaredTypes(app *model.App) error {
	if app == nil {
		return nil
	}

	typesByName := map[string]*model.EnumType{}
	literalOwners := map[string]string{}
	for i := range app.Types {
		enumType := &app.Types[i]
		if _, exists := typesByName[enumType.Name]; exists {
			return parserErrorf("duplicate type %q", enumType.Name)
		}
		typesByName[enumType.Name] = enumType
		for _, value := range enumType.Values {
			if owner, exists := literalOwners[value]; exists {
				return parserErrorf("type value %q is declared in both %s and %s. Enum values must be globally unique", value, owner, enumType.Name)
			}
			literalOwners[value] = enumType.Name
		}
	}

	for i := range app.Entities {
		for j := range app.Entities[i].Fields {
			field := &app.Entities[i].Fields[j]
			if field.RelationEntity != "" {
				continue
			}
			enumValues, ok := enumValuesForType(field.Type, typesByName)
			if !ok {
				return parserErrorf("entity %s field %s uses unknown type %s", app.Entities[i].Name, field.Name, field.Type)
			}
			field.EnumValues = append([]string{}, enumValues...)
			if err := validateFieldDefault(field, app.Entities[i].Name, typesByName); err != nil {
				return err
			}
		}
	}

	for i := range app.InputAliases {
		for j := range app.InputAliases[i].Fields {
			field := &app.InputAliases[i].Fields[j]
			if field.RelationEntity != "" {
				continue
			}
			enumValues, ok := enumValuesForType(field.Type, typesByName)
			if !ok {
				return parserErrorf("type alias %s field %s uses unknown type %s", app.InputAliases[i].Name, field.Name, field.Type)
			}
			field.EnumValues = append([]string{}, enumValues...)
		}
	}

	return nil
}

func validateFieldDefault(field *model.Field, entityName string, typesByName map[string]*model.EnumType) error {
	if field == nil || field.Default == nil {
		return nil
	}
	if isPrimitiveFieldType(field.Type) {
		return nil
	}
	enumType := typesByName[field.Type]
	if enumType == nil {
		return parserErrorf("entity %s field %s uses unknown type %s", entityName, field.Name, field.Type)
	}
	defaultValue, ok := field.Default.(string)
	if !ok {
		return parserErrorf("entity %s field %s default must be a value from type %s", entityName, field.Name, field.Type)
	}
	for _, value := range enumType.Values {
		if value == defaultValue {
			return nil
		}
	}
	return parserErrorf("entity %s field %s default %q is not a value of type %s", entityName, field.Name, defaultValue, field.Type)
}

func enumValuesForType(typeName string, typesByName map[string]*model.EnumType) ([]string, bool) {
	if isPrimitiveFieldType(typeName) {
		return nil, true
	}
	enumType := typesByName[typeName]
	if enumType == nil {
		return nil, false
	}
	return enumType.Values, true
}

func allowedVariablesForRawExpression(raw string, base map[string]struct{}, includeBuiltins bool) map[string]struct{} {
	out := make(map[string]struct{}, len(base)+8)
	for name := range base {
		out[name] = struct{}{}
	}
	if includeBuiltins {
		out = expr.AllowedVariablesWithBuiltins(out)
	}
	for _, token := range regexp.MustCompile(`\b[A-Z][A-Za-z0-9_]*\b`).FindAllString(raw, -1) {
		out[token] = struct{}{}
	}
	return out
}

func resolveEntityRelations(app *model.App) error {
	if app == nil {
		return nil
	}

	entitiesByName := make(map[string]*model.Entity, len(app.Entities))
	for i := range app.Entities {
		entitiesByName[app.Entities[i].Name] = &app.Entities[i]
	}

	for i := range app.Entities {
		ent := &app.Entities[i]
		seenStorageNames := map[string]bool{}
		for j := range ent.Fields {
			field := &ent.Fields[j]
			if field.RelationEntity != "" {
				if field.CurrentUser && ent.Name == "User" {
					return parserErrorf("entity %s field %s cannot use belongs_to current_user", ent.Name, field.Name)
				}
				target := entitiesByName[field.RelationEntity]
				if target == nil {
					return parserErrorf("entity %s field %s references unknown entity %s", ent.Name, field.Name, field.RelationEntity)
				}
				pk := entityPrimaryField(target)
				if pk == nil || !isPrimitiveFieldType(pk.Type) {
					return parserErrorf("entity %s field %s cannot belong_to %s because %s primary key is unsupported", ent.Name, field.Name, field.RelationEntity, field.RelationEntity)
				}
				field.Type = pk.Type
			}

			storageName := model.FieldStorageName(field)
			if seenStorageNames[storageName] {
				return parserErrorf("entity %s has duplicate stored field %q", ent.Name, storageName)
			}
			seenStorageNames[storageName] = true
		}
	}

	return nil
}

func resolveAliasRelations(app *model.App) error {
	if app == nil {
		return nil
	}

	entitiesByName := make(map[string]*model.Entity, len(app.Entities))
	for i := range app.Entities {
		entitiesByName[app.Entities[i].Name] = &app.Entities[i]
	}

	for i := range app.InputAliases {
		alias := &app.InputAliases[i]
		for j := range alias.Fields {
			field := &alias.Fields[j]
			if field.RelationEntity == "" {
				continue
			}
			target := entitiesByName[field.RelationEntity]
			if target == nil {
				return parserErrorf("type alias %s field %s references unknown entity %s", alias.Name, field.Name, field.RelationEntity)
			}
			pk := entityPrimaryField(target)
			if pk == nil || !isPrimitiveFieldType(pk.Type) {
				return parserErrorf("type alias %s field %s cannot reference %s because %s primary key is unsupported", alias.Name, field.Name, field.RelationEntity, field.RelationEntity)
			}
			field.Type = pk.Type
		}
	}

	return nil
}

func parseTypeAlias(lines []line, idx *int) (*model.TypeAlias, error) {
	start := lines[*idx]
	trimmed := strings.TrimSpace(start.text)
	m := match(`^type\s+alias\s+([A-Za-z][A-Za-z0-9_]*)\s*=\s*(.*)$`, trimmed)
	if m == nil {
		return nil, parserErrorf("line %d: invalid type alias declaration", start.number)
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
		return nil, parserErrorf("line %d: type alias %s must start with a record. Try: type alias %s = { field : String }", curLine, name, name)
	}
	rest = strings.TrimSpace(strings.TrimPrefix(rest, "{"))
	for {
		if rest == "" {
			(*idx)++
			if *idx >= len(lines) {
				return nil, parserErrorf("type alias %s is missing closing }", name)
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
				return nil, parserErrorf("line %d: unexpected tokens after type alias %s record", curLine, name)
			}
			(*idx)++
			if len(alias.Fields) == 0 {
				return nil, parserErrorf("line %d: type alias %s must declare at least one field", start.number, name)
			}
			return alias, nil
		}

		if err := parseAliasFieldToken(alias, seen, rest, curLine); err != nil {
			return nil, err
		}
		rest = ""
	}
}

func parseEnumType(lines []line, idx *int) (*model.EnumType, error) {
	start := lines[*idx]
	trimmed := strings.TrimSpace(start.text)
	m := match(`^type\s+([A-Za-z][A-Za-z0-9_]*)\s*\{(.*)$`, trimmed)
	if m == nil {
		return nil, parserErrorf("line %d: invalid type declaration", start.number)
	}

	enumType := &model.EnumType{Name: m[1], Values: []string{}}
	seen := map[string]bool{}
	rest := strings.TrimSpace(m[2])
	curLine := start.number

	for {
		if rest == "" {
			(*idx)++
			if *idx >= len(lines) {
				return nil, parserErrorf("type %s is missing closing }", enumType.Name)
			}
			curLine = lines[*idx].number
			rest = strings.TrimSpace(lines[*idx].text)
			if isCommentOrBlank(rest) {
				continue
			}
		}

		if strings.Contains(rest, "}") {
			before, after, _ := strings.Cut(rest, "}")
			if err := parseEnumValueTokens(enumType, seen, before, curLine); err != nil {
				return nil, err
			}
			if strings.TrimSpace(after) != "" {
				return nil, parserErrorf("line %d: unexpected tokens after type %s declaration", curLine, enumType.Name)
			}
			(*idx)++
			if len(enumType.Values) == 0 {
				return nil, parserErrorf("line %d: type %s must declare at least one value", start.number, enumType.Name)
			}
			return enumType, nil
		}

		if err := parseEnumValueTokens(enumType, seen, rest, curLine); err != nil {
			return nil, err
		}
		rest = ""
	}
}

func parseEnumValueTokens(enumType *model.EnumType, seen map[string]bool, raw string, lineNo int) error {
	normalized := strings.TrimSpace(strings.ReplaceAll(raw, ",", " "))
	if normalized == "" {
		return nil
	}
	for _, value := range strings.Fields(normalized) {
		if !upperNameRe.MatchString(value) || len(value) == 0 || !unicode.IsUpper(rune(value[0])) {
			return parserErrorf("line %d: invalid value %q in type %s. Values must use PascalCase like Admin", lineNo, value, enumType.Name)
		}
		if seen[value] {
			return parserErrorf("line %d: duplicate value %q in type %s", lineNo, value, enumType.Name)
		}
		seen[value] = true
		enumType.Values = append(enumType.Values, value)
	}
	return nil
}

func parseAliasFieldToken(alias *model.TypeAlias, seen map[string]bool, token string, lineNo int) error {
	token = strings.TrimSpace(strings.TrimPrefix(token, ","))
	token = strings.TrimSpace(strings.TrimSuffix(token, ","))
	if token == "" {
		return nil
	}
	m := match(`^([a-z][A-Za-z0-9_]*)\s*:\s*(?:(ref)\s+(`+marTypeRefPattern+`)|(`+marTypeRefPattern+`))$`, token)
	if m == nil {
		return parserErrorf("line %d: invalid field in type alias %s. Expected `name : Type` or `name : ref Entity`", lineNo, alias.Name)
	}
	name := m[1]
	if seen[name] {
		return parserErrorf("line %d: duplicate field %q in type alias %s", lineNo, name, alias.Name)
	}
	seen[name] = true
	field := model.AliasField{Name: name}
	if m[2] == "ref" {
		field.RelationEntity = m[3]
	} else {
		field.Type = m[4]
	}
	alias.Fields = append(alias.Fields, field)
	return nil
}

func parseBelongsToStatement(trimmed string, lineNo int) (*model.Field, bool, error) {
	if !strings.HasPrefix(trimmed, "belongs_to ") {
		return nil, false, nil
	}

	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "belongs_to"))
	if rest == "" {
		return nil, true, parserErrorf("line %d: belongs_to requires a target entity", lineNo)
	}

	if rest == "current_user" {
		return &model.Field{
			Name:           "user",
			RelationEntity: "User",
			CurrentUser:    true,
		}, true, nil
	}
	if strings.HasPrefix(rest, "current_user ") {
		return nil, true, parserErrorf("line %d: belongs_to current_user does not support modifiers", lineNo)
	}

	var fieldName string
	var targetEntity string
	var rawAttrs string

	if before, after, ok := strings.Cut(rest, ":"); ok {
		fieldName = strings.TrimSpace(before)
		after = strings.TrimSpace(after)
		parts := strings.Fields(after)
		if len(parts) == 0 {
			return nil, true, parserErrorf("line %d: belongs_to %s requires a target entity", lineNo, fieldName)
		}
		targetEntity = parts[0]
		rawAttrs = strings.TrimSpace(strings.TrimPrefix(after, targetEntity))
	} else {
		parts := strings.Fields(rest)
		targetEntity = parts[0]
		fieldName = toSnake(targetEntity)
		if len(parts) > 1 {
			rawAttrs = strings.Join(parts[1:], " ")
		}
	}

	if !fieldNameRe.MatchString(fieldName) {
		return nil, true, parserErrorf("line %d: belongs_to field name %q is invalid", lineNo, fieldName)
	}
	if targetEntity == "current_user" {
		if rawAttrs != "" {
			return nil, true, parserErrorf("line %d: belongs_to current_user does not support modifiers", lineNo)
		}
		return &model.Field{
			Name:           fieldName,
			RelationEntity: "User",
			CurrentUser:    true,
		}, true, nil
	}
	if !upperNameRe.MatchString(targetEntity) {
		return nil, true, parserErrorf("line %d: belongs_to target %q is invalid", lineNo, targetEntity)
	}

	field := &model.Field{
		Name:           fieldName,
		RelationEntity: targetEntity,
	}
	if err := parseBelongsToAttributes(field, rawAttrs, lineNo); err != nil {
		return nil, true, err
	}
	return field, true, nil
}

func parseBelongsToAttributes(field *model.Field, raw string, lineNo int) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	tokens, err := tokenizeFieldAttributes(raw)
	if err != nil {
		return parserErrorf("line %d: %w", lineNo, err)
	}
	for _, token := range tokens {
		switch token {
		case "optional":
			field.Optional = true
		default:
			return parserErrorf("line %d: belongs_to only supports the optional modifier", lineNo)
		}
	}
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
				return nil, parserErrorf("line %d: action %s is missing `input: TypeAlias`", ln.number, name)
			}
			if len(action.Steps) == 0 {
				return nil, parserErrorf("line %d: action %s must contain at least one write step", ln.number, name)
			}
			return action, nil
		}

		if m := match(`^input\s*:\s*([A-Za-z][A-Za-z0-9_]*)$`, trimmed); m != nil {
			if hasInput {
				return nil, parserErrorf("line %d: action %s already declares input", ln.number, name)
			}
			action.InputAlias = m[1]
			hasInput = true
			(*idx)++
			continue
		}

		if m := match(`^rule\s+"([^"]+)"\s+expect\s+(.+)$`, trimmed); m != nil {
			action.Steps = append(action.Steps, model.ActionStep{
				Kind:       "rule",
				Message:    m[1],
				Expression: strings.TrimSpace(m[2]),
			})
			(*idx)++
			continue
		}

		if m := match(`^([a-z][A-Za-z0-9_]*)\s*=\s*(load|create|update|delete)\s+([A-Za-z][A-Za-z0-9_]*)\s*\{$`, trimmed); m != nil {
			step, err := parseActionStepBlock(lines, idx, name, m[2], m[3], m[1])
			if err != nil {
				return nil, err
			}
			action.Steps = append(action.Steps, *step)
			continue
		}

		if m := match(`^(create|update|delete)\s+([A-Za-z][A-Za-z0-9_]*)\s*\{$`, trimmed); m != nil {
			step, err := parseActionStepBlock(lines, idx, name, m[1], m[2], "")
			if err != nil {
				return nil, err
			}
			action.Steps = append(action.Steps, *step)
			continue
		}

		return nil, parserErrorf("line %d: invalid action statement %q", ln.number, trimmed)
	}

	return nil, parserErrorf("action %s is missing closing }", name)
}

func parseActionStepBlock(lines []line, idx *int, actionName, kind, entityName, alias string) (*model.ActionStep, error) {
	step := &model.ActionStep{Alias: alias, Kind: kind, Entity: entityName, Values: []model.ActionFieldExpr{}}
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
				return nil, parserErrorf("line %d: %s %s in action %s must define at least one field", ln.number, kind, entityName, actionName)
			}
			return step, nil
		}

		assign := match(`^([a-z][A-Za-z0-9_]*)\s*:\s*(.+)$`, trimmed)
		if assign == nil {
			return nil, parserErrorf("line %d: invalid %s field %q. Expected `field: value`", ln.number, kind, trimmed)
		}
		field := assign[1]
		if seen[field] {
			return nil, parserErrorf("line %d: duplicate field %q in %s %s", ln.number, field, kind, entityName)
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

	return nil, parserErrorf("%s %s in action %s is missing closing }", kind, entityName, actionName)
}

func parseActionFieldExpr(raw string, lineNo int) (*model.ActionFieldExpr, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, parserErrorf("line %d: action value cannot be empty", lineNo)
	}
	return &model.ActionFieldExpr{
		Expression: raw,
	}, nil
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
		return parserErrorf("auth.user_entity %q does not match any declared entity", app.Auth.UserEntity)
	}

	if !hasField(userEntity, app.Auth.EmailField, "String") {
		return parserErrorf("auth.email_field %q must exist in entity %s with type String", app.Auth.EmailField, userEntity.Name)
	}

	if app.Auth.RoleField != "" {
		roleField := findEntityField(userEntity, app.Auth.RoleField)
		if roleField != nil && roleField.RelationEntity != "" {
			return parserErrorf("auth.role_field %q cannot be a relation field in entity %s", app.Auth.RoleField, userEntity.Name)
		}
		if roleField != nil && !isPrimitiveFieldType(roleField.Type) && len(roleField.EnumValues) == 0 {
			return parserErrorf("auth.role_field %q must be String or a declared type when present in entity %s", app.Auth.RoleField, userEntity.Name)
		}
	}

	if strings.TrimSpace(app.Auth.SMTPHost) == "" &&
		strings.TrimSpace(app.Auth.SMTPUsername) == "" &&
		strings.TrimSpace(app.Auth.SMTPPasswordEnv) == "" &&
		app.Auth.SMTPPort == 587 &&
		app.Auth.SMTPStartTLS {
		return nil
	}

	if strings.TrimSpace(app.Auth.SMTPHost) == "" {
		return parserErrorf("auth.smtp_host is required when SMTP is configured")
	}
	if app.Auth.SMTPPort < 1 || app.Auth.SMTPPort > 65535 {
		return parserErrorf("auth.smtp_port must be between 1 and 65535 when SMTP is configured")
	}
	if strings.TrimSpace(app.Auth.SMTPUsername) == "" {
		return parserErrorf("auth.smtp_username is required when SMTP is configured")
	}
	if strings.TrimSpace(app.Auth.SMTPPasswordEnv) == "" {
		return parserErrorf("auth.smtp_password_env is required when SMTP is configured")
	}
	if !envVarNameRe.MatchString(strings.TrimSpace(app.Auth.SMTPPasswordEnv)) {
		return parserErrorf("auth.smtp_password_env %q must be a valid environment variable name", app.Auth.SMTPPasswordEnv)
	}

	return nil
}

func validateActions(app *model.App) error {
	enumLiteralTypes := appEnumLiteralTypes(app)
	aliasByName := map[string]*model.TypeAlias{}
	for i := range app.InputAliases {
		alias := &app.InputAliases[i]
		if _, exists := aliasByName[alias.Name]; exists {
			return parserErrorf("duplicate type alias %q", alias.Name)
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
			return parserErrorf("duplicate action %q", action.Name)
		}
		seenActions[action.Name] = true

		alias := aliasByName[action.InputAlias]
		if alias == nil {
			return parserErrorf("action %s references unknown input type %q", action.Name, action.InputAlias)
		}
		inputFieldTypes := map[string]string{}
		aliasFieldNames := make([]string, 0, len(alias.Fields))
		for _, f := range alias.Fields {
			inputFieldTypes[f.Name] = f.Type
			aliasFieldNames = append(aliasFieldNames, f.Name)
		}
		availableVariables := map[string]string{}
		for _, f := range alias.Fields {
			availableVariables["input."+f.Name] = f.Type
		}
		availableAliases := map[string]string{}
		writeSteps := 0

		if len(action.Steps) == 0 {
			return parserErrorf("action %s must have at least one write step", action.Name)
		}
		for _, step := range action.Steps {
			if step.Kind == "rule" {
				if strings.TrimSpace(step.Message) == "" {
					return parserErrorf("action %s has a rule with an empty message", action.Name)
				}
				if strings.TrimSpace(step.Expression) == "" {
					return parserErrorf("action %s rule %q has an empty expression", action.Name, step.Message)
				}
				if err := validateBooleanExpr(step.Expression, availableVariables, authBuiltinTypes(app), true, enumLiteralTypes); err != nil {
					return parserErrorf("action %s rule %q: %w", action.Name, step.Message, err)
				}
				continue
			}

			entity := entityByName[step.Entity]
			if entity == nil {
				return parserErrorf("action %s references unknown entity %q", action.Name, step.Entity)
			}
			if step.Alias != "" {
				if step.Alias == "input" {
					return parserErrorf("action %s cannot use reserved alias name %q", action.Name, step.Alias)
				}
				if _, ok := inputFieldTypes[step.Alias]; ok {
					return parserErrorf("action %s alias %q conflicts with input field name", action.Name, step.Alias)
				}
				if existing, ok := availableAliases[step.Alias]; ok {
					return parserErrorf("action %s alias %q is already bound to %s", action.Name, step.Alias, existing)
				}
			}
			pkField := findEntityField(entity, entity.PrimaryKey)
			if pkField == nil {
				return parserErrorf("action %s references entity %s without a primary key field", action.Name, entity.Name)
			}
			assignments := map[string]model.ActionFieldExpr{}
			for _, item := range step.Values {
				field := findEntityField(entity, item.Field)
				if field == nil {
					return parserErrorf("action %s assigns unknown field %s.%s%s", action.Name, entity.Name, item.Field, suggest.DidYouMeanSuffix(item.Field, entityFieldNames(entity)))
				}
				if step.Kind == "create" && field.Auto {
					return parserErrorf("action %s cannot assign auto-generated field %s.%s", action.Name, entity.Name, item.Field)
				}
				if step.Kind == "create" && field.CurrentUser {
					return parserErrorf("action %s cannot assign current user field %s.%s because it is managed automatically", action.Name, entity.Name, item.Field)
				}
				if step.Kind == "update" && field.Auto && !field.Primary {
					return parserErrorf("action %s cannot assign auto-generated field %s.%s", action.Name, entity.Name, item.Field)
				}
				assignments[item.Field] = item

				sourceType, err := resolveActionExprType(item.Expression, availableVariables, nil, aliasFieldNames, enumLiteralTypes)
				if err != nil {
					return parserErrorf("action %s field %s.%s: %w", action.Name, entity.Name, item.Field, err)
				}
				if sourceType == "Null" {
					if !field.Optional && !field.Primary {
						return parserErrorf("action %s field %s.%s: null is only allowed on optional fields", action.Name, entity.Name, item.Field)
					}
					if field.Primary {
						return parserErrorf("action %s field %s.%s: null is not allowed on primary key fields", action.Name, entity.Name, item.Field)
					}
					continue
				}
				if !isTypeAssignable(field.Type, sourceType) {
					return parserErrorf("action %s field %s.%s expects %s but got %s", action.Name, entity.Name, item.Field, field.Type, sourceType)
				}
			}

			switch step.Kind {
			case "load":
				if step.Alias == "" {
					return parserErrorf("action %s load %s must bind its result to an alias", action.Name, entity.Name)
				}
				if len(assignments) != 1 {
					return parserErrorf("action %s load %s must only include primary key field %s", action.Name, entity.Name, entity.PrimaryKey)
				}
				if _, ok := assignments[entity.PrimaryKey]; !ok {
					return parserErrorf("action %s load %s must include primary key field %s", action.Name, entity.Name, entity.PrimaryKey)
				}
			case "create":
				writeSteps++
				for _, field := range entity.Fields {
					if field.Auto {
						continue
					}
					if field.CurrentUser {
						continue
					}
					if field.Optional || field.Default != nil {
						continue
					}
					if _, ok := assignments[field.Name]; !ok {
						return parserErrorf("action %s is missing required field %s.%s", action.Name, entity.Name, field.Name)
					}
				}
			case "update":
				writeSteps++
				if _, ok := assignments[entity.PrimaryKey]; !ok {
					return parserErrorf("action %s update %s must include primary key field %s", action.Name, entity.Name, entity.PrimaryKey)
				}
				if len(assignments) == 1 {
					return parserErrorf("action %s update %s must change at least one non-primary field", action.Name, entity.Name)
				}
			case "delete":
				writeSteps++
				if len(assignments) != 1 {
					return parserErrorf("action %s delete %s must only include primary key field %s", action.Name, entity.Name, entity.PrimaryKey)
				}
				if _, ok := assignments[entity.PrimaryKey]; !ok {
					return parserErrorf("action %s delete %s must include primary key field %s", action.Name, entity.Name, entity.PrimaryKey)
				}
			default:
				return parserErrorf("action %s has unsupported step kind %q", action.Name, step.Kind)
			}

			if step.Alias != "" {
				availableAliases[step.Alias] = entity.Name
				for _, field := range entity.Fields {
					availableVariables[step.Alias+"."+field.Name] = field.Type
				}
			}
		}
		if writeSteps == 0 {
			return parserErrorf("action %s must have at least one create, update, or delete step", action.Name)
		}
	}
	return nil
}

func validateFrontend(app *model.App) error {
	if app == nil || app.Frontend == nil {
		return nil
	}

	entityByName := map[string]*model.Entity{}
	for i := range app.Entities {
		entityByName[app.Entities[i].Name] = &app.Entities[i]
	}
	actionByName := map[string]*model.Action{}
	for i := range app.Actions {
		actionByName[app.Actions[i].Name] = &app.Actions[i]
	}
	aliasByName := map[string]*model.TypeAlias{}
	for i := range app.InputAliases {
		aliasByName[app.InputAliases[i].Name] = &app.InputAliases[i]
	}
	enumLiteralNames := map[string]struct{}{}
	for _, enumType := range app.Types {
		for _, value := range enumType.Values {
			enumLiteralNames[value] = struct{}{}
		}
	}

	screenByName := map[string]*model.FrontendScreen{}
	for i := range app.Frontend.Screens {
		screen := &app.Frontend.Screens[i]
		if _, exists := screenByName[screen.Name]; exists {
			return parserErrorf("duplicate frontend screen %q", screen.Name)
		}
		screenByName[screen.Name] = screen
		if screen.ForEntity != "" && entityByName[screen.ForEntity] == nil {
			return parserErrorf("frontend screen %s references unknown entity %s", screen.Name, screen.ForEntity)
		}
	}

	for i := range app.Frontend.Screens {
		screen := &app.Frontend.Screens[i]
		var screenEntity *model.Entity
		if screen.ForEntity != "" {
			screenEntity = entityByName[screen.ForEntity]
		}
		if strings.TrimSpace(screen.TitleExpression) != "" {
			if screenEntity == nil {
				return parserErrorAtLinef(screen.TitleLineNo, "frontend screen %s dynamic title requires `screen %s for Entity`", screen.Name, screen.Name)
			}
			if err := validateFrontendActionExpression(screen.TitleExpression, screenEntity, enumLiteralNames); err != nil {
				return parserErrorAtLinef(screen.TitleLineNo, "frontend screen %s title: %w", screen.Name, err)
			}
		}
		for _, toolbarItem := range screen.ToolbarItems {
			switch toolbarItem.Item.Kind {
			case "create":
				entity := entityByName[toolbarItem.Item.Entity]
				if entity == nil {
					return parserErrorAtLinef(toolbarItem.LineNo, "frontend screen %s toolbar create references unknown entity %s", screen.Name, toolbarItem.Item.Entity)
				}
			case "edit":
				if screenEntity == nil {
					return parserErrorAtLinef(toolbarItem.LineNo, "frontend screen %s toolbar edit requires `screen %s for Entity`", screen.Name, screen.Name)
				}
			case "editList":
				entity := entityByName[toolbarItem.Item.Entity]
				if entity == nil {
					return parserErrorAtLinef(toolbarItem.LineNo, "frontend screen %s toolbar edit list references unknown entity %s", screen.Name, toolbarItem.Item.Entity)
				}
			default:
				return parserErrorAtLinef(toolbarItem.LineNo, "frontend screen %s toolbar uses unsupported item kind %s", screen.Name, toolbarItem.Item.Kind)
			}
		}
		for j := range screen.Sections {
			section := &screen.Sections[j]
			if strings.TrimSpace(section.When) != "" {
				if err := validateFrontendExpression(section.When, screenEntity, enumLiteralNames); err != nil {
					return parserErrorAtLinef(section.WhenLineNo, "frontend screen %s section %q: %w", screen.Name, section.Title, err)
				}
			}
			for k := range section.Items {
				item := &section.Items[k]
				switch item.Kind {
				case "link":
					if screenByName[item.Target] == nil {
						return parserErrorAtLinef(item.LineNo, "frontend screen %s links to unknown screen %s", screen.Name, item.Target)
					}
					if strings.TrimSpace(item.Filter) != "" {
						if err := validateFrontendExpression(item.Filter, screenEntity, enumLiteralNames); err != nil {
							return parserErrorAtLinef(item.FilterLineNo, "frontend screen %s link %q: %w", screen.Name, item.Label, err)
						}
					}
				case "field":
					if screenEntity == nil {
						return parserErrorAtLinef(item.LineNo, "frontend screen %s field %s requires `screen %s for Entity`", screen.Name, item.Field, screen.Name)
					}
					baseFieldName, displayFieldName, hasDisplayPath := parseFrontendDisplayFieldPath(item.Field)
					baseField := findEntityField(screenEntity, baseFieldName)
					if baseField == nil {
						return parserErrorAtLinef(item.LineNo, "frontend screen %s field %s does not exist on %s", screen.Name, item.Field, screenEntity.Name)
					}
					if hasDisplayPath {
						if baseField.RelationEntity == "" {
							return parserErrorAtLinef(item.LineNo, "frontend screen %s field %s must reference a relation field", screen.Name, item.Field)
						}
						relationEntity := entityByName[baseField.RelationEntity]
						if relationEntity == nil {
							return parserErrorAtLinef(item.LineNo, "frontend screen %s field %s references unknown entity %s", screen.Name, item.Field, baseField.RelationEntity)
						}
						preferred := preferredFrontendDisplayField(relationEntity)
						if preferred == nil || !strings.EqualFold(preferred.Name, displayFieldName) {
							if preferred != nil {
								return parserErrorAtLinef(item.LineNo, "frontend screen %s field %s is unsupported; %s displays as %s.%s", screen.Name, item.Field, relationEntity.Name, baseFieldName, preferred.Name)
							}
							return parserErrorAtLinef(item.LineNo, "frontend screen %s field %s is unsupported; %s has no display field", screen.Name, item.Field, relationEntity.Name)
						}
					}
				case "edit", "delete":
					if screenEntity == nil {
						return parserErrorAtLinef(item.LineNo, "frontend screen %s %s requires `screen %s for Entity`", screen.Name, item.Kind, screen.Name)
					}
					if item.Kind == "edit" {
						if err := validateFrontendFormFieldsForEntity(item.FormFields, screenEntity, entityByName); err != nil {
							return parserErrorAtLinef(item.LineNo, "frontend screen %s edit: %w", screen.Name, err)
						}
					}
				case "create":
					entity := entityByName[item.Entity]
					if entity == nil {
						return parserErrorAtLinef(item.LineNo, "frontend screen %s create references unknown entity %s", screen.Name, item.Entity)
					}
					for _, value := range item.Values {
						field := findEntityField(entity, value.Field)
						if field == nil {
							return parserErrorAtLinef(value.LineNo, "frontend screen %s create %s assigns unknown field %s", screen.Name, entity.Name, value.Field)
						}
						if field.Primary || field.Auto || field.CurrentUser {
							return parserErrorAtLinef(value.LineNo, "frontend screen %s create %s cannot assign generated field %s", screen.Name, entity.Name, value.Field)
						}
						if strings.TrimSpace(value.Expression) == "" {
							return parserErrorAtLinef(value.LineNo, "frontend screen %s create %s field %s cannot be empty", screen.Name, entity.Name, value.Field)
						}
						if err := validateFrontendActionExpression(value.Expression, screenEntity, enumLiteralNames); err != nil {
							return parserErrorAtLinef(value.LineNo, "frontend screen %s create %s field %s: %w", screen.Name, entity.Name, value.Field, err)
						}
					}
					if err := validateFrontendFormFieldsForEntity(item.FormFields, entity, entityByName); err != nil {
						return parserErrorAtLinef(item.LineNo, "frontend screen %s create %s: %w", screen.Name, entity.Name, err)
					}
				case "list", "children":
					entity := entityByName[item.Entity]
					if entity == nil {
						return parserErrorAtLinef(item.LineNo, "frontend screen %s %s references unknown entity %s", screen.Name, item.Kind, item.Entity)
					}
					if item.Kind == "children" {
						if screenEntity == nil {
							return parserErrorAtLinef(item.LineNo, "frontend screen %s children %s requires `screen %s for Entity`", screen.Name, item.Entity, screen.Name)
						}
						relation := findEntityField(entity, item.RelationField)
						if relation == nil || relation.RelationEntity != screenEntity.Name {
							return parserErrorAtLinef(item.LineNo, "frontend screen %s children %s by %s must reference a belongs_to field pointing to %s", screen.Name, item.Entity, item.RelationField, screenEntity.Name)
						}
					}
					if item.TitleField != "" && findEntityField(entity, item.TitleField) == nil {
						return parserErrorAtLinef(item.LineNo, "frontend screen %s %s %s title field %s does not exist", screen.Name, item.Kind, item.Entity, item.TitleField)
					}
					if item.SubtitleField != "" && findEntityField(entity, item.SubtitleField) == nil {
						return parserErrorAtLinef(item.LineNo, "frontend screen %s %s %s subtitle field %s does not exist", screen.Name, item.Kind, item.Entity, item.SubtitleField)
					}
					if item.Destination != "" {
						destination := screenByName[item.Destination]
						if destination == nil {
							return parserErrorAtLinef(item.LineNo, "frontend screen %s %s %s references unknown destination screen %s", screen.Name, item.Kind, item.Entity, item.Destination)
						}
						if destination.ForEntity != "" && destination.ForEntity != item.Entity {
							return parserErrorAtLinef(item.LineNo, "frontend screen %s %s %s destination %s is for %s, expected %s", screen.Name, item.Kind, item.Entity, item.Destination, destination.ForEntity, item.Entity)
						}
					}
					if strings.TrimSpace(item.Filter) != "" {
						if err := validateFrontendExpression(item.Filter, entity, enumLiteralNames); err != nil {
							return parserErrorAtLinef(item.FilterLineNo, "frontend screen %s %s %s: %w", screen.Name, item.Kind, item.Entity, err)
						}
					}
				case "report":
					entity := entityByName[item.Entity]
					if entity == nil {
						return parserErrorAtLinef(item.LineNo, "frontend screen %s report references unknown entity %s", screen.Name, item.Entity)
					}
					if strings.TrimSpace(item.Filter) != "" {
						if err := validateFrontendExpression(item.Filter, entity, enumLiteralNames); err != nil {
							return parserErrorAtLinef(item.FilterLineNo, "frontend screen %s report %s: %w", screen.Name, item.Entity, err)
						}
					}
					if err := validateFrontendReport(item, entity); err != nil {
						return parserErrorAtLinef(item.LineNo, "frontend screen %s report %s: %w", screen.Name, item.Entity, err)
					}
				case "action":
					action := actionByName[item.Action]
					if action == nil {
						return parserErrorAtLinef(item.LineNo, "frontend screen %s action references unknown action %s", screen.Name, item.Action)
					}
					alias := aliasByName[action.InputAlias]
					if alias == nil {
						return parserErrorAtLinef(item.LineNo, "frontend screen %s action %s references unknown input %s", screen.Name, action.Name, action.InputAlias)
					}
					for _, value := range item.Values {
						if !aliasHasField(alias, value.Field) {
							return parserErrorAtLinef(value.LineNo, "frontend screen %s action %s assigns unknown input field %s", screen.Name, action.Name, value.Field)
						}
						if strings.TrimSpace(value.Expression) == "" {
							return parserErrorAtLinef(value.LineNo, "frontend screen %s action %s input %s cannot be empty", screen.Name, action.Name, value.Field)
						}
						if err := validateFrontendActionExpression(value.Expression, screenEntity, enumLiteralNames); err != nil {
							return parserErrorAtLinef(value.LineNo, "frontend screen %s action %s input %s: %w", screen.Name, action.Name, value.Field, err)
						}
					}
					if err := validateFrontendFormFieldsForAlias(item.FormFields, alias, entityByName); err != nil {
						return parserErrorAtLinef(item.LineNo, "frontend screen %s action %s: %w", screen.Name, action.Name, err)
					}
				default:
					return parserErrorAtLinef(item.LineNo, "frontend screen %s uses unsupported item kind %s", screen.Name, item.Kind)
				}
			}
		}
	}

	return nil
}

func validateFrontendActionExpression(raw string, entity *model.Entity, enumLiteralNames map[string]struct{}) error {
	allowed := map[string]struct{}{}
	if entity != nil {
		bindingName := frontendEntityBindingName(entity.Name)
		for _, field := range entity.Fields {
			allowed[bindingName+"."+field.Name] = struct{}{}
		}
	}
	for name := range enumLiteralNames {
		allowed[name] = struct{}{}
	}
	allowed = expr.AllowedVariablesWithBuiltins(allowed)
	if _, err := expr.Parse(raw, expr.ParserOptions{AllowedVariables: allowed}); err != nil {
		return fmt.Errorf("invalid expression %q (%w)", raw, err)
	}
	return nil
}

func frontendEntityBindingName(name string) string {
	if name == "" {
		return ""
	}
	runes := []rune(name)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

func validateFrontendExpression(raw string, entity *model.Entity, enumLiteralNames map[string]struct{}) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	allowed := map[string]struct{}{}
	if entity != nil {
		for _, field := range entity.Fields {
			allowed[field.Name] = struct{}{}
		}
	}
	for name := range enumLiteralNames {
		allowed[name] = struct{}{}
	}
	allowed = expr.AllowedVariablesWithBuiltins(allowed)
	if _, err := expr.Parse(raw, expr.ParserOptions{AllowedVariables: allowed}); err != nil {
		return fmt.Errorf("invalid expression %q (%w)", raw, err)
	}
	return nil
}

func validateFrontendFormFieldsForEntity(formFields []model.FrontendFormField, entity *model.Entity, entityByName map[string]*model.Entity) error {
	if len(formFields) == 0 {
		return nil
	}
	for _, formField := range formFields {
		field := findEntityField(entity, formField.Field)
		if field == nil {
			return fmt.Errorf("form field %s does not exist on %s", formField.Field, entity.Name)
		}
		if err := validateFrontendFormFieldFilter(formField.Filter, field, entityByName, func(name string) (string, string, bool) {
			parent := findEntityField(entity, name)
			if parent == nil {
				return "", "", false
			}
			return parent.Type, parent.RelationEntity, true
		}); err != nil {
			return err
		}
	}
	return nil
}

func validateFrontendFormFieldsForAlias(formFields []model.FrontendFormField, alias *model.TypeAlias, entityByName map[string]*model.Entity) error {
	if len(formFields) == 0 {
		return nil
	}
	for _, formField := range formFields {
		field := aliasField(alias, formField.Field)
		if field == nil {
			return fmt.Errorf("form field %s does not exist on %s", formField.Field, alias.Name)
		}
		if err := validateFrontendFormFieldFilter(formField.Filter, &model.Field{
			Name:           field.Name,
			Type:           field.Type,
			RelationEntity: field.RelationEntity,
			EnumValues:     field.EnumValues,
		}, entityByName, func(name string) (string, string, bool) {
			parent := aliasField(alias, name)
			if parent == nil {
				return "", "", false
			}
			return parent.Type, parent.RelationEntity, true
		}); err != nil {
			return err
		}
	}
	return nil
}

func validateFrontendFormFieldFilter(raw string, field *model.Field, entityByName map[string]*model.Entity, lookupParent func(string) (string, string, bool)) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if field == nil || field.RelationEntity == "" {
		return fmt.Errorf("form field %s can only use where when it references another entity", field.Name)
	}
	m := match(`^([a-z][A-Za-z0-9_]*)\s*==\s*form\.([a-z][A-Za-z0-9_]*)$`, raw)
	if m == nil {
		return fmt.Errorf("form field %s has invalid filter %q. Expected `relationField == form.parentField`", field.Name, raw)
	}
	relationFieldName := m[1]
	parentFieldName := m[2]
	parentType, parentRelationEntity, ok := lookupParent(parentFieldName)
	if !ok {
		return fmt.Errorf("form field %s filter references unknown form field %s", field.Name, parentFieldName)
	}
	relationEntity := entityByName[field.RelationEntity]
	if relationEntity == nil {
		return fmt.Errorf("form field %s references unknown relation entity %s", field.Name, field.RelationEntity)
	}
	relationField := findEntityField(relationEntity, relationFieldName)
	if relationField == nil {
		return fmt.Errorf("form field %s filter references unknown relation field %s", field.Name, relationFieldName)
	}
	relationFieldEntity := relationField.RelationEntity
	if relationFieldEntity == "" {
		return fmt.Errorf("form field %s filter field %s must be a relation field", field.Name, relationFieldName)
	}
	if parentRelationEntity != "" && parentRelationEntity != relationFieldEntity {
		return fmt.Errorf("form field %s filter expects form.%s to reference %s", field.Name, parentFieldName, relationFieldEntity)
	}
	if parentType == "" {
		return fmt.Errorf("form field %s filter references invalid form field %s", field.Name, parentFieldName)
	}
	return nil
}

func validateFrontendReport(item *model.FrontendItem, entity *model.Entity) error {
	if item == nil || entity == nil {
		return nil
	}
	if strings.TrimSpace(item.ReportGroup) == "" {
		return fmt.Errorf("missing `group by` clause")
	}
	if len(item.ReportMetrics) == 0 {
		return fmt.Errorf("report must declare at least one metric")
	}
	if _, err := parseFrontendReportGroup(item.ReportGroup, entity); err != nil {
		return err
	}
	for _, metric := range item.ReportMetrics {
		if err := validateFrontendReportMetric(metric, entity); err != nil {
			return err
		}
	}
	return nil
}

func parseFrontendReportGroup(raw string, entity *model.Entity) (string, error) {
	group := strings.TrimSpace(raw)
	if group == "" {
		return "", fmt.Errorf("missing `group by` clause")
	}
	if m := match(`^month\(([a-z][A-Za-z0-9_]*)\)$`, group); m != nil {
		field := findEntityField(entity, m[1])
		if field == nil {
			return "", fmt.Errorf("group field %s does not exist on %s", m[1], entity.Name)
		}
		if field.Type != "Date" && field.Type != "DateTime" {
			return "", fmt.Errorf("group field %s must be Date or DateTime", m[1])
		}
		return group, nil
	}
	field := findEntityField(entity, group)
	if field == nil {
		return "", fmt.Errorf("group field %s does not exist on %s", group, entity.Name)
	}
	return group, nil
}

func validateFrontendReportMetric(metric model.FrontendReportMetric, entity *model.Entity) error {
	aggregate := strings.TrimSpace(metric.Aggregate)
	fieldName := strings.TrimSpace(metric.Field)
	switch aggregate {
	case "count":
		if fieldName != "" {
			return fmt.Errorf("metric count() cannot specify a field")
		}
		return nil
	case "avg", "sum", "min", "max":
		if fieldName == "" {
			return fmt.Errorf("metric %s(...) requires a field", aggregate)
		}
		field := findEntityField(entity, fieldName)
		if field == nil {
			return fmt.Errorf("metric field %s does not exist on %s", fieldName, entity.Name)
		}
		if field.Type != "Int" && field.Type != "Float" {
			return fmt.Errorf("metric %s(%s) requires an Int or Float field", aggregate, fieldName)
		}
		return nil
	default:
		return fmt.Errorf("unsupported metric %s", aggregate)
	}
}

func parseFrontendDisplayFieldPath(raw string) (string, string, bool) {
	parts := strings.Split(raw, ".")
	if len(parts) == 2 && fieldNameRe.MatchString(parts[0]) && fieldNameRe.MatchString(parts[1]) {
		return parts[0], parts[1], true
	}
	return raw, "", false
}

func preferredFrontendDisplayField(entity *model.Entity) *model.Field {
	if entity == nil {
		return nil
	}

	candidates := make([]*model.Field, 0, len(entity.Fields))
	for i := range entity.Fields {
		field := &entity.Fields[i]
		if strings.EqualFold(field.Name, "id") || field.Primary || field.Auto || field.RelationEntity != "" {
			continue
		}
		candidates = append(candidates, field)
	}

	for _, preferred := range frontendDisplayFieldNames {
		for _, field := range candidates {
			if strings.EqualFold(field.Name, preferred) {
				return field
			}
		}
	}

	for _, field := range candidates {
		if field.Type == "String" {
			return field
		}
	}

	if len(candidates) > 0 {
		return candidates[0]
	}

	return nil
}

func aliasHasField(alias *model.TypeAlias, name string) bool {
	if alias == nil {
		return false
	}
	for _, field := range alias.Fields {
		if field.Name == name {
			return true
		}
	}
	return false
}

func aliasField(alias *model.TypeAlias, name string) *model.AliasField {
	if alias == nil {
		return nil
	}
	for i := range alias.Fields {
		if alias.Fields[i].Name == name {
			return &alias.Fields[i]
		}
	}
	return nil
}

func authBootstrapWarnings(app *model.App) []string {
	if app == nil || app.Auth == nil {
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
		return nil
	}

	blockingFields := make([]string, 0, len(userEntity.Fields))
	for _, field := range userEntity.Fields {
		if field.Auto {
			continue
		}
		if field.Name == app.Auth.EmailField {
			continue
		}
		if app.Auth.RoleField != "" && field.Name == app.Auth.RoleField {
			continue
		}
		if field.Default != nil || field.Optional {
			continue
		}
		if field.RelationEntity == "" {
			continue
		}
		blockingFields = append(blockingFields, field.Name)
	}
	if len(blockingFields) == 0 {
		return nil
	}

	fieldLabel := "fields"
	defaultLabel := "defaults"
	optionLabel := "these fields"
	if len(blockingFields) == 1 {
		fieldLabel = "field"
		defaultLabel = "default"
		optionLabel = "this field"
	}
	for i := range blockingFields {
		blockingFields[i] = "`" + blockingFields[i] + "`"
	}

	return []string{
		fmt.Sprintf(
			"Automatic creation of the first admin will not be possible.\nAuth user entity %s has required relation %s without %s: %s.\nYou can make %s optional, or create the first admin manually in the database.",
			userEntity.Name,
			fieldLabel,
			defaultLabel,
			strings.Join(blockingFields, ", "),
			optionLabel,
		),
	}
}

func resolveActionExprType(raw string, variableTypes map[string]string, builtinTypes map[string]string, aliasFieldNames []string, enumLiteralTypes map[string]string) (string, error) {
	parsed, err := parseTypedExpr(raw, variableTypes, builtinTypes, false, aliasFieldNames, enumLiteralTypes)
	if err != nil {
		return "", err
	}
	return inferActionExprType(parsed, typedExprVariables(variableTypes, builtinTypes, false, enumLiteralTypes))
}

func allowedExprVariables(variableTypes map[string]string) map[string]struct{} {
	out := make(map[string]struct{}, len(variableTypes))
	for name := range variableTypes {
		out[name] = struct{}{}
	}
	return out
}

func validateBooleanExpr(raw string, variableTypes map[string]string, builtinTypes map[string]string, includeBuiltins bool, enumLiteralTypes map[string]string) error {
	parsed, err := parseTypedExpr(raw, variableTypes, builtinTypes, includeBuiltins, nil, enumLiteralTypes)
	if err != nil {
		return err
	}
	typ, err := inferActionExprType(parsed, typedExprVariables(variableTypes, builtinTypes, includeBuiltins, enumLiteralTypes))
	if err != nil {
		return err
	}
	if typ != "Bool" {
		return parserErrorf("expression must evaluate to Bool, got %s", typ)
	}
	return nil
}

func parseTypedExpr(raw string, variableTypes map[string]string, builtinTypes map[string]string, includeBuiltins bool, aliasFieldNames []string, enumLiteralTypes map[string]string) (expr.Expr, error) {
	allowed := allowedExprVariables(variableTypes)
	if includeBuiltins {
		allowed = expr.AllowedVariablesWithBuiltins(allowed)
	}
	for name := range enumLiteralTypes {
		allowed[name] = struct{}{}
	}
	if includeBuiltins {
		for name := range builtinTypes {
			allowed[name] = struct{}{}
		}
	}
	parsed, err := expr.Parse(raw, expr.ParserOptions{AllowedVariables: allowed})
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "unknown identifier") {
			name := strings.Trim(strings.TrimPrefix(msg, "unknown identifier "), `"`)
			return nil, parserErrorf("references unknown value %q%s", name, suggest.DidYouMeanSuffix(name, actionVariableNames(typedExprVariables(variableTypes, builtinTypes, includeBuiltins, enumLiteralTypes), aliasFieldNames)))
		}
		return nil, err
	}
	return parsed, nil
}

func typedExprVariables(variableTypes map[string]string, builtinTypes map[string]string, includeBuiltins bool, enumLiteralTypes map[string]string) map[string]string {
	out := make(map[string]string, len(variableTypes)+len(enumLiteralTypes)+len(builtinTypes))
	for name, typ := range variableTypes {
		out[name] = typ
	}
	if includeBuiltins {
		for name, typ := range builtinTypes {
			out[name] = typ
		}
	}
	for name, typ := range enumLiteralTypes {
		out[name] = typ
	}
	return out
}

func appEnumLiteralTypes(app *model.App) map[string]string {
	out := map[string]string{}
	if app == nil {
		return out
	}
	for _, enumType := range app.Types {
		for _, value := range enumType.Values {
			out[value] = enumType.Name
		}
	}
	return out
}

func authBuiltinTypes(app *model.App) map[string]string {
	out := map[string]string{
		"anonymous":          "Bool",
		"user_authenticated": "Bool",
		"user_email":         "String",
		"user_id":            "Int",
		"user_role":          "String",
	}
	if app == nil || app.Auth == nil {
		return out
	}
	for i := range app.Entities {
		entity := &app.Entities[i]
		if entity.Name != app.Auth.UserEntity {
			continue
		}
		if field := findEntityField(entity, app.Auth.RoleField); field != nil {
			out["user_role"] = field.Type
		}
		break
	}
	return out
}

func actionVariableNames(variableTypes map[string]string, aliasFieldNames []string) []string {
	out := make([]string, 0, len(variableTypes)+len(aliasFieldNames))
	seen := map[string]struct{}{}
	for name := range variableTypes {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	for _, name := range aliasFieldNames {
		full := "input." + name
		if _, ok := seen[full]; ok {
			continue
		}
		seen[full] = struct{}{}
		out = append(out, full)
	}
	return out
}

func inferActionExprType(node expr.Expr, variableTypes map[string]string) (string, error) {
	switch n := node.(type) {
	case expr.Literal:
		switch n.Value.(type) {
		case nil:
			return "Null", nil
		case bool:
			return "Bool", nil
		case string:
			return "String", nil
		case int64, int:
			return "Int", nil
		case float64, float32:
			return "Float", nil
		default:
			return "", parserErrorf("unsupported literal value")
		}
	case expr.Variable:
		t := variableTypes[n.Name]
		if t == "" {
			return "", parserErrorf("references unknown value %q", n.Name)
		}
		return t, nil
	case expr.Unary:
		rightType, err := inferActionExprType(n.Right, variableTypes)
		if err != nil {
			return "", err
		}
		switch n.Op {
		case "not":
			if rightType != "Bool" {
				return "", parserErrorf("operator not expects Bool, got %s", rightType)
			}
			return "Bool", nil
		case "-":
			switch rightType {
			case "Int":
				return "Int", nil
			case "Float":
				return "Float", nil
			default:
				return "", parserErrorf("operator - expects Int or Float")
			}
		default:
			return "", parserErrorf("unknown unary operator %q", n.Op)
		}
	case expr.Binary:
		leftType, err := inferActionExprType(n.Left, variableTypes)
		if err != nil {
			return "", err
		}
		rightType, err := inferActionExprType(n.Right, variableTypes)
		if err != nil {
			return "", err
		}
		switch n.Op {
		case "and", "or":
			if leftType != "Bool" || rightType != "Bool" {
				return "", parserErrorf("operator %s expects Bool operands", n.Op)
			}
			return "Bool", nil
		case "==", "!=":
			if !areEqualityComparable(leftType, rightType) {
				return "", parserErrorf("operator %s expects compatible values, got %s and %s", n.Op, leftType, rightType)
			}
			return "Bool", nil
		case ">", ">=", "<", "<=":
			if !areOrderedComparable(leftType, rightType) {
				return "", parserErrorf("operator %s expects comparable values, got %s and %s", n.Op, leftType, rightType)
			}
			return "Bool", nil
		case "+":
			if leftType == "String" || rightType == "String" {
				return "String", nil
			}
			if isTemporalType(leftType) && rightType == "Int" {
				return leftType, nil
			}
			if leftType == "Int" && isTemporalType(rightType) {
				return rightType, nil
			}
			if leftType == "Float" || rightType == "Float" {
				return "Float", nil
			}
			if leftType == "Int" && rightType == "Int" {
				return "Int", nil
			}
			return "", parserErrorf("operator + expects compatible values")
		case "-":
			if isTemporalType(leftType) && rightType == "Int" {
				return leftType, nil
			}
			if isTemporalType(leftType) && isTemporalType(rightType) {
				return "Int", nil
			}
			if leftType == "Float" || rightType == "Float" {
				return "Float", nil
			}
			if leftType == "Int" && rightType == "Int" {
				return "Int", nil
			}
			return "", parserErrorf("operator - expects compatible numeric values")
		case "*":
			if leftType == "Float" || rightType == "Float" {
				return "Float", nil
			}
			if leftType == "Int" && rightType == "Int" {
				return "Int", nil
			}
			return "", parserErrorf("operator * expects numeric values")
		case "/":
			if (leftType == "Int" || leftType == "Float") && (rightType == "Int" || rightType == "Float") {
				return "Float", nil
			}
			return "", parserErrorf("operator / expects numeric values")
		default:
			return "", parserErrorf("unknown operator %q", n.Op)
		}
	case expr.Call:
		argTypes := make([]string, 0, len(n.Args))
		for _, arg := range n.Args {
			argType, err := inferActionExprType(arg, variableTypes)
			if err != nil {
				return "", err
			}
			argTypes = append(argTypes, argType)
		}
		switch n.Name {
		case "contains", "starts_with", "ends_with", "matches":
			if len(argTypes) != 2 || argTypes[0] != "String" || argTypes[1] != "String" {
				return "", parserErrorf("function %s expects String arguments", n.Name)
			}
			return "Bool", nil
		case "length":
			if len(argTypes) != 1 || argTypes[0] != "String" {
				return "", parserErrorf("function length expects a String argument")
			}
			return "Int", nil
		default:
			return "", parserErrorf("unsupported function %q", n.Name)
		}
	default:
		return "", parserErrorf("unsupported expression type")
	}
}

func areEqualityComparable(leftType, rightType string) bool {
	if leftType == "Null" || rightType == "Null" {
		return true
	}
	if leftType == rightType {
		return true
	}
	return areNumericComparable(leftType, rightType)
}

func areOrderedComparable(leftType, rightType string) bool {
	if leftType == "String" || rightType == "String" {
		return leftType == "String" && rightType == "String"
	}
	return areNumericComparable(leftType, rightType)
}

func areNumericComparable(leftType, rightType string) bool {
	return isNumericLikeType(leftType) && isNumericLikeType(rightType)
}

func isNumericLikeType(typ string) bool {
	switch typ {
	case "Int", "Float", "Date", "DateTime":
		return true
	default:
		return false
	}
}

func isTemporalType(typ string) bool {
	switch typ {
	case "Date", "DateTime":
		return true
	default:
		return false
	}
}

func isTypeAssignable(targetType, sourceType string) bool {
	if targetType == sourceType {
		return true
	}
	if targetType == "Float" && sourceType == "Int" {
		return true
	}
	if isTemporalType(targetType) && (sourceType == "Int" || isTemporalType(sourceType)) {
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

func entityFieldNames(entity *model.Entity) []string {
	out := make([]string, 0, len(entity.Fields))
	for i := range entity.Fields {
		out = append(out, entity.Fields[i].Name)
	}
	return out
}

func hasField(ent *model.Entity, name, typ string) bool {
	for _, f := range ent.Fields {
		if f.Name == name && f.Type == typ {
			return true
		}
	}
	return false
}

func isCommentOrBlank(s string) bool {
	return s == "" || strings.HasPrefix(s, "--")
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

func defaultDatabaseName(appName string) string {
	return toKebab(appName) + ".db"
}

func parseFieldAttributes(field *model.Field, raw string, lineNo int) error {
	tokens, err := tokenizeFieldAttributes(raw)
	if err != nil {
		return parserErrorf("line %d: %w", lineNo, err)
	}
	for i := 0; i < len(tokens); i++ {
		switch tokens[i] {
		case "":
			continue
		case "primary":
			field.Primary = true
		case "auto":
			field.Auto = true
		case "optional":
			field.Optional = true
		case "default":
			if i+1 >= len(tokens) {
				return parserErrorf("line %d: default requires a literal value", lineNo)
			}
			defaultValue, err := parseFieldDefaultLiteral(field.Type, tokens[i+1], lineNo)
			if err != nil {
				return err
			}
			field.Default = defaultValue
			i++
		default:
			return parserErrorf("line %d: unknown field attribute %q", lineNo, tokens[i])
		}
	}
	return nil
}

func tokenizeFieldAttributes(raw string) ([]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	var tokens []string
	var current strings.Builder
	inString := false
	escaped := false

	for _, r := range trimmed {
		switch {
		case inString:
			current.WriteRune(r)
			if escaped {
				escaped = false
			} else if r == '\\' {
				escaped = true
			} else if r == '"' {
				inString = false
			}
		case unicode.IsSpace(r):
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
			if r == '"' {
				inString = true
			}
		}
	}

	if inString {
		return nil, parserErrorf("unterminated string literal in field attributes")
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens, nil
}

func parseFieldDefaultLiteral(fieldType string, raw string, lineNo int) (any, error) {
	switch fieldType {
	case "String":
		if !(strings.HasPrefix(raw, "\"") && strings.HasSuffix(raw, "\"")) {
			return nil, parserErrorf("line %d: field default for %s must be a string literal", lineNo, fieldType)
		}
		unquoted, err := strconv.Unquote(raw)
		if err != nil {
			return nil, parserErrorf("line %d: invalid string literal %q", lineNo, raw)
		}
		return unquoted, nil
	case "Bool":
		if raw == "true" {
			return true, nil
		}
		if raw == "false" {
			return false, nil
		}
		return nil, parserErrorf("line %d: field default for %s must be true or false", lineNo, fieldType)
	case "Int", "Date", "DateTime":
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, parserErrorf("line %d: field default for %s must be an integer literal", lineNo, fieldType)
		}
		if fieldType == "Date" {
			n = normalizeDateMillis(n)
		}
		return n, nil
	case "Float":
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return float64(n), nil
		}
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, parserErrorf("line %d: field default for %s must be a numeric literal", lineNo, fieldType)
		}
		return f, nil
	default:
		if upperNameRe.MatchString(raw) && unicode.IsUpper(rune(raw[0])) {
			return raw, nil
		}
		return nil, parserErrorf("line %d: field default for %s must be a declared value like %sValue", lineNo, fieldType, fieldType)
	}
}

func isPrimitiveFieldType(fieldType string) bool {
	switch strings.TrimSpace(fieldType) {
	case "Int", "String", "Bool", "Float", "Date", "DateTime":
		return true
	default:
		return false
	}
}

func normalizeDateMillis(value int64) int64 {
	t := time.UnixMilli(value).UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC).UnixMilli()
}

func entityPrimaryField(entity *model.Entity) *model.Field {
	if entity == nil {
		return nil
	}
	for i := range entity.Fields {
		if entity.Fields[i].Name == entity.PrimaryKey {
			return &entity.Fields[i]
		}
	}
	return nil
}

func toKebab(v string) string {
	var b strings.Builder
	var prevLowerOrDigit bool
	var prevWasDash bool

	for _, ch := range v {
		switch {
		case ch == '_' || ch == '-' || ch == ' ':
			if b.Len() > 0 && !prevWasDash {
				b.WriteByte('-')
				prevWasDash = true
			}
			prevLowerOrDigit = false

		case ch >= 'A' && ch <= 'Z':
			if b.Len() > 0 && prevLowerOrDigit && !prevWasDash {
				b.WriteByte('-')
			}
			b.WriteByte(byte(ch + 32))
			prevLowerOrDigit = false
			prevWasDash = false

		default:
			b.WriteRune(ch)
			prevLowerOrDigit = (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')
			prevWasDash = false
		}
	}

	return strings.Trim(b.String(), "-")
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
