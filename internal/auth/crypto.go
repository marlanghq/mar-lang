// Package auth implements Mar's authentication primitives: code
// generation, session token issuance, the auto-managed SQLite tables,
// rate limiting, and email delivery (SMTP or stdout sink).
//
// User code never imports this package directly; the runtime calls in
// when Auth.config / Auth.protected / the framework auth services need
// to do work.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
)

// Code generates a numeric code of the requested length using
// crypto/rand. Length is clamped to [4, 10] for sanity. The default in
// callers is 6 (decimal-only so it's easy to type, no ambiguous
// 0/O/1/l).
func Code(length int) (string, error) {
	if length < 4 {
		length = 4
	}
	if length > 10 {
		length = 10
	}
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth.Code: rand: %w", err)
	}
	out := make([]byte, length)
	for i, b := range buf {
		out[i] = '0' + (b % 10)
	}
	return string(out), nil
}

// Token returns a 32-byte cryptographically random token, encoded as
// URL-safe base64 (no padding) — the value handed to the client as the
// session cookie / Bearer token.
func Token() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth.Token: rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Hash derives the storage form of a code or token via HMAC-SHA256
// using the project's session secret. The DB only ever stores this
// derived form; matching incoming values is done by comparing hashes
// in constant time.
func Hash(secret, value string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Equal compares two strings in constant time. Both arguments are
// expected to be already-hashed values of the same length; the function
// returns false if the lengths differ (without leaking which side).
func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
