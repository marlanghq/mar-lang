// New deploy flow — drives everything from mar.json's deploy.fly
// block, generates the Dockerfile + fly.toml ephemerally in /tmp,
// and runs `fly deploy` from there. The operator never sees or
// edits a Dockerfile.
//
// Phases inside runFlyDeployV2:
//
//  1. Load mar.json + validate deploy.fly block (app, region, memory)
//  2. Run main → detect topology (frontend / backend / fullstack)
//  3. Check Fly app exists (first deploy gates differ by topology)
//  4. Sync secrets (prompt for missing env:VAR refs)
//  5. Build the artifact into a fresh tmp dir (linux-amd64 for
//     backend/fullstack; static bundle for frontend)
//  6. Generate Dockerfile + fly.toml in the same tmp dir
//  7. Run `fly deploy` from the tmp dir
//  8. Health-check the resulting <app>.fly.dev
//  9. Cleanup tmp on success — keep + print path on failure for
//     post-mortem
//
// Currently invoked via `mar fly deploy --new` so the old flow stays
// the default until this is proven end-to-end. P7 will swap the
// default and delete the old code.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"mar/internal/project"
	"mar/internal/scaffold"
)

// runFlyDeploy is the entry point for `mar fly deploy`. Returns a
// process exit code (0 = success).
//
// noOpen skips the post-success browser open. CI environments
// (CI=true) skip the browser regardless via shouldOpenBrowser.
func runFlyDeploy(path string, noOpen bool) int {
	// === 1. Manifest load + validate ===
	// Going through resolveFlyApp (vs raw loadFlyManifest +
	// ValidateDeployFly) so the region-validity check
	// (invalid-region) fires for `mar fly deploy` exactly like it
	// does for the sibling subcommands (preview, logs, status,
	// secrets, destroy). Without this, a typo like region="gr"
	// would slip past validation and only fail one minute into
	// the actual Fly deploy.
	_, projectDir, manifest, err := resolveFlyApp(path)
	if err != nil {
		if _, ok := err.(*project.DeployFlyError); ok {
			printDeployFlyError(err)
		} else {
			printManifestError("mar fly deploy", err)
		}
		return 1
	}
	fly := manifest.Deploy.Fly

	// === 2. Topology detection (runs main as side effect; surfaces
	// compile errors / missing config BEFORE any Fly interaction) ===
	topo, err := scaffold.Topology(projectDir)
	if err != nil {
		printError("mar fly deploy", err)
		return 1
	}
	flyTopo := flyTopology(topo)

	// === 2b. Pre-flight: for server topologies, mar.json must carry the
	// production config the runtime needs at boot (auth + mail). Run it
	// BEFORE the cloud steps below (app + volume creation, secret sync) so
	// a missing-config gap surfaces up front instead of after Fly resources
	// are already provisioned. main has already run (Topology). ===
	if err := flyPreDeployValidate(projectDir, flyTopo); err != nil {
		var pcErr *scaffold.ProductionConfigError
		if errors.As(err, &pcErr) {
			printProductionConfigError(pcErr)
		} else {
			printError("mar fly deploy", err)
		}
		return 1
	}

	// === 3. Banner ===
	fmt.Println()
	fmt.Println(colorBold("Fly deploy"))
	fmt.Printf("  app:      %s\n", colorCyan(fly.App))
	fmt.Printf("  region:   %s\n", colorCyan(fly.Region))
	fmt.Printf("  memory:   %s\n", colorCyan(fly.Memory))
	fmt.Printf("  topology: %s\n", colorCyan(topo))
	fmt.Println()

	// === 4. Pre-flight: Fly CLI + auth ===
	if _, err := requireFlyCLI(); err != nil {
		fprintError("%v", err)
		return 1
	}
	// `fly auth whoami` is a silent ~1-2s network call when the
	// operator is logged in. Without a status line, the CLI looks
	// like it hung right after the banner — same problem
	// waitForAppHealthy solves at the end of the deploy. Reuse the
	// same TTY-aware progress pattern (in-place \r\033[K redraw on
	// success; one-shot lines on non-TTY).
	progressStep("Fly authentication", func() {
		if err := ensureFlyAuth(); err != nil {
			fprintError("mar fly deploy: %v", err)
			os.Exit(1)
		}
	})

	// === 5. App existence check + first-deploy gates ===
	// `fly status -a` is another ~1-2s silent network call. Same
	// pattern.
	var appExists bool
	progressStep("Fly app status", func() {
		appExists = flyAppExistsOnAccount(fly.App)
	})
	interactive := isInteractive()

	if !appExists {
		// Frontend-only first-deploy warning. Only shown on the
		// first deploy of THIS app (proxy: app doesn't exist on
		// Fly yet) — subsequent deploys assume the operator has
		// already weighed the trade-off.
		if flyTopo == flyTopologyFrontend && interactive {
			printFrontendOnlyWarning()
			if !confirmPrompt("Continue with Fly?") {
				fmt.Println("aborted.")
				return 0
			}
			fmt.Println()
		}

		// App creation prompt.
		if interactive {
			fmt.Printf("Fly app %s doesn't exist yet.\n", colorCyan(fly.App))
			fmt.Println()
			if !confirmPrompt(fmt.Sprintf("Create it now in region %s?", colorCyan(fly.Region))) {
				fmt.Println("aborted; no app created.")
				return 0
			}
			// Visual breathing room before flyctl's own output
			// ("automatically selected personal organization: ...")
			// starts streaming in below.
			fmt.Println()
		}

		if err := createFlyApp(fly.App); err != nil {
			fprintError("mar fly deploy: %v", err)
			return 1
		}
		fmt.Printf("  %s created Fly app %s\n", colorGreen("✓"), colorCyan(fly.App))
	}

	// === 6. Volume creation (backend / fullstack only) ===
	if flyTopo == flyTopologyBackend || flyTopo == flyTopologyFullstack {
		if err := ensureFlyVolume(fly.App, fly.Region); err != nil {
			fprintError("mar fly deploy: %v", err)
			return 1
		}
	}

	// === 7. Secrets sync — push every env:VAR in mar.json that's not
	// already set on the app. Prompts for missing values when
	// interactive; refuses to proceed silently in CI. ===
	manifestEnvRefs, _ := discoverManifestEnvRefs(filepath.Join(projectDir, "mar.json"))
	if len(manifestEnvRefs) > 0 {
		missing := missingFlySecrets(fly.App, manifestEnvRefs)
		if len(missing) > 0 {
			if !interactive {
				fprintError(
					"mar fly deploy: %s missing on Fly app %s: %s",
					pluralizeSecrets(len(missing)),
					colorCyan(fly.App),
					colorMagenta(strings.Join(missing, ", ")))
				fprintHint("set them before re-running, or run interactively.")
				return 1
			}
			sessionVar := manifestSessionSecretVar(manifest)
			if err := promptAndSetFlySecrets(fly.App, missing, sessionVar); err != nil {
				fprintError("mar fly deploy: %v", err)
				return 1
			}
		}
	}

	// === 8. Build artifact + generate Dockerfile + fly.toml into a
	// fresh tmp dir. The dir is deleted on success; preserved on
	// failure so the operator can inspect what was about to deploy. ===
	tmpDir, err := os.MkdirTemp("", "mar-fly-deploy-*")
	if err != nil {
		fprintError("mar fly deploy: create tmp dir: %v", err)
		return 1
	}
	success := false
	defer func() {
		if success {
			_ = os.RemoveAll(tmpDir)
		} else {
			fmt.Printf("\nGenerated deploy files preserved at:\n  %s\n", colorMagenta(tmpDir))
			fmt.Println("  (delete the directory when done debugging)")
		}
	}()

	distDir := filepath.Join(tmpDir, "dist")
	buildTarget := ""
	if flyTopo != flyTopologyFrontend {
		buildTarget = "linux-amd64"
	}

	fmt.Printf("[mar fly deploy] building %s → %s\n", colorCyan(manifest.Name), colorMagenta(distDir))
	if err := scaffold.Build(projectDir, distDir, buildTarget); err != nil {
		printError("mar fly deploy", err)
		return 1
	}

	dockerfile := generateDockerfile(flyTopo, manifest.Name, manifestPort(manifest))
	if err := os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		fprintError("mar fly deploy: write Dockerfile: %v", err)
		return 1
	}
	flyToml := generateFlyToml(manifest, flyTopo)
	if err := os.WriteFile(filepath.Join(tmpDir, "fly.toml"), []byte(flyToml), 0o644); err != nil {
		fprintError("mar fly deploy: write fly.toml: %v", err)
		return 1
	}

	// === 9. fly deploy from the tmp dir ===
	fmt.Printf("[mar fly deploy] running %s\n", colorGreen("fly deploy"))
	cmd := exec.Command("fly", "deploy")
	cmd.Dir = tmpDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		fprintError("mar fly deploy: fly deploy failed: %v", err)
		return 1
	}

	// === 10. Health check the resulting <app>.fly.dev ===
	appURL := "https://" + fly.App + ".fly.dev"
	if !runHealthCheck(fly.App, appURL) {
		return 1
	}

	// === 11. Post-deploy summary ===
	fmt.Println()
	fmt.Printf("  %s deployed: %s\n", colorGreen("✓"), colorCyan(appURL))
	if shouldOpenBrowser(noOpen) {
		openURL(appURL)
	}
	fmt.Println()

	success = true
	return 0
}

