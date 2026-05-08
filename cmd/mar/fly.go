// `mar fly` subcommand — full Fly.io deployment workflow.
//
// The user never has to invoke the `fly` CLI directly. `mar fly`
// wraps every step of the lifecycle:
//
//   mar fly init [path]      -- scaffold deploy/fly/{Dockerfile,fly.toml}
//   mar fly provision [path] -- create the fly app + volume + secrets
//   mar fly deploy [path]    -- build linux-amd64 binary + ship it
//   mar fly logs [path]      -- tail logs from the running machine(s)
//   mar fly status [path]    -- show app + machine status
//   mar fly destroy [path]   -- destroy the fly app (and its volume)
//
// `init` is interactive: it prompts for the Fly app name and the
// region to deploy into. The region prompt presents a continent-
// grouped table of supported Fly regions and loops until the user
// enters a valid code. Both prompts honor non-interactive overrides
// (FLY_REGION env var; ./mar.json `name` field as the default app
// name) so CI and scripts can drive the same code path.
//
// Provision/deploy/destroy fail loudly if a step would clash with
// an existing resource (app already taken, volume already exists,
// etc.). The wrapper does NOT silently reuse what's there — that
// kind of "idempotent" behavior tends to hide misconfiguration. If
// you want to recreate, run `mar fly destroy` first; if you're
// taking over an existing fly app, edit deploy/fly/fly.toml by hand
// and skip `mar fly provision`.

package main

import (
	"bufio"
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"golang.org/x/term"

	"mar/internal/project"
)

//go:embed flytmpl/Dockerfile.tmpl
var flyDockerfileTmpl string

//go:embed flytmpl/fly.toml.tmpl
var flyTomlTmpl string

// flyTemplateData is the parameter set passed to both templates.
// AppName drives the binary path, fly app, env var values; Port is
// the HTTP port the binary listens on (read from mar.json, defaulting
// to 3000); VolumeName is the fly volume mount source (must be
// alphanumeric + underscore — derived from AppName by replacing
// hyphens). Region is the fly region code (gru, iad, fra, ...) the
// user picks during init. Memory is the per-machine RAM allocation
// (256mb / 512mb / 1gb / 2gb) — also user-picked.
type flyTemplateData struct {
	AppName    string
	Port       int
	VolumeName string
	Region     string
	Memory     string
}

// Memory presets supported by `mar fly init`. Anything else has to
// be edited into fly.toml by hand. The list mirrors the lispy
// version's curated set — fly supports more sizes (4gb, 8gb...) but
// they're rarely the right answer for a small app and they cost
// linearly more. Users who need them can edit fly.toml.
const defaultFlyAppMemory = "256mb"

var flyAppMemoryOptions = []string{"256mb", "512mb", "1gb", "2gb"}

// flyAppNameRe enforces the fly.io app naming rules: lowercase
// letters, digits, and hyphens; must start and end with an
// alphanumeric.
var flyAppNameRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// flyRegion describes one fly.io region we support in the picker.
// Continent is purely cosmetic — used to group the table the user
// sees during init.
type flyRegion struct {
	Continent string
	Name      string
	Code      string
}

// flyRegions is the curated list shown by `mar fly init`. We don't
// fetch this dynamically from fly's API — keeping it local makes
// init work offline and avoids a dependency on an undocumented
// endpoint. Update by hand when fly adds regions worth supporting.
var flyRegions = []flyRegion{
	{Continent: "Africa", Name: "Johannesburg, South Africa", Code: "jnb"},
	{Continent: "Asia Pacific", Name: "Mumbai, India", Code: "bom"},
	{Continent: "Asia Pacific", Name: "Singapore, Singapore", Code: "sin"},
	{Continent: "Asia Pacific", Name: "Sydney, Australia", Code: "syd"},
	{Continent: "Asia Pacific", Name: "Tokyo, Japan", Code: "nrt"},
	{Continent: "Europe", Name: "Amsterdam, Netherlands", Code: "ams"},
	{Continent: "Europe", Name: "Frankfurt, Germany", Code: "fra"},
	{Continent: "Europe", Name: "London, United Kingdom", Code: "lhr"},
	{Continent: "Europe", Name: "Paris, France", Code: "cdg"},
	{Continent: "Europe", Name: "Stockholm, Sweden", Code: "arn"},
	{Continent: "North America", Name: "Ashburn, Virginia (US)", Code: "iad"},
	{Continent: "North America", Name: "Chicago, Illinois (US)", Code: "ord"},
	{Continent: "North America", Name: "Dallas, Texas (US)", Code: "dfw"},
	{Continent: "North America", Name: "Los Angeles, California (US)", Code: "lax"},
	{Continent: "North America", Name: "San Jose, California (US)", Code: "sjc"},
	{Continent: "North America", Name: "Secaucus, NJ (US)", Code: "ewr"},
	{Continent: "North America", Name: "Toronto, Canada", Code: "yyz"},
	{Continent: "South America", Name: "Sao Paulo, Brazil", Code: "gru"},
}

