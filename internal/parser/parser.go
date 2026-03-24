package parser

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
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

const marTypePattern = `Int|String|Bool|Float|Posix`

var (
	topLevelStatementCandidates = []string{
		"app",
		"port",
		"database",
		"system",
		"public",
		"auth",
		"entity",
		"type alias",
		"action",
	}
	authStatementCandidates = []string{
		"code_ttl_minutes",
		"session_ttl_hours",
		"email_transport",
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
		"auth_request_code_rate_limit_per_minute",
		"auth_login_rate_limit_per_minute",
		"admin_ui_session_ttl_hours",
		"security_frame_policy",
		"security_referrer_policy",
		"security_content_type_nosniff",
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
			explicitDatabase = true
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
			if entityName == "User" {
				if userExtension != nil {
					return nil, fmt.Errorf("line %d: entity User already declared", cur.number)
				}
				entity, err := parseUserExtensionBlock(lines, &idx)
				if err != nil {
					return nil, err
				}
				userExtension = entity
				continue
			}
			if seenEntities[entityName] {
				return nil, fmt.Errorf("line %d: entity %q already declared", cur.number, entityName)
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
		return nil, fmt.Errorf("missing app declaration")
	}
	if app.Auth == nil {
		app.Auth = defaultAuthConfig()
	}
	if err := injectImplicitUserEntity(app, userExtension); err != nil {
		return nil, err
	}
	if err := validateAuthConfig(app); err != nil {
		return nil, err
	}
	if err := validateActions(app); err != nil {
		return nil, err
	}

	return app, nil
}

func defaultAuthConfig() *model.AuthConfig {
	return &model.AuthConfig{
		UserEntity:      "User",
		EmailField:      "email",
		RoleField:       "role",
		CodeTTLMinutes:  10,
		SessionTTLHours: 24,
		EmailTransport:  "console",
		EmailFrom:       "no-reply@mar.local",
		EmailSubject:    "Your Mar login code",
		SMTPPort:        587,
		SMTPStartTLS:    true,
	}
}

// parseAuthBlock parses the auth configuration block and applies defaults.
func parseAuthBlock(lines []line, idx *int) (*model.AuthConfig, error) {
	auth := defaultAuthConfig()

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
			return auth, nil
		}

		var matched bool
		if m := match(`^code_ttl_minutes\s+([0-9]{1,4})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minCodeTTLMinutes || value > maxCodeTTLMinutes {
				return nil, fmt.Errorf(
					"line %d: auth.code_ttl_minutes must be between %d and %d",
					ln.number,
					minCodeTTLMinutes,
					maxCodeTTLMinutes,
				)
			}
			auth.CodeTTLMinutes = value
			matched = true
		}
		if m := match(`^session_ttl_hours\s+([0-9]{1,4})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minSessionTTLHours || value > maxSessionTTLHours {
				return nil, fmt.Errorf(
					"line %d: auth.session_ttl_hours must be between %d and %d",
					ln.number,
					minSessionTTLHours,
					maxSessionTTLHours,
				)
			}
			auth.SessionTTLHours = value
			matched = true
		}
		if m := match(`^email_transport\s+(console|smtp)$`, trimmed); m != nil {
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
		if m := match(`^smtp_host\s+"([^"]+)"$`, trimmed); m != nil {
			auth.SMTPHost = m[1]
			matched = true
		}
		if m := match(`^smtp_port\s+([0-9]{1,5})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < 1 || value > 65535 {
				return nil, fmt.Errorf("line %d: auth.smtp_port must be between 1 and 65535", ln.number)
			}
			auth.SMTPPort = value
			matched = true
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
		}
		if !matched {
			return nil, unknownStatementError(ln.number, "auth", trimmed, authStatementCandidates)
		}
		(*idx)++
	}

	return nil, fmt.Errorf("auth block is missing closing }")
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
			rawRules = append(rawRules, model.Rule{Message: strings.TrimSpace(m[1]), Expression: strings.TrimSpace(m[2])})
			(*idx)++
			continue
		}

		if m := match(`^authorize\s+(all|list|get|create|update|delete)\s+when\s+(.+)$`, trimmed); m != nil {
			rawAuthz = append(rawAuthz, model.Authorization{Action: m[1], Expression: strings.TrimSpace(m[2])})
			(*idx)++
			continue
		}

		if m := match(`^([a-z][A-Za-z0-9_]*)\s*:\s*(`+marTypePattern+`)(?:\s+(.*))?$`, trimmed); m != nil {
			fieldName := m[1]
			field := model.Field{Name: fieldName, Type: m[2]}
			if err := parseFieldAttributes(&field, strings.TrimSpace(m[3]), ln.number); err != nil {
				return nil, err
			}
			if isBuiltInUserField(fieldName) {
				if !matchesBuiltInUserField(field) {
					return nil, fmt.Errorf("line %d: entity User cannot redefine built-in field %q", ln.number, fieldName)
				}
				(*idx)++
				continue
			}
			ent.Fields = append(ent.Fields, field)
			(*idx)++
			continue
		}

		return nil, fmt.Errorf("line %d: invalid entity statement %q", ln.number, trimmed)
	}

	return nil, fmt.Errorf("entity User is missing closing }")
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
		return field.Type == "String" && !field.Primary && !field.Auto && !field.Optional && field.Default == nil
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
		user.Fields = append(user.Fields, extension.Fields...)
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
			return nil, unknownStatementError(ln.number, "public", trimmed, publicStatementCandidates)
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
		if m := match(`^http_max_request_body_mb\s+([0-9]{1,4})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minHTTPMaxRequestBodyMB || value > maxHTTPMaxRequestBodyMB {
				return nil, fmt.Errorf(
					"line %d: system.http_max_request_body_mb must be between %d and %d",
					ln.number,
					minHTTPMaxRequestBodyMB,
					maxHTTPMaxRequestBodyMB,
				)
			}
			cfg.HTTPMaxRequestBodyMB = intPtr(value)
			(*idx)++
			continue
		}
		if m := match(`^auth_request_code_rate_limit_per_minute\s+([0-9]{1,5})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minAuthRateLimitPerMinute || value > maxAuthRateLimitPerMinute {
				return nil, fmt.Errorf(
					"line %d: system.auth_request_code_rate_limit_per_minute must be between %d and %d",
					ln.number,
					minAuthRateLimitPerMinute,
					maxAuthRateLimitPerMinute,
				)
			}
			cfg.AuthRequestCodeRateLimit = intPtr(value)
			(*idx)++
			continue
		}
		if m := match(`^auth_login_rate_limit_per_minute\s+([0-9]{1,5})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minAuthRateLimitPerMinute || value > maxAuthRateLimitPerMinute {
				return nil, fmt.Errorf(
					"line %d: system.auth_login_rate_limit_per_minute must be between %d and %d",
					ln.number,
					minAuthRateLimitPerMinute,
					maxAuthRateLimitPerMinute,
				)
			}
			cfg.AuthLoginRateLimit = intPtr(value)
			(*idx)++
			continue
		}
		if m := match(`^admin_ui_session_ttl_hours\s+([0-9]{1,4})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minSessionTTLHours || value > maxSessionTTLHours {
				return nil, fmt.Errorf(
					"line %d: system.admin_ui_session_ttl_hours must be between %d and %d",
					ln.number,
					minSessionTTLHours,
					maxSessionTTLHours,
				)
			}
			cfg.AdminUISessionTTLHours = intPtr(value)
			(*idx)++
			continue
		}
		if m := match(`^security_frame_policy\s+(deny|sameorigin)$`, trimmed); m != nil {
			cfg.SecurityFramePolicy = stringPtr(m[1])
			(*idx)++
			continue
		}
		if m := match(`^security_frame_policy\s+(.+)$`, trimmed); m != nil {
			return nil, fmt.Errorf(
				"line %d: system.security_frame_policy must be one of: deny, sameorigin",
				ln.number,
			)
		}
		if m := match(`^security_referrer_policy\s+(strict-origin-when-cross-origin|no-referrer)$`, trimmed); m != nil {
			cfg.SecurityReferrerPolicy = stringPtr(m[1])
			(*idx)++
			continue
		}
		if m := match(`^security_referrer_policy\s+(.+)$`, trimmed); m != nil {
			return nil, fmt.Errorf(
				"line %d: system.security_referrer_policy must be one of: strict-origin-when-cross-origin, no-referrer",
				ln.number,
			)
		}
		if m := match(`^security_content_type_nosniff\s+(true|false)$`, trimmed); m != nil {
			cfg.SecurityContentNoSniff = boolPtr(m[1] == "true")
			(*idx)++
			continue
		}
		if m := match(`^security_content_type_nosniff\s+(.+)$`, trimmed); m != nil {
			return nil, fmt.Errorf(
				"line %d: system.security_content_type_nosniff must be true or false",
				ln.number,
			)
		}
		if m := match(`^sqlite_journal_mode\s+(wal|delete|truncate|persist|memory|off)$`, trimmed); m != nil {
			cfg.SQLiteJournalMode = stringPtr(m[1])
			(*idx)++
			continue
		}
		if m := match(`^sqlite_synchronous\s+(off|normal|full|extra)$`, trimmed); m != nil {
			cfg.SQLiteSynchronous = stringPtr(m[1])
			(*idx)++
			continue
		}
		if m := match(`^sqlite_foreign_keys\s+(true|false)$`, trimmed); m != nil {
			cfg.SQLiteForeignKeys = boolPtr(m[1] == "true")
			(*idx)++
			continue
		}
		if m := match(`^sqlite_busy_timeout_ms\s+([0-9]{1,7})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minSQLiteBusyTimeoutMs || value > maxSQLiteBusyTimeoutMs {
				return nil, fmt.Errorf(
					"line %d: system.sqlite_busy_timeout_ms must be between %d and %d",
					ln.number,
					minSQLiteBusyTimeoutMs,
					maxSQLiteBusyTimeoutMs,
				)
			}
			cfg.SQLiteBusyTimeoutMs = intPtr(value)
			(*idx)++
			continue
		}
		if m := match(`^sqlite_wal_autocheckpoint\s+([0-9]{1,7})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minSQLiteWALAutoCheckpoint || value > maxSQLiteWALAutoCheckpoint {
				return nil, fmt.Errorf(
					"line %d: system.sqlite_wal_autocheckpoint must be between %d and %d",
					ln.number,
					minSQLiteWALAutoCheckpoint,
					maxSQLiteWALAutoCheckpoint,
				)
			}
			cfg.SQLiteWALAutoCheckpoint = intPtr(value)
			(*idx)++
			continue
		}
		if m := match(`^sqlite_journal_size_limit_mb\s+(-?[0-9]{1,4})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minSQLiteJournalSizeLimitMB || value > maxSQLiteJournalSizeLimitMB {
				return nil, fmt.Errorf(
					"line %d: system.sqlite_journal_size_limit_mb must be between %d and %d",
					ln.number,
					minSQLiteJournalSizeLimitMB,
					maxSQLiteJournalSizeLimitMB,
				)
			}
			cfg.SQLiteJournalSizeLimitMB = intPtr(value)
			(*idx)++
			continue
		}
		if m := match(`^sqlite_mmap_size_mb\s+([0-9]{1,5})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minSQLiteMmapSizeMB || value > maxSQLiteMmapSizeMB {
				return nil, fmt.Errorf(
					"line %d: system.sqlite_mmap_size_mb must be between %d and %d",
					ln.number,
					minSQLiteMmapSizeMB,
					maxSQLiteMmapSizeMB,
				)
			}
			cfg.SQLiteMmapSizeMB = intPtr(value)
			(*idx)++
			continue
		}
		if m := match(`^sqlite_cache_size_kb\s+([0-9]{1,7})$`, trimmed); m != nil {
			value := mustInt(m[1])
			if value < minSQLiteCacheSizeKB || value > maxSQLiteCacheSizeKB {
				return nil, fmt.Errorf(
					"line %d: system.sqlite_cache_size_kb must be between %d and %d",
					ln.number,
					minSQLiteCacheSizeKB,
					maxSQLiteCacheSizeKB,
				)
			}
			cfg.SQLiteCacheSizeKB = intPtr(value)
			(*idx)++
			continue
		}

		return nil, unknownStatementError(ln.number, "system", trimmed, systemStatementCandidates)
	}

	return nil, fmt.Errorf("system block is missing closing }")
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

func unknownStatementError(lineNumber int, scope, trimmed string, candidates []string) error {
	label := "unknown statement"
	if strings.TrimSpace(scope) != "" {
		label = "unknown " + strings.TrimSpace(scope) + " statement"
	}
	return fmt.Errorf("line %d: %s %q%s", lineNumber, label, trimmed, suggest.DidYouMeanSuffix(statementSuggestionKey(trimmed), candidates))
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

		if m := match(`^rule\s+"([^"]+)"\s+expect\s+(.+)$`, trimmed); m != nil {
			rawRules = append(rawRules, model.Rule{Message: strings.TrimSpace(m[1]), Expression: strings.TrimSpace(m[2])})
			(*idx)++
			continue
		}

		if m := match(`^authorize\s+(all|list|get|create|update|delete)\s+when\s+(.+)$`, trimmed); m != nil {
			rawAuthz = append(rawAuthz, model.Authorization{Action: m[1], Expression: strings.TrimSpace(m[2])})
			(*idx)++
			continue
		}

		if m := match(`^([a-z][A-Za-z0-9_]*)\s*:\s*(`+marTypePattern+`)(?:\s+(.*))?$`, trimmed); m != nil {
			field := model.Field{Name: m[1], Type: m[2]}
			if err := parseFieldAttributes(&field, strings.TrimSpace(m[3]), ln.number); err != nil {
				return nil, err
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
	seenFields := map[string]bool{}
	for _, f := range ent.Fields {
		if !fieldNameRe.MatchString(f.Name) {
			return fmt.Errorf("field name %q in %s is invalid", f.Name, ent.Name)
		}
		if seenFields[f.Name] {
			return fmt.Errorf("duplicate field %q in %s", f.Name, ent.Name)
		}
		seenFields[f.Name] = true
		if f.Default != nil && f.Primary {
			return fmt.Errorf("field %s in %s cannot use default together with primary", f.Name, ent.Name)
		}
		if f.Default != nil && f.Auto {
			return fmt.Errorf("field %s in %s cannot use default together with auto", f.Name, ent.Name)
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

	exprVars := expr.AllowedVariablesWithBuiltins(allowedVars)
	authorizeOps := []string{"list", "get", "create", "update", "delete"}
	seenAction := map[string]bool{}
	var allExpression string
	var hasAll bool
	for _, authz := range rawAuthz {
		if seenAction[authz.Action] {
			return fmt.Errorf("duplicate authorize rule for %q", authz.Action)
		}
		seenAction[authz.Action] = true
		if _, err := expr.Parse(authz.Expression, expr.ParserOptions{AllowedVariables: exprVars}); err != nil {
			return fmt.Errorf("invalid authorization expression %q (%w)", authz.Expression, err)
		}
		if authz.Action == "all" {
			hasAll = true
			allExpression = authz.Expression
			continue
		}
		ent.Authorizations = append(ent.Authorizations, authz)
	}
	if hasAll {
		resolved := make([]model.Authorization, 0, len(authorizeOps))
		specificByAction := map[string]string{}
		for _, authz := range ent.Authorizations {
			specificByAction[authz.Action] = authz.Expression
		}
		for _, action := range authorizeOps {
			expression := allExpression
			if specific, ok := specificByAction[action]; ok {
				expression = specific
			}
			resolved = append(resolved, model.Authorization{Action: action, Expression: expression})
		}
		ent.Authorizations = resolved
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
	m := match(`^([a-z][A-Za-z0-9_]*)\s*:\s*(`+marTypePattern+`)$`, token)
	if m == nil {
		return fmt.Errorf("line %d: invalid field in type alias %s. Expected `name : Type` with Int/String/Bool/Float/Posix", lineNo, alias.Name)
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
				return nil, fmt.Errorf("line %d: action %s must contain at least one write step", ln.number, name)
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

		return nil, fmt.Errorf("line %d: invalid action statement %q", ln.number, trimmed)
	}

	return nil, fmt.Errorf("action %s is missing closing }", name)
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
				return nil, fmt.Errorf("line %d: %s %s in action %s must define at least one field", ln.number, kind, entityName, actionName)
			}
			return step, nil
		}

		assign := match(`^([a-z][A-Za-z0-9_]*)\s*:\s*(.+)$`, trimmed)
		if assign == nil {
			return nil, fmt.Errorf("line %d: invalid %s field %q. Expected `field: value`", ln.number, kind, trimmed)
		}
		field := assign[1]
		if seen[field] {
			return nil, fmt.Errorf("line %d: duplicate field %q in %s %s", ln.number, field, kind, entityName)
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

	return nil, fmt.Errorf("%s %s in action %s is missing closing }", kind, entityName, actionName)
}

func parseActionFieldExpr(raw string, lineNo int) (*model.ActionFieldExpr, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("line %d: action value cannot be empty", lineNo)
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

	switch app.Auth.EmailTransport {
	case "console":
		if strings.TrimSpace(app.Auth.SMTPHost) != "" {
			return fmt.Errorf("auth.smtp_host can only be used when email_transport smtp is selected")
		}
		if strings.TrimSpace(app.Auth.SMTPUsername) != "" {
			return fmt.Errorf("auth.smtp_username can only be used when email_transport smtp is selected")
		}
		if strings.TrimSpace(app.Auth.SMTPPasswordEnv) != "" {
			return fmt.Errorf("auth.smtp_password_env can only be used when email_transport smtp is selected")
		}
		if app.Auth.SMTPPort != 587 {
			return fmt.Errorf("auth.smtp_port can only be used when email_transport smtp is selected")
		}
		if !app.Auth.SMTPStartTLS {
			return fmt.Errorf("auth.smtp_starttls can only be used when email_transport smtp is selected")
		}
	case "smtp":
		if strings.TrimSpace(app.Auth.SMTPHost) == "" {
			return fmt.Errorf("auth.smtp_host is required when email_transport smtp is selected")
		}
		if app.Auth.SMTPPort < 1 || app.Auth.SMTPPort > 65535 {
			return fmt.Errorf("auth.smtp_port must be between 1 and 65535 when email_transport smtp is selected")
		}
		if strings.TrimSpace(app.Auth.SMTPUsername) == "" {
			return fmt.Errorf("auth.smtp_username is required when email_transport smtp is selected")
		}
		if strings.TrimSpace(app.Auth.SMTPPasswordEnv) == "" {
			return fmt.Errorf("auth.smtp_password_env is required when email_transport smtp is selected")
		}
		if !envVarNameRe.MatchString(strings.TrimSpace(app.Auth.SMTPPasswordEnv)) {
			return fmt.Errorf("auth.smtp_password_env %q must be a valid environment variable name", app.Auth.SMTPPasswordEnv)
		}
	default:
		return fmt.Errorf("auth.email_transport %q is not supported", app.Auth.EmailTransport)
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
			return fmt.Errorf("action %s must have at least one write step", action.Name)
		}
		for _, step := range action.Steps {
			entity := entityByName[step.Entity]
			if entity == nil {
				return fmt.Errorf("action %s references unknown entity %q", action.Name, step.Entity)
			}
			if step.Alias != "" {
				if step.Alias == "input" {
					return fmt.Errorf("action %s cannot use reserved alias name %q", action.Name, step.Alias)
				}
				if _, ok := inputFieldTypes[step.Alias]; ok {
					return fmt.Errorf("action %s alias %q conflicts with input field name", action.Name, step.Alias)
				}
				if existing, ok := availableAliases[step.Alias]; ok {
					return fmt.Errorf("action %s alias %q is already bound to %s", action.Name, step.Alias, existing)
				}
			}
			pkField := findEntityField(entity, entity.PrimaryKey)
			if pkField == nil {
				return fmt.Errorf("action %s references entity %s without a primary key field", action.Name, entity.Name)
			}
			assignments := map[string]model.ActionFieldExpr{}
			for _, item := range step.Values {
				field := findEntityField(entity, item.Field)
				if field == nil {
					return fmt.Errorf("action %s assigns unknown field %s.%s%s", action.Name, entity.Name, item.Field, suggest.DidYouMeanSuffix(item.Field, entityFieldNames(entity)))
				}
				if step.Kind == "create" && field.Primary && field.Auto {
					return fmt.Errorf("action %s cannot assign auto-generated field %s.%s", action.Name, entity.Name, item.Field)
				}
				assignments[item.Field] = item

				sourceType, err := resolveActionExprType(item.Expression, availableVariables, aliasFieldNames)
				if err != nil {
					return fmt.Errorf("action %s field %s.%s: %w", action.Name, entity.Name, item.Field, err)
				}
				if sourceType == "Null" {
					if !field.Optional && !field.Primary {
						return fmt.Errorf("action %s field %s.%s: null is only allowed on optional fields", action.Name, entity.Name, item.Field)
					}
					if field.Primary {
						return fmt.Errorf("action %s field %s.%s: null is not allowed on primary key fields", action.Name, entity.Name, item.Field)
					}
					continue
				}
				if !isTypeAssignable(field.Type, sourceType) {
					return fmt.Errorf("action %s field %s.%s expects %s but got %s", action.Name, entity.Name, item.Field, field.Type, sourceType)
				}
			}

			switch step.Kind {
			case "load":
				if step.Alias == "" {
					return fmt.Errorf("action %s load %s must bind its result to an alias", action.Name, entity.Name)
				}
				if len(assignments) != 1 {
					return fmt.Errorf("action %s load %s must only include primary key field %s", action.Name, entity.Name, entity.PrimaryKey)
				}
				if _, ok := assignments[entity.PrimaryKey]; !ok {
					return fmt.Errorf("action %s load %s must include primary key field %s", action.Name, entity.Name, entity.PrimaryKey)
				}
			case "create":
				writeSteps++
				for _, field := range entity.Fields {
					if field.Primary && field.Auto {
						continue
					}
					if field.Optional || field.Default != nil {
						continue
					}
					if _, ok := assignments[field.Name]; !ok {
						return fmt.Errorf("action %s is missing required field %s.%s", action.Name, entity.Name, field.Name)
					}
				}
			case "update":
				writeSteps++
				if _, ok := assignments[entity.PrimaryKey]; !ok {
					return fmt.Errorf("action %s update %s must include primary key field %s", action.Name, entity.Name, entity.PrimaryKey)
				}
				if len(assignments) == 1 {
					return fmt.Errorf("action %s update %s must change at least one non-primary field", action.Name, entity.Name)
				}
			case "delete":
				writeSteps++
				if len(assignments) != 1 {
					return fmt.Errorf("action %s delete %s must only include primary key field %s", action.Name, entity.Name, entity.PrimaryKey)
				}
				if _, ok := assignments[entity.PrimaryKey]; !ok {
					return fmt.Errorf("action %s delete %s must include primary key field %s", action.Name, entity.Name, entity.PrimaryKey)
				}
			default:
				return fmt.Errorf("action %s has unsupported step kind %q", action.Name, step.Kind)
			}

			if step.Alias != "" {
				availableAliases[step.Alias] = entity.Name
				for _, field := range entity.Fields {
					availableVariables[step.Alias+"."+field.Name] = field.Type
				}
			}
		}
		if writeSteps == 0 {
			return fmt.Errorf("action %s must have at least one create, update, or delete step", action.Name)
		}
	}
	return nil
}

func resolveActionExprType(raw string, variableTypes map[string]string, aliasFieldNames []string) (string, error) {
	parsed, err := expr.Parse(raw, expr.ParserOptions{AllowedVariables: allowedExprVariables(variableTypes)})
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "unknown identifier") {
			name := strings.Trim(strings.TrimPrefix(msg, "unknown identifier "), `"`)
			return "", fmt.Errorf("references unknown value %q%s", name, suggest.DidYouMeanSuffix(name, actionVariableNames(variableTypes, aliasFieldNames)))
		}
		return "", err
	}
	return inferActionExprType(parsed, variableTypes)
}

