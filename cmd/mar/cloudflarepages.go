// `mar cloudflare-pages` subcommands.
//
// Cloudflare Pages is the static-host counterpart to `mar fly deploy`:
// where Fly hosts an App.fullstack VM with SQLite + auth + backend,
// CF Pages hosts an App.frontend bundle on a global CDN. The two are
// complementary, not interchangeable — a single Mar project can
// declare both deploy.fly and deploy.cloudflare-pages blocks, and
// the operator picks which command to run based on the topology
// they want to ship.
//
// Why a native Go implementation (vs. shelling out to wrangler):
// CF's Direct Upload API is documented, stable, and HTTP-only.
// Wrangler is a Node CLI requiring `npm install -g wrangler` (plus
// a working Node toolchain), which would undercut Mar's "single
// binary, no toolchain" promise. The native implementation lives
// entirely in cmd/mar/cloudflarepages_api.go and cmd/mar/
// cloudflarepages_deploy.go.
//
// Phase 1 (this file) covers just `deploy`. Follow-on commands —
// `preview`, `secrets`, `destroy`, `logs`, `list`, `rollback` —
// would mirror their Fly counterparts when needed.

package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"mar/internal/project"
	"mar/internal/scaffold"
)

// runCloudflarePages dispatches `mar cloudflare-pages <sub> [path]`.
// Mirrors runFly's shape. The hyphen in the top-level command name
// is deliberate — see docs/cli-surface-proposal.md (and the design
// discussion that led to this naming): "cloudflare" alone is
// ambiguous (Cloudflare has many products), "cf" abbreviates too
// aggressively, and `cloudflare-pages` reads as one identifier for
// one product.
func runCloudflarePages(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, cloudflarePagesUsage())
		fmt.Fprintln(os.Stderr)
		return 2
	}
	sub := args[0]
	// Strip --no-open from positional args before path parsing so
	// the flag can appear before or after the path (matches the
	// shape `mar fly deploy` already established).
	noOpen, subArgs := extractNoOpenFlag(args[1:])
	path := "."
	if len(subArgs) >= 1 {
		path = subArgs[0]
	}
	switch sub {
	case "deploy":
		return runCloudflarePagesDeploy(path, noOpen)
	default:
		fprintError("mar cloudflare-pages: unknown subcommand %q", sub)
		fmt.Fprintln(os.Stderr, cloudflarePagesUsage())
		fmt.Fprintln(os.Stderr)
		return 2
	}
}

// cloudflarePagesUsage returns the help text for `mar cloudflare-pages`.
// Same palette as flyUsage (green binary, bold subcommands, magenta
// paths/keys, cyan URLs).
func cloudflarePagesUsage() string {
	bin := colorGreen("mar")
	name := func(s string) string { return colorBold(s) }
	pth := func(s string) string { return colorMagenta(s) }
	url := func(s string) string { return colorCyan(s) }
	hdr := func(s string) string { return colorBold(s) }
	run := func(rest string) string { return bin + " " + name(rest) }

	return "Usage: " + run("cloudflare-pages") + " " + name("<command> [path]") + "\n" +
		"\n" +
		hdr("Commands:") + "\n" +
		"  " + name("deploy") + "     Build the static bundle and push it to Cloudflare\n" +
		"             Pages. Auto-creates the Pages project on the first\n" +
		"             deploy (with confirmation).\n" +
		"\n" +
		hdr("Configuration:") + "\n" +
		"  Reads " + pth("mar.json") + "'s " + pth("deploy.cloudflare-pages") + " block:\n" +
		"\n" +
		"    " + pth(`"deploy": { "cloudflare-pages": {`) + "\n" +
		"    " + pth(`  "app": "...", "account": "...",`) + "\n" +
		"    " + pth(`  "apiToken": "env:CF_API_TOKEN"`) + "\n" +
		"    " + pth(`} }`) + "\n" +
		"\n" +
		hdr("Authentication:") + "\n" +
		"  The API token is declared in " + pth("mar.json") + " as " + pth("env:VAR_NAME") + " —\n" +
		"  the operator picks the env var name. Create the token at\n" +
		"  " + url("https://dash.cloudflare.com/profile/api-tokens") + " with the\n" +
		"  " + name("Account.Cloudflare Pages: Edit") + " permission."
}