func runFly(args []string) int {
	if len(args) < 1 {
		// One-shot exit (returns to shell) — blank line before and
		// after per docs/cli-style.md §1.
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, flyUsage())
		fmt.Fprintln(os.Stderr)
		return 2
	}
	sub := args[0]
	path := "."
	if len(args) >= 2 {
		path = args[1]
	}
	switch sub {
	case "init":
		return runFlyInit(path)
	case "provision":
		return runFlyProvision(path)
	case "deploy":
		return runFlyDeploy(path)
	case "destroy":
		return runFlyDestroy(path)
	case "logs":
		return runFlyLogs(path)
	case "status":
		return runFlyStatus(path)
	case "admin":
		// `mar fly admin list` — read-only inspection of the
		// production runtime's _mar_admins state. The args after
		// "admin" are the admin subcommand + its args; we re-parse
		// here rather than in runFly so the help text stays scoped.
		return runFlyAdmin(args[1:])
	case "database", "db":
		// `mar fly database <sub>` — operations against the
		// production database. Includes backup management; restore
		// is in the admin panel UI. Accepts `db` as a shortcut.
		return runFlyDatabase(args[1:])
	default:
		fprintError("mar fly: unknown subcommand %q", sub)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, flyUsage())
		fmt.Fprintln(os.Stderr)
		return 2
	}
}

// flyUsage returns the help text for `mar fly`. Built dynamically
// (not a const) so we can color subcommand names + commands per
// docs/cli-style.md §3:
//
//   bold     section headers ("Commands:", "Typical first-deploy flow:")
//   green    subcommand names + sample commands the user runs
//   magenta  filesystem paths and config keys (mar.json, deploy/fly/...)
//   cyan     URLs / paths in URLs (/_mar/admin)
//
// Plain text everywhere else.
func flyUsage() string {
	cmd := func(s string) string { return colorGreen(s) }
	path := func(s string) string { return colorMagenta(s) }
	url := func(s string) string { return colorCyan(s) }
	hdr := func(s string) string { return colorBold(s) }

	return "Usage: " + cmd("mar fly") + " <command> [path]\n" +
		"\n" +
		hdr("Commands:") + "\n" +
		"  " + cmd("init") + "       Generate " + path("deploy/fly/{Dockerfile,fly.toml}") + " from " + path("mar.json") + ".\n" +
		"             Run once per project; commit the files.\n" +
		"  " + cmd("provision") + "  Create the fly app + volume + push secrets to fly. Run\n" +
		"             once after init (or after destroy, to recreate).\n" +
		"  " + cmd("deploy") + "     Build the linux-amd64 binary and ship it. Run on every\n" +
		"             release.\n" +
		"  " + cmd("logs") + "       Tail logs from the running machine(s).\n" +
		"  " + cmd("status") + "     Show app + machine status.\n" +
		"  " + cmd("admin") + "      Inspect production admin state (read-only); see\n" +
		"             " + cmd("mar fly admin --help") + ".\n" +
		"  " + cmd("database") + "   Database operations (backup / list / download); see\n" +
		"             " + cmd("mar fly database --help") + ". Restore is in the admin\n" +
		"             panel UI at " + url("/_mar/admin") + ".\n" +
		"  " + cmd("destroy") + "    Destroy the fly app and its volume. Asks for confirmation\n" +
		"             twice — destructive.\n" +
		"\n" +
		hdr("Typical first-deploy flow:") + "\n" +
		"  " + cmd("mar fly init && mar fly provision && mar fly deploy") + "\n" +
		"\n" +
		hdr("Subsequent deploys:") + "\n" +
		"  " + cmd("mar fly deploy")
}

