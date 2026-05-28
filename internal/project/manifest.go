package project

import (
	"crypto/rand"
	"encoding/json"
	"errors"
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

// RateLimitConfig is the `rateLimit` block in mar.json. Tunes the
// per-IP token-bucket rate limiter that wraps /api/*, /services/*,
// /_mar/admin/api/*, /_auth/whoami, /_auth/logout. Rate limit is
// always on — no opt-out — but the values can stretch from very
// strict to very permissive within hard bounds.
//
// Defaults (when block absent OR field zero):
//
//	requestsPerMinute = 600 (= 10 req/s sustained)
//	burst             = 30
//
// The /_auth/request-code endpoint keeps its own stricter limit
// (3/h/email + 20/h/IP) regardless of this block — it triggers
// SMTP sends which are expensive and the standard email-flood
// vector.
type RateLimitConfig struct {
	// RequestsPerMinute is the sustained per-IP rate. e.g. 600
	// means the bucket refills 10 tokens per second. Must be in
	// [1, 100000].
	RequestsPerMinute int `json:"requestsPerMinute,omitempty"`

	// Burst is the bucket capacity — both the initial state for
	// first-seen IPs and the max ceiling during refill. Must be
	// in [1, 10000].
	Burst int `json:"burst,omitempty"`
}

// ResolvedRequestsPerMinute returns the effective rate, applying
// the documented default when the field is absent / zero.
func (r *RateLimitConfig) ResolvedRequestsPerMinute() int {
	if r == nil || r.RequestsPerMinute == 0 {
		return 600
	}
	return r.RequestsPerMinute
}

// ResolvedBurst returns the effective burst capacity.
func (r *RateLimitConfig) ResolvedBurst() int {
	if r == nil || r.Burst == 0 {
		return 30
	}
	return r.Burst
}

// IOSConfig holds settings that apply when building the iOS scaffold
// (`mar build --target ios`). All fields except ServerURL are
// required: `mar build --target ios` fails with a paste-ready
// suggestion when any is missing. The strict-by-default stance
// prevents silently shipping a bundle with placeholder identity to
// the App Store.
type IOSConfig struct {
	// BundleID is the iOS bundle identifier (reverse-DNS). Required.
	// Apple requires a globally-unique bundle ID registered against
	// your developer account; the build refuses to invent one.
	// Goes into the .pbxproj as PRODUCT_BUNDLE_IDENTIFIER.
	BundleID string `json:"bundleId,omitempty"`

	// DisplayName is the human-readable name shown on the home
	// screen and in the iOS app switcher. Required. Allowed to
	// contain spaces / accents — unlike the project's internal
	// SwiftIdentifier name. Goes into Info.plist as
	// CFBundleDisplayName.
	DisplayName string `json:"displayName,omitempty"`

	// MarketingVersion is the user-facing version string
	// (e.g. "1.2.3"). Required. Shown on the App Store and on the
	// Settings → app entry. Goes into the .pbxproj as
	// MARKETING_VERSION.
	MarketingVersion string `json:"marketingVersion,omitempty"`

	// BuildNumber is the internal build counter, monotonically
	// increasing per upload to TestFlight / App Store. Required.
	// Strings are accepted (Apple stores it as a string) — common
	// patterns are "1", "42", or a CI counter. Goes into the
	// .pbxproj as CURRENT_PROJECT_VERSION.
	BuildNumber string `json:"buildNumber,omitempty"`

	// ServerURL is the production backend URL the iOS app talks to
	// in RELEASE builds. Optional: when absent, `mar build` warns
	// but still produces a bundle (useful for dev / sideload
	// against a local `mar dev` discovered via Bonjour). Must be
	// HTTPS — App Store transport security rejects plain HTTP
	// without ATS exceptions, which we don't grant. Goes into
	// Info.plist as MarBaseURL.
	//
	// In DEBUG (Xcode debug-build), Bonjour discovery on the local
	// network supersedes this when a `_mar._tcp` service is found.
	// In RELEASE (TestFlight / App Store), Bonjour is compiled out;
	// the app always talks to ServerURL.
	ServerURL string `json:"serverUrl,omitempty"`
}

// Manifest is the parsed contents of mar.json.
//
// Fields are intentionally narrow. Unknown fields produce errors
// (strict schema). Sensitive fields (e.g. passwords) require the
// env: prefix.
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
	IOS        *IOSConfig        `json:"ios,omitempty"`
	RateLimit  *RateLimitConfig  `json:"rateLimit,omitempty"`
	Admins     []string          `json:"admins,omitempty"`
	AdminPanel *AdminPanelConfig `json:"adminPanel,omitempty"`
	Deploy     *DeployConfig     `json:"deploy,omitempty"`
}

