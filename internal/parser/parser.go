package parser

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"mar/internal/expr"
	"mar/internal/model"
	"mar/internal/sexp"
)

type namedValue struct {
	Name string
	Body sexp.Node
}

type queryDef struct {
	Name         string
	Parameters   []string
	EntitySymbol string
	Where        sexp.Node
	OrderBy      string
	OrderDir     string
	Limit        *int
}

type functionDef struct {
	Name       string
	Parameters []string
	Body       sexp.Node
	LineNo     int
}

type recordDef struct {
	Name   string
	Fields []model.RecordField
}

type typeDef struct {
	Name     string
	Variants []model.TypeVariant
}

type screenDef struct {
	Name       string
	Parameters []string
	Body       sexp.Node
}

type actionDef struct {
	Name string
	Body sexp.Node
}

type appDef struct {
	Name          string
	ConfigRef     string
	AuthRef       string
	EntitySymbols []string
	QuerySymbols  []string
	ActionSymbols []string
	ScreenSymbols []string
}

// Parse reads Mar source in the simplified Scheme-like syntax and returns an App model.
func Parse(source string) (*model.App, error) {
	nodes, err := sexp.Parse(source)
	if err != nil {
		return nil, err
	}

	values := map[string]namedValue{}
	queries := map[string]*queryDef{}
	functions := map[string]*functionDef{}
	records := map[string]*recordDef{}
	types := map[string]*typeDef{}
	screens := map[string]*screenDef{}
	actions := map[string]*actionDef{}
	var appDecl *appDef

	for _, node := range nodes {
		if node.Kind != sexp.KindList || len(node.Children) == 0 {
			return nil, parseError(node, "top-level form must be a list")
		}
		head, ok := symbolValue(node.Children[0])
		if !ok {
			return nil, parseError(node.Children[0], "top-level form name must be a symbol")
		}
		switch head {
		case "define":
			if len(node.Children) != 3 {
				return nil, parseError(node, "define expects a name/signature and a body")
			}
			if signature := node.Children[1]; signature.Kind == sexp.KindList {
				query, fn, screen, err := parseCallableDef(node)
				if err != nil {
					return nil, err
				}
				if query != nil {
					queries[query.Name] = query
				}
				if fn != nil {
					functions[fn.Name] = fn
				}
				if screen != nil {
					return nil, parseError(node.Children[2], "unknown define body %q; use define-screen", "screen")
				}
				continue
			}
			name, ok := symbolValue(node.Children[1])
			if !ok {
				return nil, parseError(node.Children[1], "define name must be a symbol")
			}
			if bodyItems, err := listChildren(node.Children[2], "define body"); err == nil && len(bodyItems) > 0 {
				if head, _ := symbolValue(bodyItems[0]); head == "action" {
					actions[name] = &actionDef{Name: name, Body: node.Children[2]}
					continue
				} else if head == "screen" {
					return nil, parseError(bodyItems[0], "unknown define body %q; use define-screen", head)
				}
			}
			values[name] = namedValue{Name: name, Body: node.Children[2]}
		case "define-screen":
			screen, err := parseScreenDef(node)
			if err != nil {
				return nil, err
			}
			screens[screen.Name] = screen
		case "define-record":
			record, err := parseRecordDef(node)
			if err != nil {
				return nil, err
			}
			records[record.Name] = record
		case "defrecord":
			return nil, parseError(node.Children[0], "unknown top-level form %q; use define-record", head)
		case "define-type":
			typeDef, err := parseTypeDef(node)
			if err != nil {
				return nil, err
			}
			types[typeDef.Name] = typeDef
		case "define-app":
			if appDecl != nil {
				return nil, parseError(node, "define-app already declared")
			}
			appDecl, err = parseAppDef(node)
			if err != nil {
				return nil, err
			}
		case "defapp":
			return nil, parseError(node.Children[0], "unknown top-level form %q; use define-app", head)
		default:
			return nil, parseError(node.Children[0], "unknown top-level form %q", head)
		}
	}

	if appDecl == nil {
		return nil, fmt.Errorf("define-app is required")
	}

	app := &model.App{
		AppName:  appDecl.Name,
		Port:     4200,
		Database: defaultDatabaseName(appDecl.Name),
		Auth:     defaultAuthConfig(appDecl.Name),
	}

	for _, record := range records {
		app.Records = append(app.Records, model.Record{
			Name:   canonicalFieldName(record.Name),
			Fields: record.Fields,
		})
	}

	for _, typ := range types {
		app.Types = append(app.Types, model.EnumType{
			Name:     canonicalFieldName(typ.Name),
			Variants: typ.Variants,
		})
	}

	for _, fn := range functions {
		app.Functions = append(app.Functions, model.Function{
			Name:       canonicalFunctionName(fn.Name),
			Parameters: canonicalFunctionParameters(fn.Parameters),
			Expression: sexp.InlineString(fn.Body),
			LineNo:     fn.LineNo,
		})
	}

	if appDecl.ConfigRef != "" {
		cfg, err := parseConfigValue(values[appDecl.ConfigRef])
		if err != nil {
			return nil, err
		}
		app.Port = cfg.Port
		if cfg.Database != "" {
			app.Database = cfg.Database
		}
		if cfg.IOS != nil {
			app.IOS = cfg.IOS
		}
		if cfg.Public != nil {
			app.Public = cfg.Public
		}
		if cfg.System != nil {
			app.System = cfg.System
		}
	}
	if appDecl.AuthRef != "" {
		auth, err := parseAuthValue(values[appDecl.AuthRef])
		if err != nil {
			return nil, err
		}
		applyAuthSettings(app.Auth, auth)
	}

	var userExtension *model.Entity
	app.Entities = []model.Entity{}
	for _, symbol := range appDecl.EntitySymbols {
		value, ok := values[symbol]
		if !ok {
			return nil, fmt.Errorf("define-app references unknown entity %q", symbol)
		}
		entity, err := parseEntityValue(value)
		if err != nil {
			return nil, err
		}
		if entity.Name == "User" {
			userExtension = &entity
			continue
		}
		app.Entities = append(app.Entities, entity)
	}

	userEntity := buildBuiltinUser()
	if userExtension != nil {
		userEntity = mergeUserEntity(userEntity, *userExtension)
	}
	app.Entities = append([]model.Entity{userEntity}, app.Entities...)

	entityByName := map[string]*model.Entity{}
	for i := range app.Entities {
		entityByName[app.Entities[i].Name] = &app.Entities[i]
	}

	functionByName := map[string]*model.Function{}
	for i := range app.Functions {
		functionByName[app.Functions[i].Name] = &app.Functions[i]
	}

	recordByName := map[string]*model.Record{}
	for i := range app.Records {
		recordByName[app.Records[i].Name] = &app.Records[i]
	}

	typeByName := map[string]*model.EnumType{}
	for i := range app.Types {
		if _, exists := typeByName[app.Types[i].Name]; exists {
			return nil, fmt.Errorf("duplicate type %q", app.Types[i].Name)
		}
		typeByName[app.Types[i].Name] = &app.Types[i]
	}
	variantOwners := map[string]string{}
	for _, typ := range app.Types {
		for _, variant := range typ.Variants {
			if owner, exists := variantOwners[variant.Name]; exists {
				return nil, fmt.Errorf("type variant %q is declared in both %s and %s", variant.Name, owner, typ.Name)
			}
			variantOwners[variant.Name] = typ.Name
		}
	}

	for i := range app.Records {
		if err := validateRecordTypes(&app.Records[i], recordByName, typeByName, entityByName); err != nil {
			return nil, err
		}
	}
	for i := range app.Types {
		if err := validateSumType(&app.Types[i], recordByName, typeByName, entityByName); err != nil {
			return nil, err
		}
	}

	for i := range app.Functions {
		if err := validateFunctionExpression(&app.Functions[i], functionByName, recordByName, typeByName, entityByName); err != nil {
			return nil, err
		}
	}

	for i := range app.Entities {
		if err := validateEntityExpressions(&app.Entities[i], functionByName, recordByName, typeByName, entityByName); err != nil {
			return nil, err
		}
		if err := validateEntitySchema(&app.Entities[i], entityByName); err != nil {
			return nil, err
		}
	}

	selectedQueries := queries
	if len(appDecl.QuerySymbols) > 0 {
		selectedQueries = map[string]*queryDef{}
		for _, symbol := range appDecl.QuerySymbols {
			query := queries[symbol]
			if query == nil {
				return nil, fmt.Errorf("define-app references unknown query %q", symbol)
			}
			selectedQueries[symbol] = query
			app.Queries = append(app.Queries, model.Query{
				Name:           canonicalFunctionName(query.Name),
				Parameters:     canonicalFunctionParameters(query.Parameters),
				ParameterTypes: map[string]string{},
				Entity:         canonicalTypeName(query.EntitySymbol),
				Where:          inlineNodeString(query.Where),
				OrderBy:        canonicalFieldName(query.OrderBy),
				OrderDir:       query.OrderDir,
				Limit:          query.Limit,
			})
		}
	}

	if len(appDecl.ScreenSymbols) > 0 {
		frontend := &model.Frontend{Screens: []model.FrontendScreen{}}
		for _, symbol := range appDecl.ScreenSymbols {
			if screenDef := screens[symbol]; screenDef != nil {
				screen, err := parseScreenDefinition(screenDef.Name, screenDef.Parameters, screenDef.Body, selectedQueries)
				if err != nil {
					return nil, err
				}
				frontend.Screens = append(frontend.Screens, screen)
				continue
			}
			return nil, fmt.Errorf("define-app references unknown screen %q", symbol)
		}
		app.Screens = frontend
	}

	for _, symbol := range appDecl.ActionSymbols {
		actionValue := actions[symbol]
		if actionValue == nil {
			return nil, fmt.Errorf("define-app references unknown action %q", symbol)
		}
		inputAlias, action, err := parseActionDefinition(actionValue.Name, actionValue.Body, entityByName)
		if err != nil {
			return nil, err
		}
		app.InputAliases = append(app.InputAliases, inputAlias)
		app.Actions = append(app.Actions, action)
	}

	aliasByName := map[string]*model.TypeAlias{}
	for i := range app.InputAliases {
		aliasByName[app.InputAliases[i].Name] = &app.InputAliases[i]
	}

	for _, query := range queries {
		entity := entityByName[canonicalTypeName(query.EntitySymbol)]
		if entity == nil {
			return nil, fmt.Errorf("query %s references unknown entity %q", query.Name, query.EntitySymbol)
		}
		if query.Where.Kind != "" {
			allowed := queryAllowedVariables(entity)
			for _, param := range canonicalFunctionParameters(query.Parameters) {
				allowed[param] = struct{}{}
			}
			if _, err := expr.Parse(sexp.InlineString(query.Where), expr.ParserOptions{
				AllowedVariables: allowed,
				AllowedFunctions: allowedFunctionArities(functionByName),
				AllowedRecords:   allowedRecordFields(recordByName),
				AllowedVariants:  allowedTypeVariants(typeByName),
			}); err != nil {
				return nil, fmt.Errorf("query %s where: %w", query.Name, err)
			}
			parameterTypes, err := validateBackendQueryWhere(query.Name, inlineNodeString(query.Where), entity, canonicalFunctionParameters(query.Parameters), functionByName, recordByName, typeByName, entityByName)
			if err != nil {
				return nil, err
			}
			for i := range app.Queries {
				if app.Queries[i].Name == canonicalFunctionName(query.Name) {
					app.Queries[i].ParameterTypes = parameterTypes
				}
			}
		} else if len(query.Parameters) > 0 {
			return nil, fmt.Errorf("query %s parameter %s: type could not be inferred", query.Name, canonicalFunctionParameters(query.Parameters)[0])
		}
		if query.OrderBy != "" && findEntityField(entity, canonicalFieldName(query.OrderBy)) == nil {
			return nil, fmt.Errorf("query %s order-by references unknown field %q", query.Name, query.OrderBy)
		}
	}

	for _, action := range app.Actions {
		if err := validateActionExpressions(&action, aliasByName, functionByName, recordByName, typeByName, entityByName); err != nil {
			return nil, err
		}
	}

	if app.Screens != nil {
		if err := validateFrontendScreens(app.Screens, app.Queries, app.Actions, aliasByName, functionByName, recordByName, typeByName, entityByName); err != nil {
			return nil, err
		}
	}
	if err := validateUnusedTopLevelDefinitions(appDecl, queries, actions, screens, values); err != nil {
		return nil, err
	}
	if err := validateUnusedFunctions(app); err != nil {
		return nil, err
	}

	return app, nil
}

type configValue struct {
	Database string
	Port     int
	IOS      *model.IOSConfig
	Public   *model.PublicConfig
	System   *model.SystemConfig
}

func parseConfigValue(value namedValue) (*configValue, error) {
	if value.Name == "" {
		return nil, fmt.Errorf("define-app references unknown config")
	}
	body := value.Body
	if body.Kind != sexp.KindList {
		return nil, parseError(body, "config %s must be a list of clauses", value.Name)
	}
	cfg := &configValue{Port: 4200}
	for _, clause := range body.Children {
		items, err := listChildren(clause, "config clause")
		if err != nil {
			return nil, err
		}
		head, _ := symbolValue(items[0])
		switch head {
		case "database":
			if len(items) != 2 || items[1].Kind != sexp.KindString {
				return nil, parseError(clause, "(database ...) expects one string")
			}
			cfg.Database = items[1].Value
		case "server":
			nestedClauses, err := nestedClauseList(items, clause, 1, "server")
			if err != nil {
				return nil, err
			}
			for _, nested := range nestedClauses {
				parts, err := listChildren(nested, "server clause")
				if err != nil {
					return nil, err
				}
				key, _ := symbolValue(parts[0])
				switch key {
				case "port":
					port, err := intLiteral(parts[1])
					if err != nil {
						return nil, parseError(parts[1], "port must be an integer")
					}
					cfg.Port = port
				default:
					return nil, parseError(parts[0], "unknown server clause %q", key)
				}
			}
		case "ios":
			nestedClauses, err := nestedClauseList(items, clause, 1, "ios")
			if err != nil {
				return nil, err
			}
			iosCfg := &model.IOSConfig{}
			for _, nested := range nestedClauses {
				parts, err := listChildren(nested, "ios clause")
				if err != nil {
					return nil, err
				}
				key, _ := symbolValue(parts[0])
				switch key {
				case "bundle-identifier":
					iosCfg.BundleIdentifier = stringLiteral(parts[1])
				case "display-name":
					iosCfg.DisplayName = stringLiteral(parts[1])
				case "server-url":
					iosCfg.ServerURL = stringLiteral(parts[1])
				default:
					return nil, parseError(parts[0], "unknown ios clause %q", key)
				}
			}
			cfg.IOS = iosCfg
		case "public":
			nestedClauses, err := nestedClauseList(items, clause, 1, "public")
			if err != nil {
				return nil, err
			}
			publicCfg := &model.PublicConfig{}
			for _, nested := range nestedClauses {
				parts, err := listChildren(nested, "public clause")
				if err != nil {
					return nil, err
				}
				key, _ := symbolValue(parts[0])
				switch key {
				case "dir":
					publicCfg.Dir = stringLiteral(parts[1])
				case "mount":
					publicCfg.Mount = stringLiteral(parts[1])
				case "spa-fallback":
					publicCfg.SPAFallback = stringLiteral(parts[1])
				default:
					return nil, parseError(parts[0], "unknown public clause %q", key)
				}
			}
			cfg.Public = publicCfg
		case "system":
			nestedClauses, err := nestedClauseList(items, clause, 1, "system")
			if err != nil {
				return nil, err
			}
			systemCfg := &model.SystemConfig{}
			for _, nested := range nestedClauses {
				parts, err := listChildren(nested, "system clause")
				if err != nil {
					return nil, err
				}
				key, _ := symbolValue(parts[0])
				switch key {
				case "request-logs-buffer":
					value, err := intLiteral(parts[1])
					if err != nil {
						return nil, parseError(parts[1], "request-logs-buffer must be an integer")
					}
					systemCfg.RequestLogsBuffer = value
				case "http-max-request-body-mb":
					value, err := intLiteral(parts[1])
					if err != nil {
						return nil, parseError(parts[1], "http-max-request-body-mb must be an integer")
					}
					systemCfg.HTTPMaxRequestBodyMB = &value
				case "sqlite-journal-mode":
					value, err := symbolOrStringValue(parts[1])
					if err != nil {
						return nil, parseError(parts[1], "sqlite-journal-mode must be a symbol or string")
					}
					systemCfg.SQLiteJournalMode = &value
				case "sqlite-synchronous":
					value, err := symbolOrStringValue(parts[1])
					if err != nil {
						return nil, parseError(parts[1], "sqlite-synchronous must be a symbol or string")
					}
					systemCfg.SQLiteSynchronous = &value
				case "sqlite-foreign-keys":
					value, err := boolLiteral(parts[1])
					if err != nil {
						return nil, parseError(parts[1], "sqlite-foreign-keys must be true or false")
					}
					systemCfg.SQLiteForeignKeys = &value
				case "sqlite-busy-timeout-ms":
					value, err := intLiteral(parts[1])
					if err != nil {
						return nil, parseError(parts[1], "sqlite-busy-timeout-ms must be an integer")
					}
					systemCfg.SQLiteBusyTimeoutMs = &value
				case "sqlite-wal-autocheckpoint":
					value, err := intLiteral(parts[1])
					if err != nil {
						return nil, parseError(parts[1], "sqlite-wal-autocheckpoint must be an integer")
					}
					systemCfg.SQLiteWALAutoCheckpoint = &value
				case "sqlite-journal-size-limit-mb":
					value, err := intLiteral(parts[1])
					if err != nil {
						return nil, parseError(parts[1], "sqlite-journal-size-limit-mb must be an integer")
					}
					systemCfg.SQLiteJournalSizeLimitMB = &value
				case "sqlite-mmap-size-mb":
					value, err := intLiteral(parts[1])
					if err != nil {
						return nil, parseError(parts[1], "sqlite-mmap-size-mb must be an integer")
					}
					systemCfg.SQLiteMmapSizeMB = &value
				case "sqlite-cache-size-kb":
					value, err := intLiteral(parts[1])
					if err != nil {
						return nil, parseError(parts[1], "sqlite-cache-size-kb must be an integer")
					}
					systemCfg.SQLiteCacheSizeKB = &value
				default:
					return nil, parseError(parts[0], "unknown system clause %q", key)
				}
			}
			cfg.System = systemCfg
		default:
			return nil, parseError(items[0], "unknown config clause %q", head)
		}
	}
	return cfg, nil
}

