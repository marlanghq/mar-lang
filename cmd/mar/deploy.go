// `mar deploy`: the one deploy command. It reads the project's
// `mar.json` deploy block and hands off to the flow for whichever
// provider is configured.
//
// There's no target to choose: the app type determines it 1:1:
// App.fullstack ships to a Fly VM (deploy.fly), App.frontend ships to
// a Cloudflare Pages static bundle (deploy.cloudflare-pages). So a
// project carries exactly one deploy block and `mar deploy` just
// routes to it. The provider flow re-loads + validates the manifest
// and prints its own banner, so this router stays thin.
package main

import (
	"fmt"

	"mar/internal/project"
	"mar/internal/scaffold"
)

// runDeploy dispatches `mar deploy [path]` (with an optional
// --no-open, forwarded to the provider flow).
func runDeploy(args []string) int {
	noOpen, rest := extractNoOpenFlag(args)
	path := "."
	if len(rest) >= 1 {
		path = rest[0]
	}

	// Structure-only load: we just need to see which block is present,
	// not resolve env:VAR values (the provider flow does that). Returns
	// nil, nil when there's no mar.json.
	m, err := project.LoadManifestStructure(path)
	if err != nil {
		printManifestError("mar deploy", err)
		return 1
	}

	// No mar.json at all is a different mistake than a manifest missing
	// its deploy block: the operator is almost certainly in the wrong
	// directory. Say that, instead of coaching them to add a deploy
	// block to a file that doesn't exist.
	if m == nil {
		where := colorMagenta(path)
		if path == "." {
			where = "the current directory"
		}
		fprintError("mar deploy: no %s found in %s.",
			colorMagenta("mar.json"), where)
		fprintHint("This doesn't look like a Mar project. Run %s from the project root (the folder that holds %s), or start a new project with %s.",
			colorBold("mar deploy"), colorMagenta("mar.json"), colorBold("mar init"))
		return 2
	}

	hasFly, hasCF := false, false
	if m.Deploy != nil {
		hasFly = m.Deploy.Fly != nil
		hasCF = m.Deploy.CloudflarePages != nil
	}

	switch {
	case hasFly && hasCF:
		fprintError("mar deploy: mar.json declares BOTH %s and %s.",
			colorMagenta("deploy.fly"), colorMagenta("deploy.cloudflare-pages"))
		fprintHint("An app deploys to one target — keep the block that matches its type:\n"+
			"      %s → %s,  %s → %s.",
			colorBold("App.fullstack"), colorMagenta("deploy.fly"),
			colorBold("App.frontend"), colorMagenta("deploy.cloudflare-pages"))
		return 2
	case hasFly:
		return runFlyDeploy(path, noOpen)
	case hasCF:
		return runCloudflarePagesDeploy(path, noOpen)
	default:
		fprintError("mar deploy: %s has no %s block.",
			colorMagenta("mar.json"), colorMagenta("deploy"))
		printMissingDeployHint(path)
		return 2
	}
}

// printMissingDeployHint shows how to add the deploy block. It inspects
// the topology (which runs main) so the operator sees only the block
// their app needs; missingDeployHint builds the tailored text.
func printMissingDeployHint(path string) {
	topo, err := scaffold.Topology(path)
	fprintHint("%s", missingDeployHint(topo, err))
}

// missingDeployHint returns the "add a deploy block" hint body, tailored
// to the app's actual shape: a frontend app deploys to Cloudflare Pages,
// a fullstack or backend app to a Fly VM: each with a ready-to-paste
// example, not a two-option menu to decode. A non-nil topoErr means the
// shape couldn't be determined (main doesn't compile yet, or the path has
// no app), so we fall back to listing both targets. Split from printing
// so it's unit-testable.
func missingDeployHint(topo string, topoErr error) string {
	switch {
	case topoErr != nil:
		// Pad the shorter label so the two descriptions line up (colorMagenta
		// wraps in zero-width ANSI, so padding the plain text keeps them even).
		return fmt.Sprintf("Add the block that matches your app:\n"+
			"      %s  a Fly VM with SQLite + auth (fullstack / backend apps)\n"+
			"      %s  a Cloudflare Pages static bundle (frontend apps)",
			colorMagenta(fmt.Sprintf("%-23s", "deploy.fly")),
			colorMagenta("deploy.cloudflare-pages"))
	case topo == "frontend":
		return fmt.Sprintf("This is a frontend app, so it deploys to Cloudflare Pages. Add a %s block to %s:\n\n%s",
			colorMagenta("deploy.cloudflare-pages"), colorMagenta("mar.json"), cfPagesDeployExample())
	default: // fullstack or backend → a Fly VM (the self-contained binary + SQLite)
		return fmt.Sprintf("This is a %s app, so it deploys to a Fly VM. Add a %s block to %s:\n\n%s",
			topo, colorMagenta("deploy.fly"), colorMagenta("mar.json"), flyDeployExample())
	}
}

// Ready-to-paste deploy blocks. Fields match what ValidateDeployFly /
// ValidateDeployCloudflarePages require (app/region/memory for Fly;
// app/account/apiToken for Pages). The `<...>` fields are fill-in-me
// placeholders, rendered cyan (the CLI's identifier color, cli-style.md
// §3) so the eye separates them from the literal defaults you can keep
// as-is (`256mb`, the `env:` refs). Built at call time so the color
// state is already resolved.
func flyDeployExample() string {
	return fmt.Sprintf(`      "deploy": {
        "fly": {
          "app": "%s",
          "region": "%s",
          "memory": "256mb"
        }
      }`, colorCyan("<your-app-name>"), colorCyan("<your-region>"))
}

func cfPagesDeployExample() string {
	return fmt.Sprintf(`      "deploy": {
        "cloudflare-pages": {
          "app": "%s",
          "account": "env:CLOUDFLARE_ACCOUNT_ID",
          "apiToken": "env:CLOUDFLARE_API_TOKEN"
        }
      }`, colorCyan("<your-app-name>"))
}
