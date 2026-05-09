package project

import (
	"errors"
	"strings"
	"testing"
)

// TestValidate_NilManifest pins the no-op behavior. Useful because
// LoadManifest returns nil for projects without mar.json.
func TestValidate_NilManifest(t *testing.T) {
	if err := Validate(nil, CompileTime); err != nil {
		t.Fatalf("expected no error for nil manifest; got %v", err)
	}
}

// TestValidate_AdminsAcceptsValidEmails confirms the happy path.
// Multiple addresses, mixed providers, all should pass.
func TestValidate_AdminsAcceptsValidEmails(t *testing.T) {
	m := &Manifest{
		Admins: []string{"me@example.com", "ops@team.io", "a.b+tag@domain.co.uk"},
	}
	if err := Validate(m, CompileTime); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidate_AdminsRejectsBadShape covers obvious garbage. We're
// not RFC-strict, just catching things the user clearly didn't mean.
func TestValidate_AdminsRejectsBadShape(t *testing.T) {
	cases := []struct {
		name  string
		email string
	}{
		{"empty", ""},
		{"no @", "notanemail"},
		{"no domain", "user@"},
		{"no local", "@domain.com"},
		{"no tld", "user@domain"},
		{"spaces", "user @example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{Admins: []string{tc.email}}
			err := Validate(m, CompileTime)
			if err == nil {
				t.Fatalf("expected error for %q; got nil", tc.email)
			}
			if !strings.Contains(err.Error(), "admins[0]") {
				t.Errorf("error should reference admins[0]; got %v", err)
			}
		})
	}
}

// TestValidate_AdminsRejectsDuplicates — the boot-time sync would
// silently dedupe, but accepting it at parse means the user could
// have a typo (same email twice with different casing) the check
// missed. Compile-time rejection forces them to fix it.
func TestValidate_AdminsRejectsDuplicates(t *testing.T) {
	m := &Manifest{
		Admins: []string{"me@x.com", "ops@x.com", "me@x.com"},
	}
	err := Validate(m, CompileTime)
	if err == nil {
		t.Fatal("expected duplicate error; got nil")
	}
	if !strings.Contains(err.Error(), "duplicates") {
		t.Errorf("error should mention duplicate; got %v", err)
	}
}

// TestValidate_RecentRequestsSizeAcceptsRange covers the boundaries
// + typical values. 0 is "missing" (gets default), so it's allowed
// even though it's outside the [10, 5000] range.
func TestValidate_RecentRequestsSizeAcceptsRange(t *testing.T) {
	cases := []int{0, 10, 200, 1000, 5000}
	for _, v := range cases {
		m := &Manifest{AdminPanel: &AdminPanelConfig{RecentRequestsSize: v}}
		if err := Validate(m, CompileTime); err != nil {
			t.Errorf("recentRequestsSize=%d: unexpected error %v", v, err)
		}
	}
}

// TestValidate_RecentRequestsSizeRejectsOutOfRange — the whole point
// of hard rejection is catching surprises like 99999 at compile
// time, not silently clamping to 5000.
func TestValidate_RecentRequestsSizeRejectsOutOfRange(t *testing.T) {
	cases := []struct {
		name  string
		value int
	}{
		{"too small", 5},
		{"too large", 99999},
		{"negative", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{AdminPanel: &AdminPanelConfig{RecentRequestsSize: tc.value}}
			err := Validate(m, CompileTime)
			if err == nil {
				t.Fatalf("expected error for value=%d; got nil", tc.value)
			}
			if !strings.Contains(err.Error(), "recentRequestsSize") {
				t.Errorf("error should reference field; got %v", err)
			}
		})
	}
}

