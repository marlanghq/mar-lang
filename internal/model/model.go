package model

import "strings"

type App struct {
	AppName      string        `json:"appName"`
	Port         int           `json:"port"`
	Database     string        `json:"database"`
	IOS          *IOSConfig    `json:"ios,omitempty"`
	Public       *PublicConfig `json:"public,omitempty"`
	System       *SystemConfig `json:"system,omitempty"`
	Types        []EnumType    `json:"types,omitempty"`
	Records      []Record      `json:"records,omitempty"`
	Functions    []Function    `json:"functions,omitempty"`
	Entities     []Entity      `json:"entities"`
	Queries      []Query       `json:"queries,omitempty"`
	Auth         *AuthConfig   `json:"auth,omitempty"`
	InputAliases []TypeAlias   `json:"inputAliases,omitempty"`
	Actions      []Action      `json:"actions,omitempty"`
	Screens      *Frontend     `json:"screens,omitempty"`
	Warnings     []string      `json:"warnings,omitempty"`
}

type EnumType struct {
	Name     string        `json:"name"`
	Values   []string      `json:"values,omitempty"`
	Variants []TypeVariant `json:"variants,omitempty"`
}

type TypeVariant struct {
	Name   string        `json:"name"`
	Fields []RecordField `json:"fields,omitempty"`
}

type Record struct {
	Name   string        `json:"name"`
	Fields []RecordField `json:"fields"`
}

type RecordField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type IOSConfig struct {
	BundleIdentifier string `json:"bundleIdentifier"`
	DisplayName      string `json:"displayName,omitempty"`
	ServerURL        string `json:"serverUrl"`
}

type PublicConfig struct {
	Dir         string `json:"dir"`
	Mount       string `json:"mount"`
	SPAFallback string `json:"spaFallback,omitempty"`
}

type SystemConfig struct {
	RequestLogsBuffer        int     `json:"requestLogsBuffer"`
	HTTPMaxRequestBodyMB     *int    `json:"httpMaxRequestBodyMb,omitempty"`
	SQLiteJournalMode        *string `json:"sqliteJournalMode,omitempty"`
	SQLiteSynchronous        *string `json:"sqliteSynchronous,omitempty"`
	SQLiteForeignKeys        *bool   `json:"sqliteForeignKeys,omitempty"`
	SQLiteBusyTimeoutMs      *int    `json:"sqliteBusyTimeoutMs,omitempty"`
	SQLiteWALAutoCheckpoint  *int    `json:"sqliteWalAutoCheckpoint,omitempty"`
	SQLiteJournalSizeLimitMB *int    `json:"sqliteJournalSizeLimitMb,omitempty"`
	SQLiteMmapSizeMB         *int    `json:"sqliteMmapSizeMb,omitempty"`
	SQLiteCacheSizeKB        *int    `json:"sqliteCacheSizeKb,omitempty"`
}

type AuthConfig struct {
	UserEntity               string  `json:"userEntity"`
	EmailField               string  `json:"emailField"`
	RoleField                string  `json:"roleField"`
	CodeTTLMinutes           int     `json:"codeTtlMinutes"`
	SessionTTLHours          int     `json:"sessionTtlHours"`
	AuthRequestCodeRateLimit *int    `json:"authRequestCodeRateLimitPerMinute,omitempty"`
	AuthLoginRateLimit       *int    `json:"authLoginRateLimitPerMinute,omitempty"`
	AdminUISessionTTLHours   *int    `json:"adminUiSessionTtlHours,omitempty"`
	SecurityFramePolicy      *string `json:"securityFramePolicy,omitempty"`
	SecurityReferrerPolicy   *string `json:"securityReferrerPolicy,omitempty"`
	SecurityContentNoSniff   *bool   `json:"securityContentTypeNosniff,omitempty"`
	EmailFrom                string  `json:"emailFrom"`
	EmailSubject             string  `json:"emailSubject"`
	SMTPHost                 string  `json:"smtpHost"`
	SMTPPort                 int     `json:"smtpPort"`
	SMTPUsername             string  `json:"smtpUsername"`
	SMTPPasswordEnv          string  `json:"smtpPasswordEnv"`
	SMTPStartTLS             bool    `json:"smtpStartTls"`
}

