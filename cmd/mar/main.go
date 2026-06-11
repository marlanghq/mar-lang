// Command mar is the entry point for the Mar compiler and tooling.
//
// mar is a full-stack web language. The driver reflects that focus:
//
//	mar dev [path]         Run a fullstack/frontend/backend app in dev mode
//	                       (hot reload, dev banner, browser-open).
//	mar build [dir]        Compile a project to a static dist/.
//	mar init <name>        Scaffold a new project.
//	mar check <file>       Type-check (without running).
//	mar repl               Interactive REPL.
//	mar format <file>...   Reformat in place.
//	mar lsp                Language server (used by editor extensions).
//	mar config <dir>       Print mar.json from a project.
//	mar version            Print the version.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"mar/internal/admin"
	"mar/internal/apphost"
	"mar/internal/ast"
	"mar/internal/diag"
	"mar/internal/formatter"
	"mar/internal/jsserve"
	"mar/internal/lsp"
	"mar/internal/parser"
	"mar/internal/project"
	"mar/internal/ratelimit"
	"mar/internal/runtime"
	"mar/internal/scaffold"
	"mar/internal/typecheck"
)

// openURL invokes the OS-native "open this URL" command. Errors are
// non-fatal — the dev server keeps running even if the browser launch
// fails (e.g. on a headless box without the helper binary installed).
func openURL(url string) {
	var cmd *exec.Cmd
	switch goruntime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default: // linux, *bsd
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

// extractNoOpenFlag pulls `--no-open` out of args, returning
// (flagPresent, argsWithoutFlag). Order-independent — the flag can
// appear before or after the positional path argument:
//
//	mar dev --no-open examples/foo
//	mar dev examples/foo --no-open
//
// Both work. Used by `mar dev` and `mar fly deploy` to suppress the
// auto-browser-open.
func extractNoOpenFlag(args []string) (bool, []string) {
	found := false
	rest := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--no-open" {
			found = true
			continue
		}
		rest = append(rest, a)
	}
	return found, rest
}

// shouldOpenBrowser is the single decision point for whether to auto-
// launch a browser after a command produced a viewable URL.
//
// Returns false when:
//   - The caller passed `--no-open` (local opt-out), OR
//   - The standard `CI` env var is set. GitHub Actions, GitLab CI,
//     CircleCI, Travis, Drone, etc. all set `CI=true` automatically;
//     by reading that, pipelines need no extra config to suppress
//     the browser open (which would fail in headless environments
//     and litter the logs with errors).
//
// Returns true otherwise — the default-on behavior for local dev.
func shouldOpenBrowser(noOpen bool) bool {
	if noOpen {
		return false
	}
	return os.Getenv("CI") == ""
}

// runBuild handles `mar build [--target T] [--out DIR] [path]`.
//
// Behavior depends on the topology the project's main picks:
//   - App.frontend → static dist/ (HTML + JS + program.json).
//   - App.backend / App.fullstack → self-contained executable embedding
//     the cross-compiled mar-runtime stub for `target` plus a ZIP of
//     the project sources. Default target is the host OS/arch.
func runBuild(args []string) int {
	entry := "."
	target := ""
	outDir := ""
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--target" || a == "-t":
			if i+1 >= len(args) {
				fprintError("mar build: --target needs a value (e.g. linux-amd64)")
				return 2
			}
			target = args[i+1]
			i += 2
		case strings.HasPrefix(a, "--target="):
			target = strings.TrimPrefix(a, "--target=")
			i++
		case a == "--out" || a == "-o":
			if i+1 >= len(args) {
				fprintError("mar build: --out needs a value")
				return 2
			}
			outDir = args[i+1]
			i += 2
		case strings.HasPrefix(a, "--out="):
			outDir = strings.TrimPrefix(a, "--out=")
			i++
		case a == "-h" || a == "--help":
			fmt.Println(buildUsage())
			return 0
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "mar build: unknown flag %q\n", a)
			return 2
		default:
			entry = a
			i++
		}
	}

	// distDir defaults to "dist" alongside the entry (whether entry is
	// a file or a directory).
	if outDir == "" {
		baseDir := entry
		if info, err := os.Stat(entry); err == nil && !info.IsDir() {
			baseDir = filepath.Dir(entry)
		}
		outDir = filepath.Join(baseDir, "dist")
	}

	// iOS is its own pipeline: schema-driven scaffold, doesn't run
	// mar code at build time. Everything else (frontend / backend /
	// fullstack) goes through scaffold.Build.
	if target == "ios" {
		result, err := scaffold.BuildIOS(entry, outDir, version)
		if err != nil {
			var syntaxErr *project.ManifestSyntaxError
			var cfgErr *scaffold.IOSConfigError
			switch {
			case errors.As(err, &syntaxErr):
				printManifestSyntaxError(syntaxErr)
			case errors.As(err, &cfgErr):
				printIOSConfigError(cfgErr)
			default:
				fmt.Fprintln(os.Stderr, diag.Format(err))
			}
			return 1
		}
		printIOSBuildSummary(result)
		return 0
	}
	if err := scaffold.Build(entry, outDir, target); err != nil {
		// Different error categories get different formatting:
		//   - manifest syntax errors → typed printer with snippet +
		//     caret + hint pulled from the wrapped json.SyntaxError
		//   - production-config errors → typed printer (blanks +
		//     hints in cli-style.md §3 palette)
		//   - free-mail-domain errors → typed printer with hint
		//     about why providers reject these
		//   - source errors (parser / typecheck) → diag.Format's
		//     rich rendering with line numbers + snippets
		//   - everything else (manifest validation, I/O, …) →
		//     fprintError so it gets the red "Error:" prefix
		var syntaxErr *project.ManifestSyntaxError
		var pcErr *scaffold.ProductionConfigError
		var fmErr *project.FreeMailDomainError
		switch {
		case errors.As(err, &syntaxErr):
			printManifestSyntaxError(syntaxErr)
		case errors.As(err, &pcErr):
			printProductionConfigError(pcErr)
		case errors.As(err, &fmErr):
			printFreeMailDomainError("mar build", fmErr)
		case isSourceError(err):
			fmt.Fprintln(os.Stderr, diag.Format(err))
		default:
			fprintError("mar build: %v", err)
		}
		return 1
	}
	return 0
}

// printManifestSyntaxError pretty-prints a JSON parse failure on
// mar.json with file, line:column, snippet, caret, and a heuristic
// hint about the likely cause. The plain Error() string on the
// underlying type already contains the same information; this
// printer layers on colors (per cli-style.md §3) and the standard
// Error / Hint block spacing so it reads consistently with the
// other typed errors.
func printManifestSyntaxError(e *project.ManifestSyntaxError) {
	// Embed "at line N, column M:" into the same fprintError call as
	// the main message so they read as one sentence — they ARE one
	// sentence semantically (the location is part of the error), and
	// a blank line between them adds visual noise without helping.
	fprintError("%s is not valid JSON — %s\nat line %s, column %s:",
		colorMagenta(e.Path),
		e.HumanMessage,
		colorCyan(fmt.Sprintf("%d", e.Line)),
		colorCyan(fmt.Sprintf("%d", e.Column)))
	// fprintError already added the trailing blank, which serves as
	// the visual break between the narrative (Error + at line) and
	// the code block (snippet + caret). Snippet gets a 2-space indent
	// — the "code block" idiom — so the eye reads it as content from
	// the file, distinct from the narrative.
	fmt.Fprintln(os.Stderr, "  "+e.Snippet)
	caretCol := e.Column
	if caretCol < 1 {
		caretCol = 1
	}
	fmt.Fprintln(os.Stderr, strings.Repeat(" ", 2+caretCol-1)+colorRed("^"))
	// Force a blank between the snippet block and the next section;
	// fprintHint's state-machine leading-blank is skipped because
	// fprintError's trailing-blank set hasTrail=true. The closing
	// blank serves the same role when there's no hint to print.
	fmt.Fprintln(os.Stderr)
	if e.Hint != "" {
		fprintHint("%s", e.Hint)
	}
}

