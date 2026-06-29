// `mar fly` — Fly.io deployment wrapper.
//
// The operator never invokes the `fly` CLI directly. `mar fly` reads
// everything from mar.json (the `deploy.fly` block) and translates
// each subcommand into the equivalent Fly call(s):
//
//   mar fly deploy [path]    -- build + ship in one shot (creates the
//                               Fly app + volume on the first run if
//                               needed; pushes any missing secrets)
//   mar fly preview [path]   -- print what would be deployed, no-op
//   mar fly logs [path]      -- tail logs from the running machine(s)
//   mar fly status [path]    -- show app + machine status
//   mar fly destroy [path]   -- destroy the fly app (and its volume)
//   mar fly secrets ...      -- set / list / unset secrets directly
//   mar fly admin list ...   -- read-only admin inspection
//   mar fly database ...     -- backup / list backups / download
//
// Docker is treated as an implementation detail. Every deploy
// regenerates the Dockerfile + fly.toml in a temp directory from
// mar.json + topology, deploys, and deletes the temp dir on success.
// There are no template files on disk for the operator to edit; if
// the framework doesn't provide what an app needs, that's a feature
// request, not a configuration knob.

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"mar/internal/auth"
	"mar/internal/project"
	"mar/internal/scaffold"
)

// runFly dispatches `mar fly <subcommand>`.
func runFly(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, flyUsage())
		fmt.Fprintln(os.Stderr)
		return 2
	}
	sub := args[0]
	// Strip --no-open from `mar fly deploy` args before positional
	// parsing, so the flag can appear before or after the path.
	noOpen, subArgs := extractNoOpenFlag(args[1:])
	path := "."
	if len(subArgs) >= 1 {
		path = subArgs[0]
	}
	switch sub {
	case "deploy":
		return runFlyDeploy(path, noOpen)
	case "preview":
		return runFlyPreview(path)
	case "destroy":
		return runFlyDestroy(path)
	case "logs":
		return runFlyLogs(path)
	case "status":
		return runFlyStatus(path)
	case "admin":
		return runFlyAdmin(args[1:])
	case "database", "db":
		return runFlyDatabase(args[1:])
	case "secrets":
		return runFlySecrets(args[1:])
	default:
		fprintError("mar fly: unknown subcommand %q", sub)
		fmt.Fprintln(os.Stderr, flyUsage())
		fmt.Fprintln(os.Stderr)
		return 2
	}
}

// flyUsage returns the help text for `mar fly`. Built dynamically
// to apply the standard CLI palette (green `mar`, bold subcommands,
// magenta paths/config keys, cyan URLs).
func flyUsage() string {
	bin := colorGreen("mar")
	name := func(s string) string { return colorBold(s) }
	pth := func(s string) string { return colorMagenta(s) }
	url := func(s string) string { return colorCyan(s) }
	hdr := func(s string) string { return colorBold(s) }
	run := func(rest string) string { return bin + " " + name(rest) }

	return "Usage: " + run("fly") + " " + name("<command> [path]") + "\n" +
		"\n" +
		hdr("Commands:") + "\n" +
		"  " + name("deploy") + "     Build + ship to Fly. Creates the app + volume on the\n" +
		"             first run if needed; pushes any missing secrets.\n" +
		"  " + name("preview") + "    Show what would be deployed (no side effects).\n" +
		"  " + name("logs") + "       Tail logs from the running machine(s).\n" +
		"  " + name("status") + "     Show app + machine status.\n" +
		"  " + name("admin") + "      Inspect production admin state (read-only); see\n" +
		"             " + run("fly admin --help") + ".\n" +
		"  " + name("database") + "   Database operations (backup / list / download); see\n" +
		"             " + run("fly database --help") + ". Restore is in the admin\n" +
		"             panel UI at " + url("/_mar/admin") + ".\n" +
		"  " + name("secrets") + "    Set / list / unset env: secrets on the Fly app; see\n" +
		"             " + run("fly secrets --help") + ".\n" +
		"  " + name("destroy") + "    Destroy the Fly app and its volume. Asks twice.\n" +
		"\n" +
		hdr("Configuration:") + "\n" +
		"  Every command reads " + pth("mar.json") + "'s " + pth("deploy.fly") + " block:\n" +
		"\n" +
		"    " + pth(`"deploy": { "fly": { "app": "...", "region": "gru", "memory": "256mb" } }`)
}

