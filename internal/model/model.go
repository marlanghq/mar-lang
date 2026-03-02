package model

type App struct {
	AppName  string      `json:"appName"`
	Port     int         `json:"port"`
	Database string      `json:"database"`
	Entities []Entity    `json:"entities"`
	Auth     *AuthConfig `json:"auth,omitempty"`
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