type authValue struct {
	CodeTTLMinutes           *int
	SessionTTLHours          *int
	AuthRequestCodeRateLimit *int
	AuthLoginRateLimit       *int
	AdminUISessionTTLHours   *int
	SecurityFramePolicy      *string
	SecurityReferrerPolicy   *string
	SecurityContentNoSniff   *bool
	EmailFrom                string
	EmailSubject             string
	SMTPHost                 string
	SMTPPort                 *int
	SMTPUsername             string
	SMTPPasswordEnv          string
	SMTPStartTLS             *bool
}

func parseAuthValue(value namedValue) (*authValue, error) {
	if value.Name == "" {
		return nil, fmt.Errorf("define-app references unknown auth")
	}
	body := value.Body
	if body.Kind != sexp.KindList {
		return nil, parseError(body, "auth %s must be a list of clauses", value.Name)
	}
	out := &authValue{}
	for _, clause := range body.Children {
		items, err := listChildren(clause, "auth clause")
		if err != nil {
			return nil, err
		}
		head, _ := symbolValue(items[0])
		switch head {
		case "code-ttl-minutes":
			v, err := intLiteral(items[1])
			if err != nil {
				return nil, parseError(items[1], "code-ttl-minutes must be an integer")
			}
			out.CodeTTLMinutes = &v
		case "session-ttl-hours":
			v, err := intLiteral(items[1])
			if err != nil {
				return nil, parseError(items[1], "session-ttl-hours must be an integer")
			}
			out.SessionTTLHours = &v
		case "auth-request-code-rate-limit-per-minute":
			v, err := intLiteral(items[1])
			if err != nil {
				return nil, parseError(items[1], "auth-request-code-rate-limit-per-minute must be an integer")
			}
			out.AuthRequestCodeRateLimit = &v
		case "auth-login-rate-limit-per-minute":
			v, err := intLiteral(items[1])
			if err != nil {
				return nil, parseError(items[1], "auth-login-rate-limit-per-minute must be an integer")
			}
			out.AuthLoginRateLimit = &v
		case "admin-ui-session-ttl-hours":
			v, err := intLiteral(items[1])
			if err != nil {
				return nil, parseError(items[1], "admin-ui-session-ttl-hours must be an integer")
			}
			out.AdminUISessionTTLHours = &v
		case "security-frame-policy":
			v, err := symbolOrStringValue(items[1])
			if err != nil {
				return nil, parseError(items[1], "security-frame-policy must be a symbol or string")
			}
			out.SecurityFramePolicy = &v
		case "security-referrer-policy":
			v, err := symbolOrStringValue(items[1])
			if err != nil {
				return nil, parseError(items[1], "security-referrer-policy must be a symbol or string")
			}
			out.SecurityReferrerPolicy = &v
		case "security-content-type-nosniff":
			v, err := boolLiteral(items[1])
			if err != nil {
				return nil, parseError(items[1], "security-content-type-nosniff must be true or false")
			}
			out.SecurityContentNoSniff = &v
		case "from":
			out.EmailFrom = stringLiteral(items[1])
		case "subject":
			out.EmailSubject = stringLiteral(items[1])
		case "smtp-host":
			out.SMTPHost = stringLiteral(items[1])
		case "smtp-port":
			v, err := intLiteral(items[1])
			if err != nil {
				return nil, parseError(items[1], "smtp-port must be an integer")
			}
			out.SMTPPort = &v
		case "smtp-username":
			out.SMTPUsername = stringLiteral(items[1])
		case "smtp-password-env":
			out.SMTPPasswordEnv = stringLiteral(items[1])
		case "smtp-starttls":
			v, err := boolLiteral(items[1])
			if err != nil {
				return nil, parseError(items[1], "smtp-starttls must be true or false")
			}
			out.SMTPStartTLS = &v
		default:
			return nil, parseError(items[0], "unknown auth clause %q", head)
		}
	}
	return out, nil
}

func parseEntityValue(value namedValue) (model.Entity, error) {
	body, err := unwrapDefinitionBody(value.Body, "entity", "entity "+value.Name)
	if err != nil {
		return model.Entity{}, err
	}
	entity := model.Entity{
		Name:       canonicalTypeName(value.Name),
		Table:      canonicalFieldName(value.Name),
		Resource:   "/" + strings.ReplaceAll(pluralizeSnake(value.Name), "_", "-"),
		PrimaryKey: "id",
		Fields: []model.Field{
			{Name: "id", Type: "Int", Primary: true, Auto: true},
		},
	}

	defaults := map[string]sexp.Node{}
	belongsTo := []struct {
		field  string
		target string
	}{}
	uniqueClauses := [][]string{}

	for _, clause := range body.Children {
		items, err := listChildren(clause, "entity clause")
		if err != nil {
			return model.Entity{}, err
		}
		head, _ := symbolValue(items[0])
		switch head {
		case "fields":
			fieldClauses, err := nestedClauseList(items, clause, 1, "fields")
			if err != nil {
				return model.Entity{}, err
			}
			for _, fieldClause := range fieldClauses {
				parts, err := listChildren(fieldClause, "field")
				if err != nil {
					return model.Entity{}, err
				}
				if len(parts) < 2 || len(parts) > 3 {
					return model.Entity{}, parseError(fieldClause, "field must look like (name type) or (name type optional)")
				}
				fieldName, _ := symbolValue(parts[0])
				typeName, _ := symbolValue(parts[1])
				fieldType, err := mapPrimitiveType(typeName)
				if err != nil {
					return model.Entity{}, fmt.Errorf("entity %s field %s: %w", value.Name, fieldName, err)
				}
				field := model.Field{Name: canonicalFieldName(fieldName), Type: fieldType}
				if len(parts) == 3 {
					modifier, _ := symbolValue(parts[2])
					if modifier != "optional" {
						return model.Entity{}, parseError(parts[2], "unknown field modifier %q", modifier)
					}
					field.Optional = true
				}
				entity.Fields = append(entity.Fields, field)
			}
		case "belongs-to":
			relClauses, err := nestedClauseList(items, clause, 1, "belongs-to")
			if err != nil {
				return model.Entity{}, err
			}
			for _, relClause := range relClauses {
				parts, err := listChildren(relClause, "belongs-to item")
				if err != nil {
					return model.Entity{}, err
				}
				if len(parts) < 1 || len(parts) > 2 {
					return model.Entity{}, parseError(relClause, "belongs-to item must look like (user) or (reviewer user)")
				}
				fieldName, _ := symbolValue(parts[0])
				target := fieldName
				if len(parts) == 2 {
					target, _ = symbolValue(parts[1])
				}
				belongsTo = append(belongsTo, struct {
					field  string
					target string
				}{field: fieldName, target: target})
			}
		case "defaults":
			defaultClauses, err := nestedClauseList(items, clause, 1, "defaults")
			if err != nil {
				return model.Entity{}, err
			}
			for _, defaultClause := range defaultClauses {
				parts, err := listChildren(defaultClause, "default")
				if err != nil {
					return model.Entity{}, err
				}
				if len(parts) != 2 {
					return model.Entity{}, parseError(defaultClause, "default must look like (field value)")
				}
				fieldName, _ := symbolValue(parts[0])
				defaults[fieldName] = parts[1]
			}
		case "unique":
			constraintClauses, err := nestedClauseList(items, clause, 1, "unique")
			if err != nil {
				return model.Entity{}, err
			}
			for _, constraintClause := range constraintClauses {
				parts, err := listChildren(constraintClause, "unique constraint")
				if err != nil {
					return model.Entity{}, err
				}
				if len(parts) == 0 {
					return model.Entity{}, parseError(constraintClause, "unique constraint must include at least one field")
				}
				fields := make([]string, 0, len(parts))
				for _, part := range parts {
					fieldName, ok := symbolValue(part)
					if !ok {
						return model.Entity{}, parseError(part, "unique fields must be symbols")
					}
					fields = append(fields, canonicalFieldName(fieldName))
				}
				uniqueClauses = append(uniqueClauses, fields)
			}
		case "validate":
			if len(items) != 2 {
				return model.Entity{}, parseError(clause, "validate expects one expression")
			}
			entity.Validate = sexp.InlineString(items[1])
		case "authorize":
			authClauses, err := nestedClauseList(items, clause, 1, "authorize")
			if err != nil {
				return model.Entity{}, err
			}
			for _, authClause := range authClauses {
				parts, err := listChildren(authClause, "authorize item")
				if err != nil {
					return model.Entity{}, err
				}
				if len(parts) != 2 {
					return model.Entity{}, parseError(authClause, "authorize item must look like (read expr) or ((read update) expr)")
				}
				actions, err := parseAuthorizeActions(parts[0])
				if err != nil {
					return model.Entity{}, err
				}
				for _, action := range actions {
					entity.Authorizations = append(entity.Authorizations, model.Authorization{
						Action:     action,
						Expression: sexp.InlineString(parts[1]),
						LineNo:     authClause.Line,
					})
				}
			}
		default:
			return model.Entity{}, parseError(items[0], "unknown entity clause %q", head)
		}
	}

	for _, relation := range belongsTo {
		field := model.Field{
			Name:           canonicalFieldName(relation.field),
			Type:           "Int",
			RelationEntity: canonicalTypeName(relation.target),
		}
		if defaultNode, ok := defaults[relation.field]; ok {
			if defaultNode.Kind == sexp.KindSymbol && defaultNode.Value == "current-user" {
				field.CurrentUser = true
				field.RelationEntity = "User"
			} else {
				return model.Entity{}, fmt.Errorf("entity %s default %s: belongs-to defaults currently only support current-user", value.Name, relation.field)
			}
		}
		entity.Fields = append(entity.Fields, field)
	}

	for i := 1; i < len(entity.Fields); i++ {
		field := &entity.Fields[i]
		original := strings.ReplaceAll(field.Name, "_", "-")
		if defaultNode, ok := defaults[original]; ok && field.RelationEntity == "" {
			literal, err := literalValue(defaultNode)
			if err != nil {
				return model.Entity{}, fmt.Errorf("entity %s default %s: %w", value.Name, original, err)
			}
			if err := validateFieldDefaultLiteral(value.Name, original, *field, literal); err != nil {
				return model.Entity{}, err
			}
			field.Default = literal
		}
	}

	for _, fields := range uniqueClauses {
		entity.Unique = append(entity.Unique, model.UniqueConstraint{Fields: fields})
	}

	entity.Fields = append(entity.Fields,
		model.Field{Name: "created_at", Type: "DateTime", Auto: true},
		model.Field{Name: "updated_at", Type: "DateTime", Auto: true},
	)

	return entity, nil
}

func parseScreenDefinition(name string, parameters []string, bodyNode sexp.Node, queries map[string]*queryDef) (model.FrontendScreen, error) {
	body, err := unwrapDefinitionBody(bodyNode, "screen", "screen "+name)
	if err != nil {
		return model.FrontendScreen{}, err
	}
	screen := model.FrontendScreen{
		Name:       canonicalScreenName(name),
		Parameters: canonicalFunctionParameters(parameters),
		Sections:   []model.FrontendSection{},
		LineNo:     1,
	}

	var hasMsg bool
	var hasInit bool
	var hasUpdate bool
	var hasView bool
	var initNode sexp.Node
	var updateNode sexp.Node
	var viewRawNodes []sexp.Node
	var sectionNodes []sexp.Node
	for _, clause := range body.Children {
		items, err := listChildren(clause, "screen clause")
		if err != nil {
			return model.FrontendScreen{}, err
		}
		head, _ := symbolValue(items[0])
		switch head {
		case "msg":
			hasMsg = true
		case "init":
			hasInit = true
		case "update":
			hasUpdate = true
		case "view":
			hasView = true
		}
	}

	for _, clause := range body.Children {
		items, err := listChildren(clause, "screen clause")
		if err != nil {
			return model.FrontendScreen{}, err
		}
		head, _ := symbolValue(items[0])
		switch head {
		case "title":
			if len(items) != 2 {
				return model.FrontendScreen{}, parseError(clause, "title expects one value")
			}
			switch items[1].Kind {
			case sexp.KindString:
				screen.Title = items[1].Value
			case sexp.KindSymbol:
				screen.TitleExpression = canonicalFieldName(items[1].Value)
			default:
				return model.FrontendScreen{}, parseError(items[1], "title must be a string or symbol")
			}
		case "msg":
			for _, item := range items[1:] {
				switch item.Kind {
				case sexp.KindSymbol:
					screen.Messages = append(screen.Messages, model.FrontendMessage{Name: canonicalFieldName(item.Value)})
				case sexp.KindList:
					if len(item.Children) == 0 || item.Children[0].Kind != sexp.KindSymbol {
						return model.FrontendScreen{}, parseError(item, "message pattern must start with a symbol")
					}
					message := model.FrontendMessage{Name: canonicalFieldName(item.Children[0].Value)}
					for _, param := range item.Children[1:] {
						if param.Kind != sexp.KindSymbol {
							return model.FrontendScreen{}, parseError(param, "message parameter must be a symbol")
						}
						message.Parameters = append(message.Parameters, canonicalFieldName(param.Value))
					}
					screen.Messages = append(screen.Messages, message)
				default:
					return model.FrontendScreen{}, parseError(item, "invalid msg clause item")
				}
			}
		case "init":
			if len(items) != 2 {
				return model.FrontendScreen{}, parseError(clause, "init expects a single expression")
			}
			screen.InitExpression = sexp.InlineString(items[1])
			initNode = items[1]
		case "update":
			if len(items) != 4 {
				return model.FrontendScreen{}, parseError(clause, "update expects (update msg model expr)")
			}
			msgName, ok := symbolValue(items[1])
			if !ok {
				return model.FrontendScreen{}, parseError(items[1], "update message parameter must be a symbol")
			}
			modelName, ok := symbolValue(items[2])
			if !ok {
				return model.FrontendScreen{}, parseError(items[2], "update model parameter must be a symbol")
			}
			screen.UpdateMessage = canonicalFieldName(msgName)
			screen.UpdateModel = canonicalFieldName(modelName)
			screen.UpdateBody = sexp.InlineString(items[3])
			updateNode = items[3]
		case "view":
			if len(items) < 2 {
				return model.FrontendScreen{}, parseError(clause, "view expects one or more UI nodes")
			}
			viewStart := 1
			if items[1].Kind == sexp.KindSymbol && len(items) >= 3 {
				modelName, ok := symbolValue(items[1])
				if !ok {
					return model.FrontendScreen{}, parseError(items[1], "view model parameter must be a symbol")
				}
				screen.ViewModel = canonicalFieldName(modelName)
				viewStart = 2
			}
			viewNodes := items[viewStart:]
			if len(viewNodes) == 0 {
				return model.FrontendScreen{}, parseError(clause, "view expects one or more UI nodes")
			}
			screen.ViewBody = inlineNodes(viewNodes)
			for _, viewNode := range viewNodes {
				if section, ok, err := parseViewSection(viewNode, queries); err != nil {
					return model.FrontendScreen{}, err
				} else if ok {
					screen.Sections = append(screen.Sections, section)
					sectionNodes = append(sectionNodes, viewNode)
					continue
				}
				parsedView, err := parseViewNode(viewNode)
				if err != nil {
					return model.FrontendScreen{}, err
				}
				if screen.View != nil {
					return model.FrontendScreen{}, parseError(viewNode, "view with static nodes expects a single root node")
				}
				viewRawNodes = append(viewRawNodes, viewNode)
				screen.View = parsedView
			}
		default:
			return model.FrontendScreen{}, parseError(items[0], "unknown screen clause %q", head)
		}
	}

	if hasUpdate && (!hasMsg || !hasInit) {
		return model.FrontendScreen{}, fmt.Errorf("screen %s requires msg and init when update is present", screen.Name)
	}
	if hasInit {
		if err := validateFrontendTransitionStructure(initNode); err != nil {
			return model.FrontendScreen{}, err
		}
	}
	if hasUpdate {
		if err := validateFrontendTransitionStructure(updateNode); err != nil {
			return model.FrontendScreen{}, err
		}
	}
	if err := validateFrontendScreenUIContext(screen, hasUpdate, viewRawNodes, sectionNodes); err != nil {
		return model.FrontendScreen{}, err
	}
	if !hasView {
		return model.FrontendScreen{}, fmt.Errorf("screen %s requires view", screen.Name)
	}
	return screen, nil
}