// printFreeMailDomainError pretty-prints the validation error
// for mail.from referencing a free-mail provider. Reused by the
// mar build path; mar fly's printManifestError has its own copy
// with the same shape.
func printFreeMailDomainError(prefix string, e *project.FreeMailDomainError) {
	fprintError("%s: %s %q uses a free-mail domain (%s).",
		prefix,
		colorMagenta("mail.from"),
		e.From,
		colorMagenta(e.Domain))
	// Multi-paragraph hint — the `\n\n      ` keeps the second
	// paragraph aligned under the Hint: indent but visually
	// separated. fprintHint adds the leading + trailing blanks
	// around the whole block as a single unit.
	fprintHint(
		"SMTP providers (Resend, SendGrid, AWS SES, etc.) only let you\n"+
			"      send from a domain you've verified with them via DKIM/SPF.\n"+
			"      Free-mail domains aren't yours, so the provider will reject\n"+
			"      every send.\n"+
			"\n"+
			"      Use a domain you own — e.g. %s — and verify it\n"+
			"      in your provider's dashboard.",
		colorCyan("notifications@my-app.com"))
}

// isSourceError reports whether err carries source-position info
// that diag.Format would render with a code-snippet block. Anything
// else gets the plain Error: prefix flow.
func isSourceError(err error) bool {
	// diag.SourceError is what `diag.Wrap` produces; the only path
	// to it is through user-source compilation errors. Manifest
	// validation, I/O, etc. never wrap into one.
	formatted := diag.Format(err)
	return formatted != err.Error()
}

// colorJSONSuggestion paints one line of a mar.json suggestion for
// the operator. Two visual signals beyond plain magenta:
//
//   - "..." placeholders dim — they're the only thing the user
//     actually has to replace; everything else (keys, env: refs,
//     braces) gets pasted verbatim. Dimming them says "I'm a stub,
//     fill me in" without making the whole snippet noisy.
//   - Everything else stays magenta (config slot color from
//     cli-style.md §3).
//
// Quotes around the placeholders stay magenta so the JSON shape
// is preserved visually — only the inside-of-quotes "..." dims.
func colorJSONSuggestion(line string) string {
	// Replace each "..." placeholder by an ANSI-bracketed dim run
	// inside the surrounding magenta. Done by tokenizing the line
	// on the literal `"..."` triple-dot string.
	const placeholder = `"..."`
	if !strings.Contains(line, placeholder) {
		return colorMagenta(line)
	}
	parts := strings.Split(line, placeholder)
	var b strings.Builder
	for i, p := range parts {
		b.WriteString(colorMagenta(p))
		if i < len(parts)-1 {
			b.WriteString(colorDim(placeholder))
		}
	}
	return b.String()
}

// printProductionConfigError pretty-prints the missing-config error
// from `mar build --target=<production>` with the cli-style.md §3
// palette. Multi-line block, blanks before/after (one-shot exit).
//
// Color choices (matching cli-style.md):
//
//	bold     section headers ("Add to mar.json:")
//	green    sample commands (mar fly provision, fly secrets set)
//	magenta  filesystem paths and config keys (mar.json, env:VAR)
//	cyan     literal numbers (587)
//	yellow   "Hint:" prefix (via fprintHint)
//
// printIOSBuildSummary renders the user-facing output of
// `mar build --target ios`. Lives here (in cmd/mar) rather than in
// internal/scaffold so the formatting logic has access to the
// color/blank-line helpers without scaffold needing to import a
// presentation package. scaffold.BuildIOS returns structured
// data; the formatting is this file's job.
//
// Sections, top-to-bottom:
//
//  1. Warn block when ios.serverUrl is missing (always optional —
//     dev runs work without it, but the operator needs to know
//     before shipping to TestFlight / App Store).
//  2. "iOS scaffold written" line — the output path in magenta so
//     it stands out as something the operator will cd into.
//  3. Metadata continuation lines — app name + bundle id, then
//     display name + marketing version + build number, then base
//     URL. Mirrors the shape of `buildServerExecutable`'s output:
//     the primary path on its own line, the details one level
//     indented underneath. Confirms what got baked in without
//     forcing the operator to crack open Info.plist / pbxproj.
//  4. The `open ...xcodeproj` command in green, the one thing the
//     operator runs next.
func printIOSBuildSummary(r scaffold.IOSBuildResult) {
	if r.MissingServerURL {
		// Warn states the problem (what's missing, what falls back
		// to defaults). Hint states the fix with paste-ready JSON
		// for mar.json, formatted multi-line with key-per-line so
		// the operator can paste straight into a `{ ... }` block.
		// The JSON sits 2 spaces deeper than the Hint body (so 8
		// total) — matching how nested code reads in the surrounding
		// terminal.
		fprintWarn(
			"%s is not set in %s. The generated app will\n"+
				"      default to %s in DEBUG (with Bonjour\n"+
				"      discovery) and have no production backend in RELEASE.",
			colorMagenta("ios.serverUrl"),
			colorMagenta("mar.json"),
			colorCyan("http://localhost:3000"))
		// If mar.json declares deploy.fly.app, use that slug to
		// build the suggested URL — operator can paste verbatim
		// instead of swapping `my-app` for their real name.
		suggestedURL := "https://my-app.fly.dev"
		if mf, err := project.LoadManifestStructure(r.ProjectDir); err == nil &&
			mf != nil && mf.Deploy != nil && mf.Deploy.Fly != nil && mf.Deploy.Fly.App != "" {
			suggestedURL = "https://" + mf.Deploy.Fly.App + ".fly.dev"
		}
		fprintHint(
			"add to %s before shipping to TestFlight / App Store:\n"+
				"        %s\n"+
				"        %s\n"+
				"        %s",
			colorMagenta("mar.json"),
			colorJSONSuggestion(`"ios": {`),
			colorJSONSuggestion(`  "serverUrl": "`+suggestedURL+`"`),
			colorJSONSuggestion(`}`))
	} else {
		// Happy-path: no Warn/Hint block to provide the leading
		// blank. Emit one manually so the `[mar build]` line stands
		// off from the shell prompt.
		fmt.Println()
	}

	fmt.Printf("[mar build] iOS scaffold written to %s\n", colorMagenta(r.OutputDir))
	// Metadata block — one field per line. The earlier layout packed
	// two fields per line (app + bundle, display + version), which
	// read tightly when values were short but became uncomfortable
	// once a bundleId or displayName ran long enough to wrap. One
	// field per line stays scannable at any length and reads as a
	// proper "manifest" block.
	//
	// base url gets the " (default)" tag when ios.serverUrl wasn't
	// set — the warn block above already told the full story, this
	// is just an unambiguous tag on the value the bundle carries.
	baseURLNote := ""
	if r.MissingServerURL {
		baseURLNote = colorDim(" (default)")
	}
	fmt.Printf("            app:      %s\n", colorCyan(r.AppName))
	fmt.Printf("            bundle:   %s\n", colorCyan(r.BundleID))
	fmt.Printf("            display:  %s\n", colorCyan(r.DisplayName))
	fmt.Printf("            version:  %s (build %s)\n",
		colorCyan(r.MarketingVersion),
		colorCyan(r.BuildNumber))
	fmt.Printf("            base url: %s%s\n",
		colorCyan(r.BaseURL),
		baseURLNote)
	// "Next steps" header matches the shape used by `mar fly init`:
	// bold header, numbered list, shellable command in green. Keeps
	// the actionable bits visually separate from the (passive)
	// scaffold-metadata block above.
	//
	// The `open` command follows the project-wide convention for
	// shell commands: executable in green, arguments in bold
	// (matching the `mar X Y` pattern enforced via cmdSuggest).
	// Applies to non-mar binaries too — `open` is the executable
	// here, the .xcodeproj path is the argument.
	//
	// Step 2 (signing team) only matters the first time the project
	// opens — Xcode persists the choice in the .pbxproj. The
	// "(first time only)" annotation keeps repeat builders from
	// thinking they have to revisit Signing & Capabilities each
	// time. Even simulator builds need a team set since Xcode 16
	// (the "Signing for X requires a development team" error fires
	// on the very first Run otherwise).
	fmt.Println()
	fmt.Println(colorBold("Next steps:"))
	fmt.Printf("  1. %s %s\n",
		colorGreen("open"),
		colorBold(filepath.Join(r.OutputDir, "*.xcodeproj")))
	fmt.Println("  2. Set your signing team in Signing & Capabilities (first time only)")
	fmt.Println("  3. Pick a simulator or device, then ▶ Run")
	fmt.Println()
}