// cloudflarePagesTarget bundles the resolved deploy fields. Returned
// by resolveCloudflarePagesProject so callers don't have to thread
// account/app/apiToken/projectDir/manifest through their own
// signatures.
//
// String fields (Account, App, APIToken) are the POST-env:
// resolved values. The operator may have written
// "env:CF_API_TOKEN" in mar.json; what arrives here is the actual
// token bytes.
//
// APITokenEnvVar is the special one: it carries the LITERAL env
// var name (e.g. "CF_API_TOKEN") that apiToken was bound to in
// the manifest. Captured BEFORE env resolution so error messages
// can name the var explicitly ("export CF_API_TOKEN=..." rather
// than the generic "export the env var your apiToken points to").
// Empty when apiToken is a literal (which checkSecrets rejects,
// but the field exists for completeness).
type cloudflarePagesTarget struct {
	Account        string
	App            string
	APIToken       string
	APITokenEnvVar string
	ProjectDir     string
	Manifest       *project.Manifest
}

// resolveCloudflarePagesProject is the cloudflare-pages analog of
// resolveFlyApp. Single source of truth for "which CF Pages project
// does this command operate on": every subcommand calls this, gets
// back the resolved target, and proceeds from there.
//
// Uses LoadManifest (strict env resolution) so any env:VAR in the
// deploy block is resolved before validation. Returns specific
// error types the caller can render with structure:
//
//   - *project.DeployCloudflarePagesError       — schema validation
//   - *project.EnvVarNotSetError (wrapped)      — env var missing
//   - other errors                              — malformed mar.json, IO, etc.
func resolveCloudflarePagesProject(path string) (*cloudflarePagesTarget, error) {
	projectDir, m, apiTokenEnvVar, err := loadCloudflarePagesManifest(path)
	if err != nil {
		return nil, err
	}
	if verr := project.ValidateDeployCloudflarePages(m); verr != nil {
		return nil, verr
	}
	c := m.Deploy.CloudflarePages
	return &cloudflarePagesTarget{
		Account:        c.Account,
		App:            c.App,
		APIToken:       c.APIToken,
		APITokenEnvVar: apiTokenEnvVar,
		ProjectDir:     projectDir,
		Manifest:       m,
	}, nil
}

// loadCloudflarePagesManifest resolves projectDir from `path` (file
// or directory) and loads mar.json WITH env:VAR resolution.
//
// LoadManifest (not LoadManifestStructure) is the right call for the
// returned manifest because deploy.cloudflare-pages.apiToken MUST be
// env:VAR — and validation downstream sees the resolved value.
//
// Returns apiTokenEnvVar separately: the literal env var name (e.g.
// "CF_API_TOKEN") that apiToken was bound to before resolution. We
// capture this from a parallel LoadManifestStructure pass because
// the resolving load CLOBBERS the literal — by the time it returns,
// apiToken holds the actual token bytes and the original env:VAR
// reference is gone. Error messages need the var name to be
// concrete ("export CF_API_TOKEN=..." instead of a placeholder).
func loadCloudflarePagesManifest(path string) (projectDir string, m *project.Manifest, apiTokenEnvVar string, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", nil, "", err
	}
	if info.IsDir() {
		projectDir = path
	} else {
		projectDir = pathDir(path)
	}

	// First: structural pass to capture the literal env:VAR
	// reference for apiToken before resolution overwrites it.
	// Best-effort — if this fails, the resolving load below will
	// surface the real error; we just lose the env var name for
	// error messages.
	if structural, _ := project.LoadManifestStructure(projectDir); structural != nil &&
		structural.Deploy != nil &&
		structural.Deploy.CloudflarePages != nil {
		literal := structural.Deploy.CloudflarePages.APIToken
		if strings.HasPrefix(literal, "env:") {
			apiTokenEnvVar = strings.TrimPrefix(literal, "env:")
		}
	}

	m, err = project.LoadManifest(projectDir)
	if err != nil {
		return "", nil, "", err
	}
	if m == nil {
		return "", nil, "", fmt.Errorf("%s/mar.json not found", projectDir)
	}
	return projectDir, m, apiTokenEnvVar, nil
}

