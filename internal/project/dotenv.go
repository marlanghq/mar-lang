// .env file support.
//
// `.env` next to mar.json carries environment variables for local
// development and self-hosting deployments. Loaded once when the
// manifest's env-resolved load path runs (LoadManifest /
// LoadManifestDev). Shell-set variables always win — .env is a
// fallback, never an override — so an operator can
// `export SMTP_PASSWORD=foo && mar dev` to test a new value without
// touching .env.
//
// Format:
//
//	# Full-line comments start with #.
//	KEY=value           # trailing comments allowed on unquoted values
//	KEY="quoted value"  # preserves whitespace and # characters
//	KEY='single quoted' # no escape processing
//	export KEY=value    # optional `export` prefix tolerated
//
// Production note: Fly, Docker, systemd, etc. all inject env vars
// directly. Those take precedence over .env exactly because shell-set
// vars always win. .env is for dev convenience and self-hosting setups
// where the operator drops a file next to the binary.

package project

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LoadDotenv reads .env from `root` (typically the directory
// containing mar.json) and returns the parsed key→value map. Returns
// nil, nil if .env doesn't exist (the common case). Returns an error
// only if the file exists but is malformed — we'd rather fail loudly
// than silently use a half-parsed env.
func LoadDotenv(root string) (map[string]string, error) {
	path := filepath.Join(root, ".env")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	return parseDotenv(f, path)
}

// ApplyDotenv writes each entry in env into the process environment
// IFF the variable isn't already set. This makes shell-set values
// (`export FOO=...`) override .env so an operator can shadow a single
// var without editing the file. A nil or empty map is a no-op.
func ApplyDotenv(env map[string]string) {
	for k, v := range env {
		if _, ok := os.LookupEnv(k); ok {
			continue
		}
		os.Setenv(k, v)
	}
}

// LoadAndApplyDotenv is the one-call convenience that 99% of callers
// want: read .env, fold any unset vars into the process environment,
// return the parsed map (so the caller can log "loaded N vars") and
// any parse error.
func LoadAndApplyDotenv(root string) (map[string]string, error) {
	env, err := LoadDotenv(root)
	if err != nil {
		return nil, err
	}
	ApplyDotenv(env)
	return env, nil
}

// parseDotenv reads the file content into a key→value map. Strict on
// purpose — every parse failure returns an error with file:line so
// the user can fix the bad line instead of guessing why their secret
// didn't load.
func parseDotenv(r io.Reader, path string) (map[string]string, error) {
	out := make(map[string]string)
	scanner := bufio.NewScanner(r)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Tolerate a leading `export ` — common in .env files copied
		// out of shell snippets.
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("%s:%d: expected KEY=value, got %q", path, lineNo, raw)
		}
		key := strings.TrimSpace(line[:eq])
		if !validDotenvKey(key) {
			return nil, fmt.Errorf("%s:%d: invalid variable name %q", path, lineNo, key)
		}
		val, err := parseDotenvValue(line[eq+1:])
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %s", path, lineNo, err)
		}
		out[key] = val
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// validDotenvKey enforces POSIX-style names — letters, digits,
// underscore, must start with a letter or underscore. Matches the
// shape mar.json's `env:VAR` regex (envrefs.go) expects so anything
// loadable here is also referenceable from the manifest.
func validDotenvKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		switch {
		case r == '_':
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// parseDotenvValue handles the three value shapes:
//
//   - "double quoted"  → escapes (\n \t \r \\ \") processed
//   - 'single quoted'  → literal, no escape processing
//   - unquoted         → trimmed, trailing `# comment` stripped
//
// Anything after a closing quote must be whitespace or a comment;
// otherwise we error so the user spots the typo instead of getting a
// silently truncated value.
func parseDotenvValue(raw string) (string, error) {
	v := strings.TrimLeft(raw, " \t")
	if v == "" {
		return "", nil
	}
	if v[0] == '"' || v[0] == '\'' {
		quote := v[0]
		// Single quotes are literal: stop at the first matching quote,
		// no escape handling. Double quotes honour `\"` escapes so a
		// literal quote can appear inside the value.
		end := -1
		for i := 1; i < len(v); i++ {
			if quote == '"' && v[i] == '\\' && i+1 < len(v) {
				i++
				continue
			}
			if v[i] == quote {
				end = i
				break
			}
		}
		if end < 0 {
			return "", fmt.Errorf("unterminated %c quote", quote)
		}
		trailer := strings.TrimSpace(v[end+1:])
		if trailer != "" && !strings.HasPrefix(trailer, "#") {
			return "", fmt.Errorf("unexpected characters after quoted value: %q", trailer)
		}
		val := v[1:end]
		if quote == '"' {
			val = unescapeDoubleQuoted(val)
		}
		return val, nil
	}
	// Unquoted: strip an inline `# comment`, then trim trailing space.
	if hash := strings.IndexByte(v, '#'); hash >= 0 {
		v = v[:hash]
	}
	return strings.TrimRight(v, " \t"), nil
}

// unescapeDoubleQuoted handles the minimal escape set documented in
// the package comment. Unknown escapes pass through verbatim
// (including the backslash) so users don't get cryptic errors for
// e.g. Windows paths in a quoted value.
func unescapeDoubleQuoted(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case '\\':
				sb.WriteByte('\\')
			case '"':
				sb.WriteByte('"')
			default:
				sb.WriteByte(s[i])
				sb.WriteByte(s[i+1])
			}
			i++
			continue
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}