// printDeployFlyError renders a *project.DeployFlyError as a friendly
// CLI message with a paste-ready snippet for missing-block cases.
// Other error kinds (missing-app / missing-region / etc.) get a
// one-liner since the operator just needs to add one field.
func printDeployFlyError(err error) {
	dfe, ok := err.(*project.DeployFlyError)
	if !ok {
		printError("mar fly deploy", err)
		return
	}
	switch dfe.Kind {
	case "missing-block":
		// Color palette (per docs/cli-style.md):
		//   - magenta : paths, config keys, field names (mar.json,
		//               deploy.fly, "app", "region", "memory")
		//   - cyan    : values the operator would type (region codes,
		//               memory sizes, URL placeholders)
		//   - bold    : section titles + continent headers
		//   - red     : the "Error:" prefix
		//
		// The JSON snippet is hand-colored token-by-token (no JSON
		// lexer) — fine because the snippet is hard-coded right here
		// and won't grow keys at runtime.

		// Pre-compute the regions block BEFORE printing anything.
		// formatRegionsBlock shells out to `fly platform regions
		// --json` (1-2s); resolving it up-front lets the entire
		// message stream to the terminal in one go. If we called it
		// inline between the Hints and Valid-memory-sizes sections,
		// the pause would land mid-output and read as "hung
		// mid-render" instead of "starting up".
		regionsBlock := formatRegionsBlock()

		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "%s mar fly deploy: %s has no %s block.\n",
			colorRed("Error:"), colorMagenta("mar.json"), colorMagenta("deploy.fly"))
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "%s %s:\n", colorBold("Add this to"), colorMagenta("mar.json"))
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "  %s: {\n", colorMagenta(`"deploy"`))
		fmt.Fprintf(os.Stderr, "    %s: {\n", colorMagenta(`"fly"`))
		fmt.Fprintf(os.Stderr, "      %s:    %s,\n",
			colorMagenta(`"app"`), colorCyan(`"my-app-name"`))
		fmt.Fprintf(os.Stderr, "      %s: %s,\n",
			colorMagenta(`"region"`), colorCyan(`"<region>"`))
		fmt.Fprintf(os.Stderr, "      %s: %s\n",
			colorMagenta(`"memory"`), colorCyan(`"<size>"`))
		fmt.Fprintln(os.Stderr, "    }")
		fmt.Fprintln(os.Stderr, "  }")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, colorBold("Hints:"))
		fmt.Fprintf(os.Stderr, "  %s     : globally unique on Fly (becomes %s)\n",
			colorMagenta("app"), colorCyan("<app>.fly.dev"))
		fmt.Fprintf(os.Stderr, "  %s  : pick a 3-letter code from the list below\n",
			colorMagenta("region"))
		fmt.Fprintf(os.Stderr, "  %s  : pick a machine size from the list below\n",
			colorMagenta("memory"))
		fmt.Fprintln(os.Stderr)
		// regionsBlock and formatMemoryHelp each end with a blank
		// line, so use Fprint (no extra newline) to avoid doubled
		// blanks between the sections.
		fmt.Fprint(os.Stderr, regionsBlock)
		fmt.Fprint(os.Stderr, formatMemoryHelp())
	case "missing-app":
		fprintError("mar fly deploy: %s is missing %s.",
			colorMagenta("mar.json"), colorMagenta("deploy.fly.app"))
		fprintHint("%s is the globally-unique Fly app name; it becomes\n"+
			"      %s and must not collide with another Fly user's app.\n"+
			"\n"+
			"      Add to %s:\n"+
			"\n"+
			"        %s: {\n"+
			"          %s: {\n"+
			"            %s: %s,\n"+
			"            ...\n"+
			"          }\n"+
			"        }",
			colorMagenta("app"),
			colorCyan("<app>.fly.dev"),
			colorMagenta("mar.json"),
			colorMagenta(`"deploy"`),
			colorMagenta(`"fly"`),
			colorMagenta(`"app"`),
			colorCyan(`"my-app-name"`))
	case "missing-region":
		// Pre-compute the regions block to avoid the mid-print pause
		// (same rationale as the missing-block branch).
		regionsBlock := formatRegionsBlock()
		fprintError("mar fly deploy: %s is missing %s.",
			colorMagenta("mar.json"), colorMagenta("deploy.fly.region"))
		fprintHint("pick a Fly region close to your users, paste the 3-letter\n" +
			"      code into the manifest.")
		// fprintHint already emits a trailing blank; the regions
		// block (and formatMemoryHelp below) begin with content, so
		// no extra Fprintln needed here.
		fmt.Fprint(os.Stderr, regionsBlock)
	case "invalid-region":
		// Same structure as missing-region — the operator picked a
		// code that doesn\'t exist on Fly. Pre-compute the regions
		// block so the live-fetch fallback (when bundled\'s "no" is
		// rechecked against live) doesn\'t pause mid-print.
		regionsBlock := formatRegionsBlock()
		fprintError("mar fly deploy: %s = %s is not a valid Fly region.",
			colorMagenta("deploy.fly.region"), colorRed(fmt.Sprintf("%q", dfe.BadValue)))
		fprintHint("pick a 3-letter code from the list below.")
		fmt.Fprint(os.Stderr, regionsBlock)
	case "missing-memory":
		fprintError("mar fly deploy: %s is missing %s.",
			colorMagenta("mar.json"), colorMagenta("deploy.fly.memory"))
		fprintHint("pick a machine size based on workload:")
		fmt.Fprint(os.Stderr, formatMemoryHelp())
	case "invalid-memory":
		fprintError("mar fly deploy: %s = %s is not a valid Fly machine size.",
			colorMagenta("deploy.fly.memory"), colorRed(fmt.Sprintf("%q", dfe.BadValue)))
		fprintHint("pick one of the sizes below.")
		fmt.Fprint(os.Stderr, formatMemoryHelp())
	default:
		// Fallback for any future Kind we forget to handle. Plain
		// printError keeps the line readable even if the structured
		// renderer doesn\'t know it.
		printError("mar fly deploy", err)
	}
}