// TestValidate_AutoBackupAcceptsRange — both ends of the documented
// range and a typical mid value should pass.
func TestValidate_AutoBackupAcceptsRange(t *testing.T) {
	cases := []struct {
		name     string
		interval int
		retain   int
	}{
		{"min interval, min retain", 1, 2},
		{"max interval, max retain", 168, 100},
		{"defaults explicit", 6, 28},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{
				Database: &DatabaseConfig{
					AutoBackup: &DatabaseAutoBackup{
						IntervalHours:  tc.interval,
						RetentionCount: tc.retain,
					},
				},
			}
			if err := Validate(m, CompileTime); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestValidate_AutoBackupRejectsOutOfRange — values outside the
// documented bounds fail with a message naming the field.
func TestValidate_AutoBackupRejectsOutOfRange(t *testing.T) {
	cases := []struct {
		name     string
		interval int
		retain   int
		wantSub  string // "" = expects validation to pass
	}{
		// 0 is treated as "not set" → default applies → no error.
		{"interval 0 means unset, no error", 0, 28, ""},
		{"interval negative", -1, 28, "intervalHours"},
		{"interval above max", 200, 28, "intervalHours"},
		{"retain below min (1)", 6, 1, "retentionCount"},
		{"retain above max", 6, 200, "retentionCount"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{
				Database: &DatabaseConfig{
					AutoBackup: &DatabaseAutoBackup{
						IntervalHours:  tc.interval,
						RetentionCount: tc.retain,
					},
				},
			}
			err := Validate(m, CompileTime)
			if tc.wantSub == "" {
				if err != nil {
					t.Errorf("expected no error; got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error mentioning %q; got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q should mention %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// TestAutoBackup_ResolvedDefaults — when fields are zero, the
// resolver returns the documented defaults; explicit values pass
// through verbatim.
func TestAutoBackup_ResolvedDefaults(t *testing.T) {
	var nilCfg *DatabaseAutoBackup
	if nilCfg.AutoBackupEnabled() != true {
		t.Error("nil receiver should default to enabled=true")
	}
	if nilCfg.ResolvedIntervalHours() != 6 {
		t.Error("nil receiver should default to intervalHours=6")
	}
	if nilCfg.ResolvedRetentionCount() != 28 {
		t.Error("nil receiver should default to retentionCount=28")
	}
	disabled := false
	cfg := &DatabaseAutoBackup{Enabled: &disabled, IntervalHours: 24, RetentionCount: 7}
	if cfg.AutoBackupEnabled() != false {
		t.Error("explicit false should resolve to false")
	}
	if cfg.ResolvedIntervalHours() != 24 {
		t.Error("explicit 24 should pass through")
	}
	if cfg.ResolvedRetentionCount() != 7 {
		t.Error("explicit 7 should pass through")
	}
}

// TestValidate_Mail covers the shape rules on the mail block:
// from must be email-shaped, smtpHost a bare hostname, smtpPort
// in range, and the literal "..." placeholder is rejected
// everywhere so users who paste the suggestion snippet but forget
// to fill it in get a fail-fast instead of a runtime SMTP error.
func TestValidate_Mail(t *testing.T) {
	type tc struct {
		name    string
		mail    *MailConfig
		wantSub string // "" → expects pass
	}
	cases := []tc{
		{"empty block (no fields set)", &MailConfig{}, ""},
		{"valid bare email + hostname", &MailConfig{
			From: "x@y.com", SMTPHost: "smtp.x.com",
			SMTPUsername: "apikey", SMTPPassword: "env:P",
		}, ""},
		{"valid display-name from", &MailConfig{
			From: "App <x@y.com>", SMTPHost: "smtp.x.com",
		}, ""},
		{"valid port at lower bound", &MailConfig{SMTPPort: 1}, ""},
		{"valid port at upper bound", &MailConfig{SMTPPort: 65535}, ""},
		{"placeholder ... in from", &MailConfig{
			From: "...", SMTPHost: "smtp.x.com",
		}, "mail.from"},
		{"placeholder ... in smtpHost", &MailConfig{
			From: "x@y.com", SMTPHost: "...",
		}, "mail.smtpHost"},
		{"placeholder ... in smtpUsername", &MailConfig{
			From: "x@y.com", SMTPHost: "smtp.x.com", SMTPUsername: "...",
		}, "mail.smtpUsername"},
		{"invalid email shape", &MailConfig{
			From: "notanemail", SMTPHost: "smtp.x.com",
		}, "mail.from"},
		{"smtpHost with scheme", &MailConfig{
			From: "x@y.com", SMTPHost: "https://smtp.x.com",
		}, "mail.smtpHost"},
		{"smtpHost with port suffix", &MailConfig{
			From: "x@y.com", SMTPHost: "smtp.x.com:587",
		}, "mail.smtpHost"},
		{"smtpHost with path", &MailConfig{
			From: "x@y.com", SMTPHost: "smtp.x.com/foo",
		}, "mail.smtpHost"},
		{"smtpPort negative", &MailConfig{SMTPPort: -1}, "mail.smtpPort"},
		{"smtpPort too large", &MailConfig{SMTPPort: 70000}, "mail.smtpPort"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &Manifest{Mail: c.mail}
			err := Validate(m, CompileTime)
			if c.wantSub == "" {
				if err != nil {
					t.Errorf("expected no error; got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error mentioning %q; got nil", c.wantSub)
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("error %q should mention %q", err.Error(), c.wantSub)
			}
		})
	}
}

// TestValidate_MailRejectsFreeMailDomains — using gmail.com /
// outlook.com / yahoo.com / etc. as the from address can never
// work in production (SMTP providers won't let you send from
// domains you haven't verified). Reject at compile time with the
// typed FreeMailDomainError so the CLI can render the friendly
// hint about how to fix it.
func TestValidate_MailRejectsFreeMailDomains(t *testing.T) {
	cases := []struct {
		from   string
		wantOK bool
	}{
		{"x@gmail.com", false},
		{"App <support@gmail.com>", false}, // display-name form too
		{"x@outlook.com", false},
		{"x@hotmail.com", false},
		{"x@yahoo.com", false},
		{"x@yahoo.co.uk", false},
		{"x@icloud.com", false},
		{"x@proton.me", false},
		{"x@aol.com", false},
		{"x@uol.com.br", false},
		{"X@GMAIL.COM", false}, // case-insensitive

		{"hello@my-app.com", true},
		{"App <hello@my-app.com>", true},
		{"x@notes-app.fly.dev", true},
		// fastmail / zoho deliberately NOT in the blocklist —
		// some users do host on a real domain via these.
		{"x@fastmail.com", true},
		{"x@zoho.com", true},
	}
	for _, tc := range cases {
		t.Run(tc.from, func(t *testing.T) {
			m := &Manifest{Mail: &MailConfig{
				From:         tc.from,
				SMTPHost:     "smtp.x.com",
				SMTPUsername: "u",
			}}
			err := Validate(m, CompileTime)
			if tc.wantOK {
				if err != nil {
					t.Errorf("expected pass for %q; got %v", tc.from, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected free-mail rejection for %q; got nil", tc.from)
			}
			var fmErr *FreeMailDomainError
			if !errors.As(err, &fmErr) {
				t.Errorf("expected *FreeMailDomainError for %q; got %T (%v)",
					tc.from, err, err)
			}
		})
	}
}

// TestValidate_IOSServerURL covers the shape rules for ios.serverUrl.
// Required-ness is enforced at build time (separate); validation only
// kicks in when the field is present.
func TestValidate_IOSServerURL(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"https valid", "https://my-app.fly.dev", false},
		{"https with path", "https://my-app.fly.dev/api/v1", false},
		{"https with port", "https://my-app.fly.dev:8080", false},
		{"localhost http allowed", "http://localhost:3000", false},
		{"127.0.0.1 http allowed", "http://127.0.0.1:3000", false},
		{"plain http rejected", "http://example.com", true},
		{"missing scheme", "my-app.fly.dev", true},
		{"empty is OK (not configured)", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{IOS: &IOSConfig{ServerURL: tc.url}}
			err := Validate(m, CompileTime)
			if tc.wantErr && err == nil {
				t.Errorf("expected error for %q; got nil", tc.url)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for %q: %v", tc.url, err)
			}
		})
	}
}

// TestResolvedRecentRequestsSize pins the default-fallback behavior.
// 0 / nil receiver / nil AdminPanel all yield the documented default;
// explicit values pass through verbatim.
func TestResolvedRecentRequestsSize(t *testing.T) {
	cases := []struct {
		name string
		m    *Manifest
		want int
	}{
		{"nil manifest", nil, 200},
		{"no adminPanel", &Manifest{}, 200},
		{"explicit zero", &Manifest{AdminPanel: &AdminPanelConfig{}}, 200},
		{"explicit 50", &Manifest{AdminPanel: &AdminPanelConfig{RecentRequestsSize: 50}}, 50},
		{"explicit 5000", &Manifest{AdminPanel: &AdminPanelConfig{RecentRequestsSize: 5000}}, 5000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolvedRecentRequestsSize(tc.m)
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}