// runFlyInit scaffolds deploy/fly/Dockerfile + deploy/fly/fly.toml.
// Interactive: prompts for the Fly app name (default = mar.json
// `name`) and the region (continent-grouped picker). Both inputs
// honor non-TTY shortcuts: FLY_REGION env var; the default app name
// is taken non-interactively when stdin isn't a terminal.
//
// Refuses to overwrite an existing deploy/fly/ unless the user
// confirms — these files are commonly hand-edited (region, machine
// size), and silently clobbering them would be hostile.
func runFlyInit(path string) int {
	projectDir, manifest, err := loadFlyManifest(path)
	if err != nil {
		fprintError("mar fly init: %v", err)
		return 1
	}
	if manifest.Name == "" {
		fprintError("mar fly init: mar.json is missing the %s field — required to derive the Fly app name",
			colorMagenta("name"))
		return 1
	}

	flyDir := filepath.Join(projectDir, "deploy", "fly")
	if existing, err := dirHasFiles(flyDir); err == nil && existing {
		fmt.Println()
		if !confirmPrompt(fmt.Sprintf("mar fly init: %s already exists with content. Overwrite?", colorMagenta(flyDir))) {
			fmt.Println("aborted; nothing changed.")
			return 0
		}
	}

	appName, err := promptFlyAppName(manifest.Name)
	if err != nil {
		fprintError("mar fly init: %v", err)
		return 1
	}
	region, err := promptFlyRegion()
	if err != nil {
		fprintError("mar fly init: %v", err)
		return 1
	}
	memory, err := promptFlyAppMemory()
	if err != nil {
		fprintError("mar fly init: %v", err)
		return 1
	}

	data := flyTemplateData{
		AppName:    appName,
		Port:       manifestPort(manifest),
		VolumeName: flyVolumeName(appName),
		Region:     region.Code,
		Memory:     memory,
	}

	if err := os.MkdirAll(flyDir, 0o755); err != nil {
		fprintError("mar fly init: %v", err)
		return 1
	}
	if err := writeFlyTemplate(filepath.Join(flyDir, "Dockerfile"), flyDockerfileTmpl, "Dockerfile", data); err != nil {
		fprintError("mar fly init: %v", err)
		return 1
	}
	if err := writeFlyTemplate(filepath.Join(flyDir, "fly.toml"), flyTomlTmpl, "fly.toml", data); err != nil {
		fprintError("mar fly init: %v", err)
		return 1
	}

	fmt.Println()
	fmt.Printf("[mar fly init] wrote %s\n", colorMagenta(filepath.Join(flyDir, "Dockerfile")))
	fmt.Printf("[mar fly init] wrote %s\n", colorMagenta(filepath.Join(flyDir, "fly.toml")))
	fmt.Println()
	fmt.Println(colorBold("Next steps:"))
	fmt.Printf("  1. Review %s\n", colorMagenta("deploy/fly/fly.toml"))
	fmt.Printf("  2. %s   (creates app + volume + secrets)\n", colorGreen(flySuggestion("provision", path)))
	fmt.Printf("  3. %s      (builds + ships)\n", colorGreen(flySuggestion("deploy", path)))
	fmt.Println()
	return 0
}

