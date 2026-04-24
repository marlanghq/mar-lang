package runtime

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"

	"mar/internal/model"
)

const smtpStartupTimeout = 5 * time.Second

var dialSMTPConnection = defaultDialSMTPConnection

func (r *Runtime) runStartupChecks() error {
	return r.validateEmailDeliveryStartup()
}

func (r *Runtime) ValidateStartup() error {
	if r == nil {
		return nil
	}
	if r.startupValid {
		return nil
	}
	if err := r.runStartupChecks(); err != nil {
		return err
	}
	r.startupValid = true
	return nil
}

func (r *Runtime) validateEmailDeliveryStartup() error {
	cfg := r.authConfig()
	if isMarDevMode() {
		return nil
	}
	if err := validateRequiredSMTPConfig(cfg); err != nil {
		return err
	}

	useColor := supportsANSI()
	fmt.Printf("\n%s\n", colorize(useColor, ansiSection, "SMTP"))
	fmt.Printf("  %s %s\n", colorize(useColor, ansiLabel, "Checking"), smtpAddress(cfg))
	fmt.Printf("  %s %s\n", colorize(useColor, ansiLabel, "Username"), cfg.SMTPUsername)
	fmt.Printf("  %s %s\n", colorize(useColor, ansiLabel, "Security"), smtpSecurityLabel(cfg))

	password, err := loadSMTPPassword(cfg)
	if err != nil {
		return err
	}
	if err := smtpConnectAndAuthenticate(cfg, password); err != nil {
		return wrapSMTPStartupError(cfg, err)
	}

	fmt.Printf("  %s %s\n", colorize(useColor, ansiLabel, "Status"), colorize(useColor, ansiCommand, "ready"))
	return nil
}

func sendWithSMTP(cfg model.AuthConfig, toEmail, code string) error {
	password, err := loadSMTPPassword(cfg)
	if err != nil {
		return err
	}
	client, err := openSMTPClient(cfg, password)
	if err != nil {
		return err
	}
	defer client.Close()

	if err := client.Mail(cfg.EmailFrom); err != nil {
		return err
	}
	if err := client.Rcpt(toEmail); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	message := strings.ReplaceAll(buildAuthEmailMessage(cfg.EmailFrom, cfg.EmailSubject, toEmail, code, cfg.CodeTTLMinutes), "\n", "\r\n")
	if _, err := writer.Write([]byte(message)); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func loadSMTPPassword(cfg model.AuthConfig) (string, error) {
	envName := strings.TrimSpace(cfg.SMTPPasswordEnv)
	if envName == "" {
		return "", &startupFriendlyError{
			Title:   "SMTP CONFIG ERROR",
			Message: "I cannot start this app because the SMTP password environment variable is not configured.",
			Details: []startupDetail{
				{Label: "Setting", Value: "app-auth -> smtp-password-env"},
			},
			Hints: []string{
				`Set smtp-password-env to the name of an environment variable that holds your SMTP password.`,
			},
		}
	}
	value := strings.TrimSpace(os.Getenv(envName))
	if value == "" {
		return "", &startupFriendlyError{
			Title:   "SMTP CONFIG ERROR",
			Message: fmt.Sprintf("I cannot start this app because the environment variable %s is missing or empty.", envName),
			Details: []startupDetail{
				{Label: "Host", Value: cfg.SMTPHost},
				{Label: "Port", Value: strconv.Itoa(cfg.SMTPPort)},
				{Label: "Env var", Value: envName},
			},
			Hints: []string{
				"Export that environment variable before starting the app.",
				`Example: export ` + envName + `="your-smtp-password"`,
			},
		}
	}
	return value, nil
}

func validateRequiredSMTPConfig(cfg model.AuthConfig) error {
	if strings.TrimSpace(cfg.SMTPHost) == "" {
		return &startupFriendlyError{
			Title:   "SMTP CONFIG ERROR",
			Message: "I cannot start this app because smtp-host is not configured.",
			Details: []startupDetail{
				{Label: "Setting", Value: "app-auth -> smtp-host"},
			},
			Hints: []string{
				`Set smtp-host to your SMTP server host.`,
			},
		}
	}
	if strings.TrimSpace(cfg.SMTPUsername) == "" {
		return &startupFriendlyError{
			Title:   "SMTP CONFIG ERROR",
			Message: "I cannot start this app because smtp-username is not configured.",
			Details: []startupDetail{
				{Label: "Host", Value: cfg.SMTPHost},
				{Label: "Setting", Value: "app-auth -> smtp-username"},
			},
			Hints: []string{
				`Set smtp-username to the SMTP username for your provider.`,
			},
		}
	}
	return nil
}

func wrapSMTPStartupError(cfg model.AuthConfig, err error) error {
	return &startupFriendlyError{
		Title:   "SMTP CHECK FAILED",
		Message: "I could not connect to the configured SMTP server during startup.",
		Details: []startupDetail{
			{Label: "Host", Value: cfg.SMTPHost},
			{Label: "Port", Value: strconv.Itoa(cfg.SMTPPort)},
			{Label: "Username", Value: cfg.SMTPUsername},
			{Label: "Security", Value: smtpSecurityLabel(cfg)},
			{Label: "Reason", Value: strings.TrimSpace(err.Error())},
		},
		Hints: []string{
			"Check the SMTP host, port, username, password, and firewall rules.",
			"If your provider expects STARTTLS on port 587, keep smtp-starttls true.",
			"If your provider expects implicit TLS on port 465, use smtp-port 465 and smtp-starttls false.",
		},
	}
}

func smtpConnectAndAuthenticate(cfg model.AuthConfig, password string) error {
	client, err := openSMTPClient(cfg, password)
	if err != nil {
		return err
	}
	return client.Quit()
}

func openSMTPClient(cfg model.AuthConfig, password string) (*smtp.Client, error) {
	conn, err := dialSMTPConnection(cfg)
	if err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Now().Add(smtpStartupTimeout)); err != nil {
		_ = conn.Close()
		return nil, err
	}

	client, err := smtp.NewClient(conn, cfg.SMTPHost)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	if cfg.SMTPStartTLS {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			_ = client.Close()
			return nil, fmt.Errorf("server does not advertise STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{
			ServerName: cfg.SMTPHost,
			MinVersion: tls.VersionTLS12,
		}); err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("STARTTLS failed: %w", err)
		}
	}

	auth := smtp.PlainAuth("", cfg.SMTPUsername, password, cfg.SMTPHost)
	if err := client.Auth(auth); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("authentication failed: %w", err)
	}
	return client, nil
}

func defaultDialSMTPConnection(cfg model.AuthConfig) (net.Conn, error) {
	addr := smtpAddress(cfg)
	dialer := &net.Dialer{Timeout: smtpStartupTimeout}

	if cfg.SMTPStartTLS {
		return dialer.Dial("tcp", addr)
	}
	if cfg.SMTPPort == 465 {
		return tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
			ServerName: cfg.SMTPHost,
			MinVersion: tls.VersionTLS12,
		})
	}
	return dialer.Dial("tcp", addr)
}

func smtpAddress(cfg model.AuthConfig) string {
	return net.JoinHostPort(strings.TrimSpace(cfg.SMTPHost), strconv.Itoa(cfg.SMTPPort))
}

func smtpSecurityLabel(cfg model.AuthConfig) string {
	if cfg.SMTPStartTLS {
		return "STARTTLS"
	}
	if cfg.SMTPPort == 465 {
		return "TLS"
	}
	return "plain"
}