// printIOSConfigError formats a `mar build --target ios` validation
// failure where mar.json's `ios` block is missing required fields.
// Renders a paste-ready JSON snippet so the operator can copy it
// into mar.json and retry without re-reading the docs.
//
// Only the missing fields appear in the snippet — if the operator
// already set serverUrl but forgot bundleId, we don't echo serverUrl
// back at them (avoids implying they need to overwrite what's there).
func printIOSConfigError(e *scaffold.IOSConfigError) {
	fprintError("%s is missing required fields in %s: %s.",
		colorMagenta("ios"),
		colorMagenta("mar.json"),
		colorMagenta(strings.Join(e.Missing, ", ")))
	fmt.Fprintln(os.Stderr,
		"Paste the block below into "+colorMagenta("mar.json")+
			" and adjust each value")
	fmt.Fprintln(os.Stderr,
		"to match your project.")
	fmt.Fprintln(os.Stderr)
	// Build the JSON suggestion. Each missing field becomes one line
	// inside the `"ios": { ... }` block, in the canonical order
	// (bundleId, displayName, marketingVersion, buildNumber) — even
	// though e.Missing is already ordered, we re-walk a fixed list to
	// keep the snippet stable regardless of which fields are missing.
	fmt.Fprintln(os.Stderr, "  "+colorJSONSuggestion(`"ios": {`))
	canonical := []string{"bundleId", "displayName", "marketingVersion", "buildNumber"}
	var lines []string
	for _, k := range canonical {
		if v, ok := e.Suggestions[k]; ok {
			lines = append(lines, fmt.Sprintf(`  "%s": "%s"`, k, v))
		}
	}
	for i, line := range lines {
		suffix := ","
		if i == len(lines)-1 {
			suffix = ""
		}
		fmt.Fprintln(os.Stderr, "  "+colorJSONSuggestion(line+suffix))
	}
	fmt.Fprintln(os.Stderr, "  "+colorJSONSuggestion(`}`))
	fmt.Fprintln(os.Stderr)
}

// JSON suggestions are emitted multi-line so the operator can paste
// straight into mar.json without having to reformat.
func printProductionConfigError(e *scaffold.ProductionConfigError) {
	fprintError("production build requires auth and mail config in %s.",
		colorMagenta("mar.json"))
	// fprintError already added the trailing blank.
	fmt.Fprintln(os.Stderr,
		"Your project uses "+colorGreen("Auth.config")+
			" which sends sign-in emails. The runtime")
	fmt.Fprintln(os.Stderr,
		"needs persistent secrets and a real SMTP provider in production —")
	fmt.Fprintln(os.Stderr,
		"without them, every sign-in attempt would fail.")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, colorBold("Add to "+colorMagenta("mar.json")+":"))
	fmt.Fprintln(os.Stderr)
	for entryIdx, line := range e.Missing {
		// Each entry is a top-level JSON property suggestion. Append
		// a comma to the closing line of every entry except the last
		// so the snippets paste into mar.json as valid JSON.
		isLast := entryIdx == len(e.Missing)-1
		subs := strings.Split(line, "\n")
		for subIdx, sub := range subs {
			suffix := ""
			if !isLast && subIdx == len(subs)-1 {
				suffix = ","
			}
			fmt.Fprintln(os.Stderr, "  "+colorJSONSuggestion(sub+suffix))
		}
	}
	fmt.Fprintln(os.Stderr)
	// Multiple related hints under a single "Hints:" header so the
	// section reads as a list (instead of two stacked "Hint:" blocks
	// that would compete visually).
	fmt.Fprintln(os.Stderr, colorYellow("Hints:"))
	fmt.Fprintf(os.Stderr, "  - %s is optional and defaults to %s (Resend, SendGrid,\n",
		colorMagenta("smtpPort"), colorCyan("587"))
	fmt.Fprintln(os.Stderr, "    Mailgun, AWS SES, Postmark, Brevo, Mailjet all use it).")
	fmt.Fprintf(os.Stderr, "  - %s and %s MUST be %s — push\n",
		colorMagenta("sessionSecret"), colorMagenta("smtpPassword"),
		colorMagenta("env:VAR_NAME"))
	fmt.Fprintf(os.Stderr, "    the actual values reach Fly automatically on the next %s.\n",
		cmdSuggest("fly deploy"))
	fmt.Fprintf(os.Stderr, "  - For %s: %s is %s, %s is\n",
		colorCyan("Resend"), colorMagenta("smtpHost"),
		colorCyan(`"smtp.resend.com"`), colorMagenta("smtpUsername"))
	fmt.Fprintf(os.Stderr, "    %s (literal), and %s is your API key from\n",
		colorCyan(`"resend"`), colorMagenta("smtpPassword"))
	fmt.Fprintln(os.Stderr, "    https://resend.com/api-keys.")
	fmt.Fprintln(os.Stderr)
}

// buildUsage returns the help text for `mar build --help`. Same
// palette as the root `mar` usage in main.go: only the literal `mar`
// binary name is green; subcommand names, flags and arg placeholders
// are bold; paths magenta; headers bold.
func buildUsage() string {
	bin := colorGreen("mar")
	name := func(s string) string { return colorBold(s) }
	path := func(s string) string { return colorMagenta(s) }
	hdr := func(s string) string { return colorBold(s) }
	run := func(rest string) string { return bin + " " + name(rest) }
	return "Usage: " + run("build") + " " + name("[--target <T>] [--out <dir>] [path]") + "\n" +
		"\n" +
		"Compile a mar project to a deployable artifact.\n" +
		"\n" +
		hdr("For App.frontend projects:") + "\n" +
		"  Writes a static " + path("dist/") + " (" + path("index.html") + " + " + path("runtime.js") + " + " + path("program.json") + ") to " + name("<dir>") + ".\n" +
		"\n" +
		hdr("For App.backend / App.fullstack projects:") + "\n" +
		"  Writes a self-contained executable to " + path("<dir>/<projectName>") + " by\n" +
		"  concatenating the cross-compiled mar-runtime stub for " + name("<target>") + " with\n" +
		"  a ZIP payload of the project sources + " + path("mar.json") + ". The resulting\n" +
		"  binary needs no mar toolchain on the deploy host — just run it.\n" +
		"\n" +
		hdr("For --target ios:") + "\n" +
		"  Generates a disposable Xcode project under " + path("<dir>/<AppName>/") + ". The\n" +
		"  Swift app discovers your backend via " + colorCyan("/_mar/schema") + " on cold start, so\n" +
		"  changing your mar code updates the app over the air without\n" +
		"  re-submitting to the App Store. Open the .xcodeproj in Xcode.\n" +
		"\n" +
		"  In DEBUG (Xcode debug-build) the app uses Bonjour to find your\n" +
		"  " + run("dev") + " server on the local network. In RELEASE (TestFlight /\n" +
		"  App Store) it talks only to " + path("mar.json") + "'s " + path(`"ios.serverUrl"`) + " — edit the\n" +
		"  manifest to point at a different backend.\n" +
		"\n" +
		hdr("Flags:") + "\n" +
		"  " + name("--target, -t") + "   Build target. Native: darwin-amd64, darwin-arm64,\n" +
		"                 linux-amd64, linux-arm64, windows-amd64. Mobile: ios.\n" +
		"                 Defaults to the host OS/arch.\n" +
		"  " + name("--out, -o") + "      Output directory (default: " + path("<project>/dist") + ").\n" +
		"\n" +
		"Path defaults to \".\" (" + path("Main.mar") + " in the current directory)."
}

