package jsserve

import (
	"strings"
	"testing"

	"mar/internal/auth"
	"mar/internal/runtime"
)

// Security audit 2026-07-15, finding #6.
//
// The hole: the admin panel mounts on any project with a session secret
// and a database: NO Auth.config required. An admin-only project could
// therefore boot with no SMTP at all, and every admin sign-in printed a
// valid one-time code to stdout, which in production is the log stream.
//
// guardMailSink turns that silent degradation into a refused boot.

func withAuthState(t *testing.T, secret string, cfg auth.SMTPConfig, dbPath string) {
	t.Helper()
	prevCfg, prevSecret := SMTP(), AuthSecret()
	prevDB := runtime.CurrentDBPath()
	t.Cleanup(func() {
		SetAuthRuntime(prevSecret, prevCfg)
		runtime.SetDBPath(prevDB)
	})
	SetAuthRuntime(secret, cfg)
	runtime.SetDBPath(dbPath)
}

// The motivating case: admin panel reachable (secret + db), no SMTP,
// no dev sink. This is what an admin-only Fly deploy looked like.
func TestGuardMailSinkRefusesAdminOnlyWithoutSMTP(t *testing.T) {
	withAuthState(t, "a-secret-long-enough-for-the-panel", auth.SMTPConfig{}, "/tmp/guard-test.db")

	err := guardMailSink()
	if err == nil {
		t.Fatal("expected a refusal: the admin panel would print sign-in codes to stdout")
	}
	if !strings.Contains(err.Error(), "admin panel") {
		t.Errorf("the message should name the surface that sends mail, got: %v", err)
	}
	if !strings.Contains(err.Error(), "mar dev") {
		t.Errorf("the message should point at the supported way to run without SMTP, got: %v", err)
	}
}

// `mar dev` opts into the sink, so the same configuration must boot.
func TestGuardMailSinkAllowsDevSink(t *testing.T) {
	withAuthState(t, "a-secret-long-enough-for-the-panel",
		auth.SMTPConfig{AllowStdoutSink: true}, "/tmp/guard-test.db")

	if err := guardMailSink(); err != nil {
		t.Fatalf("mar dev must still boot without SMTP, got: %v", err)
	}
}

// Properly configured SMTP boots, obviously.
func TestGuardMailSinkAllowsCompleteSMTP(t *testing.T) {
	withAuthState(t, "a-secret-long-enough-for-the-panel",
		auth.SMTPConfig{Host: "smtp.example.com", Port: 587, Password: "hunter2"},
		"/tmp/guard-test.db")

	if err := guardMailSink(); err != nil {
		t.Fatalf("complete SMTP must boot, got: %v", err)
	}
}

// Half-configured SMTP (host, no password) cannot authenticate against
// any provider, so Send would fall back to the sink. The guard has to
// treat it as unusable, not as "configured": otherwise this deploy
// boots and leaks. This is the case a naive `Host == ""` check misses.
func TestGuardMailSinkRefusesHostWithoutPassword(t *testing.T) {
	withAuthState(t, "a-secret-long-enough-for-the-panel",
		auth.SMTPConfig{Host: "smtp.example.com", Port: 587},
		"/tmp/guard-test.db")

	if err := guardMailSink(); err == nil {
		t.Fatal("host without password cannot deliver; guard must refuse")
	}
}

// A project with nothing that sends mail is unaffected: no secret means
// neither user auth nor the admin panel mounts. A frontend-only game
// must not be dragged into an SMTP requirement it has no use for.
func TestGuardMailSinkIgnoresProjectsThatSendNoMail(t *testing.T) {
	withAuthState(t, "", auth.SMTPConfig{}, "")

	if err := guardMailSink(); err != nil {
		t.Fatalf("a project with no auth and no admin panel must boot, got: %v", err)
	}
}

// The other half of the pair. `guardMailSink` refuses a boot that COULD send
// codes with no SMTP behind it; `maybeVerifySMTP` refuses one whose SMTP is
// fully configured and does not answer. Between them, a deploy that can send
// codes either has working mail or does not start.
//
// The second had no test. Its wrapper is two lines, and the line that matters
// is the one that does NOT return early: once the config is complete, the
// connection is attempted, and a host that cannot be reached stops the deploy
// instead of failing on the first sign-in — hours later, for a user.
func TestSMTPThatDoesNotAnswerStopsTheBoot(t *testing.T) {
	t.Run("nothing configured is not an error", func(t *testing.T) {
		withAuthState(t, "s", auth.SMTPConfig{}, "")
		if err := maybeVerifySMTP(); err != nil {
			t.Fatalf("an app with no mail has nothing to verify: %v", err)
		}
	})

	t.Run("a complete config that cannot be reached", func(t *testing.T) {
		// Port 1 on loopback: reserved, and nothing listens there.
		withAuthState(t, "s", auth.SMTPConfig{
			Host:     "127.0.0.1",
			Port:     1,
			Username: "u",
			Password: "p",
		}, "")
		err := maybeVerifySMTP()
		if err == nil {
			t.Fatal("the deploy would start and fail on the first sign-in instead")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "smtp") &&
			!strings.Contains(err.Error(), "127.0.0.1") {
			t.Fatalf("the error should say what could not be reached: %v", err)
		}
	})
}