// runFlyPreview prints what would be deployed without doing anything.
// Resolves topology by running the project's main (so it surfaces
// compile errors / missing config the same way deploy would).
func runFlyPreview(path string) int {
	_, projectDir, m, err := resolveFlyApp(path)
	if err != nil {
		if _, ok := err.(*project.DeployFlyError); ok {
			printDeployFlyError(err)
		} else {
			printManifestError("mar fly preview", err)
		}
		return 1
	}
	topo, err := scaffold.Topology(projectDir)
	if err != nil {
		printError("mar fly preview", err)
		return 1
	}
	fly := m.Deploy.Fly

	fmt.Println()
	fmt.Println(colorBold("Fly preview"))
	fmt.Printf("  app:      %s\n", colorCyan(fly.App))
	fmt.Printf("  region:   %s\n", colorCyan(fly.Region))
	fmt.Printf("  memory:   %s\n", colorCyan(fly.Memory))
	fmt.Printf("  topology: %s\n", colorCyan(topo))
	fmt.Printf("  url:      %s\n", colorCyan("https://"+fly.App+".fly.dev"))

	switch flyTopology(topo) {
	case flyTopologyFrontend:
		fmt.Printf("  volume:   %s\n", colorDim("none (frontend-only)"))
		fmt.Printf("  port:     %s\n", colorCyan("80"))
	default:
		fmt.Printf("  volume:   %s\n", colorCyan(flyVolumeName(fly.App)))
		fmt.Printf("  port:     %s\n", colorCyan(fmt.Sprintf("%d", manifestPort(m))))
	}

	envRefs, _ := discoverManifestEnvRefs(filepath.Join(projectDir, "mar.json"))
	if len(envRefs) == 0 {
		fmt.Printf("  secrets:  %s\n", colorDim("(none declared in mar.json)"))
	} else {
		fmt.Printf("  secrets:  %s\n", colorMagenta(strings.Join(envRefs, ", ")))
	}

	// Always-print educational note for frontend-only. preview is the
	// "tell me about my deploy" command, so the trade-off note fits
	// naturally here — and the operator can re-run it any time to
	// re-read the alternatives.
	if flyTopology(topo) == flyTopologyFrontend {
		fmt.Println()
		fmt.Println("  Note: frontend-only projects deploy to Fly as a static bundle.")
		fmt.Println("        For pure static files, dedicated static-hosting platforms")
		fmt.Println("        have advantages (global CDN, generous free tiers). Mar's")
		fmt.Println("        static bundle is portable to any of them. Build with:")
		fmt.Printf("          %s\n", cmdSuggest("build"))
	}
	fmt.Println()
	return 0
}

// runFlyDestroy tears down the Fly app + volume. Two-stage confirm:
// first a generic Y/N, then the operator must type the app name back.
// Stays interactive even in CI — destruction shouldn\'t auto-confirm.
func runFlyDestroy(path string) int {
	appName, projectDir, _, err := resolveFlyApp(path)
	if err != nil {
		if _, ok := err.(*project.DeployFlyError); ok {
			printDeployFlyError(err)
		} else {
			printManifestError("mar fly destroy", err)
		}
		return 1
	}
	if _, err := requireFlyCLI(); err != nil {
		fprintError("%v", err)
		return 1
	}

	// First confirmation: generic Y/N. Easy to muscle-memory through;
	// the second prompt forces the real pause.
	topo, _ := scaffold.Topology(projectDir)
	volumeNote := ""
	if flyTopology(topo) != flyTopologyFrontend {
		volumeNote = fmt.Sprintf(" and its volume %s", colorCyan(flyVolumeName(appName)))
	}
	fmt.Println()
	fmt.Printf("%s This will %s the Fly app %s%s.\n",
		colorRed("⚠"),
		colorRed("permanently destroy"),
		colorCyan(appName),
		volumeNote)
	if volumeNote != "" {
		fmt.Printf("%s %s\n",
			colorRed("⚠"),
			colorRed("All data in the volume will be lost. There is no undo."))
	}
	fmt.Println()
	if !confirmPrompt("Continue?") {
		fmt.Println("aborted.")
		return 0
	}

	// Second confirmation: type the app name.
	reader := bufio.NewReader(os.Stdin)
	fmt.Println()
	fmt.Printf("Type %s to confirm the %s of this app: ",
		colorCyan(appName),
		colorRed("destruction"))
	typed, _ := reader.ReadString('\n')
	if strings.TrimSpace(typed) != appName {
		fmt.Println("aborted (name didn't match).")
		return 0
	}

	if err := ensureFlyAuth(); err != nil {
		fprintError("mar fly destroy: %v", err)
		return 1
	}
	if err := ensureFlyAppExists(appName); err != nil {
		printError("mar fly destroy", err)
		return 1
	}

	fmt.Printf("\n[mar fly destroy] destroying app %s\n", colorCyan(appName))
	if err := runFlyCmd("apps", "destroy", appName, "--yes"); err != nil {
		fprintError("mar fly destroy: %v", err)
		return 1
	}
	fmt.Printf("\n%s Fly destroy finished.\n\n", colorGreen("✓"))
	return 0
}