// runFormat handles `mar format [--check] <files...>`. With files,
// each is rewritten in place. With --check, the command exits 1 if
// any file would change — useful in CI to enforce formatting.
func runFormat(args []string) int {
	check := false
	files := []string{}
	for _, a := range args {
		switch a {
		case "--check":
			check = true
		case "-h", "--help":
			fmt.Println("usage: mar format [--check] <file.mar> [file.mar...]")
			return 0
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(os.Stderr, "mar format: unknown flag %q\n", a)
				return 2
			}
			files = append(files, a)
		}
	}
	if len(files) == 0 {
		fprintError("mar format: no files given")
		fmt.Fprintf(os.Stderr, "usage: %s\n",
			cmdSuggest("format [--check] <file.mar> [file.mar...]"))
		fmt.Fprintln(os.Stderr)
		return 2
	}
	dirty := 0
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			fprintError("mar format: %v", err)
			return 1
		}
		formatted := formatter.Format(string(src))
		if formatted == string(src) {
			continue
		}
		dirty++
		if check {
			fmt.Fprintf(os.Stderr, "%s: needs formatting\n", file)
			continue
		}
		if err := os.WriteFile(file, []byte(formatted), 0o644); err != nil {
			fprintError("mar format: %v", err)
			return 1
		}
		fmt.Printf("formatted %s\n", file)
	}
	if check && dirty > 0 {
		return 1
	}
	return 0
}

// lookupMainType finds the type of the entry module's `main`. Module
// names vary (`Main`, `Calculator`, etc.) — keys in valueTypes are
// "Module.value" — so just pick the first ".main" entry. There's only
// ever one `main` in a project (the entry's), so this is unambiguous.
func lookupMainType(valueTypes map[string]typecheck.Type) typecheck.Type {
	if t, ok := valueTypes["Main.main"]; ok {
		return t
	}
	for k, t := range valueTypes {
		if strings.HasSuffix(k, ".main") {
			return t
		}
	}
	return nil
}

// checkMainSignature reports whether t is `Effect ()`. Returns an empty
// string when the signature is acceptable, else a short human-readable
// message describing the mismatch. Wrapping in a `forall` is fine — main
// can be polymorphic in unused variables.
func checkMainSignature(t typecheck.Type) string {
	if f, ok := t.(typecheck.TForall); ok {
		t = f.Body
	}
	con, ok := t.(typecheck.TCon)
	if !ok || con.Name != "Effect" || len(con.Args) != 1 {
		return fmt.Sprintf("main has type `%s`, expected `Effect ()`", typecheck.Pretty(t))
	}
	if _, uOk := con.Args[0].(typecheck.TUnit); !uOk {
		return fmt.Sprintf("main has type `%s`, expected `Effect ()` (success value must be unit `()`)", typecheck.Pretty(t))
	}
	return ""
}

// version and commit are populated at build time via -ldflags.
// See Makefile.
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr)
		usage()
		fmt.Fprintln(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "check":
		if len(os.Args) < 3 {
			fprintError("mar check: missing file argument")
			os.Exit(2)
		}
		os.Exit(runCheck(os.Args[2]))
	case "repl":
		os.Exit(runRepl())
	case "config":
		if len(os.Args) < 3 {
			fprintError("mar config: missing project directory")
			os.Exit(2)
		}
		os.Exit(runConfig(os.Args[2]))
	case "dev":
		noOpen, rest := extractNoOpenFlag(os.Args[2:])
		path := "."
		if len(rest) >= 1 {
			path = rest[0]
		}
		os.Exit(runDev(path, noOpen))
	case "format":
		// `mar format <file>` — rewrite in place. `mar format --check`
		// exits 1 if any file needs reformatting (CI-friendly).
		os.Exit(runFormat(os.Args[2:]))
	case "lsp":
		// Language server over stdio. Editors (VSCode, etc.) launch
		// `mar lsp` and pipe LSP JSON-RPC over stdin/stdout.
		if err := lsp.RunStdio(); err != nil {
			fprintError("mar lsp: %v", err)
			os.Exit(1)
		}
	case "init":
		if len(os.Args) < 3 {
			// One-shot exit. fprintError already adds leading +
			// trailing blanks; the usage line goes between Error
			// and the final blank.
			fprintError("mar init: missing project name")
			fmt.Fprintf(os.Stderr, "usage: %s\n", cmdSuggest("init <name>"))
			fmt.Fprintln(os.Stderr)
			os.Exit(2)
		}
		name := os.Args[2]
		kind := promptInitKind()
		if err := scaffold.Init(name, kind); err != nil {
			fprintError("mar init: %v", err)
			os.Exit(1)
		}
		// "Created <dir>" + a one-line summary of the scaffold +
		// a Hint: block with the next command to run. Same visual
		// shape used everywhere else in the CLI (yellow Hint:
		// prefix, blank-line-coordinated via clio, `mar` in green
		// per cli-style §3) so the user's eye already knows where
		// to look.
		fmt.Println()
		fmt.Printf("Created %s\n", colorMagenta(name+"/"))
		if desc := scaffold.Description(kind); desc != "" {
			fmt.Println(colorDim(desc))
		}
		// Hint header alone on its own line; command indented 6 spaces
		// on the next line to align with the standard multi-line Hint
		// continuation used elsewhere (cloudflarepages.go, etc.).
		// `cd` and `mar` both treated as runnable verbs (green); the
		// directory name and `dev` subcommand are arguments (bold).
		fprintHint("\n      %s %s && %s %s",
			colorGreen("cd"),
			colorBold(name),
			colorGreen("mar"),
			colorBold("dev"))
	case "build":
		os.Exit(runBuild(os.Args[2:]))
	case "migrate":
		os.Exit(runMigrate(os.Args[2:]))
	case "fly":
		os.Exit(runFly(os.Args[2:]))
	case "cloudflare-pages":
		os.Exit(runCloudflarePages(os.Args[2:]))
	case "admin":
		os.Exit(runAdmin(os.Args[2:]))
	case "completion":
		os.Exit(runCompletion(os.Args[2:]))
	case "version", "--version", "-v":
		fmt.Printf("%s (%s)\n", version, commit)
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stderr)
		usage()
		fmt.Fprintln(os.Stderr)
	default:
		// `mar foo.mar` or `mar examples/notes-auth` look like the
		// user wanted to run a project but forgot the subcommand.
		// Don't infer — make the intent explicit. Suggest the right
		// command and exit non-zero so scripts notice.
		arg := os.Args[1]
		// Before treating arg as a path, check if it's a known
		// sub-subcommand name (deploy, logs, status, secrets, ...).
		// Suggesting `mar dev deploy` when the operator typed `mar
		// deploy` is actively misleading — it implies a top-level
		// `deploy` exists, and on top of that the looksLikePath
		// check can return true just because a directory named
		// "deploy" happens to exist in the cwd (very common).
		// Surface the real path (`mar fly deploy`) instead.
		if parent := parentForSubcommand(arg); parent != "" {
			fprintError("mar: %q is not a top-level command.", arg)
			fprintHint("did you mean %s?", cmdSuggest(parent+" "+arg))
			os.Exit(2)
		}
		if looksLikePath(arg) {
			fprintError("mar: %q is not a command.", arg)
			fprintHint("to run a project, type %s.", cmdSuggest(fmt.Sprintf("dev %s", arg)))
			os.Exit(2)
		}
		fprintError("mar: unknown command %q.", arg)
		fprintHint("run %s for the command list.", cmdSuggest("help"))
		os.Exit(2)
	}
}

