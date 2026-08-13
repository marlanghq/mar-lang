package auth

import (
	"os"
	"strings"
	"testing"
)

// Security audit 2026-07-15, finding #6: with no SMTP configured, Send
// used to print the sign-in code to stdout unconditionally. In
// production stdout is the log stream, so a valid one-time code landed
// where anyone with log access could read it.
//
// The sink is now opt-in (AllowStdoutSink), and the zero value refuses.

func TestSendRefusesStdoutSinkByDefault(t *testing.T) {
	// A zero-value config is exactly what a caller that forgot the new
	// field would pass. It must refuse, not leak.
	err := Send(SMTPConfig{}, Email{
		From:    "no-reply@example.com",
		To:      "victim@example.com",
		Subject: "Your code",
		Body:    DefaultBody("424242", 10),
	})
	if err == nil {
		t.Fatal("Send with no SMTP and no AllowStdoutSink returned nil; " +
			"it must refuse rather than print the code")
	}
	if !strings.Contains(err.Error(), "refusing") {
		t.Errorf("error should say it is refusing, got: %v", err)
	}
}

// The refusal has to be silent: an error that ALSO prints the code
// would defeat the point. Capture stdout and assert the code never
// appears there.
func TestSendDoesNotPrintCodeWhenRefusing(t *testing.T) {
	const code = "989898"

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	sendErr := Send(SMTPConfig{}, Email{
		From: "no-reply@example.com", To: "victim@example.com",
		Subject: "Your code", Body: DefaultBody(code, 10),
	})

	w.Close()
	os.Stdout = orig
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, rerr := r.Read(buf)
		sb.Write(buf[:n])
		if rerr != nil {
			break
		}
	}

	if sendErr == nil {
		t.Fatal("expected refusal")
	}
	if strings.Contains(sb.String(), code) {
		t.Errorf("the one-time code leaked to stdout while refusing:\n%s", sb.String())
	}
}

// The dev path still works: opting in prints the mail, so `mar dev`
// keeps its zero-setup sign-in flow.
func TestSendUsesStdoutSinkWhenAllowed(t *testing.T) {
	const code = "135790"

	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := Send(SMTPConfig{AllowStdoutSink: true}, Email{
		From: "no-reply@example.com", To: "dev@example.com",
		Subject: "Your code", Body: DefaultBody(code, 10),
	})

	w.Close()
	os.Stdout = orig
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, rerr := r.Read(buf)
		sb.Write(buf[:n])
		if rerr != nil {
			break
		}
	}

	if err != nil {
		t.Fatalf("dev sink should succeed, got: %v", err)
	}
	if !strings.Contains(sb.String(), code) {
		t.Errorf("dev sink should print the code; stdout was:\n%s", sb.String())
	}
}

// Complete() is the single definition both Send and the boot guard use.
// A half-configured host (no password) cannot authenticate against any
// real provider, so it must NOT count as usable: otherwise the guard
// would let such a deploy boot and every send would fall through to the
// refusal at request time.
func TestCompleteRequiresHostAndPassword(t *testing.T) {
	cases := []struct {
		name string
		cfg  SMTPConfig
		want bool
	}{
		{"empty", SMTPConfig{}, false},
		{"host only", SMTPConfig{Host: "smtp.example.com"}, false},
		{"password only", SMTPConfig{Password: "hunter2"}, false},
		{"both", SMTPConfig{Host: "smtp.example.com", Password: "hunter2"}, true},
	}
	for _, c := range cases {
		if got := c.cfg.Complete(); got != c.want {
			t.Errorf("%s: Complete() = %v, want %v", c.name, got, c.want)
		}
	}
}
