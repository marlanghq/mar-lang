// Subcommands for `mar fly secrets {set,list,unset}`.
//
// All operate on the same conceptual model:
//
//   - `mar.json` is the source of truth for which env: refs the app
//     needs (auth.sessionSecret, mail.smtpPassword, any user-defined
//     env: ref in the manifest).
//   - The Fly app holds the actual values, set via `fly secrets set`.
//   - These commands keep the two in sync, never asking the operator
//     to run raw `fly` themselves. Multi-provider future (Render,
//     Railway, AWS) extends this same surface — `mar render secrets
//     {set,list,unset}` — without users learning provider CLIs.
//
// Bulk pushing is handled automatically by `mar fly deploy` (it
// prompts for any env: refs missing on the Fly app, then pushes
// them with the deploy). The set/list/unset trio is the targeted
// complement for after-the-fact rotation and inspection.
//
// The three subcommands:
//
//   set    — single-target update. `mar fly secrets set NAME=value`
//            or `mar fly secrets set NAME` (prompts with hidden echo).
//            Warns when NAME isn't declared in mar.json, but doesn't
//            block — covers the legit "I want a secret available at
//            runtime even though no env: ref reads it" case.
//
//   list   — read-only inspection. Shows what's set on Fly, cross-
//            referenced against mar.json so the operator can see
//            (a) which declared refs are missing and (b) which Fly
//            secrets are orphaned (no mar.json ref pointing at them).
//
//   unset  — remove a secret from Fly. Confirms first; warns if the
//            secret is still declared in mar.json (likely a mistake).

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"mar/internal/project"
)

// runFlySecrets dispatches `mar fly secrets <sub>` to the right
// handler. Mirrors the shape of runFly / runFlyAdmin.
func runFlySecrets(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, flySecretsUsage())
		fmt.Fprintln(os.Stderr)
		return 2
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "set":
		return runFlySecretsSet(rest)
	case "list", "ls":
		return runFlySecretsList(pathArg(rest))
	case "unset", "rm":
		return runFlySecretsUnset(rest)
	default:
		fprintError("mar fly secrets: unknown subcommand %q", sub)
		fmt.Fprintln(os.Stderr, flySecretsUsage())
		fmt.Fprintln(os.Stderr)
		return 2
	}
}

// pathArg returns the last arg (if any) as a project path, defaulting
// to ".". Used by subcommands that take an optional trailing path.
func pathArg(args []string) string {
	if len(args) == 0 {
		return "."
	}
	return args[len(args)-1]
}

// flySecretsUsage returns the help text for `mar fly secrets`.
// Same palette as flyUsage — see that function's comment.
func flySecretsUsage() string {
	bin := colorGreen("mar")
	name := func(s string) string { return colorBold(s) }
	hdr := func(s string) string { return colorBold(s) }
	run := func(rest string) string { return bin + " " + name(rest) }
	return "Usage: " + run("fly secrets") + " " + name("<command> [args]") + "\n" +
		"\n" +
		hdr("Commands:") + "\n" +
		"  " + name("set") + " NAME[=VALUE] [path]    Set one secret. " + colorMagenta("NAME=value") + " sets directly;\n" +
		"                              " + colorMagenta("NAME") + " alone prompts (input hidden).\n" +
		"  " + name("list") + " [path]                List secrets on the Fly app, cross-referenced\n" +
		"                              against " + colorMagenta("mar.json") + " (missing vs orphaned).\n" +
		"  " + name("unset") + " NAME [path]          Remove a secret from the Fly app. Confirms.\n" +
		"\n" +
		hdr("Note:") + " bulk pushing is handled by " + run("fly deploy") + ", it prompts\n" +
		"for any " + colorMagenta("env:VAR") + " ref declared in " + colorMagenta("mar.json") + " that isn't yet set\n" +
		"on the Fly app and pushes them in one shot. Use the commands\n" +
		"above only for targeted rotation / inspection after the fact.\n" +
		"\n" +
		hdr("Why use these instead of `fly secrets *`:") + "\n" +
		"  - " + run("fly secrets") + " validates against " + colorMagenta("mar.json") + " (warns on orphans,\n" +
		"    catches typos in env: refs).\n" +
		"  - Same shape will exist for other deploy targets later\n" +
		"    (" + run("render secrets") + ", " + run("railway secrets") + ", ...).\n" +
		"  - You never have to learn flyctl flags or " + colorMagenta("-a APP") + " quirks."
}