// DeployConfig is the manifest's `deploy` block — per-target deploy
// configuration. Each field is a sibling for a specific provider
// (Fly.io for fullstack VMs, Cloudflare Pages for static bundles).
// Future targets (`aws`, `render`, `github-pages`, etc.) would be
// sibling fields. The block is optional — projects without it can\'t
// use `mar X deploy` commands, but `mar dev` / `mar build` work fine.
//
// Conventional pairing today:
//
//   - App.fullstack → deploy.fly      (VM + SQLite + auth)
//   - App.frontend  → deploy.cloudflare-pages  (static CDN)
//
// A project can declare both blocks; the dispatch is by the
// subcommand the operator runs, not by topology.
type DeployConfig struct {
	Fly             *FlyDeployConfig             `json:"fly,omitempty"`
	CloudflarePages *CloudflarePagesDeployConfig `json:"cloudflare-pages,omitempty"`
}

// FlyDeployConfig holds everything `mar fly deploy` needs to push
// the project to Fly.io. The block is required for any `mar fly *`
// subcommand to work; all three fields are required (validated in
// validate.go) — none have defaults because each represents a real
// decision the operator must make consciously:
//
//   - App pins the global Fly identity (becomes <app>.fly.dev)
//   - Region affects latency for the operator\'s users
//   - Memory affects the monthly bill directly
//
// Customizing the generated Dockerfile / fly.toml is deliberately
// not exposed. Docker is treated as an implementation detail; if
// the framework doesn\'t provide what an app needs, that\'s a
// feature request, not a configuration knob.
type FlyDeployConfig struct {
	// App is the Fly.io app name (globally unique on Fly).
	// Becomes the hostname: <app>.fly.dev.
	App string `json:"app,omitempty"`

	// Region is the primary Fly region code (e.g. "gru", "iad",
	// "fra"). See https://fly.io/docs/reference/regions for the
	// full list. Single region for now — multi-region is out of
	// scope until someone needs it.
	Region string `json:"region,omitempty"`

	// Memory is the VM memory size, expressed as a Fly-accepted
	// string ("256mb", "512mb", "1gb", "2gb", "4gb", "8gb").
	// Required + validated against the allowed set. Pin this
	// consciously: memory directly affects the monthly bill.
	Memory string `json:"memory,omitempty"`
}

// CloudflarePagesDeployConfig holds everything `mar cloudflare-pages
// deploy` needs to push a static App.frontend bundle to Cloudflare
// Pages. All three fields are required (validated in validate.go) —
// none have defaults because each pins a real choice the operator
// makes:
//
//   - App ties the deployment to a name in the operator's CF
//     account (becomes <app>.pages.dev).
//   - Account ties it to a specific Cloudflare account.
//   - APIToken is the credential used to upload. Always env:VAR —
//     literal values are rejected at compile-time (same rule as
//     auth.sessionSecret and mail.smtpPassword).
//
// `app` deliberately mirrors the field name in deploy.fly so the
// shape of every deploy block reads the same way. The provider's
// dashboard may call this a "project" (CF) or "app" (Fly), but
// inside mar.json the operator sees one consistent identifier
// field across providers.
//
// Why not a CLI customization for headers / build hooks / etc.: same
// philosophy as deploy.fly — the framework picks the right defaults
// and that's the contract. If an app needs something the defaults
// don't give, that's a feature request.
type CloudflarePagesDeployConfig struct {
	// App is the Cloudflare Pages project name. Becomes the
	// hostname: <app>.pages.dev. Auto-created on the first
	// `mar cloudflare-pages deploy` (with operator confirmation
	// in interactive mode) if it doesn\'t already exist on the
	// account — no manual dashboard setup needed.
	//
	// Accepts an env:VAR reference for operators who want it
	// resolved from the environment instead of committed.
	App string `json:"app,omitempty"`

	// Account is the Cloudflare account ID (a 32-char hex string
	// visible in the dashboard URL or via the API). Not a secret
	// (it's an identifier, not a credential), but accepts env:VAR
	// for operators who prefer keeping it out of git.
	Account string `json:"account,omitempty"`

	// APIToken is the Cloudflare API token used to authenticate
	// uploads and project management calls. ALWAYS env:VAR —
	// a literal value is rejected at compile-time by checkSecrets,
	// since committing a token would be a credential leak.
	//
	// Required permission on the token: Account.Cloudflare Pages:
	// Edit (created at https://dash.cloudflare.com/profile/api-tokens).
	APIToken string `json:"apiToken,omitempty"` // must be env:VAR
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

	// MaxBodyBytes caps the size of incoming request bodies on /api/*
	// and /services/* routes. Default 1 MiB. Bounded [1 KiB, 32 MiB].
	// Zero means "use default" (consistent with the other bounded
	// knobs); there's no opt-out — a missing cap is a DoS vector and
	// the policy is "seguro por default" across the framework.
	//
	// 32 MiB upper bound matches what most JSON APIs need; bigger
	// payloads (file uploads, video chunks) belong on a dedicated
	// streaming route — a future feature, not a knob to bypass this
	// cap. /_auth/* and /_mar/admin/* have their own tighter caps in
	// their handlers and aren't affected by this value.
	MaxBodyBytes int64 `json:"maxBodyBytes,omitempty"`
}

