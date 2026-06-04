# AGENTS.md

Guidance for AI agents and contributors working on the Mar compiler,
runtime, and tooling. (User-facing docs live in `docs/` — see the index
at the bottom.)

Mar is a statically typed functional language (Hindley-Milner, Elm-like)
for fullstack apps. One codebase compiles to four targets: web, iOS,
Android, and a server binary. The Go module is `mar`; the CLI entry is
`cmd/mar`.

## Build, test, run

```sh
make build          # build ./mar (needs xcodegen + cross-compiles runtime stubs)
make test           # go test ./...  (regenerates the iOS xcodeproj first)
go test ./...       # tests only, no stub/template regen — fast inner loop
make check-examples # build, then `mar check` every example project
make vscode         # package the VSCode extension into a .vsix

./mar dev <project>     # hot-reload dev server (web)
./mar check <project>   # parse + type-check, no run
./mar build <project>   # produce dist/ (frontend) or a server binary (fullstack/backend)
```

For day-to-day Go work, `go build ./...` + `go test ./...` is enough;
`make build` is only needed to produce the full `./mar` with embedded
runtime stubs + iOS xcodeproj (requires `xcodegen`).

## Repository layout

- `cmd/mar` — the CLI (dev, build, check, init, migrate, fly,
  cloudflare-pages, admin, completion, …).
- `cmd/mar-runtime` — the standalone server runtime; cross-compiled into
  per-OS stubs that `make build` embeds into `./mar`.
- `internal/lexer`, `internal/parser`, `internal/typecheck` — front end
  (HM inference lives in `typecheck`).
- `internal/runtime` — the Go tree-walking interpreter + server runtime
  (entities, repo, services, auth, migrations, SQLite).
- `internal/jsserve` — the web server **and** `runtime.js`, the
  browser-side runtime + renderer + injected CSS.
- `internal/iosbundle/template` — the iOS app (Swift/SwiftUI). `Sources/`
  holds the runtime + `MarRenderer.swift` (the SwiftUI renderer) +
  `MarBuiltins.swift` (the UI vocabulary).
- `internal/scaffold` — `mar build` / `mar init` (dist generation,
  project templates).
- `examples/` — sample `.mar` projects, including `examples/mar-website`
  (the marketing site + the UI showcase under `Frontend/Showcase/`).
- `docs/` — reference documentation (see index below).
- `vscode-mar/`, `mar-sublime/` — editor integrations.

## The cross-runtime parity rule (read this before touching UI/stdlib)

A UI primitive or stdlib function exists in **three** runtimes that must
stay in lockstep:

1. `internal/typecheck` — so user code type-checks.
2. `internal/runtime` (Go) — server-side eval.
3. `internal/jsserve/runtime.js` (web) **and**
   `internal/iosbundle/template/Sources/MarBuiltins.swift` (iOS).

A builtin registered in only some of these is a latent bug that compiles
fine but fails (or renders blank) at runtime on the missing platform.
Guard tests catch the common cases:

- `internal/iosbundle/builtins_drift_test.go` — every frontend-reachable
  builtin in `typecheck.BaseEnv` is defined in the iOS Swift bundle.
- `internal/jsserve/builtins_drift_test.go` — same for the web runtime.
- `internal/iosbundle/renderer_parity_test.go` — every view tag a builtin
  emits (`MarView(tag:)` / `container(...)`) has a matching `case` in
  `MarRenderer.swift`, so no primitive renders blank on iOS.

When adding a UI primitive: add the type scheme, the Go runtime builtin,
the JS renderer case + CSS, and the iOS builtin + `MarRenderer` case —
then run the drift/parity tests.

**Parity is about behavior, not just builtins.** The drift/parity tests
guard that every builtin/view-tag exists on every runtime, but they
can't check *content/format* support. The same rule still applies: if a
capability only works on web, either (1) limit the feature to the common
denominator all runtimes support, or (2) implement it on every runtime.
Example: `UI.image` is **raster only** (PNG/JPEG/WebP/GIF) because that's
what web + iOS + Android decode natively; SVG is excluded (iOS/Android
need a third-party decoder). Vector art is a future `icon` primitive over
native symbol sets, not `image`.

## Gotchas an agent will hit