type Entity struct {
	Name           string             `json:"name"`
	Table          string             `json:"table"`
	Resource       string             `json:"resource"`
	PrimaryKey     string             `json:"primaryKey"`
	Fields         []Field            `json:"fields"`
	Unique         []UniqueConstraint `json:"unique,omitempty"`
	Validate       string             `json:"validate,omitempty"`
	Authorizations []Authorization    `json:"authorizations,omitempty"`
}

type UniqueConstraint struct {
	Fields []string `json:"fields"`
}

type Field struct {
	Name           string   `json:"name"`
	Type           string   `json:"type"`
	EnumValues     []string `json:"enumValues,omitempty"`
	RelationEntity string   `json:"relationEntity,omitempty"`
	CurrentUser    bool     `json:"currentUser,omitempty"`
	Primary        bool     `json:"primary"`
	Auto           bool     `json:"auto"`
	Optional       bool     `json:"optional"`
	Default        any      `json:"default,omitempty"`
}

func FieldStorageName(field *Field) string {
	if field == nil {
		return ""
	}
	if field.RelationEntity != "" {
		return field.Name + "_id"
	}
	return field.Name
}

func IsCreatedAtField(field *Field) bool {
	return field != nil && field.Name == "created_at"
}

func IsUpdatedAtField(field *Field) bool {
	return field != nil && field.Name == "updated_at"
}

func IsAuditTimestampField(field *Field) bool {
	return IsCreatedAtField(field) || IsUpdatedAtField(field)
}

type Authorization struct {
	Action     string `json:"action"`
	Expression string `json:"expression"`
	LineNo     int    `json:"-"`
}

type Query struct {
	Name           string            `json:"name"`
	Parameters     []string          `json:"parameters,omitempty"`
	ParameterTypes map[string]string `json:"parameterTypes,omitempty"`
	Entity         string            `json:"entity"`
	Where          string            `json:"where,omitempty"`
	OrderBy        string            `json:"orderBy,omitempty"`
	OrderDir       string            `json:"orderDir,omitempty"`
	Limit          *int              `json:"limit,omitempty"`
}

type Function struct {
	Name       string   `json:"name"`
	Parameters []string `json:"parameters,omitempty"`
	Expression string   `json:"expression"`
	LineNo     int      `json:"-"`
}

type TypeAlias struct {
	Name   string       `json:"name"`
	Fields []AliasField `json:"fields"`
}

type AliasField struct {
	Name           string   `json:"name"`
	Type           string   `json:"type"`
	EnumValues     []string `json:"enumValues,omitempty"`
	RelationEntity string   `json:"relationEntity,omitempty"`
}

type Action struct {
	Name       string       `json:"name"`
	InputAlias string       `json:"inputAlias"`
	Steps      []ActionStep `json:"steps"`
}

func PublicActionName(name string) string {
	return strings.ReplaceAll(strings.TrimSpace(name), "_", "-")
}

func PublicActionPath(name string) string {
	return "/actions/" + PublicActionName(name)
}

func PublicQueryName(name string) string {
	return strings.ReplaceAll(strings.TrimSpace(name), "_", "-")
}

func PublicQueryPath(name string) string {
	return "/queries/" + PublicQueryName(name)
}

type ActionStep struct {
	Alias  string            `json:"alias,omitempty"`
	Kind   string            `json:"kind"`
	Entity string            `json:"entity,omitempty"`
	Values []ActionFieldExpr `json:"values,omitempty"`
}

