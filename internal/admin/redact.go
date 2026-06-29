// Redaction for the request-log ring buffer. Goal: keep the admin
// "recent requests" view useful for debugging without ever surfacing
// tokens, codes, or stray emails that happen to land in a path, error
// message, or SQL fragment.
//
// Applied at WRITE time (inside RequestLogger.Record), not at read.
// Write-time means the in-memory buffer never holds the sensitive
// data: a pprof heap dump, debugger session, or any future code
// that learns to read the buffer won't leak. The cost — debugging
// flows that need the raw path/sql have to add a separate,
// non-buffered log path — is acceptable because the buffer is
// intentionally not a debugging tool. It's a "what's hitting my app"
// view; deeper inspection belongs on its own surface.
//
// Patterns are hardcoded by design. A configurable allow/deny list
// here would let an operator misconfigure their way into leaking
// secrets, and there's no later config change that recovers the
// already-leaked data. Adding a new pattern is a framework code
// change, reviewed once.

package admin

import "regexp"

var (
	// Email-shaped substring anywhere in the input. Run last so the
	// more specific patterns (which return only the value portion)
	// get a chance first.
	emailPattern = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)

	// `Bearer <token>` in an Authorization header value. Captures
	// the keyword so we keep "Bearer" visible in the log; only the
	// secret part is replaced.
	bearerPattern = regexp.MustCompile(`(?i)(bearer\s+)([a-z0-9._\-]+)`)

	// `?token=...`, `?api_key=...`, `?access_token=...`, `?password=...`,
	// `?secret=...` in a URL or URL-encoded body. Match both `?` and
	// `&` to catch params anywhere in the query. Names cover the
	// common framework / SaaS conventions; expand by code change if
	// usage data shows new ones in the wild.
	queryParamPattern = regexp.MustCompile(`(?i)([?&](?:token|api[_-]?key|access[_-]?token|password|secret)=)([^&\s#]+)`)

	// `code: 123456` or `code=123456` in plain text (e.g. error
	// messages, log lines a handler reflected into ErrorMessage).
	plainCodePattern = regexp.MustCompile(`(?i)(code\s*[:=]\s*)([a-z0-9._\-]+)`)

	// `"token": "..."`, `"code": "..."`, `"email": "..."` in JSON
	// bodies that surfaced into a log field. Tolerant of whitespace.
	jsonTokenPattern = regexp.MustCompile(`(?i)("token"\s*:\s*")([^"]*)(")`)
	jsonCodePattern  = regexp.MustCompile(`(?i)("code"\s*:\s*")([^"]*)(")`)
	jsonEmailPattern = regexp.MustCompile(`(?i)("email"\s*:\s*")([^"]*)(")`)
)

// SanitizeForLog scrubs values that look like sensitive material out
// of `s`. Used by RequestLogger.Record on Path before storage.
//
// What gets replaced with `<omitted>`:
//   - Bearer tokens after `Bearer ` / `bearer `
//   - Common sensitive query params: token, api_key, access_token,
//     password, secret (with or without `_` / `-` in the names)
//   - `code: ...` / `code=...` in plain text
//   - `"token" / "code" / "email"` string values in JSON fragments
//   - Any email-shaped substring (last; broad fallback)
//
// What is intentionally NOT touched:
//   - The dedicated UserEmail field on RequestLog (lives outside this
//     function — the admin needs to see who made the request).
//   - Method, status, durations, atMs.
//
// Empty / whitespace-only input is returned unchanged so the caller
// doesn't have to nil-check.
func SanitizeForLog(s string) string {
	if s == "" {
		return s
	}
	out := s
	// Order matters: the specific patterns capture only the value
	// portion, leaving the surrounding key intact for context. The
	// broad emailPattern runs last so it doesn't eat keys like
	// "email":"x@y.com" before jsonEmailPattern can preserve the key.
	out = bearerPattern.ReplaceAllString(out, "${1}<omitted>")
	out = queryParamPattern.ReplaceAllString(out, "${1}<omitted>")
	out = jsonTokenPattern.ReplaceAllString(out, `${1}<omitted>${3}`)
	out = jsonCodePattern.ReplaceAllString(out, `${1}<omitted>${3}`)
	out = jsonEmailPattern.ReplaceAllString(out, `${1}<omitted>${3}`)
	out = plainCodePattern.ReplaceAllString(out, "${1}<omitted>")
	out = emailPattern.ReplaceAllString(out, "<omitted>")
	return out
}