func parseScreenDef(node sexp.Node) (*screenDef, error) {
	if len(node.Children) < 3 {
		return nil, parseError(node, "define-screen expects a name/signature and a body")
	}
	signature := node.Children[1]
	var name string
	var params []string
	if signature.Kind == sexp.KindList {
		items, err := listChildren(signature, "define-screen signature")
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			return nil, parseError(signature, "define-screen signature cannot be empty")
		}
		var ok bool
		name, ok = symbolValue(items[0])
		if !ok {
			return nil, parseError(items[0], "define-screen name must be a symbol")
		}
		for _, item := range items[1:] {
			param, ok := symbolValue(item)
			if !ok {
				return nil, parseError(item, "define-screen parameters must be symbols")
			}
			params = append(params, param)
		}
	} else {
		var ok bool
		name, ok = symbolValue(signature)
		if !ok {
			return nil, parseError(signature, "define-screen name must be a symbol")
		}
	}
	return &screenDef{Name: name, Parameters: params, Body: sexp.Node{
		Kind:     sexp.KindList,
		Children: node.Children[2:],
		Line:     node.Line,
		Column:   node.Column,
	}}, nil
}

func parseViewSection(node sexp.Node, queries map[string]*queryDef) (model.FrontendSection, bool, error) {
	head, ok := listHead(node)
	if !ok || head != "section" {
		return model.FrontendSection{}, false, nil
	}
	section, err := parseSection(node, queries)
	if err == nil {
		return section, true, nil
	}
	if strings.Contains(err.Error(), "section does not support metadata") || strings.Contains(err.Error(), "section expects one nested list of clauses") {
		return model.FrontendSection{}, false, nil
	}
	return model.FrontendSection{}, false, err
}

func inlineNodes(nodes []sexp.Node) string {
	if len(nodes) == 1 {
		return sexp.InlineString(nodes[0])
	}
	parts := make([]string, 0, len(nodes))
	for _, node := range nodes {
		parts = append(parts, sexp.InlineString(node))
	}
	return "(" + strings.Join(parts, " ") + ")"
}

func parseViewNode(node sexp.Node) (*model.FrontendViewNode, error) {
	if node.Kind != sexp.KindList || len(node.Children) == 0 {
		return nil, parseError(node, "view body must be a non-empty list")
	}
	head, ok := symbolValue(node.Children[0])
	if !ok {
		return nil, parseError(node.Children[0], "view node head must be a symbol")
	}

	switch head {
	case "section":
		view := &model.FrontendViewNode{Kind: "section"}
		for _, child := range node.Children[1:] {
			if child.Kind != sexp.KindList || len(child.Children) == 0 {
				return nil, parseError(child, "section children must be nodes or section metadata")
			}
			key, _ := symbolValue(child.Children[0])
			if key == "title" {
				if len(child.Children) != 2 || child.Children[1].Kind != sexp.KindString {
					return nil, parseError(child, "section title expects a string")
				}
				view.Title = child.Children[1].Value
				continue
			}
			parsedChild, err := parseViewNode(child)
			if err != nil {
				return nil, err
			}
			view.Children = append(view.Children, *parsedChild)
		}
		return view, nil
	case "text":
		if len(node.Children) != 2 {
			return nil, parseError(node, "text expects one argument")
		}
		switch node.Children[1].Kind {
		case sexp.KindString:
			return &model.FrontendViewNode{Kind: "text", Text: node.Children[1].Value}, nil
		case sexp.KindSymbol:
			return &model.FrontendViewNode{Kind: "text", Text: node.Children[1].Value}, nil
		default:
			return nil, parseError(node.Children[1], "text expects a string or symbol")
		}
	case "button":
		if len(node.Children) != 3 {
			return nil, parseError(node, "button expects a label and a message")
		}
		if node.Children[1].Kind != sexp.KindString {
			return nil, parseError(node.Children[1], "button label expects a string")
		}
		return &model.FrontendViewNode{
			Kind:    "button",
			Label:   node.Children[1].Value,
			Message: sexp.InlineString(node.Children[2]),
		}, nil
	default:
		return nil, parseError(node.Children[0], "unsupported view node %q", head)
	}
}

func unwrapDefinitionBody(body sexp.Node, wrapper string, label string) (sexp.Node, error) {
	if body.Kind != sexp.KindList {
		return sexp.Node{}, parseError(body, "%s must be a list", label)
	}
	if len(body.Children) == 0 {
		return body, nil
	}
	head, ok := symbolValue(body.Children[0])
	if ok && head == wrapper {
		return sexp.Node{
			Kind:     sexp.KindList,
			Children: body.Children[1:],
			Line:     body.Line,
			Column:   body.Column,
		}, nil
	}
	return body, nil
}

func parseSection(clause sexp.Node, queries map[string]*queryDef) (model.FrontendSection, error) {
	items, err := listChildren(clause, "section")
	if err != nil {
		return model.FrontendSection{}, err
	}
	section, index, err := parseSectionMetadata(items, clause)
	if err != nil {
		return model.FrontendSection{}, err
	}
	section.Items = []model.FrontendItem{}
	children, err := nestedClauseList(items, clause, index, "section")
	if err != nil {
		return model.FrontendSection{}, err
	}
	for _, child := range children {
		item, err := parseScreenItem(child, queries)
		if err != nil {
			return model.FrontendSection{}, err
		}
		section.Items = append(section.Items, item)
	}
	return section, nil
}

func parseSectionMetadata(items []sexp.Node, clause sexp.Node) (model.FrontendSection, int, error) {
	section := model.FrontendSection{}
	index := 1
	if len(items) > 1 && items[1].Kind == sexp.KindString {
		section.Title = items[1].Value
		index = 2
	}
	for index < len(items)-1 {
		return model.FrontendSection{}, 0, parseError(items[index], "section does not support metadata; use item expressions such as (if condition item (empty))")
	}
	return section, index, nil
}

func parseScreenItem(node sexp.Node, queries map[string]*queryDef) (model.FrontendItem, error) {
	items, err := listChildren(node, "screen item")
	if err != nil {
		return model.FrontendItem{}, err
	}
	head, _ := symbolValue(items[0])
	switch head {
	case "if":
		if len(items) != 4 {
			return model.FrontendItem{}, parseError(node, "if item expects (if condition item (empty))")
		}
		elseHead, ok := listHead(items[3])
		if !ok || elseHead != "empty" {
			return model.FrontendItem{}, parseError(items[3], "if item currently expects (empty) as the else branch")
		}
		item, err := parseScreenItem(items[2], queries)
		if err != nil {
			return model.FrontendItem{}, err
		}
		if item.Kind == "empty" {
			return model.FrontendItem{}, parseError(items[2], "if item then branch cannot be empty")
		}
		item.Condition = combineItemConditions(sexp.InlineString(items[1]), item.Condition)
		return item, nil
	case "empty":
		if len(items) != 1 {
			return model.FrontendItem{}, parseError(node, "empty does not accept arguments")
		}
		return model.FrontendItem{Kind: "empty"}, nil
	case "field":
		if len(items) != 2 {
			return model.FrontendItem{}, parseError(node, "field expects one symbol")
		}
		field, ok := symbolValue(items[1])
		if !ok {
			return model.FrontendItem{}, parseError(items[1], "field expects a symbol")
		}
		return model.FrontendItem{Kind: "field", Field: canonicalFieldName(field)}, nil
	case "link":
		if len(items) != 3 || items[1].Kind != sexp.KindString {
			return model.FrontendItem{}, parseError(node, "link expects (link \"Label\" destination)")
		}
		destination, ok := symbolValue(items[2])
		if !ok {
			return model.FrontendItem{}, parseError(items[2], "link destination must be a symbol")
		}
		return model.FrontendItem{Kind: "link", Label: items[1].Value, Target: canonicalScreenName(destination)}, nil
	case "button":
		if len(items) < 3 || items[1].Kind != sexp.KindString {
			return model.FrontendItem{}, parseError(node, "button expects (button \"Label\" message)")
		}
		item := model.FrontendItem{
			Kind:    "button",
			Label:   items[1].Value,
			Message: sexp.InlineString(items[2]),
		}
		if err := applyFrontendItemOptions(&item, items[3:]); err != nil {
			return model.FrontendItem{}, err
		}
		return item, nil
	case "text-input", "textarea", "toggle":
		if len(items) < 4 || items[1].Kind != sexp.KindString {
			return model.FrontendItem{}, parseError(node, "%s expects (%s \"Label\" model-field changed-message)", head, head)
		}
		modelField, ok := symbolValue(items[2])
		if !ok {
			return model.FrontendItem{}, parseError(items[2], "%s model field must be a symbol", head)
		}
		messageName, ok := symbolValue(items[3])
		if !ok {
			return model.FrontendItem{}, parseError(items[3], "%s changed message must be a symbol", head)
		}
		item := model.FrontendItem{
			Kind:       canonicalFrontendInputKind(head),
			Label:      items[1].Value,
			ModelField: canonicalFieldName(modelField),
			Message:    canonicalFieldName(messageName),
		}
		if err := applyFrontendItemOptions(&item, items[4:]); err != nil {
			return model.FrontendItem{}, err
		}
		return item, nil
	case "select":
		if len(items) < 5 || items[1].Kind != sexp.KindString {
			return model.FrontendItem{}, parseError(node, "select expects (select \"Label\" model-field changed-message ((value \"Label\") ...))")
		}
		modelField, ok := symbolValue(items[2])
		if !ok {
			return model.FrontendItem{}, parseError(items[2], "select model field must be a symbol")
		}
		messageName, ok := symbolValue(items[3])
		if !ok {
			return model.FrontendItem{}, parseError(items[3], "select changed message must be a symbol")
		}
		options, err := parseFrontendOptions(items[4])
		if err != nil {
			return model.FrontendItem{}, err
		}
		item := model.FrontendItem{
			Kind:       "select",
			Label:      items[1].Value,
			ModelField: canonicalFieldName(modelField),
			Message:    canonicalFieldName(messageName),
			Options:    options,
		}
		if err := applyFrontendItemOptions(&item, items[5:]); err != nil {
			return model.FrontendItem{}, err
		}
		return item, nil
	case "list":
		if len(items) < 4 || items[1].Kind != sexp.KindSymbol || items[2].Kind != sexp.KindSymbol {
			return model.FrontendItem{}, parseError(node, "list expects (list model-field entity ((title field) ...))")
		}
		modelField, _ := symbolValue(items[1])
		entity, _ := symbolValue(items[2])
		item := model.FrontendItem{
			Kind:       "list",
			ModelField: canonicalFieldName(modelField),
			Entity:     canonicalTypeName(entity),
		}
		clausesIndex := 3
		clauses, err := nestedClauseList(items, node, clausesIndex, "list")
		if err != nil {
			return model.FrontendItem{}, err
		}
		for _, clause := range clauses {
			parts, err := listChildren(clause, "list clause")
			if err != nil {
				return model.FrontendItem{}, err
			}
			if len(parts) != 2 {
				return model.FrontendItem{}, parseError(clause, "list clause must be a pair")
			}
			key, _ := symbolValue(parts[0])
			value, ok := symbolValue(parts[1])
			if !ok {
				return model.FrontendItem{}, parseError(parts[1], "list clause value must be a symbol")
			}
			switch key {
			case "title":
				item.TitleField = canonicalFieldName(value)
			case "subtitle":
				item.SubtitleField = canonicalFieldName(value)
			case "open", "destination":
				item.Destination = canonicalScreenName(value)
			case "action":
				item.Action = canonicalFunctionName(value)
			default:
				return model.FrontendItem{}, parseError(parts[0], "unknown list clause %q", key)
			}
		}
		return item, nil
	case "create", "edit":
		if len(items) < 2 {
			return model.FrontendItem{}, parseError(node, "%s expects an entity", head)
		}
		entity, ok := symbolValue(items[1])
		if !ok {
			return model.FrontendItem{}, parseError(items[1], "%s entity must be a symbol", head)
		}
		item := model.FrontendItem{Kind: head, Entity: canonicalTypeName(entity)}
		clauses, err := nestedClauseList(items, node, 2, head)
		if err != nil {
			return model.FrontendItem{}, err
		}
		for _, clause := range clauses {
			parts, err := listChildren(clause, head+" clause")
			if err != nil {
				return model.FrontendItem{}, err
			}
			key, _ := symbolValue(parts[0])
			switch key {
			case "field":
				if len(parts) != 2 {
					return model.FrontendItem{}, parseError(clause, "%s field clause must be (field name)", head)
				}
				field, _ := symbolValue(parts[1])
				item.FormFields = append(item.FormFields, model.FrontendFormField{Field: canonicalFieldName(field)})
			case "value":
				if len(parts) != 3 {
					return model.FrontendItem{}, parseError(clause, "%s value clause must be (value field expr)", head)
				}
				field, ok := symbolValue(parts[1])
				if !ok {
					return model.FrontendItem{}, parseError(parts[1], "%s value field must be a symbol", head)
				}
				item.Values = append(item.Values, model.FrontendActionValue{
					Field:      canonicalFieldName(field),
					Expression: sexp.InlineString(parts[2]),
					LineNo:     clause.Line,
				})
			default:
				return model.FrontendItem{}, parseError(parts[0], "%s clause must be (field name) or (value field expr)", head)
			}
		}
		return item, nil
	case "delete":
		if len(items) != 2 {
			return model.FrontendItem{}, parseError(node, "delete expects an entity")
		}
		entity, ok := symbolValue(items[1])
		if !ok {
			return model.FrontendItem{}, parseError(items[1], "delete entity must be a symbol")
		}
		return model.FrontendItem{Kind: "delete", Entity: canonicalTypeName(entity)}, nil
	default:
		return model.FrontendItem{}, parseError(items[0], "unknown screen item %q", head)
	}
}