// runFlyProvision creates the fly app, volume, and secrets for a
// project that's already had `mar fly init` run.
//
// Steps:
//   1. fly auth (whoami; login if not authed)
//   2. fly apps create  — fails if the app already exists. The user
//      is expected to run `mar fly destroy` first if they want to
//      recreate, or skip provision and edit fly.toml by hand if
//      they're attaching to an existing app.
//   3. fly volumes create — same: fails if the volume already
//      exists.
//   4. For each env:VAR in mar.json — prompt + fly secrets set.
func runFlyProvision(path string) int {
	projectDir, _, err := loadFlyManifest(path)
	if err != nil {
		fprintError("mar fly provision: %v", err)
		return 1
	}
	flyDir := filepath.Join(projectDir, "deploy", "fly")
	flyToml := filepath.Join(flyDir, "fly.toml")
	cfg, err := readFlyToml(flyToml)
	if err != nil {
		fprintError("mar fly provision: %v", err)
		fprintHint("run %s first.", colorGreen(flySuggestion("init", path)))
		return 1
	}
	if _, err := requireFlyCLI(); err != nil {
		fprintError("%v", err)
		return 1
	}

	fmt.Println()
	fmt.Println(colorBold("Fly provision"))
	fmt.Println("  This step creates the Fly.io resources your app needs:")
	fmt.Printf("    - app:     %s\n", colorCyan(cfg.AppName))
	fmt.Printf("    - volume:  %s (region %s)\n", colorCyan(cfg.VolumeName), colorCyan(cfg.Region))
	fmt.Println("  It will log in to fly.io if needed and prompt for any")
	fmt.Println("  env:VAR secrets declared in mar.json.")
	fmt.Println()
	if !confirmPrompt("Continue?") {
		fmt.Println("aborted; nothing changed.")
		return 0
	}

	fmt.Printf("\n%s Checking fly authentication\n", colorYellow("[1/4]"))
	if err := ensureFlyAuth(); err != nil {
		fprintError("mar fly provision: %v", err)
		return 1
	}

	fmt.Printf("\n%s Creating app\n", colorYellow("[2/4]"))
	if err := runFlyCmd("apps", "create", cfg.AppName); err != nil {
		fprintError("mar fly provision: 'fly apps create' failed.")
		fprintHint("if the app already exists and you want to recreate, run %s first.",
			colorGreen("mar fly destroy"))
		return 1
	}

	fmt.Printf("\n%s Creating persistent volume\n", colorYellow("[3/4]"))
	if err := runFlyCmd(
		"volumes", "create", cfg.VolumeName,
		"--region", cfg.Region,
		"--size", "1",
		"--yes",
		"-a", cfg.AppName,
	); err != nil {
		fprintError("mar fly provision: 'fly volumes create' failed.")
		return 1
	}

	fmt.Printf("\n%s Pushing secrets\n", colorYellow("[4/4]"))
	envRefs, err := discoverManifestEnvRefs(filepath.Join(projectDir, "mar.json"))
	if err != nil {
		fprintError("mar fly provision: %v", err)
		return 1
	}
	if len(envRefs) == 0 {
		fmt.Println("  (no env:VAR fields in mar.json — nothing to push)")
	} else {
		if err := promptAndSetFlySecrets(cfg.AppName, envRefs); err != nil {
			fprintError("mar fly provision: %v", err)
			return 1
		}
	}

	fmt.Printf("\n%s Fly provision finished.\n", colorGreen("✓"))
	fmt.Printf("\nNext: %s\n\n", colorGreen(flySuggestion("deploy", path)))
	return 0
}

// runFlyDeploy: build the linux-amd64 binary into deploy/fly/dist/
// and run `fly deploy` from there.
func runFlyDeploy(path string) int {
	projectDir, manifest, err := loadFlyManifest(path)
	if err != nil {
		fprintError("mar fly deploy: %v", err)
		return 1
	}
	if manifest.Name == "" {
		fprintError("mar fly deploy: mar.json is missing the %s field", colorMagenta("name"))
		return 1
	}
	flyDir := filepath.Join(projectDir, "deploy", "fly")
	if _, err := os.Stat(filepath.Join(flyDir, "fly.toml")); err != nil {
		fprintError("mar fly deploy: %s not found", colorMagenta(filepath.Join(flyDir, "fly.toml")))
		fprintHint("run %s first.", colorGreen(flySuggestion("init", path)))
		return 1
	}
	if _, err := requireFlyCLI(); err != nil {
		fprintError("%v", err)
		return 1
	}

	distDir := filepath.Join(flyDir, "dist")
	fmt.Printf("[mar fly deploy] building %s for %s → %s\n",
		colorCyan(manifest.Name),
		colorCyan("linux-amd64"),
		colorMagenta(distDir))
	if rc := runSelf([]string{"build", "--target", "linux-amd64", "--out", distDir, projectDir}); rc != 0 {
		return rc
	}

	fmt.Printf("[mar fly deploy] running %s from %s\n", colorGreen("fly deploy"), colorMagenta(flyDir))
	cmd := exec.Command("fly", "deploy")
	cmd.Dir = flyDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		fprintError("mar fly deploy: fly deploy failed: %v", err)
		return 1
	}
	return 0
}

