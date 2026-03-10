package model

type App struct {
	AppName      string        `json:"appName"`
	Port         int           `json:"port"`
	Database     string        `json:"database"`
	Public       *PublicConfig `json:"public,omitempty"`
	System       *SystemConfig `json:"system,omitempty"`
	Entities     []Entity      `json:"entities"`
	Auth         *AuthConfig   `json:"auth,omitempty"`
	InputAliases []TypeAlias   `json:"inputAliases,omitempty"`
	Actions      []Action      `json:"actions,omitempty"`
}

type PublicConfig struct {
	Dir         string `json:"dir"`
	Mount       string `json:"mount"`
	SPAFallback string `json:"spaFallback,omitempty"`
}

type SystemConfig struct {
	RequestLogsBuffer        int     `json:"requestLogsBuffer"`
	HTTPMaxRequestBodyMB     *int    `json:"httpMaxRequestBodyMb,omitempty"`
	AuthRequestCodeRateLimit *int    `json:"authRequestCodeRateLimitPerMinute,omitempty"`
	AuthLoginRateLimit       *int    `json:"authLoginRateLimitPerMinute,omitempty"`
	AdminUISessionTTLHours   *int    `json:"adminUiSessionTtlHours,omitempty"`
	SecurityFramePolicy      *string `json:"securityFramePolicy,omitempty"`
	SecurityReferrerPolicy   *string `json:"securityReferrerPolicy,omitempty"`
	SecurityContentNoSniff   *bool   `json:"securityContentTypeNosniff,omitempty"`
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
	UserEntity      string `json:"userEntity"`
	EmailField      string `json:"emailField"`
	RoleField       string `json:"roleField"`
	CodeTTLMinutes  int    `json:"codeTtlMinutes"`
	SessionTTLHours int    `json:"sessionTtlHours"`
	EmailTransport  string `json:"emailTransport"`
	EmailFrom       string `json:"emailFrom"`
	EmailSubject    string `json:"emailSubject"`
	SendmailPath    string `json:"sendmailPath"`
	DevExposeCode   bool   `json:"devExposeCode"`
}

type Entity struct {
	Name           string          `json:"name"`
	Table          string          `json:"table"`
	Resource       string          `json:"resource"`
	PrimaryKey     string          `json:"primaryKey"`
	Fields         []Field         `json:"fields"`
	Rules          []Rule          `json:"rules,omitempty"`
	Authorizations []Authorization `json:"authorizations,omitempty"`
}

type Field struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Primary  bool   `json:"primary"`
	Auto     bool   `json:"auto"`
	Optional bool   `json:"optional"`
}

type Rule struct {
	Message    string `json:"message"`
	Expression string `json:"expression"`
}

type Authorization struct {
	Action     string `json:"action"`
	Expression string `json:"expression"`
}

type TypeAlias struct {
	Name   string       `json:"name"`
	Fields []AliasField `json:"fields"`
}

type AliasField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type Action struct {
	Name       string       `json:"name"`
	InputAlias string       `json:"inputAlias"`
	Steps      []ActionStep `json:"steps"`
}

type ActionStep struct {
	Kind   string            `json:"kind"`
	Entity string            `json:"entity"`
	Values []ActionFieldExpr `json:"values"`
}

type ActionFieldExpr struct {
	Field      string `json:"field"`
	SourceKind string `json:"sourceKind"`
	InputField string `json:"inputField,omitempty"`
	Literal    any    `json:"literal,omitempty"`
}