func listHead(node sexp.Node) (string, bool) {
	if node.Kind != sexp.KindList || len(node.Children) == 0 {
		return "", false
	}
	return symbolValue(node.Children[0])
}

func combineItemConditions(left string, right string) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	return "(and " + left + " " + right + ")"
}

func canonicalFrontendInputKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case "text-input":
		return "textInput"
	case "textarea":
		return "textarea"
	case "toggle":
		return "toggle"
	default:
		return canonicalFieldName(kind)
	}
}

func applyFrontendItemOptions(item *model.FrontendItem, options []sexp.Node) error {
	for _, option := range options {
		parts, err := listChildren(option, "screen item option")
		if err != nil {
			return err
		}
		if len(parts) != 2 {
			return parseError(option, "screen item option must be a pair")
		}
		key, ok := symbolValue(parts[0])
		if !ok {
			return parseError(parts[0], "screen item option name must be a symbol")
		}
		switch key {
		case "disabled":
			name, ok := symbolValue(parts[1])
			if !ok {
				return parseError(parts[1], "disabled expects a symbol")
			}
			item.Disabled = canonicalFieldName(name)
		default:
			return parseError(parts[0], "unknown screen item option %q", key)
		}
	}
	return nil
}

func parseFrontendOptions(node sexp.Node) ([]model.FrontendOption, error) {
	optionNodes, err := listChildren(node, "select options")
	if err != nil {
		return nil, err
	}
	if len(optionNodes) == 0 {
		return nil, parseError(node, "select requires at least one option")
	}
	options := make([]model.FrontendOption, 0, len(optionNodes))
	for _, optionNode := range optionNodes {
		parts, err := listChildren(optionNode, "select option")
		if err != nil {
			return nil, err
		}
		if len(parts) != 2 {
			return nil, parseError(optionNode, "select option must look like (value \"Label\")")
		}
		var value string
		switch parts[0].Kind {
		case sexp.KindSymbol:
			value = canonicalFieldName(parts[0].Value)
		case sexp.KindString:
			value = parts[0].Value
		default:
			return nil, parseError(parts[0], "select option value must be a symbol or string")
		}
		if parts[1].Kind != sexp.KindString {
			return nil, parseError(parts[1], "select option label must be a string")
		}
		options = append(options, model.FrontendOption{
			Value: value,
			Label: parts[1].Value,
		})
	}
	return options, nil
}

func parseCallableDef(node sexp.Node) (*queryDef, *functionDef, *screenDef, error) {
	if len(node.Children) != 3 {
		return nil, nil, nil, parseError(node, "define expects a signature and a body")
	}
	signature := node.Children[1]
	items, err := listChildren(signature, "define signature")
	if err != nil {
		return nil, nil, nil, err
	}
	if len(items) == 0 {
		return nil, nil, nil, parseError(signature, "define signature cannot be empty")
	}
	name, _ := symbolValue(items[0])
	for _, item := range items[1:] {
		if _, ok := symbolValue(item); !ok {
			return nil, nil, nil, parseError(item, "define parameters must be symbols")
		}
	}
	bodyItems, err := listChildren(node.Children[2], "define body")
	if err != nil {
		return nil, &functionDef{
			Name:       name,
			Parameters: symbolValues(items[1:]),
			Body:       node.Children[2],
			LineNo:     node.Line,
		}, nil, nil
	}
	if len(bodyItems) == 0 {
		return nil, &functionDef{
			Name:       name,
			Parameters: symbolValues(items[1:]),
			Body:       node.Children[2],
			LineNo:     node.Line,
		}, nil, nil
	}
	head, _ := symbolValue(bodyItems[0])
	if head != "query" {
		if head == "screen" {
			return nil, nil, &screenDef{
				Name:       name,
				Parameters: symbolValues(items[1:]),
				Body:       node.Children[2],
			}, nil
		}
		return nil, &functionDef{
			Name:       name,
			Parameters: symbolValues(items[1:]),
			Body:       node.Children[2],
			LineNo:     node.Line,
		}, nil, nil
	}
	if len(bodyItems) < 2 {
		return nil, nil, nil, parseError(node.Children[2], "query expects an entity symbol")
	}
	entitySymbol, _ := symbolValue(bodyItems[1])
	query := &queryDef{Name: name, Parameters: symbolValues(items[1:]), EntitySymbol: entitySymbol, OrderDir: "desc"}
	for _, clause := range bodyItems[2:] {
		parts, err := listChildren(clause, "query clause")
		if err != nil {
			return nil, nil, nil, err
		}
		head, _ := symbolValue(parts[0])
		switch head {
		case "where":
			if len(parts) != 2 {
				return nil, nil, nil, parseError(clause, "where expects one expression")
			}
			query.Where = parts[1]
		case "order-by":
			if len(parts) < 2 || len(parts) > 3 {
				return nil, nil, nil, parseError(clause, "order-by expects (order-by field [asc|desc])")
			}
			query.OrderBy, _ = symbolValue(parts[1])
			if len(parts) == 3 {
				query.OrderDir, _ = symbolValue(parts[2])
			}
		case "limit":
			if len(parts) != 2 {
				return nil, nil, nil, parseError(clause, "limit expects one integer")
			}
			limit, err := intLiteral(parts[1])
			if err != nil {
				return nil, nil, nil, parseError(parts[1], "limit must be an integer")
			}
			query.Limit = &limit
		default:
			return nil, nil, nil, parseError(parts[0], "unknown query clause %q", head)
		}
	}
	return query, nil, nil, nil
}

func parseActionDefinition(name string, bodyNode sexp.Node, entities map[string]*model.Entity) (model.TypeAlias, model.Action, error) {
	body, err := unwrapDefinitionBody(bodyNode, "action", "action "+name)
	if err != nil {
		return model.TypeAlias{}, model.Action{}, err
	}

	inputAlias := model.TypeAlias{Name: canonicalTypeName(name) + "Input"}
	action := model.Action{Name: canonicalFunctionName(name), InputAlias: inputAlias.Name}

	for _, clause := range body.Children {
		items, err := listChildren(clause, "action clause")
		if err != nil {
			return model.TypeAlias{}, model.Action{}, err
		}
		head, _ := symbolValue(items[0])
		switch head {
		case "input":
			if len(items) != 2 {
				return model.TypeAlias{}, model.Action{}, parseError(clause, "input expects one field list")
			}
			fieldClauses, err := listChildren(items[1], "action input fields")
			if err != nil {
				return model.TypeAlias{}, model.Action{}, err
			}
			for _, fieldClause := range fieldClauses {
				parts, err := listChildren(fieldClause, "action input field")
				if err != nil {
					return model.TypeAlias{}, model.Action{}, err
				}
				if len(parts) != 2 {
					return model.TypeAlias{}, model.Action{}, parseError(fieldClause, "action input field must look like (name type)")
				}
				fieldName, ok := symbolValue(parts[0])
				if !ok {
					return model.TypeAlias{}, model.Action{}, parseError(parts[0], "action input field name must be a symbol")
				}
				typeName, ok := symbolValue(parts[1])
				if !ok {
					return model.TypeAlias{}, model.Action{}, parseError(parts[1], "action input field type must be a symbol")
				}
				fieldType, err := mapPrimitiveType(typeName)
				if err != nil {
					return model.TypeAlias{}, model.Action{}, fmt.Errorf("action %s input %s: %w", name, fieldName, err)
				}
				inputAlias.Fields = append(inputAlias.Fields, model.AliasField{
					Name: canonicalFieldName(fieldName),
					Type: fieldType,
				})
			}
		case "load":
			step, err := parseActionLoadStep(name, items, entities)
			if err != nil {
				return model.TypeAlias{}, model.Action{}, err
			}
			action.Steps = append(action.Steps, step)
		case "create":
			step, err := parseActionCreateStep(name, items, entities)
			if err != nil {
				return model.TypeAlias{}, model.Action{}, err
			}
			action.Steps = append(action.Steps, step)
		case "update":
			step, err := parseActionUpdateStep(name, items, entities)
			if err != nil {
				return model.TypeAlias{}, model.Action{}, err
			}
			action.Steps = append(action.Steps, step)
		case "delete":
			step, err := parseActionDeleteStep(name, items, entities)
			if err != nil {
				return model.TypeAlias{}, model.Action{}, err
			}
			action.Steps = append(action.Steps, step)
		default:
			return model.TypeAlias{}, model.Action{}, parseError(items[0], "unknown action clause %q", head)
		}
	}

	if len(inputAlias.Fields) == 0 {
		return model.TypeAlias{}, model.Action{}, fmt.Errorf("action %s requires input", name)
	}
	if len(action.Steps) == 0 {
		return model.TypeAlias{}, model.Action{}, fmt.Errorf("action %s requires at least one step", name)
	}

	return inputAlias, action, nil
}

func parseActionLoadStep(actionName string, items []sexp.Node, entities map[string]*model.Entity) (model.ActionStep, error) {
	if len(items) != 3 {
		return model.ActionStep{}, parseError(items[0], "load expects (load entity id-expr)")
	}
	entityName, entity, err := actionStepEntity(items[1], entities)
	if err != nil {
		return model.ActionStep{}, fmt.Errorf("action %s: %w", actionName, err)
	}
	return model.ActionStep{
		Kind:   "load",
		Entity: entityName,
		Values: []model.ActionFieldExpr{{
			Field:      entity.PrimaryKey,
			Expression: sexp.InlineString(items[2]),
		}},
	}, nil
}

func parseActionCreateStep(actionName string, items []sexp.Node, entities map[string]*model.Entity) (model.ActionStep, error) {
	if len(items) != 3 {
		return model.ActionStep{}, parseError(items[0], "create expects (create entity ((field expr) ...))")
	}
	entityName, _, err := actionStepEntity(items[1], entities)
	if err != nil {
		return model.ActionStep{}, fmt.Errorf("action %s: %w", actionName, err)
	}
	values, err := parseActionStepValues(items[2])
	if err != nil {
		return model.ActionStep{}, fmt.Errorf("action %s: %w", actionName, err)
	}
	return model.ActionStep{Kind: "create", Entity: entityName, Values: values}, nil
}

func parseActionUpdateStep(actionName string, items []sexp.Node, entities map[string]*model.Entity) (model.ActionStep, error) {
	if len(items) != 4 {
		return model.ActionStep{}, parseError(items[0], "update expects (update entity id-expr ((field expr) ...))")
	}
	entityName, entity, err := actionStepEntity(items[1], entities)
	if err != nil {
		return model.ActionStep{}, fmt.Errorf("action %s: %w", actionName, err)
	}
	values, err := parseActionStepValues(items[3])
	if err != nil {
		return model.ActionStep{}, fmt.Errorf("action %s: %w", actionName, err)
	}
	values = append([]model.ActionFieldExpr{{
		Field:      entity.PrimaryKey,
		Expression: sexp.InlineString(items[2]),
	}}, values...)
	return model.ActionStep{Kind: "update", Entity: entityName, Values: values}, nil
}

func parseActionDeleteStep(actionName string, items []sexp.Node, entities map[string]*model.Entity) (model.ActionStep, error) {
	if len(items) != 3 {
		return model.ActionStep{}, parseError(items[0], "delete expects (delete entity id-expr)")
	}
	entityName, entity, err := actionStepEntity(items[1], entities)
	if err != nil {
		return model.ActionStep{}, fmt.Errorf("action %s: %w", actionName, err)
	}
	return model.ActionStep{
		Kind:   "delete",
		Entity: entityName,
		Values: []model.ActionFieldExpr{{
			Field:      entity.PrimaryKey,
			Expression: sexp.InlineString(items[2]),
		}},
	}, nil
}

func parseActionStepValues(node sexp.Node) ([]model.ActionFieldExpr, error) {
	fieldClauses, err := listChildren(node, "action step values")
	if err != nil {
		return nil, err
	}
	values := make([]model.ActionFieldExpr, 0, len(fieldClauses))
	for _, fieldClause := range fieldClauses {
		parts, err := listChildren(fieldClause, "action step value")
		if err != nil {
			return nil, err
		}
		if len(parts) != 2 {
			return nil, parseError(fieldClause, "action step values must look like (field expr)")
		}
		fieldName, ok := symbolValue(parts[0])
		if !ok {
			return nil, parseError(parts[0], "action step field name must be a symbol")
		}
		values = append(values, model.ActionFieldExpr{
			Field:      canonicalFieldName(fieldName),
			Expression: sexp.InlineString(parts[1]),
		})
	}
	return values, nil
}

func actionStepEntity(node sexp.Node, entities map[string]*model.Entity) (string, *model.Entity, error) {
	entitySymbol, ok := symbolValue(node)
	if !ok {
		return "", nil, parseError(node, "action entity must be a symbol")
	}
	entityName := canonicalTypeName(entitySymbol)
	entity := entities[entityName]
	if entity == nil {
		return "", nil, fmt.Errorf("unknown entity %q", entitySymbol)
	}
	return entityName, entity, nil
}

func parseRecordDef(node sexp.Node) (*recordDef, error) {
	if len(node.Children) < 2 {
		return nil, parseError(node, "define-record expects a name")
	}
	name, ok := symbolValue(node.Children[1])
	if !ok {
		return nil, parseError(node.Children[1], "define-record name must be a symbol")
	}
	record := &recordDef{Name: name}
	for _, fieldNode := range node.Children[2:] {
		parts, err := listChildren(fieldNode, "record field")
		if err != nil {
			return nil, err
		}
		if len(parts) != 2 {
			return nil, parseError(fieldNode, "record fields must look like (name type)")
		}
		fieldName, ok := symbolValue(parts[0])
		if !ok {
			return nil, parseError(parts[0], "record field name must be a symbol")
		}
		fieldType, err := parseRecordType(parts[1])
		if err != nil {
			return nil, fmt.Errorf("record %s field %s: %w", name, fieldName, err)
		}
		record.Fields = append(record.Fields, model.RecordField{
			Name: canonicalFieldName(fieldName),
			Type: fieldType,
		})
	}
	return record, nil
}

func parseTypeDef(node sexp.Node) (*typeDef, error) {
	if len(node.Children) < 3 {
		return nil, parseError(node, "define-type expects a name and at least one variant")
	}
	name, ok := symbolValue(node.Children[1])
	if !ok {
		return nil, parseError(node.Children[1], "define-type name must be a symbol")
	}
	out := &typeDef{Name: name}
	seen := map[string]bool{}
	for _, variantNode := range node.Children[2:] {
		parts, err := listChildren(variantNode, "type variant")
		if err != nil {
			return nil, err
		}
		if len(parts) == 0 {
			return nil, parseError(variantNode, "type variant cannot be empty")
		}
		variantName, ok := symbolValue(parts[0])
		if !ok {
			return nil, parseError(parts[0], "type variant name must be a symbol")
		}
		canonicalVariant := canonicalFieldName(variantName)
		if seen[canonicalVariant] {
			return nil, parseError(parts[0], "type variant %q is declared more than once", variantName)
		}
		seen[canonicalVariant] = true
		variant := model.TypeVariant{Name: canonicalVariant}
		for _, fieldNode := range parts[1:] {
			fieldParts, err := listChildren(fieldNode, "type variant field")
			if err != nil {
				return nil, err
			}
			if len(fieldParts) != 2 {
				return nil, parseError(fieldNode, "type variant fields must look like (name type)")
			}
			fieldName, ok := symbolValue(fieldParts[0])
			if !ok {
				return nil, parseError(fieldParts[0], "type variant field name must be a symbol")
			}
			fieldType, err := parseRecordType(fieldParts[1])
			if err != nil {
				return nil, fmt.Errorf("type %s variant %s field %s: %w", name, variantName, fieldName, err)
			}
			variant.Fields = append(variant.Fields, model.RecordField{
				Name: canonicalFieldName(fieldName),
				Type: fieldType,
			})
		}
		out.Variants = append(out.Variants, variant)
	}
	return out, nil
}