// runFlyLogs streams `fly logs` for the project's app.
//
// The banner before invocation matters because mar's stdout surface
// is intentionally narrow: only boot output, errors, and machine
// lifecycle events show up here. Per-request data lives in the
// admin panel's "Recent requests" view. Without the banner, operators
// coming from Rails / Phoenix / Express + morgan would tail this
// expecting "Started GET /foo" lines per click.
func runFlyLogs(path string) int {
	appName, _, _, err := resolveFlyApp(path)
	if err != nil {
		if _, ok := err.(*project.DeployFlyError); ok {
			printDeployFlyError(err)
		} else {
			printManifestError("mar fly logs", err)
		}
		return 1
	}
	if _, err := requireFlyCLI(); err != nil {
		fprintError("%v", err)
		return 1
	}

	appURL := "https://" + appName + ".fly.dev"
	adminURL := appURL + "/_mar/admin"

	fmt.Println()
	fmt.Println(colorBold("Streaming live logs"))
	fmt.Printf("  app: %s\n", colorCyan(appName))
	fmt.Printf("  url: %s\n", colorCyan(appURL))
	fmt.Println()
	fmt.Printf("  %s\n", colorBold("What appears here:"))
	fmt.Printf("    %s\n", colorDim("• Machine lifecycle from Fly (start, stop, health checks)"))
	fmt.Printf("    %s\n", colorDim("• mar-runtime boot output (database path, admin sync, panics)"))
	fmt.Printf("    %s\n", colorDim("• Errors and warnings from the framework runtime"))
	fmt.Println()
	fmt.Printf("  %s\n", colorBold("What does NOT appear here:"))
	fmt.Printf("    %s\n", colorDim("• Per-request method/path/status/duration"))
	fmt.Printf("    %s\n", colorDim("• SQL queries, repo calls, handler activity"))
	fmt.Println()
	fmt.Printf("  %s %s\n", colorDim("Request-level visibility lives at:"), colorCyan(adminURL))
	fmt.Println()
	fmt.Printf("  %s %s %s\n",
		colorDim("Press"),
		colorYellow("Ctrl+C"),
		colorDim("to stop."))
	fmt.Println()
	return runFlyCmdInteractive("logs", "-a", appName)
}

// runFlyStatus shows `fly status` for the project's app.
func runFlyStatus(path string) int {
	appName, _, _, err := resolveFlyApp(path)
	if err != nil {
		if _, ok := err.(*project.DeployFlyError); ok {
			printDeployFlyError(err)
		} else {
			printManifestError("mar fly status", err)
		}
		return 1
	}
	if _, err := requireFlyCLI(); err != nil {
		fprintError("%v", err)
		return 1
	}
	fmt.Println()
	return runFlyCmdInteractive("status", "-a", appName)
}

// ---------- admin subcommand ----------

func runFlyAdmin(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, flyAdminUsage())
		return 2
	}
	sub := args[0]
	path := "."
	if len(args) >= 2 {
		path = args[1]
	}
	switch sub {
	case "list", "ls":
		return runFlyAdminList(path)
	case "-h", "--help":
		fmt.Println(flyAdminUsage())
		return 0
	default:
		fprintError("mar fly admin: unknown subcommand %q", sub)
		fmt.Fprintln(os.Stderr, flyAdminUsage())
		fmt.Fprintln(os.Stderr)
		return 2
	}
}

func flyAdminUsage() string {
	bin := colorGreen("mar")
	name := func(s string) string { return colorBold(s) }
	pth := func(s string) string { return colorMagenta(s) }
	hdr := func(s string) string { return colorBold(s) }
	run := func(rest string) string { return bin + " " + name(rest) }

	return "Usage: " + run("fly admin") + " " + name("<command> [path]") + "\n" +
		"\n" +
		hdr("Commands:") + "\n" +
		"  " + name("list") + "       Show production's admin list and last-login data.\n" +
		"             Read-only.\n" +
		"\n" +
		"Adding or removing admins happens via " + pth("mar.json") + " + " + run("fly deploy") + "\n" +
		"(the source of truth is the committed config). For an urgent runtime\n" +
		"override (rare, lossy, reverted on next deploy):\n" +
		"\n" +
		"  fly ssh console --app APP -C \"mar-runtime admin add EMAIL\"\n" +
		"  fly ssh console --app APP -C \"mar-runtime admin remove EMAIL\""
}

