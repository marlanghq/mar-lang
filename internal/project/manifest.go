package project

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"mar/internal/auth"
)

// readSecure is a thin alias around crypto/rand.Read so the file's
// dependency surface stays explicit.
func readSecure(buf []byte) (int, error) {
	return io.ReadFull(rand.Reader, buf)
}

// Manifest is the parsed contents of mar.json.
//
// Fields are intentionally narrow. Unknown fields produce errors
// (strict schema). Sensitive fields (e.g. passwords) require the env: prefix.
//
// `entry` is optional — when omitted, mar dev / mar build look for
// `Main.mar` at the project root (the convention). Only set it for
// the unusual case of a non-conventional entry filename.
type Manifest struct {
	Name       string            `json:"name"`
	Entry      string            `json:"entry,omitempty"`
	Server     *ServerConfig     `json:"server,omitempty"`
	Database   *DatabaseConfig   `json:"database,omitempty"`
	Mail       *MailConfig       `json:"mail,omitempty"`
	Auth       *AuthConfig       `json:"auth,omitempty"`
	Admins     []string          `json:"admins,omitempty"`
	AdminPanel *AdminPanelConfig `json:"adminPanel,omitempty"`
}

// AdminPanelConfig is the manifest's `adminPanel` block — knobs for
// the framework-built-in admin panel served at /_mar/admin.
//
// All fields are optional with documented defaults; see
// docs/admin-panel.md §11 for the per-knob defaults and ranges.
type AdminPanelConfig struct {
	// RecentRequestsSize: cap for the in-memory ring buffer powering
	// Mar.Admin.recentRequests. Default 200, range 10–5000.
	RecentRequestsSize int `json:"recentRequestsSize,omitempty"`
}

// AuthConfig is the manifest's `auth` block. The session secret is the
// HMAC key used to derive stored hashes for codes and session tokens —
// production deploys must set it via env var; `mar dev` auto-generates
// a random secret on first run (see ResolveSessionSecret).
type AuthConfig struct {
	SessionSecret string `json:"sessionSecret,omitempty"` // must be env:VAR in production
}

type ServerConfig struct {
	Port      int    `json:"port,omitempty"`
	Host      string `json:"host,omitempty"`
	PublicURL string `json:"publicUrl,omitempty"`
}

type DatabaseConfig struct {
	Path       string                `json:"path,omitempty"`
	AutoBackup *DatabaseAutoBackup   `json:"autoBackup,omitempty"`
}

// DatabaseAutoBackup configures the periodic-backup goroutine that
// writes consistent snapshots into a catalog on the same volume as
// mar.db. Defaults (when the block is absent OR fields are zero):
// enabled=true, intervalHours=6, retentionCount=28 — i.e. a week of
// 4-per-day backups.
//
// Bounds are validated at compile time so misconfigured values fail
// the build instead of silently snapping to defaults.
type DatabaseAutoBackup struct {
	// Enabled toggles the scheduler. *bool to distinguish "not set"
	// (apply default true) from "explicitly false". Same pattern we
	// use elsewhere where the absence of a value matters.
	Enabled *bool `json:"enabled,omitempty"`

	// IntervalHours is how often the goroutine wakes to take a new
	// snapshot. Must be in [1, 168]. 1 = hourly (noisy; prefer
	// streaming replication if you need tighter); 168 = weekly
	// (looser is barely a backup). Default 6.
	IntervalHours int `json:"intervalHours,omitempty"`

	// RetentionCount is how many snapshots to keep in the catalog.
	// Must be in [2, 100]. 1 is a single point of failure on the
	// backup itself; 100+ doesn't fit on typical Fly volumes and
	// should be exported off-machine. Default 28.
	RetentionCount int `json:"retentionCount,omitempty"`
}