func allowedExprVariables(variableTypes map[string]string) map[string]struct{} {
	out := make(map[string]struct{}, len(variableTypes))
	for name := range variableTypes {
		out[name] = struct{}{}
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
			return "", fmt.Errorf("unsupported literal value")
		}
	case expr.Variable:
		t := variableTypes[n.Name]
		if t == "" {
			return "", fmt.Errorf("references unknown value %q", n.Name)
		}
		return t, nil
	case expr.Unary:
		rightType, err := inferActionExprType(n.Right, variableTypes)
		if err != nil {
			return "", err
		}
		switch n.Op {
		case "not":
			return "Bool", nil
		case "-":
			switch rightType {
			case "Int":
				return "Int", nil
			case "Float":
				return "Float", nil
			default:
				return "", fmt.Errorf("operator - expects Int or Float")
			}
		default:
			return "", fmt.Errorf("unknown unary operator %q", n.Op)
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
		case "and", "or", "==", "!=", ">", ">=", "<", "<=":
			return "Bool", nil
		case "+":
			if leftType == "String" || rightType == "String" {
				return "String", nil
			}
			if leftType == "Posix" && rightType == "Int" {
				return "Posix", nil
			}
			if leftType == "Int" && rightType == "Posix" {
				return "Posix", nil
			}
			if leftType == "Float" || rightType == "Float" {
				return "Float", nil
			}
			if leftType == "Int" && rightType == "Int" {
				return "Int", nil
			}
			return "", fmt.Errorf("operator + expects compatible values")
		case "-":
			if leftType == "Posix" && rightType == "Int" {
				return "Posix", nil
			}
			if leftType == "Posix" && rightType == "Posix" {
				return "Int", nil
			}
			if leftType == "Float" || rightType == "Float" {
				return "Float", nil
			}
			if leftType == "Int" && rightType == "Int" {
				return "Int", nil
			}
			return "", fmt.Errorf("operator - expects compatible numeric values")
		case "*":
			if leftType == "Float" || rightType == "Float" {
				return "Float", nil
			}
			if leftType == "Int" && rightType == "Int" {
				return "Int", nil
			}
			return "", fmt.Errorf("operator * expects numeric values")
		case "/":
			if (leftType == "Int" || leftType == "Float") && (rightType == "Int" || rightType == "Float") {
				return "Float", nil
			}
			return "", fmt.Errorf("operator / expects numeric values")
		default:
			return "", fmt.Errorf("unknown operator %q", n.Op)
		}
	case expr.Call:
		switch n.Name {
		case "contains", "starts_with", "ends_with", "matches":
			return "Bool", nil
		case "length":
			return "Int", nil
		default:
			return "", fmt.Errorf("unsupported function %q", n.Name)
		}
	default:
		return "", fmt.Errorf("unsupported expression type")
	}
}

