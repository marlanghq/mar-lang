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

// TestValidate_RateLimitAcceptsRange — both ends of the documented
// range and a typical mid value should pass.
func TestValidate_RateLimitAcceptsRange(t *testing.T) {
	cases := []struct {
		name string
		rpm  int
		brst int
	}{
		{"min rpm, min burst", MinRateLimitRequestsPerMinute, MinRateLimitBurst},
		{"max rpm, max burst", MaxRateLimitRequestsPerMinute, MaxRateLimitBurst},
		{"defaults explicit", DefaultRateLimitRequestsPerMinute, DefaultRateLimitBurst},
		{"both zero means unset, no error", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{
				RateLimit: &RateLimitConfig{
					RequestsPerMinute: tc.rpm,
					Burst:             tc.brst,
				},
			}
			if err := Validate(m, CompileTime); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestValidate_RateLimitRejectsOutOfRange — values outside the
// documented bounds fail with a message naming the field. Same
// "0 means unset" convention as the other bounded knobs.
func TestValidate_RateLimitRejectsOutOfRange(t *testing.T) {
	cases := []struct {
		name    string
		rpm     int
		brst    int
		wantSub string // "" = expects validation to pass
	}{
		{"rpm 0 means unset, no error", 0, 30, ""},
		{"burst 0 means unset, no error", 600, 0, ""},
		{"rpm negative", -1, 30, "requestsPerMinute"},
		{"rpm above max", MaxRateLimitRequestsPerMinute + 1, 30, "requestsPerMinute"},
		{"burst negative", 600, -1, "burst"},
		{"burst above max", 600, MaxRateLimitBurst + 1, "burst"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{
				RateLimit: &RateLimitConfig{
					RequestsPerMinute: tc.rpm,
					Burst:             tc.brst,
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

// TestValidate_MaxBodyBytesAcceptsRange — both ends of the documented
// range and a typical default value should pass.
func TestValidate_MaxBodyBytesAcceptsRange(t *testing.T) {
	cases := []struct {
		name  string
		value int64
	}{
		{"min", MinMaxBodyBytes},
		{"max", MaxMaxBodyBytes},
		{"default", DefaultMaxBodyBytes},
		{"zero means unset, no error", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{Server: &ServerConfig{MaxBodyBytes: tc.value}}
			if err := Validate(m, CompileTime); err != nil {
				t.Errorf("unexpected error for value=%d: %v", tc.value, err)
			}
		})
	}
}

// TestValidate_MaxBodyBytesRejectsOutOfRange — anything outside the
// documented bounds fails with a message naming the field. The lower
// bound exists because below ~1 KiB no real JSON API works; the
// upper bound exists because no "unlimited" — uncapped bodies are
// a DoS vector by design.
func TestValidate_MaxBodyBytesRejectsOutOfRange(t *testing.T) {
	cases := []struct {
		name    string
		value   int64
		wantSub string
	}{
		{"negative", -1, "maxBodyBytes"},
		{"below min", MinMaxBodyBytes - 1, "maxBodyBytes"},
		{"above max", MaxMaxBodyBytes + 1, "maxBodyBytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{Server: &ServerConfig{MaxBodyBytes: tc.value}}
			err := Validate(m, CompileTime)
			if err == nil {
				t.Fatalf("expected error mentioning %q for value=%d; got nil", tc.wantSub, tc.value)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q should mention %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// TestMaxBodyBytes_ResolvedDefaults — nil receiver and zero field
// both fall back to the documented default. Explicit values pass
// through. Pins the resolver against accidental drift.
func TestMaxBodyBytes_ResolvedDefaults(t *testing.T) {
	var nilCfg *ServerConfig
	if got := nilCfg.ResolvedMaxBodyBytes(); got != DefaultMaxBodyBytes {
		t.Errorf("nil receiver: got %d, want %d", got, DefaultMaxBodyBytes)
	}
	zero := &ServerConfig{MaxBodyBytes: 0}
	if got := zero.ResolvedMaxBodyBytes(); got != DefaultMaxBodyBytes {
		t.Errorf("zero field: got %d, want %d", got, DefaultMaxBodyBytes)
	}
	explicit := &ServerConfig{MaxBodyBytes: 5 << 20} // 5 MiB
	if got := explicit.ResolvedMaxBodyBytes(); got != 5<<20 {
		t.Errorf("explicit 5MiB: got %d, want %d", got, 5<<20)
	}
}

// TestIsValidEmail — pin the shape check that runtime auth handlers
// rely on. The same regex backs admin/mail validation in this
// package; this exported helper extends it to /_auth/request-code
// and /_mar/admin/auth/request-code so junk like "not-an-email"
// gets rejected before it can pollute the users table.
func TestIsValidEmail(t *testing.T) {
	good := []string{
		"a@b.co",
		"user.name+tag@example.com",
		"x_y-z@sub.example.co.uk",
	}
	bad := []string{
		"",
		"not-an-email",
		"@nohost.com",
		"missing-at.com",
		"two@@signs.com",
		"trailing-space@x.com ",
		" leading-space@x.com",
		"newline@x.com\nBcc: attacker@evil.com",
	}
	for _, s := range good {
		if !IsValidEmail(s) {
			t.Errorf("good email %q rejected", s)
		}
	}
	for _, s := range bad {
		if IsValidEmail(s) {
			t.Errorf("bad email %q accepted", s)
		}
	}
}

// TestRateLimit_ResolvedDefaults — when the block or fields are
// zero, resolvers return the documented defaults; explicit values
// pass through verbatim. Pinning this so the dev/prod boot path
// (which always feeds the resolver into ratelimit.New) doesn't
// silently shift if someone "improves" the default later.
func TestRateLimit_ResolvedDefaults(t *testing.T) {
	var nilCfg *RateLimitConfig
	if nilCfg.ResolvedRequestsPerMinute() != DefaultRateLimitRequestsPerMinute {
		t.Errorf("nil receiver should default to %d", DefaultRateLimitRequestsPerMinute)
	}
	if nilCfg.ResolvedBurst() != DefaultRateLimitBurst {
		t.Errorf("nil receiver should default to %d", DefaultRateLimitBurst)
	}
	cfg := &RateLimitConfig{RequestsPerMinute: 1200, Burst: 50}
	if cfg.ResolvedRequestsPerMinute() != 1200 {
		t.Error("explicit 1200 should pass through")
	}
	if cfg.ResolvedBurst() != 50 {
		t.Error("explicit 50 should pass through")
	}
	// Zero field falls back to default even if block is present.
	zeroBurst := &RateLimitConfig{RequestsPerMinute: 1200, Burst: 0}
	if zeroBurst.ResolvedBurst() != DefaultRateLimitBurst {
		t.Error("zero burst should fall back to default")
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

// TestValidate_MailPlaceholderIsTyped — the "..." placeholder check
// returns a *PlaceholderError so the CLI can match it via errors.As
// and render a field-specific Hint (instead of falling through to
// the generic catch-all). Pins the typed-error contract so future
// refactors don't accidentally downgrade it to a plain fmt.Errorf.
func TestValidate_MailPlaceholderIsTyped(t *testing.T) {
	cases := []struct {
		name      string
		mail      *MailConfig
		wantField string
	}{
		{"from", &MailConfig{From: "..."}, "mail.from"},
		{"smtpHost", &MailConfig{From: "x@y.com", SMTPHost: "..."}, "mail.smtpHost"},
		{"smtpUsername", &MailConfig{From: "x@y.com", SMTPHost: "smtp.x.com", SMTPUsername: "..."}, "mail.smtpUsername"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &Manifest{Mail: c.mail}
			err := Validate(m, CompileTime)
			if err == nil {
				t.Fatalf("expected error; got nil")
			}
			var phErr *PlaceholderError
			if !errors.As(err, &phErr) {
				t.Fatalf("error %v should be *PlaceholderError; got %T", err, err)
			}
			if phErr.Field != c.wantField {
				t.Errorf("Field: got %q, want %q", phErr.Field, c.wantField)
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

// validCFPages returns a manifest with every cloudflare-pages field
// populated with a known-good value. Helper for tests that want to
// vary one field at a time without re-typing the rest.
func validCFPages() *Manifest {
	return &Manifest{
		Deploy: &DeployConfig{
			CloudflarePages: &CloudflarePagesDeployConfig{
				App:      "mar-website",
				Account:  "abc123def456abc123def456abc123de", // 32 hex chars
				APIToken: "resolved-token-value",             // post-env: resolution
			},
		},
	}
}

// TestValidateDeployCloudflarePages_Happy pins the success case.
// All three fields present and well-formed → nil error. APIToken
// here is the literal resolved value (post-env: expansion), since
// ValidateDeployCloudflarePages runs after env resolution.
func TestValidateDeployCloudflarePages_Happy(t *testing.T) {
	if err := ValidateDeployCloudflarePages(validCFPages()); err != nil {
		t.Errorf("expected no error; got %v", err)
	}
}

// TestValidateDeployCloudflarePages_MissingCases covers every "field
// is required" branch. Each one asserts the error Kind so a future
// rename of a Kind shows up as a test failure immediately.
func TestValidateDeployCloudflarePages_MissingCases(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(*CloudflarePagesDeployConfig)
		wantKind string
	}{
		{
			name:     "missing-app",
			mutate:   func(c *CloudflarePagesDeployConfig) { c.App = "" },
			wantKind: "missing-app",
		},
		{
			name:     "missing-account",
			mutate:   func(c *CloudflarePagesDeployConfig) { c.Account = "" },
			wantKind: "missing-account",
		},
		{
			name:     "missing-api-token",
			mutate:   func(c *CloudflarePagesDeployConfig) { c.APIToken = "" },
			wantKind: "missing-api-token",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validCFPages()
			tc.mutate(m.Deploy.CloudflarePages)
			err := ValidateDeployCloudflarePages(m)
			if err == nil {
				t.Fatalf("expected error; got nil")
			}
			var de *DeployCloudflarePagesError
			if !errors.As(err, &de) {
				t.Fatalf("expected DeployCloudflarePagesError; got %T", err)
			}
			if de.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", de.Kind, tc.wantKind)
			}
		})
	}

	// Whole-block-missing cases handled separately — they don't
	// mutate a CloudflarePages struct because there isn't one yet.
	blockCases := []struct {
		name string
		m    *Manifest
	}{
		{"nil-manifest", nil},
		{"nil-deploy", &Manifest{Deploy: nil}},
		{"nil-cloudflare-pages", &Manifest{Deploy: &DeployConfig{}}},
	}
	for _, tc := range blockCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDeployCloudflarePages(tc.m)
			if err == nil {
				t.Fatalf("expected error; got nil")
			}
			var de *DeployCloudflarePagesError
			if !errors.As(err, &de) || de.Kind != "missing-block" {
				t.Errorf("got %v, want missing-block", err)
			}
		})
	}
}

// TestValidateDeployCloudflarePages_InvalidAccount catches typos in
// the account ID. Cloudflare's API would reject these with a 404
// minutes into the deploy; catching at compile time is much kinder.
func TestValidateDeployCloudflarePages_InvalidAccount(t *testing.T) {
	cases := []struct {
		name    string
		account string
	}{
		{"too-short", "abc123"},
		{"too-long", "abc123def456abc123def456abc123def4"},
		{"uppercase-hex", "ABC123DEF456ABC123DEF456ABC123DE"},
		{"non-hex-chars", "abc123def456abc123def456abc123dx"},
		{"contains-spaces", "abc123def456 abc123def456abc123de"},
		{"contains-dashes", "abc-123-def-456-abc-123-def-456-"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validCFPages()
			m.Deploy.CloudflarePages.Account = tc.account
			err := ValidateDeployCloudflarePages(m)
			if err == nil {
				t.Fatalf("expected error; got nil")
			}
			var de *DeployCloudflarePagesError
			if !errors.As(err, &de) {
				t.Fatalf("expected DeployCloudflarePagesError; got %T", err)
			}
			if de.Kind != "invalid-account" {
				t.Errorf("Kind = %q, want %q", de.Kind, "invalid-account")
			}
			if de.BadValue != tc.account {
				t.Errorf("BadValue = %q, want %q", de.BadValue, tc.account)
			}
		})
	}
}

// TestValidateDeployCloudflarePages_InvalidApp catches typos in
// the app (project) name. The rules mirror Cloudflare Pages's own:
// lowercase, alphanumerics + hyphens, 1-58 chars, no leading/trailing
// hyphen.
func TestValidateDeployCloudflarePages_InvalidApp(t *testing.T) {
	cases := []struct {
		name string
		app  string
	}{
		{"uppercase", "MarWebsite"},
		{"with-spaces", "mar website"},
		{"underscores", "mar_website"},
		{"dots", "mar.website"},
		{"leading-hyphen", "-mar-website"},
		{"trailing-hyphen", "mar-website-"},
		{"too-long", strings.Repeat("a", 59)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validCFPages()
			m.Deploy.CloudflarePages.App = tc.app
			err := ValidateDeployCloudflarePages(m)
			if err == nil {
				t.Fatalf("expected error; got nil")
			}
			var de *DeployCloudflarePagesError
			if !errors.As(err, &de) {
				t.Fatalf("expected DeployCloudflarePagesError; got %T", err)
			}
			if de.Kind != "invalid-app" {
				t.Errorf("Kind = %q, want %q", de.Kind, "invalid-app")
			}
		})
	}
}

// TestValidateDeployCloudflarePages_AppEdgeCases pins the 1-char and
// 58-char boundaries. Pages's own rules say 1-58 chars inclusive;
// off-by-one in the regex would silently break either edge.
func TestValidateDeployCloudflarePages_AppEdgeCases(t *testing.T) {
	cases := []struct {
		name   string
		app    string
		wantOK bool
	}{
		{"single-char", "a", true},
		{"single-digit", "1", true},
		{"max-length", strings.Repeat("a", 58), true},
		{"one-over-max", strings.Repeat("a", 59), false},
		{"empty", "", false}, // empty triggers missing-app, not invalid
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validCFPages()
			m.Deploy.CloudflarePages.App = tc.app
			err := ValidateDeployCloudflarePages(m)
			if tc.wantOK && err != nil {
				t.Errorf("expected no error; got %v", err)
			}
			if !tc.wantOK && err == nil {
				t.Errorf("expected error; got nil")
			}
		})
	}
}

// TestCheckSecrets_APITokenObligatesEnv pins the rule that
// deploy.cloudflare-pages.apiToken must be an env:VAR reference —
// committing a literal token would be a credential leak. Same
// shape as the existing checks for mail.smtpPassword and
// auth.sessionSecret.
//
// checkSecrets is the manifest.go function that enforces this
// pre-resolution. The function is unexported, but we exercise it
// via the LoadManifest path that calls it.
func TestCheckSecrets_APITokenObligatesEnv(t *testing.T) {
	// We test by direct call since checkSecrets isn't exported.
	// The path matters: tests inside package project can call it.
	cases := []struct {
		name      string
		apiToken  string
		wantError bool
	}{
		{"env-ref", "env:CF_API_TOKEN", false},
		{"literal-rejected", "abc123-secret-token", true},
		{"empty-allowed", "", false}, // missing handled by Validate, not checkSecrets
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{
				Deploy: &DeployConfig{
					CloudflarePages: &CloudflarePagesDeployConfig{
						APIToken: tc.apiToken,
					},
				},
			}
			err := checkSecrets(m)
			if tc.wantError && err == nil {
				t.Errorf("expected error; got nil")
			}
			if !tc.wantError && err != nil {
				t.Errorf("expected no error; got %v", err)
			}
			if tc.wantError && err != nil {
				if !strings.Contains(err.Error(), "apiToken") {
					t.Errorf("error should mention apiToken; got %q", err)
				}
			}
		})
	}
}
