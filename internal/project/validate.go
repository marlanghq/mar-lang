// Manifest validation — see docs/admin-panel.md §11 for the design.
//
// Two-phase: compile-time runs in `mar build` / `mar dev` startup
// against the structural manifest (env:VAR refs unresolved). Boot-time
// runs in `mar-runtime` against the env-resolved manifest, re-applies
// the compile rules, and adds checks that need real values
// (sessionSecret length, SMTP connectivity is elsewhere).
//
// Hard rejection over silent clamping: every range/enum violation
// returns an error naming the field + the violation, so the user can
// fix `mar.json` before the misconfiguration becomes mysterious
// runtime behavior.

package project

import (
	"fmt"
	"image"
	_ "image/png" // register PNG decoder for icon validation
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ValidationPhase distinguishes structural checks (no env resolution
// needed) from runtime checks (env-resolved values, ready to use).
type ValidationPhase int

const (
	// CompileTime fires from `mar build` and `mar dev` startup, against
	// LoadManifestStructure output. Catches typos, range violations,
	// and structural problems before any binary ships.
	CompileTime ValidationPhase = iota

	// BootTime fires from `mar-runtime` against LoadManifest output
	// (env vars resolved). Re-applies CompileTime rules and adds
	// checks that need real resolved values.
	BootTime
)

// Defaults for adminPanel knobs. Documented in docs/admin-panel.md §11.4.
const (
	DefaultRecentRequestsSize = 200
	MinRecentRequestsSize     = 10
	MaxRecentRequestsSize     = 5000
)

// Defaults for rateLimit knobs. Rate limit is always on — these
// bounds let users stretch from very strict (~1 req/min) to very
// permissive (~1667 req/s) but not infinite. Out-of-range values
// fail-fast at compile time, same policy as recentRequestsSize etc.
const (
	DefaultRateLimitRequestsPerMinute = 600
	MinRateLimitRequestsPerMinute     = 1
	MaxRateLimitRequestsPerMinute     = 100000
	DefaultRateLimitBurst             = 30
	MinRateLimitBurst                 = 1
	MaxRateLimitBurst                 = 10000
)

// Defaults for server knobs. Body cap is bounded: a default that's
// "good enough for typical JSON APIs", with room to stretch up for
// uploads but never "unlimited" — uncapped bodies are a classic DoS
// vector (one client sending Content-Length: 10GB exhausts memory
// before any handler runs).
const (
	DefaultMaxBodyBytes int64 = 1 << 20  // 1 MiB
	MinMaxBodyBytes     int64 = 1 << 10  // 1 KiB — anything smaller is unusable
	MaxMaxBodyBytes     int64 = 32 << 20 // 32 MiB — bigger payloads need a dedicated streaming route
)

// Defaults for database.autoBackup knobs. Bounds rationale documented
// in docs/admin-panel.md §11 (mirrors recentRequestsSize policy:
// hard rejection over silent clamping).
const (
	DefaultAutoBackupIntervalHours  = 6
	MinAutoBackupIntervalHours      = 1   // tighter than 1h → use streaming replication
	MaxAutoBackupIntervalHours      = 168 // 1 week — looser is barely a backup
	DefaultAutoBackupRetentionCount = 28
	MinAutoBackupRetentionCount     = 2   // 1 = single point of failure
	MaxAutoBackupRetentionCount     = 100 // larger → export off-machine
)

// emailRegex is a lightweight shape check — not full RFC 5322. We
// just want to reject obvious garbage in `admins: [...]` at compile
// time, not validate that the address actually exists.
var emailRegex = regexp.MustCompile(`^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}$`)

// IsValidEmail reports whether s matches the framework's email
// shape (the same one used to validate `admins:` and `mail.from`
// at compile time). Exposed for runtime handlers that take an email
// over the wire — currently /_auth/request-code, which would
// otherwise accept "not-an-email" and pollute the users table.
//
// Reject is shape-only: an unreachable domain like `x@invalid.tld`
// passes here. The SMTP send step is what actually proves
// deliverability — this just blocks the garbage that would never
// have a chance.
func IsValidEmail(s string) bool {
	return emailRegex.MatchString(s)
}

// httpsURLRegex matches https://host[:port][/path]. Used to validate
// ios.serverUrl. The check is shape-only — DNS resolution and
// reachability are not our concern here.
var httpsURLRegex = regexp.MustCompile(`^https://[A-Za-z0-9.\-]+(:[0-9]+)?(/.*)?$`)

// localhostURLRegex matches http://localhost[:port][/path] or http://
// 127.0.0.1[:port]... — accepted as ios.serverUrl ONLY when the
// caller-context allows it (e.g. local QA build). The validator
// rejects plain http for production targets per ATS / App Store
// review reality.
var localhostURLRegex = regexp.MustCompile(`^http://(localhost|127\.0\.0\.1)(:[0-9]+)?(/.*)?$`)

// Validate checks the manifest against the rules defined in
// docs/admin-panel.md §11. Returns the first violation as a
// human-readable error with the field path included.
//
// Idempotent. Safe to call from multiple phases — each rule decides
// whether it applies to the current phase.
func Validate(m *Manifest, phase ValidationPhase) error {
	if m == nil {
		return nil
	}
	if err := validateAdmins(m, phase); err != nil {
		return err
	}
	if err := validateAdminPanel(m, phase); err != nil {
		return err
	}
	if err := validateDatabaseAutoBackup(m, phase); err != nil {
		return err
	}
	if err := validateIOS(m, phase); err != nil {
		return err
	}
	if err := validateMail(m, phase); err != nil {
		return err
	}
	if err := validateRateLimit(m, phase); err != nil {
		return err
	}
	if err := validateServer(m, phase); err != nil {
		return err
	}
	if err := validatePWA(m, phase); err != nil {
		return err
	}
	return nil
}

var hexColorRe = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// validatePWA checks the structural parts of the `pwa` block — the
// colors must be valid hex. The icon FILE check (PNG / square / size)
// lives in ValidatePWAIcon, which needs the project directory to
// resolve the path and so runs from the CLI (dev + build), not from
// this JSON-only validation.
func validatePWA(m *Manifest, phase ValidationPhase) error {
	if m.PWA == nil {
		return nil
	}
	for _, c := range []struct{ field, val string }{
		{"themeColor", m.PWA.ThemeColor},
		{"backgroundColor", m.PWA.BackgroundColor},
	} {
		if c.val != "" && !hexColorRe.MatchString(c.val) {
			return fmt.Errorf("mar.json: pwa.%s must be a hex color like \"#0071e3\" (got %q)", c.field, c.val)
		}
	}
	_ = phase
	return nil
}

// PWA icon requirements. 512 is the largest size the manifest
// generates; below it the icon would be upscaled and look blurry.
const minPWAIconSize = 512

// ValidatePWAIcon checks the `pwa.icon` master image when one is set:
// it must be a square PNG of at least 512×512 (Mar downscales it to
// every size the manifest + apple-touch-icon need). Reads only the
// image header (image.DecodeConfig) — no full decode. A nil PWA block
// or empty icon path is fine (a tile is generated instead). Called from
// `mar dev` (boot) and `mar build` so a bad icon fails fast.
func ValidatePWAIcon(projectDir string, m *Manifest) error {
	if m == nil || m.PWA == nil || m.PWA.Icon == "" {
		return nil
	}
	path := m.PWA.Icon
	if !filepath.IsAbs(path) {
		path = filepath.Join(projectDir, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("mar.json: pwa.icon %s could not be opened: %w", m.PWA.Icon, err)
	}
	defer f.Close()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil || format != "png" {
		return fmt.Errorf("mar.json: pwa.icon %s must be a PNG\n"+
			"      Hint: export a square PNG at least %d×%d; Mar generates every size from it.",
			m.PWA.Icon, minPWAIconSize, minPWAIconSize)
	}
	if cfg.Width != cfg.Height {
		return fmt.Errorf("mar.json: pwa.icon %s is %d×%d but must be square\n"+
			"      Hint: app icons go in square/round slots; a non-square source gets distorted.",
			m.PWA.Icon, cfg.Width, cfg.Height)
	}
	if cfg.Width < minPWAIconSize {
		return fmt.Errorf("mar.json: pwa.icon %s is %d×%d but must be at least %d×%d\n"+
			"      Hint: export a 1024×1024 PNG — Mar downscales it; upscaling a smaller one looks blurry.",
			m.PWA.Icon, cfg.Width, cfg.Height, minPWAIconSize, minPWAIconSize)
	}
	return nil
}

// validateServer enforces bounds on the `server` block. Currently
// just maxBodyBytes — port/host validation lives elsewhere because
// those are network-shape concerns, not policy knobs.
func validateServer(m *Manifest, phase ValidationPhase) error {
	if m.Server == nil {
		return nil
	}
	if m.Server.MaxBodyBytes != 0 {
		if m.Server.MaxBodyBytes < MinMaxBodyBytes || m.Server.MaxBodyBytes > MaxMaxBodyBytes {
			return fmt.Errorf(
				"mar.json: server.maxBodyBytes must be between %d and %d (got %d)",
				MinMaxBodyBytes, MaxMaxBodyBytes, m.Server.MaxBodyBytes,
			)
		}
	}
	_ = phase
	return nil
}

// validateRateLimit enforces bounds on rateLimit knobs. Absent
// block / zero fields apply documented defaults. Hard rejection
// outside bounds — rate limit can't be effectively disabled (the
// upper bound is high enough for legitimate high-throughput
// apps but not infinite, by design).
func validateRateLimit(m *Manifest, phase ValidationPhase) error {
	if m.RateLimit == nil {
		return nil
	}
	r := m.RateLimit
	if r.RequestsPerMinute != 0 {
		if r.RequestsPerMinute < MinRateLimitRequestsPerMinute || r.RequestsPerMinute > MaxRateLimitRequestsPerMinute {
			return fmt.Errorf(
				"mar.json: rateLimit.requestsPerMinute must be between %d and %d (got %d)",
				MinRateLimitRequestsPerMinute, MaxRateLimitRequestsPerMinute, r.RequestsPerMinute,
			)
		}
	}
	if r.Burst != 0 {
		if r.Burst < MinRateLimitBurst || r.Burst > MaxRateLimitBurst {
			return fmt.Errorf(
				"mar.json: rateLimit.burst must be between %d and %d (got %d)",
				MinRateLimitBurst, MaxRateLimitBurst, r.Burst,
			)
		}
	}
	_ = phase
	return nil
}

// extractEmailDomain pulls the domain part out of either a bare
// email ("x@y.com" → "y.com") or a display-name email
// ("Foo <x@y.com>" → "y.com"). Returns "" when the shape doesn't
// match either form.
func extractEmailDomain(addr string) string {
	// Display-name form: take the last "<...>" group's contents.
	if i := strings.LastIndex(addr, "<"); i >= 0 {
		if j := strings.Index(addr[i+1:], ">"); j >= 0 {
			addr = addr[i+1 : i+1+j]
		}
	}
	at := strings.LastIndex(addr, "@")
	if at < 0 || at == len(addr)-1 {
		return ""
	}
	return addr[at+1:]
}

// freeMailDomains is the set of domains we treat as "free mail
// providers". Using one of these in mail.from is always wrong —
// SMTP providers (Resend, SendGrid, AWS SES, Postmark, …) only
// let you send from domains you've verified via DKIM/SPF, and you
// can never verify a domain you don't own.
//
// Curated, not exhaustive — covers the ~95% of common mistakes.
// More obscure free providers (Zoho, Fastmail) deliberately stay
// off the list because some users do host on a custom domain
// through them.
var freeMailDomains = map[string]bool{
	// Google
	"gmail.com":      true,
	"googlemail.com": true,
	// Microsoft
	"outlook.com":   true,
	"hotmail.com":   true,
	"hotmail.co.uk": true,
	"hotmail.fr":    true,
	"live.com":      true,
	"msn.com":       true,
	// Yahoo (top regions)
	"yahoo.com":    true,
	"yahoo.co.uk":  true,
	"yahoo.fr":     true,
	"yahoo.es":     true,
	"yahoo.de":     true,
	"yahoo.com.br": true,
	"ymail.com":    true,
	// Apple
	"icloud.com": true,
	"me.com":     true,
	"mac.com":    true,
	// Proton
	"proton.me":      true,
	"protonmail.com": true,
	"pm.me":          true,
	// AOL
	"aol.com": true,
	// GMX
	"gmx.com": true,
	"gmx.de":  true,
	"gmx.net": true,
	// Brazilian-regional commonly used
	"uol.com.br":   true,
	"bol.com.br":   true,
	"terra.com.br": true,
}

// hostnameRegex matches a bare DNS hostname — labels separated by
// dots, alphanumeric + hyphens (RFC 1123-ish). Used for smtpHost.
// Deliberately rejects schemes (https://...), paths, ports — all
// of which would break the SMTP dial.
var hostnameRegex = regexp.MustCompile(
	`^[A-Za-z0-9](?:[A-Za-z0-9\-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9\-]{0,61}[A-Za-z0-9])?)+$`)

// validateMail enforces shape rules on the `mail` block:
//   - from: valid email shape (when present)
//   - smtpHost: bare hostname (no scheme, no path, no port)
//   - smtpUsername: non-empty when present
//   - smtpPort: in [1, 65535]
//   - reject literal "..." placeholder in any field (catches users
//     who pasted the suggested snippet without filling values in)
//
// Required-ness of any of these is enforced separately at build
// time (when Auth.config is in use). This validator only catches
// malformed values when present.
func validateMail(m *Manifest, phase ValidationPhase) error {
	if m.Mail == nil {
		return nil
	}
	mail := m.Mail

	// Reject the placeholder literal anywhere in the mail block —
	// makes "I pasted the snippet but forgot to edit" a fail-fast
	// instead of a runtime mystery. Typed error so the CLI can
	// render a tailored Hint (sample value, link to provider setup).
	for field, value := range map[string]string{
		"mail.from":         mail.From,
		"mail.smtpHost":     mail.SMTPHost,
		"mail.smtpUsername": mail.SMTPUsername,
	} {
		if value == "..." {
			return &PlaceholderError{Field: field}
		}
	}

	if mail.From != "" {
		// Allow either a bare email (`x@y.com`) or "Display Name
		// <x@y.com>" — common shape on real-world SMTP From headers.
		if !emailRegex.MatchString(mail.From) &&
			!strings.Contains(mail.From, "<") {
			return fmt.Errorf(
				"mar.json: mail.from %q is not a valid email", mail.From)
		}
		// Reject free-mail domains. Even if the email shape is
		// valid, "from = anything@gmail.com" guarantees SMTP send
		// failure — providers only allow domains you verified, and
		// you can never verify gmail.com.
		if domain := extractEmailDomain(mail.From); domain != "" {
			if freeMailDomains[strings.ToLower(domain)] {
				return &FreeMailDomainError{
					From:   mail.From,
					Domain: strings.ToLower(domain),
				}
			}
		}
	}
	if mail.SMTPHost != "" {
		if !hostnameRegex.MatchString(mail.SMTPHost) {
			return fmt.Errorf(
				"mar.json: mail.smtpHost %q must be a bare hostname (no scheme, no port, no path)",
				mail.SMTPHost)
		}
	}
	// smtpUsername is intentionally permissive — providers use
	// wildly different formats (literal "apikey", emails, ARN-like
	// strings, etc.). Only catch obvious garbage (the "..." case
	// already handled above + non-empty when present).
	if mail.SMTPPort != 0 {
		if mail.SMTPPort < 1 || mail.SMTPPort > 65535 {
			return fmt.Errorf(
				"mar.json: mail.smtpPort %d is out of range (1–65535)",
				mail.SMTPPort)
		}
	}
	_ = phase // shape rules apply to both phases
	return nil
}

// validateIOS enforces shape rules on the `ios` block. Required-ness
// of serverUrl depends on the build target — that gate lives in the
// build path (scaffold.BuildIOS), not here. This validator only
// catches malformed values when the field IS present.
func validateIOS(m *Manifest, phase ValidationPhase) error {
	if m.IOS == nil || m.IOS.ServerURL == "" {
		return nil
	}
	url := m.IOS.ServerURL
	// Accept https:// always. Accept http://localhost only — never
	// http://example.com. App Store ATS rejects plain HTTP except
	// for localhost (debug-only).
	if httpsURLRegex.MatchString(url) {
		return nil
	}
	if localhostURLRegex.MatchString(url) {
		return nil
	}
	_ = phase // shape check applies to both phases
	return fmt.Errorf(
		"mar.json: ios.serverUrl must be https:// (or http://localhost for local testing); got %q",
		url,
	)
}

// validateDatabaseAutoBackup enforces the bounds on the auto-backup
// scheduler config. Hard rejection — same policy as adminPanel knobs.
func validateDatabaseAutoBackup(m *Manifest, phase ValidationPhase) error {
	if m.Database == nil || m.Database.AutoBackup == nil {
		return nil
	}
	a := m.Database.AutoBackup
	if a.IntervalHours != 0 {
		if a.IntervalHours < MinAutoBackupIntervalHours || a.IntervalHours > MaxAutoBackupIntervalHours {
			return fmt.Errorf(
				"mar.json: database.autoBackup.intervalHours must be between %d and %d (got %d)",
				MinAutoBackupIntervalHours, MaxAutoBackupIntervalHours, a.IntervalHours,
			)
		}
	}
	if a.RetentionCount != 0 {
		if a.RetentionCount < MinAutoBackupRetentionCount || a.RetentionCount > MaxAutoBackupRetentionCount {
			return fmt.Errorf(
				"mar.json: database.autoBackup.retentionCount must be between %d and %d (got %d)",
				MinAutoBackupRetentionCount, MaxAutoBackupRetentionCount, a.RetentionCount,
			)
		}
	}
	_ = phase
	return nil
}

// validateAdmins checks shape of every email in the admins list.
// Compile-time only — boot-time re-runs the same shape check on the
// same literals (admins are not env:VAR references).
func validateAdmins(m *Manifest, phase ValidationPhase) error {
	for i, email := range m.Admins {
		if email == "" {
			return fmt.Errorf("mar.json: admins[%d] is empty", i)
		}
		if !emailRegex.MatchString(email) {
			return fmt.Errorf("mar.json: admins[%d] %q is not a valid email", i, email)
		}
	}
	// Detect duplicates — these are user errors and the boot-time sync
	// would silently dedupe; better to fail at compile time.
	seen := make(map[string]int, len(m.Admins))
	for i, email := range m.Admins {
		if prev, ok := seen[email]; ok {
			return fmt.Errorf("mar.json: admins[%d] %q duplicates admins[%d]", i, email, prev)
		}
		seen[email] = i
	}
	_ = phase // shape check applies to both phases identically
	return nil
}

// validateAdminPanel enforces ranges on adminPanel knobs.
func validateAdminPanel(m *Manifest, phase ValidationPhase) error {
	if m.AdminPanel == nil {
		return nil
	}
	// recentRequestsSize: 0 is "missing" (gets default); explicit values
	// must be in range. The user explicitly typed a number; if it's out
	// of range, refuse rather than silently clamp.
	if m.AdminPanel.RecentRequestsSize != 0 {
		v := m.AdminPanel.RecentRequestsSize
		if v < MinRecentRequestsSize || v > MaxRecentRequestsSize {
			return fmt.Errorf(
				"mar.json: adminPanel.recentRequestsSize must be between %d and %d (got %d)",
				MinRecentRequestsSize, MaxRecentRequestsSize, v,
			)
		}
	}
	_ = phase
	return nil
}

// ResolvedRecentRequestsSize returns the cap for the in-memory request
// log buffer. Applies the documented default when the field is absent
// (zero value); otherwise returns the configured value (already
// validated to be in range by Validate).
func ResolvedRecentRequestsSize(m *Manifest) int {
	if m == nil || m.AdminPanel == nil || m.AdminPanel.RecentRequestsSize == 0 {
		return DefaultRecentRequestsSize
	}
	return m.AdminPanel.RecentRequestsSize
}

// AllowedFlyMemorySizes is the canonical list of memory sizes Fly.io
// accepts on shared-cpu instances. Validated as an exact match —
// arbitrary sizes like "300mb" fail with a friendly error listing
// these. Source: fly.io/docs/machines/guides-examples/machine-sizing.
var AllowedFlyMemorySizes = []string{
	"256mb", "512mb", "1gb", "2gb", "4gb", "8gb",
}

// DeployFlyError carries the validation failure for the `deploy.fly`
// block. Concrete type (vs plain error) so CLI callers can render
// the multi-line hint block with colors / paste-ready snippets,
// while library callers can read the structured fields.
type DeployFlyError struct {
	// Kind is one of:
	//   "missing-block" — deploy.fly is absent entirely
	//   "missing-app" / "missing-region" / "missing-memory"
	//   "invalid-memory"
	Kind string

	// BadValue is the offending value (for "invalid-memory"),
	// empty otherwise.
	BadValue string
}

func (e *DeployFlyError) Error() string {
	switch e.Kind {
	case "missing-block":
		return "mar.json: no `deploy.fly` block. Add app, region, memory to enable `mar fly *`."
	case "missing-app":
		return "mar.json: deploy.fly.app is required (the Fly app name; becomes <app>.fly.dev)."
	case "missing-region":
		return "mar.json: deploy.fly.region is required (a Fly region code like \"gru\", \"iad\", \"fra\")."
	case "invalid-region":
		return fmt.Sprintf("mar.json: deploy.fly.region = %q is not a valid Fly region code.", e.BadValue)
	case "missing-memory":
		return "mar.json: deploy.fly.memory is required (e.g. \"256mb\", \"512mb\", \"1gb\")."
	case "invalid-memory":
		return fmt.Sprintf("mar.json: deploy.fly.memory = %q is not a valid Fly size. Valid: %s.",
			e.BadValue, strings.Join(AllowedFlyMemorySizes, ", "))
	default:
		return "mar.json: deploy.fly validation failed"
	}
}

// ValidateDeployFly enforces that the deploy.fly block is present and
// well-formed. NOT called from the general Validate() pass — only
// from `mar fly *` subcommands, since non-deploy workflows
// (`mar dev`, `mar build`) don\'t require this block.
//
// Returns a *DeployFlyError so callers can render with the structured
// hint block. The first missing/invalid field wins (no aggregate
// "you have 3 problems" report — the operator fixes one, re-runs,
// hits the next).
func ValidateDeployFly(m *Manifest) error {
	if m == nil || m.Deploy == nil || m.Deploy.Fly == nil {
		return &DeployFlyError{Kind: "missing-block"}
	}
	f := m.Deploy.Fly
	if f.App == "" {
		return &DeployFlyError{Kind: "missing-app"}
	}
	if f.Region == "" {
		return &DeployFlyError{Kind: "missing-region"}
	}
	if f.Memory == "" {
		return &DeployFlyError{Kind: "missing-memory"}
	}
	if !isAllowedFlyMemory(f.Memory) {
		return &DeployFlyError{Kind: "invalid-memory", BadValue: f.Memory}
	}
	return nil
}

// DeployCloudflarePagesError carries the validation failure for the
// `deploy.cloudflare-pages` block. Same shape as DeployFlyError so
// CLI callers can render with the structured hint block.
type DeployCloudflarePagesError struct {
	// Kind is one of:
	//   "missing-block"     — deploy.cloudflare-pages is absent entirely
	//   "missing-app"       — `app` field missing
	//   "missing-account"   — `account` field missing
	//   "missing-api-token" — `apiToken` field missing
	//   "invalid-app"       — `app` field violates Pages naming rules
	//   "invalid-account"   — `account` field is not a CF account ID
	Kind string

	// BadValue is the offending value (for "invalid-*" kinds),
	// empty otherwise.
	BadValue string
}

func (e *DeployCloudflarePagesError) Error() string {
	switch e.Kind {
	case "missing-block":
		return "mar.json: no `deploy.cloudflare-pages` block. Add app, account, apiToken to enable `mar cloudflare-pages deploy`."
	case "missing-app":
		return "mar.json: deploy.cloudflare-pages.app is required (the Pages project name; becomes <app>.pages.dev)."
	case "missing-account":
		return "mar.json: deploy.cloudflare-pages.account is required (the Cloudflare account ID, a 32-char hex string from the dashboard)."
	case "missing-api-token":
		return "mar.json: deploy.cloudflare-pages.apiToken is required (env:VAR reference to the Cloudflare API token)."
	case "invalid-app":
		return fmt.Sprintf("mar.json: deploy.cloudflare-pages.app = %q is not a valid Pages project name (lowercase letters, digits, and hyphens only; 1-58 chars).", e.BadValue)
	case "invalid-account":
		return fmt.Sprintf("mar.json: deploy.cloudflare-pages.account = %q is not a valid Cloudflare account ID (expected 32 hex characters).", e.BadValue)
	default:
		return "mar.json: deploy.cloudflare-pages validation failed"
	}
}

// ValidateDeployCloudflarePages enforces that the deploy.cloudflare-pages
// block is present and well-formed. NOT called from the general
// Validate() pass — only from `mar cloudflare-pages *` subcommands,
// since non-deploy workflows don't require this block.
//
// The shape rules are deliberately strict (account = 32 hex chars,
// app = Pages's documented project-name regex) so typos fail at
// compile time instead of producing a confusing 404 from the API.
//
// IMPORTANT: this function runs AFTER env:VAR resolution. The
// "must be env:" rule for apiToken is enforced earlier, by
// checkSecrets (manifest.go); by the time we get here, apiToken
// is the resolved literal value. So we only check that it's
// non-empty — the env: requirement is covered elsewhere.
func ValidateDeployCloudflarePages(m *Manifest) error {
	if m == nil || m.Deploy == nil || m.Deploy.CloudflarePages == nil {
		return &DeployCloudflarePagesError{Kind: "missing-block"}
	}
	c := m.Deploy.CloudflarePages
	if c.App == "" {
		return &DeployCloudflarePagesError{Kind: "missing-app"}
	}
	if !isValidCloudflarePagesProject(c.App) {
		return &DeployCloudflarePagesError{Kind: "invalid-app", BadValue: c.App}
	}
	if c.Account == "" {
		return &DeployCloudflarePagesError{Kind: "missing-account"}
	}
	if !isValidCloudflareAccountID(c.Account) {
		return &DeployCloudflarePagesError{Kind: "invalid-account", BadValue: c.Account}
	}
	if c.APIToken == "" {
		return &DeployCloudflarePagesError{Kind: "missing-api-token"}
	}
	return nil
}

// cloudflareAccountIDRE matches the Cloudflare account ID format:
// 32 lowercase-hex characters. Every account in the dashboard URL
// follows this exact shape; rejecting anything else catches typos
// early (e.g. someone pasting their zone ID or API token by mistake).
var cloudflareAccountIDRE = regexp.MustCompile(`^[a-f0-9]{32}$`)

func isValidCloudflareAccountID(s string) bool {
	return cloudflareAccountIDRE.MatchString(s)
}

// cloudflarePagesProjectRE matches the Pages project name rules:
// lowercase alphanumerics + hyphens, 1-58 chars, can't start/end
// with a hyphen. Pages itself enforces this server-side; mirroring
// the rule locally turns a 422 from the API into a compile-time
// error message that points at the manifest.
var cloudflarePagesProjectRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,56}[a-z0-9])?$`)

func isValidCloudflarePagesProject(s string) bool {
	return cloudflarePagesProjectRE.MatchString(s)
}

func isAllowedFlyMemory(v string) bool {
	for _, m := range AllowedFlyMemorySizes {
		if v == m {
			return true
		}
	}
	return false
}