func isTypeAssignable(targetType, sourceType string) bool {
	if targetType == sourceType {
		return true
	}
	if targetType == "Float" && sourceType == "Int" {
		return true
	}
	if targetType == "Posix" && sourceType == "Int" {
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

func hasFieldName(ent *model.Entity, name string) bool {
	for _, f := range ent.Fields {
		if f.Name == name {
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
		return fmt.Errorf("line %d: %w", lineNo, err)
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
				return fmt.Errorf("line %d: default requires a literal value", lineNo)
			}
			defaultValue, err := parseFieldDefaultLiteral(field.Type, tokens[i+1], lineNo)
			if err != nil {
				return err
			}
			field.Default = defaultValue
			i++
		default:
			return fmt.Errorf("line %d: unknown field attribute %q", lineNo, tokens[i])
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
		return nil, fmt.Errorf("unterminated string literal in field attributes")
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
			return nil, fmt.Errorf("line %d: field default for %s must be a string literal", lineNo, fieldType)
		}
		unquoted, err := strconv.Unquote(raw)
		if err != nil {
			return nil, fmt.Errorf("line %d: invalid string literal %q", lineNo, raw)
		}
		return unquoted, nil
	case "Bool":
		if raw == "true" {
			return true, nil
		}
		if raw == "false" {
			return false, nil
		}
		return nil, fmt.Errorf("line %d: field default for %s must be true or false", lineNo, fieldType)
	case "Int", "Posix":
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: field default for %s must be an integer literal", lineNo, fieldType)
		}
		return n, nil
	case "Float":
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return float64(n), nil
		}
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: field default for %s must be a numeric literal", lineNo, fieldType)
		}
		return f, nil
	default:
		return nil, fmt.Errorf("line %d: unsupported field type %s", lineNo, fieldType)
	}
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
