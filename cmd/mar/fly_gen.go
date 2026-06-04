// Generators for the Fly deploy artifacts. These live entirely in Go
// strings — there are no template files on disk for the operator to
// edit. Docker is treated as an implementation detail of `mar fly`;
// every deploy regenerates the Dockerfile + fly.toml from scratch
// against mar.json + the current project topology, then deploys, then
// deletes the temp dir. The operator never sees a Dockerfile, never
// edits one, never has stale templates in their repo to debug.
//
// This file produces two artifacts:
//
//   - Dockerfile — one of two shapes, picked from topology:
//     * frontend  → Caddy serving the static bundle
//     * backend / fullstack → debian + the prebuilt mar binary
//
//   - fly.toml — driven by manifest.Deploy.Fly + topology:
//     * volume mount only for backend/fullstack (SQLite persistence)
//     * internal_port from manifest.Server.Port (default 3000) for
//       backend/fullstack, 80 for frontend (Caddy default)

package main

import (
	"fmt"
	"strings"

	"mar/internal/project"
)

// flyTopology mirrors scaffold.buildCtx.kind without re-exporting the
// unexported scaffold types. Callers detect topology via
// scaffold.Topology (which runs main and inspects which
// App.frontend/backend/fullstack was called) and pass the result here
// as a string. Tiny coupling surface — only the topology matters for
// generation, not the rest of buildCtx.
type flyTopology string

const (
	flyTopologyFrontend  flyTopology = "frontend"
	flyTopologyBackend   flyTopology = "backend"
	flyTopologyFullstack flyTopology = "fullstack"
)

// generateDockerfile returns the Dockerfile bytes for the given
// topology. Frontend uses Caddy (static file server); backend /
// fullstack uses debian + the prebuilt mar binary (which the deploy
// flow writes into the same dir at `dist/<binaryName>`).
//
// The Dockerfile is regenerated every deploy — no hand-edits to
// preserve, no drift to worry about. The body is laid out as a
// plain string for readability; %s substitutions are limited to
// the few values that vary (binary name, port).
func generateDockerfile(topo flyTopology, binaryName string, port int) string {
	switch topo {
	case flyTopologyFrontend:
		// Caddy default config serves /usr/share/caddy/ on :80.
		// The deploy flow drops the static bundle (index.html,
		// runtime.js, program.json) into `dist/` next to this
		// Dockerfile; the COPY then lands them at Caddy\'s root.
		return `FROM caddy:2-alpine
COPY dist/ /usr/share/caddy/
EXPOSE 80
`
	case flyTopologyBackend, flyTopologyFullstack:
		// Debian slim + the self-contained mar binary (which
		// embeds mar.json + all .mar source files + the SQLite
		// driver; statically linked). ca-certificates is the
		// only runtime dep — the binary needs it to validate
		// outbound TLS (SMTP, webhooks, third-party APIs).
		return fmt.Sprintf(`FROM debian:bookworm-slim

RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY dist/%s /app/%s

EXPOSE %d
CMD ["/app/%s"]
`, binaryName, binaryName, port, binaryName)
	default:
		// Defensive — unknown topology is a bug in the caller,
		// not a user error.
		return ""
	}
}

// generateFlyToml returns the fly.toml bytes. Driven by:
//
//   - manifest.Deploy.Fly: app, region, memory
//   - topology: gates the volume mount + picks the right internal port
//   - manifest.Server.Port: backend/fullstack internal_port (default 3000)
//
// Frontend topology gets no volume, no MAR_DATABASE_PATH env, and
// port 80 (Caddy\'s default). Backend / fullstack get the full
// SQLite-persistence shape.
//
// The volume name derives from the app name with dashes → underscores
// (Fly requirement: volume names must match [a-zA-Z0-9_]). This is
// stable / deterministic so re-deploys hit the same volume.
func generateFlyToml(m *project.Manifest, topo flyTopology) string {
	fly := m.Deploy.Fly
	var b strings.Builder

	fmt.Fprintf(&b, "app = %q\n", fly.App)
	fmt.Fprintf(&b, "primary_region = %q\n", fly.Region)
	b.WriteString("\n")
	b.WriteString("[build]\n")
	b.WriteString("  dockerfile = \"Dockerfile\"\n")
	b.WriteString("\n")

	switch topo {
	case flyTopologyFrontend:
		b.WriteString("[http_service]\n")
		b.WriteString("  internal_port = 80\n")
	case flyTopologyBackend, flyTopologyFullstack:
		volumeName := flyVolumeName(fly.App)
		port := 3000
		if m.Server != nil && m.Server.Port != 0 {
			port = m.Server.Port
		}
		// MAR_DATABASE_PATH redirects SQLite onto the mounted
		// volume. Without this, the runtime defaults to
		// `<name>.db` next to mar.json — which inside the
		// container lands on ephemeral storage that\'s wiped on
		// every restart.
		b.WriteString("[env]\n")
		fmt.Fprintf(&b, "  MAR_DATABASE_PATH = \"/data/%s.db\"\n", fly.App)
		b.WriteString("\n")
		b.WriteString("[mounts]\n")
		fmt.Fprintf(&b, "  source = %q\n", volumeName)
		b.WriteString("  destination = \"/data\"\n")
		b.WriteString("\n")
		b.WriteString("[http_service]\n")
		fmt.Fprintf(&b, "  internal_port = %d\n", port)
	}

	// Common service flags — terminate TLS at Fly\'s edge (free
	// certs), auto-stop the machine when idle (cuts the bill to
	// near-zero on low-traffic apps), auto-start on the first
	// request after stop.
	b.WriteString("  force_https = true\n")
	b.WriteString("  auto_stop_machines = \"stop\"\n")
	b.WriteString("  auto_start_machines = true\n")
	b.WriteString("  min_machines_running = 0\n")
	b.WriteString("\n")

	// VM size. shared-cpu-1x for everyone — promoted to dedicated
	// CPU is an opt-in we don\'t expose yet (not a knob in
	// deploy.fly because the cost jump is large and the use case
	// rare; if it ever matters we add it then).
	b.WriteString("[[vm]]\n")
	b.WriteString("  size = \"shared-cpu-1x\"\n")
	fmt.Fprintf(&b, "  memory = %q\n", fly.Memory)

	return b.String()
}