// parentForSubcommand maps a sub-subcommand name (e.g. "deploy",
// "logs") to the top-level command it lives under (e.g. "fly"), so
// `mar deploy` can suggest the real path `mar fly deploy` instead
// of either "unknown command" or the misleading "run as a project"
// hint. Returns "" when the name isn't a known sub-subcommand.
//
// Kept in lockstep with the dispatchers in fly.go / fly_database.go /
// fly_secrets.go / cloudflarepages.go / admin.go / migrate.go — if a
// new sub is added there, mirror it here so the typo hint stays
// useful.
//
// `deploy` is the only sub shared by two parents (fly + cloudflare-
// pages); pickDeployParent peeks at ./mar.json to suggest whichever
// one the project is configured for. The rest are unambiguous.
func parentForSubcommand(name string) string {
	switch name {
	case "deploy":
		return pickDeployParent()
	case "preview", "destroy", "logs", "status", "secrets":
		return "fly"
	case "backup", "backups":
		return "fly database"
	case "database", "db":
		return "fly"
	case "plan":
		return "migrate"
	}
	return ""
}

// pickDeployParent peeks at ./mar.json to decide whether `mar deploy`
// should suggest `mar fly deploy` or `mar cloudflare-pages deploy`.
// Returns "cloudflare-pages" only when the manifest declares the
// cloudflare-pages block AND not the fly block. Every other case
// (no manifest, both blocks, only fly, neither block) falls back to
// "fly" — fly is the older deploy target and the safer default when
// intent is ambiguous.
func pickDeployParent() string {
	m, err := project.LoadManifestStructure(".")
	if err != nil || m == nil || m.Deploy == nil {
		return "fly"
	}
	if m.Deploy.CloudflarePages != nil && m.Deploy.Fly == nil {
		return "cloudflare-pages"
	}
	return "fly"
}

// looksLikePath reports whether `s` looks like a path attempt —
// contains a separator, ends in .mar, or actually exists on disk.
// Used to give a more specific "did you mean" hint when the user
// almost-but-not-quite typed a real command.
func looksLikePath(s string) bool {
	if strings.HasSuffix(s, ".mar") {
		return true
	}
	if strings.ContainsAny(s, "/\\") {
		return true
	}
	if _, err := os.Stat(s); err == nil {
		return true
	}
	return false
}

// usage prints the colored help block for `mar` (no args / `mar help`).
// Caller is responsible for surrounding blank lines (this is shared by
// the no-args branch and the explicit `mar help` branch, both of which
// are one-shot exits).
//
// Color scheme (docs/cli-style.md §3):
//
//	bold     section headers
//	green    subcommand names + sample commands
//	magenta  filesystem paths and config keys (mar.json)
func usage() {
	// Two distinct command stylings:
	//
	//   exe  — the `mar` executable itself and full command lines like
	//          `mar <command> --help`. Green, matching the project-wide
	//          "commands to run" convention used everywhere else (see
	//          docs/cli-style.md §3).
	//
	//   name — command names in the Commands: list (dev, build, etc.).
	//          Bold without color: a list of names is structural, not
	//          actionable in the same way an inline "run THIS" is.
	//          Bold green became visually loud once the help expanded
	//          to two lines per command.
	exe := func(s string) string { return colorGreen(s) }
	name := func(s string) string { return colorBold(s) }
	path := func(s string) string { return colorMagenta(s) }
	hdr := func(s string) string { return colorBold(s) }

	// Two-line layout per command: the signature (name + args) on the
	// first line, the description indented on the second. Trades a
	// blank line per command for consistent alignment that doesn't
	// break when a subcommand argument list grows (fly's subcommands,
	// build's flag list, etc.).
	//
	// Single source of truth: `entries` below. If you add a new
	// top-level command, just append a row here — the renderer takes
	// care of the rest.
	type cmdEntry struct{ name, args, desc string }
	entries := []cmdEntry{
		{"dev", "[path]", `Run with hot reload (path defaults to ".")`},
		{"build", "[path] [--target T] [--out DIR]", "Compile to dist/ (frontend) or binary (backend)"},
		{"init", "<name>", "Scaffold a new project at <name>/"},
		{"check", "<path>", "Parse + type-check (no run)"},
		{"format", "[--check] <file>...", "Reformat .mar files in place"},
		{"config", "<dir>", "Print " + path("mar.json")},
		{"migrate", "<plan|status> [path]", "Show pending / applied schema migrations (read-only)"},
		{"fly", "<init|provision|deploy|destroy|logs|status|admin|database> [path]", "Full Fly.io deployment workflow"},
		{"cloudflare-pages", "deploy [path]", "Deploy a static App.frontend bundle to Cloudflare Pages"},
		{"admin", "<add|remove|list> [args]", "Manage admin panel access (" + path("mar.json") + " admins list)"},
		{"repl", "", "Interactive REPL"},
		{"lsp", "", "Language server over stdio"},
		{"completion", "<shell>", "Generate shell completion (zsh, bash, fish)"},
		{"version", "", "Print version"},
	}

	fmt.Fprintln(os.Stderr, "Usage: "+exe("mar")+" "+name("<command> [args]"))
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, hdr("Commands:"))
	for i, e := range entries {
		if i > 0 {
			fmt.Fprintln(os.Stderr)
		}
		// First line: "  name args" (or just "  name" when no args).
		sig := "  " + name(e.name)
		if e.args != "" {
			sig += " " + e.args
		}
		fmt.Fprintln(os.Stderr, sig)
		// Second line: 6-space indent for the description, lining up
		// nicely under the command name.
		fmt.Fprintln(os.Stderr, "      "+e.desc)
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Run "+exe("mar")+" "+name("<command> --help")+" for command-specific help.")
}

