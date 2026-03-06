package runtime

import (
	"strings"
	"testing"
)

func TestNormalizeAndValidateEmailAcceptsAndNormalizes(t *testing.T) {
	email, err := normalizeAndValidateEmail("  User.Name+test@Example.COM  ")
	if err != nil {
		t.Fatalf("expected valid email, got error: %v", err)
	}
	if email != "user.name+test@example.com" {
		t.Fatalf("unexpected normalized email: %q", email)
	}
}

func TestNormalizeAndValidateEmailRejectsCRLFInjection(t *testing.T) {
	_, err := normalizeAndValidateEmail("victim@example.com\r\nBcc: attacker@example.com")
	if err == nil {
		t.Fatal("expected CRLF injection email to be rejected")
	}
	if err.Error() != "invalid email" {
		t.Fatalf("expected invalid email error, got %v", err)
	}
}

func TestNormalizeAndValidateEmailRejectsDisplayNameFormat(t *testing.T) {
	_, err := normalizeAndValidateEmail("John Doe <john@example.com>")
	if err == nil {
		t.Fatal("expected display-name format to be rejected")
	}
	if err.Error() != "invalid email" {
		t.Fatalf("expected invalid email error, got %v", err)
	}
}

func TestParseAuthEmailReturnsFriendlyErrors(t *testing.T) {
	_, err := parseAuthEmail(map[string]any{})
	if err == nil {
		t.Fatal("expected missing email to fail")
	}
	apiErr, ok := err.(*apiError)
	if !ok {
		t.Fatalf("expected apiError, got %T", err)
	}
	if apiErr.Status != 400 || apiErr.Message != "email is required" {
		t.Fatalf("unexpected apiError: %+v", apiErr)
	}

	_, err = parseAuthEmail(map[string]any{"email": "not-an-email"})
	if err == nil {
		t.Fatal("expected invalid email to fail")
	}
	apiErr, ok = err.(*apiError)
	if !ok {
		t.Fatalf("expected apiError, got %T", err)
	}
	if apiErr.Status != 400 || !strings.Contains(apiErr.Message, "invalid email") {
		t.Fatalf("unexpected apiError: %+v", apiErr)
	}
}