// runFlyDestroy tears down the fly app + volume. Two-stage confirm:
// first a generic Y/N, then the user must type the app name.
func runFlyDestroy(path string) int {
	projectDir, _, err := loadFlyManifest(path)
	if err != nil {
		fprintError("mar fly destroy: %v", err)
		return 1
	}
	flyToml := filepath.Join(projectDir, "deploy", "fly", "fly.toml")
	cfg, err := readFlyToml(flyToml)
	if err != nil {
		fprintError("mar fly destroy: %v", err)
		return 1
	}
	if _, err := requireFlyCLI(); err != nil {
		fprintError("%v", err)
		return 1
	}

	fmt.Println()
	fmt.Printf("%s This will permanently destroy the fly app %s and its volume %s.\n",
		colorRed("⚠"),
		colorCyan(cfg.AppName),
		colorCyan(cfg.VolumeName))
	fmt.Printf("%s All data in the volume will be lost. There is no undo.\n", colorRed("⚠"))
	fmt.Println()
	if !confirmPrompt("Continue?") {
		fmt.Println("aborted.")
		return 0
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("Type %s to confirm: ", colorCyan(cfg.AppName))
	typed, _ := reader.ReadString('\n')
	if strings.TrimSpace(typed) != cfg.AppName {
		fmt.Println("aborted (name didn't match).")
		return 0
	}

	if err := ensureFlyAuth(); err != nil {
		fprintError("mar fly destroy: %v", err)
		return 1
	}

	// Pre-flight: confirm the app actually exists before invoking
	// `fly apps destroy`. Without this check, fly's own error
	// message ("App not found" buried in flyctl output) is harder
	// to interpret — this gives a clean, actionable message and
	// points at the deploy/fly/fly.toml that the wrapper read.
	if err := ensureFlyAppExists(cfg.AppName); err != nil {
		fprintError("mar fly destroy: %v", err)
		return 1
	}

	fmt.Printf("\n[mar fly destroy] destroying app %s (volumes are removed with it)\n", colorCyan(cfg.AppName))
	if err := runFlyCmd("apps", "destroy", cfg.AppName, "--yes"); err != nil {
		fprintError("mar fly destroy: %v", err)
		return 1
	}
	fmt.Printf("\n%s Fly destroy finished.\n\n", colorGreen("✓"))
	return 0
}

// ensureFlyAppExists verifies the named app is visible to the
// authenticated fly user. Runs `fly status -a <name>` and inspects
// the output: when the app doesn't exist, fly returns non-zero with
// a recognizable "Could not find App" message that we surface as a
// friendly error pointing at the user's local fly.toml. Any other
// non-zero (network, auth, etc.) is propagated verbatim so the user
// can see what fly itself said.
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
	if strings.Contains(out, "Could not find App") {
		return fmt.Errorf(
			"could not find Fly.io app %q.\n\nHint: the app may have already been destroyed, or `deploy/fly/fly.toml` may point at a different app name. Check the `app = \"...\"` line and try again.",
			appName,
		)
	}
	if out != "" {
		// Surface fly's own message (auth error, network blip, etc.)
		// so the user has something to act on.
		return fmt.Errorf("checking app %q failed:\n%s", appName, out)
	}
	return fmt.Errorf("failed to verify whether Fly.io app %q exists", appName)
}

// runFlyLogs streams `fly logs` for the project's app.
func runFlyLogs(path string) int {
	cfg, err := loadFlyConfig(path)
	if err != nil {
		fprintError("mar fly logs: %v", err)
		return 1
	}
	if _, err := requireFlyCLI(); err != nil {
		fprintError("%v", err)
		return 1
	}
	return runFlyCmdInteractive("logs", "-a", cfg.AppName)
}

// runFlyAdmin dispatches `mar fly admin <sub>`. Currently only
// `list` is supported — adding admins on production is intentionally
// not exposed through the Fly wrapper (it would invite confusion
// between mar.json edits and runtime mutations; see admin-panel.md
// §6.2). For the rare emergency case use `fly ssh console -C
// "mar-runtime admin add EMAIL"` directly.
func runFlyAdmin(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, flyAdminUsage)
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
		fmt.Println(flyAdminUsage)
		return 0
	default:
		fprintError("mar fly admin: unknown subcommand %q", sub)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, flyAdminUsage)
		return 2
	}
}

const flyAdminUsage = `Usage: mar fly admin <command> [path]

Commands:
  list       Show the production runtime's _mar_admins state plus
             last-login data. Read-only.

Adding or removing admins happens via mar.json + mar fly deploy
(the source of truth is the committed config). For an urgent runtime
override (rare, lossy, reverted on next deploy):

  fly ssh console --app APP -C "mar-runtime admin add EMAIL"
  fly ssh console --app APP -C "mar-runtime admin remove EMAIL"`

