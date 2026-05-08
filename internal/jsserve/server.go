package jsserve

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"strings"

	esbuild "github.com/evanw/esbuild/pkg/api"

	"mar/internal/runtime"
)

//go:embed runtime.js
var runtimeJS string

// RuntimeJSProduction returns the runtime processed for static builds:
//   - Flips the build-time flag to `__MAR_DEV__ = false`, so esbuild's
//     constant folding + DCE drops the time-travel panel, dev dock,
//     SSE reload channel, error overlay, and the `displayValue`
//     formatter (all gated behind `if (__MAR_DEV__)` blocks).
//   - Minifies whitespace, identifiers, and syntax.
//
// Used by `mar build` when writing runtime.js into a static dist/.
func RuntimeJSProduction() (string, error) {
	src := strings.Replace(
		runtimeJS,
		"const __MAR_DEV__ = true;",
		"const __MAR_DEV__ = false;",
		1,
	)
	result := esbuild.Transform(src, esbuild.TransformOptions{
		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		Target:            esbuild.ES2020,
		Loader:            esbuild.LoaderJS,
	})
	if len(result.Errors) > 0 {
		return "", fmt.Errorf("esbuild: %s", result.Errors[0].Text)
	}
	return string(result.Code), nil
}

// serveProgramJSON writes the current program AST. Honors If-None-Match
// against an ETag derived from the content hash so iOS / API clients
// can skip re-downloading unchanged programs (the OTA case).
//
// Cache-Control stays `no-store` here to be safe across dev (where
// the program changes per file save) and prod (where the deployer
// can layer their own caching at the CDN if they want). ETag handles
// the "did it change?" question regardless.
func serveProgramJSON(lp *LiveProgram) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := lp.ProgramJSON()
		etag := `"` + programETag(body) + `"`
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write(body)
	}
}

// renderShell writes the HTML page with the current program.json
// embedded inline as <script type="application/json" id="mar-program">.
//
// Cache-Control: no-store — the embedded program changes per deploy
// (and per file save in dev). Without no-store, browsers could serve
// a cached HTML pointing at a stale program. The SSE reload channel
// covers in-tab updates; this header covers cross-tab / cold loads.
//
// </script> inside the JSON would prematurely close the script tag.
// Escape the `<` as `<` (or replace `</` with `<\/`); JSON.parse
// reads either back identically. We use the latter — same trick the
// static buildIndexHTML uses.
func renderShell(w http.ResponseWriter, lp *LiveProgram) {
	body := lp.ProgramJSON()
	safeProgram := strings.ReplaceAll(string(body), "</", `<\/`)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprintf(w, pageHTML, lp.Title(), safeProgram)
}

// programETag returns the strong ETag value for a program.json payload.
// First 16 hex chars of the sha256 — plenty of collision space for a
// single app's deploy history. Cached on the LiveProgram itself
// (computed once per program swap, not per request).
func programETag(body []byte) string {
	sum := sha256.Sum256(body)
	const hex = "0123456789abcdef"
	out := make([]byte, 16)
	for i := 0; i < 8; i++ {
		out[i*2] = hex[sum[i]>>4]
		out[i*2+1] = hex[sum[i]&0x0f]
	}
	return string(out)
}

