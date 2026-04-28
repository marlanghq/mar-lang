package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Manifest is the parsed contents of mar.json.
//
// Fields are intentionally narrow for the MVP. Unknown fields produce errors
// (strict schema). Sensitive fields (e.g. passwords) require the env: prefix.
type Manifest struct {
	Name  string         `json:"name"`
	Entry string         `json:"entry"`
	Server *ServerConfig  `json:"server,omitempty"`
	Database *DatabaseConfig `json:"database,omitempty"`
	Mail   *MailConfig    `json:"mail,omitempty"`
}

type ServerConfig struct {
	Port      int    `json:"port,omitempty"`
	Host      string `json:"host,omitempty"`
	PublicURL string `json:"publicUrl,omitempty"`
}

type DatabaseConfig struct {
	Path string `json:"path,omitempty"`
}

type MailConfig struct {
	From         string `json:"from,omitempty"`
	SmtpHost     string `json:"smtpHost,omitempty"`
	SmtpPort     int    `json:"smtpPort,omitempty"`
	SmtpUsername string `json:"smtpUsername,omitempty"`
	SmtpPassword string `json:"smtpPassword,omitempty"` // must be env:VAR
}

// LoadManifest reads and parses mar.json under root, resolving env var
// references (env:NAME prefix) to actual environment values.
//
// Returns nil, nil if no mar.json exists (treated as empty).
func LoadManifest(root string) (*Manifest, error) {
	path := filepath.Join(root, "mar.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	// First decode strictly to catch unknown fields.
	var probe map[string]json.RawMessage
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&probe); err != nil {
		return nil, fmt.Errorf("mar.json: %v", err)
	}
	if err := checkUnknownTopFields(probe); err != nil {
		return nil, err
	}

	var m Manifest
	dec = json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("mar.json: %v", err)
	}

	// Validate secrets BEFORE resolving env refs, so we see the literal.
	if err := checkSecrets(&m); err != nil {
		return nil, err
	}
	if err := resolveEnvRefs(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

func checkUnknownTopFields(m map[string]json.RawMessage) error {
	known := map[string]bool{
		"name":     true,
		"entry":    true,
		"server":   true,
		"database": true,
		"mail":     true,
	}
	for k := range m {
		if !known[k] {
			return fmt.Errorf("mar.json: unknown field %q", k)
		}
	}
	return nil
}

// resolveEnvRefs walks string fields and replaces "env:VAR" with the
// environment variable's value. If the var is missing, leaves the literal
// alone (will be caught later for secret fields).
func resolveEnvRefs(m *Manifest) error {
	if m.Mail != nil {
		s, err := resolveStr(m.Mail.SmtpPassword)
		if err != nil {
			return fmt.Errorf("mail.smtpPassword: %v", err)
		}
		m.Mail.SmtpPassword = s
		s, err = resolveStr(m.Mail.SmtpHost)
		if err != nil {
			return err
		}
		m.Mail.SmtpHost = s
		s, err = resolveStr(m.Mail.SmtpUsername)
		if err != nil {
			return err
		}
		m.Mail.SmtpUsername = s
	}
	return nil
}

func resolveStr(s string) (string, error) {
	if !strings.HasPrefix(s, "env:") {
		return s, nil
	}
	name := strings.TrimPrefix(s, "env:")
	v, ok := os.LookupEnv(name)
	if !ok {
		return "", fmt.Errorf("env var %q is not set", name)
	}
	return v, nil
}

// checkSecrets ensures secret fields don't carry literal values
// (must use env:VAR).
//
// Must be called BEFORE resolveEnvRefs so we see the literal as written.
func checkSecrets(m *Manifest) error {
	if m.Mail != nil && m.Mail.SmtpPassword != "" {
		if !strings.HasPrefix(m.Mail.SmtpPassword, "env:") {
			return fmt.Errorf("mar.json: mail.smtpPassword is a secret; use env:VAR_NAME")
		}
	}
	return nil
}