// AutoBackupEnabled reports whether automatic backups should run,
// applying the default (true) when not explicitly set.
func (a *DatabaseAutoBackup) AutoBackupEnabled() bool {
	if a == nil || a.Enabled == nil {
		return true
	}
	return *a.Enabled
}

// ResolvedIntervalHours returns the effective interval, applying the
// default (6) when not set.
func (a *DatabaseAutoBackup) ResolvedIntervalHours() int {
	if a == nil || a.IntervalHours == 0 {
		return 6
	}
	return a.IntervalHours
}

// ResolvedRetentionCount returns the effective retention, applying
// the default (28) when not set.
func (a *DatabaseAutoBackup) ResolvedRetentionCount() int {
	if a == nil || a.RetentionCount == 0 {
		return 28
	}
	return a.RetentionCount
}

type MailConfig struct {
	From         string `json:"from,omitempty"`
	SMTPHost     string `json:"smtpHost,omitempty"`
	SMTPPort     int    `json:"smtpPort,omitempty"` // optional, default 587 (see ResolvedSMTPPort)
	SMTPUsername string `json:"smtpUsername,omitempty"`
	SMTPPassword string `json:"smtpPassword,omitempty"` // must be env:VAR
}

// ResolvedSMTPPort returns the port to dial. Defaults to 587, the
// IANA-assigned SMTP submission port with STARTTLS that virtually
// every modern provider supports (Resend, SendGrid, Mailgun, AWS
// SES, Postmark, Brevo, Mailjet, …). Set `smtpPort` explicitly only
// when the provider needs something else — most commonly 465 for
// implicit-TLS-on-connect or 25 for legacy MTA-to-MTA setups.
func (m *MailConfig) ResolvedSMTPPort() int {
	if m == nil || m.SMTPPort == 0 {
		return 587
	}
	return m.SMTPPort
}

// ToSMTPConfig converts the manifest's mail block into the runtime
// SMTPConfig shape consumed by `auth.Send` and the boot-time SMTP
// connectivity check. Returns the zero value when no mail is
// configured — `auth.Send` then falls back to the stdout sink so
// `mar dev` keeps working without any setup.
//
// Defined here (in project) rather than auth so the conversion
// stays in one place; both `mar dev` and `mar-runtime` (production
// stub) call this same helper to avoid drift.
func ToSMTPConfig(m *Manifest) auth.SMTPConfig {
	if m == nil || m.Mail == nil {
		return auth.SMTPConfig{}
	}
	return auth.SMTPConfig{
		Host:     m.Mail.SMTPHost,
		Port:     m.Mail.ResolvedSMTPPort(),
		Username: m.Mail.SMTPUsername,
		Password: m.Mail.SMTPPassword,
	}
}

// LoadManifestStructure reads and parses mar.json under root WITHOUT
// resolving env var references. The returned Manifest carries the
// literal "env:VAR" strings as written, useful for build-time tools
// that need to inspect schema/structure (Are required fields
// present? Are secrets correctly env-prefixed?) but don't have
// access to the runtime env (fly secrets aren't visible at build
// time). Same parse + secret-prefix validation as LoadManifest;
// only the env→value substitution is skipped.
//
// Returns nil, nil when no mar.json exists.
func LoadManifestStructure(root string) (*Manifest, error) {
	return loadManifestInternal(root, false)
}

// LoadManifest reads and parses mar.json under root, resolving env var
// references (env:NAME prefix) to actual environment values.
//
// Returns nil, nil if no mar.json exists (treated as empty).
func LoadManifest(root string) (*Manifest, error) {
	return loadManifestInternal(root, true)
}