func runFlyAdminList(path string) int {
	appName, _, _, err := resolveFlyApp(path)
	if err != nil {
		if _, ok := err.(*project.DeployFlyError); ok {
			printDeployFlyError(err)
		} else {
			printManifestError("mar fly admin list", err)
		}
		return 1
	}
	if _, err := requireFlyCLI(); err != nil {
		fprintError("%v", err)
		return 1
	}
	if err := ensureFlyAppExists(appName); err != nil {
		printError("mar fly admin list", err)
		return 1
	}
	fmt.Println()
	return runFlyCmdInteractive("ssh", "console",
		"--app", appName,
		"-C", "mar-runtime admin list",
	)
}

// ---------- manifest + error rendering ----------

// printManifestError formats a manifest loading / validation failure
// for the CLI. Recognized concrete types (EnvVarNotSetError,
// FreeMailDomainError) get tailored hints; everything else falls
// through to a generic "Error: <prefix>: <err>" line.
//
// `prefix` is the command name shown in the Error line, e.g.
// "mar fly deploy".
//
// The hint shown for EnvVarNotSetError branches on the field path:
// fields under deploy.cloudflare-pages.* get an .env pointer (CF
// Pages doesn't store deploy-time secrets server-side, so the
// operator's local machine is the source of truth and .env is the
// canonical home for it); other fields (deploy.fly.*, mail.*, etc.)
// get a hybrid hint that points to .env for local dev AND to the
// production secret store for deploys.
func printManifestError(prefix string, err error) {
	var envErr *project.EnvVarNotSetError
	if errors.As(err, &envErr) {
		if envErr.Field != "" {
			fprintError("%s: %s references env var %s, which is not set.",
				prefix,
				colorMagenta(envErr.Field),
				colorMagenta(envErr.VarName))
		} else {
			fprintError("%s: env var %s is not set.",
				prefix, colorMagenta(envErr.VarName))
		}
		switch {
		case envErr.Field == "deploy.cloudflare-pages.apiToken":
			// Most loaded case for missing-env: the operator wrote
			// `apiToken: env:CF_API_TOKEN` in mar.json but doesn't
			// have it set. Tell them where to put it (.env, the
			// canonical home for CLI-only tokens), then where to
			// GET the token if they don't have one yet (dashboard
			// link + permission scope).
			//
			// Color scheme on the snippet follows cli-style.md:
			// bold for literal args (the VAR_NAME= part), cyan for
			// placeholder values (`<your-token>` the operator
			// substitutes). colorizeCmdParts handles bold+cyan
			// automatically based on the <...> regex.
			fprintHint(
				"add %s to your %s file (next to mar.json):\n"+
					"\n"+
					"        %s\n"+
					"\n"+
					"      To create a token, visit\n"+
					"      %s\n"+
					"      with the %s permission.",
				colorMagenta(envErr.VarName),
				colorMagenta(".env"),
				colorizeCmdParts(envErr.VarName+"=<your-token>"),
				colorCyan("https://dash.cloudflare.com/profile/api-tokens"),
				colorBold("Account.Cloudflare Pages: Edit"))
		case strings.HasPrefix(envErr.Field, "deploy.cloudflare-pages."):
			// Other CF fields (app, account) — env: is optional
			// here, so hitting this branch means the operator
			// chose env: voluntarily and forgot to set it. Brief
			// hint, no dashboard link.
			fprintHint(
				"add %s to your %s file (next to mar.json):\n"+
					"\n"+
					"        %s",
				colorMagenta(envErr.VarName),
				colorMagenta(".env"),
				colorizeCmdParts(envErr.VarName+"=<value>"))
		default:
			// Generic fields: secrets like mail.smtpPassword,
			// auth.sessionSecret, deploy.fly.app, etc. .env covers
			// local dev. For production deploys to Fly, the
			// platform's secret store is the source of truth.
			fprintHint(
				"for local dev, add %s to your %s file (next to mar.json).\n"+
					"      For production on Fly, set it with %s.",
				colorMagenta(envErr.VarName),
				colorMagenta(".env"),
				cmdSuggest("fly secrets set "+envErr.VarName+"=..."))
		}
		return
	}
	var fmErr *project.FreeMailDomainError
	if errors.As(err, &fmErr) {
		printFreeMailDomainError(prefix, fmErr)
		return
	}
	fprintError("%s: %v", prefix, err)
}