// ResolvedMaxBodyBytes returns the effective per-request body cap.
// Nil receiver / zero field falls back to the default. The validator
// guarantees the value is in bounds at this point.
func (s *ServerConfig) ResolvedMaxBodyBytes() int64 {
	if s == nil || s.MaxBodyBytes == 0 {
		return DefaultMaxBodyBytes
	}
	return s.MaxBodyBytes
}

type DatabaseConfig struct {
	Path       string              `json:"path,omitempty"`
	AutoBackup *DatabaseAutoBackup `json:"autoBackup,omitempty"`
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
	return loadManifestInternal(root, false, false)
}

// LoadManifest reads and parses mar.json under root, resolving env var
// references (env:NAME prefix) to actual environment values. Missing
// env vars are a hard error — production runtime startup, fly deploy
// validation, etc. all go through this path.
//
// Returns nil, nil if no mar.json exists (treated as empty).
func LoadManifest(root string) (*Manifest, error) {
	return loadManifestInternal(root, true, false)
}

// LoadManifestDev mirrors LoadManifest but tolerates missing env
// vars: `env:VAR` references resolve to "" instead of erroring out.
// `mar dev` uses this so the operator can run a project locally
// without configuring production secrets (SMTP_PASSWORD, etc.). The
// downstream auth path detects empty SMTP and falls back to its
// stdout sink — auth codes print to the terminal where the dev
// server is already running.
//
// Strict callers (mar-runtime, mar fly *) keep using LoadManifest:
// shipping production without the secrets configured is a much
// worse failure mode than `mar dev` refusing to start.
func LoadManifestDev(root string) (*Manifest, error) {
	return loadManifestInternal(root, true, true)
}