// runFlySecretsSet handles `mar fly secrets set NAME[=VALUE] [path]`.
//
// Two forms:
//   - `set NAME=value` → push directly, no prompt
//   - `set NAME` → prompt with hidden-echo, then push
//
// In either form, we warn if NAME isn't declared in mar.json — the
// secret will be set on Fly but no app code will read it, which is
// usually a typo. We don't BLOCK because a legitimate use case exists
// (e.g. setting a value the runtime reads via some other mechanism).
func runFlySecretsSet(args []string) int {
	if len(args) < 1 {
		fprintError("mar fly secrets set: missing NAME argument")
		fprintHint("usage: %s, or %s",
			cmdSuggest("fly secrets set NAME=VALUE [path]"),
			cmdSuggest("fly secrets set NAME [path]"))
		return 2
	}
	// First arg is always the secret spec. If it contains "=", everything
	// after is the value (which may itself contain "="). Remaining args
	// are the optional project path.
	spec := args[0]
	pathPos := args[1:]
	path := "."
	if len(pathPos) >= 1 {
		path = pathPos[0]
	}

	var name, value string
	if idx := strings.Index(spec, "="); idx >= 0 {
		name = spec[:idx]
		value = spec[idx+1:]
	} else {
		name = spec
	}
	if name == "" {
		fprintError("mar fly secrets set: empty NAME")
		return 2
	}

	appName, projectDir, _, err := resolveFlyApp(path)
	if err != nil {
		if _, ok := err.(*project.DeployFlyError); ok {
			printDeployFlyError(err)
		} else {
			printManifestError("mar fly secrets set", err)
		}
		return 1
	}
	if _, err := requireFlyCLI(); err != nil {
		fprintError("%v", err)
		return 1
	}

	// Cross-check with mar.json — warn (don't block) when NAME is not
	// declared as an env: ref. Typos in env var names are a common
	// source of "I set it but the app still says missing" confusion.
	declared, _ := discoverManifestEnvRefs(filepath.Join(projectDir, "mar.json"))
	if !containsString(declared, name) {
		fprintWarn("%s is not declared as an env: ref in mar.json, setting it\n"+
			"      anyway, but no runtime code is reading it through the manifest.",
			colorMagenta(name))
	}

	// Prompt for value if not supplied on the command line.
	if value == "" {
		v, err := promptRequiredSecret(name)
		if err != nil {
			fprintError("mar fly secrets set: %v", err)
			return 1
		}
		value = v
	}

	fmt.Printf("\n%s Setting %s on %s\n",
		colorBold("[mar fly secrets set]"),
		colorMagenta(name), colorCyan(appName))
	if err := runFlyCmd("secrets", "set", name+"="+value, "-a", appName); err != nil {
		fprintError("mar fly secrets set: %v", err)
		return 1
	}
	fmt.Printf("\n%s Done.\n\n", colorGreen("✓"))
	return 0
}

// runFlySecretsList shows what's set on the Fly app, cross-referenced
// against mar.json. Two columns matter most:
//
//   - "declared?" — is this name in mar.json as env:NAME? If no, it's
//     orphaned (set on Fly but no app code reads it; either intentional
//     extra config or leftover from an older app version).
//   - "missing?" — are any mar.json env: refs NOT set on Fly? These
//     would block deploy via the pre-flight check.
//
// Values are never shown — fly doesn't return them via `secrets list`
// (good hygiene), and we wouldn't print them even if it did.
func runFlySecretsList(path string) int {
	appName, projectDir, _, err := resolveFlyApp(path)
	if err != nil {
		if _, ok := err.(*project.DeployFlyError); ok {
			printDeployFlyError(err)
		} else {
			printManifestError("mar fly secrets list", err)
		}
		return 1
	}
	if _, err := requireFlyCLI(); err != nil {
		fprintError("%v", err)
		return 1
	}

	flySecrets, err := listFlySecrets(appName)
	if err != nil {
		fprintError("mar fly secrets list: %v", err)
		return 1
	}
	declared, _ := discoverManifestEnvRefs(filepath.Join(projectDir, "mar.json"))
	declaredSet := make(map[string]struct{}, len(declared))
	for _, n := range declared {
		declaredSet[n] = struct{}{}
	}
	setOnFly := make(map[string]flySecretEntry, len(flySecrets))
	for _, s := range flySecrets {
		setOnFly[s.Name] = s
	}

	// Union of all names — declared OR set on Fly.
	allNames := map[string]struct{}{}
	for _, n := range declared {
		allNames[n] = struct{}{}
	}
	for _, s := range flySecrets {
		allNames[s.Name] = struct{}{}
	}
	if len(allNames) == 0 {
		fmt.Printf("\n  (no secrets declared in %s and none set on %s)\n\n",
			colorMagenta("mar.json"), colorCyan(appName))
		return 0
	}

	// Sort for stable output.
	names := make([]string, 0, len(allNames))
	for n := range allNames {
		names = append(names, n)
	}
	sortStrings(names)

	fmt.Printf("\n%s for %s\n\n",
		colorBold("[mar fly secrets list]"), colorCyan(appName))
	fmt.Printf("  %-30s  %-15s  %s\n",
		colorBold("NAME"), colorBold("STATUS"), colorBold("LAST SET"))
	missing := 0
	orphan := 0
	for _, name := range names {
		_, isDeclared := declaredSet[name]
		entry, isSet := setOnFly[name]
		var status, last string
		switch {
		case isDeclared && isSet:
			status = colorGreen("✓ ok")
			last = entry.CreatedAt
		case isDeclared && !isSet:
			status = colorRed("✗ missing")
			last = colorDim("(never set)")
			missing++
		case !isDeclared && isSet:
			status = colorYellow("· orphan")
			last = entry.CreatedAt
			orphan++
		}
		fmt.Printf("  %-30s  %-15s  %s\n", colorMagenta(name), status, last)
	}
	fmt.Println()
	if missing > 0 {
		fprintHint("%d declared secret(s) not set on Fly. Run %s to push them\n"+
			"      (deploy prompts for any missing values).",
			missing, flySuggestion("deploy", path))
	}
	if orphan > 0 {
		fmt.Printf("  %s orphan secret(s) are set on Fly but not referenced in %s.\n",
			colorDim(fmt.Sprintf("%d", orphan)), colorMagenta("mar.json"))
		fmt.Printf("  %s leftover from old config, or set outside this workflow.\n",
			colorDim("    Usually"))
	}
	return 0
}