// runFlyAdminList wraps fly ssh console -C "mar-runtime admin list"
// so the user doesn't have to remember the SSH incantation. The
// production binary's `admin list` subcommand prints the post-sync
// _mar_admins state + last-login info.
func runFlyAdminList(path string) int {
	cfg, err := loadFlyConfig(path)
	if err != nil {
		fprintError("mar fly admin list: %v", err)
		return 1
	}
	if _, err := requireFlyCLI(); err != nil {
		fprintError("%v", err)
		return 1
	}
	if err := ensureFlyAppExists(cfg.AppName); err != nil {
		fprintError("mar fly admin list: %v", err)
		return 1
	}
	return runFlyCmdInteractive("ssh", "console",
		"--app", cfg.AppName,
		"-C", "mar-runtime admin list",
	)
}

// runFlyStatus shows `fly status` for the project's app.
func runFlyStatus(path string) int {
	cfg, err := loadFlyConfig(path)
	if err != nil {
		fprintError("mar fly status: %v", err)
		return 1
	}
	if _, err := requireFlyCLI(); err != nil {
		fprintError("%v", err)
		return 1
	}
	return runFlyCmdInteractive("status", "-a", cfg.AppName)
}

// ---------- Interactive prompts ----------

// promptFlyAppName asks the user to confirm or override the Fly app
// name. Default is the mar.json `name` (slugified to fly's
// constraints). Non-interactive shells accept the default silently.
func promptFlyAppName(defaultName string) (string, error) {
	defaultName = slugifyFlyAppName(defaultName)
	if defaultName == "" {
		defaultName = "mar-app"
	}
	if !stdinIsTerminal() {
		// Non-interactive: take the default. Still validate it.
		if !flyAppNameRe.MatchString(defaultName) {
			return "", fmt.Errorf("invalid default app name %q: use lowercase letters, digits, and hyphens", defaultName)
		}
		return defaultName, nil
	}

	fmt.Println()
	fmt.Println("Fly app name")
	fmt.Println("  This is the name your app will have on fly.io.")
	fmt.Printf("  Press Enter to use: %s\n", colorCyan(defaultName))
	fmt.Print("  Fly app name? ")

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return "", err
	}
	name := strings.TrimSpace(line)
	if name == "" {
		name = defaultName
	}
	if !flyAppNameRe.MatchString(name) {
		return "", fmt.Errorf("invalid Fly app name %q: use lowercase letters, digits, and hyphens", name)
	}
	return name, nil
}

// promptFlyRegion presents the region picker. Honors FLY_REGION env
// var (CI override) before showing the table. Loops until a valid
// code is entered. Errors out if the shell isn't a TTY and no
// FLY_REGION is set — running fly init non-interactively without a
// region preset would silently default to whichever region we
// arbitrarily picked, which the operator wouldn't notice.
func promptFlyRegion() (flyRegion, error) {
	if code := strings.TrimSpace(os.Getenv("FLY_REGION")); code != "" {
		region, ok := findFlyRegion(code)
		if !ok {
			return flyRegion{}, fmt.Errorf("invalid FLY_REGION %q: use one of the supported Fly region codes", code)
		}
		return region, nil
	}
	if !stdinIsTerminal() {
		return flyRegion{}, fmt.Errorf("Fly region is required.\n\nHint: re-run in an interactive terminal, or export FLY_REGION with a valid Fly region code (e.g. FLY_REGION=gru).")
	}

	fmt.Println()
	fmt.Println(colorBold("Fly region"))
	fmt.Println("  Pick the region closest to your users, then enter its code.")
	fmt.Println()
	// Headers in bold so they stand out from the data rows; pad
	// BEFORE coloring so ANSI escape bytes don't count toward the
	// column width.
	fmt.Printf("  %s %s\n",
		colorBold(fmt.Sprintf("%-32s", "NAME")),
		colorBold("CODE"))

	lastContinent := ""
	for _, region := range flyRegions {
		if region.Continent != lastContinent {
			fmt.Println()
			fmt.Printf("  %s\n", colorYellow(region.Continent))
			lastContinent = region.Continent
		}
		// Region name plain (descriptive text), code in cyan
		// (identifier the user types back). Same convention as
		// app names elsewhere in the wrapper.
		fmt.Printf("  %-32s %s\n", region.Name, colorCyan(region.Code))
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("\n  Region code? ")
		line, err := reader.ReadString('\n')
		if err != nil && strings.TrimSpace(line) == "" {
			return flyRegion{}, err
		}
		code := strings.ToLower(strings.TrimSpace(line))
		region, ok := findFlyRegion(code)
		if ok {
			return region, nil
		}
		fmt.Printf("\n  Invalid Fly region %q\n", code)
		fmt.Println("  Hint: enter one of the codes listed above (e.g. gru, iad, fra).")
	}
}