// HTML page template. Loads the runtime, then the AST, then runs `main`.
// Asset paths are stable (`/_mar/...`) so hot-reload's SSE channel sits
// alongside them without colliding with user routes.
//
// Two `%s` substitutions:
//   1. <title> — the app's name
//   2. inline program.json — embedded as a <script type="application/json">
//      tag so the runtime can boot without a second round-trip. Same
//      trick the static `mar build` (App.frontend) uses; we did NOT
//      apply it to mar dev / mar fly's HTML response originally because
//      hot-reload made staleness a concern, but the SSE channel
//      already forces window.location.reload() on each program change,
//      so the embedded copy is always fresh by the time the browser
//      re-fetches the HTML.
//
// Cold-start round-trips (web): 3 → 1 (HTML embeds program; runtime.js
// is the only follow-up fetch). Same shape as iOS Approach C.
const pageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
  /* Reasonable defaults so the bare view DSL renders looking like a
     real app, not raw browser stock. The DSL itself stays sparse —
     these styles target the HTML tags the runtime emits. */

  *, *::before, *::after { box-sizing: border-box; }

  :root {
    --fg: #1a1a1a;
    --fg-muted: #666;
    --bg: #fafafa;
    --surface: #fff;
    --border: #e2e2e2;
    --accent: #2563eb;
    --accent-fg: #fff;
    --radius: 6px;
    --gap: 0.5rem;
  }

  html, body { margin: 0; padding: 0; }
  body {
    /* Neutral baseline — full viewport, top-left, default sans. The
       UI vocabulary's CSS in ensureUIStyles() (runtime.js) layers on
       top with the SwiftUI-flavored Form/List look. */
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
    font-size: 15px;
    line-height: 1.4;
    color: var(--fg);
    background: var(--bg);
    padding: 1.5rem;
  }

  /* Typography (UI.title / .subtitle / .text) */
  h1 { font-size: 1.75rem; font-weight: 700; margin: 0 0 0.5rem; }
  h2 { font-size: 1.1rem; font-weight: 600; margin: 1rem 0 0.4rem; color: var(--fg); }
  span { display: inline; }

  /* Buttons (UI.button) */
  button {
    appearance: none;
    border: 1px solid var(--border);
    background: var(--surface);
    color: var(--fg);
    padding: 0.4rem 0.9rem;
    border-radius: var(--radius);
    font: inherit;
    cursor: pointer;
    transition: background 0.1s ease, border-color 0.1s ease;
  }
  button:hover { background: #f0f0f0; }
  button:active { background: #e7e7e7; }
  button:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }

  /* Inputs (UI.textField). Cover every text-shaped input type
     (text, email, password, search, tel, url, number) so
     UI.email / UI.password / UI.numeric stay visually consistent
     with default text inputs. */
  input:not([type="checkbox"]):not([type="radio"]):not([type="submit"]):not([type="button"]):not([type="file"]),
  textarea {
    border: 1px solid var(--border);
    background: var(--surface);
    color: var(--fg);
    padding: 0.45rem 0.6rem;
    border-radius: var(--radius);
    font: inherit;
    width: 100%%;
    max-width: 24rem;
  }
  input:not([type="checkbox"]):not([type="radio"]):not([type="submit"]):not([type="button"]):not([type="file"]):focus,
  textarea:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px rgba(37, 99, 235, 0.15);
  }
  textarea { min-height: 4.5rem; resize: vertical; }

  /* Links (UI.link) */
  a { color: var(--accent); text-decoration: none; }
  a:hover { text-decoration: underline; }

  /* Bare <ul> baseline — ensureUIStyles() in runtime.js overrides
     this for the UI.list element with .mar-list. Kept neutral here
     so any incidental <ul> the user emits doesn't carry browser
     bullets and indentation. */
  ul {
    list-style: none;
    padding: 0;
    margin: 0;
  }
  li { padding: 0.35rem 0; }
  li + li { border-top: 1px solid var(--border); }

  /* Bare <section> baseline — UI.section overrides via .mar-section. */
  section { padding: 1rem 0; }

  /* Mount point: a flex column that fills the viewport height (minus
     body padding). Lets UI.centered actually center a top-level view
     on the page — without it, the column would have no inherent
     height to center within. */
  #mar-root {
    display: flex;
    flex-direction: column;
    min-height: calc(100vh - 3rem);
  }