// formatMemoryHelp returns the colored "pick based on workload" hint
// block + the "Valid memory sizes" enumeration, formatted as a
// single string ready to Fprint to stderr. Shared between the
// missing-block, missing-memory, and invalid-memory branches so the
// memory guidance reads identically across all three.
//
// The trailing blank line lets callers Fprint the result without
// having to manage spacing themselves.
func formatMemoryHelp() string {
	var b strings.Builder
	b.WriteString("        ")
	b.WriteString(colorCyan("256mb"))
	b.WriteString("  : light App.fullstack\n")
	b.WriteString("        ")
	b.WriteString(colorCyan("512mb"))
	b.WriteString("  : typical App.fullstack\n")
	b.WriteString("        ")
	b.WriteString(colorCyan("1gb"))
	b.WriteString("    : heavier traffic, larger DB\n")
	b.WriteString("\n")
	b.WriteString(colorBold("Valid memory sizes:"))
	b.WriteString("\n  ")
	b.WriteString(colorCyan("256mb"))
	b.WriteString("  ")
	b.WriteString(colorCyan("512mb"))
	b.WriteString("  ")
	b.WriteString(colorCyan("1gb"))
	b.WriteString("  ")
	b.WriteString(colorCyan("2gb"))
	b.WriteString("  ")
	b.WriteString(colorCyan("4gb"))
	b.WriteString("  ")
	b.WriteString(colorCyan("8gb"))
	b.WriteString("\n\n")
	return b.String()
}