func findFlyRegion(code string) (flyRegion, bool) {
	normalized := strings.ToLower(strings.TrimSpace(code))
	for _, region := range flyRegions {
		if region.Code == normalized {
			return region, true
		}
	}
	return flyRegion{}, false
}

// promptFlyAppMemory shows the numbered memory options, accepts
// either the index (1, 2, 3, 4) or the literal value (256mb, 1gb),
// and returns the selected size. Empty input picks the default.
//
// Honors FLY_MEMORY env var first (CI override). Non-interactive
// shells without FLY_MEMORY set silently take the default — same
// shape as promptFlyRegion would have if the table didn't matter
// (memory has a sensible default; region doesn't).
func promptFlyAppMemory() (string, error) {
	if value := strings.TrimSpace(os.Getenv("FLY_MEMORY")); value != "" {
		memory, ok := normalizeFlyAppMemory(value)
		if !ok {
			return "", fmt.Errorf("invalid FLY_MEMORY %q: use one of %s",
				value, strings.Join(flyAppMemoryOptions, ", "))
		}
		return memory, nil
	}
	if !stdinIsTerminal() {
		return defaultFlyAppMemory, nil
	}

	fmt.Println()
	fmt.Println(colorBold("App memory"))
	fmt.Println("  Smaller values cost less, but give the app less headroom.")
	fmt.Println("  Choose app memory:")
	for i, option := range flyAppMemoryOptions {
		label := option
		if option == defaultFlyAppMemory {
			label += " (default)"
		}
		fmt.Printf("    %d. %s\n", i+1, label)
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("\n  App memory? ")
		line, err := reader.ReadString('\n')
		if err != nil && strings.TrimSpace(line) == "" {
			return "", err
		}
		answer := strings.TrimSpace(line)
		if answer == "" {
			return defaultFlyAppMemory, nil
		}
		// Accept literal value: "256mb", "1gb", etc.
		if selected, ok := normalizeFlyAppMemory(answer); ok {
			return selected, nil
		}
		// Accept numeric index from the listed options.
		if idx, err := strconv.Atoi(answer); err == nil &&
			idx >= 1 && idx <= len(flyAppMemoryOptions) {
			return flyAppMemoryOptions[idx-1], nil
		}
		fmt.Println("  Choose one of the listed options (e.g. 1, 2, 512mb, 1gb).")
	}
}

// normalizeFlyAppMemory case-insensitively matches `value` against
// the supported memory option set. Whitespace tolerant. Returns
// (canonicalForm, ok).
func normalizeFlyAppMemory(value string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, option := range flyAppMemoryOptions {
		if normalized == option {
			return option, true
		}
	}
	return "", false
}

// slugifyFlyAppName turns a project name into a valid Fly app slug.
// Lowercases, replaces non-alphanumerics with hyphens, collapses
// duplicate hyphens, trims leading/trailing hyphens. Mirrors the
// lispy behavior so re-deploying an old app produces the same slug.
func slugifyFlyAppName(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	var out []rune
	prevHyphen := false
	runes := []rune(trimmed)
	for i, r := range runes {
		switch {
		case r >= 'A' && r <= 'Z':
			// Uppercase becomes lowercase. Insert a hyphen if the
			// previous char was lowercase/digit (camelCase splits
			// on case boundaries).
			if i > 0 && !prevHyphen {
				prev := runes[i-1]
				if (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9') {
					out = append(out, '-')
				}
			}
			out = append(out, r+'a'-'A')
			prevHyphen = false
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			out = append(out, r)
			prevHyphen = false
		default:
			if !prevHyphen && len(out) > 0 {
				out = append(out, '-')
				prevHyphen = true
			}
		}
	}
	slug := strings.Trim(string(out), "-")
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	return slug
}

// stdinIsTerminal reports whether stdin is connected to a TTY. Used
// by the interactive prompts to fall back to non-interactive
// defaults (or error out) in CI / piped contexts.
func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// ---------- fly.toml parsing ----------

// flyConfig is the subset of fly.toml we need.
type flyConfig struct {
	AppName    string
	Region     string
	VolumeName string
}