</style>
</head>
<body>
<div id="mar-root">
  <!-- Boot placeholder. The runtime's first render replaces #mar-root's
       children, so this disappears naturally once marRun mounts the
       user's page. Animated in after a 200ms delay so fast cold starts
       (sub-200ms total) never show it — the placeholder only appears
       when the user would otherwise be staring at a blank page. -->
  <div class="mar-boot-loading" aria-label="Loading">
    <div class="mar-boot-spinner" aria-hidden="true"></div>
    <div class="mar-boot-label">Loading…</div>
  </div>
</div>
<style>
  .mar-boot-loading {
    position: fixed; inset: 0;
    display: flex; flex-direction: column;
    align-items: center; justify-content: center;
    gap: 0.75rem;
    color: var(--fg-muted, #666);
    font-size: 14px;
    opacity: 0;
    animation: mar-boot-fade-in 0.3s ease-out 0.2s forwards;
    pointer-events: none;
  }
  .mar-boot-spinner {
    width: 28px; height: 28px;
    border: 2px solid var(--border, #e2e2e2);
    border-top-color: var(--accent, #2563eb);
    border-radius: 50%%;
    animation: mar-boot-spin 0.8s linear infinite;
  }
  @keyframes mar-boot-fade-in { to { opacity: 1; } }
  @keyframes mar-boot-spin { to { transform: rotate(360deg); } }
  @media (prefers-reduced-motion: reduce) {
    .mar-boot-spinner { animation: none; }
    .mar-boot-loading { animation-delay: 0s; }
  }
</style>
<script type="application/json" id="mar-program">%s</script>
<script src="/_mar/runtime.js"></script>
<script>
window.addEventListener('DOMContentLoaded', function () {
  marBootstrap();
});
</script>
</body>
</html>`

// ServeLive runs the dev server backed by a LiveProgram (whose contents
// can change at runtime via hot-reload) and a ReloadHub (broadcasts
// "reload" events to connected browsers via SSE).
//
// The routing topology is pinned at startup based on what the initial
// compile produced. Three modes:
//
//   - frontend-only (HasFrontend, !HasAPI): "/" serves the HTML shell;
//     "/_mar/*" serves the JS runtime, program JSON, and SSE channel.
//   - backend-only  (!HasFrontend, HasAPI): user routes mount directly
//     at "/" — no "/api" prefix, no "/_mar/*" handlers (only the SSE
//     channel for the dev banner). Use case: pure JSON API server.
//   - fullstack     (HasFrontend, HasAPI):  "/" serves the HTML shell,
//     "/api/*" dispatches to backend routes, "/_mar/*" for assets.
//
// LiveProgram is updated on every reload but the mux registration here
// is fixed — so ServeLive must be called after the initial compile so
// HasFrontend/HasAPI reflect the program shape.
func ServeLive(port int, lp *LiveProgram, hub *ReloadHub) error {
	mux := http.NewServeMux()

	hasFrontend := lp.HasFrontend()
	hasAPI := lp.HasAPI()

	// SSE channel for hot-reload + compile-error broadcasting. In
	// production (mar-runtime) the caller passes a nil hub — no
	// /_mar/reload route is registered and the JS runtime's dev
	// channel never connects (and is skipped client-side by checking
	// `program.devMode`).
	if hub != nil {
		mux.HandleFunc("/_mar/reload", hub.ServeReload)
	}

	// Auth: when the user called Auth.config in their program, the
	// runtime registered a non-nil VAuth. Combined with a configured
	// session secret (from mar.json or .mar/dev-secrets.json), we
	// mount the framework HTTP endpoints at /_auth/*.
	//
	// Before mounting the handlers, validate that the SMTP config —
	// if any — actually works. The boot SMTP check connects, runs
	// STARTTLS, authenticates, and disconnects. Failures here mean
	// the deploy would silently swallow every sign-in attempt
	// (auth.Send would error per request); failing fast at boot
	// surfaces the misconfiguration immediately.
	//
	// Skipped automatically when SMTP isn't configured (Host empty,
	// dev's stdout sink path) and via MAR_SKIP_SMTP_CHECK=1 for
	// edge cases like demos with auth disabled or restore-from-
	// backup with partial infra.
	if runtime.CurrentAuth() != nil && AuthSecret() != "" {
		if err := maybeVerifySMTP(); err != nil {
			return err
		}
		mountAuthHandlers(mux)
	}

	// Admin panel — mounted unconditionally when a session secret is
	// available. No "registered Auth.config" requirement: the admin
	// panel doesn't share user-auth's signup hook or User entity, so
	// it can run on a project that has no Auth.config at all (single
	// dev tools, internal apps).
	//
	// Without a session secret (no auth.sessionSecret in mar.json
	// AND no .mar/dev-secrets.json), admin endpoints would have
	// nothing to sign cookies with — skip mounting so the panel
	// appears unreachable rather than misbehaving.
	if AuthSecret() != "" {
		mountAdminHandlers(mux)
	}

	if hasFrontend {
		mux.HandleFunc("/_mar/runtime.js", withCompression(func(w http.ResponseWriter, r *http.Request) {
			// no-store on dev runtime: hot-reload changes the embedded
			// runtime.js on every `mar dev` restart, but Safari/Chrome
			// happily serve a cached copy unless we tell them not to.
			// `no-store` is stricter than `no-cache` — guarantees a
			// network fetch, not a 304 Not Modified.
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			_, _ = io.WriteString(w, runtimeJS)
		}))
		mux.HandleFunc("/_mar/program.json", withCompression(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			serveProgramJSON(lp)(w, r)
		}))
	}

	switch {
	case hasFrontend && hasAPI:
		// Fullstack: HTML at /, API under /api/*, RPC services under /services/*.
		// Two prefixes share the same routes slice — `/api/` strips its
		// prefix before matching (so user routes have paths like "/notes"),
		// `/services/` matches the full path against routes whose path is
		// already absolute (e.g. "/services/Backend.timelinePosts").
		mux.HandleFunc("/api/", withCompression(func(w http.ResponseWriter, r *http.Request) {
			stripped := strings.TrimPrefix(r.URL.Path, "/api")
			if stripped == "" {
				stripped = "/"
			}
			dispatchBackend(lp.Routes(), stripped, w, r)
		}))
		mux.HandleFunc("/services/", withCompression(func(w http.ResponseWriter, r *http.Request) {
			dispatchBackend(lp.Routes(), r.URL.Path, w, r)
		}))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			renderShell(w, lp)
		})
	case hasFrontend:
		// Frontend-only: HTML at /.
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			renderShell(w, lp)
		})
	case hasAPI:
		// Backend-only: user routes mount directly at /. The /_mar/reload
		// path is registered above — ServeMux longest-prefix match keeps
		// it from being shadowed by the catch-all here.
		mux.HandleFunc("/", withCompression(func(w http.ResponseWriter, r *http.Request) {
			dispatchBackend(lp.Routes(), r.URL.Path, w, r)
		}))
	}

	addr := fmt.Sprintf(":%d", port)
	// Advertise on the LAN via mDNS whenever the server has an HTTP
	// API to talk to (frontend or fullstack). Native iOS clients
	// browse for `_mar._tcp` and auto-connect — no manual baseURL
	// configuration on the same network. The instance name is not
	// surfaced in the banner: `.local` resolution is unreliable across
	// OSes, so we let it stay an iOS-discovery-only mechanism.
	if hasAPI || hasFrontend {
		_, stop := publishBonjour(lp.AppName(), port)
		defer stop()
	}
	printBanner(addr, hub, lp.AppName())
	noteServerBooted()
	// Wrap mux with the admin's request-log instrumentation.
	// Captures every request (method, path, status, duration, user
	// email) into the in-memory ring buffer powering the admin
	// panel's "recent requests" section. No-op when the buffer is
	// not configured.
	return http.ListenAndServe(addr, adminInstrument(mux))
}

