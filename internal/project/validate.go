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
	return nil
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
	// instead of a runtime mystery.
	for field, value := range map[string]string{
		"mail.from":         mail.From,
		"mail.smtpHost":     mail.SMTPHost,
		"mail.smtpUsername": mail.SMTPUsername,
	} {
		if value == "..." {
			return fmt.Errorf(
				"mar.json: %s is the placeholder %q — fill in the real value",
				field, "...")
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