func parseAppDef(node sexp.Node) (*appDef, error) {
	if len(node.Children) < 2 {
		return nil, parseError(node, "define-app expects a name")
	}
	name, ok := symbolValue(node.Children[1])
	if !ok {
		return nil, parseError(node.Children[1], "define-app name must be a symbol")
	}
	app := &appDef{Name: name}
	for _, clause := range node.Children[2:] {
		parts, err := listChildren(clause, "define-app clause")
		if err != nil {
			return nil, err
		}
		head, _ := symbolValue(parts[0])
		switch head {
		case "config":
			if len(parts) != 2 {
				return nil, parseError(clause, "config expects one symbol")
			}
			app.ConfigRef, _ = symbolValue(parts[1])
		case "auth":
			if len(parts) != 2 {
				return nil, parseError(clause, "auth expects one symbol")
			}
			app.AuthRef, _ = symbolValue(parts[1])
		case "entities":
			for _, part := range parts[1:] {
				symbol, ok := symbolValue(part)
				if !ok {
					return nil, parseError(part, "entities expects symbols")
				}
				app.EntitySymbols = append(app.EntitySymbols, symbol)
			}
		case "queries":
			for _, part := range parts[1:] {
				symbol, ok := symbolValue(part)
				if !ok {
					return nil, parseError(part, "queries expects symbols")
				}
				app.QuerySymbols = append(app.QuerySymbols, symbol)
			}
		case "actions":
			for _, part := range parts[1:] {
				symbol, ok := symbolValue(part)
				if !ok {
					return nil, parseError(part, "actions expects symbols")
				}
				app.ActionSymbols = append(app.ActionSymbols, symbol)
			}
		case "screens":
			for _, part := range parts[1:] {
				symbol, ok := symbolValue(part)
				if !ok {
					return nil, parseError(part, "screens expects symbols")
				}
				app.ScreenSymbols = append(app.ScreenSymbols, symbol)
			}
		case "backend":
			if err := parseAppSection(parts[1:], "backend", app); err != nil {
				return nil, err
			}
		case "frontend":
			if err := parseFrontendSection(parts[1:], app); err != nil {
				return nil, err
			}
		default:
			return nil, parseError(parts[0], "unknown define-app clause %q", head)
		}
	}
	return app, nil
}

func parseAppSection(clauses []sexp.Node, label string, app *appDef) error {
	for _, clause := range clauses {
		parts, err := listChildren(clause, label+" clause")
		if err != nil {
			return err
		}
		head, _ := symbolValue(parts[0])
		switch head {
		case "entities":
			for _, part := range parts[1:] {
				symbol, ok := symbolValue(part)
				if !ok {
					return parseError(part, "entities expects symbols")
				}
				app.EntitySymbols = append(app.EntitySymbols, symbol)
			}
		case "queries":
			for _, part := range parts[1:] {
				symbol, ok := symbolValue(part)
				if !ok {
					return parseError(part, "queries expects symbols")
				}
				app.QuerySymbols = append(app.QuerySymbols, symbol)
			}
		case "actions":
			for _, part := range parts[1:] {
				symbol, ok := symbolValue(part)
				if !ok {
					return parseError(part, "actions expects symbols")
				}
				app.ActionSymbols = append(app.ActionSymbols, symbol)
			}
		default:
			return parseError(parts[0], "unknown %s clause %q", label, head)
		}
	}
	return nil
}

func parseFrontendSection(clauses []sexp.Node, app *appDef) error {
	for _, clause := range clauses {
		parts, err := listChildren(clause, "frontend clause")
		if err != nil {
			return err
		}
		head, _ := symbolValue(parts[0])
		switch head {
		case "screens":
			for _, part := range parts[1:] {
				symbol, ok := symbolValue(part)
				if !ok {
					return parseError(part, "screens expects symbols")
				}
				app.ScreenSymbols = append(app.ScreenSymbols, symbol)
			}
		default:
			return parseError(parts[0], "unknown frontend clause %q", head)
		}
	}
	return nil
}

func validateFrontendScreens(frontend *model.Frontend, queries []model.Query, actions []model.Action, aliases map[string]*model.TypeAlias, functions map[string]*model.Function, records map[string]*model.Record, types map[string]*model.EnumType, entities map[string]*model.Entity) error {
	allowedCommands := allowedCommandArities(queries, actions, aliases)
	commandKinds := allowedCommandKinds(queries, actions)
	allowedFunctions := allowedFunctionArities(functions)
	allowedRecords := allowedRecordFields(records)
	allowedVariants := allowedTypeVariants(types)
	screenParamTypes, err := inferScreenParameterTypes(frontend, actions, aliases, functions, records, types, entities)
	if err != nil {
		return err
	}
	baseTypeChecker := newFrontendTypeChecker(functions, records, types, entities, screenParamTypes, nil)
	screenMessageTypes, err := inferScreenMessagePayloadTypes(frontend, queries, actions, aliases, baseTypeChecker)
	if err != nil {
		return err
	}
	typeChecker := newFrontendTypeChecker(functions, records, types, entities, screenParamTypes, screenMessageTypes)
	screenByName := map[string]model.FrontendScreen{}
	for _, screen := range frontend.Screens {
		screenByName[screen.Name] = screen
	}
	actionNames := map[string]struct{}{}
	for _, action := range actions {
		actionNames[action.Name] = struct{}{}
	}

	for _, screen := range frontend.Screens {
		messageArities := frontendMessageArities(screen.Messages)
		modelType := anyFrontendType()
		if screen.InitExpression != "" {
			node, err := sexp.ParseOne(screen.InitExpression)
			if err != nil {
				return fmt.Errorf("screen %s init: %w", screen.Name, err)
			}
			if err := validateFrontendCommandForms(node, allowedCommands, commandKinds, messageArities); err != nil {
				return fmt.Errorf("screen %s init: %w", screen.Name, err)
			}
			if _, err := expr.Parse(screen.InitExpression, expr.ParserOptions{
				AllowedVariables: expr.AllowedVariablesWithBuiltins(frontendCompileTimeVariables(screen, node, false)),
				AllowedFunctions: allowedFunctions,
				AllowedCommands:  allowedCommands,
				AllowedRecords:   allowedRecords,
				AllowedVariants:  allowedVariants,
			}); err != nil {
				return fmt.Errorf("screen %s init: %w", screen.Name, err)
			}
			modelType, err = typeChecker.inferInitModelType(screen)
			if err != nil {
				return fmt.Errorf("screen %s init: %w", screen.Name, err)
			}
		}
		uiMessageTypes, err := inferScreenUIMessagePayloadTypes(screen, modelType, typeChecker, messageArities)
		if err != nil {
			return fmt.Errorf("screen %s: %w", screen.Name, err)
		}
		screenOut := screenMessageTypes[screen.Name]
		if screenOut == nil {
			screenOut = map[string][]frontendType{}
			screenMessageTypes[screen.Name] = screenOut
		}
		for messageName, payloads := range uiMessageTypes {
			for index, payload := range payloads {
				if payload.Kind == "" {
					continue
				}
				if err := mergeScreenMessagePayloadType(screenOut, messageName, index, payload); err != nil {
					return fmt.Errorf("screen %s: message %s has incompatible payloads: %w", screen.Name, messageName, err)
				}
			}
		}

		if screen.UpdateBody != "" {
			node, err := sexp.ParseOne(screen.UpdateBody)
			if err != nil {
				return fmt.Errorf("screen %s update: %w", screen.Name, err)
			}
			if err := validateFrontendCommandForms(node, allowedCommands, commandKinds, messageArities); err != nil {
				return fmt.Errorf("screen %s update: %w", screen.Name, err)
			}
			if _, err := expr.Parse(screen.UpdateBody, expr.ParserOptions{
				AllowedVariables: expr.AllowedVariablesWithBuiltins(frontendCompileTimeVariables(screen, node, true)),
				AllowedFunctions: allowedFunctions,
				AllowedCommands:  allowedCommands,
				AllowedRecords:   allowedRecords,
				AllowedVariants:  allowedVariants,
			}); err != nil {
				return fmt.Errorf("screen %s update: %w", screen.Name, err)
			}
			if err := typeChecker.validateUpdate(screen, modelType); err != nil {
				return fmt.Errorf("screen %s update: %w", screen.Name, err)
			}
		}
		if err := validateScreenMessagePayloadTypesResolved(screen, screenOut); err != nil {
			return fmt.Errorf("screen %s: %w", screen.Name, err)
		}
		if err := validateFrontendScreenNavigation(screen, screenByName, typeChecker, modelType); err != nil {
			return fmt.Errorf("screen %s: %w", screen.Name, err)
		}
		if err := validateFrontendScreenItems(screen, screenByName, messageArities, allowedFunctions, allowedRecords, typeChecker, modelType, actionNames); err != nil {
			return fmt.Errorf("screen %s: %w", screen.Name, err)
		}
	}

	return nil
}

func validateEntitySchema(entity *model.Entity, entities map[string]*model.Entity) error {
	for _, field := range entity.Fields {
		if field.RelationEntity == "" {
			continue
		}
		if entities[field.RelationEntity] == nil {
			return fmt.Errorf("entity %s field %s references unknown entity %s", entity.Name, field.Name, field.RelationEntity)
		}
	}
	for _, constraint := range entity.Unique {
		if len(constraint.Fields) == 0 {
			return fmt.Errorf("entity %s has an empty unique constraint", entity.Name)
		}
		for _, fieldName := range constraint.Fields {
			if findEntityField(entity, fieldName) == nil {
				return fmt.Errorf("entity %s unique references unknown field %s", entity.Name, fieldName)
			}
		}
	}
	return nil
}

func validateRecordTypes(record *model.Record, records map[string]*model.Record, types map[string]*model.EnumType, entities map[string]*model.Entity) error {
	for _, field := range record.Fields {
		if err := validateRecordTypeExpr(field.Type, records, types, entities); err != nil {
			return fmt.Errorf("record %s field %s: %w", record.Name, field.Name, err)
		}
	}
	return nil
}

func validateSumType(sumType *model.EnumType, records map[string]*model.Record, types map[string]*model.EnumType, entities map[string]*model.Entity) error {
	if len(sumType.Variants) == 0 {
		return fmt.Errorf("type %s must have at least one variant", sumType.Name)
	}
	for _, variant := range sumType.Variants {
		for _, field := range variant.Fields {
			if err := validateRecordTypeExpr(field.Type, records, types, entities); err != nil {
				return fmt.Errorf("type %s variant %s field %s: %w", sumType.Name, variant.Name, field.Name, err)
			}
		}
	}
	return nil
}

func validateRecordTypeExpr(typeExpr string, records map[string]*model.Record, types map[string]*model.EnumType, entities map[string]*model.Entity) error {
	switch typeExpr {
	case "string", "bool", "int", "decimal", "date", "datetime", "cursor", "(unit)":
		return nil
	}
	if strings.HasPrefix(typeExpr, "(maybe ") || strings.HasPrefix(typeExpr, "(list ") || strings.HasPrefix(typeExpr, "(result ") {
		node, err := sexp.ParseOne(typeExpr)
		if err != nil {
			return err
		}
		if len(node.Children) == 0 || node.Children[0].Kind != sexp.KindSymbol {
			return fmt.Errorf("invalid composite type")
		}
		switch node.Children[0].Value {
		case "maybe", "list":
			if len(node.Children) != 2 {
				return fmt.Errorf("invalid composite type")
			}
			return validateRecordTypeExpr(sexp.InlineString(node.Children[1]), records, types, entities)
		case "result":
			if len(node.Children) != 3 {
				return fmt.Errorf("invalid composite type")
			}
			if err := validateRecordTypeExpr(sexp.InlineString(node.Children[1]), records, types, entities); err != nil {
				return err
			}
			return validateRecordTypeExpr(sexp.InlineString(node.Children[2]), records, types, entities)
		default:
			return fmt.Errorf("invalid composite type")
		}
	}
	if records[typeExpr] == nil && types[typeExpr] == nil && entities[canonicalTypeName(typeExpr)] == nil {
		return fmt.Errorf("unknown type %q", typeExpr)
	}
	return nil
}

func queryAllowedVariables(entity *model.Entity) map[string]struct{} {
	out := map[string]struct{}{}
	for _, field := range entity.Fields {
		out[field.Name] = struct{}{}
	}
	return out
}

func findEntityField(entity *model.Entity, name string) *model.Field {
	for i := range entity.Fields {
		if entity.Fields[i].Name == name {
			return &entity.Fields[i]
		}
	}
	return nil
}

func buildBuiltinUser() model.Entity {
	return model.Entity{
		Name:       "User",
		Table:      "users",
		Resource:   "/users",
		PrimaryKey: "id",
		Fields: []model.Field{
			{Name: "id", Type: "Int", Primary: true, Auto: true},
			{Name: "email", Type: "String"},
			{Name: "role", Type: "String"},
			{Name: "created_at", Type: "DateTime", Auto: true},
			{Name: "updated_at", Type: "DateTime", Auto: true},
		},
	}
}

func mergeUserEntity(base, extension model.Entity) model.Entity {
	for _, field := range extension.Fields {
		if field.Name == "id" || field.Name == "created_at" || field.Name == "updated_at" {
			continue
		}
		replaced := false
		for i := range base.Fields {
			if base.Fields[i].Name == field.Name {
				base.Fields[i] = field
				replaced = true
				break
			}
		}
		if !replaced {
			base.Fields = append(base.Fields[:len(base.Fields)-2], append([]model.Field{field}, base.Fields[len(base.Fields)-2:]...)...)
		}
	}
	base.Unique = extension.Unique
	base.Validate = extension.Validate
	base.Authorizations = extension.Authorizations
	return base
}

func parameterVariables(params []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, param := range params {
		out[param] = struct{}{}
	}
	return out
}

func frontendMessageArities(messages []model.FrontendMessage) map[string]int {
	out := map[string]int{}
	for _, message := range messages {
		out[message.Name] = len(message.Parameters)
	}
	return out
}

func allowedCommandArities(queries []model.Query, actions []model.Action, aliases map[string]*model.TypeAlias) map[string]int {
	out := map[string]int{}
	for _, query := range queries {
		out[query.Name] = len(query.Parameters)
	}
	for _, action := range actions {
		alias := aliases[action.InputAlias]
		if alias == nil {
			continue
		}
		out[action.Name] = len(alias.Fields)
	}
	return out
}

func allowedCommandKinds(queries []model.Query, actions []model.Action) map[string]string {
	out := map[string]string{}
	for _, query := range queries {
		out[query.Name] = "query"
	}
	for _, action := range actions {
		out[action.Name] = "action"
	}
	return out
}

func inferScreenMessagePayloadTypes(frontend *model.Frontend, queries []model.Query, actions []model.Action, aliases map[string]*model.TypeAlias, typeChecker *frontendTypeChecker) (map[string]map[string][]frontendType, error) {
	if frontend == nil {
		return map[string]map[string][]frontendType{}, nil
	}
	commandReturnTypes, err := frontendCommandReturnTypes(queries, actions, typeChecker)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string][]frontendType{}
	for _, screen := range frontend.Screens {
		screenOut := map[string][]frontendType{}
		messageArities := frontendMessageArities(screen.Messages)
		for _, raw := range []string{screen.InitExpression, screen.UpdateBody} {
			if strings.TrimSpace(raw) == "" {
				continue
			}
			node, err := sexp.ParseOne(raw)
			if err != nil {
				return nil, err
			}
			if err := collectScreenCommandMessagePayloadTypes(node, screenOut, messageArities, commandReturnTypes); err != nil {
				return nil, fmt.Errorf("screen %s: %w", screen.Name, err)
			}
		}
		out[screen.Name] = screenOut
	}
	return out, nil
}

func frontendCommandReturnTypes(queries []model.Query, actions []model.Action, typeChecker *frontendTypeChecker) (map[string]frontendType, error) {
	out := map[string]frontendType{}
	for _, query := range queries {
		entityType, ok, err := typeChecker.namedType(query.Entity)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("query %s references unknown entity %s", query.Name, query.Entity)
		}
		out[query.Name] = listFrontendType(entityType)
	}
	for _, action := range actions {
		out[action.Name] = frontendType{}
	}
	return out, nil
}