// pathDir is a tiny wrapper around filepath.Dir to keep this file
// from importing path/filepath just for one call. The fly path
// imports it for several reasons; we don't need to.
func pathDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i]
		}
	}
	return "."
}

// printDeployCloudflarePagesError renders a *project.DeployCloudflarePagesError
// with full structured guidance (the JSON snippet, hint block, link
// to the API token page). Non-CF errors fall through to printError.
//
// Color palette per docs/cli-style.md:
//   - magenta : paths, config keys, field names
//   - cyan    : values the operator types (account IDs, project names)
//   - bold    : section titles
//   - red     : Error: prefix
func printDeployCloudflarePagesError(err error) {
	de, ok := err.(*project.DeployCloudflarePagesError)
	if !ok {
		printError("mar cloudflare-pages deploy", err)
		return
	}
	switch de.Kind {
	case "missing-block":
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "%s mar cloudflare-pages deploy: %s has no %s block.\n",
			colorRed("Error:"), colorMagenta("mar.json"), colorMagenta("deploy.cloudflare-pages"))
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "%s %s:\n", colorBold("Add this to"), colorMagenta("mar.json"))
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "  %s: {\n", colorMagenta(`"deploy"`))
		fmt.Fprintf(os.Stderr, "    %s: {\n", colorMagenta(`"cloudflare-pages"`))
		fmt.Fprintf(os.Stderr, "      %s:      %s,\n",
			colorMagenta(`"app"`), colorCyan(`"<pages-project-name>"`))
		fmt.Fprintf(os.Stderr, "      %s:  %s,\n",
			colorMagenta(`"account"`), colorCyan(`"<32-char-account-id>"`))
		fmt.Fprintf(os.Stderr, "      %s: %s\n",
			colorMagenta(`"apiToken"`), colorCyan(`"env:CF_API_TOKEN"`))
		fmt.Fprintln(os.Stderr, "    }")
		fmt.Fprintln(os.Stderr, "  }")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, colorBold("Hints:"))
		fmt.Fprintf(os.Stderr, "  %s      — Pages project name; becomes %s. Auto-created on\n",
			colorMagenta("app"), colorCyan("<app>.pages.dev"))
		fmt.Fprintf(os.Stderr, "             first deploy (with confirmation).\n")
		fmt.Fprintf(os.Stderr, "  %s  — your Cloudflare account ID (32 hex chars; visible in\n",
			colorMagenta("account"))
		fmt.Fprintf(os.Stderr, "             the dashboard URL or via the API).\n")
		fmt.Fprintf(os.Stderr, "  %s — API token with %s permission.\n",
			colorMagenta("apiToken"), colorBold("Account.Cloudflare Pages: Edit"))
		fmt.Fprintf(os.Stderr, "             Must be %s — committing a literal would leak\n",
			colorMagenta("env:VAR_NAME"))
		fmt.Fprintf(os.Stderr, "             the credential. Create one at\n")
		fmt.Fprintf(os.Stderr, "             %s.\n",
			colorCyan("https://dash.cloudflare.com/profile/api-tokens"))
		fmt.Fprintln(os.Stderr)
	case "missing-app":
		fprintError("mar cloudflare-pages deploy: %s is missing %s.",
			colorMagenta("mar.json"), colorMagenta("deploy.cloudflare-pages.app"))
		fprintHint("%s is the Pages project name; becomes %s.\n"+
			"      It will be auto-created on the first deploy.",
			colorMagenta("app"),
			colorCyan("<app>.pages.dev"))
	case "invalid-app":
		fprintError("mar cloudflare-pages deploy: %s = %q is not a valid Pages\n      project name.",
			colorMagenta("deploy.cloudflare-pages.app"), de.BadValue)
		fprintHint("Pages project names use lowercase letters, digits, and hyphens\n" +
			"      only (1–58 chars; cannot start or end with a hyphen).")
	case "missing-account":
		fprintError("mar cloudflare-pages deploy: %s is missing %s.",
			colorMagenta("mar.json"), colorMagenta("deploy.cloudflare-pages.account"))
		fprintHint("%s is your Cloudflare account ID (32 hex chars; visible in the\n"+
			"      dashboard URL right after %s, or via the API).",
			colorMagenta("account"),
			colorCyan("dash.cloudflare.com/"))
	case "invalid-account":
		fprintError("mar cloudflare-pages deploy: %s = %q is not a valid Cloudflare\n      account ID (expected 32 hex characters).",
			colorMagenta("deploy.cloudflare-pages.account"), de.BadValue)
		fprintHint("Account IDs are 32 lowercase-hex chars. Find yours at\n"+
			"      %s — the slug after %s in the URL\n"+
			"      is the account ID.",
			colorCyan("https://dash.cloudflare.com/"),
			colorCyan("dash.cloudflare.com/"))
	case "missing-api-token":
		fprintError("mar cloudflare-pages deploy: %s is missing %s.",
			colorMagenta("mar.json"), colorMagenta("deploy.cloudflare-pages.apiToken"))
		fprintHint("%s is the Cloudflare API token used for uploads. Required\n"+
			"      permission: %s.\n"+
			"\n"+
			"      Declare it as %s in %s — the operator picks the\n"+
			"      env var name. Example:\n"+
			"\n"+
			"        %s: %s\n"+
			"\n"+
			"      Create the token at %s.",
			colorMagenta("apiToken"),
			colorBold("Account.Cloudflare Pages: Edit"),
			colorMagenta("env:VAR_NAME"),
			colorMagenta("mar.json"),
			colorMagenta(`"apiToken"`),
			colorCyan(`"env:CF_API_TOKEN"`),
			colorCyan("https://dash.cloudflare.com/profile/api-tokens"))
	default:
		printError("mar cloudflare-pages deploy", err)
	}
}

