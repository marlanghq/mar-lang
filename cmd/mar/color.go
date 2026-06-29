// CLI color helpers — small, opinionated, self-disabling.
//
// Two design rules:
//
//   1. Auto-disable when output isn't a TTY (piped to file, captured
//      in CI, etc.). ANSI escape codes in a log file or in a grep
//      pipeline are noise.
//   2. Respect the NO_COLOR convention (https://no-color.org). If the
//      env var is set to anything non-empty, all helpers return plain
//      text even in a real terminal.
//
// Three color groups exposed:
//
//   - Status: red / green / yellow — error / success / warning.
//   - Identifier: cyan / magenta    — values, paths, names.
//   - Emphasis: bold                — headers and key labels.
//
// Each color carries one semantic role (see docs/cli-style.md §3):
//
//   colorRed     — error headlines, dangerous actions
//   colorGreen   — success messages, commands the user should run
//   colorYellow  — "Hint:" labels, recoverable warnings
//   colorCyan    — app names, resource codes, fly region codes
//   colorMagenta — file paths, env variable names
//   colorBold    — section headers (e.g. "Fly app name", "Next steps")
//
// All helpers take a writer-aware decision via the package-level
// `colorEnabled` flag. The flag is computed once at first use,
// reading os.Stdout's TTY status; tests can override via
// SetColorEnabled.

package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/term"

	"mar/internal/clio"
)

const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiRed     = "\x1b[31m"
	ansiGreen   = "\x1b[32m"
	ansiYellow  = "\x1b[33m"
	ansiCyan    = "\x1b[36m"
	ansiMagenta = "\x1b[35m"

	// Dim — 256-color medium gray (xterm code 245, RGB ~138/138/138,
	// roughly 54% brightness). Replaces the standard `\x1b[2m`
	// (faint) attribute, which renders nearly invisible on dark
	// themes. Calibrated to stay clearly "secondary" against
	// regular text without disappearing on dark backgrounds.
	// Matches internal/jsserve/banner.go's choice — keep them in
	// sync.
	ansiDim = "\x1b[38;5;245m"

	// Bright variants — used when we want emphasis stronger than
	// the default-intensity color, e.g. the bold red `Error:` prefix
	// against a dark terminal where dim red would disappear.
	ansiBoldRed     = "\x1b[1;31m"
	ansiBoldGreen   = "\x1b[1;32m"
	ansiBoldYellow  = "\x1b[1;33m"
	ansiBoldCyan    = "\x1b[1;36m"
	ansiBoldMagenta = "\x1b[1;35m"
)

var (
	colorOnce    sync.Once
	colorEnabled bool
)

// initColorState computes whether output is a TTY and the user
// hasn't opted out via NO_COLOR. Cached after first call so repeated
// emit sites don't re-stat stdout.
func initColorState() {
	colorOnce.Do(func() {
		// Standard NO_COLOR convention: any non-empty value
		// disables colors. https://no-color.org.
		if v := os.Getenv("NO_COLOR"); v != "" {
			colorEnabled = false
			return
		}
		colorEnabled = term.IsTerminal(int(os.Stdout.Fd()))
	})
}

// SetColorEnabled overrides the auto-detected state. Test-only;
// production code should let initColorState do its job.
func SetColorEnabled(enabled bool) {
	// Run init first to mark the once.Do as fired, then override.
	// Without the init call, the next caller would re-detect from
	// stdout and clobber our override.
	initColorState()
	colorEnabled = enabled
}

// wrap is the universal "apply ANSI prefix + reset, or pass through"
// helper. All semantic helpers below funnel through this so the
// disabled-by-default behavior is consistent.
func wrap(prefix, s string) string {
	initColorState()
	if !colorEnabled {
		return s
	}
	return prefix + s + ansiReset
}

// Status colors.
func colorRed(s string) string    { return wrap(ansiBoldRed, s) }
func colorGreen(s string) string  { return wrap(ansiBoldGreen, s) }
func colorYellow(s string) string { return wrap(ansiBoldYellow, s) }

// Identifier colors.
func colorCyan(s string) string    { return wrap(ansiBoldCyan, s) }
func colorMagenta(s string) string { return wrap(ansiBoldMagenta, s) }

// Emphasis.
func colorBold(s string) string { return wrap(ansiBold, s) }

// Auxiliary / status text that isn't itself a value (per cli-style
// §3 "dim" row): labels, descriptive prose, status descriptors.
func colorDim(s string) string { return wrap(ansiDim, s) }