func collectScreenCommandMessagePayloadTypes(node sexp.Node, screenOut map[string][]frontendType, messageArities map[string]int, commandReturnTypes map[string]frontendType) error {
	if node.Kind != sexp.KindList {
		return nil
	}
	if len(node.Children) > 0 && node.Children[0].Kind == sexp.KindSymbol && node.Children[0].Value == "command" {
		if len(node.Children) == 4 && node.Children[1].Kind == sexp.KindList && len(node.Children[1].Children) > 0 && node.Children[1].Children[0].Kind == sexp.KindSymbol {
			commandName := canonicalFunctionName(node.Children[1].Children[0].Value)
			successName := canonicalFieldName(node.Children[2].Value)
			failureName := canonicalFieldName(node.Children[3].Value)
			if messageArities[successName] == 1 {
				successType := commandReturnTypes[commandName]
				if successType.Kind != "" {
					if err := mergeScreenMessagePayloadType(screenOut, successName, 0, successType); err != nil {
						return fmt.Errorf("message %s has incompatible command payloads: %w", successName, err)
					}
				}
			}
			if messageArities[failureName] == 1 {
				if err := mergeScreenMessagePayloadType(screenOut, failureName, 0, stringFrontendType()); err != nil {
					return fmt.Errorf("message %s has incompatible failure payloads: %w", failureName, err)
				}
			}
		}
	}
	for _, child := range node.Children {
		if err := collectScreenCommandMessagePayloadTypes(child, screenOut, messageArities, commandReturnTypes); err != nil {
			return err
		}
	}
	return nil
}

func mergeScreenMessagePayloadType(screenOut map[string][]frontendType, messageName string, index int, next frontendType) error {
	current := screenOut[messageName]
	if index >= len(current) {
		expanded := make([]frontendType, index+1)
		copy(expanded, current)
		current = expanded
	}
	if current[index].Kind == "" {
		current[index] = next
		screenOut[messageName] = current
		return nil
	}
	merged, err := mergeCompatibleFrontendTypes(current[index], next)
	if err != nil {
		return err
	}
	current[index] = merged
	screenOut[messageName] = current
	return nil
}