// bundledFlyRegion mirrors a single Fly region — code + display name.
// Kept tiny + JSON-tag-free since this is internal to the CLI's error
// rendering, not part of any wire format.
type bundledFlyRegion struct {
	code      string
	name      string
	continent string
}

// bundledFlyRegions is the offline fallback list — used when
// `fly platform regions --json` is unavailable (CLI not installed,
// network down, fly.io API hiccup). Updated periodically by maintenance
// when the live list drifts; the runtime never validates against this
// list because Fly is the source of truth — we only use it for the
// "what are my options?" hint inside error messages.
var bundledFlyRegions = []bundledFlyRegion{
	{code: "jnb", name: "Johannesburg", continent: "Africa"},
	{code: "bom", name: "Mumbai", continent: "Asia Pacific"},
	{code: "nrt", name: "Tokyo", continent: "Asia Pacific"},
	{code: "sin", name: "Singapore", continent: "Asia Pacific"},
	{code: "syd", name: "Sydney", continent: "Asia Pacific"},
	{code: "ams", name: "Amsterdam", continent: "Europe"},
	{code: "arn", name: "Stockholm", continent: "Europe"},
	{code: "cdg", name: "Paris", continent: "Europe"},
	{code: "fra", name: "Frankfurt", continent: "Europe"},
	{code: "lhr", name: "London", continent: "Europe"},
	{code: "dfw", name: "Dallas", continent: "North America"},
	{code: "ewr", name: "Secaucus, NJ", continent: "North America"},
	{code: "iad", name: "Ashburn", continent: "North America"},
	{code: "lax", name: "Los Angeles", continent: "North America"},
	{code: "ord", name: "Chicago", continent: "North America"},
	{code: "sjc", name: "San Jose", continent: "North America"},
	{code: "yyz", name: "Toronto", continent: "North America"},
	{code: "gru", name: "São Paulo", continent: "South America"},
}

