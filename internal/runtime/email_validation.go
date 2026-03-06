package runtime

import (
	"errors"
	"net/mail"
	"strings"
	"unicode"
)

const (
	maxEmailLen       = 254
	maxEmailLocalLen  = 64
	maxEmailDomainLen = 253
	maxDomainLabelLen = 63
)

var (
	errEmailRequired = errors.New("email is required")
	errInvalidEmail  = errors.New("invalid email")
)

// normalizeAndValidateEmail validates and normalizes an email for auth flows.
func normalizeAndValidateEmail(raw string) (string, error) {
	email := strings.TrimSpace(raw)
	if email == "" {
		return "", errEmailRequired
	}
	if len(email) > maxEmailLen {
		return "", errInvalidEmail
	}
	for _, r := range email {
		if unicode.IsControl(r) {
			return "", errInvalidEmail
		}
	}

	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed == nil {
		return "", errInvalidEmail
	}
	if strings.TrimSpace(parsed.Name) != "" {
		return "", errInvalidEmail
	}
	email = strings.TrimSpace(parsed.Address)

	at := strings.LastIndexByte(email, '@')
	if at <= 0 || at >= len(email)-1 {
		return "", errInvalidEmail
	}

	local := email[:at]
	domain := strings.ToLower(email[at+1:])
	if len(local) == 0 || len(local) > maxEmailLocalLen {
		return "", errInvalidEmail
	}
	if len(domain) == 0 || len(domain) > maxEmailDomainLen {
		return "", errInvalidEmail
	}
	if strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") || strings.Contains(domain, "..") {
		return "", errInvalidEmail
	}

	labels := strings.Split(domain, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > maxDomainLabelLen {
			return "", errInvalidEmail
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", errInvalidEmail
		}
		for _, r := range label {
			if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
				return "", errInvalidEmail
			}
		}
	}

	return strings.ToLower(local + "@" + domain), nil
}

func normalizeEmail(email string) string {
	normalized, err := normalizeAndValidateEmail(email)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(email))
	}
	return normalized
}