func readFlyToml(path string) (flyConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return flyConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	src := string(raw)
	cfg := flyConfig{
		AppName:    matchTomlString(src, `app`),
		Region:     matchTomlString(src, `primary_region`),
		VolumeName: matchTomlString(src, `source`),
	}
	if cfg.AppName == "" {
		return cfg, fmt.Errorf("%s: missing `app = \"...\"` line", path)
	}
	if cfg.Region == "" {
		return cfg, fmt.Errorf("%s: missing `primary_region = \"...\"` line", path)
	}
	if cfg.VolumeName == "" {
		return cfg, fmt.Errorf("%s: missing `source = \"...\"` under [mounts]", path)
	}
	return cfg, nil
}

func matchTomlString(src, key string) string {
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=\s*"([^"]*)"`)
	m := re.FindStringSubmatch(src)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

func loadFlyConfig(path string) (flyConfig, error) {
	projectDir, _, err := loadFlyManifest(path)
	if err != nil {
		return flyConfig{}, err
	}
	return readFlyToml(filepath.Join(projectDir, "deploy", "fly", "fly.toml"))
}

// loadFlyManifest resolves projectDir from `path` (file or dir) and
// loads mar.json. Errors out when mar.json is missing.
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
	m, err = project.LoadManifest(projectDir)
	if err != nil {
		return "", nil, err
	}
	if m == nil {
		return "", nil, fmt.Errorf("%s/mar.json not found", projectDir)
	}
	return projectDir, m, nil
}

func manifestPort(m *project.Manifest) int {
	if m != nil && m.Server != nil && m.Server.Port > 0 {
		return m.Server.Port
	}
	return 3000
}

func flyVolumeName(appName string) string {
	return strings.ReplaceAll(appName, "-", "_") + "_data"
}

// flySuggestion formats a "next command" hint, omitting the path
// argument when it's the default ("."). Keeps the printed command
// short and reads naturally — `mar fly deploy` instead of the
// awkward `mar fly deploy .`, which looks like a sentence-ending
// period.
//
// When the user invoked the parent command with an explicit path,
// we keep it in the suggestion so they can copy-paste verbatim.
func flySuggestion(sub, path string) string {
	if path == "" || path == "." {
		return "mar fly " + sub
	}
	return "mar fly " + sub + " " + path
}

// ---------- fly CLI helpers ----------

func requireFlyCLI() (string, error) {
	exe, err := exec.LookPath("fly")
	if err != nil {
		return "", fmt.Errorf("'fly' CLI not found in $PATH.\n\n  Install: https://fly.io/docs/flyctl/install/")
	}
	return exe, nil
}

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

// promptAndSetFlySecrets walks env:VAR list discovered in mar.json,
// prompts (echo off) for each value, pushes via `fly secrets set`.
// Empty input skips that var.
func promptAndSetFlySecrets(appName string, envRefs []string) error {
	pairs := make([]string, 0, len(envRefs))
	for _, name := range envRefs {
		fmt.Printf("  %s (leave blank to skip): ", name)
		val, err := readPasswordEnter()
		if err != nil {
			return err
		}
		if val == "" {
			fmt.Println("  (skipped)")
			continue
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

// discoverManifestEnvRefs is a thin wrapper over project.EnvRefsFromFile
// so callers in this file don't have to import internal/project just
// for one helper. Renames may consolidate this further later.
func discoverManifestEnvRefs(manifestPath string) ([]string, error) {
	return project.EnvRefsFromFile(manifestPath)
}

// runFlyCmd runs `fly <args...>` with stdout/stderr/stdin forwarded
// to the user's terminal. Used for non-interactive commands where we
// want fly's output to be visible.
func runFlyCmd(args ...string) error {
	cmd := exec.Command("fly", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// runFlyCmdInteractive is for fly subcommands that expect a TTY
// (logs, status). Returns the rc directly so the wrapper exits with
// whatever fly's CLI returned.
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

// readPasswordEnter reads a line from stdin with echo disabled when
// stdin is a terminal; falls back to plain ReadString otherwise.
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

func writeFlyTemplate(dst, tmplSrc, tmplName string, data any) error {
	t, err := template.New(tmplName).Parse(tmplSrc)
	if err != nil {
		return fmt.Errorf("parse template %s: %w", tmplName, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return fmt.Errorf("render %s: %w", tmplName, err)
	}
	if err := os.WriteFile(dst, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

func dirHasFiles(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return len(entries) > 0, nil
}

func runSelf(args []string) int {
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return 1
	}
	return 0
}