// runFlySecretsUnset removes a secret from Fly. Confirms first, and
// warns extra hard if the secret is still declared in mar.json — that
// means the next deploy will fail pre-flight, and the runtime would
// fail at boot anyway.
func runFlySecretsUnset(args []string) int {
	if len(args) < 1 {
		fprintError("mar fly secrets unset: missing NAME argument")
		fprintHint("usage: %s", cmdSuggest("fly secrets unset NAME [path]"))
		return 2
	}
	name := args[0]
	path := "."
	if len(args) >= 2 {
		path = args[1]
	}
	if name == "" {
		fprintError("mar fly secrets unset: empty NAME")
		return 2
	}

	appName, projectDir, _, err := resolveFlyApp(path)
	if err != nil {
		if _, ok := err.(*project.DeployFlyError); ok {
			printDeployFlyError(err)
		} else {
			printManifestError("mar fly secrets unset", err)
		}
		return 1
	}
	if _, err := requireFlyCLI(); err != nil {
		fprintError("%v", err)
		return 1
	}

	// Heads-up if the secret is still declared.
	declared, _ := discoverManifestEnvRefs(filepath.Join(projectDir, "mar.json"))
	if containsString(declared, name) {
		fprintWarn("%s is still referenced in mar.json, unsetting it will block\n"+
			"      the next %s and break the running app on next restart.",
			colorMagenta(name), cmdSuggest("fly deploy"))
	}

	fmt.Printf("Unset %s from Fly app %s?\n",
		colorMagenta(name), colorCyan(appName))
	if !confirmPrompt("Continue?") {
		fmt.Println("aborted; nothing changed.")
		return 0
	}
	if err := runFlyCmd("secrets", "unset", name, "-a", appName); err != nil {
		fprintError("mar fly secrets unset: %v", err)
		return 1
	}
	fmt.Printf("\n%s Done.\n\n", colorGreen("✓"))
	return 0
}

// flySecretEntry is the subset of `fly secrets list --json` we care
// about. flyctl returns one entry per secret with the value redacted.
type flySecretEntry struct {
	Name      string `json:"Name"`
	CreatedAt string `json:"CreatedAt"`
}

// listFlySecrets shells out to `fly secrets list --json` and returns
// the parsed entries. Empty list on no secrets is normal (fresh app).
func listFlySecrets(appName string) ([]flySecretEntry, error) {
	out, err := exec.Command("fly", "secrets", "list", "-a", appName, "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("fly secrets list failed: %w", err)
	}
	var entries []flySecretEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("parse fly secrets list output: %w", err)
	}
	return entries, nil
}

// containsString reports whether `needle` is in `haystack`. Tiny
// helper; pulled out so the call sites read cleanly.
func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// sortStrings sorts in place. Wrapper to avoid importing `sort` in
// just one place (and to keep the call site noise-free).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