// formatRegionsBlock returns the multi-line "Valid regions:" block
// rendered for inclusion in error messages. Tries `fly platform
// regions --json` first; on any failure (CLI missing, network, parse
// error) falls back to bundledFlyRegions silently — the operator
// shouldn\'t see a degraded experience just because we wanted live
// data. Output is identical in shape regardless of source.
func formatRegionsBlock() string {
	regions := cachedLiveFlyRegions()
	if regions == nil {
		regions = bundledFlyRegions
	}
	return renderRegionTable(regions)
}

// isKnownFlyRegion reports whether `code` is a real Fly region.
// Fast-path: check bundledFlyRegions (~18 entries, instant). Slow-
// path: if the bundle says no, try the live list — covers cases
// where Fly adds new regions we haven\'t catalogued. The bundled
// "yes" answer is authoritative (Fly never *removes* regions
// quickly enough to outrun a release cycle); only the bundled "no"
// triggers the live fetch.
//
// When live is unavailable (CLI missing, network), the bundled "no"
// stands — bias toward catching typos like "gr"/"grub" early.
func isKnownFlyRegion(code string) bool {
	for _, r := range bundledFlyRegions {
		if r.code == code {
			return true
		}
	}
	// Not in our bundled set — maybe Fly added a region we haven\'t
	// catalogued. Consult the live list as the source of truth.
	live := cachedLiveFlyRegions()
	if live == nil {
		return false
	}
	for _, r := range live {
		if r.code == code {
			return true
		}
	}
	return false
}

// cachedLiveFlyRegions runs fetchLiveFlyRegions at most once per
// process. Two call sites benefit from the cache: isKnownFlyRegion
// (which fires on every `mar fly *` invocation when the region is
// non-bundled) and formatRegionsBlock (which fires on the error
// rendering path). Without the cache, an invalid-region scenario
// would shell out to `fly platform regions --json` twice — once
// for the validation, once for the error block.
var (
	cachedLiveFlyRegionsOnce sync.Once
	cachedLiveFlyRegionsVal  []bundledFlyRegion
)

func cachedLiveFlyRegions() []bundledFlyRegion {
	cachedLiveFlyRegionsOnce.Do(func() {
		cachedLiveFlyRegionsVal = fetchLiveFlyRegions()
	})
	return cachedLiveFlyRegionsVal
}