func runCheck(path string) int {
	info, err := os.Stat(path)
	if err != nil {
		printError("mar check", err)
		return 1
	}
	if info.IsDir() {
		proj, err := project.Load(path)
		if err != nil {
			printError("", err)
			return 1
		}
		// `project.Load` only loads the .mar modules — validate mar.json
		// too, so a typo'd/misplaced config key (e.g. top-level "port"
		// instead of "server"."port") is caught here, matching what
		// `mar dev` and `mar build` enforce. Without this it passes
		// `mar check` clean and silently does the wrong thing later.
		if _, err := project.LoadManifestStructure(path); err != nil {
			var syntaxErr *project.ManifestSyntaxError
			if errors.As(err, &syntaxErr) {
				printManifestSyntaxError(syntaxErr)
			} else {
				printError("mar check", err)
			}
			return 1
		}
		fmt.Printf("project %s — OK (%d modules)\n", path, len(proj.Modules))
		for _, name := range proj.Order {
			m := proj.Modules[name]
			fmt.Printf("  %s\n", m.Name)
			for vname, t := range m.ValueTypes {
				fmt.Printf("    %s : %s\n", vname, typecheck.Pretty(t))
			}
		}
		return 0
	}
	src, err := os.ReadFile(path)
	if err != nil {
		printError("mar check", err)
		return 1
	}
	mod, err := parser.Parse(string(src))
	if err != nil {
		printError("", diag.Wrap(path, string(src), err))
		return 1
	}
	res, err := typecheck.CheckModule(mod)
	if err != nil {
		printError("", diag.Wrap(path, string(src), err))
		return 1
	}
	fmt.Printf("module %s — OK\n", joinModuleName(mod.Name))
	if len(res.TypeAliases) > 0 {
		fmt.Println("\nType aliases:")
		for name := range res.TypeAliases {
			fmt.Printf("  %s\n", name)
		}
	}
	if len(res.CustomTypes) > 0 {
		fmt.Println("\nCustom types:")
		for name, ct := range res.CustomTypes {
			fmt.Printf("  %s = %s\n", name, joinCtors(ct.CtorOrder))
		}
	}
	if len(res.ValueTypes) > 0 {
		fmt.Println("\nValues:")
		for name, t := range res.ValueTypes {
			fmt.Printf("  %s : %s\n", name, typecheck.Pretty(t))
		}
	}
	return 0
}

// runDev evaluates `main` in dev mode. Path can be a .mar file (single-file
// app) or a directory containing Main.mar. Defaults to "." when called with
// no arguments.
//
// Whether the runtime serves a unified server, a browser-only app, or
// something else is decided by what `main` returns:
//
//	App.fullstack { api, page }   -> unified server (port from mar.json)
//	App.serve port app            -> browser-only single-screen app
//	App.serveScreens port screens -> browser-only multi-screen app
//	any other Effect              -> just runs the effect
//
// `mar dev` keeps the HTTP server up, watches the project files, and on
// every change re-runs the project to swap in the freshly compiled output
// (via a LiveProgram shared between the watcher and the server). Browsers
// stay connected via SSE on /_mar/reload and rebuild their DOM when a
// reload event fires.
func runDev(path string, noOpen bool) int {
	entryFile, projectDir, err := resolveDevEntry(path)
	if err != nil {
		printError("mar dev", err)
		return 1
	}

	// Resolve port from mar.json (default 3000). Same value used by
	// App.fullstack / App.serve / App.serveScreens — the language no
	// longer takes port as a code argument. Validation errors in the
	// manifest are fatal: surface them now rather than fall back silently.
	port := 3000
	// LoadManifestDev tolerates missing env vars (resolves them to
	// empty) so the operator can run `mar dev` locally without
	// configuring production secrets. Auth flows fall back to
	// printing codes to the terminal when SMTP isn't fully wired —
	// mailer.Send detects empty Host/Password and uses its stdout
	// sink. Production paths (mar-runtime, mar fly *) stay strict
	// via LoadManifest.
	manifest, err := project.LoadManifestDev(projectDir)
	if err != nil {
		printError("mar dev", err)
		return 1
	}
	if manifest != nil && manifest.Server != nil && manifest.Server.Port != 0 {
		port = manifest.Server.Port
	}

	// Wire the SQLite path into the runtime — Repo.* lazy-opens this on
	// first use. ResolveDatabasePath honors MAR_DATABASE_PATH (override
	// for production deploys) and resolves relative paths against the
	// project directory so `./notes.db` lands next to Main.mar.
	dbPath, _ := project.ResolveDatabasePath(manifest, projectDir)
	if dbPath != "" {
		runtime.SetDBPath(dbPath)
	}

	// Auth: derive (or auto-generate, in dev) the session secret and
	// pass the SMTP credentials to the auth runtime. The handlers stay
	// dormant until the user's program calls `Auth.config` — at that
	// point ServeLive sees a registered VAuth and mounts /_auth/*.
	secret, secretSrc, err := project.ResolveSessionSecret(manifest, projectDir)
	if err != nil {
		printError("mar dev", err)
		return 1
	}
	if secret != "" {
		jsserve.SetAuthRuntime(secret, project.ToSMTPConfig(manifest))
		_ = secretSrc // available for diagnostics if we want to log it later
		if manifest != nil && manifest.Mail != nil {
			jsserve.SetAdminMailFrom(manifest.Mail.From)
		}
	}
	jsserve.SetAdminBuildInfo(version)
	jsserve.SetAdminRequestBufferSize(project.ResolvedRecentRequestsSize(manifest))

	// Gateway rate limiter — always on, per-IP, configured via
	// mar.json["rateLimit"]. validateRateLimit already ensured the
	// values are in bounds (or applied defaults). Rate is converted
	// from per-minute (the operator-friendly unit) to per-second
	// (the token-bucket internal unit).
	var rateLimitCfg *project.RateLimitConfig
	if manifest != nil {
		rateLimitCfg = manifest.RateLimit
	}
	jsserve.SetRateLimit(ratelimit.New(ratelimit.Policy{
		Rate:  float64(rateLimitCfg.ResolvedRequestsPerMinute()) / 60.0,
		Burst: rateLimitCfg.ResolvedBurst(),
	}))

	// Static assets: files in the project's public/ folder are served
	// verbatim at the site root (e.g. public/logo.svg → /logo.svg).
	// `mar build` copies the same folder into dist/, so paths match in
	// dev and in a deployed bundle. Set unconditionally — a missing
	// folder just means no files match.
	//
	// First reject files that collide with Mar's reserved namespace — the
	// same check `mar build` runs — so a colliding asset fails fast here
	// instead of being silently shadowed in dev and only rejected at build.
	publicDir := filepath.Join(projectDir, "public")
	if err := jsserve.ValidatePublicDir(publicDir); err != nil {
		printError("mar dev", err)
		return 1
	}
	jsserve.SetPublicDir(publicDir)

	// PWA: validate the icon (fail fast on a bad master) and install
	// the resolved config so /_mar/manifest.json + /_mar/icon-*.png
	// serve. Always on for frontends — every app is installable.
	if err := project.ValidatePWAIcon(projectDir, manifest); err != nil {
		printError("mar dev", err)
		return 1
	}
	jsserve.SetPWA(manifest.ResolvePWA(projectDir))

	// Per-request body cap. validateServer already enforced bounds;
	// the resolver returns the documented default for nil/zero.
	var serverCfg *project.ServerConfig
	if manifest != nil {
		serverCfg = manifest.Server
	}
	jsserve.SetMaxBodyBytes(serverCfg.ResolvedMaxBodyBytes())

	// Auto-backup scheduler — periodic VACUUM INTO into a catalog
	// directory alongside mar.db. No-op when the manifest disables
	// it (`database.autoBackup.enabled: false`) or when there's no
	// database configured. Runs in dev too so the developer sees
	// the catalog grow alongside their app — same code path as
	// production, no surprises at deploy time.
	if dbPath != "" {
		// The scheduler gets its OWN connection (not the app's
		// single-connection pool) so VACUUM INTO runs concurrently
		// under WAL without stalling request handlers.
		if backupDB, openErr := runtime.OpenSnapshotDB(dbPath); openErr == nil {
			admin.MaybeStartAutoBackup(
				context.Background(),
				backupDB, manifest, projectDir, dbPath, version,
			)
		}
		// Hold the DB advisory lock for the process lifetime.
		// Prevents a second `mar dev` against the same project (the
		// second instance would otherwise fight the first over SQLite
		// writes), and signals `mar-runtime restore-db` that the DB
		// is in use. Released by the kernel on exit.
		if err := runtime.HoldDBLock(dbPath); err != nil {
			if errors.Is(err, runtime.ErrDBLocked) {
				fprintError("mar dev: database %s is locked by another process (another `mar dev` running against this project? a restore in progress?)", dbPath)
			} else {
				fprintError("mar dev: database lock: %v", err)
			}
			return 1
		}
	}

	lp := &jsserve.LiveProgram{}
	lp.SetDevMode(true)
	if manifest != nil && manifest.Name != "" {
		lp.SetAppName(manifest.Name)
	}
	hub := jsserve.NewReloadHub(lp)

	// compile loads + evaluates the project, capturing the served state
	// into lp. Returns a friendly error message instead of panicking so
	// the watcher can recover from compile errors during development.
	compile := func() error {
		// Hot-reload re-runs compile; clear every package-level
		// registry that shouldn't survive across reloads (entity
		// table, migration cache, Path enum types). See
		// runtime.ResetForReload for the full list.
		runtime.ResetForReload()

		rEnv, mods, valueTypes, err := project.LoadIntoEnvWithModulesAndHook(entryFile,
			func(env *runtime.Env, mods []*ast.Module) {
				apphost.Install(env, mods, port, lp)
			})
		if err != nil {
			return err
		}

		// Find main and validate its type signature. mar is a web language
		// — every entry point ships through `main : Effect String ()`,
		// where the Effect chooses the topology by calling App.frontend /
		// App.backend / App.fullstack. Reject anything else here so users
		// get a clear up-front error instead of confusing runtime
		// behavior.
		mainVal, ok := project.LookupMain(rEnv, mods)
		if !ok {
			return newHintedError(
				"%s must export a `main` value",
				"Add a top-level declaration: `main : Effect String ()`.\n"+
					"It typically calls one of the topology builders — App.frontend / App.backend / App.fullstack.",
				entryFile)
		}
		if mainType := lookupMainType(valueTypes); mainType != nil {
			if msg := checkMainSignature(mainType); msg != "" {
				return newHintedError(
					"%s: %s",
					"mar entry points must be `main : Effect String ()` and pick a topology with App.frontend / App.backend / App.fullstack.",
					entryFile, msg)
			}
		}
		eff, ok := mainVal.(runtime.VEffect)
		if !ok {
			return newHintedError(
				"main is not an Effect (got %T)",
				"`mar dev` runs servers and UIs via App.frontend / App.backend / App.fullstack.\n"+
					"Make `main` an Effect that calls one of those (e.g. `main = App.fullstack { ... }`).",
				mainVal)
		}
		// Running the Effect calls one of the overridden builtins, which
		// captures (api, pages) and updates lp. The builtin's Effect is a
		// no-op — we drive the server lifecycle from the CLI, not the
		// user's main.
		if _, err := eff.Run(); err != nil {
			return err
		}
		// Apply any pending schema migrations before the listener
		// accepts traffic. Hot-reloads also pass through here, so
		// editing an entity declaration triggers an immediate diff
		// + apply (no restart needed). Migrator silences the
		// no-change case to keep the dev loop quiet.
		if err := runtime.RunBootMigrations(); err != nil {
			return err
		}
		// Admin panel boot: ensure framework tables + sync from
		// mar.json["admins"]. Reloads pass through here too so
		// editing the admins list triggers an immediate re-sync.
		if err := bootAdminPanel(manifest); err != nil {
			return err
		}
		return nil
	}

	// First compile must succeed — otherwise there's nothing to serve.
	if err := compile(); err != nil {
		printError("", err)
		return 1
	}
	if lp.Port() == 0 {
		// `main` didn't call any of the App.* overrides — nothing to host.
		// Just exit; this isn't a server.
		fprintError("mar dev: main returned without invoking App.serve / App.fullstack / App.serveScreens — nothing to host")
		return 0
	}

	// Start the watcher in the background. Compile errors stay visible
	// in the terminal but don't tear the server down: the previous good
	// version stays in lp.
	go watchAndReload(projectDir, compile, hub, lp)

	// Open the browser to the dev URL only after ServeLive confirms
	// the bind succeeded — passed as a callback so the open never
	// fires when another `mar dev` already holds the port. The
	// `--no-open` flag and a CI=true environment both suppress this
	// (see shouldOpenBrowser).
	url := fmt.Sprintf("http://localhost:%d", port)
	onReady := func() {
		if !shouldOpenBrowser(noOpen) {
			return
		}
		openURL(url)
	}

	// Block on the HTTP server.
	if err := jsserve.ServeLive(port, lp, hub, onReady); err != nil {
		// "address already in use" is the most common mar dev failure
		// (forgot to stop a prior instance; another process holds the
		// port) and the raw Go error is opaque. Special-case it with
		// an actionable hint.
		if isAddrInUseErr(err) {
			fprintError("port %d is already in use.", port)
			// Multi-line hint — embedded continuation lines so the
			// helper emits the block as one unit (otherwise the
			// trailing blank from fprintHint would split the Hint
			// from its continuation).
			fprintHint(
				"another process (perhaps another %s?) is bound to this port.\n"+
					"      free it with %s,\n"+
					"      or change %s to something else.",
				cmdSuggest("dev"),
				colorGreen(fmt.Sprintf("lsof -ti:%d | xargs kill", port)),
				colorMagenta(`mar.json["server"]["port"]`))
			return 1
		}
		printError("mar dev", err)
		return 1
	}
	return 0
}