// loadManifestInternal is the shared parser. `resolveEnv` controls
// whether env:VAR references are substituted with their runtime
// values — true for runtime callers (mar dev, mar-runtime), false
// for build-time validators that don't have prod env vars.
func loadManifestInternal(root string, resolveEnv bool) (*Manifest, error) {
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
	if resolveEnv {
		if err := resolveEnvRefs(&m); err != nil {
			return nil, err
		}
	}
	// Run schema validation against documented ranges/enums. Phase
	// matches what loadManifestInternal was asked to do: compile-time
	// rules apply to both phases; runtime-only rules (env-resolved
	// secret length, etc) only run when env was resolved.
	phase := CompileTime
	if resolveEnv {
		phase = BootTime
	}
	if err := Validate(&m, phase); err != nil {
		return nil, err
	}
	return &m, nil
}

func checkUnknownTopFields(m map[string]json.RawMessage) error {
	known := map[string]bool{
		"name":       true,
		"entry":      true,
		"server":     true,
		"database":   true,
		"mail":       true,
		"auth":       true,
		"admins":     true,
		"adminPanel": true,
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
		s, err := resolveStr(m.Mail.SMTPPassword)
		if err != nil {
			return fmt.Errorf("mail.smtpPassword: %v", err)
		}
		m.Mail.SMTPPassword = s
		s, err = resolveStr(m.Mail.SMTPHost)
		if err != nil {
			return err
		}
		m.Mail.SMTPHost = s
		s, err = resolveStr(m.Mail.SMTPUsername)
		if err != nil {
			return err
		}
		m.Mail.SMTPUsername = s
	}
	if m.Auth != nil {
		s, err := resolveStr(m.Auth.SessionSecret)
		if err != nil {
			return fmt.Errorf("auth.sessionSecret: %v", err)
		}
		m.Auth.SessionSecret = s
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
	if m.Mail != nil && m.Mail.SMTPPassword != "" {
		if !strings.HasPrefix(m.Mail.SMTPPassword, "env:") {
			return fmt.Errorf("mar.json: mail.smtpPassword is a secret; use env:VAR_NAME")
		}
	}
	if m.Auth != nil && m.Auth.SessionSecret != "" {
		if !strings.HasPrefix(m.Auth.SessionSecret, "env:") {
			return fmt.Errorf("mar.json: auth.sessionSecret is a secret; use env:VAR_NAME")
		}
	}
	return nil
}

// ResolveDatabasePath returns the absolute SQLite file path the runtime
// should open, honoring four layers of intent (highest priority first):
//
//  1. MAR_DATABASE_PATH env var. If set, this wins outright. Production
//     deploys typically point it at a mounted volume (e.g. Fly.io's
//     `/data/app.db`). Relative values are resolved against the launch
//     cwd. Empty value means "explicitly no database".
//
//  2. mar.json `database.path`:
//       - absolute  → used verbatim
//       - relative  → resolved against the launch cwd (see below)
//
//  3. Default `<name>.db` next to mar.json, derived from the project's
//     `name` field. Mirrors how iOS bundle name, Bonjour service name,
//     output binary, etc. all default off the same `name`. Lets the
//     bare-bones mar.json (just `name` + `entry`) cover Repo / Auth.
//
//  4. No name available either → empty string. Repo.* calls error
//     out with a clear message.
//
// The "launch cwd" is the user's working directory at process start —
// captured into MAR_DEV_LAUNCH_CWD by mar-runtime BEFORE extracting an
// embedded payload to a temp dir. Without that capture, projectDir
// would point inside `/tmp/mar-runtime-xxx/` and `./notes.db` would
// land in ephemeral storage. Falls back to projectDir for dev (`mar
// dev`), where no extraction happens and projectDir is the user's
// source directory next to mar.json — exactly what they want.
func ResolveDatabasePath(m *Manifest, projectDir string) (path, source string) {
	if v, ok := os.LookupEnv("MAR_DATABASE_PATH"); ok {
		if v == "" {
			return "", "MAR_DATABASE_PATH (empty — disabled)"
		}
		if filepath.IsAbs(v) {
			return v, "MAR_DATABASE_PATH"
		}
		return filepath.Join(launchCwd(projectDir), v), "MAR_DATABASE_PATH (relative)"
	}
	if m != nil && m.Database != nil && m.Database.Path != "" {
		p := m.Database.Path
		if filepath.IsAbs(p) {
			return p, "mar.json database.path"
		}
		return filepath.Join(launchCwd(projectDir), p), "mar.json database.path (relative)"
	}
	// Default: <name>.db next to mar.json. Same convention as the iOS
	// bundle / Bonjour service / output binary all already follow.
	if m != nil && m.Name != "" {
		return filepath.Join(launchCwd(projectDir), m.Name+".db"), "default <name>.db"
	}
	return "", "unset"
}

// launchCwd returns the directory that relative paths in mar.json
// should be resolved against. In production binaries, mar-runtime
// stamps MAR_DEV_LAUNCH_CWD with the user's cwd before extracting the
// embedded payload to a temp dir; honoring it here keeps relative
// paths shell-intuitive ("the file lands where I ran the binary
// from"). Falls back to projectDir for dev mode.
func launchCwd(projectDir string) string {
	if v := os.Getenv("MAR_DEV_LAUNCH_CWD"); v != "" {
		return v
	}
	return projectDir
}

// ResolveSessionSecret returns the HMAC key the auth runtime should
// use, with this priority:
//
//  1. mar.json auth.sessionSecret (already env-resolved at load time).
//  2. .mar/dev-secrets.json under projectDir — auto-generated on first
//     run, gitignored, so `mar dev` works with zero setup.
//  3. Newly generated secret, written to (2), returned.
//
// Returns the secret + a short source string for diagnostics. An error
// is returned only if step (3) couldn't write the file.
func ResolveSessionSecret(m *Manifest, projectDir string) (string, string, error) {
	if m != nil && m.Auth != nil && m.Auth.SessionSecret != "" {
		return m.Auth.SessionSecret, "mar.json auth.sessionSecret", nil
	}
	devDir := filepath.Join(projectDir, ".mar")
	devFile := filepath.Join(devDir, "dev-secrets.json")
	if data, err := os.ReadFile(devFile); err == nil {
		var stored struct {
			SessionSecret string `json:"sessionSecret"`
		}
		if json.Unmarshal(data, &stored) == nil && stored.SessionSecret != "" {
			return stored.SessionSecret, ".mar/dev-secrets.json", nil
		}
	}
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create .mar dir: %w", err)
	}
	secret, err := randomDevSecret()
	if err != nil {
		return "", "", err
	}
	payload, _ := json.MarshalIndent(map[string]string{
		"sessionSecret": secret,
	}, "", "  ")
	if err := os.WriteFile(devFile, payload, 0o600); err != nil {
		return "", "", fmt.Errorf("write %s: %w", devFile, err)
	}
	// Make sure it's gitignored. Append-only so we don't clobber an
	// existing .gitignore.
	ensureGitignored(filepath.Join(projectDir, ".gitignore"), ".mar/")
	return secret, ".mar/dev-secrets.json (newly generated)", nil
}

func randomDevSecret() (string, error) {
	// 32 random bytes, hex-encoded for readability.
	buf := make([]byte, 32)
	if _, err := readSecure(buf); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	const hex = "0123456789abcdef"
	out := make([]byte, len(buf)*2)
	for i, b := range buf {
		out[i*2] = hex[b>>4]
		out[i*2+1] = hex[b&0x0f]
	}
	return string(out), nil
}

// ensureGitignored appends `entry` to .gitignore if absent. Best-effort
// — we don't want a .gitignore write failure to block dev workflow.
func ensureGitignored(path, entry string) {
	existing, err := os.ReadFile(path)
	if err != nil {
		_ = os.WriteFile(path, []byte(entry+"\n"), 0o644)
		return
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == entry {
			return
		}
	}
	suffix := entry + "\n"
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		suffix = "\n" + suffix
	}
	_ = os.WriteFile(path, append(existing, []byte(suffix)...), 0o644)
}