// cmdSuggest renders a runnable command suggestion for embedding
// in error / hint messages:
//
//   - the literal `mar` binary name in green
//   - literal command parts in bold
//   - <placeholder> segments in cyan (the "identifier" semantic from
//     cli-style.md §3 — same color emails, app names, and codes get
//     when rendered as actual values)
//
// Pass the command WITHOUT the leading "mar". Examples:
//
//	cmdSuggest("dev")
//	cmdSuggest("fly provision")
//	cmdSuggest("admin add <email>")
//	cmdSuggest("init <name>")
//	cmdSuggest("fly database backup download <id>")
//
// Splitting bold literals from cyan placeholders means the user can
// tell at a glance which tokens to type verbatim and which to fill
// in. Each segment is wrapped in its own ANSI reset so the bold and
// cyan attributes don't bleed across the space between them.
//
// Why the helper exists: before it, the codebase mixed two patterns
// for runnable commands — `colorGreen("mar X Y")` (whole-command
// green) in errors/hints, and the green-mar + bold-rest palette in
// help screens. Funneling every "type this" through cmdSuggest
// pins one convention so the two surfaces can't drift apart.
func cmdSuggest(rest string) string {
	return colorGreen("mar") + " " + colorizeCmdParts(rest)
}

// placeholderRE matches <name>-style placeholders. Pattern allows
// letters, digits, dots, hyphens, and dotted suffixes inside the
// brackets (covers <email>, <file.mar>, <app-name>, <id.tar.gz>).
var placeholderRE = regexp.MustCompile(`<[A-Za-z0-9][A-Za-z0-9._\-]*>`)

// colorizeCmdParts walks `rest` and applies bold to literal
// segments and cyan to <placeholder> segments. Exported separately
// from cmdSuggest so help screens (where "mar" prefix is added
// elsewhere) can reuse the same coloring scheme on bare argument
// lists if needed.
func colorizeCmdParts(rest string) string {
	if rest == "" {
		return ""
	}
	idx := placeholderRE.FindAllStringIndex(rest, -1)
	if len(idx) == 0 {
		return colorBold(rest)
	}
	var b strings.Builder
	cursor := 0
	for _, m := range idx {
		if m[0] > cursor {
			b.WriteString(colorBold(rest[cursor:m[0]]))
		}
		b.WriteString(colorCyan(rest[m[0]:m[1]]))
		cursor = m[1]
	}
	if cursor < len(rest) {
		b.WriteString(colorBold(rest[cursor:]))
	}
	return b.String()
}

// errorPrefix is the standard "command-failed" prefix used at the
// start of stderr error messages. Bold red when colors are on,
// plain "Error:" otherwise.
func errorPrefix() string {
	return colorRed("Error:")
}

// hintPrefix is the standard "here's what to try next" prefix for
// hints printed alongside errors. Bold yellow.
func hintPrefix() string {
	return colorYellow("Hint:")
}

// warnPrefix is the standard "this looks off, but we're proceeding"
// prefix for non-fatal warnings. Bold yellow — same color family as
// Hint: (both yellow per cli-style.md §3) since both are recoverable
// signals; the difference is that Warn: surfaces something the
// runtime detected, while Hint: nudges the user toward a next step.
func warnPrefix() string {
	return colorYellow("Warn:")
}

// emitFprintBlock is the shared writer for the three helpers. It
// honors the shared blank-line state in internal/clio (no leading
// blank when a previous block already left a trailing one), writes
// `<prefix> <body>\n`, and always emits a trailing blank — so a
// single helper call followed by `return` produces the
// docs/cli-style.md §1 shape automatically without the caller
// having to remember.
//
// Coordinating via internal/clio (rather than a local state var)
// means the dev banner in internal/jsserve sees fprintHint's
// trailing blank and skips its own leading blank, so the chain
// `fprintHint → printBanner` shows ONE blank between, not two.
func emitFprintBlock(prefix, format string, args ...any) {
	if clio.WantLeadingBlank() {
		fmt.Fprintln(os.Stderr)
	}
	fmt.Fprintf(os.Stderr, "%s %s\n", prefix, fmt.Sprintf(format, args...))
	fmt.Fprintln(os.Stderr)
	clio.MarkTrailingBlank()
}

// fprintError writes a formatted error to stderr with the standard
// red `Error:` prefix. Format args are printed plain — color the
// caller-provided strings explicitly via colorMagenta / colorCyan
// when desired.
//
// Emits both a leading and a trailing blank line so the block reads
// as a standalone item against the surrounding output. Chained
// Error → Hint produces ONE blank between (the state flag
// suppresses the would-be doubled blank).
func fprintError(format string, args ...any) {
	emitFprintBlock(errorPrefix(), format, args...)
}

// fprintHint writes a hint line to stderr. Caller is responsible for
// any inline coloring of the hint body (path, command, etc.).
//
// Same blank-line semantics as fprintError — see that doc.
func fprintHint(format string, args ...any) {
	emitFprintBlock(hintPrefix(), format, args...)
}

