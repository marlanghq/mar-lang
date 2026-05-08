// Boot-time SMTP connectivity check.
//
// Runs against the configured SMTP provider before the HTTP listener
// opens so a misconfigured deploy fails loudly instead of "looking
// healthy" but silently swallowing every sign-in attempt. Mirrors
// the pattern from the early Mar (commit ee4c78b) where startup
// connected, authenticated, and disconnected — same defensive
// behavior, kept platform-agnostic so it runs the same way under
// `mar dev`, `mar-runtime`, or anything else that calls Verify.
//
// What we test:
//
//   - DNS resolves the host
//   - TCP connect succeeds within the timeout
//   - STARTTLS negotiates (port 587 default)
//   - SMTP AUTH accepts the username/password
//   - Server responds to QUIT
//
// What we deliberately don't test:
//
//   - Sending a real email. Boot is not the moment for that — costs
//     a credit, may end up in a real inbox, and AUTH passing implies
//     the credentials are valid for sending too.
//   - Outbound network policy details. If the server says HELO works
//     but later refuses to relay, that's a runtime concern (caught
//     when /_auth/request-code actually sends).

package auth

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// smtpStartupTimeout caps the entire connect+auth+quit cycle. 5s is
// generous — public SMTP submission endpoints typically respond in
// well under 500ms. If we time out, something's genuinely wrong
// (firewall, host typo, provider outage) and the wrapper exits with
// a clear error.
const smtpStartupTimeout = 5 * time.Second

// VerifySMTPConfig opens a connection to the configured SMTP server,
// runs STARTTLS, authenticates, and sends QUIT. Returns nil if
// everything works; a structured FriendlyError otherwise.
//
// No-ops with nil error when Host is empty — matches `Send`'s
// fallback to the stdout sink in dev. Production callers should
// gate this behind their own "is SMTP configured?" check; the
// no-op here is defensive only.
//
// `Insecure` is forced false — there's no provider in our supported
// list that requires plaintext SMTP, and silently allowing it would
// hide misconfiguration (port 587 without STARTTLS is a "I forgot
// to use the right port" tell).
func VerifySMTPConfig(cfg SMTPConfig) error {
	if cfg.Host == "" {
		return nil
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	conn, err := net.DialTimeout("tcp", addr, smtpStartupTimeout)
	if err != nil {
		return smtpStartupError(cfg, "TCP connect", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(smtpStartupTimeout)); err != nil {
		return smtpStartupError(cfg, "set deadline", err)
	}

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return smtpStartupError(cfg, "SMTP greeting", err)
	}
	// Quit() best-effort even on subsequent errors.
	defer func() { _ = client.Close() }()

	// STARTTLS. We require it on port 587 (and any other port that
	// supports it). The crypto/tls config validates the server's
	// certificate against the system CA bundle — a self-signed cert
	// on a public SMTP host is misconfiguration, not a use case to
	// support.
	if err := client.StartTLS(&tls.Config{ServerName: cfg.Host}); err != nil {
		return smtpStartupError(cfg, "STARTTLS", err)
	}

	if cfg.Username != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err := client.Auth(auth); err != nil {
			return smtpStartupError(cfg, "SMTP AUTH", err)
		}
	}

	if err := client.Quit(); err != nil {
		// QUIT failure isn't a real problem — connection might
		// have already closed gracefully. Don't fail the check
		// just because of QUIT etiquette.
		_ = err
	}
	return nil
}

// FriendlyError is the boot-time error shape used by SMTP (and,
// going forward, other production checks). Designed to render to
// stderr in a structured way the operator can act on. The Stage
// field tells them which step failed; Hints suggest fixes ranked
// by likelihood.
type FriendlyError struct {
	Title   string
	Message string
	Stage   string
	Details map[string]string
	Hints   []string
}

func (e *FriendlyError) Error() string {
	var b strings.Builder
	if e.Title != "" {
		b.WriteString(e.Title)
		b.WriteString("\n")
	}
	if e.Message != "" {
		b.WriteString("  " + e.Message + "\n")
	}
	if len(e.Details) > 0 {
		b.WriteString("\n")
		// Stable key order: alphabetic. Friendly errors are read
		// by humans; deterministic order makes the output
		// diffable across runs.
		keys := sortedKeys(e.Details)
		for _, k := range keys {
			b.WriteString(fmt.Sprintf("  %-9s %s\n", k+":", e.Details[k]))
		}
	}
	if len(e.Hints) > 0 {
		b.WriteString("\n  Hints:\n")
		for _, h := range e.Hints {
			b.WriteString("    - " + h + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Tiny inline insertion sort: avoid pulling sort just for ~5
	// keys per error.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}

// smtpStartupError builds a FriendlyError with stage-specific hints.
// The stage names are stable so log scrapers / dashboards can
// pattern-match on them.
func smtpStartupError(cfg SMTPConfig, stage string, cause error) error {
	reason := strings.TrimSpace(cause.Error())
	details := map[string]string{
		"Host":     cfg.Host,
		"Port":     fmt.Sprintf("%d", cfg.Port),
		"Username": cfg.Username,
		"Stage":    stage,
		"Reason":   reason,
	}
	hints := []string{
		"Check that smtpHost / smtpPort match your provider's recommendation.",
		"Verify the credentials your env var exposes are still active.",
		"On Fly: confirm the secret is set with `fly secrets list` or re-push it.",
	}
	switch stage {
	case "TCP connect":
		hints = append([]string{
			"Firewall may be blocking outbound traffic on port " +
				details["Port"] + ".",
		}, hints...)
	case "STARTTLS":
		hints = append([]string{
			"If your provider uses implicit TLS (port 465), set smtpPort = 465 explicitly.",
		}, hints...)
	case "SMTP AUTH":
		hints = append([]string{
			"AUTH failure usually means the password is wrong or revoked. Re-push the secret.",
		}, hints...)
	}
	return &FriendlyError{
		Title:   "SMTP CHECK FAILED",
		Message: "Could not validate the SMTP configuration during startup.",
		Stage:   stage,
		Details: details,
		Hints:   hints,
	}
}

// IsFriendlyError unwraps to the structured shape. Useful for
// callers that want to format vs. propagate.
func IsFriendlyError(err error) (*FriendlyError, bool) {
	var fe *FriendlyError
	if errors.As(err, &fe) {
		return fe, true
	}
	return nil, false
}
