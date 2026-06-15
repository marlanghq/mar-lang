# iOS

`mar build --target ios` produces a disposable Xcode project from
your mar source. Open it in Xcode, sign, and ship to TestFlight or
the App Store. The Swift code is regenerated on every build,
customizing your iOS app means changing your mar code, not the
Swift scaffolding.

## Mental model

The same `mar.json` + `Main.mar` produces three artifacts depending
on the build target:

| Target | Output |
|---|---|
| `linux-amd64` (and other native targets) | self-contained server binary for Fly / VPS deploy |
| `ios` | Xcode project that ships an iOS app talking to the server |
| `ios` (DEBUG build in Xcode) | the same project, but uses Bonjour to find your local `mar dev` server |

The iOS app is **schema-driven**: it doesn't compile mar code
itself. It fetches `program.json` from the backend at runtime and
the embedded interpreter renders the pages declared in your mar
source. So:

- **A change to your mar code** (a new page, a different layout, a
  new field) → ships to users on the next backend deploy. **No
  App Store re-submission required.**
- **A change to the iOS scaffold itself** (new Swift wiring, new
  framework primitive, version bump) → requires `mar build --target
  ios` again + Xcode rebuild + App Store submission.

The second case should be rare; the first case is the everyday
iteration cycle.

## DEBUG vs RELEASE, the dev/prod split

Two compile-time variants of the same Xcode project:

- **DEBUG**: Bonjour discovery active. The app browses the local
  network for `_mar._tcp` services (your `mar dev` advertises one)
  and auto-connects. Settings tab shows discovered servers + a
  manual override field.
- **RELEASE**: Bonjour compiled out entirely. The app talks **only**
  to `mar.json["ios"]["serverUrl"]` (or a UserDefaults override the
  user typed in Settings).

Why the split:

1. **Bonjour on cellular finds nothing**: wasted boot time + code.
2. **A hostile WiFi could spoof `_mar._tcp`** to route the release
   app to an attacker-controlled backend. Compiling the discovery
   code out of release closes this entirely.

The split is enforced by `#if DEBUG` blocks in the Swift template,
not by build-time substitution. One Xcode project, two build
configurations, Xcode handles which one runs based on its scheme.

## `ios.serverUrl` in `mar.json`

Required for any release-build that needs a backend. Format:
HTTPS, or `http://localhost`/`http://127.0.0.1` for local QA.
Plain `http://example.com` is **rejected at compile time**: App
Store ATS would reject it anyway, and refusing early prevents
"why doesn't my app work in TestFlight" debugging cycles.

```json
{
  "name": "notes",
  "ios": {
    "serverUrl": "https://notes-app.fly.dev"
  }
}
```

The URL gets stamped into `Info.plist` as `MarBaseURL`. The app's
`AppViewModel.resolveInitialBaseURL()` reads it at boot.

## Cold-start: the embedded program.json (Approach C)

`mar build --target ios` compiles your mar source into a
`program.json` snapshot and embeds it in the Xcode project at
`Resources/program.json`. The Swift shell loads this synchronously
at cold-start so **the first frame paints immediately**: no
waiting on the network.

After the embedded snapshot renders, the app fetches
`/_mar/program.json` from the configured backend in the background.
If the fetched version differs (e.g. you deployed new pages since
the .ipa was built), the UI re-renders with the fresher version.
If the fetch fails (offline, server down), the embedded UI stays,
no error screen for transient network problems.

The flow:

```
1. Cold start
   Bundle.main → Resources/program.json → decode → render. <100ms.
2. ~Immediately after
   GET /_mar/program.json → decode → re-render with fresher data.
3. Offline / fetch fails
   Embedded UI stays. The user sees something useful instead of
   a spinner or error.
```

State trade-off: navigating before the fetch lands and then having
the fetched program replace state can briefly flicker. For typical
apps the fetched and embedded programs are usually identical (you
only deploy occasional UI changes), so this is rare. v2 territory:
on-disk caching of the last fetched program for true "boot-with-
last-server-state" behavior.

## Build pipeline (what `mar build --target ios` does)

1. Loads `mar.json`, validates `ios.serverUrl` shape (compile-time
   rule).
2. Evaluates `Main.main` with custom App.fullstack / App.frontend
   captures that extract the page list (without running the
   backend wiring).
3. Walks reachable modules from each page's origin module.
4. Serializes those modules + the entry name into a `program.json`
   blob.
5. Materializes the Swift template under `<distDir>/<AppName>/`,
   substituting:
   - `__MAR_APP_NAME__` → SwiftIdentifier(manifest.name)
   - `__MAR_BUNDLE_ID__` → defaults to `com.marlanghq.<lowercased>`
   - `__MAR_DEFAULT_BASE_URL__` → `ios.serverUrl` (or `--base-url`
     override, or empty for localhost-fallback)
6. Writes `Resources/program.json`.
7. Runs `xcodegen generate` to produce the .xcodeproj.

## Generated project layout

```
dist/<AppName>/
├── project.yml              ← XcodeGen spec
├── Info.plist
├── README.generated.md
├── Resources/
│   └── program.json         ← embedded snapshot (Approach C)
└── Sources/
    ├── MarRuntimeIOSApp.swift
    ├── AppViewModel.swift   ← state + bg refresh + #if DEBUG Bonjour
    ├── ContentView.swift
    ├── APIClient.swift
    ├── Discovery.swift      ← compiled out in RELEASE
    ├── MarLoader.swift
    ├── MarRenderer.swift
    └── ... (the schema-driven runtime)
```

Don't hand-edit. Every `mar build --target ios` overwrites the
directory.

## `--base-url` override

Ad-hoc override for the URL stamped into Info.plist for ONE build.
Useful for staging / QA without editing `mar.json`:

```
mar build --target ios --base-url=https://staging.notes-app.fly.dev .
```

Takes precedence over `ios.serverUrl`. Doesn't persist anywhere,
just changes what gets baked into the Xcode project produced by
this invocation.

## What deliberately ISN'T here

- **No code-signing automation.** `mar build` produces the project;
  you sign in Xcode like any other iOS app. Automating signing
  needs Apple Developer credentials we don't want to handle.
- **No App Store submission.** Use Xcode's Organizer or
  `xcrun altool` directly.
- **No native UI primitives in mar.** The view DSL is the same
  cross-platform set; native wrappers (e.g. iOS-specific
  `Picker` styles) would diverge from the web target. Shared
  vocabulary first.
- **No on-disk cache of fetched program.** Today the embedded
  snapshot is the only offline source. v2 territory once the
  pattern usage shows it's worth the storage.

## Testing the iOS build locally without Xcode

Even without an Apple developer setup, you can sanity-check the
build pipeline:

```
mar build --target ios .
ls dist/<AppName>/Resources/program.json    # snapshot exists
xcodegen generate                            # if you have it
xcodebuild -project dist/<AppName>/<AppName>.xcodeproj   # if you have Xcode
```

CI without macOS can verify steps 1–2 (the .xcodeproj generation
needs xcodegen which is portable Go). The actual build needs Xcode.