// colorizeHint adds visual hierarchy to a multi-line hint body
// emitted by the runtime as plain text. The runtime marks
// identifiers with backticks so the CLI can render colors without
// the runtime knowing about ANSI / TTY state.
//
// Three transformations applied per line:
//
//  1. PROSE lines (default): backtick-quoted spans become cyan,
//     backticks stripped. e.g. "rows in `tasks`" → "rows in
//     tasks" with `tasks` in cyan.
//
//  2. CODE lines (4+ spaces of indent): walked token by token.
//     Each whitespace-separated token gets its own color:
//     - "<value>" placeholder → bold yellow ("you fill in")
//     - identifier from a backtick span in the prose → cyan
//     (matches the highlight in the surrounding paragraph)
//     - everything else (SQL keywords, types, punctuation) →
//     dim, recedes visually
//
//  3. Trailing punctuation on a token (`;`, `,`) stays in the
//     dim color even when the token body is cyan/yellow — so
//     `position;` reads as a cyan word with a dim semicolon.
//
// Safe for non-TTY: colorXxx return plain text when colors are
// disabled, so per-token wrapping is a no-op then aside from
// stripping backticks.
func colorizeHint(body string) string {
	// First pass: extract identifiers from backtick spans. These
	// drive the cyan highlight in BOTH the prose AND the code
	// block — keeping the same word colored in both places makes
	// the eye match them.
	identifiers := extractBacktickIdentifiers(body)

	var out strings.Builder
	lines := strings.Split(body, "\n")
	for idx, line := range lines {
		if isCodeLine(line) {
			out.WriteString(colorizeCodeLine(line, identifiers))
		} else {
			out.WriteString(colorizeProseLine(line))
		}
		if idx < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// isCodeLine returns true for lines that should be tokenized and
// rendered as code (SQL snippets, command examples). 4+ leading
// spaces is the threshold — high enough to skip standard 2-space
// bullet indents, low enough to catch typical code-block indent.
//
// Lines that ALREADY contain ANSI escape sequences are treated as
// prose regardless of indent: those have been colored by the CLI
// code that constructed the hint (e.g. embedded colorGreen("mar
// fly deploy") inside a multi-line hintedError), and tokenizing
// them would split the escape sequences mid-word and dim-wrap the
// fragments. Runtime-emitted hints (where colorizeHint actually
// adds value — SQL code blocks marked by indentation alone) never
// contain ANSI, so this guard doesn't affect them.
func isCodeLine(line string) bool {
	if strings.Contains(line, "\x1b[") {
		return false
	}
	return strings.HasPrefix(line, "    ")
}

// extractBacktickIdentifiers collects the set of words appearing
// inside backtick spans throughout the body. Used to highlight the
// same tokens consistently in the code block.
func extractBacktickIdentifiers(body string) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < len(body); i++ {
		if body[i] != '`' {
			continue
		}
		j := strings.IndexByte(body[i+1:], '`')
		if j < 0 {
			break
		}
		out[body[i+1:i+1+j]] = true
		i += 1 + j
	}
	return out
}

// colorizeProseLine handles regular prose: replaces `backticked`
// spans with cyan text (backticks stripped).
func colorizeProseLine(line string) string {
	var out strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '`' {
			j := strings.IndexByte(line[i+1:], '`')
			if j < 0 {
				out.WriteString(line[i:])
				return out.String()
			}
			out.WriteString(colorCyan(line[i+1 : i+1+j]))
			i += 1 + j
			continue
		}
		out.WriteByte(line[i])
	}
	return out.String()
}

// colorizeCodeLine walks an indented code line token-by-token,
// applying per-token colors. Tokens are whitespace-separated; the
// leading indentation is preserved verbatim (uncolored).
func colorizeCodeLine(line string, identifiers map[string]bool) string {
	trimmed := strings.TrimLeft(line, " ")
	indent := line[:len(line)-len(trimmed)]

	var out strings.Builder
	out.WriteString(indent)

	// Walk the trimmed portion, splitting on spaces but preserving
	// the actual space characters between tokens so multi-space
	// alignment (rare in our hints, but possible) survives.
	i := 0
	for i < len(trimmed) {
		// Pass through whitespace.
		if trimmed[i] == ' ' {
			out.WriteByte(' ')
			i++
			continue
		}
		// Capture the next token (run of non-space chars).
		start := i
		for i < len(trimmed) && trimmed[i] != ' ' {
			i++
		}
		out.WriteString(colorizeCodeToken(trimmed[start:i], identifiers))
	}
	return out.String()
}

// colorizeCodeToken picks a color for one whitespace-delimited
// token from a code line. Trailing punctuation (`;`, `,`) is
// peeled off and dimmed separately so the visual emphasis lands
// on the WORD, not on the separator.
func colorizeCodeToken(token string, identifiers map[string]bool) string {
	// Peel trailing punctuation.
	trail := ""
	for len(token) > 0 {
		last := token[len(token)-1]
		if last == ';' || last == ',' {
			trail = string(last) + trail
			token = token[:len(token)-1]
			continue
		}
		break
	}
	if token == "" {
		return colorDim(trail)
	}
	if token == "<value>" {
		// Placeholder — bright so the user spots "I fill this in".
		return colorYellow(token) + colorDim(trail)
	}
	if identifiers[token] {
		return colorCyan(token) + colorDim(trail)
	}
	// Plain SQL keyword / type / etc. — recede visually.
	return colorDim(token) + colorDim(trail)
}

// fprintWarn writes a warning line to stderr with the standard
// yellow `Warn:` prefix. For "this is probably wrong but we're
// going to keep running" signals.
//
// Same blank-line semantics as fprintError — see that doc.
func fprintWarn(format string, args ...any) {
	emitFprintBlock(warnPrefix(), format, args...)
}