// loadManifestInternal is the shared parser. `resolveEnv` controls
// whether env:VAR references are substituted with their runtime
// values — true for runtime callers (mar dev, mar-runtime), false
// for build-time validators that don't have prod env vars.
// `tolerateMissingEnv` softens the failure mode: when true, an
// `env:VAR` that resolves to nothing leaves the field as the empty
// string instead of erroring out — see LoadManifestDev's doc for
// why dev wants this.
func loadManifestInternal(root string, resolveEnv bool, tolerateMissingEnv bool) (*Manifest, error) {
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
		return nil, wrapManifestJSONError(path, raw, err)
	}
	// json.Decoder is stream-oriented: it stops after the first
	// complete JSON value and silently ignores anything after.
	// mar.json is supposed to be one root object total — anything
	// trailing (a stray `}`, leftover commented-out config, etc.) is
	// a mistake. Catch it by trying to read another token: io.EOF
	// confirms a clean end; anything else points at the extra data.
	if err := checkTrailingData(path, raw, dec); err != nil {
		return nil, err
	}
	if err := checkUnknownTopFields(probe); err != nil {
		return nil, err
	}

	var m Manifest
	dec = json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, wrapManifestJSONError(path, raw, err)
	}

	// Validate secrets BEFORE resolving env refs, so we see the literal.
	if err := checkSecrets(&m); err != nil {
		return nil, err
	}
	if resolveEnv {
		if err := resolveEnvRefs(&m, tolerateMissingEnv); err != nil {
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
	// Keep this map in lockstep with the JSON tags on the Manifest
	// struct (above). Forgetting an entry here causes a strict-mode
	// rejection ("unknown field X") even when the field decodes
	// cleanly into the Manifest struct itself — the strict probe
	// runs BEFORE the typed decode.
	known := map[string]bool{
		"name":       true,
		"entry":      true,
		"server":     true,
		"database":   true,
		"mail":       true,
		"auth":       true,
		"ios":        true,
		"rateLimit":  true,
		"admins":     true,
		"adminPanel": true,
		"deploy":     true,
	}
	for k := range m {
		if !known[k] {
			return fmt.Errorf("mar.json: unknown field %q", k)
		}
	}
	return nil
}

// resolveEnvRefs walks string fields and replaces "env:VAR" with the
// environment variable's value. When `tolerateMissing` is false
// (production callers), a missing var fails the load. When true
// (mar dev), a missing var resolves the field to the empty string
// — letting `mar dev` boot without prod secrets configured; the
// downstream auth path detects empty SMTP and prints codes to
// stdout instead of trying to send mail.
//
// Decorates *EnvVarNotSetError values with the manifest field path
// so strict-mode CLI consumers can show
// "mail.smtpPassword: env var ... not set" without each call site
// doing the wrapping by hand.
func resolveEnvRefs(m *Manifest, tolerateMissing bool) error {
	resolve := func(field, value string) (string, error) {
		out, err := resolveStr(value)
		if err == nil {
			return out, nil
		}
		if tolerateMissing {
			var notSet *EnvVarNotSetError
			if errors.As(err, &notSet) {
				return "", nil
			}
		}
		return "", decorateEnvErr(err, field)
	}

	if m.Mail != nil {
		var err error
		if m.Mail.SMTPPassword, err = resolve("mail.smtpPassword", m.Mail.SMTPPassword); err != nil {
			return err
		}
		if m.Mail.SMTPHost, err = resolve("mail.smtpHost", m.Mail.SMTPHost); err != nil {
			return err
		}
		if m.Mail.SMTPUsername, err = resolve("mail.smtpUsername", m.Mail.SMTPUsername); err != nil {
			return err
		}
	}
	if m.Auth != nil {
		var err error
		if m.Auth.SessionSecret, err = resolve("auth.sessionSecret", m.Auth.SessionSecret); err != nil {
			return err
		}
	}
	// deploy.fly fields: app/region/memory all accept env:VAR. None
	// are credentials, so env: is opt-in convenience, not required.
	// Use case: one mar.json that maps to different Fly apps /
	// regions / sizes via the operator's env (dev vs prod, etc.).
	if m.Deploy != nil && m.Deploy.Fly != nil {
		var err error
		f := m.Deploy.Fly
		if f.App, err = resolve("deploy.fly.app", f.App); err != nil {
			return err
		}
		if f.Region, err = resolve("deploy.fly.region", f.Region); err != nil {
			return err
		}
		if f.Memory, err = resolve("deploy.fly.memory", f.Memory); err != nil {
			return err
		}
	}
	// deploy.cloudflare-pages fields. apiToken is the only true
	// credential of the three (and checkSecrets enforces env: on
	// it). app/account are identifiers, env: is opt-in.
	if m.Deploy != nil && m.Deploy.CloudflarePages != nil {
		var err error
		cp := m.Deploy.CloudflarePages
		if cp.App, err = resolve("deploy.cloudflare-pages.app", cp.App); err != nil {
			return err
		}
		if cp.Account, err = resolve("deploy.cloudflare-pages.account", cp.Account); err != nil {
			return err
		}
		if cp.APIToken, err = resolve("deploy.cloudflare-pages.apiToken", cp.APIToken); err != nil {
			return err
		}
	}
	return nil
}

// ManifestSyntaxError is returned when mar.json fails to parse as
// JSON. Carries enough context — file path, raw bytes, byte offset,
// derived line/column — for callers to render a rich error message
// with snippet + caret + heuristic hint. Its Error() method already
// returns the full multi-line description (with no colors) so even
// handlers that don't detect the type via errors.As surface useful
// info instead of the cryptic stdlib message.
type ManifestSyntaxError struct {
	// Path is the absolute or relative mar.json path that failed.
	Path string

	// Raw is the file contents at the time of parse. Kept so a
	// presentation-layer printer can re-render with colors.
	Raw []byte

	// Offset is the byte position where the parser stopped, copied
	// from json.SyntaxError / json.UnmarshalTypeError.
	Offset int64

	// Line, Column are 1-based, computed from Offset against Raw.
	Line, Column int

	// Snippet is the affected source line (without trailing newline).
	Snippet string

	// HumanMessage is the stdlib parse error reworded in natural
	// English ("unexpected character X after a key/value pair"
	// instead of "invalid character X after object key:value pair").
	// Preserved as a field rather than computed on demand so the
	// default Error() and a colored printer share the same wording.
	HumanMessage string

	// Hint is a heuristic guess at the underlying cause based on
	// the stdlib error message. May be empty when no rule matches.
	Hint string

	// Underlying is the original json.SyntaxError / .UnmarshalTypeError.
	// Preserved so errors.Is / errors.As can find the stdlib error.
	Underlying error
}

func (e *ManifestSyntaxError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s is not valid JSON — %s\n", e.Path, e.HumanMessage)
	fmt.Fprintf(&b, "  at line %d, column %d:\n", e.Line, e.Column)
	fmt.Fprintf(&b, "    %s\n", e.Snippet)
	// Caret aligned under the offending column. Indent matches the
	// snippet indent (4 spaces) so the marker visually points into
	// the snippet rather than floating to the side.
	caretPad := 4 + e.Column - 1
	if caretPad < 4 {
		caretPad = 4
	}
	fmt.Fprintln(&b, strings.Repeat(" ", caretPad)+"^")
	if e.Hint != "" {
		fmt.Fprintf(&b, "\nHint: %s", e.Hint)
	}
	return b.String()
}

func (e *ManifestSyntaxError) Unwrap() error { return e.Underlying }

// checkTrailingData verifies that the decoder has consumed all of
// raw — i.e. that mar.json doesn't contain stray content after the
// root JSON object. Returns nil on a clean end; otherwise returns a
// ManifestSyntaxError positioned at the first byte of the offending
// trailing content.
//
// The check matters because json.Decoder is stream-oriented: it
// happily stops after the first complete value, leaving callers
// unaware of trailing junk (most commonly an extra `}`). Without
// this check, an obviously-malformed file parses "successfully"
// and the build proceeds with surprising defaults.
func checkTrailingData(path string, raw []byte, dec *json.Decoder) error {
	if _, err := dec.Token(); errors.Is(err, io.EOF) {
		return nil
	}
	// Either Token returned a token (real trailing data) or some
	// other error parsing trailing junk. Either way, point at where
	// the decoder stopped consuming the root value — that's the
	// first byte the user needs to delete or fix.
	offset := dec.InputOffset()
	line, col := lineColFromOffset(raw, offset)
	return &ManifestSyntaxError{
		Path:         path,
		Raw:          raw,
		Offset:       offset,
		Line:         line,
		Column:       col,
		Snippet:      snippetForLine(raw, line),
		HumanMessage: "extra data after the end of the JSON document",
		Hint:         "check for an extra `}` or stray content after the final closing brace.",
		Underlying:   fmt.Errorf("extra data after root JSON value"),
	}
}

// wrapManifestJSONError turns a stdlib json parse error into a
// ManifestSyntaxError with line/column/snippet/hint pre-computed.
// Falls back to a plain error wrap when err isn't a recognized
// parse-position-carrying type (DisallowUnknownFields errors, etc.).
func wrapManifestJSONError(path string, raw []byte, err error) error {
	var offset int64
	switch je := err.(type) {
	case *json.SyntaxError:
		offset = je.Offset
	case *json.UnmarshalTypeError:
		offset = je.Offset
	default:
		// No position info — best we can do is prefix with the path.
		return fmt.Errorf("%s: %v", path, err)
	}
	line, col := lineColFromOffset(raw, offset)
	return &ManifestSyntaxError{
		Path:         path,
		Raw:          raw,
		Offset:       offset,
		Line:         line,
		Column:       col,
		Snippet:      snippetForLine(raw, line),
		HumanMessage: humanizeJSONError(err.Error()),
		Hint:         hintForJSONError(err.Error()),
		Underlying:   err,
	}
}

// lineColFromOffset maps a byte offset back to a 1-based line and
// column position. Bytes (not runes) — matches what json.SyntaxError
// reports and keeps the math straightforward; for the kinds of
// punctuation errors that fire syntax errors, multi-byte characters
// are never the culprit.
func lineColFromOffset(raw []byte, offset int64) (line, col int) {
	line = 1
	col = 1
	for i := int64(0); i < offset && i < int64(len(raw)); i++ {
		if raw[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return
}

// snippetForLine returns the raw text of the 1-based line, without
// the trailing newline. Empty string when line is out of range.
func snippetForLine(raw []byte, line int) string {
	if line < 1 {
		return ""
	}
	lines := strings.Split(string(raw), "\n")
	if line > len(lines) {
		return ""
	}
	return lines[line-1]
}

// hintForJSONError translates the cryptic stdlib parse message into
// a one-line human guess at what's wrong. Covers the handful of
// errors that account for ~all real-world mar.json mistakes; falls
// through to an empty string when nothing matches, so we don't
// invent advice for an unfamiliar case.
func hintForJSONError(msg string) string {
	switch {
	case strings.Contains(msg, "after object key:value pair"):
		return "a comma is probably missing between two key/value pairs."
	case strings.Contains(msg, "looking for beginning of object key string"):
		return "check for a stray trailing comma before }, or a missing quoted key."
	case strings.Contains(msg, "unexpected end of JSON input"):
		return "a quote, brace, or bracket is probably unclosed."
	case strings.Contains(msg, "looking for beginning of value"):
		return "expected a value here (string, number, true/false/null, object, or array)."
	case strings.Contains(msg, "invalid character"):
		return "check for stray punctuation or an unquoted string."
	}
	return ""
}

// humanizeJSONError rephrases the stdlib parse message in natural
// English: replaces parser-state jargon ("after object key:value
// pair", "looking for beginning of …") with phrasing a non-Go user
// can read, while preserving the offending character info that the
// stdlib message embeds (e.g. `'"'`, `'}'`).
//
// Falls through to the raw stdlib message when nothing matches —
// safer to show something verbatim than to silently drop info.
func humanizeJSONError(msg string) string {
	char := extractInvalidChar(msg)
	switch {
	case strings.Contains(msg, "after object key:value pair"):
		return joinChar("unexpected character", char, "after a key/value pair")
	case strings.Contains(msg, "looking for beginning of object key string"):
		return joinChar("unexpected character", char, "where a key (a quoted string) was expected")
	case strings.Contains(msg, "unexpected end of JSON input"):
		return "the file ends mid-structure (a quote, brace, or bracket is unclosed)"
	case strings.Contains(msg, "looking for beginning of value"):
		return joinChar("unexpected character", char, "where a value was expected")
	case strings.HasPrefix(msg, "invalid character"):
		return joinChar("unexpected character", char, "")
	}
	return msg
}

// extractInvalidChar pulls the leading "'X'" token out of a stdlib
// json message like `invalid character '"' after …`. Returns the
// quoted form (with the quotes) so callers can splice it into a
// sentence verbatim. Empty string when the message doesn't start
// with that idiom.
func extractInvalidChar(msg string) string {
	const prefix = "invalid character "
	if !strings.HasPrefix(msg, prefix) {
		return ""
	}
	rest := msg[len(prefix):]
	if len(rest) < 2 || rest[0] != '\'' {
		return ""
	}
	if end := strings.IndexByte(rest[1:], '\''); end >= 0 {
		return rest[:end+2]
	}
	return ""
}

// joinChar splices an optional character token into a sentence, so
// callers don't have to repeat the "if-char-then-with-it" shape.
//
//	joinChar("foo", "'\"'", "bar") -> "foo '\"' bar"
//	joinChar("foo", "",      "bar") -> "foo bar"
//	joinChar("foo", "'\"'", "")    -> "foo '\"'"
func joinChar(prefix, char, suffix string) string {
	out := prefix
	if char != "" {
		out += " " + char
	}
	if suffix != "" {
		out += " " + suffix
	}
	return out
}

// FreeMailDomainError is returned by validateMail when the
// `mail.from` address uses a domain Mar recognizes as free-mail
// (gmail.com, outlook.com, etc). The CLI catches this specifically
// to render an "Error + Hint" block explaining why provider SMTP
// always rejects these — users on free-mail-as-from-address are
// universally misconfigured, never intentional.
type FreeMailDomainError struct {
	From   string // e.g. "support@gmail.com"
	Domain string // e.g. "gmail.com"
}

func (e *FreeMailDomainError) Error() string {
	return fmt.Sprintf(
		"mar.json: mail.from %q uses a free-mail domain (%s); SMTP providers "+
			"reject sends from domains you haven't verified with them",
		e.From, e.Domain)
}

// EnvVarNotSetError is returned by manifest loading when a `env:VAR`
// reference can't be resolved because VAR isn't in the process
// environment. CLI callers (cmd/mar) catch this specifically to
// render a friendly "Error + Hint" block pointing the user at the
// right `mar fly provision` workflow; library callers fall back to
// .Error() for a plain-text rendering.
type EnvVarNotSetError struct {
	// Field is the manifest path that referenced the env var, e.g.
	// "mail.smtpPassword" or "auth.sessionSecret". Wrapped in by the
	// resolveEnvRefs caller (resolveStr itself doesn't know which
	// field it's resolving).
	Field string

	// VarName is the unresolved variable name (without the `env:`
	// prefix), e.g. "SMTP_PASSWORD".
	VarName string
}

func (e *EnvVarNotSetError) Error() string {
	if e.Field == "" {
		return fmt.Sprintf("env var %q is not set", e.VarName)
	}
	return fmt.Sprintf("%s: env var %q is not set", e.Field, e.VarName)
}

// PlaceholderError is returned by validateMail when a mar.json field
// still has the literal "..." placeholder from the suggestion snippet
// (mar prints sample config blocks the user is meant to paste +
// customize). The CLI catches this specifically to render a tailored
// Hint pointing at the field, since this is by far the most common
// "I copied the snippet and forgot to edit it" failure mode.
type PlaceholderError struct {
	// Field is the manifest path of the placeholder field, e.g.
	// "mail.smtpHost" or "mail.from".
	Field string
}

func (e *PlaceholderError) Error() string {
	return fmt.Sprintf(
		"mar.json: %s is the placeholder %q — fill in the real value",
		e.Field, "...")
}

// decorateEnvErr stamps `field` onto an *EnvVarNotSetError so the
// CLI can render the manifest path along with the var name. Other
// error types pass through unchanged.
func decorateEnvErr(err error, field string) error {
	if e, ok := err.(*EnvVarNotSetError); ok && e.Field == "" {
		e.Field = field
		return e
	}
	return err
}

func resolveStr(s string) (string, error) {
	if !strings.HasPrefix(s, "env:") {
		return s, nil
	}
	name := strings.TrimPrefix(s, "env:")
	v, ok := os.LookupEnv(name)
	if !ok {
		return "", &EnvVarNotSetError{VarName: name}
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
	if m.Deploy != nil && m.Deploy.CloudflarePages != nil && m.Deploy.CloudflarePages.APIToken != "" {
		if !strings.HasPrefix(m.Deploy.CloudflarePages.APIToken, "env:") {
			return fmt.Errorf("mar.json: deploy.cloudflare-pages.apiToken is a secret; use env:VAR_NAME")
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
//     - absolute  → used verbatim
//     - relative  → resolved against the launch cwd (see below)
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
// captured into MAR_LAUNCH_CWD by mar-runtime BEFORE extracting an
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
// should be resolved against. In production self-extracting binaries
// (mar build --target=…), mar-runtime stamps MAR_LAUNCH_CWD with
// the user's cwd before extracting the embedded payload to a temp
// dir; honoring it here keeps relative paths shell-intuitive ("the
// file lands where I ran the binary from", not in /tmp/mar-runtime-*
// which the OS reaps).
//
// Falls back to projectDir when the env var isn't set — that's the
// `mar dev` case (no extraction happens; projectDir IS the user's
// source directory next to mar.json, exactly the right anchor).
func launchCwd(projectDir string) string {
	if v := os.Getenv("MAR_LAUNCH_CWD"); v != "" {
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