func inferScreenUIMessagePayloadTypes(screen model.FrontendScreen, modelType frontendType, typeChecker *frontendTypeChecker, messageArities map[string]int) (map[string][]frontendType, error) {
	out := map[string][]frontendType{}
	env := typeChecker.baseEnv(screen)
	if screen.ViewModel != "" {
		env[screen.ViewModel] = modelType
	}
	for _, section := range screen.Sections {
		for _, item := range section.Items {
			if err := inferScreenItemMessagePayloadTypes(item, modelType, env, typeChecker, messageArities, out); err != nil {
				return nil, err
			}
		}
	}
	if screen.View != nil {
		if err := inferFrontendViewNodeMessagePayloadTypes(*screen.View, env, typeChecker, messageArities, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func inferScreenItemMessagePayloadTypes(item model.FrontendItem, modelType frontendType, env frontendTypeEnv, typeChecker *frontendTypeChecker, messageArities map[string]int, out map[string][]frontendType) error {
	switch item.Kind {
	case "textInput", "textarea", "toggle", "select":
		arity, ok := messageArities[item.Message]
		if !ok || arity != 1 {
			return nil
		}
		fieldType, err := frontendModelFieldType(modelType, item.ModelField)
		if err != nil {
			return err
		}
		if err := mergeScreenMessagePayloadType(out, item.Message, 0, fieldType); err != nil {
			return fmt.Errorf("message %s has incompatible UI payloads: %w", item.Message, err)
		}
	case "button":
		if strings.TrimSpace(item.Message) == "" {
			return nil
		}
		node, err := sexp.ParseOne(item.Message)
		if err != nil {
			return err
		}
		if err := inferFrontendMessageNodePayloadTypes(node, env, typeChecker, messageArities, "button", out); err != nil {
			return err
		}
	}
	return nil
}

func inferFrontendViewNodeMessagePayloadTypes(node model.FrontendViewNode, env frontendTypeEnv, typeChecker *frontendTypeChecker, messageArities map[string]int, out map[string][]frontendType) error {
	if node.Kind == "button" && strings.TrimSpace(node.Message) != "" {
		messageNode, err := sexp.ParseOne(node.Message)
		if err != nil {
			return err
		}
		if err := inferFrontendMessageNodePayloadTypes(messageNode, env, typeChecker, messageArities, "button", out); err != nil {
			return err
		}
	}
	for _, child := range node.Children {
		if err := inferFrontendViewNodeMessagePayloadTypes(child, env, typeChecker, messageArities, out); err != nil {
			return err
		}
	}
	return nil
}

func inferFrontendMessageNodePayloadTypes(node sexp.Node, env frontendTypeEnv, typeChecker *frontendTypeChecker, messageArities map[string]int, context string, out map[string][]frontendType) error {
	switch node.Kind {
	case sexp.KindSymbol:
		return nil
	case sexp.KindList:
		if len(node.Children) == 0 || node.Children[0].Kind != sexp.KindSymbol {
			return nil
		}
		name := canonicalFieldName(node.Children[0].Value)
		arity, ok := messageArities[name]
		if !ok || len(node.Children)-1 != arity {
			return nil
		}
		for index, arg := range node.Children[1:] {
			argType, err := typeChecker.inferExprType(arg, env)
			if err != nil {
				return fmt.Errorf("%s message %q argument %d: %w", context, node.Children[0].Value, index+1, err)
			}
			if frontendTypeIsUnresolved(argType) {
				return fmt.Errorf("%s message %q argument %d type could not be inferred", context, node.Children[0].Value, index+1)
			}
			if err := mergeScreenMessagePayloadType(out, name, index, argType); err != nil {
				return fmt.Errorf("message %s has incompatible UI payloads: %w", name, err)
			}
		}
	}
	return nil
}

func validateScreenMessagePayloadTypesResolved(screen model.FrontendScreen, payloads map[string][]frontendType) error {
	for _, message := range screen.Messages {
		for index, param := range message.Parameters {
			var value frontendType
			if index < len(payloads[message.Name]) {
				value = payloads[message.Name][index]
			}
			if frontendTypeIsUnresolved(value) {
				return fmt.Errorf("message %q parameter %q type could not be inferred", message.Name, param)
			}
		}
	}
	return nil
}

func validateFrontendCommandForms(node sexp.Node, allowedCommands map[string]int, commandKinds map[string]string, messageArities map[string]int) error {
	if node.Kind != sexp.KindList {
		return nil
	}
	if len(node.Children) > 0 && node.Children[0].Kind == sexp.KindSymbol && node.Children[0].Value == "command" {
		if len(node.Children) != 4 {
			return fmt.Errorf("command expects a backend call, a success reply, and a failure reply")
		}
		call := node.Children[1]
		if call.Kind != sexp.KindList || len(call.Children) == 0 {
			return fmt.Errorf("command expects a backend call like (load-orders) or (like-post post-id)")
		}
		if call.Children[0].Kind != sexp.KindSymbol {
			return fmt.Errorf("command backend call must start with a symbol")
		}
		commandName := canonicalFunctionName(call.Children[0].Value)
		arity, ok := allowedCommands[commandName]
		if !ok {
			return fmt.Errorf("command can only call a query or action, got %q", call.Children[0].Value)
		}
		if len(call.Children)-1 != arity {
			return fmt.Errorf("%s expects %d arguments", call.Children[0].Value, arity)
		}
		for index, messageNode := range node.Children[2:] {
			if messageNode.Kind != sexp.KindSymbol {
				return fmt.Errorf("command reply message must be a symbol")
			}
			name := canonicalFieldName(messageNode.Value)
			arity, ok := messageArities[name]
			if !ok {
				return fmt.Errorf("command references unknown message %q", messageNode.Value)
			}
			if arity > 1 {
				return fmt.Errorf("command reply %q must accept at most one argument", messageNode.Value)
			}
			if index == 0 && commandKinds[commandName] == "action" && arity != 0 {
				return fmt.Errorf("action success reply %q must not accept a payload", messageNode.Value)
			}
		}
	}
	for _, child := range node.Children {
		if err := validateFrontendCommandForms(child, allowedCommands, commandKinds, messageArities); err != nil {
			return err
		}
	}
	return nil
}

func validateFrontendTransitionStructure(node sexp.Node) error {
	if err := validateFrontendTransitionResult(node); err != nil {
		return err
	}
	return nil
}

func validateFrontendTransitionResult(node sexp.Node) error {
	if node.Kind == sexp.KindList && len(node.Children) > 0 && node.Children[0].Kind == sexp.KindSymbol {
		switch node.Children[0].Value {
		case "if":
			if len(node.Children) != 4 {
				return nil
			}
			if err := validateNoFrontendEffectForms(node.Children[1]); err != nil {
				return err
			}
			if err := validateFrontendTransitionResult(node.Children[2]); err != nil {
				return err
			}
			return validateFrontendTransitionResult(node.Children[3])
		case "cond":
			for _, clause := range node.Children[1:] {
				if clause.Kind != sexp.KindList || len(clause.Children) != 2 {
					return nil
				}
				head := clause.Children[0]
				if !(head.Kind == sexp.KindSymbol && head.Value == "else") {
					if err := validateNoFrontendEffectForms(head); err != nil {
						return err
					}
				}
				if err := validateFrontendTransitionResult(clause.Children[1]); err != nil {
					return err
				}
			}
			return nil
		case "match":
			if len(node.Children) < 3 {
				return nil
			}
			if err := validateNoFrontendEffectForms(node.Children[1]); err != nil {
				return err
			}
			for _, clause := range node.Children[2:] {
				if clause.Kind != sexp.KindList || len(clause.Children) != 2 {
					return nil
				}
				if err := validateFrontendTransitionResult(clause.Children[1]); err != nil {
					return err
				}
			}
			return nil
		case "let", "let*":
			if len(node.Children) != 3 {
				return nil
			}
			bindings := node.Children[1]
			if bindings.Kind != sexp.KindList {
				return nil
			}
			for _, binding := range bindings.Children {
				if binding.Kind != sexp.KindList || len(binding.Children) != 2 {
					return nil
				}
				if err := validateNoFrontendEffectForms(binding.Children[1]); err != nil {
					return err
				}
			}
			return validateFrontendTransitionResult(node.Children[2])
		case "begin":
			if len(node.Children) < 2 {
				return nil
			}
			for _, exprNode := range node.Children[1 : len(node.Children)-1] {
				if err := validateNoFrontendEffectForms(exprNode); err != nil {
					return err
				}
			}
			return validateFrontendTransitionResult(node.Children[len(node.Children)-1])
		}
	}

	if node.Kind != sexp.KindList || len(node.Children) != 2 {
		return parseError(node, "screen transition must return (model effects)")
	}
	if looksLikeExtraModelWrapper(node.Children[0]) {
		return parseError(node.Children[0], "screen transition model has an extra pair of parentheses; use (record ...) instead of ((record ...))")
	}
	if err := validateNoFrontendEffectForms(node.Children[0]); err != nil {
		return err
	}
	return validateFrontendEffectsExpression(node.Children[1])
}

func looksLikeExtraModelWrapper(node sexp.Node) bool {
	return node.Kind == sexp.KindList &&
		len(node.Children) == 1 &&
		node.Children[0].Kind == sexp.KindList &&
		len(node.Children[0].Children) > 0 &&
		node.Children[0].Children[0].Kind == sexp.KindSymbol
}

func validateFrontendEffectsExpression(node sexp.Node) error {
	if node.Kind == sexp.KindList && len(node.Children) > 0 && node.Children[0].Kind == sexp.KindSymbol {
		switch node.Children[0].Value {
		case "if":
			if len(node.Children) != 4 {
				return nil
			}
			if err := validateNoFrontendEffectForms(node.Children[1]); err != nil {
				return err
			}
			if err := validateFrontendEffectsExpression(node.Children[2]); err != nil {
				return err
			}
			return validateFrontendEffectsExpression(node.Children[3])
		case "cond":
			for _, clause := range node.Children[1:] {
				if clause.Kind != sexp.KindList || len(clause.Children) != 2 {
					return nil
				}
				head := clause.Children[0]
				if !(head.Kind == sexp.KindSymbol && head.Value == "else") {
					if err := validateNoFrontendEffectForms(head); err != nil {
						return err
					}
				}
				if err := validateFrontendEffectsExpression(clause.Children[1]); err != nil {
					return err
				}
			}
			return nil
		case "match":
			if len(node.Children) < 3 {
				return nil
			}
			if err := validateNoFrontendEffectForms(node.Children[1]); err != nil {
				return err
			}
			for _, clause := range node.Children[2:] {
				if clause.Kind != sexp.KindList || len(clause.Children) != 2 {
					return nil
				}
				if err := validateFrontendEffectsExpression(clause.Children[1]); err != nil {
					return err
				}
			}
			return nil
		case "let", "let*":
			if len(node.Children) != 3 {
				return nil
			}
			bindings := node.Children[1]
			if bindings.Kind != sexp.KindList {
				return nil
			}
			for _, binding := range bindings.Children {
				if binding.Kind != sexp.KindList || len(binding.Children) != 2 {
					return nil
				}
				if err := validateNoFrontendEffectForms(binding.Children[1]); err != nil {
					return err
				}
			}
			return validateFrontendEffectsExpression(node.Children[2])
		case "begin":
			if len(node.Children) < 2 {
				return nil
			}
			for _, exprNode := range node.Children[1 : len(node.Children)-1] {
				if err := validateNoFrontendEffectForms(exprNode); err != nil {
					return err
				}
			}
			return validateFrontendEffectsExpression(node.Children[len(node.Children)-1])
		case "command", "go", "back":
			return parseError(node, "screen effects must be a list; wrap %s in an extra pair of parentheses", node.Children[0].Value)
		}
	}

	if node.Kind != sexp.KindList {
		return parseError(node, "screen effects must be a list")
	}
	for _, child := range node.Children {
		if err := validateFrontendEffect(child); err != nil {
			return err
		}
	}
	return nil
}

func validateFrontendEffect(node sexp.Node) error {
	if node.Kind == sexp.KindList && len(node.Children) > 0 && node.Children[0].Kind == sexp.KindSymbol {
		switch node.Children[0].Value {
		case "if":
			if len(node.Children) != 4 {
				return nil
			}
			if err := validateNoFrontendEffectForms(node.Children[1]); err != nil {
				return err
			}
			if err := validateFrontendEffect(node.Children[2]); err != nil {
				return err
			}
			return validateFrontendEffect(node.Children[3])
		case "cond":
			for _, clause := range node.Children[1:] {
				if clause.Kind != sexp.KindList || len(clause.Children) != 2 {
					return nil
				}
				head := clause.Children[0]
				if !(head.Kind == sexp.KindSymbol && head.Value == "else") {
					if err := validateNoFrontendEffectForms(head); err != nil {
						return err
					}
				}
				if err := validateFrontendEffect(clause.Children[1]); err != nil {
					return err
				}
			}
			return nil
		case "match":
			if len(node.Children) < 3 {
				return nil
			}
			if err := validateNoFrontendEffectForms(node.Children[1]); err != nil {
				return err
			}
			for _, clause := range node.Children[2:] {
				if clause.Kind != sexp.KindList || len(clause.Children) != 2 {
					return nil
				}
				if err := validateFrontendEffect(clause.Children[1]); err != nil {
					return err
				}
			}
			return nil
		case "let", "let*":
			if len(node.Children) != 3 {
				return nil
			}
			bindings := node.Children[1]
			if bindings.Kind != sexp.KindList {
				return nil
			}
			for _, binding := range bindings.Children {
				if binding.Kind != sexp.KindList || len(binding.Children) != 2 {
					return nil
				}
				if err := validateNoFrontendEffectForms(binding.Children[1]); err != nil {
					return err
				}
			}
			return validateFrontendEffect(node.Children[2])
		case "begin":
			if len(node.Children) < 2 {
				return nil
			}
			for _, exprNode := range node.Children[1 : len(node.Children)-1] {
				if err := validateNoFrontendEffectForms(exprNode); err != nil {
					return err
				}
			}
			return validateFrontendEffect(node.Children[len(node.Children)-1])
		case "command", "go", "back":
			return validateFrontendEffectShape(node)
		}
	}

	return parseError(node, "screen effects can only contain (command ...), (go ...), or (back)")
}

func validateNoFrontendEffectForms(node sexp.Node) error {
	if node.Kind != sexp.KindList {
		return nil
	}
	if len(node.Children) > 0 && node.Children[0].Kind == sexp.KindSymbol {
		switch node.Children[0].Value {
		case "command", "go", "back":
			return parseError(node, "%s can only be used inside the effects list returned by screen init/update", node.Children[0].Value)
		}
	}
	for _, child := range node.Children {
		if err := validateNoFrontendEffectForms(child); err != nil {
			return err
		}
	}
	return nil
}

func validateFrontendEffectShape(node sexp.Node) error {
	if node.Kind != sexp.KindList || len(node.Children) == 0 || node.Children[0].Kind != sexp.KindSymbol {
		return parseError(node, "screen effect must be an operation")
	}
	switch node.Children[0].Value {
	case "command":
		if len(node.Children) != 4 {
			return parseError(node, "command expects a backend call, a success reply, and a failure reply")
		}
		call := node.Children[1]
		if call.Kind != sexp.KindList || len(call.Children) == 0 {
			return parseError(node, "command expects a backend call like (load-orders) or (like-post post-id)")
		}
		if call.Children[0].Kind != sexp.KindSymbol {
			return parseError(node, "command backend call must start with a symbol")
		}
		if node.Children[2].Kind != sexp.KindSymbol {
			return parseError(node, "command reply message must be a symbol")
		}
		if node.Children[3].Kind != sexp.KindSymbol {
			return parseError(node, "command failure reply must be a symbol")
		}
	case "go":
		if len(node.Children) < 2 {
			return parseError(node, "go expects a destination")
		}
		if node.Children[1].Kind != sexp.KindSymbol {
			return parseError(node, "go destination must be a symbol")
		}
	case "back":
		if len(node.Children) != 1 {
			return parseError(node, "back does not accept arguments")
		}
	}
	return nil
}

func validateFrontendScreenNavigation(screen model.FrontendScreen, screens map[string]model.FrontendScreen, typeChecker *frontendTypeChecker, modelType frontendType) error {
	if strings.TrimSpace(screen.InitExpression) != "" {
		if err := validateFrontendGoCallsInTransition(screen.InitExpression, typeChecker.baseEnv(screen), typeChecker, screens); err != nil {
			return fmt.Errorf("init: %w", err)
		}
	}
	if strings.TrimSpace(screen.UpdateBody) != "" {
		env := typeChecker.baseEnv(screen)
		if screen.UpdateMessage != "" {
			env[screen.UpdateMessage] = typeChecker.screenMessageType(screen)
		}
		if screen.UpdateModel != "" {
			env[screen.UpdateModel] = modelType
		}
		if err := validateFrontendGoCallsInTransition(screen.UpdateBody, env, typeChecker, screens); err != nil {
			return fmt.Errorf("update: %w", err)
		}
	}
	return nil
}

func validateFrontendScreenItems(screen model.FrontendScreen, screens map[string]model.FrontendScreen, messageArities map[string]int, allowedFunctions map[string]int, allowedRecords map[string][]string, typeChecker *frontendTypeChecker, modelType frontendType, actionNames map[string]struct{}) error {
	for _, section := range screen.Sections {
		for _, item := range section.Items {
			if err := validateFrontendScreenItem(screen, screens, item, messageArities, allowedFunctions, allowedRecords, typeChecker, modelType, actionNames); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateFrontendScreenItem(screen model.FrontendScreen, screens map[string]model.FrontendScreen, item model.FrontendItem, messageArities map[string]int, allowedFunctions map[string]int, allowedRecords map[string][]string, typeChecker *frontendTypeChecker, modelType frontendType, actionNames map[string]struct{}) error {
	if strings.TrimSpace(item.Condition) != "" {
		allowed := map[string]struct{}{"model": {}}
		for _, param := range screen.Parameters {
			allowed[param] = struct{}{}
		}
		if _, err := expr.Parse(item.Condition, expr.ParserOptions{
			AllowedVariables: expr.AllowedVariablesWithBuiltins(allowed),
			AllowedFunctions: allowedFunctions,
			AllowedRecords:   allowedRecords,
			AllowedVariants:  allowedTypeVariants(typeChecker.types),
		}); err != nil {
			return fmt.Errorf("%s condition: %w", item.Kind, err)
		}
	}
	if item.Disabled != "" {
		fieldType, err := frontendModelFieldType(modelType, item.Disabled)
		if err != nil {
			return fmt.Errorf("%s disabled: %w", item.Kind, err)
		}
		if !frontendAssignable(fieldType, boolFrontendType()) {
			return fmt.Errorf("%s disabled expects bool field, got %s", item.Kind, fieldType.String())
		}
	}
	switch item.Kind {
	case "field":
		if len(screen.Parameters) == 0 {
			return fmt.Errorf("field requires a screen row parameter")
		}
		if item.Field == "" {
			return fmt.Errorf("field requires a row field")
		}
		rowType := anyFrontendType()
		if typeChecker != nil && len(screen.Parameters) > 0 {
			rowType = typeChecker.screenParameterType(screen.Name, 0)
		}
		if rowType.Kind == frontendTypeAny {
			return fmt.Errorf("field cannot be type-checked because screen parameter %q is unresolved", screen.Parameters[0])
		}
		if _, err := frontendRecordFieldType(rowType, item.Field); err != nil {
			return err
		}
	case "textInput", "textarea", "toggle", "select":
		if strings.TrimSpace(item.ModelField) == "" {
			return fmt.Errorf("%s requires a model field", item.Kind)
		}
		arity, ok := messageArities[item.Message]
		if !ok {
			return fmt.Errorf("%s references unknown message %q", item.Kind, item.Message)
		}
		if arity != 1 {
			return fmt.Errorf("%s message %q must accept exactly one argument", item.Kind, item.Message)
		}
		fieldType, err := frontendModelFieldType(modelType, item.ModelField)
		if err != nil {
			return err
		}
		switch item.Kind {
		case "toggle":
			if !frontendAssignable(fieldType, boolFrontendType()) {
				return fmt.Errorf("toggle model field %q must be bool, got %s", item.ModelField, fieldType.String())
			}
		default:
			if !frontendAssignable(fieldType, stringFrontendType()) {
				return fmt.Errorf("%s model field %q must be string, got %s", item.Kind, item.ModelField, fieldType.String())
			}
		}
		if item.Kind == "select" && len(item.Options) == 0 {
			return fmt.Errorf("select requires at least one option")
		}
	case "list":
		if strings.TrimSpace(item.ModelField) == "" {
			return fmt.Errorf("list requires a model field")
		}
		fieldType, err := frontendModelFieldType(modelType, item.ModelField)
		if err != nil {
			return err
		}
		if fieldType.Kind != frontendTypeList || fieldType.Element == nil {
			return fmt.Errorf("list model field %q must be a list, got %s", item.ModelField, fieldType.String())
		}
		if typeChecker != nil {
			entityType, ok, err := typeChecker.namedType(canonicalFieldName(item.Entity))
			if err != nil {
				return err
			}
			if ok && !frontendRecordTypesMatch(*fieldType.Element, entityType) {
				return fmt.Errorf("list model field %q must contain %s, got %s", item.ModelField, entityType.String(), fieldType.String())
			}
			if item.TitleField != "" {
				if _, err := frontendRecordFieldType(*fieldType.Element, item.TitleField); err != nil {
					return fmt.Errorf("list title: %w", err)
				}
			}
			if item.SubtitleField != "" {
				if _, err := frontendRecordFieldType(*fieldType.Element, item.SubtitleField); err != nil {
					return fmt.Errorf("list subtitle: %w", err)
				}
			}
			if item.Destination != "" {
				target, ok := screens[item.Destination]
				if !ok {
					return fmt.Errorf("list destination references unknown screen %q", item.Destination)
				}
				if len(target.Parameters) != 1 {
					return fmt.Errorf("list destination %s expects %d arguments, but list open provides exactly 1 row", target.Name, len(target.Parameters))
				}
				targetType := typeChecker.screenParameterType(target.Name, 0)
				if targetType.Kind == frontendTypeAny {
					return fmt.Errorf("list destination %s cannot be type-checked because parameter %q is unresolved", target.Name, target.Parameters[0])
				}
				if !frontendAssignable(entityType, targetType) {
					return fmt.Errorf("list destination %s expects %s, got %s", target.Name, targetType.String(), entityType.String())
				}
			}
		}
		if item.Action != "" {
			if _, ok := actionNames[item.Action]; !ok {
				return fmt.Errorf("list action references unknown action %q", item.Action)
			}
		}
	}
	return nil
}

func frontendModelFieldType(modelType frontendType, fieldName string) (frontendType, error) {
	return frontendRecordFieldType(modelType, fieldName)
}

func frontendRecordFieldType(recordType frontendType, fieldName string) (frontendType, error) {
	if recordType.Kind != frontendTypeRecord {
		return frontendType{}, fmt.Errorf("model must be a record, got %s", recordType.String())
	}
	value, ok := recordType.Fields[canonicalFieldName(fieldName)]
	if !ok {
		return frontendType{}, fmt.Errorf("record %s has no field %q", recordType.Name, canonicalFieldName(fieldName))
	}
	return value, nil
}

func frontendRecordTypesMatch(left frontendType, right frontendType) bool {
	if left.Kind == frontendTypeAny || right.Kind == frontendTypeAny {
		return true
	}
	if left.Kind != frontendTypeRecord || right.Kind != frontendTypeRecord {
		return frontendAssignable(left, right)
	}
	return true
}

func validateFrontendGoCallsInTransition(raw string, env frontendTypeEnv, typeChecker *frontendTypeChecker, screens map[string]model.FrontendScreen) error {
	node, err := sexp.ParseOne(raw)
	if err != nil {
		return err
	}
	return validateFrontendGoCallsInTransitionNode(node, env, typeChecker, screens)
}

func validateFrontendGoCallsInTransitionNode(node sexp.Node, env frontendTypeEnv, typeChecker *frontendTypeChecker, screens map[string]model.FrontendScreen) error {
	if node.Kind == sexp.KindList && len(node.Children) > 0 && node.Children[0].Kind == sexp.KindSymbol {
		switch node.Children[0].Value {
		case "if":
			if err := validateFrontendGoCallsInTransitionNode(node.Children[2], env, typeChecker, screens); err != nil {
				return err
			}
			return validateFrontendGoCallsInTransitionNode(node.Children[3], env, typeChecker, screens)
		case "cond":
			for _, clause := range node.Children[1:] {
				if err := validateFrontendGoCallsInTransitionNode(clause.Children[1], env, typeChecker, screens); err != nil {
					return err
				}
			}
			return nil
		case "match":
			_, clauses, err := typeChecker.prepareMatch(node.Children[1:], env)
			if err != nil {
				return err
			}
			for _, clause := range clauses {
				if err := validateFrontendGoCallsInTransitionNode(clause.Body, clause.Env, typeChecker, screens); err != nil {
					return err
				}
			}
			return nil
		case "let":
			child, err := typeChecker.bindLetEnv(node.Children[1:], env, false)
			if err != nil {
				return err
			}
			return validateFrontendGoCallsInTransitionNode(node.Children[2], child, typeChecker, screens)
		case "let*":
			child, err := typeChecker.bindLetEnv(node.Children[1:], env, true)
			if err != nil {
				return err
			}
			return validateFrontendGoCallsInTransitionNode(node.Children[2], child, typeChecker, screens)
		case "begin":
			if len(node.Children) < 2 {
				return nil
			}
			return validateFrontendGoCallsInTransitionNode(node.Children[len(node.Children)-1], env, typeChecker, screens)
		}
	}
	if node.Kind != sexp.KindList || len(node.Children) != 2 || node.Children[1].Kind != sexp.KindList {
		return nil
	}
	return validateFrontendGoCallsInEffects(node.Children[1], env, typeChecker, screens)
}

func validateFrontendGoCallsInEffects(node sexp.Node, env frontendTypeEnv, typeChecker *frontendTypeChecker, screens map[string]model.FrontendScreen) error {
	if node.Kind == sexp.KindList && len(node.Children) > 0 && node.Children[0].Kind == sexp.KindSymbol {
		switch node.Children[0].Value {
		case "if":
			if err := validateFrontendGoCallsInEffects(node.Children[2], env, typeChecker, screens); err != nil {
				return err
			}
			return validateFrontendGoCallsInEffects(node.Children[3], env, typeChecker, screens)
		case "cond":
			for _, clause := range node.Children[1:] {
				if err := validateFrontendGoCallsInEffects(clause.Children[1], env, typeChecker, screens); err != nil {
					return err
				}
			}
			return nil
		case "match":
			_, clauses, err := typeChecker.prepareMatch(node.Children[1:], env)
			if err != nil {
				return err
			}
			for _, clause := range clauses {
				if err := validateFrontendGoCallsInEffects(clause.Body, clause.Env, typeChecker, screens); err != nil {
					return err
				}
			}
			return nil
		case "let":
			child, err := typeChecker.bindLetEnv(node.Children[1:], env, false)
			if err != nil {
				return err
			}
			return validateFrontendGoCallsInEffects(node.Children[2], child, typeChecker, screens)
		case "let*":
			child, err := typeChecker.bindLetEnv(node.Children[1:], env, true)
			if err != nil {
				return err
			}
			return validateFrontendGoCallsInEffects(node.Children[2], child, typeChecker, screens)
		case "begin":
			if len(node.Children) < 2 {
				return nil
			}
			return validateFrontendGoCallsInEffects(node.Children[len(node.Children)-1], env, typeChecker, screens)
		case "go":
			return validateFrontendGoCall(node, env, typeChecker, screens)
		case "command", "back":
			return nil
		}
	}
	if node.Kind != sexp.KindList {
		return nil
	}
	for _, child := range node.Children {
		if err := validateFrontendGoCallsInEffects(child, env, typeChecker, screens); err != nil {
			return err
		}
	}
	return nil
}

func validateFrontendGoCall(node sexp.Node, env frontendTypeEnv, typeChecker *frontendTypeChecker, screens map[string]model.FrontendScreen) error {
	if len(node.Children) < 2 || node.Children[1].Kind != sexp.KindSymbol {
		return fmt.Errorf("go expects a screen")
	}
	target := canonicalScreenName(node.Children[1].Value)
	targetScreen, ok := screens[target]
	if !ok {
		return fmt.Errorf("go references unknown screen %q", node.Children[1].Value)
	}
	args := node.Children[2:]
	if len(args) != len(targetScreen.Parameters) {
		return fmt.Errorf("go to %s expects %d argument(s), got %d", target, len(targetScreen.Parameters), len(args))
	}
	for index, arg := range args {
		targetType := typeChecker.screenParameterType(target, index)
		if targetType.Kind == frontendTypeAny {
			return fmt.Errorf("go to %s cannot be type-checked because parameter %q is unresolved", target, targetScreen.Parameters[index])
		}
		argType, err := typeChecker.inferExprType(arg, env)
		if err != nil {
			return err
		}
		if !frontendAssignable(argType, targetType) {
			return fmt.Errorf("go to %s expects %s for parameter %q, got %s", target, targetType.String(), targetScreen.Parameters[index], argType.String())
		}
	}
	return nil
}

func validateFrontendScreenUIContext(screen model.FrontendScreen, hasUpdate bool, viewRawNodes []sexp.Node, sectionNodes []sexp.Node) error {
	messageArities := frontendMessageArities(screen.Messages)
	for _, viewRawNode := range viewRawNodes {
		if err := validateFrontendViewNodeContext(viewRawNode, hasUpdate, messageArities); err != nil {
			return err
		}
	}
	for _, sectionNode := range sectionNodes {
		if err := validateFrontendSectionContext(sectionNode, hasUpdate, messageArities); err != nil {
			return err
		}
	}
	return nil
}

func validateFrontendViewNodeContext(node sexp.Node, hasUpdate bool, messageArities map[string]int) error {
	if node.Kind != sexp.KindList || len(node.Children) == 0 {
		return nil
	}
	head, ok := symbolValue(node.Children[0])
	if !ok {
		return nil
	}
	switch head {
	case "section":
		for _, child := range node.Children[1:] {
			if child.Kind != sexp.KindList || len(child.Children) == 0 {
				continue
			}
			key, _ := symbolValue(child.Children[0])
			if key == "title" {
				continue
			}
			if err := validateFrontendViewNodeContext(child, hasUpdate, messageArities); err != nil {
				return err
			}
		}
	case "button":
		if !hasUpdate {
			return parseError(node, "button requires a screen that defines update")
		}
		if len(node.Children) >= 3 {
			if err := validateFrontendMessageNode(node.Children[2], messageArities, "button"); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateFrontendSectionContext(sectionNode sexp.Node, hasUpdate bool, messageArities map[string]int) error {
	items, err := listChildren(sectionNode, "section")
	if err != nil {
		return err
	}
	_, index, err := parseSectionMetadata(items, sectionNode)
	if err != nil {
		return err
	}
	children, err := nestedClauseList(items, sectionNode, index, "section")
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := validateFrontendSectionItemContext(child, hasUpdate, messageArities); err != nil {
			return err
		}
	}
	return nil
}

func validateFrontendSectionItemContext(node sexp.Node, hasUpdate bool, messageArities map[string]int) error {
	items, err := listChildren(node, "screen item")
	if err != nil {
		return err
	}
	head, _ := symbolValue(items[0])
	switch head {
	case "if":
		if len(items) >= 3 {
			return validateFrontendSectionItemContext(items[2], hasUpdate, messageArities)
		}
	case "empty":
		return nil
	case "button":
		if !hasUpdate {
			return parseError(node, "button requires a screen that defines update")
		}
		if len(items) >= 3 {
			if err := validateFrontendMessageNode(items[2], messageArities, "button"); err != nil {
				return err
			}
		}
	case "text-input", "textarea", "toggle", "select":
		if !hasUpdate {
			return parseError(node, "%s requires a screen that defines update", head)
		}
	}
	return nil
}

func validateFrontendMessageNode(node sexp.Node, messageArities map[string]int, context string) error {
	switch node.Kind {
	case sexp.KindSymbol:
		name := canonicalFieldName(node.Value)
		arity, ok := messageArities[name]
		if !ok {
			return parseError(node, "%s references unknown message %q", context, node.Value)
		}
		if arity != 0 {
			return parseError(node, "%s message %q expects %d arguments", context, node.Value, arity)
		}
		return nil
	case sexp.KindList:
		if len(node.Children) == 0 || node.Children[0].Kind != sexp.KindSymbol {
			return parseError(node, "%s message must start with a symbol", context)
		}
		head := node.Children[0].Value
		switch head {
		case "command", "go", "back":
			return parseError(node, "%s can only be used inside the effects list returned by screen init/update", head)
		}
		name := canonicalFieldName(head)
		arity, ok := messageArities[name]
		if !ok {
			return parseError(node.Children[0], "%s references unknown message %q", context, head)
		}
		if len(node.Children)-1 != arity {
			return parseError(node, "%s message %q expects %d arguments", context, head, arity)
		}
		return nil
	default:
		return parseError(node, "%s message must be a symbol or list", context)
	}
}

func allowedFunctionArities(functions map[string]*model.Function) map[string]int {
	out := map[string]int{}
	for name, fn := range functions {
		out[name] = len(fn.Parameters)
	}
	return out
}

func allowedRecordFields(records map[string]*model.Record) map[string][]string {
	out := map[string][]string{}
	for name, record := range records {
		fields := make([]string, 0, len(record.Fields))
		for _, field := range record.Fields {
			fields = append(fields, field.Name)
		}
		out[name] = fields
	}
	return out
}

func allowedTypeVariants(types map[string]*model.EnumType) map[string]int {
	out := map[string]int{}
	for _, typ := range types {
		for _, variant := range typ.Variants {
			out[variant.Name] = len(variant.Fields)
		}
	}
	return out
}

func frontendCompileTimeVariables(screen model.FrontendScreen, node sexp.Node, includeUpdate bool) map[string]struct{} {
	out := map[string]struct{}{}
	for _, param := range screen.Parameters {
		out[param] = struct{}{}
	}
	if includeUpdate {
		if screen.UpdateMessage != "" {
			out[screen.UpdateMessage] = struct{}{}
		}
		if screen.UpdateModel != "" {
			out[screen.UpdateModel] = struct{}{}
		}
	}
	collectNonHeadSymbols(node, out)
	return out
}

func collectNonHeadSymbols(node sexp.Node, out map[string]struct{}) {
	switch node.Kind {
	case sexp.KindSymbol:
		out[canonicalFieldName(node.Value)] = struct{}{}
	case sexp.KindList:
		skipHead := len(node.Children) > 0 && node.Children[0].Kind == sexp.KindSymbol
		for index, child := range node.Children {
			if skipHead && index == 0 {
				continue
			}
			collectNonHeadSymbols(child, out)
		}
	}
}

func symbolValues(nodes []sexp.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, node := range nodes {
		symbol, _ := symbolValue(node)
		out = append(out, symbol)
	}
	return out
}

func canonicalFunctionName(value string) string {
	return canonicalFieldName(value)
}

func canonicalFunctionParameters(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, canonicalFieldName(value))
	}
	return out
}

func applyAuthSettings(target *model.AuthConfig, auth *authValue) {
	if auth.CodeTTLMinutes != nil {
		target.CodeTTLMinutes = *auth.CodeTTLMinutes
	}
	if auth.SessionTTLHours != nil {
		target.SessionTTLHours = *auth.SessionTTLHours
	}
	if auth.AuthRequestCodeRateLimit != nil {
		target.AuthRequestCodeRateLimit = auth.AuthRequestCodeRateLimit
	}
	if auth.AuthLoginRateLimit != nil {
		target.AuthLoginRateLimit = auth.AuthLoginRateLimit
	}
	if auth.AdminUISessionTTLHours != nil {
		target.AdminUISessionTTLHours = auth.AdminUISessionTTLHours
	}
	if auth.SecurityFramePolicy != nil {
		target.SecurityFramePolicy = auth.SecurityFramePolicy
	}
	if auth.SecurityReferrerPolicy != nil {
		target.SecurityReferrerPolicy = auth.SecurityReferrerPolicy
	}
	if auth.SecurityContentNoSniff != nil {
		target.SecurityContentNoSniff = auth.SecurityContentNoSniff
	}
	if auth.EmailFrom != "" {
		target.EmailFrom = auth.EmailFrom
	}
	if auth.EmailSubject != "" {
		target.EmailSubject = auth.EmailSubject
	}
	if auth.SMTPHost != "" {
		target.SMTPHost = auth.SMTPHost
	}
	if auth.SMTPPort != nil {
		target.SMTPPort = *auth.SMTPPort
	}
	if auth.SMTPUsername != "" {
		target.SMTPUsername = auth.SMTPUsername
	}
	if auth.SMTPPasswordEnv != "" {
		target.SMTPPasswordEnv = auth.SMTPPasswordEnv
	}
	if auth.SMTPStartTLS != nil {
		target.SMTPStartTLS = *auth.SMTPStartTLS
	}
}

func defaultAuthConfig(appName string) *model.AuthConfig {
	return &model.AuthConfig{
		UserEntity:      "User",
		EmailField:      "email",
		RoleField:       "role",
		CodeTTLMinutes:  10,
		SessionTTLHours: 24,
		EmailFrom:       "no-reply@mar.local",
		EmailSubject:    "Your " + humanizeKebab(appName) + " login code",
		SMTPPort:        587,
		SMTPStartTLS:    true,
	}
}

func defaultDatabaseName(appName string) string {
	return appName + ".db"
}

func mapPrimitiveType(name string) (string, error) {
	switch name {
	case "string":
		return "String", nil
	case "int":
		return "Int", nil
	case "decimal":
		return "Decimal", nil
	case "bool":
		return "Bool", nil
	case "date":
		return "Date", nil
	case "datetime":
		return "DateTime", nil
	default:
		return "", fmt.Errorf("unknown type %q", name)
	}
}

func parseRecordType(node sexp.Node) (string, error) {
	switch node.Kind {
	case sexp.KindSymbol:
		switch node.Value {
		case "string", "bool", "int", "decimal", "date", "datetime", "cursor":
			return node.Value, nil
		default:
			return canonicalFieldName(node.Value), nil
		}
	case sexp.KindList:
		if len(node.Children) == 0 {
			return "", fmt.Errorf("type list cannot be empty")
		}
		head, ok := symbolValue(node.Children[0])
		if !ok {
			return "", fmt.Errorf("type head must be a symbol")
		}
		switch head {
		case "maybe", "list":
			if len(node.Children) != 2 {
				return "", fmt.Errorf("%s expects one type argument", head)
			}
			arg, err := parseRecordType(node.Children[1])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("(%s %s)", head, arg), nil
		case "result":
			if len(node.Children) != 3 {
				return "", fmt.Errorf("result expects two type arguments")
			}
			left, err := parseRecordType(node.Children[1])
			if err != nil {
				return "", err
			}
			right, err := parseRecordType(node.Children[2])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("(result %s %s)", left, right), nil
		case "unit":
			if len(node.Children) != 1 {
				return "", fmt.Errorf("unit does not accept arguments")
			}
			return "(unit)", nil
		default:
			return "", fmt.Errorf("unknown type form %q", head)
		}
	default:
		return "", fmt.Errorf("unsupported type expression")
	}
}

func listChildren(node sexp.Node, context string) ([]sexp.Node, error) {
	if node.Kind != sexp.KindList || len(node.Children) == 0 {
		return nil, parseError(node, "%s must be a non-empty list", context)
	}
	return node.Children, nil
}

func nestedClauseList(items []sexp.Node, parent sexp.Node, index int, label string) ([]sexp.Node, error) {
	if len(items) == index {
		return []sexp.Node{}, nil
	}
	if len(items) != index+1 || items[index].Kind != sexp.KindList {
		return nil, parseError(parent, "%s expects one nested list of clauses", label)
	}
	return items[index].Children, nil
}

func symbolValue(node sexp.Node) (string, bool) {
	if node.Kind != sexp.KindSymbol {
		return "", false
	}
	return node.Value, true
}

func parseAuthorizeActions(node sexp.Node) ([]string, error) {
	switch node.Kind {
	case sexp.KindSymbol:
		return []string{node.Value}, nil
	case sexp.KindList:
		if len(node.Children) == 0 {
			return nil, parseError(node, "authorize action list cannot be empty")
		}
		actions := make([]string, 0, len(node.Children))
		for _, child := range node.Children {
			action, ok := symbolValue(child)
			if !ok {
				return nil, parseError(child, "authorize action names must be symbols")
			}
			actions = append(actions, action)
		}
		return actions, nil
	default:
		return nil, parseError(node, "authorize action name must be a symbol or a list of symbols")
	}
}

func stringLiteral(node sexp.Node) string {
	return node.Value
}

func symbolOrStringValue(node sexp.Node) (string, error) {
	switch node.Kind {
	case sexp.KindString, sexp.KindSymbol:
		return node.Value, nil
	default:
		return "", fmt.Errorf("expected symbol or string")
	}
}

func boolLiteral(node sexp.Node) (bool, error) {
	if node.Kind != sexp.KindSymbol {
		return false, fmt.Errorf("expected bool")
	}
	switch node.Value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("expected bool")
	}
}

func intLiteral(node sexp.Node) (int, error) {
	if node.Kind != sexp.KindNumber && node.Kind != sexp.KindSymbol {
		return 0, fmt.Errorf("expected integer")
	}
	value, err := strconv.Atoi(node.Value)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func literalValue(node sexp.Node) (any, error) {
	switch node.Kind {
	case sexp.KindString:
		return node.Value, nil
	case sexp.KindNumber:
		if strings.Contains(node.Value, ".") {
			return expr.ParseDecimal(node.Value)
		}
		return strconv.ParseInt(node.Value, 10, 64)
	case sexp.KindSymbol:
		switch node.Value {
		case "true":
			return true, nil
		case "false":
			return false, nil
		default:
			return nil, fmt.Errorf("unsupported default literal %q", node.Value)
		}
	default:
		return nil, fmt.Errorf("unsupported default literal")
	}
}

func validateFieldDefaultLiteral(entityName, fieldName string, field model.Field, literal any) error {
	if field.RelationEntity != "" {
		return nil
	}
	switch field.Type {
	case "String":
		if _, ok := literal.(string); !ok {
			return fmt.Errorf("entity %s default %s: expects string, got %s", entityName, fieldName, literalTypeName(literal))
		}
	case "Bool":
		if _, ok := literal.(bool); !ok {
			return fmt.Errorf("entity %s default %s: expects bool, got %s", entityName, fieldName, literalTypeName(literal))
		}
	case "Int":
		if _, ok := literal.(int64); !ok {
			return fmt.Errorf("entity %s default %s: expects int, got %s", entityName, fieldName, literalTypeName(literal))
		}
	case "Decimal":
		switch literal.(type) {
		case int64, expr.Decimal:
		default:
			return fmt.Errorf("entity %s default %s: expects decimal, got %s", entityName, fieldName, literalTypeName(literal))
		}
	case "Date":
		if _, ok := literal.(int64); !ok {
			return fmt.Errorf("entity %s default %s: expects date, got %s", entityName, fieldName, literalTypeName(literal))
		}
	case "DateTime":
		if _, ok := literal.(int64); !ok {
			return fmt.Errorf("entity %s default %s: expects datetime, got %s", entityName, fieldName, literalTypeName(literal))
		}
	default:
		if len(field.EnumValues) == 0 {
			return nil
		}
		text, ok := literal.(string)
		if !ok {
			return fmt.Errorf("entity %s default %s: expects %s, got %s", entityName, fieldName, field.Type, literalTypeName(literal))
		}
		for _, enumValue := range field.EnumValues {
			if text == enumValue {
				return nil
			}
		}
		return fmt.Errorf("entity %s default %s: must be one of: %s", entityName, fieldName, strings.Join(field.EnumValues, ", "))
	}
	return nil
}

func literalTypeName(literal any) string {
	switch literal.(type) {
	case string:
		return "string"
	case bool:
		return "bool"
	case int64:
		return "int"
	case expr.Decimal:
		return "decimal"
	default:
		return "unknown"
	}
}

func canonicalTypeName(symbol string) string {
	parts := strings.FieldsFunc(symbol, func(r rune) bool {
		return r == '-' || r == '_'
	})
	var b strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(part)
		b.WriteRune(unicode.ToUpper(runes[0]))
		if len(runes) > 1 {
			b.WriteString(string(runes[1:]))
		}
	}
	return b.String()
}

func canonicalFieldName(symbol string) string {
	return strings.ReplaceAll(symbol, "-", "_")
}

func canonicalScreenName(symbol string) string {
	return canonicalTypeName(symbol)
}

func inlineNodeString(node sexp.Node) string {
	if node.Kind == "" {
		return ""
	}
	return sexp.InlineString(node)
}

func pluralizeSnake(symbol string) string {
	base := canonicalFieldName(symbol)
	switch {
	case strings.HasSuffix(base, "s"), strings.HasSuffix(base, "x"), strings.HasSuffix(base, "ch"), strings.HasSuffix(base, "sh"):
		return base + "es"
	case strings.HasSuffix(base, "y") && len(base) > 1 && !isVowel(rune(base[len(base)-2])):
		return base[:len(base)-1] + "ies"
	default:
		return base + "s"
	}
}

func isVowel(ch rune) bool {
	switch unicode.ToLower(ch) {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	default:
		return false
	}
}

func humanizeKebab(value string) string {
	parts := strings.Split(value, "-")
	for i, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(part)
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}

func parseError(node sexp.Node, format string, args ...any) error {
	return fmt.Errorf("line %d:%d: %s", node.Line, node.Column, fmt.Sprintf(format, args...))
}