// printCloudflarePagesDeployError is the catch-all error renderer
// for runtime failures during `mar cloudflare-pages deploy`. It
// recognizes structured *cfAPIError values and adds Hint blocks
// for known failure modes (auth, rate limit, etc.). Anything it
// doesn't recognize falls through to the plain printError.
//
// `target` carries deploy context (env var names, app/account
// identifiers) so error hints can be specific instead of generic.
// Pass nil if the error fires before the target is resolved —
// the renderer degrades gracefully (omits the contextual bits).
//
// Each branch maps a CF error code (defined in
// cloudflarepages_api.go) to an actionable recovery path. Keep
// the mapping narrow: only codes where we genuinely have advice
// to give. Generic 5xx / network failures fall through; nothing
// useful to add beyond "try again later".
func printCloudflarePagesDeployError(target *cloudflarePagesTarget, err error) {
	var cfErr *cfAPIError
	if errors.As(err, &cfErr) {
		switch {
		case cfErr.HasCode(cfErrCodeAuthFailed) || cfErr.HasCode(cfErrCodeAuthError):
			printCloudflareAuthError(target)
			return
		}
	}
	printError("mar cloudflare-pages deploy", err)
}

// printCloudflareAuthError renders the structured Hint for "the API
// token Cloudflare received is bad" (codes 9106 / 10000). Covers
// every common cause: wrong/typoed value, missing permission,
// expired/revoked token.
//
// When target is non-nil and APITokenEnvVar is set, the hint names
// the specific env var the operator should re-check ("export
// CF_API_TOKEN=..."). Otherwise it falls back to a generic phrasing.
func printCloudflareAuthError(target *cloudflarePagesTarget) {
	fprintError("mar cloudflare-pages deploy: Cloudflare rejected the API token.")

	// "Double-check the env var" step: name the var explicitly
	// when we know it. With the var name, the operator can
	// `echo $CF_API_TOKEN` immediately; without it, they'd have
	// to look up mar.json first.
	envCheckLine := fmt.Sprintf(
		"1. Double-check the value of the env var your %s points to.\n"+
			"           Tokens are long random strings, and partial copies or\n"+
			"           trailing whitespace will both fail.",
		colorMagenta("apiToken"))
	if target != nil && target.APITokenEnvVar != "" {
		envCheckLine = fmt.Sprintf(
			"1. Double-check the value of %s. Tokens are long\n"+
				"           random strings, and partial copies or trailing\n"+
				"           whitespace will both fail. Verify with:\n"+
				"\n"+
				"               %s",
			colorMagenta(target.APITokenEnvVar),
			colorGreen("echo")+" "+colorBold("$"+target.APITokenEnvVar))
	}

	fprintHint(
		"The token Cloudflare received is invalid, expired, revoked, or\n"+
			"      missing the required permission. Common fixes:\n"+
			"\n"+
			"        %s\n"+
			"\n"+
			"        2. Confirm the token has the %s\n"+
			"           permission. Without it, every API call from this\n"+
			"           tool will be rejected.\n"+
			"\n"+
			"        3. If the token is old, it may have been revoked. Create\n"+
			"           a fresh one and re-export it.\n"+
			"\n"+
			"      Manage your tokens at:\n"+
			"      %s",
		envCheckLine,
		colorBold("Account.Cloudflare Pages: Edit"),
		colorCyan("https://dash.cloudflare.com/profile/api-tokens"))
}