- **`runtime.js` is embedded in the `mar` binary.** Editing it does
  nothing until you rebuild `mar` AND restart `mar dev` (the running
  process serves the bundle it was compiled with). The browser tab picks
  up the new bundle automatically: the dev SSE channel does a full
  `location.reload()` when it *reconnects* (a server restart), versus the
  soft program-only swap it does for in-process `.mar` hot-reloads. A
  soft swap re-runs `program.json` but keeps the old `runtime.js` + its
  injected CSS — which is exactly how a freshly-fixed CSS rule looks like
  it "didn't take" until a manual refresh. Same embed caveat applies to
  editor completions.
- **The iOS Swift template is NOT compiled in CI.** A type error in
  `Sources/*.swift` won't fail `go test`. Verify Swift changes in Xcode /
  the simulator; for a cheap pre-check, `swiftc -typecheck` a small
  extracted snippet against the macOS SDK (SwiftUI mostly resolves).
- **`make build` needs `xcodegen`** (`brew install xcodegen`) to
  regenerate the iOS xcodeproj. `go test ./...` alone does not.
- **Frontend deploys ship a `_headers` file** (`internal/scaffold/build.go`)
  with `Cache-Control: no-cache` so Cloudflare Pages revalidates instead
  of serving a stale bundle after a redeploy.
- **PWA is web-target infra, generated from `mar.json`'s `pwa` block.**
  `internal/pwa` builds the Web App Manifest + icons; `jsserve.SetPWA`
  serves them at `/_mar/manifest.json` + `/_mar/icon-*.png` in dev, and
  `mar build` writes them into `dist/_mar/`. Always on for `App.frontend`
  (every app is installable); all `pwa` fields optional. A `pwa.icon`
  must be a square PNG ≥ 512 — `ValidatePWAIcon` (in `internal/project`)
  fails dev boot + build otherwise. No Mar language surface, no
  drift/parity concern. Icon resize is a stdlib box-downscale (no
  `x/image` dep).
- **`public/` is the static-asset folder.** Files in a project's `public/`
  are served at the site root by `mar dev` (`public/logo.png` → `/logo.png`,
  subfolders preserved) and copied into `dist/` by `mar build`. Dotfiles
  are skipped. The served file is fetched over HTTP like any other asset
  (same mechanism on web and iOS/AsyncImage) — it is NOT inlined into the
  page or bundled into the native app binary. `mar build` **errors** if a
  `public/` path collides with Mar's reserved namespace: the generated
  files (`index.html`, `runtime.js`, `program.json`, `_headers`) or the
  route prefixes `_mar/` `_auth/` `api/` `services/` (keep
  `reservedPublicPath` in `build.go` in sync with the routes in
  `jsserve/server.go`).
- **`mar cloudflare-pages deploy` auto-opens the per-deployment URL**, not
  the production alias (the alias lags a few seconds and would show the
  previous version).

## Code conventions

- Go: `gofmt`, `go vet`, `staticcheck`, and `golangci-lint run` (config in
  `.golangci.yml`) all clean. Match the surrounding file's comment density
  and idiom.
- **CLI output style** (spacing, colors, error/hint blocks) follows
  `docs/cli-style.md` — code in `cmd/mar/color.go`, `diag/source_error.go`,
  etc. references it by section (§1 spacing, §3 color). Keep it the source
  of truth; don't reinvent ANSI codes ad hoc.
- User-facing errors go through the `printError(prefix, err)` /
  `*hintedError` path (`cmd/mar`) with `Error:` / `Hint:` blocks, not raw
  `fmt.Println`.
- `.mar` example/showcase copy: keep it plain and honest; avoid em-dashes
  in prose and comments (use `:` / commas), and don't claim capabilities a
  target doesn't have (e.g. inline `link` is web-only behavior until the
  iOS `AttributedString` path covers it).

## Detailed docs (kept in `docs/`, linked here)

These stay in `docs/` because they are user/feature reference or are
referenced by code; this file links them rather than duplicating them.

- `docs/mar.md` — the language reference.
- `docs/auth.md` — authentication + sessions.
- `docs/authorization-proposal.md` — authorization design (referenced by
  runtime stubs in `internal/runtime`).
- `docs/admin-panel.md` — the built-in admin panel.
- `docs/migrations.md` — schema migrations.
- `docs/managed-effects.md` — the Effect model.
- `docs/deployment-fly.md` — Fly.io deploy flow.
- `docs/ios.md` — building the iOS app.
- `docs/cli-style.md` — CLI output style guide (referenced by code §1/§3).
- `docs/backup-smoke-test.md` — manual production backup smoke test.