type ActionFieldExpr struct {
	Field      string `json:"field"`
	Expression string `json:"expression"`
}

type Frontend struct {
	Screens []FrontendScreen `json:"screens"`
}

type FrontendScreen struct {
	Name            string                `json:"name"`
	Parameters      []string              `json:"parameters,omitempty"`
	Title           string                `json:"title,omitempty"`
	TitleExpression string                `json:"titleExpression,omitempty"`
	View            *FrontendViewNode     `json:"view,omitempty"`
	Messages        []FrontendMessage     `json:"messages,omitempty"`
	InitExpression  string                `json:"initExpression,omitempty"`
	UpdateMessage   string                `json:"updateMessage,omitempty"`
	UpdateModel     string                `json:"updateModel,omitempty"`
	UpdateBody      string                `json:"updateBody,omitempty"`
	ViewModel       string                `json:"viewModel,omitempty"`
	ViewBody        string                `json:"viewBody,omitempty"`
	ToolbarItems    []FrontendToolbarItem `json:"toolbarItems,omitempty"`
	Sections        []FrontendSection     `json:"sections,omitempty"`
	LineNo          int                   `json:"-"`
	TitleLineNo     int                   `json:"-"`
}

type FrontendMessage struct {
	Name       string   `json:"name"`
	Parameters []string `json:"parameters,omitempty"`
}

type FrontendViewNode struct {
	Kind     string             `json:"kind"`
	Title    string             `json:"title,omitempty"`
	Text     string             `json:"text,omitempty"`
	Label    string             `json:"label,omitempty"`
	Message  string             `json:"message,omitempty"`
	Children []FrontendViewNode `json:"children,omitempty"`
}

type FrontendToolbarItem struct {
	Placement string       `json:"placement"`
	Item      FrontendItem `json:"item"`
	LineNo    int          `json:"-"`
}

type FrontendSection struct {
	Title  string         `json:"title,omitempty"`
	Items  []FrontendItem `json:"items,omitempty"`
	LineNo int            `json:"-"`
}

type FrontendItem struct {
	Kind          string                 `json:"kind"`
	Condition     string                 `json:"condition,omitempty"`
	Label         string                 `json:"label,omitempty"`
	Text          string                 `json:"text,omitempty"`
	Message       string                 `json:"message,omitempty"`
	ModelField    string                 `json:"modelField,omitempty"`
	Disabled      string                 `json:"disabled,omitempty"`
	Options       []FrontendOption       `json:"options,omitempty"`
	Target        string                 `json:"target,omitempty"`
	Entity        string                 `json:"entity,omitempty"`
	RelationField string                 `json:"relationField,omitempty"`
	Filter        string                 `json:"filter,omitempty"`
	TitleField    string                 `json:"titleField,omitempty"`
	SubtitleField string                 `json:"subtitleField,omitempty"`
	Destination   string                 `json:"destination,omitempty"`
	Action        string                 `json:"action,omitempty"`
	ReportGroup   string                 `json:"reportGroup,omitempty"`
	ReportMetrics []FrontendReportMetric `json:"reportMetrics,omitempty"`
	Values        []FrontendActionValue  `json:"values,omitempty"`
	FormFields    []FrontendFormField    `json:"formFields,omitempty"`
	LineNo        int                    `json:"-"`
	FilterLineNo  int                    `json:"-"`
}

type FrontendOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type FrontendReportMetric struct {
	Aggregate string `json:"aggregate"`
	Field     string `json:"field,omitempty"`
	Label     string `json:"label,omitempty"`
	LineNo    int    `json:"-"`
}

type FrontendActionValue struct {
	Field      string `json:"field"`
	Expression string `json:"expression"`
	LineNo     int    `json:"-"`
}

type FrontendFormField struct {
	Field        string `json:"field"`
	Filter       string `json:"filter,omitempty"`
	LineNo       int    `json:"-"`
	FilterLineNo int    `json:"-"`
}
