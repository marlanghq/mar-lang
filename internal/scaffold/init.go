// Package scaffold creates new mar project layouts. `mar init <name>`
// asks for a project kind (fullstack, frontend-only, or backend-only)
// and drops a working scaffold into <name>/ so the operator can
// `cd <name> && mar dev` immediately and see something running.
package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
)

// Kind selects which scaffold to generate. Each kind ships a
// different file set under <path>/:
//
//   - KindFullstack — Main + Shared + Backend + Frontend with a
//     persisted entity, two services, and a two-page frontend.
//   - KindFullstackAuth — same layout as KindFullstack but with
//     passwordless email-code auth wired through Auth.config and
//     Auth.protect; the frontend is a single page that walks the
//     user through sign-in before showing per-user entries.
//   - KindFrontend — single Main.mar with two pages, no backend.
//   - KindBackend — single Main.mar with an entity and two services
//     exposed via App.backend (no UI).
//   - KindMinimum — single Main.mar with an empty page wired through
//     App.fullstack. The smallest possible mar app — no UI, no model
//     state, no services — meant as a blank slate to build on.
type Kind string

const (
	KindFullstack     Kind = "fullstack"
	KindFullstackAuth Kind = "fullstack-auth"
	KindFrontend      Kind = "frontend"
	KindBackend       Kind = "backend"
	KindMinimum       Kind = "minimum"
)

// Description returns a one-line summary of what the scaffold builds.
// Printed by `mar init` right after the "Created <dir>" line so the
// operator gets a quick sense of what's in the project before running
// `mar dev`. Returns "" for an unknown kind so the caller can skip the
// blurb instead of printing a placeholder.
func Description(kind Kind) string {
	switch kind {
	case KindFullstack:
		return "A fullstack starter with a persisted list and two pages."
	case KindFullstackAuth:
		return "A fullstack starter with passwordless email auth and per-user entries."
	case KindFrontend:
		return "A frontend-only starter with two pages and no backend."
	case KindBackend:
		return "A backend-only API with one entity and two services."
	case KindMinimum:
		return "A blank starter: an empty page wired up, nothing more."
	default:
		return ""
	}
}

// Init creates a new project at <path>/ with the scaffold matching
// `kind`. Errors if the directory already exists or kind is invalid.
func Init(path string, kind Kind) error {
	if path == "" {
		return fmt.Errorf("project name is required")
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	// Use just the directory name (not the full path) for the manifest's
	// "name" field — that's what humans want to see when they edit it.
	name := filepath.Base(path)

	var files map[string]string
	switch kind {
	case KindFullstack:
		files = fullstackFiles(name)
	case KindFullstackAuth:
		files = fullstackAuthFiles(name)
	case KindFrontend:
		files = frontendFiles(name)
	case KindBackend:
		files = backendFiles(name)
	case KindMinimum:
		files = minimumFiles(name)
	default:
		return fmt.Errorf("unknown project kind %q", kind)
	}

	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	for relPath, content := range files {
		full := filepath.Join(path, relPath)
		// Templates can put files under subdirs (e.g. Frontend/Home.mar);
		// the directory has to exist before WriteFile.
		if dir := filepath.Dir(full); dir != path {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// sharedFiles returns the files every scaffold ships regardless of
// kind: .gitignore, .env (with comment), README.md. The caller
// merges these into the kind-specific file map.
func sharedFiles(name string) map[string]string {
	return map[string]string{
		".gitignore": `# Database files
*.db*
*.sqlite*

# Build artifacts
*.tar.gz
dist/

# Local secrets
.env

# AI assistants
.claude/
.codex/
.cursor/
`,
		".env": `# Local environment variables for this project. Gitignored so
# secrets stay on your machine. Anything already exported in your
# shell takes precedence.
#
# Used in two ways:
#  - CLI tokens (e.g. for ` + "`mar cloudflare-pages deploy`" + `). These
#    only exist on your machine, there's nowhere else for them.
#  - Runtime secrets in dev (e.g. SMTP password). In production
#    these come from the platform's secret store (Fly secrets,
#    systemd, etc.), not from .env.

# CLOUDFLARE_API_TOKEN=your-cloudflare-token
# CLOUDFLARE_ACCOUNT=your-cloudflare-account-id

# SMTP_PASSWORD=your-smtp-password
`,
		"README.md": fmt.Sprintf("# %s\n\nA [mar](https://mar-lang.dev) project. Get started:\n\n```bash\nmar dev\n```\n\nOpens http://localhost:3000.\n", name),
	}
}
