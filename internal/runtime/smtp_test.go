package runtime

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/model"
)

func TestSMTPStartupCheckRequiresPasswordEnv(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "smtp-missing-env.db"), `
(define app-auth
  ((from "no-reply@example.com")
   (subject "Your login code")
   (smtp-host "smtp.example.com")
   (smtp-port 587)
   (smtp-username "resend")
   (smtp-password-env "MISSING_SMTP_PASSWORD")
   (smtp-starttls true)))

(define-app mail-api
  (auth app-auth))
`)

	err := r.runStartupChecks()
	if err == nil {
		t.Fatal("expected startup SMTP validation error")
	}

	var startupErr *startupFriendlyError
	if !errors.As(err, &startupErr) {
		t.Fatalf("expected startupFriendlyError, got %T (%v)", err, err)
	}
	if startupErr.Title != "SMTP CONFIG ERROR" {
		t.Fatalf("unexpected title: %q", startupErr.Title)
	}
	if !strings.Contains(startupErr.Message, "missing or empty") {
		t.Fatalf("unexpected message: %q", startupErr.Message)
	}
}

func TestSMTPStartupCheckSucceedsWithReachableServer(t *testing.T) {
	requireSQLite3(t)

	host := "127.0.0.1"
	port := "587"

	t.Setenv("TEST_SMTP_PASSWORD", "secret")
	restore := stubSMTPDial(t)
	defer restore()

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "smtp-success.db"), fmt.Sprintf(`
(define app-auth
  ((from "no-reply@example.com")
   (subject "Your login code")
   (smtp-host "%s")
   (smtp-port %s)
   (smtp-username "resend")
   (smtp-password-env "TEST_SMTP_PASSWORD")
   (smtp-starttls false)))

(define-app mail-api
  (auth app-auth))
`, host, port))

	if err := r.runStartupChecks(); err != nil {
		t.Fatalf("expected SMTP startup check to succeed, got %v", err)
	}
}

func TestSMTPStartupCheckIsSkippedInDevMode(t *testing.T) {
	requireSQLite3(t)

	t.Setenv("MAR_DEV_MODE", "1")

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "smtp-dev-override.db"), `
(define app-auth
  ((from "no-reply@example.com")
   (subject "Your login code")
   (smtp-host "smtp.example.com")
   (smtp-port 587)
   (smtp-username "resend")
   (smtp-password-env "MISSING_SMTP_PASSWORD")
   (smtp-starttls true)))

(define-app mail-api
  (auth app-auth))
`)

	if err := r.runStartupChecks(); err != nil {
		t.Fatalf("expected SMTP startup check to be skipped in dev mode, got %v", err)
	}
}

func stubSMTPDial(t *testing.T) func() {
	t.Helper()

	original := dialSMTPConnection
	dialSMTPConnection = func(_ model.AuthConfig) (net.Conn, error) {
		serverConn, clientConn := net.Pipe()
		go handleFakeSMTPConn(serverConn)
		return clientConn, nil
	}
	return func() {
		dialSMTPConnection = original
	}
}

func handleFakeSMTPConn(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	writeLine := func(line string) bool {
		if _, err := writer.WriteString(line + "\r\n"); err != nil {
			return false
		}
		return writer.Flush() == nil
	}

	if !writeLine("220 localhost ESMTP ready") {
		return
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(strings.ToUpper(line), "EHLO"), strings.HasPrefix(strings.ToUpper(line), "HELO"):
			if !writeLine("250-localhost") {
				return
			}
			if !writeLine("250 AUTH PLAIN") {
				return
			}
		case strings.HasPrefix(strings.ToUpper(line), "AUTH PLAIN"):
			if !writeLine("235 2.7.0 Authentication successful") {
				return
			}
		case strings.HasPrefix(strings.ToUpper(line), "MAIL FROM:"):
			if !writeLine("250 2.1.0 OK") {
				return
			}
		case strings.HasPrefix(strings.ToUpper(line), "RCPT TO:"):
			if !writeLine("250 2.1.5 OK") {
				return
			}
		case strings.HasPrefix(strings.ToUpper(line), "DATA"):
			if !writeLine("354 End data with <CR><LF>.<CR><LF>") {
				return
			}
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimSpace(dataLine) == "." {
					break
				}
			}
			if !writeLine("250 2.0.0 OK") {
				return
			}
		case strings.HasPrefix(strings.ToUpper(line), "QUIT"):
			_ = writeLine("221 2.0.0 Bye")
			return
		default:
			if !writeLine("250 OK") {
				return
			}
		}
	}
}