// resolveFlyApp loads mar.json, validates the deploy.fly block, and
// returns (app name, projectDir, manifest). Used by every `mar fly *`
// subcommand as the single source of truth for which Fly app to
// operate on. On validation failure, returns a *project.DeployFlyError
// so the caller can render the structured paste-ready hint via
// printDeployFlyError; on any other failure (missing mar.json,
// malformed JSON, env-var resolution) the caller renders via
// printManifestError.
func resolveFlyApp(path string) (appName, projectDir string, m *project.Manifest, err error) {
	projectDir, m, err = loadFlyManifest(path)
	if err != nil {
		return "", "", nil, err
	}
	if verr := project.ValidateDeployFly(m); verr != nil {
		return "", "", nil, verr
	}
	// ValidateDeployFly only checks that region is non-empty — it
	// doesn\'t know which 3-letter codes Fly actually offers (that
	// list lives in cmd/mar/fly_deploy.go alongside the live-fetch
	// fallback). Catch typos like "gr" instead of "gru" here,
	// before they reach Fly itself one minute into the deploy.
	if !isKnownFlyRegion(m.Deploy.Fly.Region) {
		return "", "", nil, &project.DeployFlyError{
			Kind:     "invalid-region",
			BadValue: m.Deploy.Fly.Region,
		}
	}
	return m.Deploy.Fly.App, projectDir, m, nil
}

// loadFlyManifest resolves projectDir from `path` (file or dir) and
// loads mar.json structurally (no env:VAR resolution — the values
// live in Fly Secrets at runtime, not in the operator\'s shell).
func loadFlyManifest(path string) (projectDir string, m *project.Manifest, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", nil, err
	}
	if info.IsDir() {
		projectDir = path
	} else {
		projectDir = filepath.Dir(path)
	}
	m, err = project.LoadManifestStructure(projectDir)
	if err != nil {
		return "", nil, err
	}
	if m == nil {
		return "", nil, fmt.Errorf("%s/mar.json not found", projectDir)
	}
	return projectDir, m, nil
}

// ---------- small helpers ----------

func manifestPort(m *project.Manifest) int {
	if m != nil && m.Server != nil && m.Server.Port > 0 {
		return m.Server.Port
	}
	return 3000
}

func flyVolumeName(appName string) string {
	return strings.ReplaceAll(appName, "-", "_") + "_data"
}

// ---------- fly CLI helpers ----------

func requireFlyCLI() (string, error) {
	exe, err := exec.LookPath("fly")
	if err != nil {
		return "", fmt.Errorf(
			"%s CLI not found in $PATH.\n\n      Install: %s",
			colorGreen("fly"),
			colorCyan("https://fly.io/docs/flyctl/install/"),
		)
	}
	return exe, nil
}

// ensureFlyAuth confirms `fly auth whoami` succeeds; if not, launches
// `fly auth login` interactively. Returns the eventual login error if
// authentication still doesn't succeed.
func ensureFlyAuth() error {
	cmd := exec.Command("fly", "auth", "whoami")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err == nil {
		return nil
	}
	fmt.Println("  Not logged in to fly.io. Launching `fly auth login`...")
	login := exec.Command("fly", "auth", "login")
	login.Stdout = os.Stdout
	login.Stderr = os.Stderr
	login.Stdin = os.Stdin
	if err := login.Run(); err != nil {
		return fmt.Errorf("fly auth login failed: %w", err)
	}
	return nil
}

// ensureFlyAppExists probes `fly status -a <name>`. When the app
// doesn't exist, returns a friendly hinted error pointing the
// operator at the relevant action (re-deploy to create, or fix the
// app name in mar.json). Other non-zero exits propagate verbatim
// so flyctl's own message reaches the operator.
func ensureFlyAppExists(appName string) error {
	var captured bytes.Buffer
	cmd := exec.Command("fly", "status", "-a", appName)
	cmd.Stdout = &captured
	cmd.Stderr = &captured
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err == nil {
		return nil
	}

	out := strings.TrimSpace(captured.String())
	if strings.Contains(out, "Could not find App") || strings.Contains(strings.ToLower(out), "app not found") {
		return newHintedError(
			"Fly.io app %q does not exist.",
			"Check that "+colorMagenta("deploy.fly.app")+" in "+colorMagenta("mar.json")+" matches an app on your Fly account.\n"+
				"To create it, run "+cmdSuggest("fly deploy")+".",
			appName)
	}
	if out != "" {
		return fmt.Errorf("checking app %q failed:\n%s", appName, out)
	}
	return fmt.Errorf("failed to verify whether Fly.io app %q exists", appName)
}

