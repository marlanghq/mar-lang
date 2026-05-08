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
	return nil
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
