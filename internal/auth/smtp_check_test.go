package auth

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// TestVerifySMTPConfig_NoOpWhenHostEmpty covers the dev/stdout-sink
// path: empty Host means "we're falling back to printing to stdout";
// no SMTP to verify and no error to surface.
func TestVerifySMTPConfig_NoOpWhenHostEmpty(t *testing.T) {
	if err := VerifySMTPConfig(SMTPConfig{}); err != nil {
		t.Errorf("expected nil for empty config; got %v", err)
	}
}

// TestVerifySMTPConfig_NoOpWhenPasswordEmpty mirrors the case where
// the operator declared a real `smtpHost` in mar.json but the
// `env:SMTP_PASSWORD` ref couldn't be resolved (mar dev's tolerant
// loader leaves it empty). Send falls back to the stdout sink in
// that situation, so VerifySMTPConfig should skip the preflight
// auth attempt rather than fail with the provider's "missing
// password" error.
func TestVerifySMTPConfig_NoOpWhenPasswordEmpty(t *testing.T) {
	if err := VerifySMTPConfig(SMTPConfig{
		Host:     "smtp.example.com",
		Port:     587,
		Username: "x",
		// Password intentionally empty.
	}); err != nil {
		t.Errorf("expected nil when password is empty; got %v", err)
	}
}

// TestVerifySMTPConfig_TCPConnectFailure simulates a host that
// resolves but refuses connections — we expect a FriendlyError
// pointing at the TCP-connect stage.
func TestVerifySMTPConfig_TCPConnectFailure(t *testing.T) {
	// 127.0.0.1 with no listener on the port → connection refused.
	// We pick port 1 specifically because nobody legitimately runs
	// services there (privileged port, never used by SMTP).
	err := VerifySMTPConfig(SMTPConfig{
		Host:     "127.0.0.1",
		Port:     1,
		Password: "x", // non-empty so the preflight check actually runs
	})
	if err == nil {
		t.Fatal("expected error connecting to 127.0.0.1:1; got nil")
	}
	fe, ok := IsFriendlyError(err)
	if !ok {
		t.Fatalf("expected FriendlyError, got %T: %v", err, err)
	}
	if fe.Stage != "TCP connect" {
		t.Errorf("Stage: got %q, want %q", fe.Stage, "TCP connect")
	}
	if fe.Details["Host"] != "127.0.0.1" {
		t.Errorf("Details[Host]: got %q, want %q", fe.Details["Host"], "127.0.0.1")
	}
}

// TestVerifySMTPConfig_GreetingFailure starts a TCP listener that
// closes the connection immediately — looks like a TCP-OK SMTP
// peer that died before sending the greeting. We expect Stage =
// "SMTP greeting".
func TestVerifySMTPConfig_GreetingFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// Background "server" that accepts and immediately closes.
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_ = c.Close()
	}()

	host, port := splitHostPort(t, ln.Addr().String())
	err = VerifySMTPConfig(SMTPConfig{Host: host, Port: port, Password: "x"})
	if err == nil {
		t.Fatal("expected greeting failure; got nil")
	}
	fe, ok := IsFriendlyError(err)
	if !ok {
		t.Fatalf("expected FriendlyError, got %T", err)
	}
	if fe.Stage != "SMTP greeting" {
		t.Errorf("Stage: got %q, want %q", fe.Stage, "SMTP greeting")
	}
}

// TestVerifySMTPConfig_StartTLSFailure stands up a fake SMTP server
// that completes the greeting + EHLO but rejects STARTTLS. Confirms
// the wrapper labels the failure as STARTTLS, not earlier stages.
//
// Test SMTP servers like the one here are inherently fragile (a
// real fix would be to use a battle-tested mock library), but they
// catch the failure-classification path that matters for the
// FriendlyError's hint copy.
func TestVerifySMTPConfig_StartTLSFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		// Minimal SMTP exchange: greeting + EHLO response that
		// advertises STARTTLS, then refuses the actual STARTTLS
		// command with 500.
		_, _ = c.Write([]byte("220 mock SMTP ready\r\n"))
		// Read EHLO line (we don't care about the content).
		buf := make([]byte, 256)
		_, _ = c.Read(buf)
		_, _ = c.Write([]byte("250-Hello\r\n250 STARTTLS\r\n"))
		// Read STARTTLS command.
		_, _ = c.Read(buf)
		// Refuse it.
		_, _ = c.Write([]byte("500 not today\r\n"))
	}()

	host, port := splitHostPort(t, ln.Addr().String())
	err = VerifySMTPConfig(SMTPConfig{Host: host, Port: port, Password: "x"})
	if err == nil {
		t.Fatal("expected STARTTLS failure; got nil")
	}
	fe, ok := IsFriendlyError(err)
	if !ok {
		t.Fatalf("expected FriendlyError, got %T", err)
	}
	if fe.Stage != "STARTTLS" {
		t.Errorf("Stage: got %q, want %q", fe.Stage, "STARTTLS")
	}
}

// TestFriendlyError_Format pins the error string format. Boot logs
// are diff-graded across deploys; format drift would silently break
// log dashboards.
func TestFriendlyError_Format(t *testing.T) {
	fe := &FriendlyError{
		Title:   "SMTP CHECK FAILED",
		Message: "Could not validate the SMTP configuration during startup.",
		Stage:   "SMTP AUTH",
		Details: map[string]string{
			"Host":     "smtp.example.com",
			"Port":     "587",
			"Username": "apikey",
			"Stage":    "SMTP AUTH",
			"Reason":   "535 invalid credentials",
		},
		Hints: []string{"Re-push the secret."},
	}
	out := fe.Error()
	for _, want := range []string{
		"SMTP CHECK FAILED",
		"Could not validate",
		"smtp.example.com",
		"587",
		"535 invalid credentials",
		"Re-push the secret.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

// TestVerifySMTPConfig_TimeoutBudget confirms the connect attempt
// returns within ~smtpStartupTimeout instead of using whatever
// default the OS dial would impose. Drops a non-routable address
// (RFC 5737 documentation range) and asserts we don't sit there
// for 60s.
func TestVerifySMTPConfig_TimeoutBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("network timeout test takes ~5s; skipped in -short mode")
	}
	start := time.Now()
	err := VerifySMTPConfig(SMTPConfig{
		Host:     "203.0.113.1", // TEST-NET-3, never routable
		Port:     587,
		Password: "x", // non-empty so the preflight check actually runs
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error from non-routable host; got nil")
	}
	if elapsed > smtpStartupTimeout+2*time.Second {
		t.Errorf("dial took %v; expected close to %v", elapsed, smtpStartupTimeout)
	}
}

// splitHostPort breaks a "host:port" string into typed fields,
// failing the test on any parse error so callers don't have to
// thread err returns.
func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatalf("Sscanf %q: %v", portStr, err)
	}
	return host, port
}

// Ensure the FriendlyError type implements the standard `error`
// contract (otherwise IsFriendlyError-via-errors.As would fail).
var _ error = (*FriendlyError)(nil)
var _ = errors.Unwrap // referenced indirectly via errors.As in IsFriendlyError; keep import live

// Ensure tls.Config is reachable from this test (we link against
// crypto/tls indirectly via VerifySMTPConfig). The unused-import
// avoidance line keeps a deliberate "yes, we exercise this" signal.
var _ = tls.VersionTLS12