// fetchLiveFlyRegions shells out to `fly platform regions --json`
// and parses the result. Returns nil on any failure — caller falls
// back to the bundled list. Best-effort: this is a UX nicety, not a
// correctness gate.
func fetchLiveFlyRegions() []bundledFlyRegion {
	out, err := exec.Command("fly", "platform", "regions", "--json").Output()
	if err != nil {
		return nil
	}
	// Live shape (subset of fields we care about):
	//   [{"code":"...","name":"...","deprecated":false,...}, ...]
	var raw []struct {
		Code       string `json:"code"`
		Name       string `json:"name"`
		Deprecated bool   `json:"deprecated"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil
	}
	regions := make([]bundledFlyRegion, 0, len(raw))
	for _, r := range raw {
		if r.Deprecated {
			continue
		}
		regions = append(regions, bundledFlyRegion{
			code:      r.Code,
			name:      shortenRegionName(r.Name),
			continent: continentForRegion(r.Code, r.Name),
		})
	}
	return regions
}

// shortenRegionName trims Fly\'s verbose `City, State (Country)`
// labels down to just the leading segment so the column layout
// stays compact. Examples:
//
//	"Los Angeles, California (US)"  → "Los Angeles"
//	"Mumbai, India"                  → "Mumbai"
//	"São Paulo, Brazil"              → "São Paulo"
//	"Singapore, Singapore"           → "Singapore"
//
// Everything after the first comma is dropped. Codes remain
// authoritative — the city is for human recognition only.
func shortenRegionName(name string) string {
	if idx := strings.Index(name, ","); idx >= 0 {
		return strings.TrimSpace(name[:idx])
	}
	return name
}

// continentForRegion infers the continent for a region from its
// display name. Used only when ingesting the live list (the bundled
// list carries continent labels statically). Falls back to "Other"
// for entries that don\'t match any known suffix — keeps the renderer
// from blowing up on a region in a continent we haven\'t seen yet.
func continentForRegion(code, name string) string {
	// Use the bundled list as a hint when codes are familiar — same
	// continent as before, even if the live `name` is slightly
	// different (Fly occasionally tweaks city labels).
	for _, b := range bundledFlyRegions {
		if b.code == code {
			return b.continent
		}
	}
	// Suffix-based fallback for codes we\'ve never seen. Imperfect
	// but covers the vast majority of cases. Order matters: most
	// specific first.
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "brazil") ||
		strings.Contains(lower, "argentina") ||
		strings.Contains(lower, "chile") ||
		strings.Contains(lower, "colombia"):
		return "South America"
	case strings.Contains(lower, "africa"):
		return "Africa"
	case strings.Contains(lower, "japan") ||
		strings.Contains(lower, "india") ||
		strings.Contains(lower, "singapore") ||
		strings.Contains(lower, "australia") ||
		strings.Contains(lower, "hong kong"):
		return "Asia Pacific"
	case strings.Contains(lower, "germany") ||
		strings.Contains(lower, "france") ||
		strings.Contains(lower, "netherlands") ||
		strings.Contains(lower, "uk") ||
		strings.Contains(lower, "kingdom") ||
		strings.Contains(lower, "sweden") ||
		strings.Contains(lower, "spain") ||
		strings.Contains(lower, "romania") ||
		strings.Contains(lower, "poland"):
		return "Europe"
	case strings.Contains(lower, "us)") ||
		strings.Contains(lower, "canada") ||
		strings.Contains(lower, "mexico"):
		return "North America"
	}
	return "Other"
}

// renderRegionTable lays out the two-column "by continent" table.
// Left column: bigger continents (North America + Europe). Right
// column: smaller (Asia Pacific + Africa + South America + Other).
// Layout is mostly static — we just slot the live data into the
// continent buckets and align with fixed column widths.
func renderRegionTable(regions []bundledFlyRegion) string {
	groups := make(map[string][]bundledFlyRegion)
	for _, r := range regions {
		groups[r.continent] = append(groups[r.continent], r)
	}
	// Sort each group alphabetically by code for determinism.
	for k := range groups {
		sortRegionsByCode(groups[k])
	}

	// Build left + right column line-by-line.
	leftSections := []string{"North America", "Europe"}
	rightSections := []string{"Asia Pacific", "Africa", "South America", "Other"}

	left := renderColumn(leftSections, groups)
	right := renderColumn(rightSections, groups)

	// Stitch the two columns side by side. Pad the left column to a
	// fixed width so the right column starts at a stable position.
	// Each entry is {coloredText, visibleWidth} so the padding math
	// uses visible widths (ANSI escapes don\'t take screen space) —
	// without this, the right column would shift left by ~22 chars
	// per colored region row.
	const leftWidth = 29 // 2 spaces indent + 4 (code) + 2 + ~21 (name)
	maxLines := len(left)
	if len(right) > maxLines {
		maxLines = len(right)
	}
	var b strings.Builder
	b.WriteString(colorBold("Valid regions:"))
	b.WriteString("\n")
	b.WriteString("\n")
	for i := 0; i < maxLines; i++ {
		var l columnLine
		if i < len(left) {
			l = left[i]
		}
		var r columnLine
		if i < len(right) {
			r = right[i]
		}
		b.WriteString("  ")
		b.WriteString(l.text)
		// Pad to leftWidth using the VISIBLE width of the colored
		// left cell, not its raw byte length.
		for pad := l.width; pad < leftWidth; pad++ {
			b.WriteByte(' ')
		}
		b.WriteString(r.text)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}

// columnLine pairs the rendered (colored) cell text with its visible
// width — the column stitcher needs both because ANSI escape sequences
// in `text` don't contribute to screen position. Empty cells use the
// zero value: text="" width=0, which pads cleanly to leftWidth.
type columnLine struct {
	text  string
	width int
}

// renderColumn renders the sections (continent headers + their
// region rows) of one column as a slice of columnLine. Continent
// headers are bolded; region codes are cyan (they\'re what the
// operator will paste into mar.json); names stay plain (purely
// descriptive). Empty sections are skipped entirely so a continent
// with no regions doesn\'t leave a dangling header.
func renderColumn(sections []string, groups map[string][]bundledFlyRegion) []columnLine {
	var lines []columnLine
	first := true
	for _, sec := range sections {
		rows := groups[sec]
		if len(rows) == 0 {
			continue
		}
		if !first {
			lines = append(lines, columnLine{}) // blank separator
		}
		first = false
		lines = append(lines, columnLine{text: colorBold(sec), width: len(sec)})
		for _, r := range rows {
			// 4-space indent + code + 2 spaces + name. Total ~30 chars
			// when name is moderate length.
			plain := "  " + r.code + "  " + r.name
			text := "  " + colorCyan(r.code) + "  " + r.name
			lines = append(lines, columnLine{text: text, width: len(plain)})
		}
	}
	return lines
}

// sortRegionsByCode is a tiny in-place sort by `code` — Mar\'s
// CLI has its own sort elsewhere but importing sort here just for
// this is fine.
func sortRegionsByCode(rs []bundledFlyRegion) {
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0 && rs[j].code < rs[j-1].code; j-- {
			rs[j], rs[j-1] = rs[j-1], rs[j]
		}
	}
}

// printFrontendOnlyWarning is shown on the FIRST deploy (app doesn't
// exist on Fly yet) of a frontend-only project. Leads with what Fly
// is built for so the user understands what's being skipped, then
// points at the static-host path without endorsing a specific
// provider.
func printFrontendOnlyWarning() {
	fmt.Println()
	fmt.Printf("%s Fly is optimized for fullstack apps (database, services, auth,\n",
		colorYellow("Note:"))
	fmt.Println("      background jobs). This project is frontend-only, so most of")
	fmt.Println("      that machinery is unused. Fly will just serve the static")
	fmt.Println("      bundle. It works, but you're paying for a VM where a CDN")
	fmt.Println("      would do.")
	fmt.Println()
	fmt.Println("      If you stay frontend-only, dedicated static hosts (global CDN,")
	fmt.Println("      generous free tiers) are a better fit. Build the portable")
	fmt.Println("      bundle with:")
	fmt.Printf("        %s\n", cmdSuggest("build"))
	fmt.Println("      and upload `dist/` to your host of choice.")
	fmt.Println()
	fmt.Println("      Stick with Fly if you plan to add a backend later. The switch")
	fmt.Println("      from App.frontend to App.fullstack reuses the same deploy flow.")
	fmt.Println()
}

// flyAppExistsOnAccount probes whether `fly status -a <name>` exits
// zero. Soft check: any non-zero exit (app not found, auth lapsed,
// network blip) returns false; downstream code that needs the
// distinction calls fly directly and inspects its message. Used here
// only to gate "first deploy" UX paths.
func flyAppExistsOnAccount(appName string) bool {
	cmd := exec.Command("fly", "status", "-a", appName)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// flyPreDeployValidate runs the production-config check that must pass
// BEFORE any Fly resource is created. main has already run (Topology), so
// runtime.CurrentAuth etc. are populated and ValidateProductionConfig can
// tell whether the app needs mail config. Frontend deploys carry no
// server-side config, so they skip it. Failing here — rather than at the
// build step, after the app + volume + secrets are already provisioned —
// keeps a misconfigured project from leaving orphaned Fly resources.
func flyPreDeployValidate(projectDir string, topo flyTopology) error {
	if topo == flyTopologyFrontend {
		return nil
	}
	return scaffold.ValidateProductionConfig(projectDir)
}

// createFlyApp runs `fly apps create <name>`. Region is intentionally
// NOT passed here — `fly apps create` doesn\'t accept a --region
// flag (the app itself isn\'t pinned to a region; only its machines
// are). The region we picked surfaces via the generated fly.toml's
// `primary_region` field, which `fly deploy` reads when placing the
// first machine.
func createFlyApp(appName string) error {
	cmd := exec.Command("fly", "apps", "create", appName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("fly apps create: %v", err)
	}
	return nil
}

// ensureFlyVolume creates the SQLite-persistence volume if absent.
// Idempotent: if a volume with the canonical name already exists,
// the function is a no-op (re-deploys hit the same volume, no fresh
// empty one).
//
// `fly volumes list -a <app>` returns the existing volumes; we check
// for the canonical name and skip if present. If absent, run `fly
// volumes create <name> -a <app> -r <region> --size 1 --yes`.
func ensureFlyVolume(appName, region string) error {
	volName := flyVolumeName(appName)

	// Check existing volumes (suppress non-zero exit which can mean
	// "no volumes yet" depending on fly version).
	cmd := exec.Command("fly", "volumes", "list", "-a", appName)
	out, _ := cmd.Output()
	if strings.Contains(string(out), volName) {
		return nil
	}

	fmt.Printf("  creating volume %s in region %s (1GB)\n",
		colorCyan(volName), colorCyan(region))
	cr := exec.Command("fly", "volumes", "create", volName,
		"-a", appName, "-r", region, "--size", "1", "--yes")
	cr.Stdout = os.Stdout
	cr.Stderr = os.Stderr
	cr.Stdin = os.Stdin
	if err := cr.Run(); err != nil {
		return fmt.Errorf("fly volumes create: %v", err)
	}
	return nil
}

// manifestSessionSecretVar returns the env-var name behind
// auth.sessionSecret in mar.json when it\'s declared as env:VAR_NAME
// (the only valid shape for prod). When auth isn\'t configured /
// the value is a literal / not parseable, returns "".
//
// Used by promptAndSetFlySecrets to tag that var as the
// "session secret" in its prompt (suggests generating a random
// value automatically instead of typing one).
func manifestSessionSecretVar(m *project.Manifest) string {
	if m == nil || m.Auth == nil || m.Auth.SessionSecret == "" {
		return ""
	}
	const prefix = "env:"
	v := m.Auth.SessionSecret
	if len(v) > len(prefix) && v[:len(prefix)] == prefix {
		return v[len(prefix):]
	}
	return ""
}

// isInteractive reports whether the operator is at a real terminal
// (no CI=true, stdin is a tty). Used to gate prompts: in CI we never
// prompt — missing config aborts the deploy with a clear error so
// the pipeline fails loud instead of hanging on stdin.
//
// Mirrors shouldOpenBrowser's logic but for prompts. Both look at
// the same CI env var; centralized as a separate helper so the
// intent reads clearly at each call site.
func isInteractive() bool {
	if os.Getenv("CI") != "" {
		return false
	}
	return stdinIsTerminal()
}