// isAddrInUseErr matches the various shapes Go's net package uses
// for "port already in use". Robust to errno wrapping; we just
// look for the recognizable substring.
func isAddrInUseErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "Only one usage of each socket address")
}

// fileState is a per-file fingerprint used by the watcher.
type fileState struct {
	mtime time.Time
	size  int64
}

// hintedError is the CLI-side counterpart to runtime.BlockedMigrationError:
// a small structured error carrying a one-line `Summary` plus a
// multi-line `Hint`. Use newHintedError to construct one inside any
// command that wants to attach remediation guidance to a returned
// error WITHOUT baking the literal "Hint:" prefix into the message
// string. printError detects the type and renders the two halves as
// the standard colored Error: + Hint: blocks.
//
// Why a separate type from runtime.BlockedMigrationError: that one is
// migration-specific and lives in the runtime package; this one is
// CLI-only and lives in main. printError handles both uniformly via
// the errorParts helper.
type hintedError struct {
	Summary string
	Hint    string
}

func (e *hintedError) Error() string {
	if e.Hint == "" {
		return e.Summary
	}
	return e.Summary + "\n\n" + e.Hint
}

// newHintedError builds a *hintedError. The summary slot is
// printf-formatted; the hint is taken verbatim (it's typically a
// multi-line block already composed by the caller).
func newHintedError(summary, hint string, args ...any) error {
	return &hintedError{
		Summary: fmt.Sprintf(summary, args...),
		Hint:    hint,
	}
}

// errorParts unwraps an error and returns its (Summary, Hint) split if
// the error is one of the structured types we know about
// (runtime.BlockedMigrationError, hintedError). Returns ("", "") for
// plain errors so the caller can fall back to diag.Format.
func errorParts(err error) (summary, hint string) {
	var he *hintedError
	if errors.As(err, &he) {
		return he.Summary, he.Hint
	}
	var be *runtime.BlockedMigrationError
	if errors.As(err, &be) {
		return be.Summary, be.Hint
	}
	return "", ""
}