// requireFrontendTopology errors out when the project isn't
// App.frontend. CF Pages can only host static bundles; running
// `mar cloudflare-pages deploy` on a fullstack project is a
// configuration mistake the user wants to catch early, not a
// 5-minute partial deploy followed by "your SQLite died because
// there's no filesystem on CF".
//
// The inverse of the warning printDeployFlyError shows for
// frontend-only projects deployed to Fly — same idea, different
// direction.
func requireFrontendTopology(projectDir string) error {
	topo, err := scaffold.Topology(projectDir)
	if err != nil {
		return err
	}
	if flyTopology(topo) == flyTopologyFrontend {
		return nil
	}
	return &topologyMismatchError{got: flyTopology(topo)}
}

// topologyMismatchError carries the not-frontend-only case so the
// caller can render with a structured hint pointing at mar fly
// deploy. Implements error so it composes with printError.
type topologyMismatchError struct {
	got flyTopology
}

func (e *topologyMismatchError) Error() string {
	return fmt.Sprintf("project topology is %s; cloudflare-pages only deploys frontend-only projects", e.got)
}

// printTopologyMismatch is the structured rendering for a
// topologyMismatchError. Hint points at mar fly deploy as the
// right command for the user's actual topology.
func printTopologyMismatch(e *topologyMismatchError) {
	fprintError("mar cloudflare-pages deploy: this project is %s, not frontend-only.",
		colorCyan(string(e.got)))
	fprintHint("Cloudflare Pages only hosts static bundles. For projects with\n"+
		"      a backend (database, services, auth), use %s instead\n"+
		"      — it provisions a VM with SQLite, secrets, and the runtime\n"+
		"      that fullstack apps need.",
		cmdSuggest("fly deploy"))
}