// missingFlySecrets returns the subset of `wantNames` not currently
// set on the Fly app. Uses `fly secrets list -a APP --json` for a
// stable shape across flyctl versions. Returns nil ("treat as all
// set") on any failure — the pre-flight is best-effort; the actual
// deploy will surface its own issues.
func missingFlySecrets(appName string, wantNames []string) []string {
	if appName == "" {
		return nil
	}
	out, err := exec.Command("fly", "secrets", "list", "-a", appName, "--json").Output()
	if err != nil {
		return nil
	}
	var entries []struct {
		Name string `json:"Name"`
	}
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil
	}
	have := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		have[e.Name] = struct{}{}
	}
	var missing []string
	for _, n := range wantNames {
		if _, ok := have[n]; !ok {
			missing = append(missing, n)
		}
	}
	return missing
}

func pluralizeSecrets(n int) string {
	if n == 1 {
		return "secret"
	}
	return "secrets"
}

// promptAndSetFlySecrets walks env:VAR list discovered in mar.json
// and handles each:
//
//   - If the var backs auth.sessionSecret (its name == sessionVar),
//     auto-generate a 32-byte URL-safe base64 value via auth.Token.
//     Asking the operator to invent one risks low-entropy keys.
//   - Otherwise (SMTP passwords, third-party API keys), prompt with
//     echo off. Required — empty input re-prompts.
//
// Pushes everything in one `fly secrets set` call.
func promptAndSetFlySecrets(appName string, envRefs []string, sessionVar string) error {
	pairs := make([]string, 0, len(envRefs))
	for _, name := range envRefs {
		if sessionVar != "" && name == sessionVar {
			secret, err := auth.Token()
			if err != nil {
				return fmt.Errorf("generate %s: %w", name, err)
			}
			fmt.Printf("  %s %s\n",
				colorMagenta(name),
				colorDim("(auto-generated, used to sign session cookies)"))
			pairs = append(pairs, name+"="+secret)
			continue
		}
		val, err := promptRequiredSecret(name)
		if err != nil {
			return err
		}
		pairs = append(pairs, name+"="+val)
	}
	if len(pairs) == 0 {
		fmt.Println("  (no secrets to push)")
		return nil
	}
	args := append([]string{"secrets", "set"}, pairs...)
	args = append(args, "-a", appName)
	return runFlyCmd(args...)
}

func promptRequiredSecret(name string) (string, error) {
	const maxAttempts = 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		fmt.Printf("  %s %s ",
			colorMagenta(name),
			colorDim("(required):"))
		val, err := readPasswordEnter()
		if err != nil {
			return "", err
		}
		if val != "" {
			return val, nil
		}
		fprintWarn("%s is required. The app won't boot without it.",
			colorMagenta(name))
	}
	return "", fmt.Errorf("%s: no value provided after %d attempts", name, maxAttempts)
}

func discoverManifestEnvRefs(manifestPath string) ([]string, error) {
	return project.EnvRefsFromFile(manifestPath)
}

func runFlyCmd(args ...string) error {
	cmd := exec.Command("fly", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func runFlyCmdInteractive(args ...string) int {
	cmd := exec.Command("fly", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return 1
	}
	return 0
}

// confirmPrompt asks "<prompt> [y/N] " and returns true on y/yes.
func confirmPrompt(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// readPasswordEnter reads a line with echo disabled when stdin is a
// terminal; falls back to plain ReadString otherwise.
func readPasswordEnter() (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		buf, err := term.ReadPassword(fd)
		fmt.Println()
		if err != nil {
			return "", err
		}
		return string(buf), nil
	}
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// stdinIsTerminal reports whether stdin is a TTY.
func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// flySuggestion formats a "next command" hint with the standard
// styling — green `mar` + bold rest, via cmdSuggest — omitting the
// path argument when it's the default. Used by sibling subcommands
// (secrets, health check) when surfacing the canonical next step.
func flySuggestion(sub, path string) string {
	rest := "fly " + sub
	if path != "" && path != "." {
		rest += " " + path
	}
	return cmdSuggest(rest)
}