// printError pretty-prints a CLI error to stderr and returns the same
// content as plain text (no ANSI) for callers forwarding the message
// to the dev banner (lp.SetError) and the SSE channel (hub.Error).
//
// `prefix` is an optional command marker prepended to the summary
// (e.g. "mar dev", "mar check"). Pass "" when the error already
// carries enough context on its own — typical for compile errors that
// come back from diag.Format with their own "Type error:" / "Parse
// error:" prefix.
//
// Rendering rules:
//
//   - Structured error (*hintedError, *runtime.BlockedMigrationError):
//     summary goes through fprintError (bold red Error: + summary in
//     the standard CLI style), hint goes through fprintHint (bold
//     yellow Hint: + body). The Error and Hint blocks share the
//     standard blank-line spacing handled by emitFprintBlock.
//
//   - Plain error: routed through diag.Format. If a prefix is given,
//     the whole rendered string is printed under fprintError (so the
//     prefix and Error: marker share one line). Otherwise printed
//     raw, preserving the diag.Format output verbatim — important for
//     positioned compile errors that already have their own colored
//     "Type error:" prefix and source snippet.
func printError(prefix string, err error) string {
	summary, hint := errorParts(err)
	if summary != "" || hint != "" {
		head := summary
		if prefix != "" {
			head = prefix + ": " + summary
		}
		fprintError("%s", head)
		if hint != "" {
			// colorizeHint turns backtick-spans cyan and dims
			// indented code blocks, giving the hint body visual
			// hierarchy. The runtime emits raw text (no ANSI)
			// because it doesn't know about TTY state; coloring
			// lives here so non-TTY output stays plain.
			fprintHint("%s", colorizeHint(hint))
		}
		if hint == "" {
			return head
		}
		// The returned string is plumbed into the dev banner /
		// SSE channel where colors would be noise; pass the raw
		// (un-colorized) hint there.
		return head + "\n\n" + hint
	}
	pretty := diag.Format(err)
	if prefix != "" {
		fprintError("%s: %s", prefix, pretty)
		return prefix + ": " + pretty
	}
	fmt.Fprintln(os.Stderr, pretty)
	return pretty
}

// watchAndReload polls .mar / .json files under root every ~250ms. On any
// change (mtime / size / file added / removed), it runs compile and
// broadcasts the result on the hub. Compile errors don't stop the loop —
// the dev banner shows them in the browser; the previous good version
// keeps running.
func watchAndReload(root string, compile func() error, hub *jsserve.ReloadHub, lp *jsserve.LiveProgram) {
	snapshot := func() map[string]fileState {
		out := map[string]fileState{}
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(p, ".mar") && !strings.HasSuffix(p, ".json") {
				return nil
			}
			out[p] = fileState{mtime: info.ModTime(), size: info.Size()}
			return nil
		})
		return out
	}
	sameSnapshot := func(a, b map[string]fileState) bool {
		if len(a) != len(b) {
			return false
		}
		for k, av := range a {
			bv, ok := b[k]
			if !ok || !av.mtime.Equal(bv.mtime) || av.size != bv.size {
				return false
			}
		}
		return true
	}
	prev := snapshot()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for range tick.C {
		cur := snapshot()
		if sameSnapshot(prev, cur) {
			continue
		}
		prev = cur
		fmt.Println("[mar dev] file change detected, recompiling…")
		if err := compile(); err != nil {
			pretty := printError("", err)
			lp.SetError(pretty)
			hub.Error(pretty)
			continue
		}
		// Successful compile clears the banner if there was one.
		if lp.LastError() != "" {
			lp.ClearError()
			hub.OK()
		}
		fmt.Println("[mar dev] reloaded")
		hub.Reload()
	}
}

// resolveDevEntry decides which file to load and which dir to read mar.json
// from, given a path that can be either a file or directory.
//
// Convention: when `path` is a directory, the entry file is `Main.mar`
// unless mar.json specifies a different `entry`. Most projects don't
// need to set it — the default is enough.
func resolveDevEntry(path string) (entryFile string, projectDir string, err error) {
	info, statErr := os.Stat(path)
	if statErr != nil {
		return "", "", statErr
	}
	if info.IsDir() {
		// Honor an explicit `entry` field in mar.json; fall back to
		// the conventional Main.mar otherwise. Structural load — the
		// entry filename is a literal, never an env:VAR reference,
		// and trying to resolve env here would block on unset
		// secrets that runDev handles correctly downstream.
		entryName := "Main.mar"
		entryFromManifest := false
		if m, mErr := project.LoadManifestStructure(path); mErr == nil && m != nil && m.Entry != "" {
			entryName = m.Entry
			entryFromManifest = true
		}
		entry := filepath.Join(path, entryName)
		if _, err := os.Stat(entry); err != nil {
			if entryFromManifest {
				return "", "", newHintedError(
					"%s not found",
					fmt.Sprintf(
						"mar.json has \"entry\": %q but that file doesn't exist.\n"+
							"Create it, fix the typo, or remove \"entry\" to use the default (Main.mar).",
						entryName),
					entry)
			}
			return "", "", newHintedError(
				"%s not found",
				"By convention the entry file is Main.mar at the project root.\n"+
					"Create it, or set \"entry\": \"<file>\" in mar.json to point elsewhere.",
				entry)
		}
		return entry, path, nil
	}
	return path, filepath.Dir(path), nil
}

// (App.* override builtins, page-bundle slicing, and route assembly all
// moved to internal/apphost — shared between `mar dev` and `mar-runtime`.)

func runConfig(dir string) int {
	// Structural load — prints whatever the operator wrote in
	// mar.json verbatim (including `env:VAR` references as-is).
	// Avoids needing the production env vars set in the shell where
	// `mar config` runs, and gives a more honest answer ("here's
	// what your manifest declares") than printing post-resolution
	// values that depend on the current shell environment.
	m, err := project.LoadManifestStructure(dir)
	if err != nil {
		printError("mar config", err)
		return 1
	}
	if m == nil {
		fmt.Println("(no mar.json)")
		return 0
	}
	fmt.Printf("name:  %s\n", m.Name)
	if m.Entry != "" {
		fmt.Printf("entry: %s\n", m.Entry)
	} else {
		fmt.Printf("entry: Main.mar (default)\n")
	}
	if m.Server != nil {
		fmt.Printf("server.port:      %d\n", m.Server.Port)
		fmt.Printf("server.host:      %s\n", m.Server.Host)
		fmt.Printf("server.publicUrl: %s\n", m.Server.PublicURL)
	}
	if m.Database != nil {
		fmt.Printf("database.path:    %s\n", m.Database.Path)
	}
	if m.Mail != nil {
		fmt.Printf("mail.from:         %s\n", m.Mail.From)
		fmt.Printf("mail.smtpHost:     %s\n", m.Mail.SMTPHost)
		fmt.Printf("mail.smtpPort:     %d\n", m.Mail.SMTPPort)
		fmt.Printf("mail.smtpUsername: %s\n", m.Mail.SMTPUsername)
		// Password value is shown raw (typically an `env:VAR` ref);
		// the actual secret never reaches this codepath because the
		// load is structural.
		fmt.Printf("mail.smtpPassword: %s\n", m.Mail.SMTPPassword)
	}
	return 0
}

func joinModuleName(parts []string) string {
	if len(parts) == 0 {
		return "(unnamed)"
	}
	return strings.Join(parts, ".")
}

func joinCtors(names []string) string {
	return strings.Join(names, " | ")
}
